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

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/server"
	tangentws "github.com/hollis-labs/tangent/internal/ws"
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

	// envDBPath overrides the default ~/.tangent/tangent.db location.
	envDBPath = "TANGENT_DB_PATH"
)

func main() {
	flagSet := flag.NewFlagSet("tangent", flag.ExitOnError)
	port := flagSet.Int("port", resolvePort(), "HTTP listen port (overrides "+envPort+")")
	migrateOnly := flagSet.Bool("migrate-only", false, "apply DB migrations and exit")
	rollbackOne := flagSet.Bool("rollback-one", false, "roll back the most recent DB migration and exit")
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		// flag.ExitOnError already handled this; keep the linter happy.
		os.Exit(2)
	}
	if *migrateOnly && *rollbackOne {
		fmt.Fprintln(os.Stderr, "tangent: --migrate-only and --rollback-one are mutually exclusive")
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	sqlDB, err := tangentdb.Open(resolveDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "tangent: open db: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if closeErr := tangentdb.Close(sqlDB); closeErr != nil {
			fmt.Fprintf(os.Stderr, "tangent: close db: %v\n", closeErr)
		}
	}()
	if *rollbackOne {
		if rollbackErr := tangentdb.RollbackOne(sqlDB); rollbackErr != nil {
			fmt.Fprintf(os.Stderr, "tangent: rollback db: %v\n", rollbackErr)
			os.Exit(1)
		}
		logger.Info("rolled back latest tangent migration")
		return
	}
	if migrateErr := tangentdb.RunMigrations(sqlDB); migrateErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: migrate db: %v\n", migrateErr)
		os.Exit(1)
	}
	if *migrateOnly {
		logger.Info("applied tangent migrations")
		return
	}

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

	// Plugin-extension registration. Triage isn't in go-envelopes core
	// in v0.1.0 (planned for v0.3); we register the in-tree manifest
	// fragment here via the plugin extension API so envelope validation
	// in Dispatcher.Dispatch succeeds for triage envelopes. When core
	// learns about triage upstream, this call goes away — handler
	// registration below is unaffected.
	if regErr := extensions.RegisterTriage(envSvc); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register triage extension: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := extensions.RegisterFeedback(envSvc); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register feedback extension: %v\n", regErr)
		os.Exit(1)
	}
	logger.Info("registered tangent envelope extensions", "plugin", extensions.PluginID, "count", envSvc.Len())

	// Dispatcher is shared across transports. PR 4 registers the
	// triage handler that bridges to a WebSocket-connected room.
	dispatcher := envelope.NewDispatcher(envSvc)

	// Room manager + WS handler — the bridge between MCP envelopes and
	// browser tabs. Created before the MCP server so the triage
	// handler has somewhere to push.
	roomMgr := room.NewManager(sqlDB)
	if hydrateErr := roomMgr.Hydrate(context.Background()); hydrateErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: hydrate rooms: %v\n", hydrateErr)
		os.Exit(1)
	}
	wsHandler := tangentws.New(roomMgr, logger)

	// Use 127.0.0.1 to match the listener's actual bind so the URL
	// hint we log when triage creates a room resolves correctly even
	// on IPv6-preferring systems where "localhost" lands on ::1.
	roomURLBase := fmt.Sprintf("http://127.0.0.1:%d", *port)
	mcpSrv, err := mcp.New(envSvc, dispatcher, roomMgr, roomURLBase)
	if err != nil {
		// MCP construction failure is fatal: the binary advertises an MCP
		// surface as part of its v0.1 contract, so booting without it
		// would silently strip a documented capability.
		fmt.Fprintf(os.Stderr, "tangent: build mcp server: %v\n", err)
		os.Exit(1)
	}
	triageHandler := mcp.NewTriageHandler(roomMgr, logger, roomURLBase)
	if regErr := mcp.RegisterTriageOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register triage handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterFeedbackOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register feedback handler: %v\n", regErr)
		os.Exit(1)
	}

	srv, err := server.New(server.Config{
		Port:           *port,
		DevFrontendURL: os.Getenv(envDevFrontendURL),
		Logger:         logger,
		Envelope:       envSvc,
		MCP:            mcpSrv,
		WSHandler:      wsHandler,
		RoomManager:    roomMgr,
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
		"url", fmt.Sprintf("http://127.0.0.1:%d/", *port),
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

func resolveDBPath() string {
	return os.Getenv(envDBPath)
}
