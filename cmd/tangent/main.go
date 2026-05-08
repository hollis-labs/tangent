// Command tangent serves the embedded Tangent SPA over HTTP.
//
// In v0.1 this is the entire binary surface: HTTP listener, SPA bundle,
// graceful shutdown. Subsequent PRs add the WebSocket envelope channel
// and MCP server tooling. The binary is intentionally small so that a
// future Wails wrapper can reuse the same internal/server package.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/server"
)

const (
	// defaultPort is the TCP port the Tangent HTTP server binds to when
	// neither --port nor TANGENT_HTTP_PORT is set. 7842 is unassigned
	// in IANA's well-known list and unlikely to clash with common dev
	// services (Vite 5173, Nanite 8090, Postgres 5432, etc.).
	defaultPort = 7842

	// envPort overrides the listen port when set to a positive integer.
	envPort = "TANGENT_HTTP_PORT"

	// envDevFrontendURL, when set, makes the Go server reverse-proxy
	// non-API requests to that URL (typically a Vite dev server). When
	// unset, the embedded SPA is served. `make dev` sets this; prod
	// builds leave it empty.
	envDevFrontendURL = "TANGENT_DEV_FRONTEND_URL"
)

func main() {
	flagSet := flag.NewFlagSet("tangent", flag.ExitOnError)
	port := flagSet.Int("port", resolvePort(), "HTTP listen port (overrides "+envPort+")")
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		// flag.ExitOnError already handled this; keep the linter happy.
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// Boot context governs envelope-registry load. Cancellation tears
	// down the schema-compile loop cleanly; we rebind it once the
	// service is up.
	bootCtx, cancelBoot := context.WithCancel(context.Background())
	envSvc, err := envelope.New(bootCtx)
	cancelBoot()
	if err != nil {
		// Fail fast: a half-loaded registry would let unknown envelope
		// types through silently. Without an envelope substrate the
		// rest of Tangent is meaningless.
		fmt.Fprintf(os.Stderr, "tangent: load envelope registry: %v\n", err)
		os.Exit(1)
	}
	logger.Info("loaded envelope types", "count", envSvc.Len())

	srv, err := server.New(server.Config{
		Port:           *port,
		DevFrontendURL: os.Getenv(envDevFrontendURL),
		Logger:         logger,
		Envelope:       envSvc,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "tangent: %v\n", err)
		os.Exit(1)
	}

	// Bind synchronously so port-in-use surfaces before the readiness
	// log line. Only after the listener is open do we declare ready.
	ln, err := srv.Listen()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tangent: listen: %v\n", err)
		os.Exit(1)
	}

	listenErr := make(chan error, 1)
	go func() {
		listenErr <- srv.Serve(ln)
	}()

	logger.Info("tangent ready",
		"url", fmt.Sprintf("http://localhost:%d/", *port),
	)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("shutdown signal received", "signal", sig.String())
	case err := <-listenErr:
		// Listener returned without a shutdown signal — usually
		// "address already in use" or a genuine bind failure.
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "tangent: listen: %v\n", err)
			os.Exit(1)
		}
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(os.Stderr, "tangent: shutdown: %v\n", err)
		os.Exit(1)
	}
	// Drain the listener result so the goroutine exits cleanly.
	if err := <-listenErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "tangent: post-shutdown: %v\n", err)
		os.Exit(1)
	}
	logger.Info("tangent stopped")
}

// resolvePort returns the listen port, honoring TANGENT_HTTP_PORT when
// it parses as a positive integer, otherwise falling back to defaultPort.
func resolvePort() int {
	raw := os.Getenv(envPort)
	if raw == "" {
		return defaultPort
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultPort
	}
	return n
}
