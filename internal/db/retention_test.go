package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// errDeliberate is the injected failure that proves the rollback path.
var errDeliberate = errors.New("deliberate failure inside the guard-suspended transaction")

// TestImmutabilityGuardsBlockEveryDirectPath is the obstruction, restated as a
// test. Everything else in this file only means something if these still fail.
func TestImmutabilityGuardsBlockEveryDirectPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		query string
		args  []any
	}{
		{"delete surface cascades into surface_events", `DELETE FROM surfaces WHERE id = ?`, []any{f.surfaceID}},
		{"delete interaction cascades into interaction_events", `DELETE FROM interactions WHERE id = ?`,
			[]any{"int_resolved"}},
		{"delete surface open request", `DELETE FROM surface_open_requests WHERE surface_id = ?`,
			[]any{f.surfaceID}},
		{"update resolution payload", `UPDATE resolutions SET response_payload = '{}' WHERE id = ?`,
			[]any{"res_int_resolved"}},
		{"update draft payload", `UPDATE draft_revisions SET payload = '{}' WHERE interaction_id = ?`,
			[]any{"int_resolved"}},
		{"update interaction request snapshot",
			`UPDATE interactions SET request_snapshot = '{}', revision = revision + 1 WHERE id = ?`,
			[]any{"int_resolved"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := f.db.ExecContext(ctx, testCase.query, testCase.args...); err == nil {
				t.Fatal("expected the immutability guard to abort this statement, but it succeeded")
			}
		})
	}
}

// TestGuardInventoryIsDerivedFromTheMigrations pins the inventory this work
// actually found, rather than the count ADR 0002 recorded at 0a45caa. The
// numbers are asserted so that a migration adding an immutable table without
// telling the retention path about it fails here rather than in production.
func TestGuardInventoryIsDerivedFromTheMigrations(t *testing.T) {
	ctx := context.Background()
	reference, err := ReferenceGuards(ctx)
	if err != nil {
		t.Fatalf("reference guards: %v", err)
	}

	var deletes, updates int
	for _, guard := range reference.Guards {
		switch guard.Event {
		case "DELETE":
			deletes++
		case "UPDATE":
			updates++
		}
	}
	// Twelve delete guards at migration 0012: the nine ADR 0002 counted at
	// 0a45caa, plus terminal_outcome_acknowledgements (0006),
	// definition_manifests (0007), and retention_operations (0012).
	if deletes != 12 {
		t.Fatalf("expected 12 DELETE guards, found %d: %v", deletes, reference.Names())
	}
	if updates != 29 {
		t.Fatalf("expected 29 UPDATE guards, found %d: %v", updates, reference.Names())
	}

	f := newFixture(t)
	installed, err := ReadGuards(ctx, f.db)
	if err != nil {
		t.Fatalf("read guards: %v", err)
	}
	if drift := CompareGuards(installed, reference); !drift.Intact() {
		t.Fatalf("a freshly migrated database drifted from the reference: %+v", drift)
	}
}

// TestSurfaceOpenRequestsCascades proves ADR 0002 §Q9's decision landed: the
// second permanent copy of the caller payload is now reachable.
func TestSurfaceOpenRequestsCascades(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	var onDelete string
	rows, err := f.db.QueryContext(ctx, `PRAGMA foreign_key_list("surface_open_requests");`)
	if err != nil {
		t.Fatalf("read foreign keys: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, seq int
		var table, from, onUpdate, match string
		var to any
		if scanErr := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); scanErr != nil {
			t.Fatalf("scan foreign keys: %v", scanErr)
		}
	}
	if onDelete != "CASCADE" {
		t.Fatalf("surface_open_requests.surface_id is ON DELETE %s, want CASCADE", onDelete)
	}
}

