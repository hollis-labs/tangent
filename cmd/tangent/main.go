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

	"github.com/hollis-labs/tangent/internal/boot"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/participant"
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

	// envManagedResource names the managed-runtime resource that owns this
	// process's lifecycle, when one does (Cerberus resource `tangent-dev` in
	// the reference installation). Health reports use it to recommend a
	// concrete restart command instead of describing a wish. Unset is normal
	// and supported: an unmanaged `./tangent` has no resource to name, and the
	// reports phrase themselves generically rather than sending an operator to
	// a resource that is not there.
	envManagedResource = "TANGENT_MANAGED_RESOURCE"

	// envOpenTelemetry opts this process into the OpenTelemetry bridge.
	//
	// It is off by default and has to be turned on deliberately. Tangent
	// depends on the OTel *API* and ships no SDK and no exporter, so the
	// default providers are no-ops and nothing leaves the machine; the flag
	// exists because those providers are process-global and a future
	// dependency that installs an SDK for its own reasons must not thereby
	// start exporting a single user's interaction telemetry. Setting it on a
	// build with no SDK installed changes nothing observable, which is the
	// honest state of the seam. See internal/telemetry/otel.go.
	envOpenTelemetry = "TANGENT_OTEL"
)

func main() {
	flagSet := flag.NewFlagSet("tangent", flag.ExitOnError)
	// --version prints the release this binary is and exits. It is the same
	// string the MCP server advertises as serverInfo.version and definitions
	// declare compatibility against (internal/envelope.HostVersion), which the
	// version gate keeps equal to ui/package.json. The install script compares
	// it before and after an upgrade.
	showVersion := flagSet.Bool("version", false, "print the release version and exit")
	port := flagSet.Int("port", resolvePort(), "HTTP listen port (overrides "+envPort+")")
	migrateOnly := flagSet.Bool("migrate-only", false, "apply DB migrations and exit")
	rollbackOne := flagSet.Bool("rollback-one", false, "roll back the most recent DB migration and exit")
	// Participant sessions have no idle expiry — a local single-user tool must
	// not log its user out mid-decision — so revocation is an explicit
	// operator act rather than a timer or a UI control. Every browser simply
	// mints a fresh session on its next page load.
	revokeSessions := flagSet.Bool("revoke-participant-sessions", false,
		"revoke every browser participant session and exit")
	// The operator surface for backup, restore, repair, compaction, and the six
	// retention operations (CW-20260825-0072). Each is a one-shot that exits
	// before the server would start; see maintenance.go.
	maintenance := registerMaintenanceFlags(flagSet)
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		// flag.ExitOnError already handled this; keep the linter happy.
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println("tangent " + envelope.HostVersion)
		return
	}
	maintenanceModes := maintenance.requested()
	if maintenanceModes > 1 {
		fmt.Fprintln(os.Stderr, "tangent: maintenance commands are one at a time")
		os.Exit(2)
	}
	oneShots := exclusiveModes(*migrateOnly, *rollbackOne, *revokeSessions) + maintenanceModes
	if oneShots > 1 {
		fmt.Fprintln(os.Stderr,
			"tangent: --migrate-only, --rollback-one, --revoke-participant-sessions, and the "+
				"maintenance commands are mutually exclusive")
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// The single-writer contract (ADR 0002 §6, CW-20260825-0072). Every mode
	// that writes takes an exclusive advisory lock on `<database>.owner` before
	// the database is opened, and refuses rather than queues when another
	// process holds it. The maintenance path suspends immutability triggers for
	// the length of one transaction, and a second writer during that window
	// would write into a database without its guards.
	//
	// Read-only maintenance commands take no lock on purpose: an operator
	// diagnosing a live installation must not have to stop it to look at it.
	dbPath := resolveDBPath()
	if maintenanceModes > 0 {
		ownership, refusal := refuseIfServing(dbPath, *port, maintenance.needsExclusiveOwnership())
		if refusal != nil {
			fmt.Fprintf(os.Stderr, "tangent: %v\n", refusal)
			os.Exit(1)
		}
		// Migrations deliberately do NOT run first. A restore, a check, and a
		// repair are all things an operator reaches for when the schema is the
		// problem, and silently migrating the database they are trying to
		// inspect is how a diagnosis becomes a second incident.
		sqlDB, openErr := tangentdb.Open(dbPath)
		if openErr != nil {
			fmt.Fprintf(os.Stderr, "tangent: open db: %v\n", openErr)
			os.Exit(1)
		}
		closed := false
		closeOnce := func() error {
			if closed {
				return nil
			}
			closed = true
			return tangentdb.Close(sqlDB)
		}
		code := runMaintenance(context.Background(), maintenance, sqlDB, dbPath, closeOnce)
		if closeErr := closeOnce(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "tangent: close db: %v\n", closeErr)
		}
		if ownership != nil {
			if releaseErr := ownership.Release(); releaseErr != nil {
				fmt.Fprintf(os.Stderr, "tangent: release database ownership: %v\n", releaseErr)
			}
		}
		os.Exit(code)
	}

	// --migrate-only, --rollback-one, and --revoke-participant-sessions are
	// CLI-transport one-shots. They claim ownership and open the database
	// themselves rather than going through boot.Boot, because none wants the
	// full service graph and --rollback-one deliberately does not want
	// migrations run first either.
	if exclusiveModes(*migrateOnly, *rollbackOne, *revokeSessions) > 0 {
		ownership, claimErr := tangentdb.AcquireOwnership(dbPath, tangentdb.RoleMaintenance, commandLabel())
		if claimErr != nil {
			fmt.Fprintf(os.Stderr, "tangent: %v\n", claimErr)
			os.Exit(1)
		}
		defer func() {
			if releaseErr := ownership.Release(); releaseErr != nil {
				fmt.Fprintf(os.Stderr, "tangent: release database ownership: %v\n", releaseErr)
			}
		}()

		sqlDB, openErr := tangentdb.Open(dbPath)
		if openErr != nil {
			fmt.Fprintf(os.Stderr, "tangent: open db: %v\n", openErr)
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
		if *revokeSessions {
			// Migrations run first: the table has to exist before it can be
			// emptied, and an operator reaching for this flag after an upgrade
			// should not have to run --migrate-only first.
			if migrateErr := tangentdb.RunMigrations(sqlDB); migrateErr != nil {
				fmt.Fprintf(os.Stderr, "tangent: migrate db: %v\n", migrateErr)
				os.Exit(1)
			}
			store, storeErr := participant.NewStore(sqlDB)
			if storeErr != nil {
				fmt.Fprintf(os.Stderr, "tangent: build participant session store: %v\n", storeErr)
				os.Exit(1)
			}
			revoked, revokeErr := store.RevokeAll(context.Background(), "operator-revoked")
			if revokeErr != nil {
				fmt.Fprintf(os.Stderr, "tangent: revoke participant sessions: %v\n", revokeErr)
				os.Exit(1)
			}
			logger.Info("revoked browser participant sessions", "count", revoked)
			return
		}
		// *migrateOnly is the only mode left in this branch.
		if migrateErr := tangentdb.RunMigrations(sqlDB); migrateErr != nil {
			fmt.Fprintf(os.Stderr, "tangent: migrate db: %v\n", migrateErr)
			os.Exit(1)
		}
		logger.Info("applied tangent migrations")
		return
	}

	// Every other mode has already returned or exited. What's left is
	// ordinary serving: acquire ownership, open and migrate the database,
	// build every service, and construct the mux — all of it internal/boot,
	// so cmd/tangent-app's adopt-or-boot path can call the exact same
	// function this does.
	_, srv, closer, bootErr := boot.Boot(boot.Config{
		DBPath:          dbPath,
		Port:            *port,
		DevFrontendURL:  os.Getenv(envDevFrontendURL),
		ManagedResource: os.Getenv(envManagedResource),
		OTel:            os.Getenv(envOpenTelemetry) != "",
		OwnerLabel:      "tangent serve",
		Logger:          logger,
	})
	if bootErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: %v\n", bootErr)
		os.Exit(1)
	}
	defer func() {
		if closeErr := closer.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "tangent: %v\n", closeErr)
		}
	}()

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

// exclusiveModes counts how many one-shot maintenance modes were requested.
// Each of them exits before the server starts, so asking for two is a mistake
// rather than a sequence.
func exclusiveModes(modes ...bool) int {
	count := 0
	for _, requested := range modes {
		if requested {
			count++
		}
	}
	return count
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
