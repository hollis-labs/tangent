package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/hollis-labs/tangent/internal/envelope"
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
		mux.Handle("/mcp", cfg.MCP.HTTPHandler())
		// SSE fallback for legacy Claude Code / older MCP clients. GET
		// opens the long-lived event stream; POST is used for outbound
		// JSON-RPC frames keyed against the streamed session id.
		mux.Handle("/sse", cfg.MCP.SSEHandler())
		logger.Info("MCP server ready",
			"http_url", fmt.Sprintf("http://localhost:%d/mcp", cfg.Port),
			"sse_url", fmt.Sprintf("http://localhost:%d/sse", cfg.Port),
		)
	}

	rootHandler, err := buildRootHandler(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("build root handler: %w", err)
	}
	mux.Handle("/", rootHandler)

	addr := fmt.Sprintf(":%d", cfg.Port)
	httpS := &http.Server{
		Addr:              addr,
		Handler:           loggingMiddleware(logger, mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
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

// Envelope returns the wired envelope service. Exposed so future PRs
// (MCP, WebSocket) can grab it after construction without revisiting
// New's signature.
func (s *Server) Envelope() *envelope.Service { return s.envelope }

// ListenAndServe starts the server. It blocks until the listener errors
// out; callers should treat http.ErrServerClosed as a clean shutdown.
func (s *Server) ListenAndServe() error {
	s.logger.Info("tangent listening",
		"addr", s.httpS.Addr,
		"dev_frontend_url", s.cfg.DevFrontendURL,
	)
	return s.httpS.ListenAndServe()
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
