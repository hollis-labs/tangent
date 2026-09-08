package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/interaction"
)

// The operator command surface for CW-20260825-0072.
//
// Every one of these is a one-shot: it runs, prints a JSON report, and exits
// before the server would have started. They are flags on the same flag set as
// the server's own because that is the shape this binary already had, and
// because a maintenance mode that could be combined with serving would defeat
// the refusal these commands are built around.
//
// Two properties are enforced here rather than in internal/db, because they are
// process-level questions that the storage layer cannot answer:
//
//  1. Whether anything is serving. internal/db knows about the single-writer
//     lock; only this layer knows there is an HTTP port and a /readyz on it. A
//     Tangent that predates the lock is still a serving process, and the
//     readiness probe is how a command notices one.
//  2. Whether the operator meant it. --confirm is required for every command
//     that removes content or replaces the database, because there is no undo
//     for a purge and the superseded copy a restore leaves behind is a
//     consolation rather than a fix.

// maintenanceFlags is the operator surface, registered on the server's own
// flag set.
type maintenanceFlags struct {
	check   *bool
	backup  *string
	drain   *bool
	restore *string
	compact *bool
	repair  *bool

	retentionPlan  *bool
	retentionApply *bool

	eraseSurface     *string
	eraseInteraction *string
	eraseExchange    *string
	eraseChannel     *string
	eraseDrafts      *string
	purgeSurface     *string
	closeSurface     *string
	expireCapability *bool
	externalReport   *string

	actor     *string
	policyRef *string
	backupDir *string
	dryRun    *bool
	confirm   *bool
	history   *bool
}

func registerMaintenanceFlags(flagSet *flag.FlagSet) *maintenanceFlags {
	f := &maintenanceFlags{}
	f.check = flagSet.Bool("db-check", false,
		"report database integrity, schema, immutability guards, storage, and ownership, then exit")
	f.backup = flagSet.String("db-backup", "",
		"write a consistent backup to this path with VACUUM INTO, then exit")
	f.drain = flagSet.Bool("db-drain", false,
		"with --db-backup: require exclusive ownership and checkpoint the WAL first")
	f.restore = flagSet.String("db-restore", "",
		"replace the database with the backup at this path, then exit (requires --confirm)")
	f.compact = flagSet.Bool("db-compact", false,
		"VACUUM the database and truncate the WAL, then exit")
	f.repair = flagSet.Bool("db-repair", false,
		"recreate missing immutability guards and checkpoint the WAL, then exit")

	f.retentionPlan = flagSet.Bool("retention-plan", false,
		"report which interactions and surfaces are past their retention window, then exit")
	f.retentionApply = flagSet.Bool("retention-apply", false,
		"redact every record past its retention window, then exit (requires --confirm)")

	f.eraseInteraction = flagSet.String("erase-interaction", "",
		"redact one interaction's caller payload, drafts, resolution, and adapter text (requires --confirm)")
	f.eraseSurface = flagSet.String("erase-surface", "",
		"redact every payload on one surface, including the legacy room copy (requires --confirm)")
	f.eraseExchange = flagSet.String("erase-exchange", "",
		"redact one relay exchange's body and its delivery receipts' and outbox row's freeform "+
			"text (requires --confirm)")
	f.eraseChannel = flagSet.String("erase-channel", "",
		"redact the body and delivery freeform text of every exchange in one channel, both "+
			"directions (requires --confirm)")
	f.eraseDrafts = flagSet.String("erase-drafts", "",
		"delete one interaction's draft revisions and write their tombstones (requires --confirm)")
	f.purgeSurface = flagSet.String("purge-surface", "",
		"delete one surface and everything that cascades from it, destroying record "+
			"skeletons and idempotency keys (requires --confirm)")
	f.closeSurface = flagSet.String("close-surface", "",
		"close one surface and cancel its outstanding interactions; removes no content")
	f.expireCapability = flagSet.Bool("expire-capabilities", false,
		"delete every expired or revoked host-mediated effect handle, then exit")
	f.externalReport = flagSet.String("external-deletion-report", "",
		"list the external references one interaction holds, which Tangent cannot delete at the source")
	f.history = flagSet.Bool("retention-history", false,
		"print the retention operation log, newest first, then exit")

	f.actor = flagSet.String("actor", "", "actor reference recorded on the retention operation")
	f.policyRef = flagSet.String("policy-ref", "", "policy reference recorded on the retention operation")
	f.backupDir = flagSet.String("backup-dir", "",
		"survey this directory for backup manifests, so the operation records which backups still "+
			"hold the content it removes")
	f.dryRun = flagSet.Bool("dry-run", false,
		"report what the operation would remove and write nothing, not even an audit row")
	f.confirm = flagSet.Bool("confirm", false,
		"required for any command that removes content or replaces the database")
	return f
}

