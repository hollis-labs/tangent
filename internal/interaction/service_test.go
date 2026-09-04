package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

type testDefinitionCatalog struct{}

type unavailableDefinitionCatalog struct{}

type testPrivilegedActorPolicy struct{}

type testDeliveryWorkerPolicy struct{}

func (testPrivilegedActorPolicy) AuthorizeHostPolicy(actor ActorBinding) bool {
	return actor == (ActorBinding{
		Scope: "host", PrincipalRef: "scheduler", Authority: "host-policy", Assurance: "trusted",
	})
}

func (testPrivilegedActorPolicy) AuthorizeAdministrator(actor ActorBinding) bool {
	return actor == (ActorBinding{
		Scope: "host:administration", PrincipalRef: "admin:test",
		Authority: "administrator", Assurance: "trusted",
	})
}

func (testDeliveryWorkerPolicy) AuthorizeDeliveryWorker(actor ActorBinding) bool {
	return actor == testDeliveryWorker()
}

func (testDefinitionCatalog) ListInteractionKinds(context.Context) ([]InteractionKind, error) {
	binding, _ := testDefinitionCatalog{}.ResolveInteractionDefinition(context.Background(), DefinitionRef{Kind: "test.generic"})
	return []InteractionKind{{Kind: binding.Kind, Version: binding.Version, ResponseKind: "data", Binding: binding}}, nil
}

func (testDefinitionCatalog) ResolveInteractionDefinition(
	_ context.Context,
	ref DefinitionRef,
) (DefinitionBinding, error) {
	if ref.Kind != "test.generic" || (ref.Version != "" && ref.Version != "1.0") {
		return DefinitionBinding{}, ErrDefinitionNotFound
	}
	return DefinitionBinding{
		Publisher: "test", Kind: "test.generic", Version: "1.0", Revision: 1,
		Digest: "sha256:test-definition", Source: "test", SchemaIdentity: "test.generic@1.0#data",
		SchemaDigest: "sha256:test-schema", HostVersion: "test", Assurance: "test",
	}, nil
}

// RetainDefinitionMaterial is a no-op for the stub catalogs: they synthesize a
// binding rather than resolving one from registered material, so there is
// nothing to retain. The production catalog is covered by
// TestPinnedInteractionReplaysAfterVersionChange.
func (testDefinitionCatalog) RetainDefinitionMaterial(context.Context, DefinitionBinding) error {
	return nil
}

func (unavailableDefinitionCatalog) RetainDefinitionMaterial(context.Context, DefinitionBinding) error {
	return nil
}

func (testDefinitionCatalog) ValidateInteractionRequest(
	_ context.Context,
	_ DefinitionBinding,
	request json.RawMessage,
) error {
	var value map[string]any
	if err := json.Unmarshal(request, &value); err != nil {
		return err
	}
	if _, ok := value["prompt"]; !ok {
		return errors.New("prompt is required")
	}
	return nil
}

func (testDefinitionCatalog) ValidateInteractionResponse(
	_ context.Context,
	binding DefinitionBinding,
	kind string,
	payload json.RawMessage,
) error {
	if binding.Digest != "sha256:test-definition" {
		return ErrDefinitionUnavailable
	}
	if kind != "data" || !json.Valid(payload) {
		return errors.New("invalid response")
	}
	return nil
}

func (unavailableDefinitionCatalog) ListInteractionKinds(context.Context) ([]InteractionKind, error) {
	return nil, ErrDefinitionUnavailable
}

func (unavailableDefinitionCatalog) ResolveInteractionDefinition(
	context.Context,
	DefinitionRef,
) (DefinitionBinding, error) {
	return DefinitionBinding{}, ErrDefinitionUnavailable
}

func (unavailableDefinitionCatalog) ValidateInteractionRequest(
	context.Context,
	DefinitionBinding,
	json.RawMessage,
) error {
	return ErrDefinitionUnavailable
}

func (unavailableDefinitionCatalog) ValidateInteractionResponse(
	context.Context,
	DefinitionBinding,
	string,
	json.RawMessage,
) error {
	return ErrDefinitionUnavailable
}

