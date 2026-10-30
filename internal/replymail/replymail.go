// Package replymail mails the operator's answer to the agent that asked.
//
// Every agent-facing submit carries reply_to, the agent's Tether URN. Tangent
// queues a tether_mail delivery beside the caller-pull one (internal/interaction)
// when an interaction resolves or ends another way, and this worker sends it as
// Tether mail. The agent gets the answer on its next turn, or at once if idle,
// and never has to poll or hold a call open.
//
// The worker rides the existing delivery queue: leases, attempts, receipts and
// retry timing are the interaction store's, so a restart neither loses nor
// repeats a send (the Idempotency-Key makes a repeat harmless at Tether).
package replymail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hollis-labs/tangent/internal/interaction"
)

// Message is one reply mail, in the shape Tether's notify endpoint takes.
type Message struct {
	To             string
	Kind           string
	Subject        string
	Payload        map[string]any
	IdempotencyKey string
}

// Receipt is what the sender observed. Raw is kept as the delivery receipt.
type Receipt struct {
	MessageID string `json:"message_id,omitempty"`
	// Woken is whether the agent was woken now. false with a Reason such as
	// "offline" or "busy" is still a success: the mail is stored and arrives on
	// the agent's next pull.
	Woken  bool            `json:"woken"`
	Reason string          `json:"reason,omitempty"`
	Raw    json.RawMessage `json:"raw,omitempty"`
}

// SendError tells the worker whether trying again can help.
type SendError struct {
	Retryable bool
	Status    int
	Err       error
}

func (e *SendError) Error() string {
	return fmt.Sprintf("send reply mail: status %d: %v", e.Status, e.Err)
}
func (e *SendError) Unwrap() error { return e.Err }

// Sender delivers one message.
type Sender interface {
	Send(ctx context.Context, msg Message) (Receipt, error)
}

const (
	leaseOwner = "tangent-replymail"
	// maxAttempts bounds retries on network and 5xx failures. With the backoff
	// below this is about a day before the delivery is marked terminal.
	maxAttempts = 12
	// maxPayloadBytes caps the response carried inline. The full resolution is
	// always readable with tangent_interaction_get.
	maxPayloadBytes = 48 * 1024
)

// Worker drains tether_mail deliveries.
type Worker struct {
	store  *interaction.Store
	sender Sender
	logger *slog.Logger
	tick   time.Duration
	now    func() time.Time
	wake   chan struct{}
}

// New builds a worker. Call Run in a goroutine and Wake after commits.
func New(store *interaction.Store, sender Sender, logger *slog.Logger) *Worker {
	return &Worker{store: store, sender: sender, logger: logger, tick: 2 * time.Second, now: time.Now, wake: make(chan struct{}, 1)}
}

// Wake nudges the worker after a transition commits. It never blocks.
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run drains until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	timer := time.NewTicker(w.tick)
	defer timer.Stop()
	for {
		w.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-w.wake:
		}
	}
}

func (w *Worker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		if !w.deliverOne(ctx) {
			return
		}
	}
}

// deliverOne claims and sends one delivery. It reports whether it found one.
func (w *Worker) deliverOne(ctx context.Context) bool {
	claim, err := w.store.ClaimNextDelivery(ctx, interaction.ClaimDeliveryParams{
		LeaseOwner: leaseOwner, LeaseDuration: time.Minute, DestinationKind: interaction.DestinationKindTetherMail,
	})
	if errors.Is(err, interaction.ErrNoDeliveryAvailable) {
		return false
	}
	if err != nil {
		w.logger.Error("reply mail: claim", "error", err)
		return false
	}
	msg, err := w.build(ctx, claim)
	if err != nil {
		w.complete(ctx, claim, interaction.DeliveryStateTerminalFailure, nil, "build_failed", err.Error(), nil)
		return true
	}
	receipt, err := w.sender.Send(ctx, msg)
	if err == nil {
		raw, _ := json.Marshal(receipt)
		w.complete(ctx, claim, interaction.DeliveryStateDelivered, raw, "", "", nil)
	} else {
		var sendErr *SendError
		retryable := !errors.As(err, &sendErr) || sendErr.Retryable
		if retryable && claim.AttemptNumber < maxAttempts {
			next := w.now().Add(backoff(claim.AttemptNumber))
			w.complete(ctx, claim, interaction.DeliveryStateRetryableFailure, nil, "send_failed", err.Error(), &next)
		} else {
			w.complete(ctx, claim, interaction.DeliveryStateTerminalFailure, nil, "send_failed", err.Error(), nil)
		}
	}
	return true
}

