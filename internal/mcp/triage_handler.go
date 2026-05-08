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

// TriageRoomURLBase is the format string used when logging the "open
// this URL" hint after creating a triage Room. The %s is replaced by
// the room id; the prefix is taken from Server.triageRoomURL when set.
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
// One Room is created per triage call in v0.1; the optional metadata
// "roomID" key on env.Meta allows a future client to reuse an existing
// room, but for v0.1 we fail fast if the requested room is not present
// (we do not implicitly create on demand from arbitrary input — that's
// a v0.2 refinement so the contract stays predictable).
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
// (defaults to slog.Default). timeoutOverride may be zero to use the
// envTriageTimeout env var or the package default.
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
// whether it was newly created. v0.1 policy: server-issued IDs only;
// a Meta["roomID"] referencing an unknown room is a hard error so
// arbitrary client input cannot smuggle rooms into existence.
func (t *TriageHandler) resolveRoom(env *envelopes.Envelope) (*room.Room, bool, error) {
	if id, ok := metaString(env.Meta, "roomID"); ok && id != "" {
		if rm, found := t.manager.Get(id); found {
			return rm, false, nil
		}
		return nil, false, fmt.Errorf("triage: requested roomID %q does not exist", id)
	}
	rm := t.manager.Create(map[string]string{
		"envelopeID":   env.ID,
		"envelopeType": env.Type,
	})
	return rm, true, nil
}

// translateRoomError converts a Room.Push error into the appropriate
// dispatch-layer return. Cancel becomes a synthesized cancelled
// Response; everything else propagates as an error.
func (t *TriageHandler) translateRoomError(env *envelopes.Envelope, err error) (*envelopes.Response, error) {
	// User cancel: room.Push returns "user cancelled envelope %q" —
	// match by message because the cancel hook is anonymous (it'd be
	// cleaner with a sentinel; v0.2 refactor).
	if err != nil && containsUserCancel(err) {
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

// metaString returns the string value of env.Meta[key] if present and
// of type string. Tangent's Meta is map[string]any (per the canonical
// Envelope shape) so we type-assert here.
func metaString(meta map[string]any, key string) (string, bool) {
	if meta == nil {
		return "", false
	}
	v, ok := meta[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// containsUserCancel reports whether err originated from Pending.Cancel.
// The hook formats a known message; v0.2 should replace this with a
// sentinel error in the room package.
func containsUserCancel(err error) bool {
	return err != nil && stringHas(err.Error(), "user cancelled envelope")
}

// stringHas is strings.Contains without the import (one tiny call site).
func stringHas(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
