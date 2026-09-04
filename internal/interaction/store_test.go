package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

func TestCreateInteractionCollapsesRetriesAndAllocatesConcurrentOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, databasePath := openTestStore(t)
	_ = databasePath
	surface := createTestSurface(t, store, "surface-order")

	params := testInteractionParams(surface.ID, "caller-a", "request-1", "first")
	first, createErr := store.CreateInteraction(ctx, params)
	if createErr != nil {
		t.Fatalf("CreateInteraction first: %v", createErr)
	}
	if !first.Created || first.Interaction.SurfaceSequence != 1 || first.Interaction.Revision != 3 {
		t.Fatalf("first interaction = %#v", first)
	}

	retryParams := params
	retryParams.ID = "must-not-replace-original"
	retry, retryErr := store.CreateInteraction(ctx, retryParams)
	if retryErr != nil {
		t.Fatalf("CreateInteraction retry: %v", retryErr)
	}
	if retry.Created || retry.Interaction.ID != first.Interaction.ID || retry.Interaction.SurfaceSequence != 1 {
		t.Fatalf("retry = %#v, want original", retry)
	}
	otherSurface := createTestSurface(t, store, "surface-order-other")
	materializedRetry := params
	materializedRetry.SurfaceID = otherSurface.ID
	materializedRetry.Definition.Digest = "sha256:later-definition"
	materializedRetry.Definition.SchemaDigest = "sha256:later-schema"
	materializedRetry.Definition.HostVersion = "0.13.0"
	materializedRetry.CallerAuthority = "verified-gateway"
	materializedRetry.CallerAssurance = "authenticated"
	materializedRetry.ExternalRefs = json.RawMessage(`{"later":"projection"}`)
	materializedRetry.Policy = json.RawMessage(`{"later":"policy"}`)
	later, laterErr := store.CreateInteraction(ctx, materializedRetry)
	if laterErr != nil {
		t.Fatalf("same canonical request after later materialization: %v", laterErr)
	}
	if later.Created || later.Interaction.ID != first.Interaction.ID || later.Interaction.SurfaceID != surface.ID {
		t.Fatalf("later materialized retry = %#v, want original request record", later)
	}

	conflictParams := params
	conflictParams.RequestSnapshot = json.RawMessage(`{"summary":"different"}`)
	if _, err := store.CreateInteraction(ctx, conflictParams); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different request with same key error = %v, want ErrIdempotencyConflict", err)
	}

	const concurrent = 12
	sequences := make(chan int64, concurrent)
	errorsCh := make(chan error, concurrent)
	var wait sync.WaitGroup
	for i := range concurrent {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			result, err := store.CreateInteraction(
				ctx,
				testInteractionParams(surface.ID, "caller-a", fmt.Sprintf("request-%d", index+2), fmt.Sprintf("item-%d", index)),
			)
			if err != nil {
				errorsCh <- err
				return
			}
			sequences <- result.Interaction.SurfaceSequence
		}(i)
	}
	wait.Wait()
	close(errorsCh)
	close(sequences)
	for err := range errorsCh {
		t.Errorf("concurrent CreateInteraction: %v", err)
	}
	var got []int
	for sequence := range sequences {
		got = append(got, int(sequence))
	}
	sort.Ints(got)
	if len(got) != concurrent {
		t.Fatalf("created %d concurrent interactions, want %d", len(got), concurrent)
	}
	for index, sequence := range got {
		if sequence != index+2 {
			t.Fatalf("sequence[%d] = %d, want %d (all=%v)", index, sequence, index+2, got)
		}
	}

	updatedSurface, getErr := store.GetSurface(ctx, surface.ID)
	if getErr != nil {
		t.Fatalf("GetSurface: %v", getErr)
	}
	if updatedSurface.NextInteractionSequence != concurrent+2 {
		t.Fatalf("next sequence = %d, want %d", updatedSurface.NextInteractionSequence, concurrent+2)
	}
}

