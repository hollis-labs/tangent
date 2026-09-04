package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
)

const (
	hitlMaxCommandBytes = 32 * 1024
	hitlEventPoll       = 750 * time.Millisecond
	hitlEventLifetime   = 50 * time.Second
)

var errHITLContentType = errors.New("hitl browser API requires application/json")

// HITLService is the browser adapter's narrow application boundary. It keeps
// the HTTP layer out of durable interaction storage and makes the dedicated
// operator channel distinct from the caller-facing MCP tools.
type HITLService interface {
	Inbox(context.Context) (hitl.OperatorInbox, error)
	InspectOperatorItem(context.Context, string) (hitl.OperatorItemView, error)
	Present(context.Context, hitl.PresentInput) (hitl.ItemHandle, error)
	Resolve(context.Context, hitl.ResolveInput) (hitl.TerminalOutcome, error)
}

type hitlHTTPHandler struct {
	service       HITLService
	eventPoll     time.Duration
	eventLifetime time.Duration
}

type presentCommand struct {
	ExpectedRevision            int64  `json:"expected_revision"`
	PresentedProjectionRevision int64  `json:"presented_projection_revision"`
	ConnectionID                string `json:"connection_id"`
}

type resolveCommand struct {
	ExpectedRevision            int64           `json:"expected_revision"`
	PresentedProjectionRevision int64           `json:"presented_projection_revision"`
	Response                    json.RawMessage `json:"response"`
}

func newHITLHTTPHandler(service HITLService) *hitlHTTPHandler {
	return &hitlHTTPHandler{
		service: service, eventPoll: hitlEventPoll, eventLifetime: hitlEventLifetime,
	}
}

func (h *hitlHTTPHandler) inbox(w http.ResponseWriter, r *http.Request) {
	inbox, err := h.service.Inbox(r.Context())
	if err != nil {
		writeHITLError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, inbox)
}

func (h *hitlHTTPHandler) item(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.InspectOperatorItem(r.Context(), r.PathValue("itemID"))
	if err != nil {
		writeHITLError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, item)
}

func (h *hitlHTTPHandler) present(w http.ResponseWriter, r *http.Request) {
	var command presentCommand
	if err := decodeHITLCommand(w, r, &command); err != nil {
		writeHITLError(w, err)
		return
	}
	_, err := h.service.Present(r.Context(), hitl.PresentInput{
		ItemID: r.PathValue("itemID"), ExpectedRevision: command.ExpectedRevision,
		PresentedProjectionRevision: command.PresentedProjectionRevision,
		ConnectionID:                command.ConnectionID,
	})
	if err != nil {
		writeHITLError(w, err)
		return
	}
	item, err := h.service.InspectOperatorItem(r.Context(), r.PathValue("itemID"))
	if err != nil {
		writeHITLError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, item)
}

func (h *hitlHTTPHandler) resolve(w http.ResponseWriter, r *http.Request) {
	var command resolveCommand
	if err := decodeHITLCommand(w, r, &command); err != nil {
		writeHITLError(w, err)
		return
	}
	outcome, err := h.service.Resolve(r.Context(), hitl.ResolveInput{
		ItemID: r.PathValue("itemID"), ExpectedRevision: command.ExpectedRevision,
		PresentedProjectionRevision: command.PresentedProjectionRevision,
		Response:                    command.Response,
	})
	if err != nil {
		writeHITLError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, outcome)
}

