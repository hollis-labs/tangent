package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestDurableInteractionMigrationUpDownPreservesV012History(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "tangent.db")
	database, openErr := Open(databasePath)
	if openErr != nil {
		t.Fatalf("Open: %v", openErr)
	}
	t.Cleanup(func() { _ = database.Close() })

	if err := RunMigrations(database); err != nil {
		t.Fatalf("initial RunMigrations: %v", err)
	}
	rollbackToV012Schema(t, database)
	seedV012History(t, database)

	if err := RunMigrations(database); err != nil {
		t.Fatalf("upgrade existing database: %v", err)
	}

	var surfaceState string
	var nextSequence int64
	if err := database.QueryRow(`
SELECT lifecycle_state, next_interaction_sequence
FROM surfaces WHERE id = 'room-existing'`).Scan(&surfaceState, &nextSequence); err != nil {
		t.Fatalf("load migrated surface: %v", err)
	}
	if surfaceState != "active" || nextSequence != 3 {
		t.Fatalf("migrated surface = state %q next sequence %d, want active/3", surfaceState, nextSequence)
	}

	rows, err := database.Query(`
	SELECT legacy_envelope_id, surface_sequence, lifecycle_state,
	       terminal_error_code, terminal_reason
FROM interactions
WHERE legacy_room_id = 'room-existing'
ORDER BY surface_sequence`)
	if err != nil {
		t.Fatalf("query migrated interactions: %v", err)
	}
	defer func() { _ = rows.Close() }()
	type interactionRow struct {
		envelopeID string
		sequence   int64
		state      string
		errorCode  sql.NullString
		reason     sql.NullString
	}
	var migrated []interactionRow
	for rows.Next() {
		var row interactionRow
		if err := rows.Scan(&row.envelopeID, &row.sequence, &row.state, &row.errorCode, &row.reason); err != nil {
			t.Fatalf("scan migrated interaction: %v", err)
		}
		migrated = append(migrated, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migrated interactions: %v", err)
	}
	if len(migrated) != 2 ||
		migrated[0].envelopeID != "env-pending" || migrated[0].sequence != 1 || migrated[0].state != "failed" ||
		migrated[0].errorCode.String != "legacy_recovery_unavailable" || !migrated[0].reason.Valid ||
		migrated[1].envelopeID != "env-resolved" || migrated[1].sequence != 2 || migrated[1].state != "resolved" ||
		migrated[1].errorCode.Valid || migrated[1].reason.Valid {
		t.Fatalf("migrated interactions = %#v", migrated)
	}
	var legacyPendingStatus string
	if err := database.QueryRow(`
	SELECT status FROM legacy_room_history_v12
	WHERE room_id = 'room-existing' AND envelope_id = 'env-pending'`).Scan(&legacyPendingStatus); err != nil {
		t.Fatalf("read pending compatibility projection: %v", err)
	}
	if legacyPendingStatus != "pending" {
		t.Fatalf("pending compatibility status = %q, want pending", legacyPendingStatus)
	}

	var legacyStatus, canonicalSurfaceID, canonicalInteractionID string
	if err := database.QueryRow(`
SELECT status, surface_id, interaction_id
FROM legacy_room_history_v12
WHERE room_id = 'room-existing' AND envelope_id = 'env-resolved'`).Scan(
		&legacyStatus, &canonicalSurfaceID, &canonicalInteractionID,
	); err != nil {
		t.Fatalf("read compatibility projection: %v", err)
	}
	if legacyStatus != "submitted" || canonicalSurfaceID != "room-existing" ||
		canonicalInteractionID != "legacy:interaction:room-existing:env-resolved" {
		t.Fatalf("compatibility projection = %q %q %q", legacyStatus, canonicalSurfaceID, canonicalInteractionID)
	}

	var resolutionCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM resolutions`).Scan(&resolutionCount); err != nil {
		t.Fatalf("count migrated resolutions: %v", err)
	}
	if resolutionCount != 1 {
		t.Fatalf("resolution count = %d, want 1", resolutionCount)
	}

	rollbackToV012Schema(t, database)
	assertCount(t, database, `SELECT COUNT(*) FROM rooms WHERE id = 'room-existing'`, 1)
	assertCount(t, database, `SELECT COUNT(*) FROM envelopes WHERE room_id = 'room-existing'`, 2)
	if _, err := database.Exec(`SELECT 1 FROM surfaces LIMIT 1`); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("surfaces query after rollback error = %v, want no such table", err)
	}
}

func TestAsyncInteractionOperationsMigrationRollsBackIndependently(t *testing.T) {
	t.Parallel()
	database, err := Open(filepath.Join(t.TempDir(), "async.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if _, err := database.Exec(`SELECT 1 FROM surface_open_requests LIMIT 1`); err != nil {
		t.Fatalf("surface_open_requests after up: %v", err)
	}
	for name, query := range map[string]string{
		"interaction participant binding": `SELECT participant_scope, participant_authority, participant_assurance FROM interactions LIMIT 1`,
		"draft participant binding":       `SELECT participant_scope, participant_authority, participant_assurance FROM draft_revisions LIMIT 1`,
		"resolution participant scope":    `SELECT participant_scope FROM resolutions LIMIT 1`,
	} {
		if _, err := database.Exec(query); err != nil {
			t.Fatalf("%s after up: %v", name, err)
		}
	}
	rollbackTo(t, database, durableMigrationsAboveV012(t)-2)
	if _, err := database.Exec(`SELECT 1 FROM delivery_events LIMIT 1`); err == nil ||
		!strings.Contains(err.Error(), "no such table") {
		t.Fatalf("delivery_events query after rollback error = %v, want no such table", err)
	}
	if _, err := database.Exec(`SELECT 1 FROM surface_open_requests LIMIT 1`); err != nil {
		t.Fatalf("surface_open_requests should remain after rolling back only delivery lifecycle: %v", err)
	}
	if err := RollbackOne(database); err != nil {
		t.Fatalf("RollbackOne: %v", err)
	}
	if _, err := database.Exec(`SELECT 1 FROM surface_open_requests LIMIT 1`); err == nil ||
		!strings.Contains(err.Error(), "no such table") {
		t.Fatalf("surface_open_requests query after rollback error = %v, want no such table", err)
	}
	if _, err := database.Exec(`SELECT 1 FROM surfaces LIMIT 1`); err != nil {
		t.Fatalf("surfaces should remain after rolling back only async operations: %v", err)
	}
	if _, err := database.Exec(`SELECT participant_scope FROM interactions LIMIT 1`); err == nil ||
		!strings.Contains(err.Error(), "no such column") {
		t.Fatalf("participant binding column after rollback error = %v, want no such column", err)
	}
}

func TestDeliveryAttemptLifecycleMigrationBackfillsAndSealsOnce(t *testing.T) {
	t.Parallel()
	database, err := Open(filepath.Join(t.TempDir(), "delivery-lifecycle.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	rollbackTo(t, database, durableMigrationsAboveV012(t)-2)

	const instant = "2026-08-25T10:00:00Z"
	statements := []string{
		`INSERT INTO surfaces (
  id, owner_scope, lifecycle_state, metadata, policy,
  next_interaction_sequence, revision, created_at, updated_at
) VALUES ('surface-delivery-migration', 'operator:local', 'active', '{}', '{}', 2, 1, '` + instant + `', '` + instant + `')`,
		`INSERT INTO interactions (
  id, surface_id, caller_scope, caller_authority, caller_assurance,
  idempotency_key, surface_sequence, request_snapshot, external_refs, policy,
  lifecycle_state, revision, created_at, updated_at, terminal_at
) VALUES (
  'interaction-delivery-migration', 'surface-delivery-migration', 'application:test', 'direct', 'asserted',
  'delivery-migration', 1, '{}', '{}', '{}', 'resolved', 4,
  '` + instant + `', '` + instant + `', '` + instant + `'
)`,
		`INSERT INTO resolutions (
  id, interaction_id, expected_interaction_revision, presented_projection_revision,
  participant_ref, participant_authority, participant_assurance,
  response_kind, response_payload, integrity_digest, submitted_at, validated_at, recorded_at
) VALUES (
  'resolution-delivery-migration', 'interaction-delivery-migration', 3, 1,
  'local-operator', 'loopback-ui', 'loopback-unverified',
  'data', '{}', 'digest', '` + instant + `', '` + instant + `', '` + instant + `'
)`,
		`INSERT INTO resolution_deliveries (
  id, resolution_id, destination_binding, idempotency_key, policy,
  lifecycle_state, revision, lease_owner, lease_expires_at, created_at, updated_at
) VALUES (
  'delivery-migration', 'resolution-delivery-migration', '{"kind":"external"}', 'effect:1', '{}',
  'delivering', 2, 'dead-process', '2026-08-25T10:10:00Z', '` + instant + `', '` + instant + `'
)`,
	}
	for index, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("seed statement %d: %v", index, err)
		}
	}

	if err := RunMigrations(database); err != nil {
		t.Fatalf("apply delivery lifecycle migration: %v", err)
	}
	var attemptID, status string
	var attemptNumber int64
	var completedAt sql.NullTime
	if err := database.QueryRow(`
SELECT id, attempt_number, status, completed_at
FROM delivery_attempts
WHERE resolution_delivery_id = 'delivery-migration'`).Scan(
		&attemptID, &attemptNumber, &status, &completedAt,
	); err != nil {
		t.Fatalf("read backfilled attempt: %v", err)
	}
	if attemptID != "migration:0005:resolution:delivery-migration" || attemptNumber != 1 ||
		status != "delivering" || completedAt.Valid {
		t.Fatalf("backfilled attempt = %q %d %q %#v", attemptID, attemptNumber, status, completedAt)
	}
	assertCount(t, database, `SELECT COUNT(*) FROM delivery_events
WHERE resolution_delivery_id = 'delivery-migration' AND event_type = 'delivery.queued'`, 1)
	assertCount(t, database, `SELECT COUNT(*) FROM delivery_events
WHERE resolution_delivery_id = 'delivery-migration' AND event_type = 'delivery.started'`, 1)

	if _, err := database.Exec(`
UPDATE delivery_attempts
SET status = 'delivered', receipt = '{}', completed_at = '2026-08-25T10:02:00Z'
WHERE id = ?`, attemptID); err != nil {
		t.Fatalf("seal backfilled attempt: %v", err)
	}
	if _, err := database.Exec(`UPDATE delivery_attempts SET receipt = '{"changed":true}' WHERE id = ?`, attemptID); err == nil ||
		!strings.Contains(err.Error(), "may only be sealed once") {
		t.Fatalf("second attempt update error = %v, want immutable seal error", err)
	}

	rollbackTo(t, database, durableMigrationsAboveV012(t)-2)
	if _, err := database.Exec(`SELECT 1 FROM delivery_events LIMIT 1`); err == nil ||
		!strings.Contains(err.Error(), "no such table") {
		t.Fatalf("delivery_events after rollback error = %v, want no such table", err)
	}
	if _, err := database.Exec(`UPDATE delivery_attempts SET receipt = '{"changed":true}' WHERE id = ?`, attemptID); err == nil ||
		!strings.Contains(err.Error(), "immutable") {
		t.Fatalf("sealed attempt after rollback error = %v, want immutable", err)
	}
}

func TestLegacyRestartTimeoutMigrationRemainsPredictable(t *testing.T) {
	t.Parallel()
	database, err := Open(filepath.Join(t.TempDir(), "legacy-timeout.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	rollbackToV012Schema(t, database)
	if _, err := database.Exec(`
INSERT INTO rooms (id, meta, created_at, updated_at)
VALUES ('room-timeout', '{}', '2026-08-20T10:00:00Z', '2026-08-20T10:01:00Z')`); err != nil {
		t.Fatalf("insert legacy room: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO envelopes (
  room_id, envelope_id, type, request_payload, response_kind, response_payload,
  status, error_code, error_message, created_at, resolved_at
) VALUES (
  'room-timeout', 'env-timeout', 'tangent.triage', '{"prompt":"old"}',
  'error', '{"code":"SERVER_RESTART"}', 'timeout', 'SERVER_RESTART',
  'pending envelope timed out after server restart',
  '2026-08-20T10:00:30Z', '2026-08-20T10:01:00Z'
)`); err != nil {
		t.Fatalf("insert legacy timeout envelope: %v", err)
	}
	if err := RunMigrations(database); err != nil {
		t.Fatalf("upgrade legacy timeout: %v", err)
	}
	var state, errorCode, reason string
	if err := database.QueryRow(`
SELECT lifecycle_state, terminal_error_code, terminal_reason
FROM interactions WHERE legacy_room_id = 'room-timeout' AND legacy_envelope_id = 'env-timeout'`).Scan(
		&state,
		&errorCode,
		&reason,
	); err != nil {
		t.Fatalf("read canonical timeout: %v", err)
	}
	if state != "failed" || errorCode != "SERVER_RESTART" ||
		reason != "pending envelope timed out after server restart" {
		t.Fatalf("canonical timeout = %q %q %q", state, errorCode, reason)
	}
	var projectedStatus, projectedCode string
	if err := database.QueryRow(`
SELECT status, error_code FROM legacy_room_history_v12
WHERE room_id = 'room-timeout' AND envelope_id = 'env-timeout'`).Scan(
		&projectedStatus,
		&projectedCode,
	); err != nil {
		t.Fatalf("read timeout compatibility projection: %v", err)
	}
	if projectedStatus != "timeout" || projectedCode != "SERVER_RESTART" {
		t.Fatalf("timeout projection = %q %q", projectedStatus, projectedCode)
	}
}

