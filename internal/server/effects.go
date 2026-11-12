package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/effect"
	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/telemetry"
)

// The browser effect API: the one channel a renderer uses to ask the host to
// do something in the world.
//
// It mirrors /api/hitl and /api/rooms deliberately — participant-session
// authenticated, origin guarded, `Cache-Control: no-store` — because an effect
// request is a participant act and must be admitted on exactly the same
// evidence as a resolution.
//
// One route, not one per capability. ADR 0004 put every object-access refusal
// through a single `authz.Authorize`; an effect model with a route per
// capability would have as many places to forget a check as it has effects.
//
// The route exists and nothing in the shipped SPA calls it, which is the
// honest state of v0.x: no shipped definition declares a
// `required_capability`, so every request through here is refused with
// `effect_capability_undeclared`. That refusal is the product. The alternative
// — no channel at all — would mean the first renderer to need an effect
// invents its own, which is exactly the situation ADR 0003 §2.5 was written to
// prevent.

// EffectContextResolver supplies the pinned definition binding and owning
// scope for one interaction.
//
// It is an interface here, like MCPServer and RoomService, so internal/server
// does not import internal/interaction for this: the dependency stays one-way
// and a test can answer the question without a database.
type EffectContextResolver interface {
	// ResolveEffectContext returns the binding whose granted capabilities
	// govern this interaction, and the scope that owns it. An unknown
	// interaction is an error; the handler turns every error into the same
	// refusal, so a probe cannot tell an unknown interaction from an
	// unauthorized one.
	ResolveEffectContext(ctx context.Context, interactionID string) (effect.Binding, string, error)
}