func TestServiceOpenSurfaceIsDurablyIdempotentAndConcurrent(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	input := testOpenSurfaceInput("surface-open-key")

	const callers = 12
	results := make(chan SurfaceHandle, callers)
	errorsCh := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			handle, err := service.OpenSurface(context.Background(), input)
			if err != nil {
				errorsCh <- err
				return
			}
			results <- handle
		}()
	}
	wait.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		t.Errorf("OpenSurface: %v", err)
	}
	var surfaceID string
	created := 0
	count := 0
	for result := range results {
		count++
		if surfaceID == "" {
			surfaceID = result.SurfaceID
		}
		if result.SurfaceID != surfaceID || result.State != SurfaceStateActive || result.Revision != 2 {
			t.Errorf("surface handle = %#v, want same active durable handle", result)
		}
		if result.Created {
			created++
		}
	}
	if count != callers || created != 1 {
		t.Fatalf("results=%d created=%d, want %d/1", count, created, callers)
	}
	snapshot, err := service.GetSurface(context.Background(), GetSurfaceInput{
		SurfaceID: surfaceID, RequesterScope: input.Caller.Scope,
	})
	if err != nil || snapshot.Surface.ID != surfaceID {
		t.Fatalf("opening scope could not resume surface by handle: %#v, %v", snapshot.Surface, err)
	}
	if _, getErr := service.GetSurface(context.Background(), GetSurfaceInput{
		SurfaceID: surfaceID, RequesterScope: "application:intruder",
	}); !errors.Is(getErr, ErrUnauthorized) {
		t.Fatalf("intruder surface get error = %v, want ErrUnauthorized", getErr)
	}

	conflict := input
	conflict.Metadata = json.RawMessage(`{"title":"different"}`)
	if _, conflictErr := service.OpenSurface(context.Background(), conflict); !errors.Is(conflictErr, ErrIdempotencyConflict) {
		t.Fatalf("conflicting open error = %v, want ErrIdempotencyConflict", conflictErr)
	}
	differentScope := input
	differentScope.Caller.Scope = "application:other"
	differentScope.Caller.PrincipalRef = "agent:other"
	other, err := service.OpenSurface(context.Background(), differentScope)
	if err != nil || other.SurfaceID == surfaceID {
		t.Fatalf("other caller scope open = %#v, %v", other, err)
	}
}

func TestServiceSubmitReturnsImmediateStableHandleAcrossRestart(t *testing.T) {
	t.Parallel()
	store, databasePath := openTestStore(t)
	service := newTestService(t, store)
	surface := mustOpenSurface(t, service, "restart-surface")
	input := testSubmitInput(surface.SurfaceID, "restart-interaction")

	handle, err := service.SubmitInteraction(context.Background(), input)
	if err != nil {
		t.Fatalf("SubmitInteraction: %v", err)
	}
	if handle.InteractionID == "" || handle.State != InteractionStateStaged || handle.Revision != 3 ||
		handle.SurfaceSequence != 1 || !handle.Created {
		t.Fatalf("immediate handle = %#v", handle)
	}
	retry, err := service.SubmitInteraction(context.Background(), input)
	if err != nil {
		t.Fatalf("SubmitInteraction retry: %v", err)
	}
	if retry.InteractionID != handle.InteractionID || retry.SurfaceSequence != handle.SurfaceSequence || retry.Created {
		t.Fatalf("retry handle = %#v, want original", retry)
	}

	if closeErr := store.db.Close(); closeErr != nil {
		t.Fatalf("close database: %v", closeErr)
	}
	database, err := tangentdb.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrationErr := tangentdb.RunMigrations(database); migrationErr != nil {
		t.Fatalf("RunMigrations after restart: %v", migrationErr)
	}
	restarted := newTestService(t, NewStore(database))
	reopened, err := restarted.OpenSurface(context.Background(), testOpenSurfaceInput("restart-surface"))
	if err != nil || reopened.SurfaceID != surface.SurfaceID || reopened.Created {
		t.Fatalf("OpenSurface retry after restart = %#v, %v", reopened, err)
	}
	got, err := restarted.GetInteraction(context.Background(), GetInteractionInput{
		InteractionID: handle.InteractionID, RequesterScope: input.Caller.Scope,
	})
	if err != nil || got.Interaction.ID != handle.InteractionID || got.Interaction.State != InteractionStateStaged {
		t.Fatalf("GetInteraction after restart = %#v, %v", got, err)
	}
}

