package tetherbridge

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	tether "github.com/hollis-labs/go-tether-client"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/turns"
)

type mockTetherClient struct {
	mu          sync.Mutex
	healthMap   map[string]tether.RuntimeHealthResponse
	sessionMap  map[string]tether.Session
	sentTurns   []struct{ SessionID, Text string }
	eventsCh    chan tether.StreamEvent
	errCh       chan error
	sendTurnErr error
	healthErr   error
}

func newMockTetherClient() *mockTetherClient {
	return &mockTetherClient{
		healthMap:  make(map[string]tether.RuntimeHealthResponse),
		sessionMap: make(map[string]tether.Session),
		eventsCh:   make(chan tether.StreamEvent, 10),
		errCh:      make(chan error, 10),
	}
}

func (m *mockTetherClient) SendTurn(ctx context.Context, sessionID, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendTurnErr != nil {
		return m.sendTurnErr
	}
	m.sentTurns = append(m.sentTurns, struct{ SessionID, Text string }{sessionID, text})
	return nil
}

func (m *mockTetherClient) SessionHealth(ctx context.Context, sessionID string) (tether.RuntimeHealthResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.healthErr != nil {
		return tether.RuntimeHealthResponse{}, m.healthErr
	}
	if h, ok := m.healthMap[sessionID]; ok {
		return h, nil
	}
	return tether.RuntimeHealthResponse{
		SessionID: sessionID,
		Alive:     true,
		LiveState: "idle",
	}, nil
}

func (m *mockTetherClient) GetSession(ctx context.Context, sessionID string) (tether.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessionMap[sessionID]; ok {
		return s, nil
	}
	return tether.Session{
		ID:    sessionID,
		State: "running",
	}, nil
}

func (m *mockTetherClient) StreamEvents(ctx context.Context, opts tether.StreamEventsOptions) (<-chan tether.StreamEvent, <-chan error) {
	return m.eventsCh, m.errCh
}

type mockTurnsService struct {
	mu          sync.Mutex
	enqueued    []turns.EnqueueInput
	acked       []turns.AckInput
	inboxResult turns.TurnsInbox
	enqueueErr  error
	ackErr      error
}

func newMockTurnsService() *mockTurnsService {
	return &mockTurnsService{}
}

func (m *mockTurnsService) Inbox(ctx context.Context) (turns.TurnsInbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inboxResult, nil
}

func (m *mockTurnsService) InspectTurn(ctx context.Context, id string) (turns.TurnItemView, error) {
	return turns.TurnItemView{}, nil
}

func (m *mockTurnsService) Enqueue(ctx context.Context, input turns.EnqueueInput) (turns.TurnHandle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.enqueueErr != nil {
		return turns.TurnHandle{}, m.enqueueErr
	}
	m.enqueued = append(m.enqueued, input)
	return turns.TurnHandle{
		ItemID:    "item-mock-1",
		SessionID: "sess-mock-1",
		State:     interaction.InteractionStatePresented,
	}, nil
}

func (m *mockTurnsService) Ack(ctx context.Context, input turns.AckInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ackErr != nil {
		return m.ackErr
	}
	m.acked = append(m.acked, input)
	return nil
}

func (m *mockTurnsService) SessionReplies(ctx context.Context, sessionID string) ([]turns.TurnItemView, error) {
	return nil, nil
}

func TestBridge_ImmediateDeliveryWhenIdle(t *testing.T) {
	tetherMock := newMockTetherClient()
	tetherMock.healthMap["sess-1"] = tether.RuntimeHealthResponse{
		SessionID: "sess-1",
		Alive:     true,
		LiveState: "idle",
	}

	turnsMock := newMockTurnsService()

	b, err := NewBridge(BridgeConfig{
		Tether: tetherMock,
		Turns:  turnsMock,
	})
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}

	item := turns.TurnItemView{
		ItemID:    "item-123",
		SessionID: "sess-1",
		Resolution: &turns.TurnResolution{
			Action:       "reply",
			ResponseText: "Here is my answer to proceed.",
		},
	}

	if err := b.DispatchReply(context.Background(), item); err != nil {
		t.Fatalf("DispatchReply: %v", err)
	}

	tetherMock.mu.Lock()
	if len(tetherMock.sentTurns) != 1 {
		t.Fatalf("sentTurns count = %d, want 1", len(tetherMock.sentTurns))
	}
	if tetherMock.sentTurns[0].SessionID != "sess-1" || tetherMock.sentTurns[0].Text != "Here is my answer to proceed." {
		t.Fatalf("unexpected sentTurn: %+v", tetherMock.sentTurns[0])
	}
	tetherMock.mu.Unlock()

	turnsMock.mu.Lock()
	if len(turnsMock.acked) != 1 || turnsMock.acked[0].ItemID != "item-123" {
		t.Fatalf("expected ack for item-123, got %+v", turnsMock.acked)
	}
	turnsMock.mu.Unlock()

	if b.Outbox().Len("sess-1") != 0 {
		t.Fatalf("outbox len = %d, want 0", b.Outbox().Len("sess-1"))
	}
}

