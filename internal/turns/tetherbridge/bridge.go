package tetherbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	tether "github.com/hollis-labs/go-tether-client"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/turns"
)

// TurnsService defines the Tangent turns API surface required by the bridge.
type TurnsService interface {
	Inbox(context.Context) (turns.TurnsInbox, error)
	InspectTurn(context.Context, string) (turns.TurnItemView, error)
	Enqueue(context.Context, turns.EnqueueInput) (turns.TurnHandle, error)
	Ack(context.Context, turns.AckInput) error
	SessionReplies(context.Context, string) ([]turns.TurnItemView, error)
}

// TurnWaitingInputPayload is the JSON payload carried by Tether's session.turn_waiting_input event.
type TurnWaitingInputPayload struct {
	SessionID      string `json:"session_id"`
	LogicalAgentID string `json:"logical_agent_id"`
	TurnID         string `json:"turn_id"`
	Prose          string `json:"prose"`
	Title          string `json:"title,omitempty"`
	Kind           string `json:"kind,omitempty"`
	At             string `json:"at,omitempty"`
}

// OptInFilterFunc decides whether a Tether session should be routed to the Tangent turns inbox.
type OptInFilterFunc func(sessionID, logicalAgentID string) bool

// BridgeConfig configures the Tether-to-Tangent turns bridge.
type BridgeConfig struct {
	Tether       TetherClient
	Turns        TurnsService
	OptInFilter  OptInFilterFunc
	PollInterval time.Duration
	Logger       *slog.Logger
}

// Bridge mediates between Tether sessions and Tangent's /turns FIFO inbox.
// It intercepts turns when agents go idle and delivers operator replies immediately
// if the session is idle, or buffers them for delivery at the next stop if busy.
type Bridge struct {
	tether       TetherClient
	turns        TurnsService
	optInFilter  OptInFilterFunc
	pollInterval time.Duration
	logger       *slog.Logger
	outbox       *SessionOutbox

	mu       sync.Mutex
	cancel   context.CancelFunc
	stopDone chan struct{}
}

// NewBridge creates a new turns bridge.
func NewBridge(cfg BridgeConfig) (*Bridge, error) {
	if cfg.Tether == nil {
		return nil, errors.New("tetherbridge: tether client is required")
	}
	if cfg.Turns == nil {
		return nil, errors.New("tetherbridge: turns service is required")
	}
	poll := cfg.PollInterval
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Bridge{
		tether:       cfg.Tether,
		turns:        cfg.Turns,
		optInFilter:  cfg.OptInFilter,
		pollInterval: poll,
		logger:       logger,
		outbox:       NewSessionOutbox(),
	}, nil
}

// Outbox returns the active SessionOutbox holding pending replies.
func (b *Bridge) Outbox() *SessionOutbox {
	return b.outbox
}

// Start launches background inbound event consumption and outbound reply dispatching.
func (b *Bridge) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cancel != nil {
		return errors.New("tetherbridge: bridge already running")
	}

	runCtx, cancel := context.WithCancel(ctx)
	b.cancel = cancel
	b.stopDone = make(chan struct{})

	go b.run(runCtx)
	return nil
}

// Stop terminates the background event and dispatch loops.
func (b *Bridge) Stop() {
	b.mu.Lock()
	cancel := b.cancel
	done := b.stopDone
	b.cancel = nil
	b.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (b *Bridge) run(ctx context.Context) {
	defer close(b.stopDone)

	// Stream events from Tether
	eventsCh, errCh := b.tether.StreamEvents(ctx, tether.StreamEventsOptions{
		Scopes: []string{tether.ScopeSession},
		Kinds:  []string{"session.turn_waiting_input", "session.state_changed"},
	})

	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-errCh:
			if !ok {
				return
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				b.logger.Warn("tetherbridge: stream error", "err", err)
			}
		case ev, ok := <-eventsCh:
			if !ok {
				return
			}
			if err := b.HandleEvent(ctx, ev); err != nil {
				b.logger.Error("tetherbridge: handle event error", "err", err, "kind", ev.Kind, "seq", ev.Seq)
			}
		case <-ticker.C:
			if err := b.PollAndDispatch(ctx); err != nil && !errors.Is(err, context.Canceled) {
				b.logger.Error("tetherbridge: poll dispatch error", "err", err)
			}
		}
	}
}

