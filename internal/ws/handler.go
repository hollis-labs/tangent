// Package ws hosts the HTTP→WebSocket upgrade handler that fronts the
// Room manager. One handler instance serves /ws for the lifetime of the
// Tangent process; per-connection state lives on the room.Connection the
// upgrade allocates.
//
// Wire shape:
//
//	server → client : {"type":"envelope","envelopeId":"<id>","revision":1,"envelope":{...}}
//	server → client : {"type":"connection","connectionId":"<id>","role":"resolver","lease":{...},"connections":[...]}
//	server → client : {"type":"sync","sync":{"surface_revision":7,"presentations":[...]}}
//	server → client : {"type":"error","code":"resolver_lease_held","envelopeId":"<id>","lease":{...}}
//	client → server : {"type":"response","envelopeId":"<id>","revision":1,"response":{...}}
//	client → server : {"type":"cancel","envelopeId":"<id>","revision":1}
//	client → server : {"type":"claim_resolver","takeover":true}
//	client → server : {"type":"release_resolver"}
//	client → server : {"type":"resync"}
//	client → server : {"type":"heartbeat"}
//
// Anything else from the client is logged and ignored. The handler is
// deliberately dumb — Room owns the lifecycle of the Pending entries and the
// resolver lease, and emits errors via Push's return value or an explicit
// error frame, never by guessing.
//
// Connection identity: the server issues the connection id. A client supplies
// only a `clientID` — the identity of the tab behind the socket — which
// decides whether a new attachment replaces its own predecessor (a refresh) or
// joins alongside it (a second tab). It is a routing label and confers nothing.
//
// Access policy: an upgrade requires a valid participant session, immediately
// and with no grace period (ADR 0004 §5). Knowing a room UUID is no longer
// sufficient to attach — the URL is a locator, and the cookie is the
// authority. internal/server additionally applies the same-origin guard to the
// route, and AcceptOptions.OriginPatterns keeps the WebSocket handshake itself
// localhost-only.
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"
	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/room"
)

// Wire error codes. They are stable strings the SPA branches on, so a losing
// tab can explain itself rather than appearing to have done nothing.
const (
	errorCodeResolverLeaseHeld = "resolver_lease_held"
	errorCodeStalePresentation = "stale_presentation"
	errorCodeRoomClosed        = "room_closed"
	// errorCodeNotAuthorized reports a frame the attached session may not
	// send. It is a Refused outcome in the sense of
	// docs/room-validation-affordances.md — the server declined a submission —
	// and the SPA renders it through describeServerError like every other
	// server refusal, never as a second error surface.
	errorCodeNotAuthorized = "not_authorized"
)

// ParticipantResolver establishes the principal behind an upgrade request.
//
// It runs *before* the upgrade so a refusal is an ordinary 403 rather than a
// socket that closes without saying why. Production installs a resolver backed
// by the participant session store, which is the change that stops
// `/ws?roomID=` plus a known room UUID from being an answer credential: the
// upgrade now requires a valid participant session immediately, with no grace
// period. A grace period would be that same bug with a deadline.
type ParticipantResolver func(*http.Request) (room.ParticipantBinding, error)

// defaultParticipant is the honest description of a loopback attachment with
// no authenticated session: a local operator whose identity nothing has
// checked.
//
// It remains the fallback for embedders and transport tests that construct a
// handler without a session store; cmd/tangent always installs the real
// resolver. A handler left on this default is not enforcing anything, which is
// exactly the pre-ADR-0004 behavior and is why it is named for what it is.
func defaultParticipant(*http.Request) (room.ParticipantBinding, error) {
	return room.ParticipantBinding{
		Scope:        "operator:local",
		PrincipalRef: "local-operator",
		Authority:    "tangent-loopback",
		Assurance:    "loopback-unverified",
		// An unauthenticated attachment still gets the default participant
		// capability set: this fallback describes the pre-session behavior
		// exactly, so installing the real resolver is the only thing that
		// changes what a socket may do.
		Capabilities: authz.FormatCapabilities(authz.DefaultParticipantCapabilities()),
	}, nil
}

// Handler is the HTTP handler that upgrades incoming WS connections,
// binds them to a pre-existing Room, and runs the read loop.
//
// Rooms are NOT created on connect — a request must reference an
// existing roomID (one created server-side via Manager.Create when an
// MCP call decides to bridge to the UI). This is the rejection of
// "any tab can spawn a room": room ids are server-issued.
type Handler struct {
	manager *room.Manager
	logger  *slog.Logger

	// originPatterns are passed to coder/websocket.Accept. Defaults to
	// localhost-friendly patterns when empty (test code can override).
	originPatterns []string

	// participant establishes the principal an attachment acts as.
	participant ParticipantResolver
}

