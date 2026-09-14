package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/turns"
)

const (
	turnsMaxCommandBytes = 64 * 1024
	turnsEventPoll       = 750 * time.Millisecond
	turnsEventLifetime   = 50 * time.Second
)

var errTurnsContentType = errors.New("turns API requires application/json")

// TurnsService is the application service boundary for agent turns.
type TurnsService interface {
	Inbox(context.Context) (turns.TurnsInbox, error)
	InspectTurn(context.Context, string) (turns.TurnItemView, error)
	Reply(context.Context, turns.ReplyInput) (turns.TurnItemView, error)
	Dismiss(context.Context, turns.DismissInput) (turns.TurnItemView, error)
	Enqueue(context.Context, turns.EnqueueInput) (turns.TurnHandle, error)
	Ack(context.Context, turns.AckInput) error
	SessionReplies(context.Context, string) ([]turns.TurnItemView, error)
}

type turnsHTTPHandler struct {
	service       TurnsService
	eventPoll     time.Duration
	eventLifetime time.Duration
}

func newTurnsHTTPHandler(service TurnsService) *turnsHTTPHandler {
	return &turnsHTTPHandler{
		service:       service,
		eventPoll:     turnsEventPoll,
		eventLifetime: turnsEventLifetime,
	}
}

type turnReplyCommand struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Action           string `json:"action"`
	ResponseText     string `json:"response_text,omitempty"`
	SelectedOption   string `json:"selected_option,omitempty"`
	Note             string `json:"note,omitempty"`
}

type turnDismissCommand struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason,omitempty"`
}

type turnAckCommand struct {
	ReplyID string `json:"reply_id,omitempty"`
}

func (h *turnsHTTPHandler) inbox(w http.ResponseWriter, r *http.Request) {
	inbox, err := h.service.Inbox(r.Context())
	if err != nil {
		writeTurnsError(w, err)
		return
	}
	writeTurnsJSON(w, http.StatusOK, inbox)
}

func (h *turnsHTTPHandler) item(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.InspectTurn(r.Context(), r.PathValue("itemID"))
	if err != nil {
		writeTurnsError(w, err)
		return
	}
	writeTurnsJSON(w, http.StatusOK, item)
}

func (h *turnsHTTPHandler) reply(w http.ResponseWriter, r *http.Request) {
	var cmd turnReplyCommand
	if err := decodeTurnsCommand(w, r, &cmd); err != nil {
		writeTurnsError(w, err)
		return
	}
	item, err := h.service.Reply(r.Context(), turns.ReplyInput{
		ItemID:           r.PathValue("itemID"),
		ExpectedRevision: cmd.ExpectedRevision,
		Action:           cmd.Action,
		ResponseText:     cmd.ResponseText,
		SelectedOption:   cmd.SelectedOption,
		Note:             cmd.Note,
	})
	if err != nil {
		writeTurnsError(w, err)
		return
	}
	writeTurnsJSON(w, http.StatusOK, item)
}

func (h *turnsHTTPHandler) dismiss(w http.ResponseWriter, r *http.Request) {
	var cmd turnDismissCommand
	if err := decodeTurnsCommand(w, r, &cmd); err != nil {
		writeTurnsError(w, err)
		return
	}
	item, err := h.service.Dismiss(r.Context(), turns.DismissInput{
		ItemID:           r.PathValue("itemID"),
		ExpectedRevision: cmd.ExpectedRevision,
		Reason:           cmd.Reason,
	})
	if err != nil {
		writeTurnsError(w, err)
		return
	}
	writeTurnsJSON(w, http.StatusOK, item)
}

func (h *turnsHTTPHandler) enqueue(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeTurnsError(w, errTurnsContentType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, turnsMaxCommandBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeTurnsError(w, fmt.Errorf("%w: read body: %w", turns.ErrInvalidRequest, err))
		return
	}
	caller := interaction.ActorBinding{
		Scope:        "standalone-local:tether",
		PrincipalRef: "tether",
		Authority:    "standalone-local",
		Assurance:    "loopback-unverified",
	}
	handle, err := h.service.Enqueue(r.Context(), turns.EnqueueInput{
		Request: body,
		Caller:  caller,
	})
	if err != nil {
		writeTurnsError(w, err)
		return
	}
	writeTurnsJSON(w, http.StatusCreated, handle)
}

