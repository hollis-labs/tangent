package db

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The cross-schema cases, which CW-20260825-0072 never constructed and
// CW-20260905-0014 was.
//
// Every fixture in the original suite was migrated to HEAD before anything was
// measured, so every backup source in every test was already at the binary's
// schema. That is the one shape a pre-upgrade backup never has: you take the
// backup *because* you are about to migrate, so the source is behind by
// definition. These tests are all built the other way round — an older
// database, backed up and restored by a newer binary — and they are what makes
// "older" and "damaged" two answers rather than one.

// backupSchemaVersion is the version the cross-schema fixtures are built at:
// two migrations behind HEAD when this was written, and the version of the
// live database that exposed the defect. It is derived rather than hard-coded
// so that adding a migration moves it forward instead of quietly making these
// tests same-schema tests again.
func backupSchemaVersion(t *testing.T) int64 {
	t.Helper()
	expected, err := ExpectedMigrationVersion()
	if err != nil {
		t.Fatalf("expected migration version: %v", err)
	}
	if expected < 11 {
		t.Fatalf("this binary embeds %d migrations; the fixture needs at least 11", expected)
	}
	return expected - 2
}

// preRetentionOperationsSchemaVersion is fixed, not derived: the erasure log
// (retention_operations) arrives at a specific historical migration, 0012,
// and "before that log existed" is version 11 regardless of how many
// migrations land on top of it later. Deriving this one the way
// backupSchemaVersion derives its "two behind HEAD" would have been wrong
// the same way TestOlderSchemaAnswersAreLegibleNotDriverErrors's own
// "-2" assumption was: CW-20260906-0065 (migration 0014) pushed HEAD-2 to
// version 12, which already has the log, and the assertion that a database
// behind it has no log silently stopped holding.
const preRetentionOperationsSchemaVersion int64 = 11

// lastGuardChangingSchemaVersion is fixed for the same reason
// preRetentionOperationsSchemaVersion is: TestGuardReferenceIsBuiltAtTheDatabaseOwnSchema
// needs a schema whose guard count is strictly less than HEAD's, and that
// depends on which migration last added a guard-suspending trigger, not on
// how many migrations have landed since. Migration 0014 (CW-20260906-0065)
// added the relay journal's six immutability triggers; 0015
// (participant_presence) and 0016 (CW-20260907-0043's retention_operations
// columns) added none, so backupSchemaVersion's "two behind HEAD" stopped
// implying a smaller guard count the moment a second guard-free migration
// landed on top of the first.
const lastGuardChangingSchemaVersion int64 = 13

// TestBackupOfADatabaseOlderThanTheBinaryIsSoundAndFingerprinted is the
// defect, asserted directly. Against the code this replaces it fails three
// times over: integrity_ok false, a fingerprint of zeros, and a
// fingerprint.schema_version of 0.
func TestBackupOfADatabaseOlderThanTheBinaryIsSoundAndFingerprinted(t *testing.T) {
	version := backupSchemaVersion(t)
	f := newFixtureAtSchema(t, version)
	ctx := context.Background()

	backup, err := Backup(ctx, f.db, f.path, filepath.Join(t.TempDir(), "pre-upgrade.db"), BackupOnline)
	if err != nil {
		t.Fatalf("backup an older database: %v", err)
	}
	if !backup.IntegrityOK {
		t.Fatalf("a sound database two migrations behind reported damaged: %v", backup.Damage)
	}
	if len(backup.Damage) != 0 {
		t.Fatalf("damage reported on an undamaged backup: %v", backup.Damage)
	}
	if backup.SchemaState != SchemaBehind {
		t.Fatalf("schema state = %q, want %q", backup.SchemaState, SchemaBehind)
	}
	if backup.SchemaVersion != version {
		t.Fatalf("manifest records schema %d, want %d", backup.SchemaVersion, version)
	}
	// The advice is the operator's half of the answer: the copy is sound, and
	// here is what to do about the schema it carries.
	if !strings.Contains(backup.SchemaAdvice, "--migrate-only") {
		t.Fatalf("the manifest does not say how to move the schema forward: %q", backup.SchemaAdvice)
	}

	// The fingerprint is the part that was zeros. Every section is applicable
	// at this schema, and every one of them measured rows.
	if backup.Fingerprint.SchemaVersion != version {
		t.Fatalf("fingerprint records schema %d, want %d",
			backup.Fingerprint.SchemaVersion, version)
	}
	if !backup.Fingerprint.Complete() {
		t.Fatalf("properties were skipped at schema %d: %v", version, backup.Fingerprint.Skipped())
	}
	for _, section := range fingerprintSections {
		property := *section.target(&backup.Fingerprint)
		if !property.Applicable {
			t.Fatalf("%s is not applicable at schema %d", section.name, version)
		}
		if property.Rows == 0 || property.SHA256 == "" {
			t.Fatalf("%s was not measured: %+v", section.name, property)
		}
	}
}