// requested reports whether any maintenance mode was asked for, and how many.
func (f *maintenanceFlags) requested() int {
	modes := []bool{
		*f.check, *f.backup != "", *f.restore != "", *f.compact, *f.repair,
		*f.retentionPlan, *f.retentionApply, *f.eraseSurface != "", *f.eraseInteraction != "",
		*f.eraseExchange != "", *f.eraseChannel != "",
		*f.eraseDrafts != "", *f.purgeSurface != "", *f.closeSurface != "",
		*f.expireCapability, *f.externalReport != "", *f.history,
	}
	count := 0
	for _, mode := range modes {
		if mode {
			count++
		}
	}
	return count
}

// needsExclusiveOwnership reports whether the requested command writes.
//
// --db-check, --retention-plan, --retention-history, and an online --db-backup
// are readers: they run against a serving installation on purpose, because an
// operator diagnosing an incident should not have to stop the thing they are
// diagnosing in order to look at it.
func (f *maintenanceFlags) needsExclusiveOwnership() bool {
	switch {
	case *f.backup != "" && *f.drain:
		return true
	case *f.restore != "", *f.compact, *f.repair, *f.retentionApply,
		*f.eraseSurface != "", *f.eraseInteraction != "", *f.eraseExchange != "", *f.eraseChannel != "",
		*f.eraseDrafts != "", *f.purgeSurface != "", *f.closeSurface != "", *f.expireCapability,
		// The external-source report writes a refusal row, and a write is a
		// write: it is listed here rather than treated as a reader because the
		// erasure log must not be appended to by a second process.
		*f.externalReport != "" && !*f.dryRun:
		return true
	}
	return false
}

// needsConfirmation reports whether the command removes content or replaces the
// database.
func (f *maintenanceFlags) needsConfirmation() bool {
	if *f.dryRun {
		return false
	}
	return *f.restore != "" || *f.retentionApply || *f.eraseSurface != "" ||
		*f.eraseInteraction != "" || *f.eraseExchange != "" || *f.eraseChannel != "" ||
		*f.eraseDrafts != "" || *f.purgeSurface != "" || *f.expireCapability
}

