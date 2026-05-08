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
}

// Server is the Tangent HTTP server. It wraps a *http.Server and the
// route registration; in v0.1 the only handler is the SPA (or its dev
// reverse-proxy).
type Server struct {
	cfg    Config
	logger *slog.Logger
	mux    *http.ServeMux
	httpS  *http.Server
}

// New constructs a Server with the embedded-SPA or dev-proxy handler.
func New(cfg Config) (*Server, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)

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
		cfg:    cfg,
		logger: logger,
		mux:    mux,
		httpS:  httpS,
	}, nil
}

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
