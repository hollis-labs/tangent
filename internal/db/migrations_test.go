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
	for range 3 {
		if err := RollbackOne(database); err != nil {
			t.Fatalf("rollback to v0.12 schema: %v", err)
		}
	}
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

	for range 3 {
		if err := RollbackOne(database); err != nil {
			t.Fatalf("rollback durable migrations: %v", err)
		}
	}
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
	if err := RollbackOne(database); err != nil {
		t.Fatalf("RollbackOne delivery attempt lifecycle: %v", err)
	}
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
	if err := RollbackOne(database); err != nil {
		t.Fatalf("rollback to pre-delivery-lifecycle schema: %v", err)
	}

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

	if err := RollbackOne(database); err != nil {
		t.Fatalf("rollback delivery lifecycle migration: %v", err)
	}
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
	for range 3 {
		if err := RollbackOne(database); err != nil {
			t.Fatalf("rollback to v0.12: %v", err)
		}
	}
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
