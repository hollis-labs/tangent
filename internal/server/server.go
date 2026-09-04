package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/room"
)

// Config controls Server construction.
type Config struct {
	// Port is the TCP port to listen on (e.g. 7842).
	Port int

	// DevFrontendURL, when non-empty, makes the server reverse-proxy all
	// non-API requests to the given URL (typically a Vite dev server at
	// http://localhost:5173). When empty, the embedded SPA is served.
	DevFrontendURL string

	// Logger receives structured logs. Defaults to slog.Default() when nil.
	Logger *slog.Logger

	// Envelope is the loaded go-envelopes service. PR 2 wires it through
	// without consuming it; PR 3 (MCP) and PR 4 (WebSocket) attach
	// validators and dispatchers to it. New returns an error if Envelope
	// is nil so misconfigured boots fail fast rather than at first
	// envelope.
	Envelope *envelope.Service

	// MCP is the constructed MCP server (PR 3). When non-nil the HTTP
	// server mounts /mcp (streamable HTTP) and /sse (legacy fallback).
	// It's optional in Config so test code can spin up a server without
	// the full MCP wiring; production main always passes a non-nil value.
	MCP MCPServer

	// WSHandler is the constructed WebSocket bridge handler (PR 4).
	// When non-nil the HTTP server mounts /ws. Optional in Config so
	// test code can spin up a server without the full WS wiring;
	// production main always passes a non-nil value.
	WSHandler http.Handler

	// RoomManager is the in-memory multi-room session model (PR 4).
	// When non-nil the HTTP server uses it to validate /r/<roomID>
	// links — unknown ids return 404 immediately, before the SPA
	// loads (which would otherwise show a confusing blank room).
	// Optional in Config; when nil all /r/ requests fall through to
	// the SPA which renders its own "room not found" view client-side.
	RoomManager *room.Manager

	// HITL is the durable operator-owned inbox application service. When set,
	// the server mounts its dedicated browser API and revision event stream;
	// it remains separate from caller-facing MCP and room WebSockets.
	HITL HITLService
}

// MCPServer is the minimal contract internal/mcp satisfies. Declared as
// an interface here (rather than importing the package directly) so
// tests can inject a stub and to keep the import graph one-way: mcp
// depends on envelope; server depends on mcp via this interface only.
type MCPServer interface {
	HTTPHandler() http.Handler
	SSEHandler() http.Handler
}

// Server is the Tangent HTTP server. It wraps a *http.Server and the
// route registration; in v0.1 the only handler is the SPA (or its dev
// reverse-proxy).
type Server struct {
	cfg    Config
	logger *slog.Logger
	mux    *http.ServeMux
	httpS  *http.Server

	// envelope holds the registry-backed validation/dispatch service
	// shared with future MCP and WebSocket subsystems.
	envelope *envelope.Service
}

const httpServerWriteTimeout = 60 * time.Second