func seedV012History(t *testing.T, database *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `
INSERT INTO rooms (
  id, title, meta, created_at, updated_at,
  current_phase, phases_visited, phase_outputs
) VALUES (
  'room-existing', 'Existing room', '{"title":"Existing room"}',
  '2026-08-20T10:00:00Z', '2026-08-20T10:02:00Z',
  'drafting', '["drafting"]', '{}'
)`); err != nil {
		t.Fatalf("insert v0.12 room: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO envelopes (
  room_id, envelope_id, type, request_payload, status, created_at
) VALUES (
	  'room-existing', 'env-pending', 'vendor.unknown-interaction',
  '{"prompt":"pending"}', 'pending', '2026-08-20T10:00:30Z'
)`); err != nil {
		t.Fatalf("insert pending v0.12 envelope: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO envelopes (
  room_id, envelope_id, type, request_payload,
  response_kind, response_payload, status, created_at, resolved_at
) VALUES (
  'room-existing', 'env-resolved', 'tangent.feedback',
  '{"prompt":"resolved"}', 'data', '{"answer":"yes"}',
  'submitted', '2026-08-20T10:01:00Z', '2026-08-20T10:02:00Z'
)`); err != nil {
		t.Fatalf("insert resolved v0.12 envelope: %v", err)
	}
}