// effectCommand is the request body. Every field a renderer may set is here,
// and every field it may not — the principal, the binding, the owner scope —
// is assembled by the handler from evidence.
type effectCommand struct {
	Capability     string `json:"capability"`
	InteractionID  string `json:"interaction_id"`
	HandleID       string `json:"handle_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
	Intent         struct {
		ControlID         string `json:"control_id"`
		PresentedRevision int64  `json:"presented_revision,omitempty"`
	} `json:"intent"`
	Params struct {
		MaxBytes  int64  `json:"max_bytes,omitempty"`
		MediaType string `json:"media_type,omitempty"`
		Origin    string `json:"origin,omitempty"`
		// Content is base64 so a write's bytes survive JSON without the host
		// having to guess an encoding. It is decoded and never logged.
		Content string `json:"content,omitempty"`
	} `json:"params"`
}

// effectResponse is the receipt, plus whatever the effect produced.
//
// The content is a sibling of the receipt rather than a field on it, mirroring
// effect.Result: the receipt is the durable, safe-to-quote row and the content
// is neither.
type effectResponse struct {
	Receipt effect.Receipt `json:"receipt"`
	Content string         `json:"content,omitempty"`
}

type effectHTTPHandler struct {
	broker    *effect.Broker
	contexts  EffectContextResolver
	telemetry *telemetry.Recorder
}

func newEffectHTTPHandler(
	broker *effect.Broker,
	contexts EffectContextResolver,
	recorder *telemetry.Recorder,
) *effectHTTPHandler {
	return &effectHTTPHandler{broker: broker, contexts: contexts, telemetry: recorder}
}

// maximumEffectRequestBytes bounds a request body. A write's content is the
// only thing that grows, and the broker clamps it again against the root's own
// ceiling; this is the transport's own floor so a malformed body cannot be
// read into memory before anything has authorized it.
const maximumEffectRequestBytes = 1 << 20

func (h *effectHTTPHandler) request(w http.ResponseWriter, r *http.Request) {
	session, ok := participant.FromContext(r.Context())
	if !ok {
		writeEffectRefusal(w, http.StatusForbidden, effect.CodeNotAuthorized)
		return
	}

	var command effectCommand
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maximumEffectRequestBytes))
	if err := decoder.Decode(&command); err != nil {
		writeEffectRefusal(w, http.StatusBadRequest, effect.CodeInvalidRequest)
		return
	}

	content, err := base64.StdEncoding.DecodeString(command.Params.Content)
	if command.Params.Content != "" && err != nil {
		writeEffectRefusal(w, http.StatusBadRequest, effect.CodeInvalidRequest)
		return
	}

	// The binding is resolved from the interaction, never read from the body.
	// Every failure — unknown interaction, unreadable binding, a caller
	// naming someone else's work — becomes the same refusal, so the route
	// cannot be used to discover which interactions exist.
	binding, ownerScope, resolveErr := h.contexts.ResolveEffectContext(
		r.Context(), strings.TrimSpace(command.InteractionID))
	if resolveErr != nil {
		writeEffectRefusal(w, http.StatusForbidden, effect.CodeNotAuthorized)
		return
	}

	result, err := h.broker.Request(r.Context(), effect.Request{
		Capability: effect.Capability(strings.TrimSpace(command.Capability)),
		Principal: effect.Principal{
			Kind:    authz.KindParticipant,
			Scope:   session.Scope,
			Ref:     session.Ref,
			Granted: session.Grants,
		},
		Binding:        binding,
		OwnerScope:     ownerScope,
		InteractionID:  strings.TrimSpace(command.InteractionID),
		HandleID:       strings.TrimSpace(command.HandleID),
		IdempotencyKey: strings.TrimSpace(command.IdempotencyKey),
		Intent: effect.Intent{
			ControlID:         command.Intent.ControlID,
			PresentedRevision: command.Intent.PresentedRevision,
			// The host times the intent. A renderer-supplied timestamp would
			// be a claim about when a person acted, which is not a claim a
			// renderer is in a position to make.
			ConfirmedAt: time.Now().UTC(),
		},
		Params: effect.Params{
			MaxBytes:  command.Params.MaxBytes,
			Content:   content,
			MediaType: command.Params.MediaType,
			Origin:    command.Params.Origin,
		},
	})
	if err != nil {
		// A storage failure is the host's problem, not the participant's, and
		// it must not be reported as a refusal: a refusal says "the model said
		// no", and this did not reach the model's answer.
		writeHITLJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "effect_unavailable",
			"message": "The effect could not be recorded. Nothing was performed.",
		})
		return
	}

	status := http.StatusOK
	if result.Receipt.Decision == effect.DecisionRefused {
		status = effectRefusalStatus(result.Receipt.Code)
		reportEffectRefusal(r.Context(), h.telemetry, result.Receipt, binding, ownerScope)
	}
	response := effectResponse{Receipt: result.Receipt}
	if len(result.Content) > 0 {
		response.Content = base64.StdEncoding.EncodeToString(result.Content)
	}
	writeHITLJSON(w, status, response)
}

// effectRefusalStatus maps a refusal onto an HTTP status.
//
// Everything the model refuses is a 403 except the shapes that are genuinely
// about the request rather than about authority. The ADR 0004 §5 split — 403
// inside an authority, 404 across one — does not apply here: an effect request
// always arrives on an authenticated same-origin session, so there is no
// foreign authority to hide existence from, and a 404 on a route that exists
// would be a worse lie than a 403.
func effectRefusalStatus(code string) int {
	switch code {
	case effect.CodeInvalidRequest, effect.CodeIntentMissing:
		return http.StatusBadRequest
	case effect.CodeIdempotencyConflict:
		return http.StatusConflict
	case effect.CodeTooLarge:
		return http.StatusRequestEntityTooLarge
	case effect.CodeUnavailable:
		return http.StatusUnprocessableEntity
	default:
		return http.StatusForbidden
	}
}

// writeEffectRefusal reports a refusal raised before the broker was reached.
//
// The message is fixed and says nothing: an effect refusal that named the
// missing capability, the owning scope, or the handle would be a probe, for
// the same reason ADR 0004 §6.5 fixes the text of an authorization failure.
func writeEffectRefusal(w http.ResponseWriter, status int, code string) {
	writeHITLJSON(w, status, map[string]any{
		"code":    code,
		"message": effectRefusalMessage,
	})
}

const effectRefusalMessage = "this browser has no authorized Tangent session for that effect"

// ErrEffectContextUnknown is what a resolver returns for an interaction it
// cannot bind. It is exported so a composing host can return the same shape.
var ErrEffectContextUnknown = errors.New("server: no effect context for that interaction")