// events sends only durable revision notifications. The browser always
// follows a notification with Inbox(), so a dropped/reordered event cannot
// become an alternate source of state. The bounded stream lifetime stays
// below the server write timeout; EventSource reconnects automatically.
func (h *hitlHTTPHandler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	inbox, err := h.service.Inbox(r.Context())
	if err != nil {
		writeHITLError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if err := writeHITLRevisionEvent(w, inbox.Revision); err != nil {
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
				// Closing the stream makes EventSource surface onerror and retry;
				// the browser never mistakes stale data for a healthy connection.
				return
			}
			if current.Revision == lastRevision {
				continue
			}
			lastRevision = current.Revision
			if err := writeHITLRevisionEvent(w, current.Revision); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeHITLRevisionEvent(w io.Writer, revision string) error {
	payload, err := json.Marshal(map[string]string{"revision": revision})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: revision\ndata: %s\n\n", payload)
	return err
}

func decodeHITLCommand(w http.ResponseWriter, r *http.Request, target any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errHITLContentType
	}
	r.Body = http.MaxBytesReader(w, r.Body, hitlMaxCommandBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(target); decodeErr != nil {
		return fmt.Errorf("%w: decode command: %w", hitl.ErrInvalidRequest, decodeErr)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: command must contain one JSON object", hitl.ErrInvalidRequest)
	}
	return nil
}

// hitlSameOrigin protects the privileged loopback operator API from browser
// cross-site requests. Non-browser local clients omit these headers and remain
// usable for smoke testing; browser requests that supply either signal must be
// explicitly same-origin.
func hitlSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requestHostIsLoopback(r.Host) {
			writeHITLJSON(w, http.StatusForbidden, map[string]any{
				"contract_version": hitl.ContractVersion,
				"code":             "forbidden_origin",
				"message":          "HITL operator API is available on loopback hosts only",
			})
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
			writeHITLJSON(w, http.StatusForbidden, map[string]any{
				"contract_version": hitl.ContractVersion,
				"code":             "forbidden_origin",
				"message":          "HITL operator API accepts same-origin browser requests only",
			})
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !requestOriginMatches(r, origin) {
			writeHITLJSON(w, http.StatusForbidden, map[string]any{
				"contract_version": hitl.ContractVersion,
				"code":             "forbidden_origin",
				"message":          "HITL operator API accepts same-origin browser requests only",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestHostIsLoopback(requestHost string) bool {
	host := requestHost
	if parsedHost, _, err := net.SplitHostPort(requestHost); err == nil {
		host = parsedHost
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func requestOriginMatches(r *http.Request, origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	expectedScheme := "http"
	if r.TLS != nil {
		expectedScheme = "https"
	}
	return strings.EqualFold(parsed.Scheme, expectedScheme) && strings.EqualFold(parsed.Host, r.Host)
}

func writeHITLJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeHITLError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "hitl_error"
	response := map[string]any{
		"contract_version": hitl.ContractVersion,
		"code":             code,
		"message":          err.Error(),
	}
	var stale *hitl.StaleRevisionError
	switch {
	case errors.Is(err, errHITLContentType):
		status = http.StatusUnsupportedMediaType
		response["code"] = "unsupported_media_type"
	case errors.As(err, &stale):
		status = http.StatusConflict
		response["code"] = "stale_revision"
		response["operation"] = stale.Operation
		response["item_id"] = stale.ItemID
		response["revision_kind"] = stale.RevisionKind
		response["expected_revision"] = stale.ExpectedRevision
		response["actual_revision"] = stale.ActualRevision
		response["current_state"] = stale.CurrentState
		if stale.TerminalOutcome != nil {
			response["terminal_outcome"] = stale.TerminalOutcome
		}
	case errors.Is(err, interaction.ErrNotFound):
		status = http.StatusNotFound
		response["code"] = "not_found"
	case errors.Is(err, interaction.ErrUnauthorized):
		status = http.StatusForbidden
		response["code"] = "forbidden"
	case errors.Is(err, hitl.ErrInvalidRequest), errors.Is(err, interaction.ErrDefinitionValidation):
		status = http.StatusBadRequest
		response["code"] = "invalid_request"
	case errors.Is(err, hitl.ErrTerminalConflict), errors.Is(err, interaction.ErrRevisionConflict),
		errors.Is(err, interaction.ErrTerminal), errors.Is(err, interaction.ErrNotRespondable):
		status = http.StatusConflict
		response["code"] = "conflict"
	}
	writeHITLJSON(w, status, response)
}