// runMaintenance executes the requested command. It returns the process exit
// code; zero means the command ran and reported.
//
//nolint:gocyclo // one dispatch per operator command; splitting it hides the surface
func runMaintenance(
	ctx context.Context,
	flags *maintenanceFlags,
	database *sql.DB,
	databasePath string,
	closeDatabase func() error,
) int {
	if flags.needsConfirmation() && !*flags.confirm {
		return fail("this command removes content or replaces the database; " +
			"re-run it with --confirm, or with --dry-run to see what it would do")
	}

	actor := *flags.actor
	if actor == "" {
		actor = "operator:cli"
	}
	req := tangentdb.RetentionRequest{
		ActorRef:  actor,
		Authority: tangentdb.AuthorityLocalUser,
		PolicyRef: *flags.policyRef,
		DryRun:    *flags.dryRun,
	}
	// A failed survey is not a failed command: the survey's own state says
	// `survey-failed`, and the audit row carries that rather than silently
	// reading as though nobody looked.
	survey, surveyErr := tangentdb.SurveyBackups(*flags.backupDir, time.Now().UTC())
	if surveyErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: backup survey: %v\n", surveyErr)
	}
	req.Backups = survey

	switch {
	case *flags.check:
		report, err := tangentdb.Check(ctx, database, databasePath)
		if err != nil {
			return fail("db-check: %v", err)
		}
		emit(report)
		// Damage and age are reported separately, because the actions are
		// opposite: one is a restore, the other is a migration.
		if damage := report.Verification.Damage(); len(damage) > 0 {
			return fail("db-check: this database is damaged: %s", strings.Join(damage, "; "))
		}
		if !report.Verification.Healthy() {
			return fail("db-check: %s", report.Verification.SchemaAdvice)
		}
		if report.Verification.SchemaAdvice != "" {
			fmt.Fprintf(os.Stderr, "tangent: db-check: %s\n", report.Verification.SchemaAdvice)
		}
		return 0

	case *flags.backup != "":
		mode := tangentdb.BackupOnline
		if *flags.drain {
			mode = tangentdb.BackupDrained
		}
		result, err := tangentdb.Backup(ctx, database, databasePath, *flags.backup, mode)
		if err != nil {
			return fail("db-backup: %v", err)
		}
		emit(result)
		if !result.IntegrityOK {
			return fail("db-backup: the backup was written to %s but is damaged: %s. "+
				"Do not rely on it.", result.Path, strings.Join(result.Damage, "; "))
		}
		// A source older than this binary is the ordinary pre-upgrade case, so
		// it is a note rather than a failure: the copy is sound, and the
		// schema it carries is the one you had when you took it.
		if result.SchemaAdvice != "" {
			fmt.Fprintf(os.Stderr, "tangent: db-backup: the backup is sound. %s\n", result.SchemaAdvice)
		}
		return 0

	case *flags.restore != "":
		// The handle is closed first, and this is not a tidiness preference. A
		// restore renames the database file aside and copies another into its
		// place; an open descriptor would follow the renamed inode, so the
		// process would go on reading and writing the database it just
		// replaced, and its WAL would be flushed into the superseded copy at
		// exit. The ownership lock is held throughout, so closing here does not
		// open a window for another writer.
		if closeErr := closeDatabase(); closeErr != nil {
			return fail("db-restore: close the database before replacing it: %v", closeErr)
		}
		result, err := tangentdb.Restore(ctx, databasePath, *flags.restore)
		if err != nil {
			emit(result)
			return fail("db-restore: %v", err)
		}
		emit(result)
		// Restoring a pre-upgrade backup leaves the database behind this
		// binary on purpose — that is what rolling back a migration means. The
		// operator is told what to run next rather than left to discover it
		// when the server refuses to boot.
		if result.NextStep != "" {
			fmt.Fprintf(os.Stderr, "tangent: db-restore: %s\n", result.NextStep)
		}
		return 0

	case *flags.compact:
		report, err := tangentdb.Compact(ctx, database, databasePath)
		if err != nil {
			return fail("db-compact: %v", err)
		}
		emit(report)
		return 0

	case *flags.repair:
		report, err := tangentdb.Repair(ctx, database, actor)
		if err != nil {
			return fail("db-repair: %v", err)
		}
		emit(report)
		if len(report.Unfixable) > 0 {
			return 1
		}
		return 0

	case *flags.retentionPlan:
		plan, err := tangentdb.PlanRetention(ctx, database, tangentdb.DefaultRetentionWindows(), time.Now().UTC())
		if err != nil {
			return fail("retention-plan: %v", err)
		}
		emit(plan)
		return 0

	case *flags.retentionApply:
		results, err := tangentdb.ApplyRetention(ctx, database,
			tangentdb.DefaultRetentionWindows(), req, time.Now().UTC())
		if err != nil {
			emit(results)
			return fail("retention-apply: %v", err)
		}
		emit(results)
		return 0

	case *flags.eraseInteraction != "":
		req.InteractionID = *flags.eraseInteraction
		result, err := tangentdb.RedactInteraction(ctx, database, req)
		if err != nil {
			return fail("erase-interaction: %v", err)
		}
		emit(result)
		return 0

	case *flags.eraseSurface != "":
		req.SurfaceID = *flags.eraseSurface
		result, err := tangentdb.RedactSurface(ctx, database, req)
		if err != nil {
			return fail("erase-surface: %v", err)
		}
		emit(result)
		return 0

	case *flags.eraseExchange != "":
		req.ExchangeID = *flags.eraseExchange
		result, err := tangentdb.RedactExchange(ctx, database, req)
		if err != nil {
			return fail("erase-exchange: %v", err)
		}
		emit(result)
		return 0

	case *flags.eraseChannel != "":
		req.ChannelID = *flags.eraseChannel
		result, err := tangentdb.RedactChannel(ctx, database, req)
		if err != nil {
			return fail("erase-channel: %v", err)
		}
		emit(result)
		return 0

	case *flags.eraseDrafts != "":
		req.InteractionID = *flags.eraseDrafts
		result, err := tangentdb.DeleteDrafts(ctx, database, req)
		if err != nil {
			return fail("erase-drafts: %v", err)
		}
		emit(result)
		return 0

	case *flags.purgeSurface != "":
		req.SurfaceID = *flags.purgeSurface
		result, err := tangentdb.PurgeSurface(ctx, database, req)
		if err != nil {
			return fail("purge-surface: %v", err)
		}
		emit(result)
		return 0

	case *flags.closeSurface != "":
		req.SurfaceID = *flags.closeSurface
		return closeSurface(ctx, database, req, *flags.closeSurface)

	case *flags.expireCapability:
		req.Authority = tangentdb.AuthorityHostPolicy
		result, err := tangentdb.ExpireCapabilities(ctx, database, req, time.Now().UTC())
		if err != nil {
			return fail("expire-capabilities: %v", err)
		}
		emit(result)
		return 0

	case *flags.externalReport != "":
		req.InteractionID = *flags.externalReport
		result, references, err := tangentdb.ReportExternalSourceDeletion(ctx, database, req)
		if err != nil {
			return fail("external-deletion-report: %v", err)
		}
		emit(struct {
			Result     tangentdb.RetentionResult     `json:"result"`
			References []tangentdb.ExternalReference `json:"references"`
		}{result, references})
		return 0

	case *flags.history:
		history, err := tangentdb.RetentionHistory(ctx, database, 100)
		if err != nil {
			return fail("retention-history: %v", err)
		}
		emit(history)
		return 0
	}
	return fail("no maintenance command was requested")
}

