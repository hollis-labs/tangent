package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Backup, restore, and the fingerprint that makes "the restore preserved it"
// a measurement rather than a claim.
//
// ADR 0002 §7 fixed three things and they are all load-bearing here:
//
//   - A file copy of tangent.db is not a backup. The database runs in WAL mode,
//     so the main file alone is a silently stale database and the -wal
//     companion is only meaningful alongside it. Backups use `VACUUM INTO`,
//     which writes a complete, checkpointed, defragmented database from a read
//     transaction.
//   - A backup is a point-in-time copy that redaction cannot reach. Nothing
//     here claims otherwise; retention_operations records which backups are
//     known to still hold removed content.
//   - Backups are opt-in. Nothing in this file runs unless an operator names a
//     destination path.
//
// Two modes, because "consistent while the service runs" and "drains
// explicitly" are different guarantees and an operator needs to know which one
// they have.

// BackupMode distinguishes the two guarantees.
type BackupMode string

const (
	// BackupOnline runs against a live database, including one a serving
	// Tangent is writing to.
	//
	// Guarantees: the destination is a complete, self-contained,
	// integrity-checked database holding every transaction committed before the
	// read transaction began. `VACUUM INTO` runs inside a read transaction, so
	// it sees one snapshot and never a torn one, and WAL readers do not block
	// writers, so the running service is not stalled.
	//
	// Does not guarantee: that a write committed while the copy was being
	// written is in it. It is a point-in-time copy, and the point in time is
	// the start of the copy, not the end.
	BackupOnline BackupMode = "online"

	// BackupDrained requires that nothing else holds the database, checkpoints
	// the WAL into the main file first, and verifies the result.
	//
	// Guarantees everything BackupOnline does, plus: no write was in flight,
	// the WAL is empty so the source is left in a state a byte copy could also
	// have captured, and the destination has been opened and integrity-checked
	// before the command returns.
	//
	// Use it before an upgrade that migrates, before a purge, and any time the
	// backup is the thing being relied on rather than a convenience — because
	// it is the only mode that can say "nothing was mid-write".
	BackupDrained BackupMode = "drained"
)

// backupManifestSuffix names the sidecar written next to a backup.
//
// The catalog is a file rather than a table because an online backup has no
// write authority over the database it is copying — that is the single-writer
// contract — and a backup that could only be recorded by stopping the service
// would defeat the point of having an online mode.
const backupManifestSuffix = ".manifest.json"

// BackupManifest is what a backup knows about itself.
type BackupManifest struct {
	ID            string     `json:"id"`
	Mode          BackupMode `json:"mode"`
	TakenAt       time.Time  `json:"taken_at"`
	SchemaVersion int64      `json:"schema_version"`
	SizeBytes     int64      `json:"size_bytes"`
	SHA256        string     `json:"sha256"`
	// IntegrityOK reports that the copy is undamaged: pages, foreign keys, and
	// the immutability guards its own schema defines. It deliberately does not
	// mean "at this binary's schema" — a backup taken before an upgrade is
	// behind by design, and reporting that as an integrity failure is
	// CW-20260905-0014.
	IntegrityOK bool `json:"integrity_ok"`
	// SchemaState is that second, separate question: current, behind, ahead,
	// dirty, or uninitialized.
	SchemaState SchemaState `json:"schema_state"`
	// SchemaAdvice is what to do about the state, empty when it is current.
	SchemaAdvice string `json:"schema_advice,omitempty"`
	// Damage is why IntegrityOK is false, empty when it is true. It is
	// recorded in the manifest so a backup that was written and is not
	// trustworthy says so on disk rather than only on the terminal that took
	// it.
	Damage      []string    `json:"damage,omitempty"`
	Fingerprint Fingerprint `json:"fingerprint"`
	// SourceLabel is the base name of the database that was copied. The full
	// path is deliberately absent: a manifest travels with the backup and there
	// is no reason for it to carry the layout of the machine it came from.
	SourceLabel string `json:"source_label"`
}

// BackupResult is the manifest plus where it landed.
type BackupResult struct {
	BackupManifest
	Path         string `json:"path"`
	ManifestPath string `json:"manifest_path"`
}