func assertCount(t *testing.T, database *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := database.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("query count: %v", err)
	}
	if got != want {
		t.Fatalf("count = %d, want %d", got, want)
	}
}

// durableMigrationsAboveV012 counts the migrations layered on top of the v0.12
// schema (0001_init + 0002_room_phase_state). Tests roll back exactly this
// many to reach the pre-durable schema, so adding a migration does not require
// hand-editing every rollback loop.
func durableMigrationsAboveV012(t *testing.T) int {
	t.Helper()
	entries, err := migrationsFS.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".up.sql") {
			count++
		}
	}
	return count - 2
}

func rollbackToV012Schema(t *testing.T, database *sql.DB) {
	t.Helper()
	for range durableMigrationsAboveV012(t) {
		if err := RollbackOne(database); err != nil {
			t.Fatalf("rollback to v0.12 schema: %v", err)
		}
	}
}

// rollbackTo rolls back every migration applied after the named one, leaving
// that migration as the newest applied version.
func rollbackTo(t *testing.T, database *sql.DB, migrationsAfter int) {
	t.Helper()
	for range migrationsAfter {
		if err := RollbackOne(database); err != nil {
			t.Fatalf("rollback %d migrations: %v", migrationsAfter, err)
		}
	}
}

// TestRoomWorkflowCompletionMigrationAddsAcknowledgementAndProjectionState
// covers migration 0006: the caller-acknowledgement fact is immutable, the
// legacy compatibility projection reports the canonical interaction state
// beside the legacy row, and the whole thing rolls back cleanly to the
// previous schema.
func TestRoomWorkflowCompletionMigrationAddsAcknowledgementAndProjectionState(t *testing.T) {
	t.Parallel()
	database, err := Open(filepath.Join(t.TempDir(), "room-workflow-completion.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	const instant = "2026-09-04T12:57:17Z"
	statements := []string{
		`INSERT INTO rooms (id, meta, created_at, updated_at)
VALUES ('room-completion', '{}', '` + instant + `', '` + instant + `')`,
		`INSERT INTO envelopes (
  room_id, envelope_id, type, request_payload, status, created_at
) VALUES ('room-completion', 'env-completion', 'tangent.approval-queue', '{}', 'pending', '` + instant + `')`,
		`INSERT INTO surfaces (
  id, owner_scope, lifecycle_state, metadata, policy,
  next_interaction_sequence, revision, created_at, updated_at, legacy_room_id
) VALUES ('room-completion', 'standalone-local', 'active', '{}', '{}', 2, 2, '` + instant + `', '` + instant + `', 'room-completion')`,
		`INSERT INTO interactions (
  id, surface_id, caller_scope, caller_authority, caller_assurance,
  idempotency_key, surface_sequence, request_snapshot, external_refs, policy,
  lifecycle_state, revision, created_at, updated_at, terminal_at,
  legacy_room_id, legacy_envelope_id
) VALUES (
  'interaction-completion', 'room-completion', 'standalone-local', 'direct-mcp', 'unverified',
  'workflow:tangent.approval-queue:env-completion', 1, '{}', '{}', '{}',
  'resolved', 4, '` + instant + `', '` + instant + `', '` + instant + `',
  'room-completion', 'env-completion'
)`,
		`INSERT INTO definition_bindings (
  interaction_id, publisher, kind, version, revision, source, assurance, bound_at
) VALUES (
  'interaction-completion', 'hollis-labs/tangent', 'tangent.approval-queue', '0.4', '0.4',
  'plugin', 'content-addressed-registry', '` + instant + `'
)`,
		`INSERT INTO resolutions (
  id, interaction_id, expected_interaction_revision, presented_projection_revision,
  participant_ref, participant_authority, participant_assurance,
  response_kind, response_payload, integrity_digest, submitted_at, validated_at, recorded_at
) VALUES (
  'resolution-completion', 'interaction-completion', 3, 1,
  'local-operator', 'tangent-loopback', 'loopback-unverified',
  'data', '{}', 'digest', '` + instant + `', '` + instant + `', '` + instant + `'
)`,
	}
	for index, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("seed statement %d: %v", index, err)
		}
	}

	// The legacy row still reads "pending" while the canonical record is
	// resolved. The projection now says both, which is exactly the point: the
	// legacy row is a view, not an authority.
	var legacyStatus, interactionState string
	if err := database.QueryRow(`
SELECT status, interaction_state FROM legacy_room_history_v12
WHERE room_id = 'room-completion' AND envelope_id = 'env-completion'`).Scan(
		&legacyStatus, &interactionState,
	); err != nil {
		t.Fatalf("read compatibility projection: %v", err)
	}
	if legacyStatus != "pending" || interactionState != "resolved" {
		t.Fatalf("projection = legacy %q canonical %q", legacyStatus, interactionState)
	}

	if _, err := database.Exec(`
INSERT INTO terminal_outcome_acknowledgements (
  interaction_id, acknowledgement_id, resolution_id, requester_scope,
  transport_correlation, acknowledged_at
) VALUES (
  'interaction-completion', 'ack-1', 'resolution-completion', 'standalone-local',
  '{"transport":"mcp"}', '` + instant + `'
)`); err != nil {
		t.Fatalf("insert acknowledgement: %v", err)
	}
	if _, err := database.Exec(`
UPDATE terminal_outcome_acknowledgements SET requester_scope = 'rewritten'
WHERE interaction_id = 'interaction-completion'`); err == nil ||
		!strings.Contains(err.Error(), "immutable") {
		t.Fatalf("acknowledgement update error = %v, want immutable", err)
	}
	if _, err := database.Exec(`
DELETE FROM terminal_outcome_acknowledgements WHERE interaction_id = 'interaction-completion'`); err == nil ||
		!strings.Contains(err.Error(), "immutable") {
		t.Fatalf("acknowledgement delete error = %v, want immutable", err)
	}
	var acknowledgedAt sql.NullString
	if err := database.QueryRow(`
SELECT caller_acknowledged_at FROM legacy_room_history_v12
WHERE room_id = 'room-completion' AND envelope_id = 'env-completion'`).Scan(&acknowledgedAt); err != nil {
		t.Fatalf("read projected acknowledgement: %v", err)
	}
	if !acknowledgedAt.Valid {
		t.Fatal("projection did not surface the caller acknowledgement")
	}

	// Every migration above 0006 has to come off before 0006 itself can —
	// 0007 (the definition registry), 0008 (participant sessions), 0009 (effect
	// handles and receipts), 0010 (renderer trust classes on the receipt),
	// 0011 (interaction telemetry), and whatever lands next. The count is
	// derived from the embedded set rather than written down, so adding a
	// migration does not silently turn this assertion into a different one.
	rollbackTo(t, database, durableMigrationsAboveV012(t)-3)
	if _, err := database.Exec(`SELECT 1 FROM terminal_outcome_acknowledgements LIMIT 1`); err == nil ||
		!strings.Contains(err.Error(), "no such table") {
		t.Fatalf("acknowledgements after rollback = %v, want no such table", err)
	}
	if _, err := database.Exec(`SELECT interaction_state FROM legacy_room_history_v12 LIMIT 1`); err == nil ||
		!strings.Contains(err.Error(), "no such column") {
		t.Fatalf("projection column after rollback = %v, want no such column", err)
	}
	if _, err := database.Exec(`SELECT surface_id FROM legacy_room_history_v12 LIMIT 1`); err != nil {
		t.Fatalf("previous projection did not come back: %v", err)
	}
}