// TestRestoreAcceptsABackupOlderThanTheBinaryAndSaysWhatIsNext is the other
// half of the upgrade contract: restoring the pre-upgrade backup and
// re-migrating is the rollback, so a restore that refuses an older backup
// refuses at exactly the moment it is needed.
func TestRestoreAcceptsABackupOlderThanTheBinaryAndSaysWhatIsNext(t *testing.T) {
	version := backupSchemaVersion(t)
	f := newFixtureAtSchema(t, version)
	ctx := context.Background()

	backupPath := filepath.Join(t.TempDir(), "pre-upgrade.db")
	backup, err := Backup(ctx, f.db, f.path, backupPath, BackupOnline)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	target := filepath.Join(t.TempDir(), "restored.db")
	result, err := Restore(ctx, target, backupPath)
	if err != nil {
		t.Fatalf("restore an older backup: %v", err)
	}
	if len(result.Differences) != 0 {
		t.Fatalf("restore reported differences: %+v", result.Differences)
	}
	if result.SchemaVersion != version || result.SchemaState != SchemaBehind {
		t.Fatalf("restore misreported the schema: %d/%s", result.SchemaVersion, result.SchemaState)
	}
	if result.BinarySchemaVersion <= result.SchemaVersion {
		t.Fatalf("binary schema %d is not ahead of the restored %d",
			result.BinarySchemaVersion, result.SchemaVersion)
	}
	if !strings.Contains(result.NextStep, "--migrate-only") {
		t.Fatalf("restore did not say what to run next: %q", result.NextStep)
	}
	if !result.Source.Complete() || result.Source.InteractionIdentities.Rows == 0 {
		t.Fatalf("the source fingerprint is empty: %+v", result.Source)
	}
	if !backup.Fingerprint.InteractionIdentities.Equal(result.Target.InteractionIdentities) {
		t.Fatal("the manifest's fingerprint does not describe what was restored")
	}

	// And the rollback completes: the restored database migrates forward, and
	// every preservation property survives the migration it was rolled back
	// past.
	restored, err := Open(target)
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer func() { _ = restored.Close() }()

	before, err := TakeFingerprint(ctx, restored)
	if err != nil {
		t.Fatalf("fingerprint restored: %v", err)
	}
	if migrateErr := RunMigrations(restored); migrateErr != nil {
		t.Fatalf("re-migrate the restored database: %v", migrateErr)
	}
	after, err := TakeFingerprint(ctx, restored)
	if err != nil {
		t.Fatalf("fingerprint migrated: %v", err)
	}
	if after.SchemaVersion != result.BinarySchemaVersion {
		t.Fatalf("the re-migration landed on schema %d, want %d",
			after.SchemaVersion, result.BinarySchemaVersion)
	}
	if differences := before.Compare(after); len(differences) != 0 {
		t.Fatalf("the re-migration changed a preservation property: %+v", differences)
	}

	verification, err := VerifyDatabase(ctx, restored)
	if err != nil {
		t.Fatalf("verify migrated: %v", err)
	}
	if !verification.Healthy() || verification.SchemaState != SchemaCurrent {
		t.Fatalf("the migrated database is not current and healthy: %+v", verification)
	}
	var payload string
	if err := restored.QueryRowContext(ctx,
		`SELECT response_payload FROM resolutions WHERE id = 'res_int_resolved'`).Scan(&payload); err != nil {
		t.Fatalf("read restored resolution: %v", err)
	}
	if !strings.Contains(payload, "the participant's own words") {
		t.Fatal("the round trip through an older schema lost the participant's answer")
	}
}