// New constructs a Server with the embedded-SPA or dev-proxy handler.
//
// cfg.Envelope must be non-nil. The envelope service is constructed at
// startup in cmd/tangent/main.go; passing it through Config keeps the
// server agnostic to manifest-load lifecycle.
func New(cfg Config) (*Server, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Envelope == nil {
		return nil, fmt.Errorf("server: envelope service is required")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)

	if cfg.MCP != nil {
		// Streamable-HTTP transport (modern MCP clients). The SDK's
		// handler accepts POST + GET on the same URL — clients use POST
		// to send JSON-RPC requests and may GET to subscribe to a
		// session-scoped event stream. We mount it at the path level
		// rather than per-method so both routes resolve to the same
		// handler.
		mux.Handle("/mcp", longLivedMCPHandler(cfg.MCP.HTTPHandler()))
		// SSE fallback for legacy Claude Code / older MCP clients. GET
		// opens the long-lived event stream; POST is used for outbound
		// JSON-RPC frames keyed against the streamed session id.
		mux.Handle("/sse", longLivedMCPHandler(cfg.MCP.SSEHandler()))
		// Logged URLs use 127.0.0.1 (matching the actual bind) rather
		// than the human-friendly "localhost" alias — on systems where
		// localhost resolves to ::1 first without an IPv4 fallback, a
		// pasted link would dead-end against an IPv4-only listener.
		logger.Info("MCP server ready",
			"http_url", fmt.Sprintf("http://127.0.0.1:%d/mcp", cfg.Port),
			"sse_url", fmt.Sprintf("http://127.0.0.1:%d/sse", cfg.Port),
		)
	}

	if cfg.WSHandler != nil {
		// /ws upgrades to WebSocket. Mounted before the catch-all so
		// the SPA (or dev proxy) never sees the upgrade request.
		mux.Handle("GET /ws", cfg.WSHandler)
		logger.Info("WebSocket bridge ready",
			"ws_url", fmt.Sprintf("ws://127.0.0.1:%d/ws", cfg.Port),
		)
	}

	if cfg.HITL != nil {
		hitlHandler := newHITLHTTPHandler(cfg.HITL)
		mux.Handle("GET /api/hitl", hitlSameOrigin(http.HandlerFunc(hitlHandler.inbox)))
		mux.Handle("GET /api/hitl/events", hitlSameOrigin(http.HandlerFunc(hitlHandler.events)))
		mux.Handle("GET /api/hitl/items/{itemID}", hitlSameOrigin(http.HandlerFunc(hitlHandler.item)))
		mux.Handle("GET /api/hitl/items/{itemID}/evidence/{evidenceIndex}/reference", hitlSameOrigin(http.HandlerFunc(hitlHandler.tangentEvidence)))
		mux.Handle("GET /api/hitl/items/{itemID}/evidence/{evidenceIndex}/preview", hitlSameOrigin(http.HandlerFunc(hitlHandler.artifactPreview)))
		mux.Handle("POST /api/hitl/items/{itemID}/present", hitlSameOrigin(http.HandlerFunc(hitlHandler.present)))
		mux.Handle("POST /api/hitl/items/{itemID}/resolve", hitlSameOrigin(http.HandlerFunc(hitlHandler.resolve)))
	}

	rootHandler, err := buildRootHandler(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("build root handler: %w", err)
	}

	// /r/{roomID} → SPA. The SPA's React Router picks up the roomID
	// from the path. When a RoomManager is configured we pre-validate
	// the id so unknown rooms get a 404 before the SPA bundle loads;
	// keeps the dev experience honest about ephemeral state.
	if cfg.RoomManager != nil {
		mux.Handle("GET /r/{roomID}", roomFallbackHandler(cfg.RoomManager, rootHandler))
	}
	mux.Handle("/", rootHandler)

	// v0.x is localhost-only by design (concept doc: single-user, no
	// auth, no cloud). Bind explicitly to 127.0.0.1 so a developer
	// running `./tangent` on a coffee-shop wifi isn't inadvertently
	// exposing MCP + WS to the LAN. A future release may add an
	// opt-in BindAll flag; until then the constraint is enforced
	// here rather than relying on docs/firewall hygiene.
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	httpS := &http.Server{
		Addr:              addr,
		Handler:           loggingMiddleware(logger, mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      httpServerWriteTimeout,
		IdleTimeout:       120 * time.Second,
	}

	return &Server{
		cfg:      cfg,
		logger:   logger,
		mux:      mux,
		httpS:    httpS,
		envelope: cfg.Envelope,
	}, nil
}

// longLivedMCPHandler removes the server-wide response write deadline for MCP
// transports. Room-backed tools deliberately wait for a human response, and
// SSE subscriptions are likewise long-lived; both may validly outlive the
// ordinary HTTP timeout. Request-context cancellation remains authoritative.
func longLivedMCPHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		next.ServeHTTP(w, r)
	})
}

