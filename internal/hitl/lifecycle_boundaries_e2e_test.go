package hitl

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
)

type hitlLifecyclePrivilegedPolicy struct{}

func (hitlLifecyclePrivilegedPolicy) AuthorizeHostPolicy(actor interaction.ActorBinding) bool {
	return actor == (interaction.ActorBinding{
		Scope: "host", PrincipalRef: "expiry-scheduler", Authority: "host-policy", Assurance: "trusted",
	})
}

func (hitlLifecyclePrivilegedPolicy) AuthorizeAdministrator(interaction.ActorBinding) bool {
	return false
}

// TestHITLLifecycleBoundariesKeepTerminalAuthoritiesDistinct complements the
// joined HTTP test with the host-only transitions that intentionally have no
// public browser or direct-MCP operation. It proves cancellation, withdrawal,
// expiry, and transport-context loss cannot collapse into one terminal fact.
func TestHITLLifecycleBoundariesKeepTerminalAuthoritiesDistinct(t *testing.T) {
	database, openErr := tangentdb.Open(filepath.Join(t.TempDir(), "hitl-lifecycle-boundaries.db"))
	if openErr != nil {
		t.Fatalf("db.Open: %v", openErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrationErr := tangentdb.RunMigrations(database); migrationErr != nil {
		t.Fatalf("RunMigrations: %v", migrationErr)
	}
	envelopes, envelopeErr := envelope.New(context.Background())
	if envelopeErr != nil {
		t.Fatalf("envelope.New: %v", envelopeErr)
	}
	if registrationErr := extensions.RegisterHITLItem(envelopes); registrationErr != nil {
		t.Fatalf("RegisterHITLItem: %v", registrationErr)
	}
	interactions, interactionErr := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopes, "hitl-lifecycle-e2e"),
		interaction.WithAwaitPollInterval(time.Millisecond),
		interaction.WithSurfaceAccessPolicy(SurfaceAccessPolicy{}),
		interaction.WithPrivilegedActorPolicy(hitlLifecyclePrivilegedPolicy{}),
	)
	if interactionErr != nil {
		t.Fatalf("interaction.NewService: %v", interactionErr)
	}
	service, serviceErr := NewService(interactions)
	if serviceErr != nil {
		t.Fatalf("hitl.NewService: %v", serviceErr)
	}

	callerCanceled := enqueueLifecycleItem(t, service, "caller-cancel", "Caller cancellation")
	participantCanceled := enqueueLifecycleItem(t, service, "participant-cancel", "Participant cancellation")
	expired := enqueueLifecycleItem(t, service, "expiry", "Policy expiry")
	transportPreserved := enqueueLifecycleItem(t, service, "transport", "Transport context loss")
	caller := directTestCaller("lifecycle", "boundary-worker")

	callerResult, err := interactions.CancelInteraction(context.Background(), interaction.CancelInteractionInput{
		InteractionID: callerCanceled.ItemID, ExpectedRevision: callerCanceled.Revision,
		Requester: caller, Cause: interaction.TerminalCauseCallerCanceled,
		Reason: "originating operation was explicitly canceled", Capability: reservedSurfaceCapability,
	})
	if err != nil || callerResult.Interaction.State != interaction.InteractionStateCanceled ||
		callerResult.Interaction.TerminalCause != interaction.TerminalCauseCallerCanceled {
		t.Fatalf("caller cancellation = %#v, %v", callerResult, err)
	}

	presented, err := service.Present(context.Background(), PresentInput{
		ItemID: participantCanceled.ItemID, ExpectedRevision: participantCanceled.Revision,
		PresentedProjectionRevision: participantCanceled.Revision, ConnectionID: "participant-tab",
	})
	if err != nil {
		t.Fatalf("Present participant-cancel item: %v", err)
	}
	participantResult, err := interactions.CancelInteraction(context.Background(), interaction.CancelInteractionInput{
		InteractionID: participantCanceled.ItemID, ExpectedRevision: presented.Revision,
		Requester: OperatorParticipant, Cause: interaction.TerminalCauseParticipantCanceled,
		Reason: "operator explicitly canceled", Capability: reservedSurfaceCapability,
	})
	if err != nil || participantResult.Interaction.State != interaction.InteractionStateCanceled ||
		participantResult.Interaction.TerminalCause != interaction.TerminalCauseParticipantCanceled {
		t.Fatalf("participant cancellation = %#v, %v", participantResult, err)
	}

	host := interaction.ActorBinding{
		Scope: "host", PrincipalRef: "expiry-scheduler", Authority: "host-policy", Assurance: "trusted",
	}
	expiredResult, err := interactions.ExpireInteraction(context.Background(), interaction.ExpireInteractionInput{
		InteractionID: expired.ItemID, ExpectedRevision: expired.Revision,
		PolicyRef: "request.expires_at", Reason: "deadline elapsed", Actor: host,
		Capability: reservedSurfaceCapability,
	})
	if err != nil || expiredResult.Interaction.State != interaction.InteractionStateExpired ||
		expiredResult.Interaction.TerminalCause != "" || expiredResult.Interaction.TerminalPolicyRef != "request.expires_at" {
		t.Fatalf("expiry = %#v, %v", expiredResult, err)
	}

	transportContext, cancel := context.WithCancel(context.Background())
	cancel()
	_, transportErr := service.Get(transportContext, GetInput{
		ItemID: transportPreserved.ItemID, Caller: caller,
	})
	if !errors.Is(transportErr, context.Canceled) {
		t.Fatalf("canceled transport Get error = %v", transportErr)
	}
	preserved, err := service.InspectOperatorItem(context.Background(), transportPreserved.ItemID)
	if err != nil || preserved.State != interaction.InteractionStateStaged || preserved.TerminalOutcome != nil {
		t.Fatalf("transport loss changed durable item = %#v, %v", preserved, err)
	}

	inbox, err := service.Inbox(context.Background())
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(inbox.Pending) != 1 || inbox.Pending[0].ItemID != transportPreserved.ItemID ||
		len(inbox.History) != 3 {
		t.Fatalf("lifecycle inbox = %#v", inbox)
	}
	want := map[string]struct {
		state interaction.InteractionState
		cause interaction.TerminalCause
	}{
		callerCanceled.ItemID:      {interaction.InteractionStateCanceled, interaction.TerminalCauseCallerCanceled},
		participantCanceled.ItemID: {interaction.InteractionStateCanceled, interaction.TerminalCauseParticipantCanceled},
		expired.ItemID:             {interaction.InteractionStateExpired, ""},
	}
	for _, item := range inbox.History {
		expected, ok := want[item.ItemID]
		if !ok || item.TerminalOutcome == nil || item.State != expected.state || item.TerminalOutcome.Cause != expected.cause {
			t.Fatalf("history item = %#v, expected=%#v found=%v", item, expected, ok)
		}
		delete(want, item.ItemID)
	}
	if len(want) != 0 {
		t.Fatalf("missing lifecycle outcomes: %#v", want)
	}

	// Once a terminal authority wins, a later caller withdrawal returns that
	// immutable outcome rather than rewriting its cause.
	_, err = service.Withdraw(context.Background(), WithdrawInput{
		ItemID: callerCanceled.ItemID, Caller: caller,
	})
	var terminalConflict *TerminalConflictError
	if !errors.As(err, &terminalConflict) || terminalConflict.Outcome.Cause != interaction.TerminalCauseCallerCanceled ||
		terminalConflict.Outcome.InteractionRevision != callerResult.Interaction.Revision {
		t.Fatalf("terminal retry rewrote caller cancellation = %#v, %v", terminalConflict, err)
	}
}

func enqueueLifecycleItem(t *testing.T, service *Service, key, title string) ItemHandle {
	t.Helper()
	request, err := json.Marshal(map[string]any{
		"contract_version": ContractVersion,
		"kind":             "approval",
		"idempotency_key":  "lifecycle-" + key,
		"title":            title,
		"summary":          "Keep terminal authorities distinct.",
		"request":          "Record the lifecycle fact without taking downstream action.",
		"source": map[string]any{
			"application_id": "lifecycle", "agent_id": "boundary-worker",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: request, Caller: directTestCaller("lifecycle", "boundary-worker"),
	})
	if err != nil {
		t.Fatalf("Enqueue %s: %v", key, err)
	}
	return handle
}