// closeSurface performs the lifecycle transition through internal/interaction
// and files the audit row that records it as removing nothing.
//
// It goes through the lifecycle layer rather than writing the surface row here
// because a close cancels outstanding interactions, advances revisions, and
// emits events; reimplementing that in a CLI would be a second, divergent
// close.
func closeSurface(
	ctx context.Context,
	database *sql.DB,
	req tangentdb.RetentionRequest,
	surfaceID string,
) int {
	var revision int64
	if err := database.QueryRowContext(ctx,
		`SELECT revision FROM surfaces WHERE id = ?`, surfaceID).Scan(&revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fail("close-surface: no surface with id %q", surfaceID)
		}
		return fail("close-surface: read surface revision: %v", err)
	}

	if req.DryRun {
		// A close is a lifecycle transition, not a removal, so there is nothing
		// for a dry run to preview removing. What it can say is how many
		// outstanding interactions the close would cancel — and it must not
		// perform the close, which an earlier shape of this function would
		// have done because CloseSurface has no dry mode of its own.
		var outstanding int
		if err := database.QueryRowContext(ctx, `
SELECT COUNT(*) FROM interactions
WHERE surface_id = ?
  AND lifecycle_state NOT IN ('resolved', 'canceled', 'expired', 'failed', 'superseded')`,
			surfaceID).Scan(&outstanding); err != nil {
			return fail("close-surface: count outstanding interactions: %v", err)
		}
		audit, _ := tangentdb.RecordSurfaceClose(ctx, database, req, outstanding)
		emit(struct {
			Close tangentdb.RetentionResult `json:"close"`
			Rows  int                       `json:"canceled_interactions"`
		}{audit, outstanding})
		return 0
	}

	policyRef := req.PolicyRef
	if policyRef == "" {
		policyRef = "operator-close"
	}
	store := interaction.NewStore(database)
	result, err := store.CloseSurface(ctx, interaction.CloseSurfaceParams{
		SurfaceID:        surfaceID,
		ExpectedRevision: revision,
		Reason:           "operator closed the surface from the command line",
		PolicyRef:        policyRef,
		ActorRef:         req.ActorRef,
		Authority:        "administrator",
	})
	if err != nil {
		return fail("close-surface: %v", err)
	}
	audit, auditErr := tangentdb.RecordSurfaceClose(ctx, database, req, result.DisposedInteractions)
	if auditErr != nil {
		return fail("close-surface: the surface closed but the audit row could not be written: %v", auditErr)
	}
	emit(struct {
		Close tangentdb.RetentionResult `json:"close"`
		Rows  int                       `json:"canceled_interactions"`
	}{audit, result.DisposedInteractions})
	return 0
}