// Fingerprint is criterion 2's five preservation properties, each measured
// independently so a restore can be asserted property by property rather than
// by one opaque checksum that says only "something differs".
//
// Each property is a row count plus a SHA-256 over the sorted, canonicalized
// tuples that define it. Content columns are excluded on purpose: the
// fingerprint has to keep matching across a redaction of payload that
// preserves identity, which is precisely the invariant ADR 0002 §2 promises.
type Fingerprint struct {
	SchemaVersion int64 `json:"schema_version"`

	// DefinitionDigests: every interaction's pinned definition binding —
	// publisher, kind, version, revision, digest, schema identity and digest.
	// This is what makes a restored record still say which definition produced
	// it.
	DefinitionDigests Property `json:"definition_digests"`

	// InteractionIdentities: id, surface, caller scope, idempotency key,
	// sequence, lifecycle state, revision. The idempotency key is in here
	// because losing it is how a caller's retry becomes a duplicate.
	InteractionIdentities Property `json:"interaction_identities"`

	// Resolutions: id, interaction, participant binding, response kind,
	// integrity digest, timestamps. Not the payload — a redacted resolution
	// must still fingerprint as the same resolution.
	Resolutions Property `json:"resolutions"`

	// AuditHistory: every surface, interaction, and delivery event, by id,
	// type, actor, authority, and revision transition.
	AuditHistory Property `json:"audit_history"`

	// DeliveryObligations: every resolution delivery and terminal
	// notification, by id, lifecycle state, revision, and idempotency key.
	DeliveryObligations Property `json:"delivery_obligations"`

	// PendingDeliveries and PendingTerminalNotifications count the obligations
	// that are not yet acknowledged or terminally failed — the ones a restore
	// has to hand to restart recovery rather than drop.
	PendingDeliveries            int64 `json:"pending_deliveries"`
	PendingTerminalNotifications int64 `json:"pending_terminal_notifications"`
}

// Property is one measured preservation property.
type Property struct {
	// Applicable is false when the source schema predates the tables this
	// property is measured over. It is not a failure and not a zero
	// measurement: it is the statement that this schema has nothing to measure
	// here. A reader that cannot tell those apart is how CW-20260905-0014
	// wrote a fingerprint of zeros into a manifest and then refused the
	// restore that compared against it.
	Applicable bool `json:"applicable"`

	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}

// Equal compares two properties. Applicability is part of the comparison: a
// property that was measurable before a copy and is not measurable after it
// has changed, whatever the digests say.
func (p Property) Equal(other Property) bool {
	return p.Applicable == other.Applicable && p.Rows == other.Rows && p.SHA256 == other.SHA256
}

// fingerprintSection is one preservation property: the tables it is measured
// over, and the query that canonicalizes them.
//
// The tables are declared rather than inferred from the query, because they
// are what makes the fingerprint schema-aware. Fingerprinting a database older
// than this binary is the *normal* case — a pre-upgrade backup is taken
// precisely because a migration is about to run — and a section whose tables
// that schema predates is inapplicable, not broken.
type fingerprintSection struct {
	name   string
	tables []string
	query  string
	// target is where the measurement lands, so the sections stay an ordered
	// list rather than a map iterated in random order.
	target func(*Fingerprint) *Property
}

// fingerprintSections are the five properties, each as a query returning one
// text column per row. Ordering is applied after the fact in Go over the
// collected strings, so a difference in SQLite's collation cannot change the
// digest.
var fingerprintSections = []fingerprintSection{{
	name:   "definition_digests",
	tables: []string{"definition_bindings"},
	target: func(f *Fingerprint) *Property { return &f.DefinitionDigests },
	query: `
SELECT interaction_id || '|' || publisher || '|' || kind || '|' || version || '|' || revision ||
       '|' || COALESCE(digest, '') || '|' || source || '|' || COALESCE(schema_identity, '') ||
       '|' || COALESCE(schema_digest, '') || '|' || assurance
FROM definition_bindings`,
}, {
	name:   "interaction_identities",
	tables: []string{"interactions"},
	target: func(f *Fingerprint) *Property { return &f.InteractionIdentities },
	query: `
SELECT id || '|' || surface_id || '|' || caller_scope || '|' || idempotency_key || '|' ||
       surface_sequence || '|' || lifecycle_state || '|' || revision || '|' ||
       COALESCE(terminal_cause, '') || '|' || COALESCE(participant_ref, '')
FROM interactions`,
}, {
	name:   "resolutions",
	tables: []string{"resolutions"},
	target: func(f *Fingerprint) *Property { return &f.Resolutions },
	query: `
SELECT id || '|' || interaction_id || '|' || participant_ref || '|' || participant_authority ||
       '|' || participant_assurance || '|' || response_kind || '|' || integrity_digest ||
       '|' || expected_interaction_revision || '|' || presented_projection_revision
FROM resolutions`,
}, {
	name: "audit_history",
	// Three journals, measured as one property. A schema that has any two of
	// them and not the third cannot be measured here at all — the property is
	// the union, and a union missing a term is a different measurement wearing
	// the same name.
	tables: []string{"surface_events", "interaction_events", "delivery_events"},
	target: func(f *Fingerprint) *Property { return &f.AuditHistory },
	query: `
SELECT 'surface|' || event_id || '|' || surface_id || '|' || event_type || '|' ||
       COALESCE(actor_ref, '') || '|' || COALESCE(authority, '') || '|' ||
       COALESCE(from_revision, -1) || '|' || to_revision
FROM surface_events
UNION ALL
SELECT 'interaction|' || event_id || '|' || interaction_id || '|' || event_type || '|' ||
       COALESCE(actor_ref, '') || '|' || COALESCE(authority, '') || '|' ||
       COALESCE(from_revision, -1) || '|' || to_revision
FROM interaction_events
UNION ALL
SELECT 'delivery|' || event_id || '|' || COALESCE(resolution_delivery_id, '') || '|' ||
       COALESCE(terminal_notification_id, '') || '|' || event_type || '|' ||
       from_revision || '|' || to_revision || '|' || COALESCE(attempt_number, -1)
FROM delivery_events`,
}, {
	name:   "delivery_obligations",
	tables: []string{"resolution_deliveries", "terminal_notifications"},
	target: func(f *Fingerprint) *Property { return &f.DeliveryObligations },
	query: `
SELECT 'resolution|' || id || '|' || resolution_id || '|' || idempotency_key || '|' ||
       lifecycle_state || '|' || revision
FROM resolution_deliveries
UNION ALL
SELECT 'terminal|' || id || '|' || interaction_id || '|' || idempotency_key || '|' ||
       lifecycle_state || '|' || revision
FROM terminal_notifications`,
}}

