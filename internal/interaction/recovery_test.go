package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

func TestRestartRecoveryPreservesEveryLifecycleAndResumesIdempotently(t *testing.T) {
	ctx := context.Background()
	store, databasePath := openTestStore(t)
	service := newTestService(t, store)
	surface := mustOpenSurface(t, service, "complete-restart-fixture")

	staged := mustSubmit(t, service, surface.SurfaceID, "restart-staged")
	presented := presentForRecovery(t, service, surface.SurfaceID, "restart-presented")
	draftBearing := presentForRecovery(t, service, surface.SurfaceID, "restart-draft")
	if _, err := service.SaveDraft(ctx, SaveDraftInput{
		InteractionID: draftBearing.InteractionID, DraftRevision: 1,
		InteractionRevision: draftBearing.Revision, Participant: testParticipant(),
		DefinitionVersion: "1.0", Payload: json.RawMessage(`{"answer":"still drafting"}`),
	}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	interruptedSafe := resolveForRecovery(t, service, surface.SurfaceID, "restart-safe", nil)
	markDeliveryDelivering(t, store.db, "resolution_deliveries", interruptedSafe.Deliveries[0].ID)
	interruptedManual := resolveForRecovery(t, service, surface.SurfaceID, "restart-manual", []ResolutionDeliveryParams{{
		DestinationBinding: json.RawMessage(`{"kind":"external","destination":"opaque"}`),
		IdempotencyKey:     "external:manual-reconciliation",
		Policy:             json.RawMessage(`{"restart_recovery":"manual"}`),
	}})
	markDeliveryDelivering(t, store.db, "resolution_deliveries", interruptedManual.Deliveries[0].ID)
	spoofedCallerPull := resolveForRecovery(t, service, surface.SurfaceID, "restart-spoofed-caller-pull", []ResolutionDeliveryParams{{
		DestinationBinding: json.RawMessage(`{"kind":"external","destination":"not-caller-pull"}`),
		IdempotencyKey:     "external:spoofed-caller-pull",
		Policy:             json.RawMessage(`{"delivery":"durable-caller-pull"}`),
	}})
	markDeliveryDelivering(t, store.db, "resolution_deliveries", spoofedCallerPull.Deliveries[0].ID)
	manualOverridesCallerPull := resolveForRecovery(t, service, surface.SurfaceID, "restart-manual-override", []ResolutionDeliveryParams{{
		DestinationBinding: json.RawMessage(`{"kind":"caller_pull","caller_scope":"caller:test"}`),
		IdempotencyKey:     "caller-pull:manual-override",
		Policy: json.RawMessage(
			`{"delivery":"durable-caller-pull","restart_recovery":"manual"}`,
		),
	}})
	markDeliveryDelivering(t, store.db, "resolution_deliveries", manualOverridesCallerPull.Deliveries[0].ID)
	explicitIdempotent := resolveForRecovery(t, service, surface.SurfaceID, "restart-explicit-idempotent", []ResolutionDeliveryParams{{
		DestinationBinding: json.RawMessage(`{"kind":"external","destination":"idempotent-api"}`),
		IdempotencyKey:     "external:explicit-idempotent",
		Policy:             json.RawMessage(`{"restart_recovery":"idempotent_retry"}`),
	}})
	markDeliveryDelivering(t, store.db, "resolution_deliveries", explicitIdempotent.Deliveries[0].ID)
	delivered := resolveForRecovery(t, service, surface.SurfaceID, "restart-delivered", nil)
	markDeliveryDelivered(t, store.db, "resolution_deliveries", delivered.Deliveries[0].ID)
	humanResponse := resolveForRecovery(t, service, surface.SurfaceID, "restart-human-response", nil)
	revoked := resolveForRecovery(t, service, surface.SurfaceID, "restart-revoked", []ResolutionDeliveryParams{{
		DestinationBinding: json.RawMessage(`{"kind":"external","destination":"revoked"}`),
		IdempotencyKey:     "external:revoked",
		Policy:             json.RawMessage(`{"restart_recovery":"manual"}`),
	}})
	markDeliveryTerminalFailure(t, store.db, "resolution_deliveries", revoked.Deliveries[0].ID)

	canceled := mustSubmit(t, service, surface.SurfaceID, "restart-canceled")
	if _, err := service.CancelInteraction(ctx, CancelInteractionInput{
		InteractionID: canceled.InteractionID, ExpectedRevision: canceled.Revision,
		Requester: testCaller(), Cause: TerminalCauseCallerCanceled,
	}); err != nil {
		t.Fatalf("CancelInteraction: %v", err)
	}
	canceledNotifications, err := store.ListTerminalNotifications(ctx, canceled.InteractionID)
	if err != nil || len(canceledNotifications) != 1 {
		t.Fatalf("canceled notifications = %#v, %v", canceledNotifications, err)
	}
	markDeliveryDelivering(t, store.db, "terminal_notifications", canceledNotifications[0].ID)
	expiring := mustSubmit(t, service, surface.SurfaceID, "restart-expired")
	if _, expireErr := service.ExpireInteraction(ctx, ExpireInteractionInput{
		InteractionID: expiring.InteractionID, ExpectedRevision: expiring.Revision,
		PolicyRef: "policy:test-expiry", Reason: "expired before restart",
		Actor: ActorBinding{
			Scope: "host", PrincipalRef: "scheduler", Authority: "host-policy", Assurance: "trusted",
		},
	}); expireErr != nil {
		t.Fatalf("ExpireInteraction: %v", expireErr)
	}

	if closeErr := store.db.Close(); closeErr != nil {
		t.Fatalf("close pre-restart database: %v", closeErr)
	}
	restartedDB, err := tangentdb.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() { _ = restartedDB.Close() })
	if migrationErr := tangentdb.RunMigrations(restartedDB); migrationErr != nil {
		t.Fatalf("RunMigrations after restart: %v", migrationErr)
	}
	restartedStore := NewStore(restartedDB)
	restarted := newTestService(t, restartedStore)
	report, err := restarted.RecoverAfterRestart(ctx)
	if err != nil {
		t.Fatalf("RecoverAfterRestart: %v", err)
	}
	if report.Staged != 1 || report.Presented != 1 || report.DraftBearing != 1 ||
		report.ResolvedUndelivered != 6 || report.DeliveredUnacknowledged != 1 ||
		report.PreservedTerminalInteractions != 2 || report.TerminalFailedDeliveries != 1 ||
		report.InterruptedDeliveries != 6 ||
		report.ImmediatelyRetryable != 3 || report.ManualReconciliationRequired != 3 {
		t.Fatalf("recovery report = %#v", report)
	}

	snapshot, err := restartedStore.HydrateSurface(ctx, surface.SurfaceID)
	if err != nil {
		t.Fatalf("HydrateSurface after restart: %v", err)
	}
	assertRecoveredInteractionState(t, snapshot, staged.InteractionID, InteractionStateStaged)
	assertRecoveredInteractionState(t, snapshot, presented.InteractionID, InteractionStatePresented)
	assertRecoveredInteractionState(t, snapshot, draftBearing.InteractionID, InteractionStateInProgress)
	assertRecoveredInteractionState(t, snapshot, canceled.InteractionID, InteractionStateCanceled)
	assertRecoveredInteractionState(t, snapshot, expiring.InteractionID, InteractionStateExpired)
	if len(snapshot.Drafts) != 1 || string(snapshot.Drafts[0].Payload) != `{"answer":"still drafting"}` {
		t.Fatalf("recovered drafts = %#v", snapshot.Drafts)
	}
	assertDeliveryState(t, snapshot.ResolutionDeliveries, interruptedSafe.Deliveries[0].ID, DeliveryStateRetryableFailure, true)
	assertDeliveryState(t, snapshot.ResolutionDeliveries, interruptedManual.Deliveries[0].ID, DeliveryStateRetryableFailure, false)
	assertDeliveryState(t, snapshot.ResolutionDeliveries, spoofedCallerPull.Deliveries[0].ID, DeliveryStateRetryableFailure, false)
	assertDeliveryState(t, snapshot.ResolutionDeliveries, manualOverridesCallerPull.Deliveries[0].ID, DeliveryStateRetryableFailure, false)
	assertDeliveryState(t, snapshot.ResolutionDeliveries, explicitIdempotent.Deliveries[0].ID, DeliveryStateRetryableFailure, true)
	assertDeliveryState(t, snapshot.ResolutionDeliveries, delivered.Deliveries[0].ID, DeliveryStateDelivered, false)
	assertDeliveryState(t, snapshot.ResolutionDeliveries, revoked.Deliveries[0].ID, DeliveryStateTerminalFailure, false)
	if len(snapshot.DeliveryAttempts) != 6 {
		t.Fatalf("recovered delivery attempts = %d, want 6", len(snapshot.DeliveryAttempts))
	}
	for _, attempt := range snapshot.DeliveryAttempts {
		if attempt.Status != DeliveryStateRetryableFailure || attempt.ErrorCode != recoveryErrorCode ||
			attempt.CompletedAt == nil {
			t.Fatalf("restart attempt = %#v", attempt)
		}
	}

	// A terminal human response is available from a new service immediately;
	// retrieval neither depends on nor marks its caller-pull delivery.
	got, err := restarted.GetInteraction(ctx, GetInteractionInput{
		InteractionID: humanResponse.Interaction.ID, RequesterScope: testCaller().Scope,
	})
	if err != nil || got.Resolution == nil ||
		string(got.Resolution.ResponsePayload) != `{"decision":"restart-human-response"}` {
		t.Fatalf("GetInteraction preserved response = %#v, %v", got, err)
	}
	awaited, err := restarted.AwaitResolution(ctx, AwaitResolutionInput{
		InteractionID: humanResponse.Interaction.ID, RequesterScope: testCaller().Scope,
		MaximumWait: 50 * time.Millisecond,
	})
	if err != nil || awaited.Resolution == nil || awaited.Resolution.ID != got.Resolution.ID {
		t.Fatalf("AwaitResolution preserved response = %#v, %v", awaited, err)
	}

	// Simulate a destination adapter. Every eligible obligation is performed
	// once with its durable idempotency key. Delivered, revoked, and manually
	// paused unknown outcomes must never be reclaimed.
	effects := map[string]int{delivered.Deliveries[0].IdempotencyKey: 1}
	for {
		claim, claimErr := restarted.ClaimNextDelivery(ctx, ClaimDeliveryInput{
			Worker: testDeliveryWorker(), LeaseDuration: time.Minute,
		})
		if errors.Is(claimErr, ErrNoDeliveryAvailable) {
			break
		}
		if claimErr != nil {
			t.Fatalf("ClaimNextDelivery: %v", claimErr)
		}
		effects[claim.IdempotencyKey]++
		completed, completeErr := restarted.CompleteDelivery(ctx, CompleteDeliveryInput{
			Worker: testDeliveryWorker(), Kind: claim.Kind, ID: claim.ID,
			ExpectedRevision: claim.Revision, To: DeliveryStateDelivered,
			Receipt: json.RawMessage(fmt.Sprintf(`{"idempotency_key":%q}`, claim.IdempotencyKey)),
		})
		if completeErr != nil || completed.State != DeliveryStateDelivered ||
			completed.AttemptNumber != claim.AttemptNumber {
			t.Fatalf("CompleteDelivery = %#v, %v (claim %#v)", completed, completeErr, claim)
		}
	}
	if effects[delivered.Deliveries[0].IdempotencyKey] != 1 {
		t.Fatalf("already-delivered effect repeated: %#v", effects)
	}
	if effects[interruptedSafe.Deliveries[0].IdempotencyKey] != 1 ||
		effects[canceledNotifications[0].IdempotencyKey] != 1 ||
		effects[humanResponse.Deliveries[0].IdempotencyKey] != 1 ||
		effects[explicitIdempotent.Deliveries[0].IdempotencyKey] != 1 {
		t.Fatalf("eligible obligations were not resumed exactly once: %#v", effects)
	}
	if effects[interruptedManual.Deliveries[0].IdempotencyKey] != 0 ||
		effects[spoofedCallerPull.Deliveries[0].IdempotencyKey] != 0 ||
		effects[manualOverridesCallerPull.Deliveries[0].IdempotencyKey] != 0 ||
		effects[revoked.Deliveries[0].IdempotencyKey] != 0 {
		t.Fatalf("unsafe or revoked effect was replayed: %#v", effects)
	}

	secondReport, err := restarted.RecoverAfterRestart(ctx)
	if err != nil {
		t.Fatalf("second RecoverAfterRestart: %v", err)
	}
	if secondReport.InterruptedDeliveries != 0 || secondReport.ManualReconciliationRequired != 0 ||
		secondReport.ImmediatelyRetryable != 0 {
		t.Fatalf("idempotent recovery report = %#v", secondReport)
	}
	if _, err := restarted.ClaimNextDelivery(ctx, ClaimDeliveryInput{
		Worker: testDeliveryWorker(), LeaseDuration: time.Minute,
	}); !errors.Is(err, ErrNoDeliveryAvailable) {
		t.Fatalf("claim after all safe delivery completion error = %v, want ErrNoDeliveryAvailable", err)
	}
}

