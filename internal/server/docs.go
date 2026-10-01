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

	"github.com/hollis-labs/tangent/internal/docs"
	"github.com/hollis-labs/tangent/internal/interaction"
)

const (
	docsMaxCommandBytes = 16 * 1024
	docsEventPoll       = 750 * time.Millisecond
	docsEventLifetime   = 50 * time.Second
)

var errDocsContentType = errors.New("docs API requires application/json")

// DocsService is the application service boundary for the Docs inbox.
type DocsService interface {
	Inbox(context.Context) (docs.DocsInbox, error)
	InspectDoc(context.Context, string) (docs.DocItemView, error)
	MarkRead(context.Context, string) (docs.DocItemView, error)
	Acknowledge(context.Context, docs.AcknowledgeInput) (docs.DocItemView, error)
	Archive(context.Context, docs.ArchiveInput) (docs.DocItemView, error)
	Delete(context.Context, docs.ArchiveInput) (docs.DocItemView, error)
}

type docsHTTPHandler struct {
	service       DocsService
	eventPoll     time.Duration
	eventLifetime time.Duration
}

func newDocsHTTPHandler(service DocsService) *docsHTTPHandler {
	return &docsHTTPHandler{
		service:       service,
		eventPoll:     docsEventPoll,
		eventLifetime: docsEventLifetime,
	}
}

type docsAcknowledgeCommand struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Note             string `json:"note,omitempty"`
}

type docsArchiveCommand struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason,omitempty"`
}

func (h *docsHTTPHandler) inbox(w http.ResponseWriter, r *http.Request) {
	inbox, err := h.service.Inbox(r.Context())
	if err != nil {
		writeDocsError(w, err)
		return
	}
	writeDocsJSON(w, http.StatusOK, inbox)
}

func (h *docsHTTPHandler) item(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.InspectDoc(r.Context(), r.PathValue("itemID"))
	if err != nil {
		writeDocsError(w, err)
		return
	}
	writeDocsJSON(w, http.StatusOK, item)
}

func (h *docsHTTPHandler) markRead(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.MarkRead(r.Context(), r.PathValue("itemID"))
	if err != nil {
		writeDocsError(w, err)
		return
	}
	writeDocsJSON(w, http.StatusOK, item)
}

func (h *docsHTTPHandler) acknowledge(w http.ResponseWriter, r *http.Request) {
	var cmd docsAcknowledgeCommand
	if err := decodeDocsCommand(w, r, &cmd); err != nil {
		writeDocsError(w, err)
		return
	}
	item, err := h.service.Acknowledge(r.Context(), docs.AcknowledgeInput{
		ItemID:           r.PathValue("itemID"),
		ExpectedRevision: cmd.ExpectedRevision,
		Note:             cmd.Note,
	})
	if err != nil {
		writeDocsError(w, err)
		return
	}
	writeDocsJSON(w, http.StatusOK, item)
}

func (h *docsHTTPHandler) archive(w http.ResponseWriter, r *http.Request) {
	var cmd docsArchiveCommand
	if err := decodeDocsCommand(w, r, &cmd); err != nil {
		writeDocsError(w, err)
		return
	}
	item, err := h.service.Archive(r.Context(), docs.ArchiveInput{
		ItemID:           r.PathValue("itemID"),
		ExpectedRevision: cmd.ExpectedRevision,
		Reason:           cmd.Reason,
	})
	if err != nil {
		writeDocsError(w, err)
		return
	}
	writeDocsJSON(w, http.StatusOK, item)
}

func (h *docsHTTPHandler) delete(w http.ResponseWriter, r *http.Request) {
	var cmd docsArchiveCommand
	if err := decodeDocsCommand(w, r, &cmd); err != nil {
		writeDocsError(w, err)
		return
	}
	item, err := h.service.Delete(r.Context(), docs.ArchiveInput{
		ItemID:           r.PathValue("itemID"),
		ExpectedRevision: cmd.ExpectedRevision,
		Reason:           cmd.Reason,
	})
	if err != nil {
		writeDocsError(w, err)
		return
	}
	writeDocsJSON(w, http.StatusOK, item)
}

func (h *docsHTTPHandler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	inbox, err := h.service.Inbox(r.Context())
	if err != nil {
		writeDocsError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if err := writeDocsRevisionEvent(w, inbox.Revision); err != nil {
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
			if err := writeDocsRevisionEvent(w, current.Revision); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeDocsRevisionEvent(w io.Writer, revision string) error {
	payload, err := json.Marshal(map[string]string{"revision": revision})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: revision\ndata: %s\n\n", payload)
	return err
}

func decodeDocsCommand(w http.ResponseWriter, r *http.Request, target any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errDocsContentType
	}
	r.Body = http.MaxBytesReader(w, r.Body, docsMaxCommandBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(target); decodeErr != nil {
		return fmt.Errorf("%w: decode command: %w", docs.ErrInvalidRequest, decodeErr)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: command must contain one JSON object", docs.ErrInvalidRequest)
	}
	return nil
}

func writeDocsJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeDocsError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "docs_error"
	response := map[string]any{
		"contract_version": docs.ContractVersion,
		"code":             code,
		"message":          err.Error(),
	}
	switch {
	case errors.Is(err, errDocsContentType):
		status = http.StatusUnsupportedMediaType
		response["code"] = "unsupported_media_type"
	case errors.Is(err, docs.ErrNotFound), errors.Is(err, interaction.ErrNotFound):
		status = http.StatusNotFound
		response["code"] = "not_found"
	case errors.Is(err, interaction.ErrUnauthorized):
		status = http.StatusForbidden
		response["code"] = "forbidden"
	case errors.Is(err, docs.ErrInvalidRequest), errors.Is(err, interaction.ErrDefinitionValidation):
		status = http.StatusBadRequest
		response["code"] = "invalid_request"
	case errors.Is(err, docs.ErrTerminalConflict), errors.Is(err, docs.ErrStaleRevision),
		errors.Is(err, interaction.ErrRevisionConflict), errors.Is(err, interaction.ErrTerminal),
		errors.Is(err, interaction.ErrNotRespondable), errors.Is(err, interaction.ErrIdempotencyConflict):
		status = http.StatusConflict
		response["code"] = "conflict"
	}
	writeDocsJSON(w, status, response)
}