func TestBridge_QueuedDeliveryAtNextStopWhenBusy(t *testing.T) {
	tetherMock := newMockTetherClient()
	tetherMock.healthMap["sess-busy"] = tether.RuntimeHealthResponse{
		SessionID: "sess-busy",
		Alive:     true,
		LiveState: "processing", // Session is busy
	}

	turnsMock := newMockTurnsService()

	b, err := NewBridge(BridgeConfig{
		Tether: tetherMock,
		Turns:  turnsMock,
	})
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}

	item := turns.TurnItemView{
		ItemID:    "item-busy-1",
		SessionID: "sess-busy",
		Resolution: &turns.TurnResolution{
			Action:       "reply",
			ResponseText: "Proceed with the refactor.",
		},
	}

	// 1. Dispatch while session is busy -> should queue in Outbox, NOT send immediately
	if err := b.DispatchReply(context.Background(), item); err != nil {
		t.Fatalf("DispatchReply: %v", err)
	}

	tetherMock.mu.Lock()
	if len(tetherMock.sentTurns) != 0 {
		t.Fatalf("sentTurns count = %d, want 0 while busy", len(tetherMock.sentTurns))
	}
	tetherMock.mu.Unlock()

	if b.Outbox().Len("sess-busy") != 1 {
		t.Fatalf("outbox len = %d, want 1", b.Outbox().Len("sess-busy"))
	}

	// 2. Next turn event arrives (agent reached the next stop / waiting input)
	payloadJSON, _ := json.Marshal(TurnWaitingInputPayload{
		SessionID:      "sess-busy",
		LogicalAgentID: "agent-arch",
		TurnID:         "turn-agent-step-2",
		Prose:          "Finished searching files. What should I do next?",
	})

	ev := tether.StreamEvent{
		Seq:         42,
		Kind:        "session.turn_waiting_input",
		SessionID:   "sess-busy",
		PayloadJSON: string(payloadJSON),
	}

	if err := b.HandleEvent(context.Background(), ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}

	// 3. Queued reply should now be delivered immediately to Tether!
	tetherMock.mu.Lock()
	if len(tetherMock.sentTurns) != 1 {
		t.Fatalf("sentTurns count = %d, want 1 after next stop", len(tetherMock.sentTurns))
	}
	if tetherMock.sentTurns[0].SessionID != "sess-busy" || tetherMock.sentTurns[0].Text != "Proceed with the refactor." {
		t.Fatalf("unexpected sentTurn: %+v", tetherMock.sentTurns[0])
	}
	tetherMock.mu.Unlock()

	// 4. Item should be ACK'd and outbox cleared
	turnsMock.mu.Lock()
	if len(turnsMock.acked) != 1 || turnsMock.acked[0].ItemID != "item-busy-1" {
		t.Fatalf("expected ack for item-busy-1, got %+v", turnsMock.acked)
	}
	// And no new turn enqueued because queued reply handled it
	if len(turnsMock.enqueued) != 0 {
		t.Fatalf("enqueued turns = %d, want 0", len(turnsMock.enqueued))
	}
	turnsMock.mu.Unlock()

	if b.Outbox().Len("sess-busy") != 0 {
		t.Fatalf("outbox len = %d, want 0 after delivery", b.Outbox().Len("sess-busy"))
	}
}

func TestBridge_InboundTurnEnqueue(t *testing.T) {
	tetherMock := newMockTetherClient()
	turnsMock := newMockTurnsService()

	b, err := NewBridge(BridgeConfig{
		Tether: tetherMock,
		Turns:  turnsMock,
	})
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}

	payloadJSON, _ := json.Marshal(TurnWaitingInputPayload{
		SessionID:      "sess-inbound",
		LogicalAgentID: "agent-designer",
		TurnID:         "turn-100",
		Prose:          "Which color scheme do you prefer for the nav rail?",
		Title:          "Select design option",
		Kind:           "question",
	})

	ev := tether.StreamEvent{
		Seq:         105,
		Kind:        "session.turn_waiting_input",
		SessionID:   "sess-inbound",
		PayloadJSON: string(payloadJSON),
	}

	if err := b.HandleEvent(context.Background(), ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}

	turnsMock.mu.Lock()
	defer turnsMock.mu.Unlock()

	if len(turnsMock.enqueued) != 1 {
		t.Fatalf("enqueued turns = %d, want 1", len(turnsMock.enqueued))
	}

	var req turns.AgentTurnRequest
	if err := json.Unmarshal(turnsMock.enqueued[0].Request, &req); err != nil {
		t.Fatalf("unmarshal enqueued request: %v", err)
	}

	if req.TurnID != "turn-100" || req.SessionID != "sess-inbound" {
		t.Fatalf("unexpected turn request fields: %+v", req)
	}
	if req.Content != "Which color scheme do you prefer for the nav rail?" {
		t.Fatalf("unexpected content: %q", req.Content)
	}
	if req.Title != "Select design option" || req.Kind != "question" {
		t.Fatalf("unexpected title/kind: %q / %q", req.Title, req.Kind)
	}
}