// Skipped names the properties this database's schema is too old to carry, in
// section order. An empty result means the fingerprint measured everything.
func (f Fingerprint) Skipped() []string {
	skipped := []string{}
	for _, section := range fingerprintSections {
		if !section.target(&f).Applicable {
			skipped = append(skipped, section.name)
		}
	}
	return skipped
}

// Complete reports whether every property was measurable at this schema.
func (f Fingerprint) Complete() bool {
	return len(f.Skipped()) == 0
}

// TakeFingerprint measures the preservation properties this database's schema
// can carry, and says which ones those were.
//
// It probes the table catalog first rather than querying unconditionally. A
// table that is absent because the schema predates it is an expected answer,
// and an error is the wrong way to give it: the caller asked what this
// database holds, not whether it looks like the current one.
func TakeFingerprint(ctx context.Context, database *sql.DB) (Fingerprint, error) {
	status, err := InspectMigrations(ctx, database)
	if err != nil {
		return Fingerprint{}, err
	}
	present, err := existingTables(ctx, database)
	if err != nil {
		return Fingerprint{}, err
	}
	fingerprint := Fingerprint{SchemaVersion: status.Applied}

	for _, section := range fingerprintSections {
		if !hasAllTables(present, section.tables) {
			continue
		}
		property, propertyErr := measureProperty(ctx, database, section.query)
		if propertyErr != nil {
			return Fingerprint{}, fmt.Errorf("fingerprint %s: %w", section.name, propertyErr)
		}
		property.Applicable = true
		*section.target(&fingerprint) = property
	}

	// The pending counts share the delivery_obligations tables, so they are
	// measurable exactly when that property is. A zero here on a schema that
	// has neither table reads correctly only alongside
	// `delivery_obligations.applicable: false`.
	if fingerprint.DeliveryObligations.Applicable {
		if err := database.QueryRowContext(ctx, `
SELECT COUNT(*) FROM resolution_deliveries
WHERE lifecycle_state NOT IN ('acknowledged', 'terminal_failure')`).
			Scan(&fingerprint.PendingDeliveries); err != nil {
			return Fingerprint{}, fmt.Errorf("count pending deliveries: %w", err)
		}
		if err := database.QueryRowContext(ctx, `
SELECT COUNT(*) FROM terminal_notifications
WHERE lifecycle_state NOT IN ('acknowledged', 'terminal_failure')`).
			Scan(&fingerprint.PendingTerminalNotifications); err != nil {
			return Fingerprint{}, fmt.Errorf("count pending terminal notifications: %w", err)
		}
	}
	return fingerprint, nil
}

// existingTables reads the table catalog. It is one query rather than one per
// table so that a fingerprint of a database with a dozen sections still costs
// a single round trip.
func existingTables(ctx context.Context, database *sql.DB) (map[string]struct{}, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table';`)
	if err != nil {
		return nil, fmt.Errorf("read table catalog: %w", err)
	}
	defer func() { _ = rows.Close() }()

	present := map[string]struct{}{}
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			return nil, fmt.Errorf("scan table catalog: %w", scanErr)
		}
		present[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate table catalog: %w", err)
	}
	return present, nil
}

func hasAllTables(present map[string]struct{}, wanted []string) bool {
	for _, table := range wanted {
		if _, ok := present[table]; !ok {
			return false
		}
	}
	return true
}

