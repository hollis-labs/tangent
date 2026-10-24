package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tangent/internal/artifacts"
	"github.com/hollis-labs/tangent/internal/interaction"
)

const (
	artifactsMaxCommandBytes = 16 * 1024
	artifactsEventPoll       = 750 * time.Millisecond
	artifactsEventLifetime   = 50 * time.Second
)

var errArtifactsContentType = errors.New("artifacts API requires application/json")

// ArtifactsService is the application service boundary for the artifact inbox.
type ArtifactsService interface {
	Inbox(context.Context) (artifacts.Inbox, error)
	Revision(context.Context) (string, error)
	Inspect(context.Context, string) (artifacts.View, error)
	MarkRead(context.Context, string) (artifacts.View, error)
	OperatorMessage(context.Context, artifacts.OperatorMessageInput) (artifacts.View, error)
	Signoff(context.Context, artifacts.SignoffInput) (artifacts.View, error)
	Archive(context.Context, artifacts.ArchiveInput) (artifacts.View, error)
	Delete(context.Context, artifacts.ArchiveInput) (artifacts.View, error)
	OpenRevision(context.Context, string, int) (artifacts.Revision, *os.File, error)
}

type artifactsHTTPHandler struct {
	service       ArtifactsService
	eventPoll     time.Duration
	eventLifetime time.Duration
}

func newArtifactsHTTPHandler(service ArtifactsService) *artifactsHTTPHandler {
	return &artifactsHTTPHandler{service: service, eventPoll: artifactsEventPoll, eventLifetime: artifactsEventLifetime}
}

type artifactMessageCommand struct {
	Body string `json:"body"`
	Kind string `json:"kind,omitempty"`
}

type artifactSignoffCommand struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Note             string `json:"note,omitempty"`
}

type artifactArchiveCommand struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason,omitempty"`
}

func (h *artifactsHTTPHandler) inbox(w http.ResponseWriter, r *http.Request) {
	inbox, err := h.service.Inbox(r.Context())
	if err != nil {
		writeArtifactsError(w, err)
		return
	}
	writeArtifactsJSON(w, http.StatusOK, inbox)
}

func (h *artifactsHTTPHandler) item(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Inspect(r.Context(), r.PathValue("itemID"))
	if err != nil {
		writeArtifactsError(w, err)
		return
	}
	writeArtifactsJSON(w, http.StatusOK, item)
}

func (h *artifactsHTTPHandler) markRead(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.MarkRead(r.Context(), r.PathValue("itemID"))
	if err != nil {
		writeArtifactsError(w, err)
		return
	}
	writeArtifactsJSON(w, http.StatusOK, item)
}

func (h *artifactsHTTPHandler) message(w http.ResponseWriter, r *http.Request) {
	var cmd artifactMessageCommand
	if err := decodeArtifactsCommand(w, r, &cmd); err != nil {
		writeArtifactsError(w, err)
		return
	}
	item, err := h.service.OperatorMessage(r.Context(), artifacts.OperatorMessageInput{
		ItemID: r.PathValue("itemID"), Body: cmd.Body, Kind: cmd.Kind,
	})
	if err != nil {
		writeArtifactsError(w, err)
		return
	}
	writeArtifactsJSON(w, http.StatusOK, item)
}

func (h *artifactsHTTPHandler) signoff(w http.ResponseWriter, r *http.Request) {
	var cmd artifactSignoffCommand
	if err := decodeArtifactsCommand(w, r, &cmd); err != nil {
		writeArtifactsError(w, err)
		return
	}
	item, err := h.service.Signoff(r.Context(), artifacts.SignoffInput{
		ItemID: r.PathValue("itemID"), ExpectedRevision: cmd.ExpectedRevision, Note: cmd.Note,
		Participant: sessionParticipant(r),
	})
	if err != nil {
		writeArtifactsError(w, err)
		return
	}
	writeArtifactsJSON(w, http.StatusOK, item)
}

func (h *artifactsHTTPHandler) archive(w http.ResponseWriter, r *http.Request) {
	var cmd artifactArchiveCommand
	if err := decodeArtifactsCommand(w, r, &cmd); err != nil {
		writeArtifactsError(w, err)
		return
	}
	item, err := h.service.Archive(r.Context(), artifacts.ArchiveInput{
		ItemID: r.PathValue("itemID"), ExpectedRevision: cmd.ExpectedRevision, Reason: cmd.Reason,
	})
	if err != nil {
		writeArtifactsError(w, err)
		return
	}
	writeArtifactsJSON(w, http.StatusOK, item)
}

func (h *artifactsHTTPHandler) delete(w http.ResponseWriter, r *http.Request) {
	var cmd artifactArchiveCommand
	if err := decodeArtifactsCommand(w, r, &cmd); err != nil {
		writeArtifactsError(w, err)
		return
	}
	item, err := h.service.Delete(r.Context(), artifacts.ArchiveInput{
		ItemID: r.PathValue("itemID"), ExpectedRevision: cmd.ExpectedRevision, Reason: cmd.Reason,
	})
	if err != nil {
		writeArtifactsError(w, err)
		return
	}
	writeArtifactsJSON(w, http.StatusOK, item)
}