func TestHydrateSurfacePreservesOpenStateAcrossRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, databasePath := openTestStore(t)
	surface := createTestSurface(t, store, "surface-restart")
	created, createErr := store.CreateInteraction(ctx, testInteractionParams(surface.ID, "caller-a", "restart-key", "restart"))
	if createErr != nil {
		t.Fatalf("CreateInteraction: %v", createErr)
	}
	if err := store.db.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}

	database, openErr := tangentdb.Open(databasePath)
	if openErr != nil {
		t.Fatalf("reopen database: %v", openErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := tangentdb.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations after restart: %v", err)
	}
	restarted := NewStore(database)
	snapshot, err := restarted.HydrateSurface(ctx, surface.ID)
	if err != nil {
		t.Fatalf("HydrateSurface: %v", err)
	}
	if len(snapshot.Interactions) != 1 {
		t.Fatalf("hydrated interactions = %d, want 1", len(snapshot.Interactions))
	}
	got := snapshot.Interactions[0]
	if got.ID != created.Interaction.ID || got.State != InteractionStateStaged || got.TerminalAt != nil {
		t.Fatalf("hydrated interaction = %#v", got)
	}
}

func TestResolveInteractionIsAtomicCompareAndSetAndImmutable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := openTestStore(t)
	surface := createTestSurface(t, store, "surface-resolve")
	created, createErr := store.CreateInteraction(ctx, testInteractionParams(surface.ID, "caller-a", "resolve-key", "resolve"))
	if createErr != nil {
		t.Fatalf("CreateInteraction: %v", createErr)
	}
	presented, presentErr := store.AdvanceInteraction(ctx, AdvanceInteractionParams{
		InteractionID:               created.Interaction.ID,
		ExpectedRevision:            created.Interaction.Revision,
		To:                          InteractionStatePresented,
		PresentedProjectionRevision: 9,
		ParticipantScope:            "operator:local",
		ParticipantRef:              "local-operator",
		ParticipantAuthority:        "local",
		ParticipantAssurance:        "loopback-unverified",
		ConnectionID:                "connection-1",
		Authority:                   "loopback-unverified",
	})
	if presentErr != nil {
		t.Fatalf("AdvanceInteraction presented: %v", presentErr)
	}
	draftRevision := int64(1)
	if _, err := store.SaveDraftRevision(ctx, DraftRevision{
		InteractionID:        presented.ID,
		Revision:             draftRevision,
		InteractionRevision:  presented.Revision,
		ParticipantScope:     "operator:local",
		ParticipantRef:       "local-operator",
		ParticipantAuthority: "local",
		ParticipantAssurance: "loopback-unverified",
		DefinitionVersion:    presented.Definition.Version,
		Payload:              json.RawMessage(`{"note":"checked"}`),
	}); err != nil {
		t.Fatalf("SaveDraftRevision: %v", err)
	}
	inProgress, getErr := store.GetInteraction(ctx, presented.ID)
	if getErr != nil {
		t.Fatalf("GetInteraction after draft: %v", getErr)
	}
	if inProgress.State != InteractionStateInProgress || inProgress.Revision != presented.Revision+1 {
		t.Fatalf("draft did not atomically advance interaction: %#v", inProgress)
	}

	invalid := resolutionParams(inProgress, &draftRevision)
	invalid.Deliveries = append(invalid.Deliveries, invalid.Deliveries[0])
	if _, err := store.ResolveInteraction(ctx, invalid); err == nil {
		t.Fatal("ResolveInteraction with duplicate delivery unexpectedly succeeded")
	}
	afterRollback, reloadErr := store.GetInteraction(ctx, inProgress.ID)
	if reloadErr != nil {
		t.Fatalf("GetInteraction after rollback: %v", reloadErr)
	}
	if afterRollback.State != InteractionStateInProgress || afterRollback.Revision != inProgress.Revision {
		t.Fatalf("failed terminal transaction mutated interaction: %#v", afterRollback)
	}
	if _, err := store.GetResolution(ctx, inProgress.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolution after rollback error = %v, want ErrNotFound", err)
	}
	differentParticipant := resolutionParams(inProgress, &draftRevision)
	differentParticipant.ParticipantRef = "different-operator"
	if _, err := store.ResolveInteraction(ctx, differentParticipant); !errors.Is(err, ErrNotRespondable) {
		t.Fatalf("different participant resolution error = %v, want ErrNotRespondable", err)
	}
	afterParticipantMismatch, participantReloadErr := store.GetInteraction(ctx, inProgress.ID)
	if participantReloadErr != nil {
		t.Fatalf("GetInteraction after participant mismatch: %v", participantReloadErr)
	}
	if afterParticipantMismatch.State != InteractionStateInProgress ||
		afterParticipantMismatch.Revision != inProgress.Revision {
		t.Fatalf("participant mismatch mutated interaction: %#v", afterParticipantMismatch)
	}

	const contenders = 10
	results := make(chan ResolveInteractionResult, contenders)
	errorsCh := make(chan error, contenders)
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := store.ResolveInteraction(ctx, resolutionParams(inProgress, &draftRevision))
			if err != nil {
				errorsCh <- err
				return
			}
			results <- result
		}()
	}
	wait.Wait()
	close(results)
	close(errorsCh)
	if len(results) != 1 {
		t.Fatalf("successful resolutions = %d, want 1", len(results))
	}
	for err := range errorsCh {
		if !errors.Is(err, ErrTerminal) && !errors.Is(err, ErrRevisionConflict) {
			t.Errorf("competing resolution error = %v", err)
		}
	}
	winner := <-results
	if winner.Interaction.State != InteractionStateResolved || winner.Interaction.Revision != inProgress.Revision+1 {
		t.Fatalf("winner interaction = %#v", winner.Interaction)
	}
	if len(winner.Deliveries) != 1 || winner.Deliveries[0].State != DeliveryStateQueued {
		t.Fatalf("winner deliveries = %#v", winner.Deliveries)
	}
	if winner.Resolution.IntegrityDigest == "" || winner.Resolution.SourceDraftRevision == nil {
		t.Fatalf("winner resolution = %#v", winner.Resolution)
	}
	retrieval, retrievalErr := store.RecordTerminalOutcomeRetrieval(ctx, RecordTerminalOutcomeRetrievalParams{
		InteractionID:        winner.Interaction.ID,
		RequesterScope:       "caller-a",
		TransportCorrelation: json.RawMessage(`{"mcp_request_id":"request-42"}`),
	})
	if retrievalErr != nil {
		t.Fatalf("RecordTerminalOutcomeRetrieval: %v", retrievalErr)
	}
	if retrieval.ResolutionID != winner.Resolution.ID {
		t.Fatalf("retrieval resolution = %q, want %q", retrieval.ResolutionID, winner.Resolution.ID)
	}
	snapshot, hydrateErr := store.HydrateSurface(ctx, surface.ID)
	if hydrateErr != nil {
		t.Fatalf("HydrateSurface terminal records: %v", hydrateErr)
	}
	if len(snapshot.Drafts) != 1 || len(snapshot.Resolutions) != 1 ||
		len(snapshot.ResolutionDeliveries) != 1 || len(snapshot.OutcomeRetrievals) != 1 {
		t.Fatalf("terminal snapshot = %#v", snapshot)
	}

	if _, err := store.db.ExecContext(ctx, `UPDATE resolutions SET response_kind = 'rewritten' WHERE id = ?`, winner.Resolution.ID); err == nil {
		t.Fatal("immutable resolution update unexpectedly succeeded")
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE definition_bindings SET version = 'rewritten' WHERE interaction_id = ?`, inProgress.ID); err == nil {
		t.Fatal("immutable definition update unexpectedly succeeded")
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE interactions SET terminal_reason = 'rewritten', revision = revision + 1 WHERE id = ?`, inProgress.ID); err == nil {
		t.Fatal("terminal interaction update unexpectedly succeeded")
	}
}

