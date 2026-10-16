package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/participant"
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
	logger        *slog.Logger
	eventPoll     time.Duration
	eventLifetime time.Duration
	eventStream   *HITLEventStream
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
		service: service, logger: slog.Default(), eventPoll: hitlEventPoll, eventLifetime: hitlEventLifetime,
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
		ConnectionID:                canonicalConnectionID(r, command.ConnectionID),
		Participant:                 sessionParticipant(r),
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
		Participant:                 sessionParticipant(r),
	})
	if err != nil {
		writeHITLError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, outcome)
}

// sessionParticipant returns the participant binding of the session
// requireParticipant resolved for this request, so a presentation and a
// resolution record who actually acted — including `operator-token`
// assurance for an elevated session (CW-20261016-0174). With no session (an
// embedder that wired no gate) the zero binding selects the HITL service's
// historical OperatorParticipant.
func sessionParticipant(r *http.Request) interaction.ActorBinding {
	session, ok := participant.FromContext(r.Context())
	if !ok {
		return interaction.ActorBinding{}
	}
	return interaction.ActorBinding{
		Scope: session.Scope, PrincipalRef: session.Ref,
		Authority: session.Authority, Assurance: session.Assurance,
	}
}

// canonicalConnectionID returns the server-assigned connection id for a
// browser request.
//
// The client-supplied value has always been a diagnostic label — its own
// comment in ui/src/lib/hitl-api.ts says so — and ADR 0004 §6.6 makes the
// canonical id server-assigned from the participant session instead. It is
// derived rather than copied: the session identifier is not something a
// durable record needs, and a truncated digest is a stable per-session label
// that reveals nothing about the session it names.
//
// With no session (an embedder that wired no gate) the client's label is used
// unchanged, which is the pre-ADR-0004 behavior.
func canonicalConnectionID(r *http.Request, clientLabel string) string {
	session, ok := participant.FromContext(r.Context())
	if !ok {
		return clientLabel
	}
	digest := sha256.Sum256([]byte(session.ID))
	return "browser-" + hex.EncodeToString(digest[:6])
}

// events preserves revision hints for the browser and optionally adds typed
// replayable events. Neither stream replaces the durable Inbox() projection.
// The bounded lifetime also covers replay; EventSource reconnects automatically.
func (h *hitlHTTPHandler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.eventLifetime)
	defer cancel()
	stream, err := h.eventStream.open(ctx, r)
	if err != nil {
		if errors.Is(err, errHITLEventCursor) {
			http.Error(w, errHITLEventCursor.Error(), http.StatusBadRequest)
			return
		}
		// A failed typed log must not disable legacy revision hints. Retry
		// opening it when this bounded stream ends and EventSource reconnects.
		h.logger.WarnContext(ctx, "typed HITL event log unavailable; serving revision hints", "error", err)
	}
	defer stream.close()
	var notify <-chan struct{}
	if stream != nil {
		notify = stream.notify
	}
	inbox, err := h.service.Inbox(ctx)
	if err != nil {
		writeHITLError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if err := stream.writeCheckpoint(w); err != nil {
		return
	}
	if err := writeHITLRevisionEvent(w, inbox.Revision); err != nil {
		return
	}
	flusher.Flush()
	if err := stream.replay(ctx, w); err != nil {
		return
	}
	flusher.Flush()

	lastRevision := inbox.Revision
	poll := time.NewTicker(h.eventPoll)
	keepAlive := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer keepAlive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, open := <-notify:
			if !open {
				notify = nil
				continue
			}
			if err := stream.replay(ctx, w); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-poll.C:
			current, loadErr := h.service.Inbox(ctx)
			if loadErr != nil {
				// Closing the stream makes EventSource surface onerror and retry;
				// the browser never mistakes stale data for a healthy connection.
				return
			}
			if current.Revision != lastRevision {
				lastRevision = current.Revision
				if err := writeHITLRevisionEvent(w, current.Revision); err != nil {
					return
				}
			}
			// Polling also catches coalesced/lost wakeups and supports a nil notifier.
			if err := stream.replay(ctx, w); err != nil {
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
	var terminal *hitl.TerminalConflictError
	switch {
	case errors.As(err, &terminal):
		// Another terminal outcome won before this request looked: the
		// contract's terminal_conflict, carrying the winner, exactly as the
		// MCP adapter reports it. It previously fell through to the generic
		// "conflict", which is what the browser/withdraw race test caught.
		status = http.StatusConflict
		response["code"] = "terminal_conflict"
		response["terminal_outcome"] = terminal.Outcome
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
