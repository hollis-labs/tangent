package db

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestOnlineBackupRoundTripPreservesEveryProperty is criterion 2, asserted
// property by property rather than as one checksum.
func TestOnlineBackupRoundTripPreservesEveryProperty(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	backupPath := filepath.Join(t.TempDir(), "tangent-backup.db")
	backup, err := Backup(ctx, f.db, f.path, backupPath, BackupOnline)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if !backup.IntegrityOK {
		t.Fatal("the backup failed its own integrity check")
	}
	if backup.SizeBytes == 0 || backup.SHA256 == "" || backup.ID == "" {
		t.Fatalf("manifest is incomplete: %+v", backup.BackupManifest)
	}

	source, err := TakeFingerprint(ctx, f.db)
	if err != nil {
		t.Fatalf("fingerprint source: %v", err)
	}

	restoreTarget := filepath.Join(t.TempDir(), "restored.db")
	result, err := Restore(ctx, restoreTarget, backupPath)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	// The five properties, each on its own, so a failure names which one.
	properties := []struct {
		name   string
		before Property
		after  Property
	}{
		{"definition digests", source.DefinitionDigests, result.Target.DefinitionDigests},
		{"interaction identities", source.InteractionIdentities, result.Target.InteractionIdentities},
		{"resolutions", source.Resolutions, result.Target.Resolutions},
		{"audit history", source.AuditHistory, result.Target.AuditHistory},
		{"delivery obligations", source.DeliveryObligations, result.Target.DeliveryObligations},
	}
	for _, property := range properties {
		t.Run(property.name, func(t *testing.T) {
			if property.before.Rows == 0 {
				t.Fatal("the fixture has no rows for this property, so preserving it proves nothing")
			}
			if !property.before.Equal(property.after) {
				t.Fatalf("not preserved: before=%+v after=%+v", property.before, property.after)
			}
		})
	}
	if len(result.Differences) != 0 {
		t.Fatalf("restore reported differences: %+v", result.Differences)
	}

	// Delivery obligations are not merely present; they are still pending, and
	// they still carry the lease of the process that died. A restore that
	// silently marked them delivered would pass a row-count check and fail
	// this one.
	if result.PendingDeliveries != 1 || result.PendingTerminalNotifications != 1 {
		t.Fatalf("pending obligations were not preserved: %d deliveries, %d notifications",
			result.PendingDeliveries, result.PendingTerminalNotifications)
	}
	if result.LeasesHeld != 2 {
		t.Fatalf("expected 2 held leases to survive for restart recovery, got %d", result.LeasesHeld)
	}

	// And the content is really there, not just the identity.
	restored, err := Open(restoreTarget)
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer func() { _ = restored.Close() }()
	var payload string
	if err := restored.QueryRowContext(ctx,
		`SELECT response_payload FROM resolutions WHERE id = 'res_int_resolved'`).Scan(&payload); err != nil {
		t.Fatalf("read restored resolution: %v", err)
	}
	if !strings.Contains(payload, "the participant's own words") {
		t.Fatal("the restored resolution lost its payload")
	}
}

// TestOnlineBackupIsConsistentWhileTheDatabaseIsWritten is criterion 1's first
// half. The writes run on the same handle, which is the strongest form of
// "while the service runs" this process can arrange: SetMaxOpenConns(1) means
// they genuinely interleave with the copy's transaction.
func TestOnlineBackupIsConsistentWhileTheDatabaseIsWritten(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			_, _ = f.db.ExecContext(ctx, `INSERT INTO telemetry_events
(event_id, trace_id, span_id, event_name, outcome, occurred_at)
VALUES (?, '0123456789abcdef0123456789abcdef', 'fedcba9876543210', 'test.write', 'ok', ?)`,
				"tev_"+time.Now().UTC().Format("150405.000000000")+"_"+string(rune('a'+i%26)),
				time.Now().UTC())
		}
	}()

	backupPath := filepath.Join(t.TempDir(), "concurrent.db")
	backup, err := Backup(ctx, f.db, f.path, backupPath, BackupOnline)
	<-done
	if err != nil {
		t.Fatalf("backup during writes: %v", err)
	}
	if !backup.IntegrityOK {
		t.Fatal("a backup taken during writes failed its integrity check")
	}
	verification, err := VerifyDatabaseFile(ctx, backupPath)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !verification.Healthy() {
		t.Fatalf("the backup is not healthy: %+v", verification)
	}
}

