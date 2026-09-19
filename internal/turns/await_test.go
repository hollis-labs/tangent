package turns

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/interaction"
)

// answered enqueues one turn in sessionID and has the operator answer it,
// returning the handle and the resolved view.
func answered(t *testing.T, svc *Service, sessionID, turnID string) (TurnHandle, TurnItemView) {
	t.Helper()
	ctx := context.Background()
	handle, err := svc.Enqueue(ctx, EnqueueInput{
		Request: turnRequestJSON(sessionID, turnID, "question", "Q "+turnID, "Details"),
		Caller:  testCaller("tether"),
	})
	if err != nil {
		t.Fatalf("Enqueue %s: %v", turnID, err)
	}
	view, err := svc.Reply(ctx, ReplyInput{
		ItemID: handle.ItemID, ExpectedRevision: handle.Revision,
		Action: "respond", ResponseText: "answer to " + turnID,
	})
	if err != nil {
		t.Fatalf("Reply %s: %v", turnID, err)
	}
	return handle, view
}

func waitOf(d time.Duration) *time.Duration { return &d }

func TestAwait_ReturnsAnUndeliveredReplyAtOnce(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.awaitPoll = time.Millisecond
	_, view := answered(t, svc, "sess-a", "t1")

	start := time.Now()
	res, err := svc.Await(context.Background(), AwaitInput{SessionID: "sess-a", Wait: waitOf(5 * time.Second)})
	if err != nil {
		t.Fatalf("Await: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Await took %v with a reply already waiting", elapsed)
	}
	if res.WaitStatus != AwaitStatusReplies || len(res.Replies) != 1 {
		t.Fatalf("Await = %+v, want one reply", res)
	}
	got := res.Replies[0]
	if got.ItemID != view.ItemID || got.Resolution == nil || got.Resolution.ResponseText != "answer to t1" {
		t.Errorf("reply = %+v, want the answered turn with its response text", got)
	}
	if res.ContractVersion != ContractVersion || res.SessionID != "sess-a" {
		t.Errorf("envelope fields = %q / %q", res.ContractVersion, res.SessionID)
	}
}