func TestServiceSubmitRetryPrecedesDefinitionResolution(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := mustOpenSurface(t, service, "retry-before-registry-surface")
	input := testSubmitInput(surface.SurfaceID, "retry-before-registry")
	created, err := service.SubmitInteraction(context.Background(), input)
	if err != nil {
		t.Fatalf("SubmitInteraction: %v", err)
	}
	unavailable, err := NewService(store, unavailableDefinitionCatalog{})
	if err != nil {
		t.Fatalf("NewService with unavailable catalog: %v", err)
	}
	retry, err := unavailable.SubmitInteraction(context.Background(), input)
	if err != nil {
		t.Fatalf("idempotent retry consulted unavailable catalog: %v", err)
	}
	if retry.InteractionID != created.InteractionID || retry.Created {
		t.Fatalf("retry = %#v, want original handle %#v", retry, created)
	}
	conflict := input
	conflict.Request = json.RawMessage(`{"prompt":"different canonical request"}`)
	if _, err := unavailable.SubmitInteraction(context.Background(), conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting retry error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestServiceAwaitIsBoundedResumableAndDoesNotOwnLifecycle(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	submitter := newTestService(t, store)
	resumer := newTestService(t, store)
	surface := mustOpenSurface(t, submitter, "await-surface")
	input := testSubmitInput(surface.SurfaceID, "await-interaction")
	handle, err := submitter.SubmitInteraction(context.Background(), input)
	if err != nil {
		t.Fatalf("SubmitInteraction: %v", err)
	}

	if _, waitErr := resumer.AwaitResolution(context.Background(), AwaitResolutionInput{
		InteractionID: handle.InteractionID, RequesterScope: input.Caller.Scope,
		MaximumWait: 20 * time.Millisecond,
	}); !errors.Is(waitErr, ErrWaitTimeout) {
		t.Fatalf("short AwaitResolution error = %v, want ErrWaitTimeout", waitErr)
	}
	stillOpen, err := store.GetInteraction(context.Background(), handle.InteractionID)
	if err != nil || stillOpen.State != InteractionStateStaged || stillOpen.TerminalAt != nil {
		t.Fatalf("wait timeout changed lifecycle: %#v, %v", stillOpen, err)
	}
	snapshot, err := store.HydrateSurface(context.Background(), surface.SurfaceID)
	if err != nil || len(snapshot.OutcomeRetrievals) != 0 {
		t.Fatalf("nonterminal wait retrievals = %d, %v", len(snapshot.OutcomeRetrievals), err)
	}

	awaitResult := make(chan TerminalOutcome, 1)
	awaitErr := make(chan error, 1)
	go func() {
		outcome, waitErr := resumer.AwaitResolution(context.Background(), AwaitResolutionInput{
			InteractionID: handle.InteractionID, RequesterScope: input.Caller.Scope,
			MaximumWait:          time.Second,
			TransportCorrelation: json.RawMessage(`{"transport":"new-mcp-session"}`),
		})
		awaitResult <- outcome
		awaitErr <- waitErr
	}()
	time.Sleep(20 * time.Millisecond)
	if _, cancelErr := submitter.CancelInteraction(context.Background(), CancelInteractionInput{
		InteractionID: handle.InteractionID, ExpectedRevision: handle.Revision,
		Requester: input.Caller, Cause: TerminalCauseCallerWithdrawn, Reason: "caller moved on",
	}); cancelErr != nil {
		t.Fatalf("CancelInteraction: %v", cancelErr)
	}
	if waitErr := <-awaitErr; waitErr != nil {
		t.Fatalf("AwaitResolution: %v", waitErr)
	}
	outcome := <-awaitResult
	if outcome.Interaction.State != InteractionStateCanceled ||
		outcome.Interaction.TerminalCause != TerminalCauseCallerWithdrawn || outcome.Retrieval == nil ||
		len(outcome.Notifications) != 1 || outcome.Notifications[0].State != DeliveryStateQueued {
		t.Fatalf("await outcome = %#v", outcome)
	}

	pending := mustSubmit(t, submitter, surface.SurfaceID, "canceled-wait-interaction")
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, waitErr := resumer.AwaitResolution(cancelCtx, AwaitResolutionInput{
		InteractionID: pending.InteractionID, RequesterScope: input.Caller.Scope,
		MaximumWait: time.Second,
	}); !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("canceled context error = %v, want context.Canceled", waitErr)
	}
	pendingRecord, err := store.GetInteraction(context.Background(), pending.InteractionID)
	if err != nil || pendingRecord.State != InteractionStateStaged || pendingRecord.TerminalAt != nil {
		t.Fatalf("canceled waiter changed lifecycle: %#v, %v", pendingRecord, err)
	}
}

func TestServiceDefaultAwaitBoundFitsMountedHTTPTransport(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	service, err := NewService(store, testDefinitionCatalog{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := service.AwaitResolution(context.Background(), AwaitResolutionInput{
		InteractionID: "not-consulted", RequesterScope: "not-consulted",
		MaximumWait: 50*time.Second + time.Nanosecond,
	}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("transport-unsafe default wait error = %v, want ErrInvalidRecord", err)
	}
	if _, err := service.AwaitResolution(context.Background(), AwaitResolutionInput{
		InteractionID: "not-consulted", RequesterScope: "not-consulted",
		MaximumWaitMillis: 50_001,
	}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("transport-unsafe millisecond wait error = %v, want ErrInvalidRecord", err)
	}
}

func TestServiceDraftResolveAndRetrievalKeepIndependentFacts(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := mustOpenSurface(t, service, "resolve-surface")
	input := testSubmitInput(surface.SurfaceID, "resolve-interaction")
	handle, err := service.SubmitInteraction(context.Background(), input)
	if err != nil {
		t.Fatalf("SubmitInteraction: %v", err)
	}
	participant := testParticipant()
	presented, err := service.AcknowledgePresentation(context.Background(), PresentInteractionInput{
		InteractionID: handle.InteractionID, ExpectedRevision: handle.Revision,
		PresentedProjectionRevision: 7, Participant: participant, ConnectionID: "socket-1",
	})
	if err != nil {
		t.Fatalf("AcknowledgePresentation: %v", err)
	}
	impersonator := participant
	impersonator.Assurance = "asserted"
	if _, draftErr := service.SaveDraft(context.Background(), SaveDraftInput{
		InteractionID: handle.InteractionID, DraftRevision: 1,
		InteractionRevision: presented.Revision, Participant: impersonator,
		DefinitionVersion: "1.0", Payload: json.RawMessage(`{"note":"forged"}`),
	}); !errors.Is(draftErr, ErrUnauthorized) {
		t.Fatalf("draft with mismatched participant binding error = %v, want ErrUnauthorized", draftErr)
	}
	if _, resolveErr := service.ResolveInteraction(context.Background(), ResolveInteractionInput{
		InteractionID: handle.InteractionID, ExpectedInteractionRevision: presented.Revision,
		PresentedProjectionRevision: 7, Participant: impersonator, ResponseKind: "data",
		ResponsePayload: json.RawMessage(`{"decision":"forged"}`),
	}); !errors.Is(resolveErr, ErrUnauthorized) {
		t.Fatalf("resolve with mismatched participant binding error = %v, want ErrUnauthorized", resolveErr)
	}
	if _, cancelErr := service.CancelInteraction(context.Background(), CancelInteractionInput{
		InteractionID: handle.InteractionID, ExpectedRevision: presented.Revision,
		Requester: impersonator, Cause: TerminalCauseParticipantCanceled,
	}); !errors.Is(cancelErr, ErrUnauthorized) {
		t.Fatalf("cancel with mismatched participant binding error = %v, want ErrUnauthorized", cancelErr)
	}
	draft, err := service.SaveDraft(context.Background(), SaveDraftInput{
		InteractionID: handle.InteractionID, DraftRevision: 1,
		InteractionRevision: presented.Revision, Participant: participant,
		DefinitionVersion: "1.0", Payload: json.RawMessage(`{"note":"working"}`),
	})
	if err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	result, err := service.ResolveInteraction(context.Background(), ResolveInteractionInput{
		InteractionID: handle.InteractionID, ExpectedInteractionRevision: draft.InteractionRevision + 1,
		PresentedProjectionRevision: 7, Participant: participant, ResponseKind: "data",
		ResponsePayload: json.RawMessage(`{"decision":"approved"}`), SourceDraftRevision: &draft.Revision,
	})
	if err != nil {
		t.Fatalf("ResolveInteraction: %v", err)
	}
	if result.Interaction.State != InteractionStateResolved || len(result.Deliveries) != 1 ||
		result.Deliveries[0].State != DeliveryStateQueued {
		t.Fatalf("resolve result = %#v", result)
	}
	first, err := service.GetInteraction(context.Background(), GetInteractionInput{
		InteractionID: handle.InteractionID, RequesterScope: input.Caller.Scope,
		TransportCorrelation: json.RawMessage(`{"attempt":1}`),
	})
	if err != nil || first.Resolution == nil || first.Retrieval == nil {
		t.Fatalf("first retrieval = %#v, %v", first, err)
	}
	second, err := service.GetInteraction(context.Background(), GetInteractionInput{
		InteractionID: handle.InteractionID, RequesterScope: input.Caller.Scope,
		TransportCorrelation: json.RawMessage(`{"attempt":2}`),
	})
	if err != nil || second.Resolution == nil || second.Resolution.ID != first.Resolution.ID || second.Retrieval == nil ||
		second.Retrieval.ID == first.Retrieval.ID {
		t.Fatalf("second retrieval = %#v, %v", second, err)
	}
	snapshot, err := store.HydrateSurface(context.Background(), surface.SurfaceID)
	if err != nil || len(snapshot.Resolutions) != 1 || len(snapshot.ResolutionDeliveries) != 1 ||
		len(snapshot.Drafts) != 1 || len(snapshot.OutcomeRetrievals) != 2 ||
		snapshot.ResolutionDeliveries[0].State != DeliveryStateQueued {
		t.Fatalf("independent durable facts = %#v, %v", snapshot, err)
	}
	if snapshot.Drafts[0].ParticipantScope != participant.Scope ||
		snapshot.Drafts[0].ParticipantAuthority != participant.Authority ||
		snapshot.Drafts[0].ParticipantAssurance != participant.Assurance ||
		snapshot.Resolutions[0].ParticipantScope != participant.Scope {
		t.Fatalf("participant binding not retained on immutable records: %#v", snapshot)
	}
}

func TestServiceTerminalOperationsAreTypedAuthorizedAndPersistent(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := mustOpenSurface(t, service, "terminal-surface")
	caller := testCaller()
	first := mustSubmit(t, service, surface.SurfaceID, "superseded")
	replacement := mustSubmit(t, service, surface.SurfaceID, "replacement")

	unauthorized := caller
	unauthorized.Scope = "application:intruder"
	unauthorized.PrincipalRef = "agent:intruder"
	if _, err := service.GetInteraction(context.Background(), GetInteractionInput{
		InteractionID: first.InteractionID, RequesterScope: unauthorized.Scope,
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unauthorized get error = %v", err)
	}
	if _, unauthorizedErr := service.CancelInteraction(context.Background(), CancelInteractionInput{
		InteractionID: first.InteractionID, ExpectedRevision: first.Revision,
		Requester: unauthorized, Cause: TerminalCauseCallerCanceled,
	}); !errors.Is(unauthorizedErr, ErrUnauthorized) {
		t.Fatalf("unauthorized cancel error = %v", unauthorizedErr)
	}
	superseded, err := service.SupersedeInteraction(context.Background(), SupersedeInteractionInput{
		InteractionID: first.InteractionID, ExpectedRevision: first.Revision,
		ReplacementInteractionID: replacement.InteractionID, Requester: caller, Reason: "new request",
	})
	if err != nil {
		t.Fatalf("SupersedeInteraction: %v", err)
	}
	if superseded.Interaction.State != InteractionStateSuperseded ||
		superseded.Interaction.ReplacementInteractionID != replacement.InteractionID {
		t.Fatalf("superseded = %#v", superseded)
	}
	expiring := mustSubmit(t, service, surface.SurfaceID, "expiring")
	if _, expiryErr := service.ExpireInteraction(context.Background(), ExpireInteractionInput{
		InteractionID: expiring.InteractionID, ExpectedRevision: expiring.Revision,
		PolicyRef: "policy:deadline-v1", Actor: caller,
	}); !errors.Is(expiryErr, ErrUnauthorized) {
		t.Fatalf("caller-authorized expiry error = %v, want ErrUnauthorized", expiryErr)
	}
	expired, err := service.ExpireInteraction(context.Background(), ExpireInteractionInput{
		InteractionID: expiring.InteractionID, ExpectedRevision: expiring.Revision,
		PolicyRef: "policy:deadline-v1", Reason: "deadline elapsed",
		Actor: ActorBinding{Scope: "host", PrincipalRef: "scheduler", Authority: "host-policy", Assurance: "trusted"},
	})
	if err != nil {
		t.Fatalf("ExpireInteraction: %v", err)
	}
	if expired.Interaction.State != InteractionStateExpired || expired.Interaction.TerminalPolicyRef != "policy:deadline-v1" ||
		expired.Interaction.TerminalCause != "" {
		t.Fatalf("expired = %#v", expired)
	}

	participantCanceled := mustSubmit(t, service, surface.SurfaceID, "participant-cancel")
	participant := testParticipant()
	presented, err := service.AcknowledgePresentation(context.Background(), PresentInteractionInput{
		InteractionID: participantCanceled.InteractionID, ExpectedRevision: participantCanceled.Revision,
		PresentedProjectionRevision: 3, Participant: participant,
	})
	if err != nil {
		t.Fatalf("AcknowledgePresentation: %v", err)
	}
	participantResult, err := service.CancelInteraction(context.Background(), CancelInteractionInput{
		InteractionID: presented.InteractionID, ExpectedRevision: presented.Revision,
		Requester: participant, Cause: TerminalCauseParticipantCanceled,
	})
	if err != nil || participantResult.Interaction.TerminalCause != TerminalCauseParticipantCanceled {
		t.Fatalf("participant cancellation = %#v, %v", participantResult, err)
	}

	administratorCanceled := mustSubmit(t, service, surface.SurfaceID, "administrator-cancel")
	administrator := ActorBinding{
		Scope: "host:administration", PrincipalRef: "admin:test",
		Authority: "administrator", Assurance: "trusted",
	}
	forgedAdministrator := administrator
	forgedAdministrator.Assurance = "asserted"
	if _, forgedErr := service.CancelInteraction(context.Background(), CancelInteractionInput{
		InteractionID: administratorCanceled.InteractionID, ExpectedRevision: administratorCanceled.Revision,
		Requester: forgedAdministrator, Cause: TerminalCauseAdministratorCanceled,
	}); !errors.Is(forgedErr, ErrUnauthorized) {
		t.Fatalf("forged administrator cancellation error = %v, want ErrUnauthorized", forgedErr)
	}
	administratorResult, err := service.CancelInteraction(context.Background(), CancelInteractionInput{
		InteractionID: administratorCanceled.InteractionID, ExpectedRevision: administratorCanceled.Revision,
		Requester: administrator, Cause: TerminalCauseAdministratorCanceled,
	})
	if err != nil || administratorResult.Interaction.TerminalCause != TerminalCauseAdministratorCanceled {
		t.Fatalf("administrator cancellation = %#v, %v", administratorResult, err)
	}
}

func TestServicePrivilegedOperationsDefaultDeny(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	setup := newTestService(t, store)
	surface := mustOpenSurface(t, setup, "privileged-default-deny-surface")
	handle := mustSubmit(t, setup, surface.SurfaceID, "privileged-default-deny")
	service, err := NewService(store, testDefinitionCatalog{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	administrator := ActorBinding{
		Scope: "host:administration", PrincipalRef: "admin:test",
		Authority: "administrator", Assurance: "trusted",
	}
	if _, err := service.CancelInteraction(context.Background(), CancelInteractionInput{
		InteractionID: handle.InteractionID, ExpectedRevision: handle.Revision,
		Requester: administrator, Cause: TerminalCauseAdministratorCanceled,
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("default policy trusted magic strings, error = %v", err)
	}
}

func TestStoreSupersedeRevalidatesReplacementInsideTerminalTransaction(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := mustOpenSurface(t, service, "supersede-transaction-surface")
	original := mustSubmit(t, service, surface.SurfaceID, "supersede-transaction-original")
	replacement := mustSubmit(t, service, surface.SurfaceID, "supersede-transaction-replacement")
	if _, err := service.CancelInteraction(context.Background(), CancelInteractionInput{
		InteractionID: replacement.InteractionID, ExpectedRevision: replacement.Revision,
		Requester: testCaller(), Cause: TerminalCauseCallerCanceled,
	}); err != nil {
		t.Fatalf("CancelInteraction replacement: %v", err)
	}

	// This calls the durable primitive directly to prove it does not trust a
	// service-layer preflight read that could have raced with replacement
	// terminalization.
	if _, err := store.TerminalizeInteraction(context.Background(), TerminalizeInteractionParams{
		InteractionID: original.InteractionID, ExpectedRevision: original.Revision,
		To: InteractionStateSuperseded, ReplacementInteractionID: replacement.InteractionID,
		ActorRef: testCaller().PrincipalRef, Authority: testCaller().Authority,
		Notifications: []TerminalNotificationParams{{
			DestinationBinding: json.RawMessage(`{"kind":"caller_pull"}`),
			IdempotencyKey:     "supersede-transaction-terminal-replacement",
		}},
	}); !errors.Is(err, ErrNotRespondable) {
		t.Fatalf("supersede with terminal replacement error = %v, want ErrNotRespondable", err)
	}
	after, err := store.GetInteraction(context.Background(), original.InteractionID)
	if err != nil || after.State != InteractionStateStaged || after.ReplacementInteractionID != "" {
		t.Fatalf("failed supersede mutated original = %#v, %v", after, err)
	}
	notifications, err := store.ListTerminalNotifications(context.Background(), original.InteractionID)
	if err != nil || len(notifications) != 0 {
		t.Fatalf("failed supersede notifications = %#v, %v", notifications, err)
	}

	otherSurface := mustOpenSurface(t, service, "supersede-other-surface")
	crossSurface := mustSubmit(t, service, otherSurface.SurfaceID, "supersede-cross-surface")
	if _, err := store.TerminalizeInteraction(context.Background(), TerminalizeInteractionParams{
		InteractionID: original.InteractionID, ExpectedRevision: original.Revision,
		To: InteractionStateSuperseded, ReplacementInteractionID: crossSurface.InteractionID,
		ActorRef: testCaller().PrincipalRef, Authority: testCaller().Authority,
		Notifications: []TerminalNotificationParams{{
			DestinationBinding: json.RawMessage(`{"kind":"caller_pull"}`),
			IdempotencyKey:     "supersede-transaction-cross-surface",
		}},
	}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("cross-surface supersede error = %v, want ErrInvalidRecord", err)
	}
}

func TestServiceCloseSurfaceAtomicallyDisposesOutstandingAndRejectsNewWork(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := mustOpenSurface(t, service, "close-surface")
	first := mustSubmit(t, service, surface.SurfaceID, "close-first")
	second := mustSubmit(t, service, surface.SurfaceID, "close-second")
	currentSurface, err := store.GetSurface(context.Background(), surface.SurfaceID)
	if err != nil {
		t.Fatalf("GetSurface: %v", err)
	}
	closed, err := service.CloseSurface(context.Background(), CloseSurfaceInput{
		SurfaceID: surface.SurfaceID, ExpectedRevision: currentSurface.Revision,
		Requester: testOwner(), Reason: "operator closed inbox", PolicyRef: "close:cancel-outstanding-v1",
	})
	if err != nil {
		t.Fatalf("CloseSurface: %v", err)
	}
	if closed.Surface.State != SurfaceStateClosed || closed.DisposedInteractions != 2 {
		t.Fatalf("close result = %#v", closed)
	}
	for _, id := range []string{first.InteractionID, second.InteractionID} {
		interaction, err := store.GetInteraction(context.Background(), id)
		if err != nil || interaction.State != InteractionStateCanceled ||
			interaction.TerminalCause != TerminalCauseSurfacePolicy {
			t.Fatalf("disposed interaction %s = %#v, %v", id, interaction, err)
		}
		notifications, err := store.ListTerminalNotifications(context.Background(), id)
		if err != nil || len(notifications) != 1 {
			t.Fatalf("notifications for %s = %#v, %v", id, notifications, err)
		}
	}
	late := testSubmitInput(surface.SurfaceID, "after-close")
	if _, err := service.SubmitInteraction(context.Background(), late); !errors.Is(err, ErrTerminal) {
		t.Fatalf("submit after close error = %v, want ErrTerminal", err)
	}
}

func TestServiceCloseAndSubmitRaceLeavesNoOutstandingWork(t *testing.T) {
	t.Parallel()
	for iteration := range 20 {
		store, _ := openTestStore(t)
		service := newTestService(t, store)
		surface := mustOpenSurface(t, service, fmt.Sprintf("race-surface-%d", iteration))
		current, err := store.GetSurface(context.Background(), surface.SurfaceID)
		if err != nil {
			t.Fatalf("GetSurface: %v", err)
		}
		start := make(chan struct{})
		var submitHandle InteractionHandle
		var submitErr, closeErr error
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			submitHandle, submitErr = service.SubmitInteraction(
				context.Background(), testSubmitInput(surface.SurfaceID, fmt.Sprintf("race-%d", iteration)),
			)
		}()
		go func() {
			defer wait.Done()
			<-start
			_, closeErr = service.CloseSurface(context.Background(), CloseSurfaceInput{
				SurfaceID: surface.SurfaceID, ExpectedRevision: current.Revision,
				Requester: testOwner(), PolicyRef: "close:race-v1",
			})
		}()
		close(start)
		wait.Wait()
		if closeErr != nil && !errors.Is(closeErr, ErrRevisionConflict) {
			t.Fatalf("iteration %d close error = %v", iteration, closeErr)
		}
		if submitErr != nil && !errors.Is(submitErr, ErrTerminal) {
			t.Fatalf("iteration %d submit error = %v", iteration, submitErr)
		}
		if closeErr == nil && submitErr == nil {
			interaction, err := store.GetInteraction(context.Background(), submitHandle.InteractionID)
			if err != nil || !isTerminalState(interaction.State) {
				t.Fatalf("iteration %d successful close left open interaction %#v, %v", iteration, interaction, err)
			}
		}
	}
}

func TestStoreCloseSurfaceRollsBackAllDispositionWritesOnFailure(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := mustOpenSurface(t, service, "close-rollback-surface")
	first := mustSubmit(t, service, surface.SurfaceID, "close-rollback-first")
	second := mustSubmit(t, service, surface.SurfaceID, "close-rollback-second")
	currentSurface, err := store.GetSurface(context.Background(), surface.SurfaceID)
	if err != nil {
		t.Fatalf("GetSurface: %v", err)
	}
	// Reserve the deterministic close-notification key for the first row so
	// CloseSurface fails after attempting its first interaction transition.
	now := time.Now().UTC()
	if _, seedErr := store.db.Exec(`
INSERT INTO terminal_notifications (
  id, interaction_id, terminal_state, terminal_cause,
  destination_binding, idempotency_key, policy,
  lifecycle_state, revision, created_at, updated_at
) VALUES (?, ?, 'canceled', 'surface_policy', '{}', ?, '{}', 'queued', 1, ?, ?)`,
		"forced-conflict",
		first.InteractionID,
		fmt.Sprintf("surface-close:%s:%d", surface.SurfaceID, currentSurface.Revision),
		now,
		now,
	); seedErr != nil {
		t.Fatalf("seed close notification conflict: %v", seedErr)
	}
	if _, closeErr := service.CloseSurface(context.Background(), CloseSurfaceInput{
		SurfaceID: surface.SurfaceID, ExpectedRevision: currentSurface.Revision,
		Requester: testOwner(), PolicyRef: "close:rollback-v1",
	}); closeErr == nil {
		t.Fatal("CloseSurface unexpectedly succeeded despite notification conflict")
	}
	afterSurface, err := store.GetSurface(context.Background(), surface.SurfaceID)
	if err != nil || afterSurface.State != SurfaceStateActive || afterSurface.Revision != currentSurface.Revision {
		t.Fatalf("surface after rolled back close = %#v, %v", afterSurface, err)
	}
	for _, id := range []string{first.InteractionID, second.InteractionID} {
		after, err := store.GetInteraction(context.Background(), id)
		if err != nil || after.State != InteractionStateStaged || after.TerminalAt != nil {
			t.Fatalf("interaction %s after rolled back close = %#v, %v", id, after, err)
		}
	}
}

func newTestService(t *testing.T, store *Store) *Service {
	t.Helper()
	service, err := NewService(
		store,
		testDefinitionCatalog{},
		WithAwaitPollInterval(5*time.Millisecond),
		WithMaximumAwait(2*time.Second),
		WithPrivilegedActorPolicy(testPrivilegedActorPolicy{}),
		WithDeliveryWorkerPolicy(testDeliveryWorkerPolicy{}),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

func testCaller() ActorBinding {
	return ActorBinding{
		Scope: "application:test", PrincipalRef: "agent:test",
		Authority: "direct-mcp", Assurance: "asserted",
	}
}

func testOwner() ActorBinding {
	return ActorBinding{
		Scope: "operator:local", PrincipalRef: "local-operator",
		Authority: "local", Assurance: "loopback-unverified",
	}
}

func testParticipant() ActorBinding { return testOwner() }

func testDeliveryWorker() ActorBinding {
	return ActorBinding{
		Scope: "host:delivery", PrincipalRef: "worker:test",
		Authority: "destination-adapter", Assurance: "trusted",
	}
}

func testOpenSurfaceInput(key string) OpenSurfaceInput {
	return OpenSurfaceInput{
		Caller: testCaller(), IdempotencyKey: key, OwnerScope: testOwner().Scope,
		Metadata: json.RawMessage(`{"title":"Generic surface"}`),
		Policy:   json.RawMessage(`{"close":"cancel-outstanding"}`),
	}
}

func mustOpenSurface(t *testing.T, service *Service, key string) SurfaceHandle {
	t.Helper()
	handle, err := service.OpenSurface(context.Background(), testOpenSurfaceInput(key))
	if err != nil {
		t.Fatalf("OpenSurface: %v", err)
	}
	return handle
}

func testSubmitInput(surfaceID, key string) SubmitInteractionInput {
	return SubmitInteractionInput{
		SurfaceID: surfaceID, Caller: testCaller(), IdempotencyKey: key,
		Definition: DefinitionRef{Kind: "test.generic", Version: "1.0"},
		Request:    json.RawMessage(fmt.Sprintf(`{"prompt":%q}`, key)),
		Policy:     json.RawMessage(`{"expiry":"none"}`),
	}
}

func mustSubmit(t *testing.T, service *Service, surfaceID, key string) InteractionHandle {
	t.Helper()
	handle, err := service.SubmitInteraction(context.Background(), testSubmitInput(surfaceID, key))
	if err != nil {
		t.Fatalf("SubmitInteraction %s: %v", key, err)
	}
	return handle
}
