package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/room"
)

// The browser room API replaces the SPA's direct `/mcp` POSTs.
//
// Until now `ui/src/routes/Room.tsx` and `ui/src/components/TabStrip.tsx`
// reached `tangent.session_get`, `tangent.session_list`, and
// `tangent.session_close` by posting JSON-RPC at `/mcp`, a route with no
// origin, same-site, or CSRF middleware at all. The browser was therefore both
// participant and caller, through a route any origin could reach — and once
// `session_close` became partition-enforcing, that same route was the one the
// tab-strip close button depended on.
//
// This surface mirrors `/api/hitl` deliberately: participant-session
// authenticated, origin guarded, `Cache-Control: no-store`, and the same JSON
// payload shapes the `session_*` tools already return, so migrating the SPA is
// a transport change and not a contract change.

// RoomService is the browser room API's narrow application boundary.
//
// It is declared here rather than importing internal/mcp so the dependency
// stays one-way, exactly as MCPServer and HITLService already do. Its
// implementation acts as the SPA's own caller application (ADR 0004 §1), which
// is why closing a room through it is subject to the same partition rule as
// closing one through the MCP tool.
type RoomService interface {
	// ListBrowserRooms returns the rooms visible to the browser caller.
	ListBrowserRooms(ctx context.Context, activeOnly bool) ([]room.RoomSummary, error)
	// InspectBrowserRoom returns one room's projection. The payload shape is
	// the `tangent.session_get` result, unchanged.
	InspectBrowserRoom(ctx context.Context, roomID string) (any, error)
	// CloseBrowserRoom closes one room under the browser caller's identity.
	CloseBrowserRoom(ctx context.Context, roomID, status string) error
}

type roomHTTPHandler struct {
	service RoomService
}

type closeRoomCommand struct {
	Status string `json:"status,omitempty"`
}

func newRoomHTTPHandler(service RoomService) *roomHTTPHandler {
	return &roomHTTPHandler{service: service}
}

func (h *roomHTTPHandler) list(w http.ResponseWriter, r *http.Request) {
	// The tab strip only ever wants live rooms, and a closed-room listing is a
	// different question with a different answer shape. `active_only` stays an
	// explicit query parameter so the default is not silently load-bearing.
	activeOnly := r.URL.Query().Get("active_only") != "false"
	rooms, err := h.service.ListBrowserRooms(r.Context(), activeOnly)
	if err != nil {
		writeRoomError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, map[string]any{"rooms": rooms})
}

func (h *roomHTTPHandler) inspect(w http.ResponseWriter, r *http.Request) {
	state, err := h.service.InspectBrowserRoom(r.Context(), r.PathValue("roomID"))
	if err != nil {
		writeRoomError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, state)
}

func (h *roomHTTPHandler) close(w http.ResponseWriter, r *http.Request) {
	command := closeRoomCommand{}
	// A close carries no required body. An empty one is the ordinary case, so
	// only a present body is parsed, and a malformed one is still an error
	// rather than a silently ignored intent.
	if r.ContentLength > 0 {
		if err := decodeHITLCommand(w, r, &command); err != nil {
			writeRoomError(w, err)
			return
		}
	}
	roomID := r.PathValue("roomID")
	if err := h.service.CloseBrowserRoom(r.Context(), roomID, command.Status); err != nil {
		writeRoomError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, map[string]any{"ok": true, "roomID": roomID})
}

// writeRoomError renders a refused or failed room operation.
//
// The two refusal shapes are the ADR 0004 §5 contract and must not be
// collapsed: 403 within the caller's own authority, where a local user needs
// something debuggable, and 404 across authorities, so a foreign authority
// cannot probe for existence. Neither body names the required capability, the
// owning scope, the session, or the participant.
func writeRoomError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, interaction.ErrNotFound), errors.Is(err, room.ErrRoomNotFound):
		writeHITLJSON(w, http.StatusNotFound, map[string]any{
			"code": "not_found", "message": "room not found",
		})
	case errors.Is(err, interaction.ErrUnauthorized):
		writeHITLJSON(w, http.StatusForbidden, map[string]any{
			"code": "forbidden", "message": "not authorized for that room",
		})
	case errors.Is(err, interaction.ErrRevisionConflict), errors.Is(err, interaction.ErrTerminal):
		writeHITLJSON(w, http.StatusConflict, map[string]any{
			"code": "conflict", "message": err.Error(),
		})
	case errors.Is(err, errHITLContentType):
		writeHITLJSON(w, http.StatusUnsupportedMediaType, map[string]any{
			"code": "unsupported_media_type", "message": err.Error(),
		})
	default:
		writeHITLJSON(w, http.StatusInternalServerError, map[string]any{
			"code": "room_error", "message": err.Error(),
		})
	}
}
