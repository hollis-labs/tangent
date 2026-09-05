package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// The fixture is built with raw SQL rather than through internal/interaction
// on purpose. These tests are about what the *storage layer* does when the
// immutability guards are suspended, and building the rows through the service
// would mean the test could only reach states the service is currently willing
// to produce. A retention operation has to work on rows written by a build that
// is no longer running.

type fixture struct {
	path      string
	db        *sql.DB
	surfaceID string
	// interactionIDs are in surface_sequence order.
	interactionIDs []string
	roomID         string
	now            time.Time
}

// newFixture builds a populated database in a temp directory. It is never
// pointed at a real installation: every caller passes t.TempDir().
func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureAt(t, 0)
}

// newFixtureAtSchema builds the same populated database at an older schema.
//
// It exists because every fixture in CW-20260825-0072 was migrated to HEAD
// before anything was measured, so every source in every backup and restore
// test was already at the binary's schema — and the cross-schema case is the
// only one that matters for an upgrade. You take a backup *because* you are
// about to migrate, so the source is always behind. CW-20260905-0014 is what
// went unnoticed for want of this.
//
// It migrates *to* the version rather than migrating to HEAD and rolling back,
// because those are different databases: a rollback leaves a schema that once
// held the newer tables. Every table the seed writes exists from migration
// 0009 onward, so 9 is the oldest version the whole fixture fits in.
func newFixtureAtSchema(t *testing.T, version int64) *fixture {
	t.Helper()
	return newFixtureAt(t, version)
}

// newFixtureAt migrates to `version`, or to HEAD when it is zero.
func newFixtureAt(t *testing.T, version int64) *fixture {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fixture.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if version <= 0 {
		if err := RunMigrations(database); err != nil {
			t.Fatalf("migrate fixture: %v", err)
		}
	} else if err := migrateTo(database, version); err != nil {
		t.Fatalf("migrate fixture to %d: %v", version, err)
	}

	f := &fixture{
		path: path, db: database,
		surfaceID: "srf_fixture", roomID: "room_fixture",
		now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	f.seed(t)
	return f
}

func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("fixture exec failed: %v\nquery: %s", err, query)
	}
}

func (f *fixture) seed(t *testing.T) {
	t.Helper()
	terminal := f.now.Add(-365 * 24 * time.Hour)

	f.exec(t, `INSERT INTO rooms (id, title, meta, phase_outputs, closed_reason, created_at, updated_at)
VALUES (?, 'fixture room', ?, ?, ?, ?, ?)`,
		f.roomID, `{"caller_note":"legacy caller payload"}`,
		`{"drafting":{"notes":"legacy participant notes"}}`,
		"legacy close reason text", terminal, terminal)

	f.exec(t, `INSERT INTO envelopes
(room_id, envelope_id, type, request_payload, response_kind, response_payload, status, error_code, error_message, created_at, resolved_at)
VALUES (?, 'env_1', 'tangent.triage', ?, 'approved', ?, 'resolved', '', ?, ?, ?)`,
		f.roomID, `{"question":"legacy request payload"}`, `{"answer":"legacy response payload"}`,
		"legacy adapter error text", terminal, terminal)

	f.exec(t, `INSERT INTO surfaces
(id, owner_scope, lifecycle_state, metadata, policy, next_interaction_sequence, revision,
 created_at, updated_at, closed_at, close_reason, legacy_room_id)
VALUES (?, 'standalone-local', 'closed', ?, ?, 3, 4, ?, ?, ?, ?, ?)`,
		f.surfaceID, `{"title":"fixture surface"}`, `{"policy_ref":"host-default"}`,
		terminal, terminal, terminal, "surface closed because the caller said so", f.roomID)

	f.exec(t, `INSERT INTO surface_open_requests
(caller_scope, idempotency_key, surface_id, request_snapshot, created_at)
VALUES ('standalone-local', 'open-1', ?, ?, ?)`,
		f.surfaceID, `{"open_request":"the second permanent copy of the caller payload"}`, terminal)

	for i, event := range []string{"surface.created", "surface.activated", "surface.closed"} {
		f.exec(t, `INSERT INTO surface_events
(event_id, surface_id, event_type, actor_ref, authority, from_revision, to_revision, metadata, recorded_at)
VALUES (?, ?, ?, 'caller:standalone-local', 'caller', ?, ?, '{}', ?)`,
			fmt.Sprintf("sev_%d", i), f.surfaceID, event, i+1, i+2, terminal)
	}

	// Two interactions: one resolved with a delivery obligation still pending,
	// one canceled with a terminal notification still pending. Both matter —
	// criterion 2's "delivery obligations" is exactly these rows.
	f.seedInteraction(t, "int_resolved", 1, "resolved", terminal, true)
	f.seedInteraction(t, "int_canceled", 2, "canceled", terminal, false)
	f.interactionIDs = []string{"int_resolved", "int_canceled"}

	f.exec(t, `INSERT INTO effect_handles
(id, class, root_id, relative_path, interaction_id, participant_scope, binding_digest,
 capabilities, issued_at, expires_at, max_uses, used)
VALUES ('eh_1', 'file', 'root', 'notes.md', 'int_resolved', 'participant:browser', 'digest',
        '["file.read"]', ?, ?, 3, 0)`,
		terminal, f.now.Add(-24*time.Hour))
}