func (h *turnsHTTPHandler) ack(w http.ResponseWriter, r *http.Request) {
	var cmd turnAckCommand
	if r.Body != nil && r.ContentLength > 0 {
		if err := decodeTurnsCommand(w, r, &cmd); err != nil {
			writeTurnsError(w, err)
			return
		}
	}
	if err := h.service.Ack(r.Context(), turns.AckInput{
		ItemID:  r.PathValue("itemID"),
		ReplyID: cmd.ReplyID,
	}); err != nil {
		writeTurnsError(w, err)
		return
	}
	writeTurnsJSON(w, http.StatusOK, map[string]any{
		"contract_version": turns.ContractVersion,
		"status":           "acknowledged",
		"item_id":          r.PathValue("itemID"),
	})
}

func (h *turnsHTTPHandler) sessionReplies(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")
	replies, err := h.service.SessionReplies(r.Context(), sessionID)
	if err != nil {
		writeTurnsError(w, err)
		return
	}
	if replies == nil {
		replies = make([]turns.TurnItemView, 0)
	}
	writeTurnsJSON(w, http.StatusOK, map[string]any{
		"contract_version": turns.ContractVersion,
		"session_id":       sessionID,
		"replies":          replies,
	})
}

func (h *turnsHTTPHandler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	inbox, err := h.service.Inbox(r.Context())
	if err != nil {
		writeTurnsError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if err := writeTurnsRevisionEvent(w, inbox.Revision); err != nil {
		return
	}
	flusher.Flush()

	lastRevision := inbox.Revision
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
			current, loadErr := h.service.Inbox(r.Context())
			if loadErr != nil {
				return
			}
			if current.Revision == lastRevision {
				continue
			}
			lastRevision = current.Revision
			if err := writeTurnsRevisionEvent(w, current.Revision); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeTurnsRevisionEvent(w io.Writer, revision string) error {
	payload, err := json.Marshal(map[string]string{"revision": revision})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: revision\ndata: %s\n\n", payload)
	return err
}

func decodeTurnsCommand(w http.ResponseWriter, r *http.Request, target any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errTurnsContentType
	}
	r.Body = http.MaxBytesReader(w, r.Body, turnsMaxCommandBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(target); decodeErr != nil {
		return fmt.Errorf("%w: decode command: %w", turns.ErrInvalidRequest, decodeErr)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: command must contain one JSON object", turns.ErrInvalidRequest)
	}
	return nil
}

func writeTurnsJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeTurnsError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "turns_error"
	response := map[string]any{
		"contract_version": turns.ContractVersion,
		"code":             code,
		"message":          err.Error(),
	}
	switch {
	case errors.Is(err, errTurnsContentType):
		status = http.StatusUnsupportedMediaType
		response["code"] = "unsupported_media_type"
	case errors.Is(err, turns.ErrNotFound), errors.Is(err, interaction.ErrNotFound):
		status = http.StatusNotFound
		response["code"] = "not_found"
	case errors.Is(err, interaction.ErrUnauthorized):
		status = http.StatusForbidden
		response["code"] = "forbidden"
	case errors.Is(err, turns.ErrInvalidRequest), errors.Is(err, interaction.ErrDefinitionValidation):
		status = http.StatusBadRequest
		response["code"] = "invalid_request"
	case errors.Is(err, turns.ErrTerminalConflict), errors.Is(err, turns.ErrStaleRevision),
		errors.Is(err, interaction.ErrRevisionConflict), errors.Is(err, interaction.ErrTerminal),
		errors.Is(err, interaction.ErrNotRespondable), errors.Is(err, interaction.ErrIdempotencyConflict):
		status = http.StatusConflict
		response["code"] = "conflict"
	}
	writeTurnsJSON(w, status, response)
}

func turnsLoopbackOrParticipantRoute(
	mux *http.ServeMux,
	routes *[]ParticipantRoute,
	cfg Config,
	pattern string,
	capability authz.Capability,
	handler http.HandlerFunc,
) {
	*routes = append(*routes, ParticipantRoute{Pattern: pattern, Capability: capability})
	guarded := hitlSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Non-browser loopback callers (like Tether daemon or test clients) without Origin/Sec-Fetch headers
		// do not carry a browser cookie, but are permitted as local daemons.
		if requestHostIsLoopback(r.Host) && r.Header.Get("Sec-Fetch-Site") == "" && r.Header.Get("Origin") == "" {
			handler(w, r)
			return
		}
		requireParticipant(cfg.Participants, cfg.Telemetry, capability, handler).ServeHTTP(w, r)
	}))
	mux.Handle(pattern, guarded)
}