// TestOlderIsDistinguishableFromDamaged is the assertion that keeps the two
// apart in the output. Before this, both arrived as "failed its own integrity
// check" and an operator had no way to tell a sound pre-upgrade backup from a
// broken one.
func TestOlderIsDistinguishableFromDamaged(t *testing.T) {
	version := backupSchemaVersion(t)
	ctx := context.Background()

	older := newFixtureAtSchema(t, version)
	backupPath := filepath.Join(t.TempDir(), "older.db")
	if _, err := Backup(ctx, older.db, older.path, backupPath, BackupOnline); err != nil {
		t.Fatalf("backup: %v", err)
	}

	sound, err := VerifyDatabaseFile(ctx, backupPath)
	if err != nil {
		t.Fatalf("verify older: %v", err)
	}
	if damage := sound.Damage(); len(damage) != 0 {
		t.Fatalf("an older database reported damage: %v", damage)
	}
	if !sound.Intact() || !sound.Healthy() {
		t.Fatalf("an older database is neither intact nor healthy: %+v", sound)
	}
	if sound.SchemaAdvice == "" {
		t.Fatal("an older database offers no advice about its schema")
	}

	t.Run("page corruption", func(t *testing.T) {
		corrupt := filepath.Join(t.TempDir(), "corrupt.db")
		raw, readErr := os.ReadFile(backupPath) //nolint:gosec // test-owned temp path
		if readErr != nil {
			t.Fatalf("read backup: %v", readErr)
		}
		if len(raw) < 24000 {
			t.Skipf("the backup is only %d bytes; the corruption offset is past its end", len(raw))
		}
		for i := 20000; i < 20400; i++ {
			raw[i] ^= 0xFF
		}
		//nolint:gosec // corrupt is a t.TempDir() path this test just built
		if writeErr := os.WriteFile(corrupt, raw, 0o600); writeErr != nil {
			t.Fatalf("write corrupt copy: %v", writeErr)
		}

		report, verifyErr := VerifyDatabaseFile(ctx, corrupt)
		if verifyErr != nil {
			t.Fatalf("verify corrupt: %v", verifyErr)
		}
		if report.Intact() {
			t.Fatal("a corrupt database reported intact")
		}
		damage := report.Damage()
		if len(damage) == 0 || !strings.Contains(damage[0], "page-level corruption") {
			t.Fatalf("corruption was not named as damage: %v", damage)
		}
		// The two conditions land in different fields, which is what makes
		// them distinguishable without reading prose.
		if report.SchemaState != SchemaBehind {
			t.Fatalf("schema state = %q; damage must not change the schema answer", report.SchemaState)
		}

		_, restoreErr := Restore(ctx, filepath.Join(t.TempDir(), "target.db"), corrupt)
		if restoreErr == nil {
			t.Fatal("restore accepted a corrupt backup")
		}
		if !strings.Contains(restoreErr.Error(), "integrity check") {
			t.Fatalf("the refusal does not name the damage: %v", restoreErr)
		}
		if strings.Contains(restoreErr.Error(), "--migrate-only") {
			t.Fatalf("a damaged backup was refused with a schema remedy: %v", restoreErr)
		}
	})

	t.Run("a guard the schema defines is missing", func(t *testing.T) {
		// The sharper case: a guard that migration 0003 creates, dropped from
		// a schema-N-2 database. Comparing against this binary's inventory
		// would have called the schema's own absent guards missing too and
		// buried this one; comparing against the source's schema names exactly
		// the one that went away.
		damaged := newFixtureAtSchema(t, version)
		if _, err := damaged.db.ExecContext(ctx,
			`DROP TRIGGER resolutions_immutable_delete;`); err != nil {
			t.Fatalf("drop guard: %v", err)
		}
		report, verifyErr := VerifyDatabase(ctx, damaged.db)
		if verifyErr != nil {
			t.Fatalf("verify: %v", verifyErr)
		}
		if report.Intact() {
			t.Fatal("a database missing an immutability guard reported intact")
		}
		if report.GuardReferenceVersion != version {
			t.Fatalf("the guards were compared against schema %d, not the database's %d",
				report.GuardReferenceVersion, version)
		}
		if len(report.GuardDrift.Missing) != 1 ||
			report.GuardDrift.Missing[0] != "resolutions_immutable_delete" {
			t.Fatalf("drift did not name exactly the dropped guard: %+v", report.GuardDrift)
		}
	})
}

