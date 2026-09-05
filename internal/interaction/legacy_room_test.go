package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

// TestEnsureLegacyRoomSurfaceAdoptsMigratedAndFreshRooms covers both halves of
// the compatibility boundary: a room that existed before the durable substrate
// (and therefore already has a migration-imported surface with no open
// request) and a room created afterwards (which has no surface at all). Both
// must converge on the same stable identity, and repeating either must not
// open a second surface.
func TestEnsureLegacyRoomSurfaceAdoptsMigratedAndFreshRooms(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, openErr := tangentdb.Open(filepath.Join(t.TempDir(), "legacy-room.db"))
	if openErr != nil {
		t.Fatalf("db.Open: %v", openErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := tangentdb.RunMigrations(database); err != nil {
		t.Fatalf("db.RunMigrations: %v", err)
	}
	// Unwind to the pre-durable schema so the seeded room is genuinely a
	// pre-upgrade room, then upgrade it the way a real installation would.
	for {
		if _, probeErr := database.ExecContext(ctx, `SELECT 1 FROM surfaces LIMIT 1`); probeErr != nil {
			break
		}
		if err := tangentdb.RollbackOne(database); err != nil {
			t.Fatalf("RollbackOne: %v", err)
		}
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO rooms (id, meta, created_at, updated_at)
VALUES ('migrated-room', '{}', '2026-08-20T10:00:00Z', '2026-08-20T10:00:00Z')`); err != nil {
		t.Fatalf("seed migrated room: %v", err)
	}
	if err := tangentdb.RunMigrations(database); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO rooms (id, meta, created_at, updated_at)
VALUES ('fresh-room', '{}', '2026-09-04T10:00:00Z', '2026-09-04T10:00:00Z')`); err != nil {
		t.Fatalf("seed fresh room: %v", err)
	}

	store := NewStore(database)
	for _, test := range []struct {
		roomID      string
		wantCreated bool
	}{
		{roomID: "migrated-room", wantCreated: false},
		{roomID: "fresh-room", wantCreated: true},
	} {
		params := EnsureLegacyRoomSurfaceParams{
			RoomID: test.roomID, CallerScope: "standalone-local",
			Metadata: json.RawMessage(`{"presentation":"legacy-room"}`),
		}
		surface, created, err := store.EnsureLegacyRoomSurface(ctx, params)
		if err != nil {
			t.Fatalf("EnsureLegacyRoomSurface %s: %v", test.roomID, err)
		}
		if created != test.wantCreated {
			t.Fatalf("%s created = %v, want %v", test.roomID, created, test.wantCreated)
		}
		if surface.ID != test.roomID || surface.LegacyRoomID != test.roomID {
			t.Fatalf("%s surface = %#v", test.roomID, surface)
		}
		if surface.State != SurfaceStateActive {
			t.Fatalf("%s surface state = %q, want active", test.roomID, surface.State)
		}

		repeat, repeatCreated, err := store.EnsureLegacyRoomSurface(ctx, params)
		if err != nil {
			t.Fatalf("EnsureLegacyRoomSurface repeat %s: %v", test.roomID, err)
		}
		if repeatCreated || repeat.ID != surface.ID {
			t.Fatalf("%s repeat opened a second surface: %#v", test.roomID, repeat)
		}
		var openRequests int
		if err := database.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM surface_open_requests WHERE surface_id = ?`, test.roomID,
		).Scan(&openRequests); err != nil {
			t.Fatalf("count open requests: %v", err)
		}
		if openRequests != 1 {
			t.Fatalf("%s has %d open requests, want exactly one", test.roomID, openRequests)
		}
	}
}

// TestLegacyRoomInteractionsAreListedForRestartAndBindBothWays asserts the
// correlation columns are written and that restart reconstruction sees exactly
// the open ones.
func TestLegacyRoomInteractionsAreListedForRestartAndBindBothWays(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := openTestStore(t)
	surface := createTestSurface(t, store, "legacy-room-list")

	open := testInteractionParams(surface.ID, "standalone-local", "workflow:tangent.triage:env-open", "open")
	open.LegacyRoomID = surface.ID
	open.LegacyEnvelopeID = "env-open"
	openResult, err := store.CreateInteraction(ctx, open)
	if err != nil {
		t.Fatalf("CreateInteraction open: %v", err)
	}

	closed := testInteractionParams(surface.ID, "standalone-local", "workflow:tangent.triage:env-closed", "closed")
	closed.LegacyRoomID = surface.ID
	closed.LegacyEnvelopeID = "env-closed"
	closedResult, err := store.CreateInteraction(ctx, closed)
	if err != nil {
		t.Fatalf("CreateInteraction closed: %v", err)
	}
	if _, terminalErr := store.TerminalizeInteraction(ctx, TerminalizeInteractionParams{
		InteractionID: closedResult.Interaction.ID, ExpectedRevision: closedResult.Interaction.Revision,
		To: InteractionStateCanceled, Cause: TerminalCauseCallerWithdrawn,
		Reason: "withdrawn", ActorRef: "caller", Authority: "asserted",
		Notifications: []TerminalNotificationParams{{
			DestinationBinding: json.RawMessage(`{"kind":"caller_pull","caller_scope":"standalone-local"}`),
			IdempotencyKey:     "cancel:1",
			Policy:             json.RawMessage(`{"delivery":"durable-caller-pull"}`),
		}},
	}); terminalErr != nil {
		t.Fatalf("TerminalizeInteraction: %v", terminalErr)
	}

	listed, err := store.ListOpenLegacyRoomInteractions(ctx)
	if err != nil {
		t.Fatalf("ListOpenLegacyRoomInteractions: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != openResult.Interaction.ID {
		t.Fatalf("listed = %#v, want only the open interaction", listed)
	}
	if listed[0].LegacyRoomID != surface.ID || listed[0].LegacyEnvelopeID != "env-open" {
		t.Fatalf("legacy binding = %q/%q", listed[0].LegacyRoomID, listed[0].LegacyEnvelopeID)
	}

	found, ok, err := store.FindInteractionByLegacyEnvelope(ctx, surface.ID, "env-closed")
	if err != nil || !ok {
		t.Fatalf("FindInteractionByLegacyEnvelope: %v (found=%v)", err, ok)
	}
	if found.ID != closedResult.Interaction.ID {
		t.Fatalf("found %q, want %q", found.ID, closedResult.Interaction.ID)
	}
	if _, ok, err := store.FindInteractionByLegacyEnvelope(ctx, surface.ID, "env-missing"); err != nil || ok {
		t.Fatalf("missing envelope lookup = %v (found=%v)", err, ok)
	}
}

// TestTerminalOutcomeDeliveryAndAcknowledgementAreDistinctFacts is the
// contract's separation, asserted at the storage layer where it is enforced:
// recording a delivery twice is one delivery, acknowledgement is a separate
// idempotent fact, and neither can be asserted about a nonterminal record.
func TestTerminalOutcomeDeliveryAndAcknowledgementAreDistinctFacts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := openTestStore(t)
	surface := createTestSurface(t, store, "delivery-ack")

	params := testInteractionParams(surface.ID, "standalone-local", "workflow:tangent.triage:env-1", "ack")
	params.LegacyRoomID = surface.ID
	params.LegacyEnvelopeID = "env-1"
	created, err := store.CreateInteraction(ctx, params)
	if err != nil {
		t.Fatalf("CreateInteraction: %v", err)
	}

	// Neither fact may be asserted while the interaction is still open.
	if _, _, openErr := store.RecordTerminalOutcomeDelivery(ctx, RecordTerminalOutcomeDeliveryParams{
		InteractionID: created.Interaction.ID, LeaseOwner: "worker",
	}); !errors.Is(openErr, ErrNotRespondable) {
		t.Fatalf("delivery on an open interaction = %v, want ErrNotRespondable", openErr)
	}
	if _, openErr := store.AcknowledgeTerminalOutcome(ctx, AcknowledgeTerminalOutcomeParams{
		InteractionID: created.Interaction.ID, RequesterScope: "standalone-local",
	}); !errors.Is(openErr, ErrNotRespondable) {
		t.Fatalf("acknowledgement on an open interaction = %v, want ErrNotRespondable", openErr)
	}

	presented, err := store.AdvanceInteraction(ctx, AdvanceInteractionParams{
		InteractionID: created.Interaction.ID, ExpectedRevision: created.Interaction.Revision,
		To: InteractionStatePresented, PresentedProjectionRevision: 1,
		ParticipantScope: "operator:local", ParticipantRef: "local-operator",
		ParticipantAuthority: "local", ParticipantAssurance: "loopback-unverified",
		ActorRef: "local-operator", Authority: "local",
	})
	if err != nil {
		t.Fatalf("AdvanceInteraction: %v", err)
	}
	resolveParams := resolutionParams(presented, nil)
	resolveParams.Deliveries = []ResolutionDeliveryParams{{
		DestinationBinding: json.RawMessage(`{"kind":"caller_pull","caller_scope":"standalone-local"}`),
		IdempotencyKey:     "caller-pull",
		Policy:             json.RawMessage(`{"delivery":"durable-caller-pull"}`),
	}}
	if _, resolveErr := store.ResolveInteraction(ctx, resolveParams); resolveErr != nil {
		t.Fatalf("ResolveInteraction: %v", resolveErr)
	}

	claim, delivered, err := store.RecordTerminalOutcomeDelivery(ctx, RecordTerminalOutcomeDeliveryParams{
		InteractionID: created.Interaction.ID, LeaseOwner: "worker",
		Receipt: json.RawMessage(`{"transport":"mcp-tool-result"}`),
	})
	if err != nil || !delivered {
		t.Fatalf("RecordTerminalOutcomeDelivery = %v (delivered=%v)", err, delivered)
	}
	if claim.State != DeliveryStateDelivered {
		t.Fatalf("delivery state = %q, want delivered", claim.State)
	}

	// Handing the same immutable result back again is one delivery, not two.
	if _, repeatDelivered, repeatErr := store.RecordTerminalOutcomeDelivery(ctx, RecordTerminalOutcomeDeliveryParams{
		InteractionID: created.Interaction.ID, LeaseOwner: "worker",
		Receipt: json.RawMessage(`{"transport":"mcp-tool-result"}`),
	}); repeatErr != nil || repeatDelivered {
		t.Fatalf("repeat delivery = %v (delivered=%v), want a no-op", repeatErr, repeatDelivered)
	}
	assertSingleRow(t, store, `
SELECT COUNT(*) FROM delivery_attempts a
JOIN resolution_deliveries d ON d.id = a.resolution_delivery_id
JOIN resolutions r ON r.id = d.resolution_id
WHERE r.interaction_id = ?`, created.Interaction.ID)

	// Delivery is not acknowledgement.
	assertRowCountIs(t, store, 0,
		`SELECT COUNT(*) FROM terminal_outcome_acknowledgements WHERE interaction_id = ?`,
		created.Interaction.ID)

	first, err := store.AcknowledgeTerminalOutcome(ctx, AcknowledgeTerminalOutcomeParams{
		InteractionID: created.Interaction.ID, RequesterScope: "standalone-local",
		TransportCorrelation: json.RawMessage(`{"transport":"mcp"}`),
	})
	if err != nil || !first.Created {
		t.Fatalf("first acknowledgement = %#v err=%v", first, err)
	}
	second, err := store.AcknowledgeTerminalOutcome(ctx, AcknowledgeTerminalOutcomeParams{
		InteractionID: created.Interaction.ID, RequesterScope: "standalone-local",
	})
	if err != nil {
		t.Fatalf("second acknowledgement: %v", err)
	}
	if second.Created || second.ID != first.ID || !second.AcknowledgedAt.Equal(first.AcknowledgedAt) {
		t.Fatalf("acknowledgement is not idempotent: %#v then %#v", first, second)
	}
	assertSingleRow(t, store,
		`SELECT COUNT(*) FROM terminal_outcome_acknowledgements WHERE interaction_id = ?`,
		created.Interaction.ID)
	assertSingleRow(t, store, `
SELECT COUNT(*) FROM resolution_deliveries d
JOIN resolutions r ON r.id = d.resolution_id
WHERE r.interaction_id = ? AND d.lifecycle_state = 'acknowledged'`, created.Interaction.ID)
	assertSingleRow(t, store, `
SELECT COUNT(*) FROM delivery_events e
JOIN resolution_deliveries d ON d.id = e.resolution_delivery_id
JOIN resolutions r ON r.id = d.resolution_id
WHERE r.interaction_id = ? AND e.event_type = 'delivery.acknowledged'`, created.Interaction.ID)

	// The acknowledgement record itself is immutable.
	if _, err := store.db.ExecContext(ctx,
		`UPDATE terminal_outcome_acknowledgements SET requester_scope = 'rewritten' WHERE interaction_id = ?`,
		created.Interaction.ID,
	); err == nil {
		t.Fatal("acknowledgement update unexpectedly succeeded")
	}
	if _, err := store.db.ExecContext(ctx,
		`DELETE FROM terminal_outcome_acknowledgements WHERE interaction_id = ?`, created.Interaction.ID,
	); err == nil {
		t.Fatal("acknowledgement delete unexpectedly succeeded")
	}
}

func assertSingleRow(t *testing.T, store *Store, query string, args ...any) {
	t.Helper()
	assertRowCountIs(t, store, 1, query, args...)
}

func assertRowCountIs(t *testing.T, store *Store, want int, query string, args ...any) {
	t.Helper()
	var got int
	if err := store.db.QueryRowContext(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if got != want {
		t.Fatalf("count query %q = %d, want %d", query, got, want)
	}
}