// artifactHTMLPolicy is the policy an HTML artifact runs under. The document is
// loaded into an iframe whose `sandbox` attribute omits allow-same-origin, so
// it has an opaque origin and no access to Tangent's cookies, storage or API;
// this header repeats that as `sandbox` so a direct navigation is held to the
// same rules. Remote scripts, styles, fonts and images stay allowed because
// agent mocks routinely use CDN Tailwind and React; `connect-src 'none'` means
// the page cannot call out (ADR 0015 §6).
const artifactHTMLPolicy = "sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox; " +
	"default-src 'none'; " +
	"script-src 'unsafe-inline' 'unsafe-eval' https:; " +
	"style-src 'unsafe-inline' https:; " +
	"img-src data: blob: https:; " +
	"font-src data: https:; " +
	"media-src data: blob: https:; " +
	"connect-src 'none'; " +
	"form-action 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'self'"

// artifactPassivePolicy covers every non-HTML revision: nothing runs, nothing
// loads, and the file can only be framed by Tangent itself.
const artifactPassivePolicy = "sandbox; default-src 'none'; img-src data:; media-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'self'"

// artifactPDFPolicy is the passive policy without `sandbox`: the browser's built-in
// PDF viewer does not run inside a sandboxed document, and a PDF's own scripts run in
// that viewer's isolated process, not in Tangent's origin.
const artifactPDFPolicy = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'"

// content serves one revision's bytes. It overrides the document-wide security
// headers, which forbid framing, with the narrower policy the artifact needs.
func (h *artifactsHTTPHandler) content(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.Atoi(r.PathValue("revision"))
	if err != nil || revision < 1 {
		writeArtifactsError(w, fmt.Errorf("%w: revision must be a positive integer", artifacts.ErrInvalidRequest))
		return
	}
	rev, file, err := h.service.OpenRevision(r.Context(), r.PathValue("itemID"), revision)
	if err != nil {
		writeArtifactsError(w, err)
		return
	}
	defer func() { _ = file.Close() }()
	header := w.Header()
	policy := artifactPassivePolicy
	switch rev.MediaType {
	case "text/html":
		policy = artifactHTMLPolicy
	case "application/pdf":
		policy = artifactPDFPolicy
	}
	header.Set("Content-Security-Policy", policy)
	header.Set("X-Frame-Options", "SAMEORIGIN")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Cache-Control", "private, no-store")
	mediaType := rev.MediaType
	if strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" {
		mediaType += "; charset=utf-8"
	}
	header.Set("Content-Type", mediaType)
	if r.URL.Query().Get("download") != "" {
		header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": rev.Filename}))
	} else {
		header.Set("Content-Disposition", "inline")
	}
	info, statErr := file.Stat()
	modified := rev.CreatedAt
	if statErr == nil {
		modified = info.ModTime()
	}
	http.ServeContent(w, r, rev.Filename, modified, file)
}

func (h *artifactsHTTPHandler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	initial, err := h.service.Revision(r.Context())
	if err != nil {
		writeArtifactsError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if err := writeDocsRevisionEvent(w, initial); err != nil {
		return
	}
	flusher.Flush()
	last := initial
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
			if current == last {
				continue
			}
			last = current
			if err := writeDocsRevisionEvent(w, current); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func decodeArtifactsCommand(w http.ResponseWriter, r *http.Request, target any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errArtifactsContentType
	}
	r.Body = http.MaxBytesReader(w, r.Body, artifactsMaxCommandBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(target); decodeErr != nil {
		return fmt.Errorf("%w: decode command: %w", artifacts.ErrInvalidRequest, decodeErr)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: command must contain one JSON object", artifacts.ErrInvalidRequest)
	}
	return nil
}

func writeArtifactsJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeArtifactsError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	response := map[string]any{
		"contract_version": artifacts.ContractVersion,
		"code":             "artifact_error",
		"message":          err.Error(),
	}
	switch {
	case errors.Is(err, errArtifactsContentType):
		status = http.StatusUnsupportedMediaType
		response["code"] = "unsupported_media_type"
	case errors.Is(err, artifacts.ErrNotFound), errors.Is(err, interaction.ErrNotFound):
		status = http.StatusNotFound
		response["code"] = "not_found"
	case errors.Is(err, interaction.ErrUnauthorized):
		status = http.StatusForbidden
		response["code"] = "forbidden"
	case errors.Is(err, artifacts.ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
		response["code"] = "too_large"
	case errors.Is(err, artifacts.ErrInvalidRequest), errors.Is(err, interaction.ErrDefinitionValidation):
		status = http.StatusBadRequest
		response["code"] = "invalid_request"
	case errors.Is(err, artifacts.ErrTerminalConflict), errors.Is(err, artifacts.ErrStaleRevision),
		errors.Is(err, interaction.ErrRevisionConflict), errors.Is(err, interaction.ErrTerminal),
		errors.Is(err, interaction.ErrNotRespondable), errors.Is(err, interaction.ErrIdempotencyConflict):
		status = http.StatusConflict
		response["code"] = "conflict"
	}
	writeArtifactsJSON(w, status, response)
}