func TestRedactInteractionRemovesContentAndPreservesIdentity(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	before, err := TakeFingerprint(ctx, f.db)
	if err != nil {
		t.Fatalf("fingerprint before: %v", err)
	}

	result, err := RedactInteraction(ctx, f.db, RetentionRequest{
		InteractionID: "int_resolved", ActorRef: "operator:test",
		Authority: AuthorityLocalUser, PolicyRef: "test-erasure", Now: f.now,
	})
	if err != nil {
		t.Fatalf("redact interaction: %v", err)
	}
	if result.Outcome != OutcomeApplied {
		t.Fatalf("outcome = %s, want applied", result.Outcome)
	}
	if !result.GuardsRestored {
		t.Fatal("the maintenance path did not verify the guards back in place")
	}
	if len(result.Removed) == 0 {
		t.Fatal("nothing was reported removed")
	}

	// Content is gone, and gone in the exact shape ADR 0002 §2 specifies.
	snapshot := f.column(t, `SELECT request_snapshot FROM interactions WHERE id = 'int_resolved'`)
	var tombstone RedactionTombstone
	if decodeErr := json.Unmarshal([]byte(snapshot), &tombstone); decodeErr != nil {
		t.Fatalf("request_snapshot is not a tombstone: %v", decodeErr)
	}
	if !tombstone.Redacted || tombstone.ContentDigest == "" ||
		tombstone.PolicyRef != "test-erasure" || tombstone.ActorRef != "operator:test" {
		t.Fatalf("tombstone is incomplete: %+v", tombstone)
	}
	if strings.Contains(snapshot, "the caller payload") {
		t.Fatal("the caller payload survived redaction")
	}
	if payload := f.column(t,
		`SELECT response_payload FROM resolutions WHERE id = 'res_int_resolved'`); strings.Contains(
		payload, "the participant's own words") {
		t.Fatal("the participant's resolution payload survived redaction")
	}
	if reason := f.column(t,
		`SELECT terminal_reason FROM resolution_deliveries WHERE id = 'rd_int_resolved'`); strings.Contains(
		reason, "connection reset") {
		t.Fatal("adapter freeform text survived redaction")
	}
	if message := f.column(t,
		`SELECT error_message FROM delivery_attempts WHERE id = 'da_1'`); strings.Contains(
		message, "socket closed") {
		t.Fatal("delivery attempt error text survived redaction")
	}

	// Identity, lifecycle, digests, and participant binding are untouched.
	if digest := f.column(t,
		`SELECT integrity_digest FROM resolutions WHERE id = 'res_int_resolved'`); digest !=
		"sha256:integrity-of-the-answer" {
		t.Fatalf("integrity_digest changed to %q", digest)
	}
	if participant := f.column(t,
		`SELECT participant_ref FROM resolutions WHERE id = 'res_int_resolved'`); participant !=
		"participant:browser" {
		t.Fatalf("participant_ref changed to %q", participant)
	}
	if key := f.column(t,
		`SELECT idempotency_key FROM interactions WHERE id = 'int_resolved'`); key != "idem-int_resolved" {
		t.Fatalf("idempotency key changed to %q", key)
	}
	if refs := f.column(t,
		`SELECT external_refs FROM interactions WHERE id = 'int_resolved'`); !strings.Contains(
		refs, "sha256:abc") {
		t.Fatal("external references were removed; ADR 0002 §4 keeps them as what survives redaction")
	}

	after, err := TakeFingerprint(ctx, f.db)
	if err != nil {
		t.Fatalf("fingerprint after: %v", err)
	}
	if differences := before.Compare(after); len(differences) != 0 {
		t.Fatalf("redaction changed a preservation property: %+v", differences)
	}

	// The audit row exists and says what happened.
	history, err := RetentionHistory(ctx, f.db, 10)
	if err != nil {
		t.Fatalf("retention history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected exactly one audit row, got %d", len(history))
	}
	row := history[0]
	if row.Kind != string(OperationPayloadRedaction) || row.Outcome != string(OutcomeApplied) ||
		!row.GuardsRestored || row.RemovedCount != len(result.Removed) ||
		row.BackupSurvey != "not-surveyed" {
		t.Fatalf("audit row does not describe the operation: %+v", row)
	}
}

func TestRedactionIsIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	req := RetentionRequest{InteractionID: "int_resolved", ActorRef: "operator:test", Now: f.now}

	first, err := RedactInteraction(ctx, f.db, req)
	if err != nil {
		t.Fatalf("first redaction: %v", err)
	}
	second, err := RedactInteraction(ctx, f.db, req)
	if err != nil {
		t.Fatalf("second redaction: %v", err)
	}
	if len(second.Removed) != 0 {
		t.Fatalf("the second redaction removed %d more columns; it should have found tombstones",
			len(second.Removed))
	}
	if second.Code != "already_redacted" {
		t.Fatalf("second redaction code = %q, want already_redacted", second.Code)
	}
	// A digest of a digest would be a silent corruption of the audit answer.
	firstDigest := first.Removed[0].SHA256
	snapshot := f.column(t, `SELECT request_snapshot FROM interactions WHERE id = 'int_resolved'`)
	var tombstone RedactionTombstone
	if err := json.Unmarshal([]byte(snapshot), &tombstone); err != nil {
		t.Fatalf("decode tombstone: %v", err)
	}
	if tombstone.ContentDigest != f.digestFor(t, first, "interactions", "request_snapshot") {
		t.Fatal("the tombstone digest changed on the second pass")
	}
	_ = firstDigest
}