func measureProperty(ctx context.Context, database *sql.DB, query string) (Property, error) {
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return Property{}, err
	}
	defer func() { _ = rows.Close() }()

	tuples := []string{}
	for rows.Next() {
		var tuple sql.NullString
		if scanErr := rows.Scan(&tuple); scanErr != nil {
			return Property{}, scanErr
		}
		tuples = append(tuples, tuple.String)
	}
	if err := rows.Err(); err != nil {
		return Property{}, err
	}
	sort.Strings(tuples)
	digest := sha256.Sum256([]byte(strings.Join(tuples, "\n")))
	return Property{Rows: int64(len(tuples)), SHA256: hex.EncodeToString(digest[:])}, nil
}

// PropertyDifference names one preservation property that did not survive.
type PropertyDifference struct {
	Property string   `json:"property"`
	Before   Property `json:"before"`
	After    Property `json:"after"`
	// Reason distinguishes content that changed from a schema that did. They
	// look identical in the digests and mean entirely different things.
	Reason string `json:"reason"`
}

// Compare reports which of the five properties differ. An empty result is the
// proof criterion 2 asks for, property by property.
//
// Two properties that were both inapplicable compare equal: a schema too old
// to carry a section is not a section that was lost, and a copy of that
// database has preserved exactly as much of it as existed.
func (f Fingerprint) Compare(other Fingerprint) []PropertyDifference {
	differences := []PropertyDifference{}
	for _, section := range fingerprintSections {
		before, after := *section.target(&f), *section.target(&other)
		if before.Equal(after) {
			continue
		}
		reason := "the measured rows differ"
		if before.Applicable != after.Applicable {
			reason = "the schemas differ: this property is measurable on one side and not the other"
		}
		differences = append(differences, PropertyDifference{
			Property: section.name, Before: before, After: after, Reason: reason,
		})
	}
	return differences
}

// Backup writes a consistent copy of the database to dest.
//
// The caller owns the mode decision and, for BackupDrained, owns having
// acquired the single-writer lock first: this function cannot tell whether the
// lock it did not take is held by the caller or by someone else.
func Backup(
	ctx context.Context,
	database *sql.DB,
	sourcePath string,
	dest string,
	mode BackupMode,
) (BackupResult, error) {
	if database == nil {
		return BackupResult{}, errors.New("backup: nil database")
	}
	if strings.TrimSpace(dest) == "" {
		return BackupResult{}, errors.New("backup: no destination path")
	}
	absolute, err := filepath.Abs(dest)
	if err != nil {
		return BackupResult{}, fmt.Errorf("resolve backup destination: %w", err)
	}
	if _, statErr := os.Stat(absolute); statErr == nil {
		// VACUUM INTO refuses an existing file too; refusing here makes the
		// message an operator's rather than SQLite's.
		return BackupResult{}, fmt.Errorf("backup: %q already exists; name a path that does not", absolute)
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(absolute), 0o750); mkdirErr != nil {
		return BackupResult{}, fmt.Errorf("create backup directory: %w", mkdirErr)
	}

	if mode == BackupDrained {
		// Fold the WAL into the main file first. TRUNCATE blocks on readers,
		// which is exactly the drain: if it cannot get exclusive access, the
		// caller was wrong that nothing else is using the database.
		if drainErr := checkpointTruncate(ctx, database); drainErr != nil {
			return BackupResult{}, fmt.Errorf("drain: %w", drainErr)
		}
	}

	takenAt := time.Now().UTC()
	if _, vacuumErr := database.ExecContext(ctx, `VACUUM INTO ?;`, absolute); vacuumErr != nil {
		return BackupResult{}, fmt.Errorf("vacuum into %q: %w", absolute, vacuumErr)
	}

	manifest := BackupManifest{
		ID:          "bkp_" + uuid.NewString(),
		Mode:        mode,
		TakenAt:     takenAt,
		SourceLabel: filepath.Base(sourcePath),
	}
	if manifest.SourceLabel == "." || manifest.SourceLabel == string(filepath.Separator) {
		manifest.SourceLabel = defaultDatabaseName
	}

	// Verify the copy by opening it, not by trusting that VACUUM INTO worked.
	// A backup nobody has read is a backup nobody knows they have.
	verified, err := VerifyDatabaseFile(ctx, absolute)
	if err != nil {
		return BackupResult{}, err
	}
	manifest.IntegrityOK = verified.Intact()
	manifest.SchemaVersion = verified.Migrations.Applied
	manifest.SchemaState = verified.SchemaState
	manifest.SchemaAdvice = verified.SchemaAdvice
	manifest.Damage = verified.Damage()
	manifest.Fingerprint = verified.Fingerprint

	// The digest is taken *after* verification, and the order is load-bearing.
	// `VACUUM INTO` writes a rollback-journal database; opening it to verify it
	// applies `PRAGMA journal_mode = WAL`, which rewrites the file header. A
	// digest taken before that describes a file that no longer exists, so an
	// operator checking their backup against the manifest would find every
	// backup corrupt. Found re-verifying §2 of docs/database-operations.md
	// against the real artifact in CW-20260905-0014.
	info, err := os.Stat(absolute)
	if err != nil {
		return BackupResult{}, fmt.Errorf("stat backup: %w", err)
	}
	manifest.SizeBytes = info.Size()
	manifest.SHA256, err = fileDigest(absolute)
	if err != nil {
		return BackupResult{}, err
	}

	manifestPath := absolute + backupManifestSuffix
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BackupResult{}, fmt.Errorf("encode backup manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, append(encoded, '\n'), 0o600); err != nil {
		return BackupResult{}, fmt.Errorf("write backup manifest: %w", err)
	}

	return BackupResult{BackupManifest: manifest, Path: absolute, ManifestPath: manifestPath}, nil
}

