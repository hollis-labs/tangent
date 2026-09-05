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
	ID            string      `json:"id"`
	Mode          BackupMode  `json:"mode"`
	TakenAt       time.Time   `json:"taken_at"`
	SchemaVersion int64       `json:"schema_version"`
	SizeBytes     int64       `json:"size_bytes"`
	SHA256        string      `json:"sha256"`
	IntegrityOK   bool        `json:"integrity_ok"`
	Fingerprint   Fingerprint `json:"fingerprint"`
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
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}

// Equal compares two properties.
func (p Property) Equal(other Property) bool {
	return p.Rows == other.Rows && p.SHA256 == other.SHA256
}

// fingerprintQueries are the five properties, each as a query returning one
// text column per row. Ordering is applied after the fact in Go over the
// collected strings, so a difference in SQLite's collation cannot change the
// digest.
var fingerprintQueries = map[string]string{
	"definition_digests": `
SELECT interaction_id || '|' || publisher || '|' || kind || '|' || version || '|' || revision ||
       '|' || COALESCE(digest, '') || '|' || source || '|' || COALESCE(schema_identity, '') ||
       '|' || COALESCE(schema_digest, '') || '|' || assurance
FROM definition_bindings`,

	"interaction_identities": `
SELECT id || '|' || surface_id || '|' || caller_scope || '|' || idempotency_key || '|' ||
       surface_sequence || '|' || lifecycle_state || '|' || revision || '|' ||
       COALESCE(terminal_cause, '') || '|' || COALESCE(participant_ref, '')
FROM interactions`,

	"resolutions": `
SELECT id || '|' || interaction_id || '|' || participant_ref || '|' || participant_authority ||
       '|' || participant_assurance || '|' || response_kind || '|' || integrity_digest ||
       '|' || expected_interaction_revision || '|' || presented_projection_revision
FROM resolutions`,

	"audit_history": `
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

	"delivery_obligations": `
SELECT 'resolution|' || id || '|' || resolution_id || '|' || idempotency_key || '|' ||
       lifecycle_state || '|' || revision
FROM resolution_deliveries
UNION ALL
SELECT 'terminal|' || id || '|' || interaction_id || '|' || idempotency_key || '|' ||
       lifecycle_state || '|' || revision
FROM terminal_notifications`,
}

// TakeFingerprint measures the five preservation properties.
func TakeFingerprint(ctx context.Context, database *sql.DB) (Fingerprint, error) {
	status, err := InspectMigrations(ctx, database)
	if err != nil {
		return Fingerprint{}, err
	}
	fingerprint := Fingerprint{SchemaVersion: status.Applied}

	properties := map[string]*Property{
		"definition_digests":     &fingerprint.DefinitionDigests,
		"interaction_identities": &fingerprint.InteractionIdentities,
		"resolutions":            &fingerprint.Resolutions,
		"audit_history":          &fingerprint.AuditHistory,
		"delivery_obligations":   &fingerprint.DeliveryObligations,
	}
	for name, target := range properties {
		property, propertyErr := measureProperty(ctx, database, fingerprintQueries[name])
		if propertyErr != nil {
			return Fingerprint{}, fmt.Errorf("fingerprint %s: %w", name, propertyErr)
		}
		*target = property
	}

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
	return fingerprint, nil
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
}

// Compare reports which of the five properties differ. An empty result is the
// proof criterion 2 asks for, property by property.
func (f Fingerprint) Compare(other Fingerprint) []PropertyDifference {
	differences := []PropertyDifference{}
	pairs := []struct {
		name   string
		before Property
		after  Property
	}{
		{"definition_digests", f.DefinitionDigests, other.DefinitionDigests},
		{"interaction_identities", f.InteractionIdentities, other.InteractionIdentities},
		{"resolutions", f.Resolutions, other.Resolutions},
		{"audit_history", f.AuditHistory, other.AuditHistory},
		{"delivery_obligations", f.DeliveryObligations, other.DeliveryObligations},
	}
	for _, pair := range pairs {
		if !pair.before.Equal(pair.after) {
			differences = append(differences, PropertyDifference{
				Property: pair.name, Before: pair.before, After: pair.after,
			})
		}
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

	info, err := os.Stat(absolute)
	if err != nil {
		return BackupResult{}, fmt.Errorf("stat backup: %w", err)
	}
	manifest.SizeBytes = info.Size()
	manifest.SHA256, err = fileDigest(absolute)
	if err != nil {
		return BackupResult{}, err
	}

	// Verify the copy by opening it, not by trusting that VACUUM INTO worked.
	// A backup nobody has read is a backup nobody knows they have.
	verified, err := VerifyDatabaseFile(ctx, absolute)
	if err != nil {
		return BackupResult{}, err
	}
	manifest.IntegrityOK = verified.Healthy()
	manifest.SchemaVersion = verified.Migrations.Applied
	manifest.Fingerprint = verified.Fingerprint

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
type VerificationReport struct {
	Path                 string          `json:"-"`
	Migrations           MigrationStatus `json:"migrations"`
	IntegrityProblems    []string        `json:"integrity_problems"`
	ForeignKeyViolations int64           `json:"foreign_key_violations"`
	GuardDrift           GuardDrift      `json:"guard_drift"`
	Fingerprint          Fingerprint     `json:"fingerprint"`
}

// Healthy reports whether the file is usable as-is.
func (r VerificationReport) Healthy() bool {
	return len(r.IntegrityProblems) == 0 &&
		r.ForeignKeyViolations == 0 &&
		r.GuardDrift.Intact() &&
		r.Migrations.UpToDate()
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
func VerifyDatabase(ctx context.Context, database *sql.DB) (VerificationReport, error) {
	report := VerificationReport{IntegrityProblems: []string{}}

	problems, err := integrityProblems(ctx, database)
	if err != nil {
		return report, err
	}
	report.IntegrityProblems = problems

	if fkErr := database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_foreign_key_check;`).Scan(&report.ForeignKeyViolations); fkErr != nil {
		return report, fmt.Errorf("foreign key check: %w", fkErr)
	}

	status, err := InspectMigrations(ctx, database)
	if err != nil {
		return report, err
	}
	report.Migrations = status

	installed, err := ReadGuards(ctx, database)
	if err != nil {
		return report, err
	}
	reference, err := ReferenceGuards(ctx)
	if err != nil {
		return report, err
	}
	report.GuardDrift = CompareGuards(installed, reference)

	// A fingerprint of a schema that is not the one this binary knows would be
	// a fingerprint of columns that may not exist. Skip it and let the caller
	// read the migration status instead of a spurious error.
	if status.UpToDate() {
		fingerprint, fingerprintErr := TakeFingerprint(ctx, database)
		if fingerprintErr != nil {
			return report, fingerprintErr
		}
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
	if verification.Migrations.Applied > verification.Migrations.Expected {
		return RestoreResult{}, fmt.Errorf(
			"restore: backup %q is at schema %d and this binary knows %d; restore with the newer binary",
			absoluteSource, verification.Migrations.Applied, verification.Migrations.Expected)
	}
	if !verification.GuardDrift.Intact() && verification.Migrations.UpToDate() {
		return RestoreResult{}, fmt.Errorf(
			"restore: backup %q is missing immutability guards %v; repair it before restoring",
			absoluteSource, verification.GuardDrift.Missing)
	}

	result := RestoreResult{
		SourcePath: absoluteSource, TargetPath: resolvedTarget,
		Source: verification.Fingerprint, Differences: []PropertyDifference{},
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
			return fmt.Errorf("restore: %d preservation propert(ies) did not survive the copy",
				len(result.Differences))
		}
		result.PendingDeliveries = fingerprint.PendingDeliveries
		result.PendingTerminalNotifications = fingerprint.PendingTerminalNotifications

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