func (f *fixture) digestFor(t *testing.T, result RetentionResult, table, column string) string {
	t.Helper()
	for _, removed := range result.Removed {
		if removed.Table == table && removed.Column == column {
			return removed.SHA256
		}
	}
	t.Fatalf("no removed record for %s.%s", table, column)
	return ""
}

func TestRedactSurfaceReachesTheLegacyCopiesAndTheSecondSnapshot(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	result, err := RedactSurface(ctx, f.db, RetentionRequest{
		SurfaceID: f.surfaceID, ActorRef: "operator:test", Now: f.now,
	})
	if err != nil {
		t.Fatalf("redact surface: %v", err)
	}
	if !result.GuardsRestored {
		t.Fatal("guards were not verified restored")
	}

	checks := []struct {
		name   string
		query  string
		forbid string
	}{
		{"surface open request second copy",
			`SELECT request_snapshot FROM surface_open_requests WHERE surface_id = '` + f.surfaceID + `'`,
			"second permanent copy"},
		{"legacy room meta", `SELECT meta FROM rooms WHERE id = '` + f.roomID + `'`, "legacy caller payload"},
		{"legacy phase outputs", `SELECT phase_outputs FROM rooms WHERE id = '` + f.roomID + `'`,
			"legacy participant notes"},
		{"legacy envelope request", `SELECT request_payload FROM envelopes WHERE envelope_id = 'env_1'`,
			"legacy request payload"},
		{"legacy envelope response", `SELECT response_payload FROM envelopes WHERE envelope_id = 'env_1'`,
			"legacy response payload"},
		{"surface close reason", `SELECT close_reason FROM surfaces WHERE id = '` + f.surfaceID + `'`,
			"because the caller said so"},
		{"first interaction payload",
			`SELECT request_snapshot FROM interactions WHERE id = 'int_resolved'`, "the caller payload"},
		{"second interaction payload",
			`SELECT request_snapshot FROM interactions WHERE id = 'int_canceled'`, "the caller payload"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if value := f.column(t, check.query); strings.Contains(value, check.forbid) {
				t.Fatalf("content survived: %q still contains %q", value, check.forbid)
			}
		})
	}
}

func TestDeleteDraftsWritesTheTombstonesNobodyEverWrote(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if existing := f.count(t, `SELECT COUNT(*) FROM draft_revision_tombstones`); existing != 0 {
		t.Fatalf("fixture already has %d tombstones", existing)
	}
	result, err := DeleteDrafts(ctx, f.db, RetentionRequest{
		InteractionID: "int_resolved", ActorRef: "participant:browser", Now: f.now,
	})
	if err != nil {
		t.Fatalf("delete drafts: %v", err)
	}
	if result.Outcome != OutcomeApplied || len(result.Removed) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if tombstones := f.count(t,
		`SELECT COUNT(*) FROM draft_revision_tombstones WHERE interaction_id = 'int_resolved'`); tombstones != 1 {
		t.Fatalf("expected one draft tombstone, got %d", tombstones)
	}
	if payload := f.column(t,
		`SELECT payload FROM draft_revisions WHERE interaction_id = 'int_resolved'`); strings.Contains(
		payload, "unfinished participant work") {
		t.Fatal("the draft payload survived")
	}
	// The draft revision row itself survives, because the resolution points at
	// it as its source and destroying that reference destroys provenance.
	if rows := f.count(t,
		`SELECT COUNT(*) FROM draft_revisions WHERE interaction_id = 'int_resolved'`); rows != 1 {
		t.Fatalf("the draft revision row was destroyed; %d remain", rows)
	}
}

func TestExpireCapabilitiesNeedsNoGuardSuspension(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	result, err := ExpireCapabilities(ctx, f.db, RetentionRequest{
		InteractionID: "int_resolved", ActorRef: "operator:test", Now: f.now,
	}, f.now)
	if err != nil {
		t.Fatalf("expire capabilities: %v", err)
	}
	if result.AffectedRows != 1 {
		t.Fatalf("expected one handle removed, got %d", result.AffectedRows)
	}
	if result.GuardsRestored {
		t.Fatal("capability expiry reported suspending guards; it must not need to")
	}
	if remaining := f.count(t, `SELECT COUNT(*) FROM effect_handles`); remaining != 0 {
		t.Fatalf("%d handles remain", remaining)
	}
}