// TestExpectedMigrationVersionTracksTheEmbeddedTree holds the derivation
// honest: the expected version comes from the embedded migrations rather than
// a constant, so adding a migration cannot forget to update it.
func TestExpectedMigrationVersionTracksTheEmbeddedTree(t *testing.T) {
	t.Parallel()

	expected, err := ExpectedMigrationVersion()
	if err != nil {
		t.Fatalf("ExpectedMigrationVersion: %v", err)
	}
	entries, err := migrationsFS.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	var ups int
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".up.sql") {
			ups++
		}
	}
	if int(expected) != ups {
		t.Fatalf("expected version %d but the tree holds %d up-migrations; the numbering has a "+
			"gap or a duplicate, and a readiness probe would report the wrong target", expected, ups)
	}
}

// TestInspectMigrationsDistinguishesTheDegradedSchemaStates covers what a
// readiness probe has to be able to tell apart. Each state is constructed for
// real — migrated, rolled back, marked dirty — rather than described.
func TestInspectMigrationsDistinguishesTheDegradedSchemaStates(t *testing.T) {
	t.Parallel()

	expected, err := ExpectedMigrationVersion()
	if err != nil {
		t.Fatalf("ExpectedMigrationVersion: %v", err)
	}
	database, openErr := Open(filepath.Join(t.TempDir(), "inspect.db"))
	if openErr != nil {
		t.Fatalf("Open: %v", openErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()

	// Never migrated: the bookkeeping table does not exist. That is a state to
	// describe, not an error to return.
	status, err := InspectMigrations(ctx, database)
	if err != nil {
		t.Fatalf("InspectMigrations on a fresh database: %v", err)
	}
	if status.Initialized || status.UpToDate() || status.Expected != expected {
		t.Fatalf("fresh database status = %+v, want uninitialized at expected %d", status, expected)
	}

	if migrateErr := RunMigrations(database); migrateErr != nil {
		t.Fatalf("RunMigrations: %v", migrateErr)
	}
	status, err = InspectMigrations(ctx, database)
	if err != nil {
		t.Fatalf("InspectMigrations after migrate: %v", err)
	}
	if !status.UpToDate() || status.Applied != expected {
		t.Fatalf("migrated status = %+v, want up to date at %d", status, expected)
	}

	if rollbackErr := RollbackOne(database); rollbackErr != nil {
		t.Fatalf("RollbackOne: %v", rollbackErr)
	}
	status, err = InspectMigrations(ctx, database)
	if err != nil {
		t.Fatalf("InspectMigrations after rollback: %v", err)
	}
	if status.UpToDate() || status.Applied != expected-1 {
		t.Fatalf("rolled-back status = %+v, want applied %d and not up to date", status, expected-1)
	}

	if _, dirtyErr := database.Exec(`UPDATE schema_migrations SET dirty = 1;`); dirtyErr != nil {
		t.Fatalf("mark dirty: %v", dirtyErr)
	}
	status, err = InspectMigrations(ctx, database)
	if err != nil {
		t.Fatalf("InspectMigrations on a dirty schema: %v", err)
	}
	if !status.Dirty || status.UpToDate() {
		t.Fatalf("dirty status = %+v, want dirty and not up to date", status)
	}
}

// TestInspectMigrationsFailsLoudlyOnAClosedDatabase: "cannot be read" is a
// different answer from "has never been migrated", and collapsing them would
// let a readiness probe recommend running a migration against a database that
// is not there.
func TestInspectMigrationsFailsLoudlyOnAClosedDatabase(t *testing.T) {
	t.Parallel()

	database, openErr := Open(filepath.Join(t.TempDir(), "closed.db"))
	if openErr != nil {
		t.Fatalf("Open: %v", openErr)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := InspectMigrations(context.Background(), database); err == nil {
		t.Fatal("InspectMigrations reported a schema state for a closed database")
	}
	if _, err := InspectMigrations(context.Background(), nil); err == nil {
		t.Fatal("InspectMigrations reported a schema state for a nil handle")
	}
}

// TestChannelsAndParticipantBindingsMigrationRollsBackIndependently proves
// the CW-20260906-0064 migration is additive and self-contained: it adds
// exactly six tables, none of them touch the existing surface/interaction
// substrate's rows or shape, and RollbackOne removes only what this
// migration added.
func TestChannelsAndParticipantBindingsMigrationRollsBackIndependently(t *testing.T) {
	t.Parallel()

	database, err := Open(filepath.Join(t.TempDir(), "channels.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	// migrateTo(13), not RunMigrations: this test's RollbackOne below has to
	// undo exactly migration 0013, and RunMigrations now lands on whatever
	// is newest — 0014 (CW-20260906-0065) stacks on top of this one — so
	// "the newest applied migration" stopped meaning "this one" the moment
	// a migration was added after it.
	if err := migrateTo(database, 13); err != nil {
		t.Fatalf("migrate to version 13: %v", err)
	}

	newTables := []string{
		"channels",
		"channel_subjects",
		"participants",
		"channel_participants",
		"channel_participant_bindings",
		"channel_view_focus",
	}
	for _, table := range newTables {
		if err := probeTableExists(database, table); err != nil {
			t.Fatalf("%s after up: %v", table, err)
		}
	}

	// Existing surface authority is unaffected: a surface and an interaction
	// seeded before this migration existed still read the same after it.
	now := "2026-09-07T00:00:00Z"
	if _, err := database.Exec(`
INSERT INTO surfaces (id, owner_scope, lifecycle_state, created_at, updated_at)
VALUES ('surface-existing', 'standalone-local:anonymous', 'active', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed surface: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO interactions (
  id, surface_id, caller_scope, caller_authority, caller_assurance,
  idempotency_key, surface_sequence, request_snapshot, lifecycle_state,
  created_at, updated_at
) VALUES (
  'interaction-existing', 'surface-existing', 'standalone-local:anonymous',
  'standalone-local', 'loopback-unverified', 'idem-existing', 1, '{}',
  'submitted', ?, ?
)`, now, now); err != nil {
		t.Fatalf("seed interaction: %v", err)
	}

	// A channel subject may associate with that canonical interaction without
	// altering it.
	if _, err := database.Exec(`
INSERT INTO channels (id, owner_scope, created_at, updated_at)
VALUES ('channel-1', 'standalone-local:anonymous', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO channel_subjects (id, channel_id, subject_type, interaction_id, created_at, updated_at)
VALUES ('subject-1', 'channel-1', 'interaction', 'interaction-existing', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed channel subject: %v", err)
	}

	var interactionState string
	if err := database.QueryRow(`SELECT lifecycle_state FROM interactions WHERE id = 'interaction-existing'`).
		Scan(&interactionState); err != nil {
		t.Fatalf("read seeded interaction: %v", err)
	}
	if interactionState != "submitted" {
		t.Fatalf("seeded interaction lifecycle_state = %q, want submitted (unaffected by the channel association)", interactionState)
	}

	if err := RollbackOne(database); err != nil {
		t.Fatalf("RollbackOne: %v", err)
	}

	for _, table := range newTables {
		if err := probeTableExists(database, table); err == nil || !strings.Contains(err.Error(), "no such table") {
			t.Fatalf("%s query after rollback error = %v, want no such table", table, err)
		}
	}

	// The rollback removed the channel substrate but left the pre-existing
	// surface/interaction rows exactly as they were: existing surface
	// authority is not a casualty of an additive rollback.
	if err := database.QueryRow(`SELECT lifecycle_state FROM interactions WHERE id = 'interaction-existing'`).
		Scan(&interactionState); err != nil {
		t.Fatalf("read interaction after rollback: %v", err)
	}
	if interactionState != "submitted" {
		t.Fatalf("interaction lifecycle_state after rollback = %q, want submitted", interactionState)
	}
	var survivingSurfaces int
	if err := database.QueryRow(`SELECT COUNT(*) FROM surfaces WHERE id = 'surface-existing'`).
		Scan(&survivingSurfaces); err != nil {
		t.Fatalf("count surviving surface: %v", err)
	}
	if survivingSurfaces != 1 {
		t.Fatalf("surface row count after rolling back only the channel substrate = %d, want 1 (the row itself, not just the table)", survivingSurfaces)
	}

	// Re-applying brings the schema back to current, proving up is idempotent
	// after a rollback-one round trip.
	if err := RunMigrations(database); err != nil {
		t.Fatalf("re-apply after rollback: %v", err)
	}
	for _, table := range newTables {
		if err := probeTableExists(database, table); err != nil {
			t.Fatalf("%s after re-apply: %v", table, err)
		}
	}
}

// TestRelayJournalAndOutboxMigrationRollsBackIndependently proves the
// CW-20260906-0065 migration is additive and self-contained the same way
// TestChannelsAndParticipantBindingsMigrationRollsBackIndependently proved
// it for 0013: it adds exactly four tables, none of them alter the existing
// channel/participant substrate's rows or shape, and RollbackOne removes
// only what this migration added.
func TestRelayJournalAndOutboxMigrationRollsBackIndependently(t *testing.T) {
	t.Parallel()

	database, err := Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	// migrateTo(14), not RunMigrations: RollbackOne below has to undo
	// exactly this migration, and RunMigrations lands on whatever is
	// newest — the same fragility fixed on the 0013 test above once this
	// migration stopped being the last one.
	if err := migrateTo(database, 14); err != nil {
		t.Fatalf("migrate to version 14: %v", err)
	}

	newTables := []string{
		"exchanges",
		"exchange_outbox",
		"exchange_delivery_receipts",
		"exchange_reads",
	}
	for _, table := range newTables {
		if err := probeTableExists(database, table); err != nil {
			t.Fatalf("%s after up: %v", table, err)
		}
	}

	now := "2026-09-07T00:00:00Z"
	if _, err := database.Exec(`
INSERT INTO channels (id, owner_scope, created_at, updated_at)
VALUES ('ch1', 'standalone-local:anonymous', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	for _, id := range []string{"p_sender", "p_recipient"} {
		if _, err := database.Exec(`
INSERT INTO participants (id, kind, created_at, updated_at)
VALUES (?, 'agent', ?, ?)`, id, now, now); err != nil {
			t.Fatalf("seed participant %s: %v", id, err)
		}
	}
	if _, err := database.Exec(`
INSERT INTO exchanges (id, idempotency_key, channel_id, sender_participant_id, recipient_participant_id, body, sequence, created_at)
VALUES ('ex1', 'idem-1', 'ch1', 'p_sender', 'p_recipient', 'hello', 1, ?)`, now); err != nil {
		t.Fatalf("seed exchange: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO exchange_outbox (exchange_id, created_at, updated_at) VALUES ('ex1', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	var channelTitle sql.NullString
	if err := database.QueryRow(`SELECT title FROM channels WHERE id = 'ch1'`).Scan(&channelTitle); err != nil {
		t.Fatalf("read seeded channel: %v", err)
	}

	if err := RollbackOne(database); err != nil {
		t.Fatalf("RollbackOne: %v", err)
	}

	for _, table := range newTables {
		if err := probeTableExists(database, table); err == nil || !strings.Contains(err.Error(), "no such table") {
			t.Fatalf("%s query after rollback error = %v, want no such table", table, err)
		}
	}

	// The rollback removed the relay substrate but left the channel and
	// participant rows exactly as 0064 left them. Counting the matching row,
	// not just running the SELECT: Exec succeeds on zero rows too, so it
	// would prove the table still exists, never that this row survived.
	var survivingChannels, survivingParticipants int
	if err := database.QueryRow(`SELECT COUNT(*) FROM channels WHERE id = 'ch1'`).Scan(&survivingChannels); err != nil {
		t.Fatalf("count surviving channel: %v", err)
	}
	if survivingChannels != 1 {
		t.Fatalf("channel row count after rolling back only the relay substrate = %d, want 1", survivingChannels)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM participants WHERE id = 'p_sender'`).Scan(&survivingParticipants); err != nil {
		t.Fatalf("count surviving participant: %v", err)
	}
	if survivingParticipants != 1 {
		t.Fatalf("participant row count after rolling back only the relay substrate = %d, want 1", survivingParticipants)
	}

	if err := RunMigrations(database); err != nil {
		t.Fatalf("re-apply after rollback: %v", err)
	}
	for _, table := range newTables {
		if err := probeTableExists(database, table); err != nil {
			t.Fatalf("%s after re-apply: %v", table, err)
		}
	}
}

// TestParticipantPresenceMigrationRollsBackIndependently proves the
// CW-20260906-0066 migration is additive and self-contained: it adds
// exactly one table, does not alter participants' shape, and RollbackOne
// removes only what this migration added.
func TestParticipantPresenceMigrationRollsBackIndependently(t *testing.T) {
	t.Parallel()

	database, err := Open(filepath.Join(t.TempDir(), "presence.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := migrateTo(database, 15); err != nil {
		t.Fatalf("migrate to version 15: %v", err)
	}

	if err := probeTableExists(database, "participant_presence"); err != nil {
		t.Fatalf("participant_presence after up: %v", err)
	}

	now := "2026-09-07T00:00:00Z"
	if _, err := database.Exec(`
INSERT INTO participants (id, kind, created_at, updated_at) VALUES ('p1', 'agent', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed participant: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO participant_presence (participant_id, last_seen_at, updated_at) VALUES ('p1', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed presence: %v", err)
	}

	if err := RollbackOne(database); err != nil {
		t.Fatalf("RollbackOne: %v", err)
	}
	if err := probeTableExists(database, "participant_presence"); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("participant_presence query after rollback error = %v, want no such table", err)
	}
	var survivingParticipants int
	if err := database.QueryRow(`SELECT COUNT(*) FROM participants WHERE id = 'p1'`).Scan(&survivingParticipants); err != nil {
		t.Fatalf("count surviving participant: %v", err)
	}
	if survivingParticipants != 1 {
		t.Fatalf("participant row count after rolling back only presence = %d, want 1", survivingParticipants)
	}

	if err := RunMigrations(database); err != nil {
		t.Fatalf("re-apply after rollback: %v", err)
	}
	if err := probeTableExists(database, "participant_presence"); err != nil {
		t.Fatalf("participant_presence after re-apply: %v", err)
	}
}

// TestRetentionOperationsRelayTargetsMigrationRollsBackIndependently proves
// migration 0016 only widens retention_operations (two new columns, two new
// indexes) and that RollbackOne narrows it back without touching a row this
// migration did not write.
func TestRetentionOperationsRelayTargetsMigrationRollsBackIndependently(t *testing.T) {
	t.Parallel()

	database, err := Open(filepath.Join(t.TempDir(), "retention-relay-targets.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := migrateTo(database, 16); err != nil {
		t.Fatalf("migrate to version 16: %v", err)
	}

	if err := probeColumnExists(database, "retention_operations", "target_channel_id"); err != nil {
		t.Fatalf("target_channel_id after up: %v", err)
	}
	if err := probeColumnExists(database, "retention_operations", "target_exchange_id"); err != nil {
		t.Fatalf("target_exchange_id after up: %v", err)
	}

	now := "2026-09-07T00:00:00Z"
	if _, err := database.Exec(`
INSERT INTO retention_operations (
  operation_id, kind, target_channel_id, target_exchange_id,
  actor_ref, authority, requested_at, completed_at, outcome
) VALUES ('op1', 'payload-redaction', 'ch1', 'ex1', 'operator:test', 'local-user', ?, ?, 'applied')`,
		now, now); err != nil {
		t.Fatalf("seed retention operation: %v", err)
	}

	if err := RollbackOne(database); err != nil {
		t.Fatalf("RollbackOne: %v", err)
	}
	if err := probeColumnExists(database, "retention_operations", "target_channel_id"); err == nil ||
		!strings.Contains(err.Error(), "no such column") {
		t.Fatalf("target_channel_id query after rollback error = %v, want no such column", err)
	}
	var survivingOperations int
	if err := database.QueryRow(`SELECT COUNT(*) FROM retention_operations WHERE operation_id = 'op1'`).
		Scan(&survivingOperations); err != nil {
		t.Fatalf("count surviving operation: %v", err)
	}
	if survivingOperations != 1 {
		t.Fatalf("retention_operations row count after rolling back only its relay columns = %d, want 1",
			survivingOperations)
	}

	if err := RunMigrations(database); err != nil {
		t.Fatalf("re-apply after rollback: %v", err)
	}
	if err := probeColumnExists(database, "retention_operations", "target_channel_id"); err != nil {
		t.Fatalf("target_channel_id after re-apply: %v", err)
	}
	// A row written before the rollback comes back with the empty-string
	// default, the same trade migration 0010's own rollback makes — reading
	// as "not recorded" is honest for a column that briefly did not exist.
	var channelID string
	if err := database.QueryRow(`SELECT target_channel_id FROM retention_operations WHERE operation_id = 'op1'`).
		Scan(&channelID); err != nil {
		t.Fatalf("read target_channel_id after re-apply: %v", err)
	}
	if channelID != "" {
		t.Fatalf("target_channel_id after re-apply = %q, want empty string for a pre-rollback row", channelID)
	}
}

// probeColumnExists runs a trivial SELECT against a fixed, internally-listed
// table and column name. Neither is ever caller-supplied.
//
//nolint:gosec // table and column are always this file's own literal names
func probeColumnExists(database *sql.DB, table, column string) error {
	_, err := database.Exec(`SELECT ` + column + ` FROM ` + table + ` LIMIT 1`)
	return err
}

// probeTableExists runs a trivial SELECT against a fixed, internally-listed
// table name. The name is never caller-supplied, so string concatenation
// here carries none of the injection risk gosec's G202 checks for; SQLite
// also has no placeholder syntax for identifiers.
//
//nolint:gosec // table is always one of this file's own literal table-name lists
func probeTableExists(database *sql.DB, table string) error {
	_, err := database.Exec(`SELECT 1 FROM ` + table + ` LIMIT 1`)
	return err
}

// TestUSEnglishCanceledErrorCodeMigrationRewritesAndRollsBack pins migration
// 0017 against the two columns it rewrites and the three it deliberately
// leaves alone.
//
// The three it leaves alone are the point. `envelopes.status` holds Tangent's
// own vocabulary, which only coincidentally spells cancellation the British
// way; `interactions.policy` holds 0003's archival record of what a v0.12
// row's code WAS; and `interactions.terminal_error_code` belongs to a record
// the schema guards as immutable. A migration that swept any of them would
// look like it had done a more thorough job while actually rewriting history
// it does not own.
func TestUSEnglishCanceledErrorCodeMigrationRewritesAndRollsBack(t *testing.T) {
	t.Parallel()

	database, err := Open(filepath.Join(t.TempDir(), "us-english-canceled.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := migrateTo(database, 16); err != nil {
		t.Fatalf("migrate to version 16: %v", err)
	}

	const ts = "2026-09-10T00:00:00Z"
	if _, err := database.Exec(
		`INSERT INTO rooms (id, title, created_at, updated_at) VALUES ('room-1', 'legacy', ?, ?)`,
		ts, ts); err != nil {
		t.Fatalf("seed room: %v", err)
	}
	// A cancelled envelope as the pre-migration code wrote one: Tangent's own
	// status vocabulary in `status`, the protocol's British code in
	// `error_code`, and a second copy of that code inside the error body.
	if _, err := database.Exec(`
INSERT INTO envelopes (
  room_id, envelope_id, type, request_payload,
  response_kind, response_payload, status, error_code, error_message,
  created_at, resolved_at
) VALUES (
  'room-1', 'env-1', 'triage', '{}',
  'error', '{"code":"user-cancelled","message":"context canceled"}',
  'cancelled', 'user-cancelled', 'context canceled', ?, ?)`, ts, ts); err != nil {
		t.Fatalf("seed legacy envelope: %v", err)
	}
	// The shape migration 0003's backfill produces when it copies a status
	// 'error' / code 'user-cancelled' envelope forward: lifecycle_state
	// 'failed', the code in terminal_error_code, and the legacy provenance
	// blob in `policy`.
	if _, err := database.Exec(
		`INSERT INTO surfaces (id, owner_scope, lifecycle_state, created_at, updated_at, legacy_room_id)
		 VALUES ('surface-1', 'standalone-local', 'active', ?, ?, 'room-1')`, ts, ts); err != nil {
		t.Fatalf("seed surface: %v", err)
	}
	if _, err := database.Exec(`
INSERT INTO interactions (
  id, surface_id, caller_scope, caller_authority, caller_assurance,
  idempotency_key, surface_sequence, request_snapshot, policy,
  lifecycle_state, terminal_reason, terminal_error_code,
  revision, created_at, updated_at, terminal_at
) VALUES (
  'int-1', 'surface-1', 'standalone-local', 'legacy-unknown', 'legacy-uncertain',
  'legacy:key:1', 1, '{}', '{"legacy_status":"error","legacy_error_code":"user-cancelled"}',
  'failed', 'legacy failure', 'user-cancelled',
  1, ?, ?, ?)`, ts, ts, ts); err != nil {
		t.Fatalf("seed legacy interaction: %v", err)
	}

	if err := RunMigrations(database); err != nil {
		t.Fatalf("apply 0017: %v", err)
	}

	assertScalar(t, database,
		`SELECT error_code FROM envelopes WHERE envelope_id = 'env-1'`,
		"user-canceled", "envelopes.error_code after up")
	assertScalar(t, database,
		`SELECT response_payload FROM envelopes WHERE envelope_id = 'env-1'`,
		`{"code":"user-canceled","message":"context canceled"}`, "envelopes.response_payload after up")
	// Untouched on purpose.
	assertScalar(t, database,
		`SELECT terminal_error_code FROM interactions WHERE id = 'int-1'`,
		"user-cancelled", "a terminal interaction is immutable; roomflow canonicalizes it on read instead")
	assertScalar(t, database,
		`SELECT status FROM envelopes WHERE envelope_id = 'env-1'`,
		"cancelled", "envelopes.status is Tangent's own vocabulary and must not move")
	assertScalar(t, database,
		`SELECT json_extract(policy, '$.legacy_error_code') FROM interactions WHERE id = 'int-1'`,
		"user-cancelled", "the legacy provenance blob records what the row said, and must not be rewritten")

	if err := RollbackOne(database); err != nil {
		t.Fatalf("RollbackOne: %v", err)
	}
	assertScalar(t, database,
		`SELECT error_code FROM envelopes WHERE envelope_id = 'env-1'`,
		"user-cancelled", "envelopes.error_code after down")
	assertScalar(t, database,
		`SELECT response_payload FROM envelopes WHERE envelope_id = 'env-1'`,
		`{"code":"user-cancelled","message":"context canceled"}`, "envelopes.response_payload after down")
	// Re-applying is the real-world case: a row already carrying the US
	// spelling must not be double-rewritten into something else.
	if err := RunMigrations(database); err != nil {
		t.Fatalf("re-apply after rollback: %v", err)
	}
	assertScalar(t, database,
		`SELECT error_code FROM envelopes WHERE envelope_id = 'env-1'`,
		"user-canceled", "envelopes.error_code after re-apply")
}

func assertScalar(t *testing.T, database *sql.DB, query, want, what string) {
	t.Helper()
	var got string
	if err := database.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("%s: query: %v", what, err)
	}
	if got != want {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
}