// SurveyBackups reads the manifests in a directory and returns the identifiers
// of backups taken before `before`.
//
// It is how a retention operation answers "which backups still contain what I
// just removed" without pretending to know about backups nobody told it about.
// A directory with no manifests reports `surveyed` with an empty list, which is
// a different and weaker statement than `not-surveyed`.
func SurveyBackups(directory string, before time.Time) (BackupSurvey, error) {
	survey := BackupSurvey{State: backupSurveyDone, IDs: []string{}}
	if strings.TrimSpace(directory) == "" {
		return BackupSurvey{State: backupSurveyNone, IDs: []string{}}, nil
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return BackupSurvey{State: backupSurveyFailed, IDs: []string{}},
			fmt.Errorf("survey backups in %q: %w", directory, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), backupManifestSuffix) {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(directory, entry.Name())) //nolint:gosec // operator-named path
		if readErr != nil {
			continue
		}
		var manifest BackupManifest
		if json.Unmarshal(raw, &manifest) != nil || manifest.ID == "" {
			continue
		}
		if manifest.TakenAt.Before(before) {
			survey.IDs = append(survey.IDs, manifest.ID)
		}
	}
	sort.Strings(survey.IDs)
	return survey, nil
}

// VerificationReport is what opening a database file tells you about it.
//
// It answers two questions separately, and keeping them separate is the point
// of CW-20260905-0014. `Intact` is "is this database damaged" — pages, foreign
// keys, immutability guards, a half-applied migration. `SchemaState` is "is
// this database the one this binary was built for". A pre-upgrade backup is
// intact and behind, and collapsing those into one boolean is what made the
// documented upgrade procedure fail at the first step.
type VerificationReport struct {
	Path        string          `json:"-"`
	Migrations  MigrationStatus `json:"migrations"`
	SchemaState SchemaState     `json:"schema_state"`
	// SchemaAdvice is the action the state calls for, in the operator's terms.
	// Empty when the schema is current.
	SchemaAdvice         string     `json:"schema_advice,omitempty"`
	IntegrityProblems    []string   `json:"integrity_problems"`
	ForeignKeyViolations int64      `json:"foreign_key_violations"`
	GuardDrift           GuardDrift `json:"guard_drift"`
	// GuardsEvaluated reports whether the guard inventory could be compared at
	// all. It is false only for a schema this binary cannot build a reference
	// for — one ahead of it — where "no drift" would be an unearned claim
	// rather than a measurement.
	GuardsEvaluated bool `json:"guards_evaluated"`
	// GuardReferenceVersion is the schema version the inventory was compared
	// against, which is the database's own version rather than this binary's.
	// A guard a schema never had is not a guard that went away.
	GuardReferenceVersion int64       `json:"guard_reference_version"`
	Fingerprint           Fingerprint `json:"fingerprint"`
}

// Intact reports whether the file is undamaged: no page-level corruption, no
// foreign key violations, no half-applied migration, and every immutability
// guard its own schema defines still present.
//
// It says nothing about whether the schema is current. Being older than this
// binary is not damage, and a backup is taken from an older database by
// design. Damage is the enumeration; this is the question asked of it, so the
// two can never drift apart.
func (r VerificationReport) Intact() bool {
	return len(r.Damage()) == 0
}

// Healthy reports whether the file is undamaged *and* at a schema this binary
// can work with — current, or behind and migratable forward.
func (r VerificationReport) Healthy() bool {
	return r.Intact() && (r.SchemaState == SchemaCurrent || r.SchemaState == SchemaBehind)
}