func TestPurgeSurfaceCascadesAndLeavesTheAuditRow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	result, err := PurgeSurface(ctx, f.db, RetentionRequest{
		SurfaceID: f.surfaceID, ActorRef: "operator:test", Authority: AuthorityLocalUser, Now: f.now,
	})
	if err != nil {
		t.Fatalf("purge surface: %v", err)
	}
	if !result.GuardsRestored {
		t.Fatal("guards were not verified restored after the purge")
	}

	emptied := []string{
		"surfaces", "surface_events", "surface_open_requests", "interactions", "interaction_events",
		"definition_bindings", "draft_revisions", "resolutions", "resolution_deliveries",
		"terminal_notifications", "delivery_attempts", "delivery_events",
		"terminal_outcome_retrievals", "terminal_outcome_acknowledgements", "rooms", "envelopes",
		"effect_handles",
	}
	for _, table := range emptied {
		if rows := f.count(t, `SELECT COUNT(*) FROM `+table); rows != 0 {
			t.Errorf("%s still holds %d rows after the purge", table, rows)
		}
	}

	// The audit row has no foreign key precisely so that it outlives what it
	// records. If a cascade had reached it, this is where that shows.
	history, err := RetentionHistory(ctx, f.db, 10)
	if err != nil {
		t.Fatalf("retention history: %v", err)
	}
	if len(history) != 1 || history[0].Kind != string(OperationSurfacePurge) ||
		history[0].SurfaceID != f.surfaceID {
		t.Fatalf("the purge audit row did not survive its own purge: %+v", history)
	}

	if drift := f.guardDrift(t); !drift.Intact() {
		t.Fatalf("the purge left the schema without its guards: %+v", drift)
	}
}