func TestDeliveryClaimAndCompletionAreConcurrentCompareAndSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := mustOpenSurface(t, service, "delivery-cas-surface")
	resolved := resolveForRecovery(t, service, surface.SurfaceID, "delivery-cas", nil)

	const contenders = 12
	claims := make(chan DeliveryClaim, contenders)
	errorsCh := make(chan error, contenders)
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			claim, err := service.ClaimNextDelivery(ctx, ClaimDeliveryInput{
				Worker: testDeliveryWorker(), LeaseDuration: time.Minute,
			})
			if err != nil {
				errorsCh <- err
				return
			}
			claims <- claim
		}()
	}
	wait.Wait()
	close(claims)
	close(errorsCh)
	var claimed []DeliveryClaim
	for claim := range claims {
		claimed = append(claimed, claim)
	}
	noWork := 0
	for err := range errorsCh {
		if !errors.Is(err, ErrNoDeliveryAvailable) {
			t.Errorf("concurrent claim error = %v", err)
		}
		noWork++
	}
	if len(claimed) != 1 || noWork != contenders-1 || claimed[0].ID != resolved.Deliveries[0].ID {
		t.Fatalf("claims=%#v noWork=%d", claimed, noWork)
	}
	claimedSnapshot, err := store.HydrateSurface(ctx, surface.SurfaceID)
	if err != nil {
		t.Fatalf("HydrateSurface after claim: %v", err)
	}
	if len(claimedSnapshot.DeliveryAttempts) != 1 ||
		claimedSnapshot.DeliveryAttempts[0].ID != claimed[0].AttemptID ||
		claimedSnapshot.DeliveryAttempts[0].Status != DeliveryStateDelivering ||
		claimedSnapshot.DeliveryAttempts[0].CompletedAt != nil {
		t.Fatalf("persist-before-effect attempt = %#v, claim %#v", claimedSnapshot.DeliveryAttempts, claimed[0])
	}
	assertDeliveryEventTypes(t, claimedSnapshot.DeliveryEvents, "delivery.queued", "delivery.started")

	completionErrors := make(chan error, contenders)
	successes := make(chan DeliveryClaim, contenders)
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, completeErr := service.CompleteDelivery(ctx, CompleteDeliveryInput{
				Worker: testDeliveryWorker(), Kind: claimed[0].Kind, ID: claimed[0].ID,
				ExpectedRevision: claimed[0].Revision, To: DeliveryStateDelivered,
				Receipt: json.RawMessage(`{}`),
			})
			if completeErr != nil {
				completionErrors <- completeErr
				return
			}
			successes <- result
		}()
	}
	wait.Wait()
	close(completionErrors)
	close(successes)
	completed := 0
	for range successes {
		completed++
	}
	conflicts := 0
	for err := range completionErrors {
		if !errors.Is(err, ErrRevisionConflict) {
			t.Errorf("concurrent completion error = %v", err)
		}
		conflicts++
	}
	if completed != 1 || conflicts != contenders-1 {
		t.Fatalf("completed=%d conflicts=%d, want 1/%d", completed, conflicts, contenders-1)
	}
	snapshot, err := store.HydrateSurface(ctx, surface.SurfaceID)
	if err != nil || len(snapshot.DeliveryAttempts) != 1 ||
		snapshot.DeliveryAttempts[0].ID != claimed[0].AttemptID ||
		snapshot.DeliveryAttempts[0].Status != DeliveryStateDelivered ||
		string(snapshot.DeliveryAttempts[0].Receipt) != `{}` ||
		snapshot.DeliveryAttempts[0].CompletedAt == nil {
		t.Fatalf("delivery after concurrent completion = %#v, %v", snapshot.DeliveryAttempts, err)
	}
	assertDeliveryEventTypes(t, snapshot.DeliveryEvents, "delivery.queued", "delivery.started", "delivery.delivered")
}

