package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/hollis-labs/tangent/internal/channel"
	"github.com/hollis-labs/tangent/internal/channelpane"
	"github.com/hollis-labs/tangent/internal/relay"
)

// The channel pane's browser API (CW-20260907-0017).
//
// This surface is the operator's send/read path over channels — 0066
// shipped the agent-facing tangent.relay_* MCP tools only, and its package
// doc says so explicitly. An operator is not an MCP client; this is a REST
// route over channel.Store and relay.Store (via internal/channelpane),
// mirroring /api/hitl and /api/rooms rather than reaching for the tool
// surface.

// ChannelService is the channel pane's narrow application boundary,
// satisfied by *channelpane.Service.
type ChannelService interface {
	ListChannels(ctx context.Context) ([]channelpane.ChannelSummary, error)
	GetChannel(ctx context.Context, channelID string) (channelpane.ChannelDetail, error)
	SendMessage(ctx context.Context, channelID string, input channelpane.SendInput) (channelpane.MessageView, error)
	MarkRead(ctx context.Context, channelID string) (int, error)
	Revision(ctx context.Context) (string, error)
}

type channelHTTPHandler struct {
	service       ChannelService
	eventPoll     time.Duration
	eventLifetime time.Duration
}

func newChannelHTTPHandler(service ChannelService) *channelHTTPHandler {
	return &channelHTTPHandler{service: service, eventPoll: hitlEventPoll, eventLifetime: hitlEventLifetime}
}

func (h *channelHTTPHandler) list(w http.ResponseWriter, r *http.Request) {
	channels, err := h.service.ListChannels(r.Context())
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, map[string]any{"channels": channels})
}

func (h *channelHTTPHandler) get(w http.ResponseWriter, r *http.Request) {
	detail, err := h.service.GetChannel(r.Context(), r.PathValue("channelID"))
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, detail)
}

type sendChannelMessageCommand struct {
	Body                   string `json:"body"`
	RecipientApplicationID string `json:"recipient_application_id,omitempty"`
	RecipientAgentID       string `json:"recipient_agent_id,omitempty"`
}

func (h *channelHTTPHandler) send(w http.ResponseWriter, r *http.Request) {
	var command sendChannelMessageCommand
	if err := decodeHITLCommand(w, r, &command); err != nil {
		writeChannelError(w, err)
		return
	}
	message, err := h.service.SendMessage(r.Context(), r.PathValue("channelID"), channelpane.SendInput{
		Body: command.Body, RecipientApplicationID: command.RecipientApplicationID, RecipientAgentID: command.RecipientAgentID,
	})
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, message)
}

// events sends only revision notifications, never state — the browser
// always follows one with a ListChannels refetch, matching /api/hitl/events'
// contract exactly (see its own doc comment for why: a dropped or reordered
// event can never become an alternate source of truth this way).
func (h *channelHTTPHandler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	revision, err := h.service.Revision(r.Context())
	if err != nil {
		writeChannelError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if err := writeHITLRevisionEvent(w, revision); err != nil {
		return
	}
	flusher.Flush()

	lastRevision := revision
	poll := time.NewTicker(h.eventPoll)
	keepAlive := time.NewTicker(15 * time.Second)
	lifetime := time.NewTimer(h.eventLifetime)
	defer poll.Stop()
	defer keepAlive.Stop()
	defer lifetime.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-lifetime.C:
			return
		case <-keepAlive.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-poll.C:
			current, loadErr := h.service.Revision(r.Context())
			if loadErr != nil {
				return
			}
			if current == lastRevision {
				continue
			}
			lastRevision = current
			if err := writeHITLRevisionEvent(w, current); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *channelHTTPHandler) markRead(w http.ResponseWriter, r *http.Request) {
	count, err := h.service.MarkRead(r.Context(), r.PathValue("channelID"))
	if err != nil {
		writeChannelError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, map[string]any{"marked_read": count})
}

// writeChannelError maps a channelpane error to the same
// {"code","message"} shape /api/hitl and /api/rooms already use.
func writeChannelError(w http.ResponseWriter, err error) {
	var ambiguous *channelpane.AmbiguousRecipientError
	switch {
	case errors.As(err, &ambiguous):
		writeHITLJSON(w, http.StatusConflict, map[string]any{"code": "ambiguous_recipient", "message": err.Error()})
	case errors.Is(err, channel.ErrNotFound), errors.Is(err, relay.ErrNotFound):
		writeHITLJSON(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "channel not found"})
	case errors.Is(err, channel.ErrInvalidRecord), errors.Is(err, relay.ErrInvalidRecord):
		writeHITLJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_request", "message": err.Error()})
	case errors.Is(err, relay.ErrNotMember):
		writeHITLJSON(w, http.StatusConflict, map[string]any{"code": "not_a_member", "message": err.Error()})
	case errors.Is(err, errHITLContentType):
		writeHITLJSON(w, http.StatusUnsupportedMediaType, map[string]any{"code": "unsupported_media_type", "message": err.Error()})
	default:
		writeHITLJSON(w, http.StatusInternalServerError, map[string]any{"code": "channel_error", "message": err.Error()})
	}
}