// TestPurgeSurfaceLeavesChannelSubjectsAsTombstones is the regression for a
// director review finding on CW-20260906-0064 (PR #32): channel_subjects'
// CHECK constraint originally required interaction_id/surface_id non-NULL
// for their subject_type, which SQLite enforces on every row UPDATE
// including one an ON DELETE SET NULL foreign-key action performs
// internally — so purging a surface with a channel subject correlating it
// aborted the whole purge transaction with SQLITE_CONSTRAINT_CHECK. The fix
// relaxes the CHECK to allow the FK to go NULL while subject_type stays: the
// thread survives its purged correlation as a tombstone, exposed by
// channel.Subject.ReferentPurged, rather than being cascaded away with the
// content it once pointed at.
func TestPurgeSurfaceLeavesChannelSubjectsAsTombstones(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	now := f.now
	if _, err := f.db.ExecContext(ctx, `
INSERT INTO channels (id, owner_scope, created_at, updated_at)
VALUES ('ch_fixture', 'standalone-local:anonymous', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if _, err := f.db.ExecContext(ctx, `
INSERT INTO channel_subjects (id, channel_id, subject_type, interaction_id, created_at, updated_at)
VALUES ('sub_interaction', 'ch_fixture', 'interaction', ?, ?, ?)`, f.interactionIDs[0], now, now); err != nil {
		t.Fatalf("seed interaction subject: %v", err)
	}
	if _, err := f.db.ExecContext(ctx, `
INSERT INTO channel_subjects (id, channel_id, subject_type, surface_id, created_at, updated_at)
VALUES ('sub_surface', 'ch_fixture', 'surface', ?, ?, ?)`, f.surfaceID, now, now); err != nil {
		t.Fatalf("seed surface subject: %v", err)
	}

	result, err := PurgeSurface(ctx, f.db, RetentionRequest{
		SurfaceID: f.surfaceID, ActorRef: "operator:test", Authority: AuthorityLocalUser, Now: f.now,
	})
	if err != nil {
		t.Fatalf("purge surface with a channel subject present: %v", err)
	}
	if !result.GuardsRestored {
		t.Fatal("guards were not verified restored after the purge")
	}

	// The channel and both subjects survive: a surface purge is not a channel
	// operation, and reaches channel_subjects only through the FK it declared.
	if remaining := f.count(t, `SELECT COUNT(*) FROM channels WHERE id = 'ch_fixture'`); remaining != 1 {
		t.Fatalf("channel did not survive the purge: %d rows", remaining)
	}
	if remaining := f.count(t, `SELECT COUNT(*) FROM channel_subjects`); remaining != 2 {
		t.Fatalf("expected both subjects to survive as tombstones, found %d rows", remaining)
	}

	var interactionRef, surfaceRef sql.NullString
	if err := f.db.QueryRow(`SELECT interaction_id FROM channel_subjects WHERE id = 'sub_interaction'`).
		Scan(&interactionRef); err != nil {
		t.Fatalf("read interaction subject: %v", err)
	}
	if interactionRef.Valid {
		t.Fatalf("sub_interaction.interaction_id = %q, want NULL after its interaction was purged", interactionRef.String)
	}
	if err := f.db.QueryRow(`SELECT surface_id FROM channel_subjects WHERE id = 'sub_surface'`).
		Scan(&surfaceRef); err != nil {
		t.Fatalf("read surface subject: %v", err)
	}
	if surfaceRef.Valid {
		t.Fatalf("sub_surface.surface_id = %q, want NULL after its surface was purged", surfaceRef.String)
	}

	if drift := f.guardDrift(t); !drift.Intact() {
		t.Fatalf("the purge left the schema without its guards: %+v", drift)
	}
}

func (f *fixture) guardDrift(t *testing.T) GuardDrift {
	t.Helper()
	ctx := context.Background()
	installed, err := ReadGuards(ctx, f.db)
	if err != nil {
		t.Fatalf("read guards: %v", err)
	}
	reference, err := ReferenceGuards(ctx)
	if err != nil {
		t.Fatalf("reference guards: %v", err)
	}
	return CompareGuards(installed, reference)
}

func TestExternalSourceDeletionRefusesAndRemovesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	result, references, err := ReportExternalSourceDeletion(ctx, f.db, RetentionRequest{
		InteractionID: "int_resolved", ActorRef: "operator:test", Now: f.now,
	})
	if err != nil {
		t.Fatalf("external source deletion: %v", err)
	}
	if result.Outcome != OutcomeRefused || result.Code != "external_source_unreachable" {
		t.Fatalf("expected a typed refusal, got %+v", result)
	}
	if len(references) != 1 {
		t.Fatalf("expected one reference set, got %d", len(references))
	}
	if refs := f.column(t,
		`SELECT external_refs FROM interactions WHERE id = 'int_resolved'`); !strings.Contains(
		refs, "sha256:abc") {
		t.Fatal("the reference was removed; §4 keeps it as the durable fact")
	}
	history, err := RetentionHistory(ctx, f.db, 10)
	if err != nil {
		t.Fatalf("retention history: %v", err)
	}
	if len(history) != 1 || history[0].Outcome != string(OutcomeRefused) {
		t.Fatalf("the refusal was not recorded: %+v", history)
	}
}

func TestSurfaceCloseIsRecordedAsRemovingNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	result, err := RecordSurfaceClose(ctx, f.db, RetentionRequest{
		SurfaceID: f.surfaceID, ActorRef: "operator:test", Now: f.now,
	}, 2)
	if err != nil {
		t.Fatalf("record surface close: %v", err)
	}
	if result.AffectedRows != 0 || len(result.Removed) != 0 {
		t.Fatalf("a close reported removing something: %+v", result)
	}
	if payload := f.column(t,
		`SELECT request_snapshot FROM interactions WHERE id = 'int_resolved'`); !strings.Contains(
		payload, "the caller payload") {
		t.Fatal("a close removed content")
	}
}

// TestGuardSuspensionRollsBackOnFailure is the test ADR 0002 §6 asks for by
// name: a bug in the maintenance path must not be able to leave the database
// without its immutability guards.
func TestGuardSuspensionRollsBackOnFailure(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	restored, err := withGuardsSuspended(ctx, f.db, redactionGuards,
		func(context.Context, *sql.Tx) error { return errDeliberate })
	if err == nil {
		t.Fatal("expected the deliberate failure to propagate")
	}
	if restored {
		t.Fatal("a failed operation reported the guards verified restored")
	}
	if drift := f.guardDrift(t); !drift.Intact() {
		t.Fatalf("the rollback did not restore the guards: %+v", drift)
	}
	// And the guards still actually abort, which is the property the inventory
	// is a proxy for.
	if _, execErr := f.db.ExecContext(ctx,
		`UPDATE resolutions SET response_payload = '{}' WHERE id = 'res_int_resolved'`); execErr == nil {
		t.Fatal("the resolution guard no longer aborts")
	}
}

func TestRetentionOperationsAreThemselvesImmutable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := RedactInteraction(ctx, f.db, RetentionRequest{
		InteractionID: "int_resolved", ActorRef: "operator:test", Now: f.now,
	}); err != nil {
		t.Fatalf("redact: %v", err)
	}
	if _, err := f.db.ExecContext(ctx,
		`UPDATE retention_operations SET outcome = 'refused'`); err == nil {
		t.Fatal("the erasure log accepted an UPDATE")
	}
	if _, err := f.db.ExecContext(ctx, `DELETE FROM retention_operations`); err == nil {
		t.Fatal("the erasure log accepted a DELETE")
	}
}

func TestPlanAndApplyRetentionWindows(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	windows := DefaultRetentionWindows()

	plan, err := PlanRetention(ctx, f.db, windows, f.now)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// The fixture's interactions went terminal a year ago; both are past the
	// 30-day window and the surface is past the 90-day one.
	if len(plan.Interactions) != 2 || len(plan.Surfaces) != 1 {
		t.Fatalf("plan found %d interactions and %d surfaces",
			len(plan.Interactions), len(plan.Surfaces))
	}
	for _, candidate := range plan.Interactions {
		if candidate.UnredactedColumns == 0 {
			t.Fatalf("candidate %s reports nothing to remove", candidate.InteractionID)
		}
	}
	if before := f.count(t, `SELECT COUNT(*) FROM retention_operations`); before != 0 {
		t.Fatalf("planning wrote %d audit rows; a plan is not an act", before)
	}

	results, err := ApplyRetention(ctx, f.db, windows, RetentionRequest{ActorRef: "host", Now: f.now}, f.now)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected three operations (two interactions, one surface), got %d", len(results))
	}
	for _, result := range results {
		if result.Outcome != OutcomeApplied || !result.GuardsRestored {
			t.Fatalf("operation did not complete cleanly: %+v", result)
		}
	}

	// Idempotent and restart-safe: a second sweep finds tombstones.
	again, err := ApplyRetention(ctx, f.db, windows, RetentionRequest{ActorRef: "host", Now: f.now}, f.now)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("the second sweep found %d operations to run", len(again))
	}
}

// TestNonTerminalInteractionsAreNeverEligible is the race ADR 0002 names: a
// window sweep must never redact an interaction a participant is still
// answering.
func TestNonTerminalInteractionsAreNeverEligible(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.exec(t, `INSERT INTO interactions
(id, surface_id, caller_scope, caller_authority, caller_assurance, idempotency_key, surface_sequence,
 request_snapshot, external_refs, policy, lifecycle_state, revision, created_at, updated_at)
VALUES ('int_live', ?, 'standalone-local', 'caller', 'unverified', 'idem-live', 9,
        '{"prompt":"still open"}', '{}', '{}', 'presented', 1, ?, ?)`,
		f.surfaceID, f.now.Add(-400*24*time.Hour), f.now.Add(-400*24*time.Hour))

	plan, err := PlanRetention(ctx, f.db, DefaultRetentionWindows(), f.now)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for _, candidate := range plan.Interactions {
		if candidate.InteractionID == "int_live" {
			t.Fatal("a non-terminal interaction was listed as eligible for window expiry")
		}
	}
}

func TestRefusalsAreRecordedNotSwallowed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	result, err := RedactInteraction(ctx, f.db, RetentionRequest{
		InteractionID: "int_does_not_exist", ActorRef: "operator:test", Now: f.now,
	})
	if err != nil {
		t.Fatalf("redact: %v", err)
	}
	if result.Outcome != OutcomeRefused || result.Code != "unknown_interaction" {
		t.Fatalf("expected a typed refusal, got %+v", result)
	}
	history, err := RetentionHistory(ctx, f.db, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 || history[0].Outcome != string(OutcomeRefused) {
		t.Fatalf("the refusal was not recorded: %+v", history)
	}
}

func TestDryRunWritesNothingAtAll(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	result, err := RedactSurface(ctx, f.db, RetentionRequest{
		SurfaceID: f.surfaceID, ActorRef: "operator:test", DryRun: true, Now: f.now,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !result.DryRun || len(result.Removed) == 0 {
		t.Fatalf("a dry run reported nothing to do: %+v", result)
	}
	if rows := f.count(t, `SELECT COUNT(*) FROM retention_operations`); rows != 0 {
		t.Fatalf("the dry run wrote %d audit rows", rows)
	}
	if payload := f.column(t,
		`SELECT request_snapshot FROM interactions WHERE id = 'int_resolved'`); !strings.Contains(
		payload, "the caller payload") {
		t.Fatal("the dry run removed content")
	}
}