func TestDeliveryWorkerAuthorityDefaultsDenyAndBindsFullActor(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	service, err := NewService(store, testDefinitionCatalog{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, claimErr := service.ClaimNextDelivery(context.Background(), ClaimDeliveryInput{
		Worker: testDeliveryWorker(), LeaseDuration: time.Minute,
	}); !errors.Is(claimErr, ErrUnauthorized) {
		t.Fatalf("default delivery authority error = %v, want ErrUnauthorized", claimErr)
	}
	forged := testDeliveryWorker()
	forged.Assurance = "asserted"
	configured, err := NewService(
		store, testDefinitionCatalog{}, WithDeliveryWorkerPolicy(testDeliveryWorkerPolicy{}),
	)
	if err != nil {
		t.Fatalf("NewService configured: %v", err)
	}
	if _, err := configured.ClaimNextDelivery(context.Background(), ClaimDeliveryInput{
		Worker: forged, LeaseDuration: time.Minute,
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("forged delivery authority error = %v, want ErrUnauthorized", err)
	}
}

func presentForRecovery(t *testing.T, service *Service, surfaceID, key string) InteractionHandle {
	t.Helper()
	handle := mustSubmit(t, service, surfaceID, key)
	presented, err := service.AcknowledgePresentation(context.Background(), PresentInteractionInput{
		InteractionID: handle.InteractionID, ExpectedRevision: handle.Revision,
		PresentedProjectionRevision: 1, Participant: testParticipant(),
	})
	if err != nil {
		t.Fatalf("AcknowledgePresentation %s: %v", key, err)
	}
	return presented
}

func resolveForRecovery(
	t *testing.T,
	service *Service,
	surfaceID string,
	key string,
	deliveries []ResolutionDeliveryParams,
) ResolveInteractionResult {
	t.Helper()
	presented := presentForRecovery(t, service, surfaceID, key)
	result, err := service.ResolveInteraction(context.Background(), ResolveInteractionInput{
		InteractionID: presented.InteractionID, ExpectedInteractionRevision: presented.Revision,
		PresentedProjectionRevision: 1, Participant: testParticipant(), ResponseKind: "data",
		ResponsePayload: json.RawMessage(fmt.Sprintf(`{"decision":%q}`, key)),
		Deliveries:      deliveries,
	})
	if err != nil {
		t.Fatalf("ResolveInteraction %s: %v", key, err)
	}
	return result
}

func markDeliveryDelivering(t *testing.T, database *sql.DB, table, id string) {
	t.Helper()
	now := time.Now().UTC()
	query := deliveryFixtureUpdate(t, table, `UPDATE resolution_deliveries
SET lifecycle_state = 'delivering', revision = revision + 1,
    lease_owner = 'dead-process', lease_expires_at = ?, updated_at = ?
WHERE id = ?`, `UPDATE terminal_notifications
SET lifecycle_state = 'delivering', revision = revision + 1,
    lease_owner = 'dead-process', lease_expires_at = ?, updated_at = ?
WHERE id = ?`)
	if _, err := database.Exec(query, now.Add(time.Minute), now, id); err != nil {
		t.Fatalf("mark %s delivery %s delivering: %v", table, id, err)
	}
}

func markDeliveryDelivered(t *testing.T, database *sql.DB, table, id string) {
	t.Helper()
	markDeliveryDelivering(t, database, table, id)
	now := time.Now().UTC()
	query := deliveryFixtureUpdate(t, table, `UPDATE resolution_deliveries
SET lifecycle_state = 'delivered', revision = revision + 1,
    lease_owner = NULL, lease_expires_at = NULL,
    receipt = '{"transport":"before-restart"}', delivered_at = ?, updated_at = ?
WHERE id = ?`, `UPDATE terminal_notifications
SET lifecycle_state = 'delivered', revision = revision + 1,
    lease_owner = NULL, lease_expires_at = NULL,
    receipt = '{"transport":"before-restart"}', delivered_at = ?, updated_at = ?
WHERE id = ?`)
	if _, err := database.Exec(query, now, now, id); err != nil {
		t.Fatalf("mark %s delivery %s delivered: %v", table, id, err)
	}
}

func markDeliveryTerminalFailure(t *testing.T, database *sql.DB, table, id string) {
	t.Helper()
	markDeliveryDelivering(t, database, table, id)
	now := time.Now().UTC()
	query := deliveryFixtureUpdate(t, table, `UPDATE resolution_deliveries
SET lifecycle_state = 'terminal_failure', revision = revision + 1,
    lease_owner = NULL, lease_expires_at = NULL,
    terminal_reason = 'destination capability revoked', updated_at = ?
WHERE id = ?`, `UPDATE terminal_notifications
SET lifecycle_state = 'terminal_failure', revision = revision + 1,
    lease_owner = NULL, lease_expires_at = NULL,
    terminal_reason = 'destination capability revoked', updated_at = ?
WHERE id = ?`)
	if _, err := database.Exec(query, now, id); err != nil {
		t.Fatalf("mark %s delivery %s terminal failure: %v", table, id, err)
	}
}

func deliveryFixtureUpdate(t *testing.T, table, resolutionQuery, notificationQuery string) string {
	t.Helper()
	switch table {
	case "resolution_deliveries":
		return resolutionQuery
	case "terminal_notifications":
		return notificationQuery
	default:
		t.Fatalf("unknown delivery fixture table %q", table)
		return ""
	}
}

func assertRecoveredInteractionState(
	t *testing.T,
	snapshot SurfaceSnapshot,
	id string,
	want InteractionState,
) {
	t.Helper()
	for _, interaction := range snapshot.Interactions {
		if interaction.ID == id {
			if interaction.State != want {
				t.Fatalf("interaction %s state = %s, want %s", id, interaction.State, want)
			}
			return
		}
	}
	t.Fatalf("interaction %s missing from recovered snapshot", id)
}

func assertDeliveryState(
	t *testing.T,
	deliveries []ResolutionDeliveryRecord,
	id string,
	want DeliveryState,
	wantNextEligible bool,
) {
	t.Helper()
	for _, delivery := range deliveries {
		if delivery.ID == id {
			if delivery.State != want || (delivery.NextEligibleAt != nil) != wantNextEligible {
				t.Fatalf("delivery %s = %#v, want state %s nextEligible=%v", id, delivery, want, wantNextEligible)
			}
			return
		}
	}
	t.Fatalf("delivery %s missing", id)
}

func assertDeliveryEventTypes(t *testing.T, events []DeliveryEvent, want ...string) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("delivery events = %#v, want types %#v", events, want)
	}
	for index, event := range events {
		if event.Type != want[index] {
			t.Fatalf("delivery event %d = %#v, want type %q", index, event, want[index])
		}
	}
}
