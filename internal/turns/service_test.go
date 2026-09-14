package turns

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
)

func newTestService(t *testing.T) (*Service, *interaction.Service, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "turns-test.db")
	database, err := tangentdb.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := tangentdb.RunMigrations(database); err != nil {
		t.Fatalf("db.RunMigrations: %v", err)
	}
	envSvc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if err := extensions.RegisterAgentTurn(envSvc); err != nil {
		t.Fatalf("RegisterAgentTurn: %v", err)
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envSvc, "turns-test-host"),
		interaction.WithSurfaceAccessPolicy(SurfaceAccessPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	svc, err := NewService(interactions)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, interactions, database
}

func turnRequestJSON(sessionID, turnID, kind, title, content string) json.RawMessage {
	data, _ := json.Marshal(map[string]any{
		"contract_version": "1.0",
		"session_id":       sessionID,
		"turn_id":          turnID,
		"idempotency_key":  fmt.Sprintf("tether:%s:%s", sessionID, turnID),
		"kind":             kind,
		"source": map[string]any{
			"agent_id":    "nanite-worker",
			"agent_label": "Nanite",
		},
		"title":   title,
		"summary": "Agent needs guidance",
		"content": content,
		"options": []map[string]any{
			{"label": "Option A", "value": "opt_a", "recommended": true},
			{"label": "Option B", "value": "opt_b"},
		},
		"correlations": map[string]any{
			"task_id": "CW-20260913-0019",
		},
	})
	return data
}

func testCaller(appID string) interaction.ActorBinding {
	return interaction.ActorBinding{
		Scope:        "standalone-local:" + appID,
		PrincipalRef: appID,
		Authority:    "standalone-local",
		Assurance:    "loopback-unverified",
	}
}

func TestEnqueue_FIFOArrivalOrder(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	const count = 5
	var handles []TurnHandle
	for i := 1; i <= count; i++ {
		h, err := svc.Enqueue(ctx, EnqueueInput{
			Request: turnRequestJSON("session-1", fmt.Sprintf("turn-%d", i), "question", fmt.Sprintf("Question %d", i), "Details"),
			Caller:  testCaller("tether"),
		})
		if err != nil {
			t.Fatalf("Enqueue turn %d: %v", i, err)
		}
		handles = append(handles, h)
	}

	// Verify monotonic QueueSequence
	for i := 0; i < count; i++ {
		expectedSeq := int64(i + 1)
		if handles[i].QueueSequence != expectedSeq {
			t.Errorf("turn %d QueueSequence = %d, want %d", i, handles[i].QueueSequence, expectedSeq)
		}
	}

	// Verify Inbox pending list is in strict arrival FIFO
	inbox, err := svc.Inbox(ctx)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if inbox.TotalPending != count {
		t.Fatalf("TotalPending = %d, want %d", inbox.TotalPending, count)
	}
	for i := 0; i < count; i++ {
		expectedSeq := int64(i + 1)
		if inbox.Pending[i].QueueSequence != expectedSeq {
			t.Errorf("inbox pending [%d] QueueSequence = %d, want %d", i, inbox.Pending[i].QueueSequence, expectedSeq)
		}
	}
}

func TestEnqueue_Idempotency(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	req := turnRequestJSON("session-idem", "turn-1", "approval", "Approve Step", "Proceed with deploy?")
	h1, err := svc.Enqueue(ctx, EnqueueInput{Request: req, Caller: testCaller("tether")})
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}

	h2, err := svc.Enqueue(ctx, EnqueueInput{Request: req, Caller: testCaller("tether")})
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}

	if h1.ItemID != h2.ItemID {
		t.Errorf("idempotent replay item ID mismatch: %q vs %q", h1.ItemID, h2.ItemID)
	}
	if h1.QueueSequence != h2.QueueSequence {
		t.Errorf("idempotent replay sequence mismatch: %d vs %d", h1.QueueSequence, h2.QueueSequence)
	}
}

func TestReply_Resolution(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	h, err := svc.Enqueue(ctx, EnqueueInput{
		Request: turnRequestJSON("session-reply", "turn-1", "question", "Which database?", "Postgres or SQLite?"),
		Caller:  testCaller("tether"),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Reply to the turn
	item, err := svc.Reply(ctx, ReplyInput{
		ItemID:           h.ItemID,
		ExpectedRevision: h.Revision,
		Action:           "respond",
		ResponseText:     "Use SQLite for local-first single user",
		SelectedOption:   "opt_b",
		Note:             "Reviewed with team",
	})
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}

	if item.State != interaction.InteractionStateResolved {
		t.Errorf("State = %q, want resolved", item.State)
	}
	if item.Resolution == nil {
		t.Fatalf("Resolution is nil")
	}
	if item.Resolution.Action != "respond" || item.Resolution.SelectedOption != "opt_b" {
		t.Errorf("Resolution = %+v", item.Resolution)
	}

	// Verify Inbox reflects resolution in History, not Pending
	inbox, err := svc.Inbox(ctx)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if inbox.TotalPending != 0 {
		t.Errorf("TotalPending = %d, want 0", inbox.TotalPending)
	}
	if inbox.TotalTerminal != 1 {
		t.Errorf("TotalTerminal = %d, want 1", inbox.TotalTerminal)
	}
}

func TestDismiss_Cancellation(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	h, err := svc.Enqueue(ctx, EnqueueInput{
		Request: turnRequestJSON("session-dismiss", "turn-1", "checkpoint", "Checkpoint Gate", "Inspection"),
		Caller:  testCaller("tether"),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	item, err := svc.Dismiss(ctx, DismissInput{
		ItemID:           h.ItemID,
		ExpectedRevision: h.Revision,
		Reason:           "Operator dismissed checkpoint without changes",
	})
	if err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	if item.State != interaction.InteractionStateCanceled {
		t.Errorf("State = %q, want canceled", item.State)
	}
}

func TestSessionRepliesAndAck(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	const targetSession = "session-pull-target"
	h, err := svc.Enqueue(ctx, EnqueueInput{
		Request: turnRequestJSON(targetSession, "turn-1", "question", "Target Question", "Details"),
		Caller:  testCaller("tether"),
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Resolve it
	_, err = svc.Reply(ctx, ReplyInput{
		ItemID:           h.ItemID,
		ExpectedRevision: h.Revision,
		Action:           "approve",
		ResponseText:     "Approved",
	})
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}

	// Tether pulls replies for the session
	replies, err := svc.SessionReplies(ctx, targetSession)
	if err != nil {
		t.Fatalf("SessionReplies: %v", err)
	}
	if len(replies) != 1 {
		t.Fatalf("expected 1 reply for session, got %d", len(replies))
	}
	if replies[0].Resolution.Action != "approve" {
		t.Errorf("reply action = %q, want approve", replies[0].Resolution.Action)
	}

	// Tether acks delivery
	if err := svc.Ack(ctx, AckInput{ItemID: h.ItemID, ReplyID: replies[0].Resolution.ResolutionID}); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	// Inspect turn to confirm acknowledged
	view, err := svc.InspectTurn(ctx, h.ItemID)
	if err != nil {
		t.Fatalf("InspectTurn: %v", err)
	}
	if view.DeliveryState != interaction.DeliveryStateAcknowledged {
		t.Errorf("DeliveryState = %q, want acknowledged", view.DeliveryState)
	}
}
