package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/room"
)

// triageRoomURLPath is the format string used when logging the "open
// this URL" hint after creating a triage Room. The %s is replaced by
// the room id; the prefix comes from the roomURLBase argument passed
// to NewTriageHandler.
const triageRoomURLPath = "/r/%s"

// envTriageTimeout is the env var clients use to override the default
// 5-minute MCP triage timeout. Parsed as a Go duration string ("3m",
// "30s", etc).
const envTriageTimeout = "TANGENT_TRIAGE_TIMEOUT"

const defaultTriageTimeout = 5 * time.Minute

// TriageHandler is the envelope.Handler that bridges incoming triage
// envelopes to a per-call WebSocket Room. Registered against the
// dispatcher at boot via NewTriageHandler + dispatcher.Register.
//
// One Room is created per triage call by default; the optional metadata
// "roomID" key on env.Meta allows reuse of an existing room. Unknown
// room ids fail fast rather than implicitly creating on demand.
//
// Registration is what makes a workflow visible to tangent.list_workflows;
// no production code path calls Dispatcher.Dispatch, so this handler's
// blocking Push is not the route a caller's tool invocation takes. Every
// named room workflow tool and tangent.session_advance go through
// Server.advanceRoomEnvelope, which routes onto the durable completion
// adapter in internal/roomflow.
type TriageHandler struct {
	manager *room.Manager
	logger  *slog.Logger

	// roomURLBase is "http://host:port" — the handler appends
	// "/r/<roomID>" to log the open-tab URL. Empty disables logging.
	roomURLBase string

	// timeout is the per-call deadline applied when ctx has no deadline
	// of its own. The MCP SDK propagates client-side cancel; tests may
	// also wrap ctx, so this is a backstop for ill-behaved clients.
	timeout time.Duration
}

// NewTriageHandler constructs a TriageHandler. logger may be nil
// (defaults to slog.Default); roomURLBase may be empty to disable the
// "open this URL" log hint. The per-call timeout is resolved from the
// TANGENT_TRIAGE_TIMEOUT env var (Go duration string), falling back to
// the package default — there's no constructor argument for it.
func NewTriageHandler(manager *room.Manager, logger *slog.Logger, roomURLBase string) *TriageHandler {
	if logger == nil {
		logger = slog.Default()
	}
	timeout := resolveTriageTimeout()
	return &TriageHandler{
		manager:     manager,
		logger:      logger,
		roomURLBase: roomURLBase,
		timeout:     timeout,
	}
}

// Handle dispatches the triage envelope by:
//
//  1. Resolving the target Room (existing if env.Meta["roomID"] set
//     and known to the manager; new otherwise).
//  2. Logging the open-tab URL so the developer running v0.1 manually
//     can paste it into their browser.
//  3. Pushing the envelope on the Room and blocking until the user
//     submits, cancels, or the connection drops.
//
// Errors map to high-level cases:
//
//   - context deadline → fmt.Errorf wrapping ctx.Err()
//   - user cancel → kind=ack, status=cancelled response (synthesized
//     here so the dispatcher's response-validation gate sees a valid
//     envelope shape)
//   - room disconnect → fmt.Errorf wrapping ErrRoomDisconnected;
//     mapped onto "host-error: room disconnected" by the MCP layer
//
// Note: cancel synthesizes a Response rather than returning an error
// because the protocol explicitly carries cancel-as-data: clients that
// can branch on Response.Kind/Status see a structured cancel; clients
// that key off MCP-tool errors see a clean status-200.
func (t *TriageHandler) Handle(ctx context.Context, env *envelopes.Envelope) (*envelopes.Response, error) {
	if env == nil {
		return nil, fmt.Errorf("triage: nil envelope")
	}

	// Apply backstop timeout if ctx has no deadline.
	if _, ok := ctx.Deadline(); !ok && t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	rm, created, err := t.resolveRoom(env)
	if err != nil {
		return nil, err
	}
	if created {
		hint := t.formatRoomURL(rm.ID)
		if hint != "" {
			t.logger.Info("triage room created", "room", rm.ID, "url", hint, "envelope", env.ID)
		} else {
			t.logger.Info("triage room created", "room", rm.ID, "envelope", env.ID)
		}
	} else {
		t.logger.Info("triage routed to existing room", "room", rm.ID, "envelope", env.ID)
	}

	resp, err := rm.Push(ctx, env)
	if err != nil {
		return t.translateRoomError(env, err)
	}
	return resp, nil
}

// resolveRoom returns the Room env should target plus a bool indicating
// whether it was newly created. Policy: server-issued IDs only; a
// Meta["roomID"] referencing an unknown room is a hard error so
// arbitrary client input cannot smuggle rooms into existence.
func (t *TriageHandler) resolveRoom(env *envelopes.Envelope) (*room.Room, bool, error) {
	if id, ok := metaRoomID(env.Meta); ok && id != "" {
		if rm, found := t.manager.Get(id); found {
			return rm, false, nil
		}
		return nil, false, fmt.Errorf("triage: requested roomID %q does not exist", id)
	}
	rm, err := t.manager.CreateWithError(map[string]string{
		"envelopeID":   env.ID,
		"envelopeType": env.Type,
	})
	if err != nil {
		return nil, false, fmt.Errorf("triage: create room: %w", err)
	}
	return rm, true, nil
}