// Damage names what is wrong with this database, in the operator's terms, and
// is empty when nothing is.
//
// Every entry here calls for a restore. None of them is fixed by a migration,
// which is exactly what separates them from SchemaAdvice: an operator reading
// a refusal has to be able to tell "this file is broken" from "this file is
// older than the binary", and before CW-20260905-0014 both arrived as the same
// sentence.
func (r VerificationReport) Damage() []string {
	damage := []string{}
	if len(r.IntegrityProblems) > 0 {
		damage = append(damage,
			"page-level corruption: "+strings.Join(r.IntegrityProblems, "; "))
	}
	if r.ForeignKeyViolations > 0 {
		damage = append(damage,
			fmt.Sprintf("%d foreign key violations", r.ForeignKeyViolations))
	}
	if r.Migrations.Dirty {
		damage = append(damage, fmt.Sprintf(
			"the schema is dirty at version %d: a migration failed part-way", r.Migrations.Applied))
	}
	switch {
	case !r.GuardsEvaluated && r.SchemaState != SchemaAhead:
		// A schema *ahead* of this binary has guards this binary has no
		// reference for, and that is a statement about the binary rather than
		// about the file. Anything else that stops the inventory being read is
		// the file, and an unread inventory must never pass for a clean one.
		damage = append(damage,
			"the immutability guards could not be read, so this file cannot be shown to be sound")
	case len(r.GuardDrift.Missing) > 0:
		damage = append(damage, fmt.Sprintf(
			"immutability guards %v that schema %d defines are missing",
			r.GuardDrift.Missing, r.GuardReferenceVersion))
	}
	if len(r.GuardDrift.Unexpected) > 0 {
		damage = append(damage, fmt.Sprintf(
			"triggers %v are present that schema %d does not define",
			r.GuardDrift.Unexpected, r.GuardReferenceVersion))
	}
	return damage
}

// VerifyDatabaseFile opens a database file and reports on it without changing
// it. It is used on a fresh backup, on a restore source before it is trusted,
// and by the operator check command.
func VerifyDatabaseFile(ctx context.Context, path string) (VerificationReport, error) {
	report := VerificationReport{Path: path}
	target, err := Open(path)
	if err != nil {
		return report, fmt.Errorf("open %q for verification: %w", path, err)
	}
	defer func() { _ = target.Close() }()
	return VerifyDatabase(ctx, target)
}