// New constructs a Handler with the given room manager and logger.
// Logger may be nil; Handler falls back to slog.Default.
func New(manager *room.Manager, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		manager: manager,
		logger:  logger,
		// Default origin policy: allow localhost variants at any port.
		// httptest's Server.URL host already passes the request-host
		// check (which is always allowed); these patterns cover real
		// browsers loading the SPA from localhost.
		originPatterns: []string{"localhost", "localhost:*", "127.0.0.1:*", "[::1]:*"},
		participant:    defaultParticipant,
	}
}

// SetOriginPatterns overrides the default origin allowlist. Used by
// tests to pin a specific pattern; production callers should leave the
// default until v0.2 bakes in proper auth.
func (h *Handler) SetOriginPatterns(patterns []string) {
	h.originPatterns = patterns
}

// SetParticipantResolver installs the principal resolver for new attachments.
// Passing nil restores the unverified loopback default.
func (h *Handler) SetParticipantResolver(resolver ParticipantResolver) {
	if resolver == nil {
		resolver = defaultParticipant
	}
	h.participant = resolver
}

// inboundMessage is the union shape of frames we accept from the client.
// Only `type` is universal; `envelopeId` is required for the disposition
// frames and `response` for type=response.
type inboundMessage struct {
	Type       string              `json:"type"`
	EnvelopeID string              `json:"envelopeId"`
	Revision   int64               `json:"revision,omitempty"`
	Response   *envelopes.Response `json:"response,omitempty"`
	// Takeover asks to revoke a live peer's resolver lease. It is only ever
	// set by an explicit operator action in the SPA.
	Takeover bool `json:"takeover,omitempty"`
}

// outboundError reports a rejected client action.
type outboundError struct {
	Type         string          `json:"type"`
	Code         string          `json:"code"`
	Message      string          `json:"message"`
	EnvelopeID   string          `json:"envelopeId,omitempty"`
	Revision     int64           `json:"revision,omitempty"`
	ConnectionID string          `json:"connectionId,omitempty"`
	Lease        *room.LeaseView `json:"lease,omitempty"`
}

// ServeHTTP implements http.Handler. It enforces the roomID query
// parameter, looks up the Room, accepts the upgrade, synchronizes the new
// connection from durable state, runs the read loop, and detaches on exit.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("roomID")
	if roomID == "" {
		http.Error(w, "missing roomID query parameter", http.StatusBadRequest)
		return
	}

	rm, ok := h.manager.Get(roomID)
	if !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}

	// The participant is established before the upgrade so a future rejection
	// is an ordinary HTTP status rather than a closed WebSocket.
	participant, err := h.participant(r)
	if err != nil {
		h.logger.Info("ws: participant rejected", "room", roomID, "err", err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// `view` is what attaching takes. Refusing here, before the upgrade, is
	// what makes an unauthorized attachment an ordinary 403 that a client and
	// a human can both read.
	if !participant.Holds(string(authz.View)) {
		h.logger.Info("ws: participant lacks view", "room", roomID)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: h.originPatterns,
		// Compression is disabled by default (per coder/websocket); we
		// don't override because envelope payloads are small JSON and
		// CPU > bandwidth on localhost.
	})
	if err != nil {
		h.logger.Warn("ws: accept failed", "room", roomID, "err", err)
		return
	}
	// SetReadLimit guards against runaway frames. v0.1 envelopes are
	// small; cap at 1 MiB which is generous and well below default-OS
	// memory pressure.
	conn.SetReadLimit(1 << 20)

	connection, err := rm.AttachConn(r.Context(), conn, room.AttachOptions{
		ClientID:    r.URL.Query().Get("clientID"),
		Label:       r.URL.Query().Get("label"),
		ClientKind:  clientKind(r),
		Role:        requestedRole(r),
		Participant: participant,
	})
	if err != nil {
		h.logger.Debug("ws: room closed before attachment", "room", roomID)
		_ = conn.Close(websocket.StatusGoingAway, "room closed")
		return
	}
	h.logger.Info("ws: connection attached",
		"room", roomID, "connection", connection.ID(),
		"client", connection.ClientID(), "role", rm.RoleOf(connection))

	// Connection state first, then the durable revision snapshot, then the
	// envelopes themselves. A client therefore knows who else is here and what
	// it is synchronized to before it is asked to render anything.
	if err := rm.SendConnectionState(r.Context(), connection); err != nil {
		h.logger.Debug("ws: connection state send failed", "room", roomID, "err", err)
	}
	if _, err := rm.SendSync(r.Context(), connection); err != nil {
		h.logger.Debug("ws: durable sync send failed", "room", roomID, "err", err)
	}
	if err := rm.ReplayPending(r.Context(), connection); err != nil &&
		!errors.Is(err, room.ErrStaleConnection) && !errors.Is(err, room.ErrNoConn) {
		h.logger.Info("ws: pending replay failed", "room", roomID, "err", err)
	}

	// readLoop runs until the conn closes or the context is cancelled.
	h.readLoop(r.Context(), rm, connection)

	// A socket is a replaceable presentation attachment. Read-loop exit only
	// updates connection state; explicit Room/Manager close is the authority
	// that terminalizes pending work.
	wasAttached := rm.DetachConn(connection)
	h.logger.Info("ws: connection detached",
		"room", roomID, "connection", connection.ID(), "was_attached", wasAttached)
}