func (w *Worker) complete(
	ctx context.Context, claim interaction.DeliveryClaim, to interaction.DeliveryState,
	receipt json.RawMessage, code, message string, next *time.Time,
) {
	_, err := w.store.CompleteDelivery(ctx, interaction.CompleteDeliveryParams{
		Kind: claim.Kind, ID: claim.ID, ExpectedRevision: claim.Revision, LeaseOwner: leaseOwner,
		To: to, Receipt: receipt, ErrorCode: code, ErrorMessage: message, NextEligibleAt: next,
	})
	if err != nil {
		w.logger.Error("reply mail: complete", "delivery", claim.ID, "error", err)
		return
	}
	if to != interaction.DeliveryStateDelivered {
		w.logger.Warn("reply mail not delivered", "delivery", claim.ID, "state", string(to), "error", message)
	}
}

// backoff is 30s, 1m, 2m ... capped at 4h.
func backoff(attempt int64) time.Duration {
	d := 30 * time.Second
	for i := int64(1); i < attempt && d < 4*time.Hour; i++ {
		d *= 2
	}
	if d > 4*time.Hour {
		d = 4 * time.Hour
	}
	return d
}

// build turns a claimed delivery into the mail the agent reads.
func (w *Worker) build(ctx context.Context, claim interaction.DeliveryClaim) (Message, error) {
	var binding struct {
		ReplyTo string `json:"reply_to"`
	}
	if err := json.Unmarshal(claim.DestinationBinding, &binding); err != nil || binding.ReplyTo == "" {
		return Message{}, errors.New("delivery has no reply_to")
	}
	record, err := w.store.GetInteraction(ctx, claim.InteractionID)
	if err != nil {
		return Message{}, fmt.Errorf("read interaction: %w", err)
	}
	info := map[string]any{
		"interaction_id": record.ID,
		"kind":           record.Definition.Kind,
		"surface_id":     record.SurfaceID,
		"state":          string(record.State),
	}
	if record.LegacyRoomID != "" {
		info["room_id"] = record.LegacyRoomID
	}
	if record.LegacyEnvelopeID != "" {
		info["envelope_id"] = record.LegacyEnvelopeID
	}
	subject := fmt.Sprintf("Tangent: %s %s", record.Definition.Kind, record.State)
	body := fmt.Sprintf("Your Tangent request (%s, %s) is %s.", record.Definition.Kind, record.ID, record.State)

	if claim.ResolutionID != "" {
		resolution, err := w.store.GetResolution(ctx, record.ID)
		if err != nil {
			return Message{}, fmt.Errorf("read resolution: %w", err)
		}
		info["resolution_id"] = resolution.ID
		info["response_kind"] = resolution.ResponseKind
		info["resolved_at"] = resolution.RecordedAt
		info["resolved_by"] = resolution.ParticipantRef
		payload := resolution.ResponsePayload
		if len(payload) > maxPayloadBytes {
			info["response_truncated"] = true
			info["response_bytes"] = len(payload)
			body += " The answer is too large to carry here: read it with tangent_interaction_get."
		} else {
			info["response"] = payload
			body += " The operator's answer is in tangent.response."
		}
	} else {
		info["terminal_cause"] = string(record.TerminalCause)
		if record.TerminalReason != "" {
			info["terminal_reason"] = record.TerminalReason
		}
		body += fmt.Sprintf(" It ended without an answer (%s).", record.TerminalCause)
	}
	return Message{
		To: binding.ReplyTo, Kind: "response", Subject: subject, IdempotencyKey: claim.IdempotencyKey,
		Payload: map[string]any{"subject": subject, "body": body, "tangent": info},
	}, nil
}