// HandleEvent processes a single Tether stream event.
func (b *Bridge) HandleEvent(ctx context.Context, ev tether.StreamEvent) error {
	switch ev.Kind {
	case "session.turn_waiting_input":
		return b.handleTurnWaitingInput(ctx, ev)
	case "session.state_changed":
		return b.handleStateChanged(ev)
	default:
		return nil
	}
}

func (b *Bridge) handleTurnWaitingInput(ctx context.Context, ev tether.StreamEvent) error {
	var p TurnWaitingInputPayload
	if err := json.Unmarshal([]byte(ev.PayloadJSON), &p); err != nil {
		return fmt.Errorf("tetherbridge: parse turn payload: %w", err)
	}

	if p.SessionID == "" {
		p.SessionID = ev.SessionID
	}
	if p.SessionID == "" {
		return nil
	}

	// Apply opt-in filter
	if b.optInFilter != nil && !b.optInFilter(p.SessionID, p.LogicalAgentID) {
		return nil
	}

	// 1. Check if there is an operator reply queued for this session (the "next stop" deliver path)
	if pending, ok := b.outbox.Dequeue(p.SessionID); ok {
		b.logger.Info("tetherbridge: delivering queued reply at next stop",
			"session_id", p.SessionID,
			"item_id", pending.ItemID,
		)
		if err := b.tether.SendTurn(ctx, p.SessionID, pending.ResponseText); err != nil {
			// On send failure, re-enqueue and return error
			b.outbox.Enqueue(pending)
			return fmt.Errorf("tetherbridge: send queued turn to %s: %w", p.SessionID, err)
		}
		// Acknowledge the Tangent turn item delivery
		return b.turns.Ack(ctx, turns.AckInput{ItemID: pending.ItemID})
	}

	// 2. If no pending reply, this is an agent turn awaiting operator attention -> enqueue into Tangent
	return b.enqueueTurn(ctx, p, ev.Seq)
}

func (b *Bridge) handleStateChanged(ev tether.StreamEvent) error {
	var payload struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Reason string `json:"reason,omitempty"`
	}
	if err := json.Unmarshal([]byte(ev.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("tetherbridge: parse state changed payload: %w", err)
	}

	// If session has terminated (done, killed, failed), clear any stale outbox replies
	if payload.To == "done" || payload.To == "killed" || payload.To == "failed" {
		dropped := b.outbox.Clear(ev.SessionID)
		if len(dropped) > 0 {
			b.logger.Warn("tetherbridge: dropped pending replies for terminated session",
				"session_id", ev.SessionID,
				"count", len(dropped),
				"state", payload.To,
			)
		}
	}
	return nil
}

func (b *Bridge) enqueueTurn(ctx context.Context, p TurnWaitingInputPayload, seq int64) error {
	turnID := p.TurnID
	if turnID == "" {
		turnID = fmt.Sprintf("tturn-%s-%d", p.SessionID, seq)
	}

	kind := p.Kind
	if kind == "" {
		kind = "question"
	}
	title := p.Title
	if title == "" {
		title = "Agent awaiting input"
	}

	req := turns.AgentTurnRequest{
		ContractVersion: extensions.AgentTurnContractVersion,
		TurnID:          turnID,
		SessionID:       p.SessionID,
		IdempotencyKey:  fmt.Sprintf("tether-%s-%s", p.SessionID, turnID),
		Kind:            kind,
		Source: turns.AgentTurnSource{
			AgentID:       p.LogicalAgentID,
			ApplicationID: "tether",
			AgentLabel:    p.LogicalAgentID,
		},
		Title:   title,
		Content: p.Prose,
		Correlations: map[string]any{
			"tether_session_id": p.SessionID,
			"tether_event_seq":  seq,
		},
	}

	raw, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("tetherbridge: marshal turn request: %w", err)
	}

	_, err = b.turns.Enqueue(ctx, turns.EnqueueInput{
		Request: raw,
		Caller: interaction.ActorBinding{
			Scope:        "tether:bridge",
			PrincipalRef: "tether-session-" + p.SessionID,
			Authority:    "tether",
			Assurance:    "local-daemon",
		},
	})
	if err != nil && !errors.Is(err, turns.ErrTerminalConflict) {
		return fmt.Errorf("tetherbridge: enqueue turn: %w", err)
	}
	return nil
}