func TestAwait_WaitsForAReplyThatArrivesMidWait(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.awaitPoll = 2 * time.Millisecond
	ctx := context.Background()

	handle, err := svc.Enqueue(ctx, EnqueueInput{
		Request: turnRequestJSON("sess-w", "t1", "question", "Waiting", "Details"),
		Caller:  testCaller("tether"),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	go func() {
		time.Sleep(40 * time.Millisecond)
		_, _ = svc.Reply(ctx, ReplyInput{
			ItemID: handle.ItemID, ExpectedRevision: handle.Revision, Action: "approve", ResponseText: "go",
		})
	}()

	res, err := svc.Await(ctx, AwaitInput{SessionID: "sess-w", Wait: waitOf(5 * time.Second)})
	if err != nil {
		t.Fatalf("Await: %v", err)
	}
	if res.WaitStatus != AwaitStatusReplies || len(res.Replies) != 1 {
		t.Fatalf("Await = %+v, want the reply that arrived while waiting", res)
	}
	if res.Replies[0].Resolution.Action != "approve" {
		t.Errorf("action = %q, want approve", res.Replies[0].Resolution.Action)
	}
}

func TestAwait_TimesOutWithoutChangingAnything(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.awaitPoll = 2 * time.Millisecond
	ctx := context.Background()

	handle, err := svc.Enqueue(ctx, EnqueueInput{
		Request: turnRequestJSON("sess-t", "t1", "question", "Unanswered", "Details"),
		Caller:  testCaller("tether"),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	res, err := svc.Await(ctx, AwaitInput{SessionID: "sess-t", Wait: waitOf(30 * time.Millisecond)})
	if err != nil {
		t.Fatalf("a timeout is a result, not an error: %v", err)
	}
	if res.WaitStatus != AwaitStatusTimeout {
		t.Errorf("WaitStatus = %q, want %q", res.WaitStatus, AwaitStatusTimeout)
	}
	if res.Replies == nil || len(res.Replies) != 0 {
		t.Errorf("Replies = %#v, want an empty non-nil slice so it serializes as []", res.Replies)
	}

	view, err := svc.InspectTurn(ctx, handle.ItemID)
	if err != nil {
		t.Fatalf("InspectTurn: %v", err)
	}
	if view.Revision != handle.Revision || view.State != handle.State {
		t.Errorf("Await changed the turn: revision %d→%d, state %q→%q",
			handle.Revision, view.Revision, handle.State, view.State)
	}
}

func TestAwait_ZeroWaitLooksOnceAndReturns(t *testing.T) {
	svc, _, _ := newTestService(t)
	start := time.Now()
	res, err := svc.Await(context.Background(), AwaitInput{SessionID: "nobody", Wait: waitOf(0)})
	if err != nil {
		t.Fatalf("Await: %v", err)
	}
	if res.WaitStatus != AwaitStatusTimeout || len(res.Replies) != 0 {
		t.Errorf("Await = %+v, want an immediate empty timeout", res)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("zero wait took %v", elapsed)
	}
}

func TestAwait_ExcludesAcknowledgedRepliesAndKeepsUnacknowledgedOnes(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.awaitPoll = time.Millisecond
	ctx := context.Background()

	first, firstView := answered(t, svc, "sess-ack", "t1")
	_, secondView := answered(t, svc, "sess-ack", "t2")

	before, err := svc.Await(ctx, AwaitInput{SessionID: "sess-ack", Wait: waitOf(0)})
	if err != nil {
		t.Fatalf("Await: %v", err)
	}
	if len(before.Replies) != 2 {
		t.Fatalf("before ack: %d replies, want 2", len(before.Replies))
	}

	if err = svc.Ack(ctx, AckInput{ItemID: first.ItemID, ReplyID: firstView.Resolution.ResolutionID}); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	after, err := svc.Await(ctx, AwaitInput{SessionID: "sess-ack", Wait: waitOf(0)})
	if err != nil {
		t.Fatalf("Await: %v", err)
	}
	if len(after.Replies) != 1 || after.Replies[0].ItemID != secondView.ItemID {
		t.Fatalf("after ack: %+v, want only the unacknowledged second reply", after.Replies)
	}
	if after.Replies[0].DeliveryState == interaction.DeliveryStateAcknowledged {
		t.Errorf("an acknowledged reply came back from Await")
	}

	// The history read is unchanged: it still reports every reply.
	all, err := svc.SessionReplies(ctx, "sess-ack")
	if err != nil {
		t.Fatalf("SessionReplies: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("SessionReplies = %d, want 2 (Await must not change the history read)", len(all))
	}
}

func TestAwait_ScopesToTheSessionAndOrdersByWhenTheOperatorAnswered(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.awaitPoll = time.Millisecond
	ctx := context.Background()

	// Enqueue t1 then t2, but answer t2 first: delivery order is answer order.
	h1, err := svc.Enqueue(ctx, EnqueueInput{
		Request: turnRequestJSON("sess-o", "t1", "question", "One", "Details"), Caller: testCaller("tether"),
	})
	if err != nil {
		t.Fatalf("Enqueue t1: %v", err)
	}
	h2, err := svc.Enqueue(ctx, EnqueueInput{
		Request: turnRequestJSON("sess-o", "t2", "question", "Two", "Details"), Caller: testCaller("tether"),
	})
	if err != nil {
		t.Fatalf("Enqueue t2: %v", err)
	}
	_, _ = answered(t, svc, "sess-other", "t9") // a different session's reply must not leak in

	if _, err = svc.Reply(ctx, ReplyInput{ItemID: h2.ItemID, ExpectedRevision: h2.Revision, Action: "respond", ResponseText: "two"}); err != nil {
		t.Fatalf("Reply t2: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err = svc.Reply(ctx, ReplyInput{ItemID: h1.ItemID, ExpectedRevision: h1.Revision, Action: "respond", ResponseText: "one"}); err != nil {
		t.Fatalf("Reply t1: %v", err)
	}

	res, err := svc.Await(ctx, AwaitInput{SessionID: "sess-o", Wait: waitOf(0)})
	if err != nil {
		t.Fatalf("Await: %v", err)
	}
	if len(res.Replies) != 2 {
		t.Fatalf("got %d replies for sess-o, want 2 (and none from sess-other)", len(res.Replies))
	}
	if res.Replies[0].TurnID != "t2" || res.Replies[1].TurnID != "t1" {
		t.Errorf("order = [%s %s], want [t2 t1] (the order the operator answered)",
			res.Replies[0].TurnID, res.Replies[1].TurnID)
	}
}

func TestAwait_HonoursContextCancellation(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.awaitPoll = 2 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := svc.Await(ctx, AwaitInput{SessionID: "quiet", Wait: waitOf(10 * time.Second)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Await err = %v, want context.Canceled", err)
	}
}

func TestAwait_RejectsInvalidInput(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	for name, input := range map[string]AwaitInput{
		"missing session":   {SessionID: ""},
		"negative wait":     {SessionID: "s", Wait: waitOf(-time.Millisecond)},
		"wait over ceiling": {SessionID: "s", Wait: waitOf(MaximumAwaitWait + time.Millisecond)},
	} {
		if _, err := svc.Await(ctx, input); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: err = %v, want ErrInvalidRequest", name, err)
		}
	}
}

func TestAck_IsIdempotent(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	handle, view := answered(t, svc, "sess-i", "t1")

	for i := 0; i < 2; i++ {
		if err := svc.Ack(ctx, AckInput{ItemID: handle.ItemID, ReplyID: view.Resolution.ResolutionID}); err != nil {
			t.Fatalf("Ack #%d: %v", i+1, err)
		}
	}
	got, err := svc.InspectTurn(ctx, handle.ItemID)
	if err != nil {
		t.Fatalf("InspectTurn: %v", err)
	}
	if got.DeliveryState != interaction.DeliveryStateAcknowledged {
		t.Errorf("DeliveryState = %q, want acknowledged", got.DeliveryState)
	}
}

func TestAck_RejectsAReplyIDThatIsNotThisTurnsReply(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	handle, _ := answered(t, svc, "sess-m", "t1")

	err := svc.Ack(ctx, AckInput{ItemID: handle.ItemID, ReplyID: "not-the-reply"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Ack err = %v, want ErrInvalidRequest", err)
	}
	got, inspectErr := svc.InspectTurn(ctx, handle.ItemID)
	if inspectErr != nil {
		t.Fatalf("InspectTurn: %v", inspectErr)
	}
	if got.DeliveryState == interaction.DeliveryStateAcknowledged {
		t.Errorf("a mismatched reply_id still acknowledged the turn")
	}
}

func TestAck_RejectsATurnWithNoReplyYet(t *testing.T) {
	svc, _, _ := newTestService(t)
	handle, err := svc.Enqueue(context.Background(), EnqueueInput{
		Request: turnRequestJSON("sess-n", "t1", "question", "Unanswered", "Details"),
		Caller:  testCaller("tether"),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err = svc.Ack(context.Background(), AckInput{ItemID: handle.ItemID}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Ack err = %v, want ErrInvalidRequest for a turn with nothing to acknowledge", err)
	}
}

// A dismissed turn was never answered, so it has no reply to deliver. The docs
// tell an agent this — it will see only timeouts for a turn the operator
// dismissed — so the behavior is pinned rather than assumed.
func TestAwait_DoesNotReturnADismissedTurn(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.awaitPoll = time.Millisecond
	ctx := context.Background()

	handle, err := svc.Enqueue(ctx, EnqueueInput{
		Request: turnRequestJSON("sess-d", "t1", "question", "Dismissed", "Details"),
		Caller:  testCaller("tether"),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	dismissed, err := svc.Dismiss(ctx, DismissInput{ItemID: handle.ItemID, ExpectedRevision: handle.Revision})
	if err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if dismissed.State != interaction.InteractionStateCanceled {
		t.Fatalf("state after dismiss = %q, want canceled", dismissed.State)
	}

	res, err := svc.Await(ctx, AwaitInput{SessionID: "sess-d", Wait: waitOf(0)})
	if err != nil {
		t.Fatalf("Await: %v", err)
	}
	if res.WaitStatus != AwaitStatusTimeout || len(res.Replies) != 0 {
		t.Errorf("Await = %+v, want a dismissed turn to produce no reply", res)
	}
}
