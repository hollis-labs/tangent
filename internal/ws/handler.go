// Package ws hosts the HTTP→WebSocket upgrade handler that fronts the
// Room manager. One handler instance serves /ws for the lifetime of the
// Tangent process; per-connection state lives on the Room.
//
// Wire shape:
//
//	server → client : {"type":"envelope","envelopeId":"<id>","revision":1,"envelope":{...}}
//	client → server : {"type":"response","envelopeId":"<id>","revision":1,"response":{...}}
//	client → server : {"type":"cancel","envelopeId":"<id>","revision":1}
//
// Anything else from the client is logged and ignored. The handler is
// deliberately dumb — Room owns the lifecycle of the Pending entries
// and emits errors via Push's return value, not via WS frames.
//
// Origin policy: localhost-only in v0.1. We rely on
// AcceptOptions.OriginPatterns="localhost*"+InsecureSkipVerify=false.
// Production deployments will tighten this; the brief explicitly
// permits "no auth in v0.1, localhost only."
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"
	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/room"
)

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
	}
}

// SetOriginPatterns overrides the default origin allowlist. Used by
// tests to pin a specific pattern; production callers should leave the
// default until v0.2 bakes in proper auth.
func (h *Handler) SetOriginPatterns(patterns []string) {
	h.originPatterns = patterns
}

// inboundMessage is the union shape of frames we accept from the client.
// Only `type` and `envelopeId` are universal; `response` is required for
// type=response.
type inboundMessage struct {
	Type       string              `json:"type"`
	EnvelopeID string              `json:"envelopeId"`
	Revision   int64               `json:"revision,omitempty"`
	Response   *envelopes.Response `json:"response,omitempty"`
}

// ServeHTTP implements http.Handler. It enforces the roomID query
// parameter, looks up the Room, accepts the upgrade, runs the read
// loop, and detaches/closes on exit.
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

	// Bind the conn to the Room. Replaces any prior conn (refresh-tab
	// semantics).
	if err := rm.AttachConn(r.Context(), conn); err != nil {
		h.logger.Debug("ws: room closed before attachment", "room", roomID)
		_ = conn.Close(websocket.StatusGoingAway, "room closed")
		return
	}
	h.logger.Info("ws: room attached", "room", roomID)
	if err := rm.ReplayPending(r.Context(), conn); err != nil && !errors.Is(err, room.ErrStaleConnection) {
		h.logger.Info("ws: pending replay failed", "room", roomID, "err", err)
	}

	// readLoop runs until the conn closes or the context is cancelled.
	h.readLoop(r.Context(), rm, conn)

	// A socket is a replaceable presentation attachment. Read-loop exit only
	// updates connection state; explicit Room/Manager close is the authority
	// that terminalizes pending work.
	wasActive := rm.DetachConn(conn)
	h.logger.Info("ws: room detached", "room", roomID, "was_active", wasActive)
}

// readLoop reads frames until error/close. Each frame is parsed as
// inboundMessage and routed to Room.HandleResponse / HandleCancel.
// Unknown types are logged and dropped; malformed JSON likewise.
func (h *Handler) readLoop(ctx context.Context, rm *room.Room, conn *websocket.Conn) {
	for {
		msgType, payload, err := conn.Read(ctx)
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
		switch msg.Type {
		case "response":
			if msg.EnvelopeID == "" || msg.Response == nil {
				h.logger.Warn("ws: response missing envelopeId or response", "room", rm.ID)
				continue
			}
			if err := rm.HandleResponseFrom(conn, msg.EnvelopeID, msg.Revision, msg.Response); err != nil {
				h.handleDispositionConflict(ctx, rm, conn, msg.EnvelopeID, err)
			}
		case "cancel":
			if msg.EnvelopeID == "" {
				h.logger.Warn("ws: cancel missing envelopeId", "room", rm.ID)
				continue
			}
			if err := rm.HandleCancelFrom(conn, msg.EnvelopeID, msg.Revision); err != nil {
				h.handleDispositionConflict(ctx, rm, conn, msg.EnvelopeID, err)
			}
		default:
			h.logger.Debug("ws: ignoring unknown frame type", "room", rm.ID, "ws_type", msg.Type)
		}
	}
}

func (h *Handler) handleDispositionConflict(
	ctx context.Context,
	rm *room.Room,
	conn *websocket.Conn,
	envelopeID string,
	err error,
) {
	if errors.Is(err, room.ErrStaleConnection) {
		h.logger.Debug("ws: ignored disposition from replaced connection", "room", rm.ID, "envelope", envelopeID)
		return
	}
	if !errors.Is(err, room.ErrPresentationRevisionConflict) {
		h.logger.Warn("ws: disposition failed", "room", rm.ID, "envelope", envelopeID, "err", err)
		return
	}
	h.logger.Debug("ws: stale presentation revision; resynchronizing", "room", rm.ID, "envelope", envelopeID)
	if replayErr := rm.ReplayEnvelope(ctx, conn, envelopeID); replayErr != nil && !errors.Is(replayErr, room.ErrStaleConnection) {
		h.logger.Info("ws: revision resync failed", "room", rm.ID, "envelope", envelopeID, "err", replayErr)
	}
}