func TestBridge_OptInFilter(t *testing.T) {
	tetherMock := newMockTetherClient()
	turnsMock := newMockTurnsService()

	b, err := NewBridge(BridgeConfig{
		Tether: tetherMock,
		Turns:  turnsMock,
		OptInFilter: func(sessionID, logicalAgentID string) bool {
			return sessionID == "opted-in-session"
		},
	})
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}

	payloadJSON, _ := json.Marshal(TurnWaitingInputPayload{
		SessionID:      "ignored-session",
		LogicalAgentID: "daemon-linter",
		Prose:          "Lint complete.",
	})

	evIgnored := tether.StreamEvent{
		Seq:         1,
		Kind:        "session.turn_waiting_input",
		SessionID:   "ignored-session",
		PayloadJSON: string(payloadJSON),
	}

	if err := b.HandleEvent(context.Background(), evIgnored); err != nil {
		t.Fatalf("HandleEvent ignored: %v", err)
	}

	turnsMock.mu.Lock()
	if len(turnsMock.enqueued) != 0 {
		t.Fatalf("enqueued = %d, want 0 for filtered session", len(turnsMock.enqueued))
	}
	turnsMock.mu.Unlock()

	payloadOpted, _ := json.Marshal(TurnWaitingInputPayload{
		SessionID:      "opted-in-session",
		LogicalAgentID: "pair-coder",
		Prose:          "Ready for next task.",
	})

	evOpted := tether.StreamEvent{
		Seq:         2,
		Kind:        "session.turn_waiting_input",
		SessionID:   "opted-in-session",
		PayloadJSON: string(payloadOpted),
	}

	if err := b.HandleEvent(context.Background(), evOpted); err != nil {
		t.Fatalf("HandleEvent opted: %v", err)
	}

	turnsMock.mu.Lock()
	if len(turnsMock.enqueued) != 1 {
		t.Fatalf("enqueued = %d, want 1 for opted in session", len(turnsMock.enqueued))
	}
	turnsMock.mu.Unlock()
}

func TestBridge_StateChangedClearsOutbox(t *testing.T) {
	tetherMock := newMockTetherClient()
	turnsMock := newMockTurnsService()

	b, err := NewBridge(BridgeConfig{
		Tether: tetherMock,
		Turns:  turnsMock,
	})
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}

	b.Outbox().Enqueue(PendingReply{
		ItemID:       "item-term-1",
		SessionID:    "sess-term",
		ResponseText: "will be cleared",
	})

	if b.Outbox().Len("sess-term") != 1 {
		t.Fatalf("outbox len = %d, want 1", b.Outbox().Len("sess-term"))
	}

	payloadJSON := `{"from":"running","to":"killed","reason":"user cancel"}`
	ev := tether.StreamEvent{
		Seq:         10,
		Kind:        "session.state_changed",
		SessionID:   "sess-term",
		PayloadJSON: payloadJSON,
	}

	if err := b.HandleEvent(context.Background(), ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}

	if b.Outbox().Len("sess-term") != 0 {
		t.Fatalf("outbox len = %d, want 0 after session killed", b.Outbox().Len("sess-term"))
	}
}

func TestBridge_PollAndDispatch(t *testing.T) {
	tetherMock := newMockTetherClient()
	turnsMock := newMockTurnsService()

	turnsMock.inboxResult = turns.TurnsInbox{
		History: []turns.TurnItemView{
			{
				ItemID:        "item-resolved-unacked",
				SessionID:     "sess-poll",
				DeliveryState: interaction.DeliveryStateQueued,
				Resolution: &turns.TurnResolution{
					Action:       "reply",
					ResponseText: "Poll answer",
				},
			},
			{
				ItemID:        "item-already-acked",
				SessionID:     "sess-poll",
				DeliveryState: interaction.DeliveryStateAcknowledged,
				Resolution: &turns.TurnResolution{
					Action:       "reply",
					ResponseText: "Old answer",
				},
			},
		},
	}

	b, err := NewBridge(BridgeConfig{
		Tether: tetherMock,
		Turns:  turnsMock,
	})
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}

	if err := b.PollAndDispatch(context.Background()); err != nil {
		t.Fatalf("PollAndDispatch: %v", err)
	}

	tetherMock.mu.Lock()
	if len(tetherMock.sentTurns) != 1 || tetherMock.sentTurns[0].Text != "Poll answer" {
		t.Fatalf("unexpected sentTurns: %+v", tetherMock.sentTurns)
	}
	tetherMock.mu.Unlock()

	turnsMock.mu.Lock()
	if len(turnsMock.acked) != 1 || turnsMock.acked[0].ItemID != "item-resolved-unacked" {
		t.Fatalf("unexpected acked: %+v", turnsMock.acked)
	}
	turnsMock.mu.Unlock()
}