// DispatchReply delivers an operator's reply to the corresponding Tether session.
// If the session is LiveStateIdle, it is sent immediately.
// If the session is LiveStateProcessing (busy), it is queued in the SessionOutbox
// for immediate delivery at the next stop.
func (b *Bridge) DispatchReply(ctx context.Context, item turns.TurnItemView) error {
	if item.Resolution == nil {
		return nil
	}
	sessionID := item.SessionID
	if sessionID == "" {
		return errors.New("tetherbridge: turn item has empty session_id")
	}

	replyText := item.Resolution.ResponseText

	// Check runtime health
	health, err := b.tether.SessionHealth(ctx, sessionID)
	if err != nil {
		// Fallback check if session exists
		sess, sessErr := b.tether.GetSession(ctx, sessionID)
		if sessErr != nil || (sess.State != "" && sess.State != "running") {
			b.logger.Warn("tetherbridge: session not running or unreachable",
				"session_id", sessionID,
				"health_err", err,
				"sess_err", sessErr,
			)
			// Mark turn delivery acknowledged as terminal session failure
			return b.turns.Ack(ctx, turns.AckInput{ItemID: item.ItemID})
		}
	}

	// 1. Immediate delivery if session is idle
	if health.LiveState == "idle" || (err != nil && health.LiveState == "") {
		b.logger.Info("tetherbridge: delivering reply immediately to idle session",
			"session_id", sessionID,
			"item_id", item.ItemID,
		)
		if err := b.tether.SendTurn(ctx, sessionID, replyText); err != nil {
			return fmt.Errorf("tetherbridge: send turn: %w", err)
		}
		return b.turns.Ack(ctx, turns.AckInput{ItemID: item.ItemID})
	}

	// 2. Session is busy (LiveStateProcessing) -> Queue in Outbox for the next stop
	b.logger.Info("tetherbridge: session busy; staging reply in outbox for next stop",
		"session_id", sessionID,
		"item_id", item.ItemID,
		"live_state", health.LiveState,
	)
	b.outbox.Enqueue(PendingReply{
		ItemID:       item.ItemID,
		SessionID:    sessionID,
		ResponseText: replyText,
		Action:       item.Resolution.Action,
		QueuedAt:     time.Now(),
	})
	return nil
}

// PollAndDispatch scans the Tangent turns inbox for resolved turns whose replies have not yet
// been acknowledged, and dispatches them to Tether.
func (b *Bridge) PollAndDispatch(ctx context.Context) error {
	inbox, err := b.turns.Inbox(ctx)
	if err != nil {
		return err
	}

	for _, item := range inbox.History {
		// If item is resolved and delivery has not yet been acknowledged
		if item.Resolution != nil && item.DeliveryState != interaction.DeliveryStateAcknowledged {
			// Avoid duplicate queuing if already present in outbox
			if b.isAlreadyQueued(item.SessionID, item.ItemID) {
				continue
			}
			if err := b.DispatchReply(ctx, item); err != nil {
				b.logger.Error("tetherbridge: dispatch reply failed",
					"item_id", item.ItemID,
					"session_id", item.SessionID,
					"err", err,
				)
			}
		}
	}
	return nil
}

func (b *Bridge) isAlreadyQueued(sessionID, itemID string) bool {
	b.outbox.mu.Lock()
	defer b.outbox.mu.Unlock()
	for _, q := range b.outbox.pending[sessionID] {
		if q.ItemID == itemID {
			return true
		}
	}
	return false
}
