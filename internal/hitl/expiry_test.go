package hitl

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/interaction"
)

func expiringRequest(t *testing.T, key string, expiresAt time.Time) json.RawMessage {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(hitlRequest("app", "agent", key, "Expiring"), &body); err != nil {
		t.Fatal(err)
	}
	body["expires_at"] = expiresAt.UTC().Format(time.RFC3339)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestEnqueueRejectsPastExpiry(t *testing.T) {
	svc, _, _, _ := newTestService(t, "expiry-past.db")
	_, err := svc.Enqueue(context.Background(), EnqueueInput{
		Request: expiringRequest(t, "past", time.Now().Add(-time.Minute)), Caller: directTestCaller("app", "agent"),
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("past expires_at: err = %v, want ErrInvalidRequest", err)
	}
}

func TestExpiredItemRefusesLateWithdrawAndReadsExpired(t *testing.T) {
	svc, _, _, _ := newTestService(t, "expiry-late.db")
	ctx := context.Background()
	caller := directTestCaller("app", "agent")
	handle, err := svc.Enqueue(ctx, EnqueueInput{
		Request: expiringRequest(t, "late", time.Now().Add(time.Hour)), Caller: caller,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	svc.now = func() time.Time { return time.Now().Add(2 * time.Hour) }

	var conflict *TerminalConflictError
	if _, withdrawErr := svc.Withdraw(ctx, WithdrawInput{ItemID: handle.ItemID, Caller: caller}); !errors.As(withdrawErr, &conflict) ||
		conflict.Outcome.State != interaction.InteractionStateExpired {
		t.Fatalf("late withdraw = %v, want terminal conflict carrying expired", withdrawErr)
	}
	got, err := svc.Get(ctx, GetInput{ItemID: handle.ItemID, Caller: caller})
	if err != nil || got.Item.State != interaction.InteractionStateExpired || got.Item.TerminalOutcome == nil {
		t.Fatalf("Get after deadline = %#v, %v", got, err)
	}
}

func TestExpireDueSweepsAndAwaitReturnsExpired(t *testing.T) {
	svc, _, _, _ := newTestService(t, "expiry-sweep.db")
	ctx := context.Background()
	caller := directTestCaller("app", "agent")
	handle, err := svc.Enqueue(ctx, EnqueueInput{
		Request: expiringRequest(t, "sweep", time.Now().Add(time.Hour)), Caller: caller,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if n, sweepErr := svc.ExpireDue(ctx); sweepErr != nil || n != 0 {
		t.Fatalf("ExpireDue before deadline = %d, %v", n, sweepErr)
	}
	svc.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if n, sweepErr := svc.ExpireDue(ctx); sweepErr != nil || n != 1 {
		t.Fatalf("ExpireDue after deadline = %d, %v", n, sweepErr)
	}
	wait := time.Millisecond
	res, err := svc.Await(ctx, AwaitInput{ItemID: handle.ItemID, Caller: caller, Wait: &wait})
	if err != nil || res.WaitStatus != "terminal" || res.Item.State != interaction.InteractionStateExpired {
		t.Fatalf("Await = %#v, %v", res, err)
	}
	if n, _ := svc.ExpireDue(ctx); n != 0 {
		t.Fatalf("second sweep expired %d, want 0", n)
	}
}

func TestEnqueueRetryPastDeadlineReturnsOriginalItem(t *testing.T) {
	svc, _, _, _ := newTestService(t, "expiry-retry.db")
	ctx := context.Background()
	caller := directTestCaller("app", "agent")
	request := expiringRequest(t, "retry", time.Now().Add(time.Hour))
	first, err := svc.Enqueue(ctx, EnqueueInput{Request: request, Caller: caller})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	svc.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	retry, err := svc.Enqueue(ctx, EnqueueInput{Request: request, Caller: caller})
	if err != nil || retry.ItemID != first.ItemID {
		t.Fatalf("retry past deadline = %#v, %v; want original item %q", retry, err, first.ItemID)
	}
}

func TestExpireIfDueRetriesAfterNonTerminalRevisionConflict(t *testing.T) {
	svc, interactions, _, _ := newTestService(t, "expiry-conflict.db")
	ctx := context.Background()
	caller := directTestCaller("app", "agent")
	handle, err := svc.Enqueue(ctx, EnqueueInput{
		Request: expiringRequest(t, "conflict", time.Now().Add(time.Hour)), Caller: caller,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	stale, err := interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID: handle.ItemID, RequesterScope: OperatorParticipant.Scope, Capability: reservedSurfaceCapability,
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	// A presentation ack bumps the revision without terminating the item.
	if _, presentErr := svc.Present(ctx, PresentInput{
		ItemID: handle.ItemID, ExpectedRevision: stale.Interaction.Revision,
		PresentedProjectionRevision: 1, ConnectionID: "test",
	}); presentErr != nil {
		t.Fatalf("Present: %v", presentErr)
	}
	svc.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	done, err := svc.expireIfDue(ctx, stale.Interaction)
	if err != nil || !done {
		t.Fatalf("expireIfDue(stale) = %v, %v; want terminal", done, err)
	}
	// Read the store directly: Get would itself expire the item and hide a
	// missed retry here.
	stored, err := interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID: handle.ItemID, RequesterScope: OperatorParticipant.Scope, Capability: reservedSurfaceCapability,
	})
	if err != nil || stored.Interaction.State != interaction.InteractionStateExpired {
		t.Fatalf("stored state = %v, %v; want expired", stored.Interaction.State, err)
	}
}