func TestTerminalizeInteractionIsAtomicCompareAndSetAndDurable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := openTestStore(t)
	surface := createTestSurface(t, store, "surface-terminal")
	created, createErr := store.CreateInteraction(
		ctx,
		testInteractionParams(surface.ID, "caller-a", "terminal-key", "withdraw"),
	)
	if createErr != nil {
		t.Fatalf("CreateInteraction: %v", createErr)
	}
	params := TerminalizeInteractionParams{
		InteractionID:    created.Interaction.ID,
		ExpectedRevision: created.Interaction.Revision,
		To:               InteractionStateCanceled,
		Cause:            TerminalCauseCallerWithdrawn,
		Reason:           "caller withdrew request",
		ActorRef:         "caller-a",
		Authority:        "asserted",
		Notifications: []TerminalNotificationParams{{
			DestinationBinding: json.RawMessage(`{"kind":"retrieval","scope":"caller-a"}`),
			IdempotencyKey:     "terminal-primary",
			Policy:             json.RawMessage(`{"max_attempts":3}`),
		}},
	}
	for name, mutate := range map[string]func(*TerminalizeInteractionParams){
		"transport is not cancellation authority": func(candidate *TerminalizeInteractionParams) {
			candidate.Cause = TerminalCause("transport_disconnected")
		},
		"expiry cannot use caller cause": func(candidate *TerminalizeInteractionParams) {
			candidate.To = InteractionStateExpired
			candidate.PolicyRef = "request.expires_at"
		},
		"failure cannot use cancellation cause": func(candidate *TerminalizeInteractionParams) {
			candidate.To = InteractionStateFailed
			candidate.ErrorCode = "materialization_failed"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := params
			mutate(&candidate)
			if _, err := store.TerminalizeInteraction(ctx, candidate); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("TerminalizeInteraction error = %v, want ErrInvalidRecord", err)
			}
		})
	}
	if _, err := store.db.ExecContext(ctx, `
UPDATE interactions
SET lifecycle_state = 'canceled', terminal_cause = 'transport_disconnected',
    revision = revision + 1, updated_at = CURRENT_TIMESTAMP, terminal_at = CURRENT_TIMESTAMP
WHERE id = ?`, created.Interaction.ID); err == nil {
		t.Fatal("database accepted transport cancellation cause")
	}
	invalid := params
	invalid.Notifications = append(invalid.Notifications, invalid.Notifications[0])
	if _, err := store.TerminalizeInteraction(ctx, invalid); err == nil {
		t.Fatal("TerminalizeInteraction with duplicate notification unexpectedly succeeded")
	}
	afterRollback, reloadErr := store.GetInteraction(ctx, created.Interaction.ID)
	if reloadErr != nil {
		t.Fatalf("GetInteraction after terminal rollback: %v", reloadErr)
	}
	if afterRollback.State != InteractionStateStaged || afterRollback.Revision != created.Interaction.Revision {
		t.Fatalf("failed terminal transaction mutated interaction: %#v", afterRollback)
	}
	notifications, listErr := store.ListTerminalNotifications(ctx, created.Interaction.ID)
	if listErr != nil || len(notifications) != 0 {
		t.Fatalf("notifications after rollback = %#v, err %v", notifications, listErr)
	}

	const contenders = 8
	results := make(chan TerminalizeInteractionResult, contenders)
	errorsCh := make(chan error, contenders)
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := store.TerminalizeInteraction(ctx, params)
			if err != nil {
				errorsCh <- err
				return
			}
			results <- result
		}()
	}
	wait.Wait()
	close(results)
	close(errorsCh)
	if len(results) != 1 {
		t.Fatalf("successful terminal dispositions = %d, want 1", len(results))
	}
	for err := range errorsCh {
		if !errors.Is(err, ErrTerminal) && !errors.Is(err, ErrRevisionConflict) {
			t.Errorf("competing terminal disposition error = %v", err)
		}
	}
	winner := <-results
	if winner.Interaction.State != InteractionStateCanceled ||
		winner.Interaction.TerminalCause != TerminalCauseCallerWithdrawn ||
		len(winner.Notifications) != 1 || winner.Notifications[0].State != DeliveryStateQueued {
		t.Fatalf("winner terminal result = %#v", winner)
	}
	if _, err := store.RecordTerminalOutcomeRetrieval(ctx, RecordTerminalOutcomeRetrievalParams{
		InteractionID:        winner.Interaction.ID,
		RequesterScope:       "caller-a",
		TransportCorrelation: json.RawMessage(`{"operation":"get"}`),
	}); err != nil {
		t.Fatalf("RecordTerminalOutcomeRetrieval: %v", err)
	}
	snapshot, hydrateErr := store.HydrateSurface(ctx, surface.ID)
	if hydrateErr != nil {
		t.Fatalf("HydrateSurface terminal disposition: %v", hydrateErr)
	}
	if len(snapshot.TerminalNotifications) != 1 || len(snapshot.OutcomeRetrievals) != 1 {
		t.Fatalf("terminal disposition snapshot = %#v", snapshot)
	}
	if _, err := store.db.ExecContext(ctx, `
UPDATE terminal_notifications
SET terminal_cause = 'rewritten', revision = revision + 1
WHERE id = ?`, winner.Notifications[0].ID); err == nil {
		t.Fatal("immutable terminal notification identity update unexpectedly succeeded")
	}
}