// translateRoomError converts a Room.Push error into the appropriate
// dispatch-layer return. Cancel becomes a synthesized cancelled
// Response; everything else propagates as an error.
func (t *TriageHandler) translateRoomError(env *envelopes.Envelope, err error) (*envelopes.Response, error) {
	// User cancel is synthesized into an ack/cancelled response so MCP
	// clients see the protocol-level cancel shape rather than a tool
	// failure.
	if errors.Is(err, room.ErrUserCancelled) {
		return &envelopes.Response{
			V:           envelopes.ProtocolVersion,
			EnvelopeID:  env.ID,
			Kind:        envelopes.ResponseKindAck,
			Status:      envelopes.ResponseStatusCancelled,
			CompletedAt: time.Now().UTC().Format(time.RFC3339),
		}, nil
	}
	if errors.Is(err, room.ErrRoomDisconnected) || errors.Is(err, room.ErrRoomClosed) {
		return nil, fmt.Errorf("triage: %w", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return nil, fmt.Errorf("triage: %w", err)
	}
	return nil, fmt.Errorf("triage: room push: %w", err)
}

// formatRoomURL builds the open-this-URL hint. Empty if no base
// configured.
func (t *TriageHandler) formatRoomURL(roomID string) string {
	if t.roomURLBase == "" {
		return ""
	}
	return t.roomURLBase + fmt.Sprintf(triageRoomURLPath, roomID)
}

// RegisterTriageOnDispatcher is a convenience wrapper used by main and
// integration tests to wire the handler in one call. The triage type
// MUST already be registered on the envelope service (see
// internal/envelope/extensions.RegisterTriage).
func RegisterTriageOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(triageEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

// RegisterFeedbackOnDispatcher wires the same room-bridging handler for
// tangent.feedback envelopes. Feedback uses the same room semantics as
// triage; the MCP layer keeps separate tool entrypoints and schemas.
func RegisterFeedbackOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(feedbackEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

// RegisterFormCollectOnDispatcher wires the same room-bridging handler for
// tangent.form-collect envelopes.
func RegisterFormCollectOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(formCollectEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

// RegisterDesignIterationOnDispatcher wires the same room-bridging
// handler for tangent.design-iteration envelopes.
func RegisterDesignIterationOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(designIterationEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

// RegisterInterviewQuestionOnDispatcher wires the same room-bridging
// handler for tangent.interview-question envelopes.
func RegisterInterviewQuestionOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(interviewQuestionEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

// RegisterBlockDraftOnDispatcher wires the same room-bridging handler
// for tangent.block-draft envelopes.
func RegisterBlockDraftOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(blockDraftEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

// RegisterProseRevisionOnDispatcher wires the same room-bridging handler
// for tangent.prose-revision envelopes.
func RegisterProseRevisionOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(proseRevisionEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

// RegisterOutputRenderOnDispatcher wires the same room-bridging handler
// for tangent.output-render envelopes.
func RegisterOutputRenderOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(outputRenderEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

// RegisterWhiteboardOnDispatcher wires the same room-bridging handler
// for tangent.whiteboard envelopes.
func RegisterWhiteboardOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(whiteboardEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

func RegisterDashboardOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(dashboardEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

func RegisterFilePickerOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(filePickerEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

func RegisterProgressPanelOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(progressPanelEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

func RegisterWizardOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(wizardEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

// RegisterDiffReviewOnDispatcher wires the same room-bridging handler
// for tangent.diff-review envelopes.
func RegisterDiffReviewOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(diffReviewEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

// RegisterSpreadsheetReviewOnDispatcher wires the same room-bridging
// handler for tangent.spreadsheet-review envelopes.
func RegisterSpreadsheetReviewOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(spreadsheetReviewEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

// RegisterApprovalQueueOnDispatcher wires the same room-bridging
// handler for tangent.approval-queue envelopes.
func RegisterApprovalQueueOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(approvalQueueEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

// RegisterSynthesisNotesOnDispatcher wires the same room-bridging
// handler for tangent.synthesis-notes envelopes.
func RegisterSynthesisNotesOnDispatcher(dispatcher *envelope.Dispatcher, handler *TriageHandler) error {
	return dispatcher.Register(synthesisNotesEnvelopeType, envelope.HandlerFunc(handler.Handle))
}

// resolveTriageTimeout reads envTriageTimeout once at construction. A
// malformed value silently falls back to the package default — v0.1
// prefers "boots no matter what" over "fails fast on bad config" for
// non-fatal knobs.
func resolveTriageTimeout() time.Duration {
	raw := os.Getenv(envTriageTimeout)
	if raw == "" {
		return defaultTriageTimeout
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return defaultTriageTimeout
}

// metaRoomID returns env.Meta["roomID"] when present and string-typed.
// Tangent's Meta is map[string]any (per the canonical Envelope shape)
// so we type-assert here.
func metaRoomID(meta map[string]any) (string, bool) {
	if meta == nil {
		return "", false
	}
	v, ok := meta["roomID"]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