func TestDrainedBackupLeavesAnEmptyWAL(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Make the WAL non-trivial first, so "it is empty afterwards" means
	// something.
	for i := range 50 {
		f.exec(t, `INSERT INTO telemetry_events
(event_id, trace_id, span_id, event_name, outcome, occurred_at)
VALUES (?, '0123456789abcdef0123456789abcdef', 'fedcba9876543210', 'test.fill', 'ok', ?)`,
			"fill_"+string(rune('a'+i%26))+string(rune('a'+i/26)), f.now)
	}
	if fileSize(f.path+"-wal") == 0 {
		t.Skip("this SQLite build checkpointed eagerly; the drain assertion needs a non-empty WAL")
	}

	backupPath := filepath.Join(t.TempDir(), "drained.db")
	backup, err := Backup(ctx, f.db, f.path, backupPath, BackupDrained)
	if err != nil {
		t.Fatalf("drained backup: %v", err)
	}
	if backup.Mode != BackupDrained {
		t.Fatalf("mode = %s", backup.Mode)
	}
	if size := fileSize(f.path + "-wal"); size != 0 {
		t.Fatalf("the drain left %d bytes in the WAL", size)
	}
	if !backup.IntegrityOK {
		t.Fatal("the drained backup failed its integrity check")
	}
}

func TestBackupWritesAManifestAndSurveyFindsIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	directory := t.TempDir()
	backup, err := Backup(ctx, f.db, f.path, filepath.Join(directory, "one.db"), BackupOnline)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	raw, err := os.ReadFile(backup.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest BackupManifest
	if decodeErr := json.Unmarshal(raw, &manifest); decodeErr != nil {
		t.Fatalf("decode manifest: %v", decodeErr)
	}
	if manifest.ID != backup.ID || manifest.SchemaVersion == 0 {
		t.Fatalf("manifest does not describe the backup: %+v", manifest)
	}
	// The manifest travels with the backup and must not carry the layout of the
	// machine it came from.
	if strings.Contains(string(raw), directory) || strings.Contains(string(raw), f.path) {
		t.Fatal("the manifest leaks a filesystem path")
	}

	survey, err := SurveyBackups(directory, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("survey: %v", err)
	}
	if survey.State != backupSurveyDone || len(survey.IDs) != 1 || survey.IDs[0] != backup.ID {
		t.Fatalf("survey did not find the backup: %+v", survey)
	}

	// An unsurveyed request says so, which is a weaker and more honest claim
	// than an empty list.
	empty, err := SurveyBackups("", time.Now().UTC())
	if err != nil {
		t.Fatalf("empty survey: %v", err)
	}
	if empty.State != backupSurveyNone {
		t.Fatalf("an unsurveyed request reported %q", empty.State)
	}
}

func TestBackupRefusesToOverwrite(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	destination := filepath.Join(t.TempDir(), "taken.db")
	if err := os.WriteFile(destination, []byte("not a database"), 0o600); err != nil {
		t.Fatalf("seed destination: %v", err)
	}
	if _, err := Backup(ctx, f.db, f.path, destination, BackupOnline); err == nil {
		t.Fatal("the backup overwrote an existing file")
	}
}

func TestRestoreRefusesABadBackupAndLeavesTheTargetAlone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	target := f.path
	corrupt := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("this is not a SQLite file"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	if _, err := Restore(ctx, target, corrupt); err == nil {
		t.Fatal("restore accepted a file that is not a database")
	}
	// The original is untouched: it was never moved aside, because verification
	// runs before anything is renamed.
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("the target database was disturbed by a refused restore: %v", err)
	}
	if payload := f.column(t,
		`SELECT request_snapshot FROM interactions WHERE id = 'int_resolved'`); !strings.Contains(
		payload, "the caller payload") {
		t.Fatal("a refused restore changed the target's contents")
	}
}

func TestRestoreMovesTheSupersededDatabaseAsideRatherThanDeletingIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	backupPath := filepath.Join(t.TempDir(), "before.db")
	if _, err := Backup(ctx, f.db, f.path, backupPath, BackupOnline); err != nil {
		t.Fatalf("backup: %v", err)
	}

	// A second database to restore over, so the source handle stays open and
	// the test never touches a file another connection owns.
	targetDir := t.TempDir()
	target := filepath.Join(targetDir, "target.db")
	seed, err := Open(target)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	if migrateErr := RunMigrations(seed); migrateErr != nil {
		t.Fatalf("migrate target: %v", migrateErr)
	}
	if closeErr := seed.Close(); closeErr != nil {
		t.Fatalf("close target: %v", closeErr)
	}

	result, err := Restore(ctx, target, backupPath)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if result.SupersededPath == "" {
		t.Fatal("the replaced database was not preserved")
	}
	if _, err := os.Stat(result.SupersededPath); err != nil {
		t.Fatalf("the superseded database is not where the result says it is: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(result.SupersededPath), "target.db.superseded-") {
		t.Fatalf("unexpected superseded name %q", result.SupersededPath)
	}
}

func TestVerifyReportsAMissingGuard(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.db.ExecContext(ctx, `DROP TRIGGER resolutions_immutable_delete;`); err != nil {
		t.Fatalf("drop guard: %v", err)
	}
	verification, err := VerifyDatabase(ctx, f.db)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verification.Healthy() {
		t.Fatal("a database missing an immutability guard reported healthy")
	}
	if len(verification.GuardDrift.Missing) != 1 ||
		verification.GuardDrift.Missing[0] != "resolutions_immutable_delete" {
		t.Fatalf("drift did not name the missing guard: %+v", verification.GuardDrift)
	}
}