func TestLegacyRoomHistoryCompatibilityProjection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	database, openErr := tangentdb.Open(databasePath)
	if openErr != nil {
		t.Fatalf("Open: %v", openErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := tangentdb.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	for range 3 {
		if err := tangentdb.RollbackOne(database); err != nil {
			t.Fatalf("RollbackOne: %v", err)
		}
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO rooms (id, meta, created_at, updated_at)
VALUES ('legacy-room', '{}', '2026-08-20T10:00:00Z', '2026-08-20T10:00:00Z')`); err != nil {
		t.Fatalf("insert legacy room: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO envelopes (
  room_id, envelope_id, type, request_payload, response_kind,
  response_payload, status, created_at, resolved_at
) VALUES (
  'legacy-room', 'legacy-envelope', 'tangent.triage', '{"items":[]}',
  'data', '{"decisions":[]}', 'submitted',
  '2026-08-20T10:00:01Z', '2026-08-20T10:00:02Z'
)`); err != nil {
		t.Fatalf("insert legacy envelope: %v", err)
	}
	if err := tangentdb.RunMigrations(database); err != nil {
		t.Fatalf("upgrade legacy database: %v", err)
	}
	store := NewStore(database)
	history, historyErr := store.ListLegacyRoomHistory(ctx, "legacy-room")
	if historyErr != nil {
		t.Fatalf("ListLegacyRoomHistory: %v", historyErr)
	}
	if len(history) != 1 || history[0].Status != "submitted" ||
		history[0].SurfaceID != "legacy-room" || history[0].InteractionID == "" {
		t.Fatalf("legacy history = %#v", history)
	}
}

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "tangent.db")
	database, openErr := tangentdb.Open(databasePath)
	if openErr != nil {
		t.Fatalf("db.Open: %v", openErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := tangentdb.RunMigrations(database); err != nil {
		t.Fatalf("db.RunMigrations: %v", err)
	}
	return NewStore(database), databasePath
}

func createTestSurface(t *testing.T, store *Store, id string) SurfaceRecord {
	t.Helper()
	surface, createErr := store.CreateSurface(context.Background(), CreateSurfaceParams{
		ID:         id,
		OwnerScope: "operator:local",
		Metadata:   json.RawMessage(`{"title":"Inbox"}`),
		Policy:     json.RawMessage(`{"retention":"durable-record"}`),
		ActorRef:   "test",
		Authority:  "test",
	})
	if createErr != nil {
		t.Fatalf("CreateSurface: %v", createErr)
	}
	active, activateErr := store.AdvanceSurface(context.Background(), AdvanceSurfaceParams{
		SurfaceID:        surface.ID,
		ExpectedRevision: surface.Revision,
		To:               SurfaceStateActive,
		ActorRef:         "test",
		Authority:        "test",
	})
	if activateErr != nil {
		t.Fatalf("AdvanceSurface active: %v", activateErr)
	}
	return active
}

func testInteractionParams(surfaceID, callerScope, idempotencyKey, summary string) CreateInteractionParams {
	return CreateInteractionParams{
		SurfaceID:          surfaceID,
		CallerScope:        callerScope,
		CallerPrincipalRef: "agent:test",
		CallerAuthority:    "direct-mcp",
		CallerAssurance:    "asserted",
		IdempotencyKey:     idempotencyKey,
		Definition: DefinitionBinding{
			Publisher:      "hollis-labs/tangent",
			Kind:           "tangent.hitl-item",
			Version:        "1.0",
			Revision:       "1",
			Digest:         "sha256:definition",
			Source:         "built-in",
			SchemaIdentity: "tangent.hitl-item.request.v1",
			SchemaDigest:   "sha256:schema",
			HostVersion:    "0.12.0",
			Assurance:      "built-in",
		},
		RequestSnapshot: json.RawMessage(fmt.Sprintf(`{"summary":%q}`, summary)),
		ExternalRefs:    json.RawMessage(`{"task":{"id":"task-1"}}`),
		Policy:          json.RawMessage(`{"expiry":"none"}`),
		ActorRef:        "caller-a",
		Authority:       "asserted",
	}
}

func resolutionParams(interaction InteractionRecord, sourceDraftRevision *int64) ResolveInteractionParams {
	return ResolveInteractionParams{
		InteractionID:               interaction.ID,
		ExpectedInteractionRevision: interaction.Revision,
		PresentedProjectionRevision: *interaction.PresentedProjectionRevision,
		ParticipantScope:            "operator:local",
		ParticipantRef:              "local-operator",
		ParticipantAuthority:        "local",
		ParticipantAssurance:        "loopback-unverified",
		ResponseKind:                "approval",
		ResponsePayload:             json.RawMessage(`{"kind":"approval","decision":"approved","note":"ship it"}`),
		SourceDraftRevision:         sourceDraftRevision,
		Deliveries: []ResolutionDeliveryParams{{
			DestinationBinding: json.RawMessage(`{"kind":"caller","scope":"caller-a"}`),
			IdempotencyKey:     "primary",
			Policy:             json.RawMessage(`{"max_attempts":3}`),
		}},
	}
}