// refuseIfServing is criterion 4's refusal, keyed on the database the command
// is about to touch.
//
// It used to be two refusals — the single-writer lock, and a /readyz probe on
// the configured HTTP port — and the port half was answering a different
// question than the one that matters. A port is not a database.
// CW-20260905-0014 hit the false positive that follows: a restore into an
// unrelated temporary database was refused because the live installation was
// serving on 7842, and the only way past it was to misstate the port with
// TANGENT_HTTP_PORT.
//
// So the lock is the refusal now, and it is taken first. Every Tangent since
// CW-20260825-0072 claims `<database>.owner` before it opens the database,
// serving processes included, so a conflict names the process that holds
// *this* database — pid, role, host, since when — rather than describing a
// socket that may belong to something else entirely.
//
// The probe survives as a note rather than a refusal, because it still catches
// the one case the lock cannot: a Tangent old enough to take no lock. It
// cannot be more than a note, because nothing in an HTTP readiness answer says
// which database the answering process has open, and a refusal that cannot
// tell will sooner or later refuse the wrong thing. §1 of
// docs/database-operations.md records the residual gap.
func refuseIfServing(databasePath string, port int, requireExclusive bool) (*tangentdb.Ownership, error) {
	if !requireExclusive {
		// Readers run against a live installation on purpose. An online backup
		// is criterion 1's first half — consistent while the service runs — and
		// --db-check exists to be run during an incident, not after one.
		return nil, nil //nolint:nilnil // a reader needs no claim; nil ownership is the answer
	}
	ownership, err := tangentdb.AcquireOwnership(databasePath, tangentdb.RoleMaintenance, commandLabel())
	if err != nil {
		var conflict *tangentdb.OwnershipConflict
		if errors.As(err, &conflict) {
			return nil, fmt.Errorf("%w; stop it, or wait for it to finish", conflict)
		}
		return nil, err
	}
	if serving, detail := probeServing(port); serving {
		fmt.Fprintf(os.Stderr,
			"tangent: note: something answers /readyz on port %d (%s). This command holds the "+
				"single-writer lock on %s, so that process is not writing the database this "+
				"command will touch — unless it is a Tangent old enough to take no lock, in "+
				"which case stop it first.\n",
			port, detail, databasePath)
	}
	return ownership, nil
}

// probeServing asks the loopback listener whether something answers.
//
// It probes /readyz rather than opening a bare TCP connection because a port
// that accepts a connection is not necessarily a Tangent, and because a
// readiness answer is what tells an operator *which* installation they found.
// Any HTTP response at all counts as serving: a Tangent whose database is gone
// still holds the database file open.
func probeServing(port int) (bool, string) {
	address := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
	client := &http.Client{Timeout: 750 * time.Millisecond}
	response, err := client.Get("http://" + address + "/readyz") //nolint:noctx // bounded by the client timeout
	if err != nil {
		return false, ""
	}
	defer func() { _ = response.Body.Close() }()
	return true, fmt.Sprintf("/readyz answered %d", response.StatusCode)
}

func commandLabel() string {
	if len(os.Args) > 1 {
		return "tangent " + os.Args[1]
	}
	return "tangent"
}

// emit prints a report as indented JSON on stdout. Operator commands print
// structured output because the answers are consumed by a person reading them
// during an incident and by a script that wants the numbers, and one format
// that serves both beats two that drift.
func emit(value any) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "tangent: encode report: %v\n", err)
		return
	}
	fmt.Println(string(encoded))
}

func fail(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "tangent: "+format+"\n", args...)
	return 1
}