func TestRepairRestoresAMissingGuardAndRecordsIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.db.ExecContext(ctx, `DROP TRIGGER resolutions_immutable_delete;`); err != nil {
		t.Fatalf("drop guard: %v", err)
	}
	report, err := Repair(ctx, f.db, "operator:test")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if len(report.RestoredGuards) != 1 || report.RestoredGuards[0] != "resolutions_immutable_delete" {
		t.Fatalf("repair did not restore the guard: %+v", report)
	}
	if len(report.Unfixable) != 0 {
		t.Fatalf("repair reported unfixable conditions on a healthy database: %v", report.Unfixable)
	}
	if report.OperationID == "" {
		t.Fatal("the repair was not recorded in the retention log")
	}
	if drift := f.guardDrift(t); !drift.Intact() {
		t.Fatalf("the repair did not close the drift: %+v", drift)
	}
	// The restored guard is the real one, not a stub that reports present.
	if _, err := f.db.ExecContext(ctx,
		`DELETE FROM resolutions WHERE id = 'res_int_resolved'`); err == nil {
		t.Fatal("the recreated guard does not abort DELETE")
	}
}

func TestRepairRefusesToRepairADirtySchema(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.db.ExecContext(ctx, `UPDATE schema_migrations SET dirty = 1`); err != nil {
		t.Fatalf("mark dirty: %v", err)
	}
	report, err := Repair(ctx, f.db, "operator:test")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if len(report.RestoredGuards) != 0 {
		t.Fatal("repair touched a dirty schema")
	}
	if len(report.Unfixable) != 1 || !strings.Contains(report.Unfixable[0], "dirty") {
		t.Fatalf("repair did not name the dirty schema: %+v", report.Unfixable)
	}
}

func TestCompactPreservesEveryPropertyAndTheGuards(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	before, err := TakeFingerprint(ctx, f.db)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	report, err := Compact(ctx, f.db, f.path)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if report.Before.PageSize == 0 || report.After.PageSize == 0 {
		t.Fatalf("storage report is empty: %+v", report)
	}
	after, err := TakeFingerprint(ctx, f.db)
	if err != nil {
		t.Fatalf("fingerprint after: %v", err)
	}
	if differences := before.Compare(after); len(differences) != 0 {
		t.Fatalf("compaction changed a preservation property: %+v", differences)
	}
	if drift := f.guardDrift(t); !drift.Intact() {
		t.Fatalf("compaction dropped guards: %+v", drift)
	}
}

func TestCheckReportsStorageAndOwnership(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	report, err := Check(ctx, f.db, f.path)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !report.Verification.Healthy() {
		t.Fatalf("a fresh fixture is not healthy: %+v", report.Verification)
	}
	if report.Storage.JournalMode != "wal" {
		t.Fatalf("journal mode = %q, want wal", report.Storage.JournalMode)
	}
	if !report.Storage.ForeignKeysEnforced {
		t.Fatal("foreign keys are not enforced; the purge cascade would silently do nothing")
	}
	if report.OwnershipHeld {
		t.Fatal("nothing acquired the lock, but check reports it held")
	}

	owner, err := AcquireOwnership(f.path, RoleServer, "test")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer func() { _ = owner.Release() }()

	held, err := Check(ctx, f.db, f.path)
	if err != nil {
		t.Fatalf("check while owned: %v", err)
	}
	if !held.OwnershipHeld || held.Ownership.Role != RoleServer {
		t.Fatalf("check did not see the owner: %+v", held.Ownership)
	}
}

// TestBackupTakenBeforeRedactionStillHoldsTheContent is ADR 0002 §7's
// uncomfortable fact, asserted rather than assumed. If this ever stops being
// true, the documentation that says redaction cannot reach a backup is wrong.
func TestBackupTakenBeforeRedactionStillHoldsTheContent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	backupPath := filepath.Join(t.TempDir(), "before-erasure.db")
	if _, err := Backup(ctx, f.db, f.path, backupPath, BackupOnline); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if _, err := RedactInteraction(ctx, f.db, RetentionRequest{
		InteractionID: "int_resolved", ActorRef: "operator:test", Now: f.now,
	}); err != nil {
		t.Fatalf("redact: %v", err)
	}

	backup, err := Open(backupPath)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer func() { _ = backup.Close() }()
	var payload string
	if err := backup.QueryRowContext(ctx,
		`SELECT response_payload FROM resolutions WHERE id = 'res_int_resolved'`).Scan(&payload); err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !strings.Contains(payload, "the participant's own words") {
		t.Fatal("the backup no longer holds the erased content, which would make the " +
			"documented limitation wrong rather than the code right")
	}
}