// clientKind labels the sort of client behind an attachment for
// operator-facing display. It is descriptive only.
func clientKind(r *http.Request) string {
	if kind := r.URL.Query().Get("clientKind"); kind != "" {
		return kind
	}
	return "browser"
}

// requestedRole reads an explicit observer request. Any other value means the
// client would like to resolve and gets the lease when it is free.
func requestedRole(r *http.Request) room.ConnectionRole {
	if r.URL.Query().Get("role") == string(room.RoleObserver) {
		return room.RoleObserver
	}
	return roleUnspecified
}

// roleUnspecified is the zero ConnectionRole: "no preference, resolve if you
// can". It is named so the handler reads as a decision rather than an omission.
const roleUnspecified = room.ConnectionRole("")

// readLoop reads frames until error/close. Each frame is parsed as
// inboundMessage and routed to the Room. Unknown types are logged and dropped;
// malformed JSON likewise.
func (h *Handler) readLoop(ctx context.Context, rm *room.Room, c *room.Connection) {
	for {
		msgType, payload, err := c.Socket().Read(ctx)
		if err != nil {
			// Normal close paths return CloseError or context-canceled.
			// Either way, end the loop without yelling unless this is
			// genuinely unexpected.
			status := websocket.CloseStatus(err)
			switch {
			case errors.Is(err, context.Canceled):
				h.logger.Debug("ws: read loop ctx cancelled", "room", rm.ID)
			case status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway:
				h.logger.Debug("ws: clean close", "room", rm.ID, "status", status)
			default:
				h.logger.Info("ws: read error", "room", rm.ID, "err", err)
			}
			return
		}
		if msgType != websocket.MessageText {
			h.logger.Debug("ws: ignoring non-text frame", "room", rm.ID, "type", msgType)
			continue
		}
		var msg inboundMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			h.logger.Warn("ws: bad json from client", "room", rm.ID, "err", err)
			continue
		}
		// Any frame from the lease holder proves the tab is alive, which is
		// the only thing the lease TTL is measuring.
		rm.TouchResolver(c)
		h.dispatch(ctx, rm, c, msg)
	}
}

// dispatch routes one decoded client frame.
func (h *Handler) dispatch(ctx context.Context, rm *room.Room, c *room.Connection, msg inboundMessage) {
	switch msg.Type {
	case "response":
		if msg.EnvelopeID == "" || msg.Response == nil {
			h.logger.Warn("ws: response missing envelopeId or response", "room", rm.ID)
			return
		}
		// `view` got this socket attached; `resolve` is what a terminal answer
		// takes. A connection holds no capability of its own — the session that
		// opened it does — so the check reads the binding, never the socket.
		if !h.authorize(ctx, rm, c, msg, authz.Resolve) {
			return
		}
		if err := rm.HandleResponseFrom(c, msg.EnvelopeID, msg.Revision, msg.Response); err != nil {
			h.handleDispositionConflict(ctx, rm, c, msg, err)
			return
		}
		rm.BroadcastConnectionState(ctx)
	case "cancel":
		if msg.EnvelopeID == "" {
			h.logger.Warn("ws: cancel missing envelopeId", "room", rm.ID)
			return
		}
		// A participant may cancel only under the participant cause, which the
		// room applies; the capability is what decides whether it may cancel at
		// all.
		if !h.authorize(ctx, rm, c, msg, authz.Cancel) {
			return
		}
		if err := rm.HandleCancelFrom(c, msg.EnvelopeID, msg.Revision); err != nil {
			h.handleDispositionConflict(ctx, rm, c, msg, err)
			return
		}
		rm.BroadcastConnectionState(ctx)
	case "claim_resolver":
		if _, err := rm.ClaimResolver(c, msg.Takeover); err != nil {
			h.reportLeaseConflict(ctx, rm, c, msg, err)
			return
		}
		h.logger.Info("ws: resolver lease granted",
			"room", rm.ID, "connection", c.ID(), "takeover", msg.Takeover)
	case "release_resolver":
		rm.ReleaseResolver(c)
	case "resync":
		if err := rm.Resynchronize(ctx, c); err != nil {
			h.logger.Info("ws: resync failed", "room", rm.ID, "connection", c.ID(), "err", err)
		}
	case "heartbeat":
		// TouchResolver in the read loop already did the work.
	default:
		h.logger.Debug("ws: ignoring unknown frame type", "room", rm.ID, "ws_type", msg.Type)
	}
}