// TestGuardReferenceIsBuiltAtTheDatabaseOwnSchema is the mechanism behind the
// distinction: the inventory a database is measured against is its own, not
// the binary's.
func TestGuardReferenceIsBuiltAtTheDatabaseOwnSchema(t *testing.T) {
	ctx := context.Background()
	version := lastGuardChangingSchemaVersion

	older, err := ReferenceGuardsAt(ctx, version)
	if err != nil {
		t.Fatalf("reference at %d: %v", version, err)
	}
	head, err := ReferenceGuards(ctx)
	if err != nil {
		t.Fatalf("reference at head: %v", err)
	}
	if len(older.Guards) >= len(head.Guards) {
		t.Fatalf("the reference at schema %d has %d guards and HEAD has %d; "+
			"if they are equal this test proves nothing",
			version, len(older.Guards), len(head.Guards))
	}
	// The guards HEAD has and schema N-2 does not are the ones the later
	// migrations create. Measuring an older database against HEAD reports
	// exactly these as missing, which is the false alarm this replaces.
	drift := CompareGuards(older, head)
	if len(drift.Missing) == 0 {
		t.Fatal("the two inventories do not differ in the direction this guards against")
	}

	f := newFixtureAtSchema(t, version)
	report, err := VerifyDatabase(ctx, f.db)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.GuardsEvaluated {
		t.Fatal("the guards were not evaluated against an older schema")
	}
	if !report.GuardDrift.Intact() {
		t.Fatalf("an untouched schema-%d database reported guard drift: %+v",
			version, report.GuardDrift)
	}

	// Version 0 is a database that has never been migrated. It defines no
	// guards, and saying so is not the same as failing.
	empty, err := ReferenceGuardsAt(ctx, 0)
	if err != nil {
		t.Fatalf("reference at 0: %v", err)
	}
	if len(empty.Guards) != 0 {
		t.Fatalf("an unmigrated schema defines %d guards", len(empty.Guards))
	}
	if _, err := ReferenceGuardsAt(ctx, 9999); err == nil {
		t.Fatal("a reference was produced for a schema this binary does not embed")
	}
}

// TestFingerprintIsMeasurableAtEverySchemaVersion walks every version this
// binary embeds, because the table gating in fingerprintSections is a claim
// about history and the only way to check a claim about history is to visit it.
//
// It is also the guard against the next version of this bug: a migration that
// adds a column to a fingerprint query without declaring where that column
// arrived fails here, at the version before it existed, rather than in
// production against somebody's pre-upgrade backup.
func TestFingerprintIsMeasurableAtEverySchemaVersion(t *testing.T) {
	ctx := context.Background()
	expected, err := ExpectedMigrationVersion()
	if err != nil {
		t.Fatalf("expected migration version: %v", err)
	}

	for version := int64(1); version <= expected; version++ {
		database, openErr := Open(":memory:")
		if openErr != nil {
			t.Fatalf("open in-memory database: %v", openErr)
		}
		if migrateErr := migrateTo(database, version); migrateErr != nil {
			_ = database.Close()
			t.Fatalf("migrate to %d: %v", version, migrateErr)
		}
		fingerprint, fingerprintErr := TakeFingerprint(ctx, database)
		if fingerprintErr != nil {
			_ = database.Close()
			t.Fatalf("fingerprint at schema %d: %v", version, fingerprintErr)
		}
		if fingerprint.SchemaVersion != version {
			_ = database.Close()
			t.Fatalf("fingerprint at schema %d reports %d", version, fingerprint.SchemaVersion)
		}
		// A section is applicable exactly when every table it reads exists.
		// Asserting against the catalog rather than against a hard-coded map
		// of version numbers keeps this true when a migration moves.
		present, tableErr := existingTables(ctx, database)
		if tableErr != nil {
			_ = database.Close()
			t.Fatalf("read catalog at %d: %v", version, tableErr)
		}
		for _, section := range fingerprintSections {
			want := hasAllTables(present, section.tables)
			if got := section.target(&fingerprint).Applicable; got != want {
				_ = database.Close()
				t.Fatalf("at schema %d, %s applicable = %v, want %v",
					version, section.name, got, want)
			}
		}
		if version == expected && !fingerprint.Complete() {
			_ = database.Close()
			t.Fatalf("at the current schema, properties were skipped: %v", fingerprint.Skipped())
		}
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("close in-memory database: %v", closeErr)
		}
	}
}