func (f *fixture) seedInteraction(
	t *testing.T,
	id string,
	sequence int,
	state string,
	terminal time.Time,
	resolved bool,
) {
	t.Helper()

	terminalCause := sql.NullString{}
	terminalReason := sql.NullString{String: "caller withdrew: " + id, Valid: true}
	if resolved {
		// A resolved interaction carries no terminal cause or reason: the CHECK
		// on `interactions` forbids it.
		terminalReason = sql.NullString{}
	} else {
		terminalCause = sql.NullString{String: "caller_canceled", Valid: true}
	}

	f.exec(t, `INSERT INTO interactions
(id, surface_id, caller_scope, caller_principal_ref, caller_authority, caller_assurance,
 idempotency_key, surface_sequence, request_snapshot, external_refs, policy, lifecycle_state,
 presented_projection_revision, participant_ref, terminal_cause, terminal_reason,
 revision, created_at, updated_at, terminal_at, legacy_room_id, legacy_envelope_id)
VALUES (?, ?, 'standalone-local', 'caller:agent', 'caller', 'unverified',
        ?, ?, ?, ?, ?, ?, 1, 'participant:browser', ?, ?, 5, ?, ?, ?, NULL, NULL)`,
		id, f.surfaceID, "idem-"+id, sequence,
		`{"prompt":"the caller payload for `+id+`"}`,
		`{"artifact_ref":{"authority":"cerberus","artifact_id":"art_1","digest":"sha256:abc"}}`,
		`{"policy_ref":"host-default"}`, state,
		terminalCause, terminalReason, terminal, terminal, terminal)

	f.exec(t, `INSERT INTO definition_bindings
(interaction_id, publisher, kind, version, revision, digest, source, schema_identity,
 schema_digest, host_version, assurance, bound_at)
VALUES (?, 'tangent', 'tangent.triage', '1.0.0', 'r1', 'sha256:definition', 'in-tree',
        'tangent.triage/1.0.0', 'sha256:schema', '0.12.0', 'pinned', ?)`, id, terminal)

	for i, event := range []string{"interaction.submitted", "interaction.presented", "interaction.terminal"} {
		f.exec(t, `INSERT INTO interaction_events
(event_id, surface_id, interaction_id, event_type, actor_ref, authority, from_revision, to_revision, metadata, recorded_at)
VALUES (?, ?, ?, ?, 'caller:agent', 'caller', ?, ?, '{}', ?)`,
			fmt.Sprintf("iev_%s_%d", id, i), f.surfaceID, id, event, i+1, i+2, terminal)
	}

	f.exec(t, `INSERT INTO draft_revisions
(interaction_id, revision, interaction_revision, participant_ref, definition_version, payload,
 sensitivity, created_at)
VALUES (?, 1, 3, 'participant:browser', '1.0.0', ?, 'normal', ?)`,
		id, `{"draft":"unfinished participant work for `+id+`"}`, terminal)

	if !resolved {
		f.exec(t, `INSERT INTO terminal_notifications
(id, interaction_id, terminal_state, terminal_cause, destination_binding, idempotency_key,
 policy, lifecycle_state, revision, lease_owner, lease_expires_at, terminal_reason, created_at, updated_at)
VALUES (?, ?, 'canceled', 'caller_canceled', '{"kind":"caller-pull"}', 'tn-idem', '{}',
        'queued', 1, 'worker:dead-process', ?, ?, ?, ?)`,
			"tn_"+id, id, terminal.Add(time.Hour), "adapter said: the caller went away", terminal, terminal)
		return
	}

	f.exec(t, `INSERT INTO resolutions
(id, interaction_id, expected_interaction_revision, presented_projection_revision, participant_ref,
 participant_authority, participant_assurance, response_kind, response_payload, source_draft_revision,
 integrity_digest, submitted_at, validated_at, recorded_at)
VALUES (?, ?, 4, 1, 'participant:browser', 'participant', 'session', 'approved', ?, 1,
        'sha256:integrity-of-the-answer', ?, ?, ?)`,
		"res_"+id, id, `{"decision":"approved","comment":"the participant's own words"}`,
		terminal, terminal, terminal)

	f.exec(t, `INSERT INTO resolution_deliveries
(id, resolution_id, destination_binding, idempotency_key, policy, lifecycle_state, revision,
 lease_owner, lease_expires_at, terminal_reason, created_at, updated_at)
VALUES (?, ?, '{"kind":"caller-pull"}', 'rd-idem', '{}', 'delivering', 2,
        'worker:dead-process', ?, ?, ?, ?)`,
		"rd_"+id, "res_"+id, terminal.Add(time.Hour), "adapter said: connection reset", terminal, terminal)

	f.exec(t, `INSERT INTO delivery_events
(event_id, resolution_delivery_id, event_type, from_revision, to_revision, attempt_number, metadata, recorded_at)
VALUES ('dev_1', ?, 'delivery.queued', 0, 1, NULL, '{}', ?)`, "rd_"+id, terminal)
	f.exec(t, `INSERT INTO delivery_events
(event_id, resolution_delivery_id, event_type, from_revision, to_revision, attempt_number, metadata, recorded_at)
VALUES ('dev_2', ?, 'delivery.started', 1, 2, 1, '{}', ?)`, "rd_"+id, terminal)

	f.exec(t, `INSERT INTO delivery_attempts
(id, resolution_delivery_id, attempt_number, status, error_code, error_message, started_at)
VALUES ('da_1', ?, 1, 'delivering', '', ?, ?)`,
		"rd_"+id, "adapter said: the socket closed mid-write", terminal)

	f.exec(t, `INSERT INTO terminal_outcome_retrievals
(id, interaction_id, resolution_id, requester_scope, transport_correlation, retrieved_at)
VALUES ('tor_1', ?, ?, 'standalone-local', '{}', ?)`, id, "res_"+id, terminal)

	f.exec(t, `INSERT INTO terminal_outcome_acknowledgements
(interaction_id, acknowledgement_id, resolution_id, requester_scope, transport_correlation, acknowledged_at)
VALUES (?, 'ack_1', ?, 'standalone-local', '{}', ?)`, id, "res_"+id, terminal)
}

// column reads one column value, for asserting on content directly.
func (f *fixture) column(t *testing.T, query string, args ...any) string {
	t.Helper()
	var value sql.NullString
	if err := f.db.QueryRowContext(context.Background(), query, args...).Scan(&value); err != nil {
		t.Fatalf("read column: %v\nquery: %s", err, query)
	}
	return value.String
}

func (f *fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var count int
	if err := f.db.QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count: %v\nquery: %s", err, query)
	}
	return count
}