// authorize checks one frame against the capability set of the session that
// opened the connection, and reports the refusal to the client when it fails.
//
// The refusal names nothing beyond "not authorized". An authorization failure
// that reports the required capability, the owning scope, or the participant
// is a probe, so the message stays fixed (ADR 0004 §6.5).
func (h *Handler) authorize(
	ctx context.Context,
	rm *room.Room,
	c *room.Connection,
	msg inboundMessage,
	capability authz.Capability,
) bool {
	if c.Participant().Holds(string(capability)) {
		return true
	}
	h.sendError(ctx, c, outboundError{
		Code:         errorCodeNotAuthorized,
		Message:      "this browser session is not authorized to answer here",
		EnvelopeID:   msg.EnvelopeID,
		Revision:     msg.Revision,
		ConnectionID: c.ID(),
	})
	h.logger.Info("ws: frame refused",
		"room", rm.ID, "connection", c.ID(), "ws_type", msg.Type)
	return false
}

// handleDispositionConflict turns a refused response/cancel into something the
// client can act on: a lease conflict says who holds the surface, and a
// revision conflict is answered with a fresh presentation.
func (h *Handler) handleDispositionConflict(
	ctx context.Context,
	rm *room.Room,
	c *room.Connection,
	msg inboundMessage,
	err error,
) {
	switch {
	case errors.Is(err, room.ErrResolverLeaseHeld):
		h.reportLeaseConflict(ctx, rm, c, msg, err)
	case errors.Is(err, room.ErrStaleConnection):
		h.logger.Debug("ws: ignored disposition from detached connection",
			"room", rm.ID, "connection", c.ID(), "envelope", msg.EnvelopeID)
	case errors.Is(err, room.ErrRoomClosed):
		h.sendError(ctx, c, outboundError{
			Code: errorCodeRoomClosed, Message: err.Error(), EnvelopeID: msg.EnvelopeID,
		})
	case errors.Is(err, room.ErrPresentationRevisionConflict):
		h.logger.Debug("ws: stale presentation revision; resynchronizing",
			"room", rm.ID, "connection", c.ID(), "envelope", msg.EnvelopeID)
		h.sendError(ctx, c, outboundError{
			Code:       errorCodeStalePresentation,
			Message:    "the presentation this response was rendered from is no longer current",
			EnvelopeID: msg.EnvelopeID,
			Revision:   msg.Revision,
		})
		if replayErr := rm.ReplayEnvelope(ctx, c, msg.EnvelopeID); replayErr != nil &&
			!errors.Is(replayErr, room.ErrStaleConnection) {
			h.logger.Info("ws: revision resync failed",
				"room", rm.ID, "envelope", msg.EnvelopeID, "err", replayErr)
		}
	default:
		h.logger.Warn("ws: disposition failed",
			"room", rm.ID, "envelope", msg.EnvelopeID, "err", err)
	}
}

// reportLeaseConflict tells a losing client exactly which connection holds the
// surface and until when, then re-sends connection state so its UI settles on
// the observer role rather than silently appearing broken.
func (h *Handler) reportLeaseConflict(
	ctx context.Context,
	rm *room.Room,
	c *room.Connection,
	msg inboundMessage,
	err error,
) {
	frame := outboundError{
		Code:         errorCodeResolverLeaseHeld,
		Message:      err.Error(),
		EnvelopeID:   msg.EnvelopeID,
		Revision:     msg.Revision,
		ConnectionID: c.ID(),
	}
	conflict := &room.LeaseConflictError{}
	if errors.As(err, &conflict) {
		holder := conflict.Holder
		frame.Lease = &holder
	}
	h.sendError(ctx, c, frame)
	if stateErr := rm.SendConnectionState(ctx, c); stateErr != nil {
		h.logger.Debug("ws: lease conflict state send failed", "room", rm.ID, "err", stateErr)
	}
	h.logger.Info("ws: resolver lease conflict",
		"room", rm.ID, "connection", c.ID(), "envelope", msg.EnvelopeID)
}

func (h *Handler) sendError(ctx context.Context, c *room.Connection, frame outboundError) {
	frame.Type = "error"
	if err := c.WriteJSON(ctx, frame); err != nil {
		h.logger.Debug("ws: error frame send failed", "connection", c.ID(), "err", err)
	}
}