// VerifyDatabase runs the same checks against an open handle.
//
// Once integrity_check has found page-level corruption, the later steps are
// allowed to fail without failing the verification. A corrupt page makes the
// foreign key check, the schema read, and the fingerprint unrunnable, and
// those are consequences of the damage rather than separate faults — returning
// the driver's "database disk image is malformed" as an error would replace a
// report that says "this database is damaged, restore it" with one that says
// less. Against a sound database the same failures are still errors.
func VerifyDatabase(ctx context.Context, database *sql.DB) (VerificationReport, error) {
	report := VerificationReport{IntegrityProblems: []string{}}

	problems, err := integrityProblems(ctx, database)
	if err != nil {
		// An integrity check that cannot finish is itself an integrity
		// finding: the pages it was reading are unreadable. Recording it as
		// one is what lets a refusal say "this backup fails its integrity
		// check" rather than handing the operator a driver message.
		problems = append(problems, "the integrity check could not finish: "+err.Error())
	}
	report.IntegrityProblems = problems
	damaged := len(problems) > 0

	// note records a step that could not run against an already-damaged file,
	// and returns the error to propagate against a sound one.
	note := func(step string, stepErr error) error {
		if !damaged {
			return fmt.Errorf("%s: %w", step, stepErr)
		}
		report.IntegrityProblems = append(report.IntegrityProblems,
			step+" could not run: "+stepErr.Error())
		return nil
	}

	if fkErr := database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_foreign_key_check;`).Scan(&report.ForeignKeyViolations); fkErr != nil {
		if noteErr := note("foreign key check", fkErr); noteErr != nil {
			return report, noteErr
		}
	}

	status, statusErr := InspectMigrations(ctx, database)
	if statusErr != nil {
		if noteErr := note("schema version read", statusErr); noteErr != nil {
			return report, noteErr
		}
	}
	report.Migrations = status
	report.SchemaState = status.State()
	report.SchemaAdvice = status.Advice()

	installed, guardErr := ReadGuards(ctx, database)
	if guardErr != nil {
		if noteErr := note("trigger catalog read", guardErr); noteErr != nil {
			return report, noteErr
		}
	}
	// The reference is built at the database's own schema version, not this
	// binary's. Against a schema-10 database the inventory at migration 12
	// would report the guards migrations 0011 and 0012 create as missing, and
	// "missing" is the word for a guard that was removed — not for one that
	// was never created.
	//
	// Neither a corrupt file nor a schema ahead of this binary can be compared
	// at all, and GuardsEvaluated says so rather than reporting no drift.
	if !damaged && guardErr == nil && status.State() != SchemaAhead {
		reference, referenceErr := ReferenceGuardsAt(ctx, status.Applied)
		if referenceErr != nil {
			return report, referenceErr
		}
		report.GuardDrift = CompareGuards(installed, reference)
		report.GuardsEvaluated = true
		report.GuardReferenceVersion = status.Applied
	}

	// The fingerprint is taken at every readable schema. It measures the
	// properties the schema can carry and records which those were, so a
	// backup of an older database gets a real fingerprint rather than the
	// zeros CW-20260905-0014 wrote into its manifest.
	fingerprint, fingerprintErr := TakeFingerprint(ctx, database)
	if fingerprintErr != nil {
		if noteErr := note("fingerprint", fingerprintErr); noteErr != nil {
			return report, noteErr
		}
	} else {
		report.Fingerprint = fingerprint
	}
	return report, nil
}

// RestoreResult is what a restore did and what it found.
type RestoreResult struct {
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
	// SupersededPath is where the database that was replaced now lives. It is
	// never deleted: a restore that destroys the thing it is recovering from is
	// how a bad backup becomes total data loss.
	SupersededPath string `json:"superseded_path,omitempty"`

	// SchemaVersion is what the restored database is now at, and
	// BinarySchemaVersion is what this binary expects. They differ whenever a
	// pre-upgrade backup is restored, which is the ordinary rollback: you took
	// the backup before migrating, so restoring it puts you back before the
	// migration.
	SchemaVersion       int64       `json:"schema_version"`
	BinarySchemaVersion int64       `json:"binary_schema_version"`
	SchemaState         SchemaState `json:"schema_state"`
	// NextStep is what the operator has to do before serving, empty when the
	// restored schema is already this binary's.
	NextStep string `json:"next_step,omitempty"`

	Source Fingerprint `json:"source_fingerprint"`
	Target Fingerprint `json:"restored_fingerprint"`
	// Differences is empty on a correct restore. It is the per-property proof,
	// not a summary of one.
	Differences []PropertyDifference `json:"differences"`

	// PendingDeliveries and PendingTerminalNotifications are the delivery
	// obligations the restored database carries. They are reported rather than
	// acted on: reconciliation is `RecoverAfterRestart`, which runs on the next
	// boot, and doing it here would mean two implementations of recovery.
	PendingDeliveries            int64 `json:"pending_deliveries"`
	PendingTerminalNotifications int64 `json:"pending_terminal_notifications"`
	// LeasesHeld counts obligations still carrying a worker lease from the
	// process that was running when the backup was taken. Restart recovery
	// releases them; the count is here so an operator is not surprised by it.
	LeasesHeld int64 `json:"leases_held"`
}

// Restore replaces the database at targetPath with the backup at sourcePath.
//
// The caller must hold the single-writer lock and must have established that
// nothing is serving. This function verifies the source before it touches the
// target, moves the existing database aside rather than deleting it, and puts
// it back if anything after that point fails.
//
// A backup at an *older* schema is accepted, and that is the decision
// CW-20260905-0014 forced into the open. Restoring a pre-upgrade backup and
// re-migrating is the normal rollback: refusing it would mean the recovery
// path the upgrade procedure tells an operator to prepare does not work at the
// moment they need it. The result says which schema they landed on and what to
// run next. A backup at a *newer* schema is still refused — this binary's
// queries were written for a different shape — and so is a dirty one, because
// a half-applied migration is damage rather than age.
func Restore(ctx context.Context, targetPath, sourcePath string) (RestoreResult, error) {
	resolvedTarget, err := resolvePath(targetPath)
	if err != nil {
		return RestoreResult{}, err
	}
	absoluteSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("resolve restore source: %w", err)
	}
	if absoluteSource == resolvedTarget {
		return RestoreResult{}, errors.New("restore: source and target are the same file")
	}

	// Verify first. A restore that overwrites a working database with a corrupt
	// backup is worse than one that refuses.
	verification, err := VerifyDatabaseFile(ctx, absoluteSource)
	if err != nil {
		return RestoreResult{}, err
	}
	if len(verification.IntegrityProblems) > 0 {
		return RestoreResult{}, fmt.Errorf(
			"restore: backup %q fails integrity check: %s",
			absoluteSource, strings.Join(verification.IntegrityProblems, "; "))
	}
	if verification.ForeignKeyViolations > 0 {
		return RestoreResult{}, fmt.Errorf(
			"restore: backup %q has %d foreign key violations",
			absoluteSource, verification.ForeignKeyViolations)
	}
	if verification.Migrations.Dirty {
		return RestoreResult{}, fmt.Errorf(
			"restore: backup %q has a dirty schema at version %d; it was taken during a failed migration",
			absoluteSource, verification.Migrations.Applied)
	}
	if verification.SchemaState == SchemaAhead {
		return RestoreResult{}, fmt.Errorf(
			"restore: backup %q is at schema %d and this binary knows %d; restore with the newer binary",
			absoluteSource, verification.Migrations.Applied, verification.Migrations.Expected)
	}
	if verification.SchemaState == SchemaUninitialized {
		return RestoreResult{}, fmt.Errorf(
			"restore: backup %q has no schema at all; it is not a Tangent database", absoluteSource)
	}
	// The guard inventory is compared against the backup's own schema version,
	// so a missing guard here is a guard that was dropped rather than one the
	// schema predates.
	if !verification.GuardDrift.Intact() {
		return RestoreResult{}, fmt.Errorf(
			"restore: backup %q is missing immutability guards %v that schema %d defines; "+
				"repair it before restoring",
			absoluteSource, verification.GuardDrift.Missing, verification.Migrations.Applied)
	}

	result := RestoreResult{
		SourcePath: absoluteSource, TargetPath: resolvedTarget,
		SchemaVersion:       verification.Migrations.Applied,
		BinarySchemaVersion: verification.Migrations.Expected,
		SchemaState:         verification.SchemaState,
		Source:              verification.Fingerprint, Differences: []PropertyDifference{},
	}
	if verification.SchemaState == SchemaBehind {
		result.NextStep = fmt.Sprintf(
			"the restored database is at schema %d and this binary expects %d. Run `tangent "+
				"--migrate-only` before serving, or start the server, which migrates on boot.",
			verification.Migrations.Applied, verification.Migrations.Expected)
	}

	stamp := time.Now().UTC().Format("20060102T150405Z")
	superseded := resolvedTarget + ".superseded-" + stamp
	moved, err := movePreviousDatabase(resolvedTarget, superseded)
	if err != nil {
		return RestoreResult{}, err
	}
	if moved {
		result.SupersededPath = superseded
	}

	restore := func() error {
		if copyErr := copyFile(absoluteSource, resolvedTarget); copyErr != nil {
			return copyErr
		}
		restored, openErr := Open(resolvedTarget)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = restored.Close() }()

		fingerprint, fingerprintErr := TakeFingerprint(ctx, restored)
		if fingerprintErr != nil {
			return fingerprintErr
		}
		result.Target = fingerprint
		result.Differences = verification.Fingerprint.Compare(fingerprint)
		if len(result.Differences) > 0 {
			names := make([]string, 0, len(result.Differences))
			for _, difference := range result.Differences {
				names = append(names, difference.Property)
			}
			return fmt.Errorf(
				"restore: %d preservation propert(ies) did not survive the copy (%s); the copy "+
					"differs from the backup it was made from, which is damage rather than age",
				len(result.Differences), strings.Join(names, ", "))
		}
		result.PendingDeliveries = fingerprint.PendingDeliveries
		result.PendingTerminalNotifications = fingerprint.PendingTerminalNotifications

		if !fingerprint.DeliveryObligations.Applicable {
			// A schema without the delivery tables has no obligations to hand
			// to restart recovery, and counting them would mean querying
			// tables it does not have.
			return nil
		}
		return restored.QueryRowContext(ctx, `
SELECT (SELECT COUNT(*) FROM resolution_deliveries WHERE lease_owner IS NOT NULL) +
       (SELECT COUNT(*) FROM terminal_notifications WHERE lease_owner IS NOT NULL)`).
			Scan(&result.LeasesHeld)
	}

	if err := restore(); err != nil {
		// Put the operator back where they started. The failed attempt's file
		// is removed so a half-copied database cannot be mistaken for the
		// restored one.
		_ = os.Remove(resolvedTarget)
		if moved {
			_ = os.Rename(superseded, resolvedTarget)
			result.SupersededPath = ""
		}
		return result, err
	}
	return result, nil
}

// movePreviousDatabase renames the target and its WAL companions aside.
func movePreviousDatabase(target, superseded string) (bool, error) {
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("stat %q: %w", target, err)
	}
	if err := os.Rename(target, superseded); err != nil {
		return false, fmt.Errorf("move existing database aside: %w", err)
	}
	// The -wal and -shm companions belong to the file that just moved. Leaving
	// them next to a freshly restored database is how a restore silently
	// resurrects the transactions it was supposed to replace.
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(target + suffix); err == nil {
			if renameErr := os.Rename(target+suffix, superseded+suffix); renameErr != nil {
				return true, fmt.Errorf("move %s companion aside: %w", suffix, renameErr)
			}
		}
	}
	return true, nil
}

func copyFile(source, destination string) error {
	in, err := os.Open(source) //nolint:gosec // operator-named path
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer func() { _ = in.Close() }()

	//nolint:gosec // destination is the configured database path, named by the operator
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create restored database: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy backup into place: %w", err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("sync restored database: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close restored database: %w", err)
	}
	return nil
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path) //nolint:gosec // operator-named path
	if err != nil {
		return "", fmt.Errorf("open %q for digest: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("digest %q: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