// Envelope returns the wired envelope service. Exposed so future PRs
// (MCP, WebSocket) can grab it after construction without revisiting
// New's signature.
func (s *Server) Envelope() *envelope.Service { return s.envelope }

// Listen binds the server's TCP socket and returns the listener.
// Callers can fail-fast on bind errors (e.g. EADDRINUSE) before logging
// readiness. Pass the returned listener to Serve.
func (s *Server) Listen() (net.Listener, error) {
	return net.Listen("tcp", s.httpS.Addr)
}

// Serve runs the HTTP server on the given listener. Blocks until the
// listener errors out; callers should treat http.ErrServerClosed as a
// clean shutdown.
func (s *Server) Serve(ln net.Listener) error {
	s.logger.Info("tangent listening",
		"addr", s.httpS.Addr,
		"dev_frontend_url", s.cfg.DevFrontendURL,
	)
	return s.httpS.Serve(ln)
}

// ListenAndServe is a convenience for tests / non-production callers
// that don't need to log readiness only after a successful bind.
// Production main() uses Listen + Serve so bind failures surface before
// the "tangent ready" log line.
func (s *Server) ListenAndServe() error {
	ln, err := s.Listen()
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// Shutdown gracefully drains in-flight requests with the given timeout.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpS.Shutdown(ctx)
}

// buildRootHandler returns the root handler — either the embedded SPA
// or a reverse proxy to a Vite dev server.
func buildRootHandler(cfg Config, logger *slog.Logger) (http.Handler, error) {
	if cfg.DevFrontendURL == "" {
		return spaHandler(), nil
	}

	target, err := url.Parse(cfg.DevFrontendURL)
	if err != nil {
		return nil, fmt.Errorf("parse dev frontend url: %w", err)
	}
	// url.Parse is lenient: "localhost:5173" parses with scheme="" and
	// host="" (it lands in Path), which would silently produce a broken
	// reverse proxy. Reject anything that isn't an absolute http(s) URL
	// so misconfigured TANGENT_DEV_FRONTEND_URL fails fast.
	if scheme := strings.ToLower(target.Scheme); scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("dev frontend url %q must use http or https scheme", cfg.DevFrontendURL)
	}
	if target.Host == "" {
		return nil, fmt.Errorf("dev frontend url %q must include a host (e.g. http://localhost:5173)", cfg.DevFrontendURL)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		// Connection refused / timeout → frontend dev server isn't up.
		// Surface a clear message rather than the default 502.
		logger.Warn("dev frontend proxy error",
			"url", cfg.DevFrontendURL,
			"path", r.URL.Path,
			"err", err,
		)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "tangent dev: frontend unreachable at "+cfg.DevFrontendURL+"\n")
	}
	return proxy, nil
}

// handleHealth returns a tiny liveness probe. Useful for `curl` smoke
// checks and for later container/orchestrator probes.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

// roomFallbackHandler validates the {roomID} path variable against the
// live room manager and either delegates to the SPA handler (allowing
// the React Router to pick up the route) or returns a 404. We don't
// rewrite the request path — the SPA receives /r/<id> and chooses what
// to render.
func roomFallbackHandler(mgr *room.Manager, fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("roomID")
		if id == "" {
			http.Error(w, "missing roomID", http.StatusBadRequest)
			return
		}
		if _, ok := mgr.Get(id); !ok {
			http.Error(w, "room not found", http.StatusNotFound)
			return
		}
		fallback.ServeHTTP(w, r)
	})
}

// loggingMiddleware emits a single structured log entry per request.
func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start).String(),
		)
	})
}

// IsClosedErr reports whether err is the sentinel returned by a clean
// http.Server.Shutdown call. Convenience wrapper so cmd/tangent doesn't
// need to import net/http just for the comparison.
func IsClosedErr(err error) bool {
	return errors.Is(err, http.ErrServerClosed)
}