// TestAnInapplicablePropertyIsNotAMissingOne separates the two things a zero
// can mean. A schema with no delivery journals has nothing to preserve there;
// a schema that has them and lost the rows has everything to answer for.
func TestAnInapplicablePropertyIsNotAMissingOne(t *testing.T) {
	ctx := context.Background()

	// Migration 0005 creates delivery_events, which audit_history is measured
	// over, so schema 4 cannot carry that property.
	early, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = early.Close() }()
	if migrateErr := migrateTo(early, 4); migrateErr != nil {
		t.Fatalf("migrate to 4: %v", migrateErr)
	}
	fingerprint, err := TakeFingerprint(ctx, early)
	if err != nil {
		t.Fatalf("fingerprint at schema 4: %v", err)
	}
	if fingerprint.AuditHistory.Applicable {
		t.Fatal("audit_history reported applicable at a schema without delivery_events")
	}
	if skipped := fingerprint.Skipped(); len(skipped) != 1 || skipped[0] != "audit_history" {
		t.Fatalf("skipped = %v, want exactly [audit_history]", skipped)
	}

	// Two inapplicable measurements compare equal: a copy of that database
	// preserved exactly as much of the property as existed.
	if differences := fingerprint.Compare(fingerprint); len(differences) != 0 {
		t.Fatalf("a fingerprint differs from itself: %+v", differences)
	}

	// An applicable measurement and an inapplicable one never do, and the
	// difference says which kind it is.
	applicable := fingerprint
	applicable.AuditHistory = Property{Applicable: true, Rows: 0, SHA256: "e3b0c442"}
	differences := fingerprint.Compare(applicable)
	if len(differences) != 1 || differences[0].Property != "audit_history" {
		t.Fatalf("differences = %+v, want one for audit_history", differences)
	}
	if !strings.Contains(differences[0].Reason, "schemas differ") {
		t.Fatalf("the difference blames content rather than schema: %q", differences[0].Reason)
	}
}

// TestOlderSchemaAnswersAreLegibleNotDriverErrors covers the rest of the
// operator surface against a database older than the binary. None of these
// paths is damaged; each one has to say which schema it is on and what to do,
// rather than surfacing "no such table" from the driver.
func TestOlderSchemaAnswersAreLegibleNotDriverErrors(t *testing.T) {
	version := preRetentionOperationsSchemaVersion
	f := newFixtureAtSchema(t, version)
	ctx := context.Background()

	// The erasure log arrives with migration 0012, so a database behind it has
	// no log rather than a broken one.
	if _, err := RetentionHistory(ctx, f.db, 20); err == nil {
		t.Fatal("retention history read a table the schema does not have")
	} else {
		if strings.Contains(err.Error(), "no such table") {
			t.Fatalf("the driver's message reached the operator: %v", err)
		}
		if !strings.Contains(err.Error(), "--migrate-only") {
			t.Fatalf("the refusal does not say what to run: %v", err)
		}
	}

	// The MCP posture reports the schema state alongside the integrity counts,
	// so a reader can tell an old installation from a broken one.
	status, err := Status(ctx, f.db, f.path, DefaultRetentionWindows(), 20)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.SchemaState != SchemaBehind {
		t.Fatalf("status reports schema state %q, want %q", status.SchemaState, SchemaBehind)
	}
	if !status.GuardsIntact {
		t.Fatalf("an untouched older database reports its guards broken: %v", status.MissingGuards)
	}
	if status.IntegrityProblems != 0 || status.ForeignKeyViolations != 0 {
		t.Fatalf("an older database reports integrity problems: %+v", status)
	}

	// Repair refuses, and names the migration rather than a restore.
	report, err := Repair(ctx, f.db, "operator:test")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if len(report.Unfixable) != 1 || !strings.Contains(report.Unfixable[0], "--migrate-only") {
		t.Fatalf("repair did not send an older schema to a migration: %+v", report.Unfixable)
	}
}

// TestManifestDigestDescribesTheFileOnDisk is the check an operator performs
// when they want to know their backup is the one the manifest describes, and
// it failed for every backup ever taken until CW-20260905-0014.
//
// `VACUUM INTO` writes a rollback-journal database; verifying it means opening
// it, and opening it applies `PRAGMA journal_mode = WAL`, which rewrites the
// header. A digest taken before that describes a file that no longer exists.
func TestManifestDigestDescribesTheFileOnDisk(t *testing.T) {
	ctx := context.Background()
	for _, version := range []int64{0, backupSchemaVersion(t)} {
		f := newFixtureAt(t, version)
		path := filepath.Join(t.TempDir(), "digest.db")
		backup, err := Backup(ctx, f.db, f.path, path, BackupOnline)
		if err != nil {
			t.Fatalf("backup at schema %d: %v", version, err)
		}
		actual, digestErr := fileDigest(path)
		if digestErr != nil {
			t.Fatalf("digest the backup: %v", digestErr)
		}
		if backup.SHA256 != actual {
			t.Fatalf("the manifest describes a file that is not on disk: manifest %s, actual %s",
				backup.SHA256, actual)
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("stat the backup: %v", statErr)
		}
		if backup.SizeBytes != info.Size() {
			t.Fatalf("the manifest records %d bytes and the file is %d",
				backup.SizeBytes, info.Size())
		}
	}
}
