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
	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/effect"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/health"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/packages"
	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/roomflow"
	"github.com/hollis-labs/tangent/internal/server"
	"github.com/hollis-labs/tangent/internal/telemetry"
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
	port := flagSet.Int("port", resolvePort(), "HTTP listen port (overrides "+envPort+")")
	migrateOnly := flagSet.Bool("migrate-only", false, "apply DB migrations and exit")
	rollbackOne := flagSet.Bool("rollback-one", false, "roll back the most recent DB migration and exit")
	// Participant sessions have no idle expiry — a local single-user tool must
	// not log its user out mid-decision — so revocation is an explicit
	// operator act rather than a timer or a UI control. Every browser simply
	// mints a fresh session on its next page load.
	revokeSessions := flagSet.Bool("revoke-participant-sessions", false,
		"revoke every browser participant session and exit")
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		// flag.ExitOnError already handled this; keep the linter happy.
		os.Exit(2)
	}
	if exclusiveModes(*migrateOnly, *rollbackOne, *revokeSessions) > 1 {
		fmt.Fprintln(os.Stderr,
			"tangent: --migrate-only, --rollback-one, and --revoke-participant-sessions are mutually exclusive")
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
	if migrateErr := tangentdb.RunMigrations(sqlDB); migrateErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: migrate db: %v\n", migrateErr)
		os.Exit(1)
	}
	if *migrateOnly {
		logger.Info("applied tangent migrations")
		return
	}

	// Correlation telemetry (CW-20260825-0078). It is constructed before
	// anything it observes, and it shares the one database handle: an
	// observation whose interaction lives in another transaction is an
	// observation that can go missing.
	//
	// Every consumer below takes the recorder as an optional dependency and
	// treats nil as a no-op, so nothing downstream can fail for want of
	// telemetry. The store's only failure mode is a nil database handle, which
	// is a build defect rather than an operator condition, so it is fatal here
	// for the same reason every other composition-root construction is.
	telemetryStore, telemetryStoreErr := telemetry.NewSQLStore(sqlDB)
	if telemetryStoreErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: build telemetry store: %v\n", telemetryStoreErr)
		os.Exit(1)
	}
	recorder := telemetry.New(
		telemetry.WithSink(telemetryStore),
		telemetry.WithLogger(logger),
		telemetry.WithOpenTelemetry(os.Getenv(envOpenTelemetry) != ""),
	)
	// Retention is applied at boot rather than on a timer. The table carries no
	// content and its rows are small, so the only thing an in-process sweeper
	// would buy is a goroutine to shut down cleanly; a restart is frequent
	// enough on a single-user desktop tool to keep the ceiling honest.
	if pruned, pruneErr := telemetryStore.Prune(
		context.Background(),
		time.Now().UTC().Add(-telemetry.DefaultRetention),
		telemetry.DefaultRetainedRows,
	); pruneErr != nil {
		logger.Warn("tangent: telemetry retention sweep incomplete")
	} else if pruned > 0 {
		logger.Info("pruned telemetry observations", "count", pruned)
	}

	// The host authority: a Cerberus Workspace where one is composed, and
	// otherwise the standalone one, which registers no workspace root, grants
	// no host-mediated effect capability, and holds no administrator
	// (ADR 0004 §10). Composition is never mandatory; standalone is the normal
	// configuration, not a degraded one.
	//
	// It is constructed first because two things downstream are derived from
	// it and neither may be spelled independently: the definition registry's
	// grantable-capability set, and the privileged-actor policy.
	hostAuthority := effect.Standalone()

	// Boot context governs envelope-registry load. Cancellation tears
	// down the schema-compile loop cleanly; we rebind it once the
	// service is up.
	bootCtx, cancelBoot := context.WithCancel(context.Background())
	envSvc, err := envelope.New(bootCtx, envelope.WithHostPolicy(definition.HostPolicy{
		HostVersion:     envelope.HostVersion,
		ProtocolVersion: envelope.ProtocolVersion,
		// This field has had no producer until now. It is the host half of
		// ADR 0003 §2.5's capability intersection, and it is empty for a
		// standalone Tangent — so a definition that requires a non-optional
		// capability is quarantined rather than served, which is the
		// fail-closed behavior §8 C7 requires.
		GrantableCapabilities: effect.GrantableCapabilityIDs(bootCtx, hostAuthority),
	}))
	cancelBoot()
	if err != nil {
		// Fail fast: a half-loaded registry would let unknown envelope
		// types through silently. Without an envelope substrate the
		// rest of Tangent is meaningless.
		fmt.Fprintf(os.Stderr, "tangent: load envelope registry: %v\n", err)
		os.Exit(1)
	}
	logger.Info("loaded envelope types", "count", envSvc.Len())

	// Plugin-extension registration. The Tangent-owned kinds are not in
	// go-envelopes core in v0.1.0; extensions.RegisterAll installs the
	// in-tree manifest fragments through the plugin extension API so
	// envelope validation in Dispatcher.Dispatch succeeds for them. The
	// same function backs cmd/tangent-dump-types, so the generated
	// TypeScript always describes the registry this server serves.
	if regErr := extensions.RegisterAll(envSvc); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register envelope extensions: %v\n", regErr)
		os.Exit(1)
	}
	logger.Info("registered tangent envelope extensions", "plugin", extensions.PluginID, "count", envSvc.Len())

	// Dispatcher is shared across transports. PR 4 registers the
	// triage handler that bridges to a WebSocket-connected room.
	dispatcher := envelope.NewDispatcher(envSvc)
	// One store, shared: it persists the interaction records and, through
	// interaction.WithRetainedMaterialStore, the exact definition material each
	// record pins. Those have to be the same database — a pin whose material
	// lives somewhere the record's own transaction cannot see is a pin that can
	// go missing.
	interactionStore := interaction.NewStore(sqlDB)
	interactionService, interactionErr := interaction.NewService(
		interactionStore,
		interaction.NewEnvelopeDefinitionCatalog(envSvc, mcp.HostVersion,
			interaction.WithRetainedMaterialStore(interactionStore)),
		interaction.WithSurfaceAccessPolicy(hitl.SurfaceAccessPolicy{}),
		// The in-process caller-pull adapter is the only actor allowed to
		// assert that a terminal outcome was delivered. Direct MCP callers
		// never hold that authority.
		interaction.WithDeliveryWorkerPolicy(roomflow.DeliveryWorkerPolicy{}),
		// ADR 0004 §Q10 assigned CW-20260825-0077 the choice of whether to
		// supply a real PrivilegedActorPolicy. The choice is: wire the seam,
		// keep the default deny. Over the standalone authority this denies
		// both host-policy and administrator questions — the same answer the
		// unwired fallback gave, now reached through a construction that has a
		// call site and a test.
		interaction.WithPrivilegedActorPolicy(
			interaction.NewEffectPrivilegedActorPolicy(hostAuthority)),
		interaction.WithTelemetry(recorder),
	)
	if interactionErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: build interaction service: %v\n", interactionErr)
		os.Exit(1)
	}
	hitlService, hitlErr := hitl.NewService(interactionService)
	if hitlErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: build hitl service: %v\n", hitlErr)
		os.Exit(1)
	}
	recovery, recoveryErr := interactionService.RecoverAfterRestart(context.Background())
	if recoveryErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: recover durable interactions: %v\n", recoveryErr)
		os.Exit(1)
	}
	logger.Info(
		"recovered durable interactions",
		"staged", recovery.Staged,
		"presented", recovery.Presented,
		"draft_bearing", recovery.DraftBearing,
		"resolved_undelivered", recovery.ResolvedUndelivered,
		"delivered_unacknowledged", recovery.DeliveredUnacknowledged,
		"preserved_terminal_interactions", recovery.PreservedTerminalInteractions,
		"terminal_failed_deliveries", recovery.TerminalFailedDeliveries,
		"interrupted_deliveries", recovery.InterruptedDeliveries,
		"immediately_retryable", recovery.ImmediatelyRetryable,
		"manual_reconciliation_required", recovery.ManualReconciliationRequired,
	)

	// Room manager + WS handler — the bridge between MCP envelopes and
	// browser tabs. Created before the MCP server so the triage
	// handler has somewhere to push.
	roomMgr := room.NewManager(sqlDB)
	if hydrateErr := roomMgr.Hydrate(context.Background()); hydrateErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: hydrate rooms: %v\n", hydrateErr)
		os.Exit(1)
	}
	wsHandler := tangentws.New(roomMgr, logger)
	wsHandler.SetTelemetry(recorder)

	// The authenticated browser participant session (ADR 0004 §4). It is what
	// moves room authority off a URL that the product deliberately publishes —
	// returned by session_create, logged when a workflow opens a room, pasted
	// into agent transcripts — and onto a cookie that a link cannot carry.
	participantStore, participantErr := participant.NewStore(sqlDB)
	if participantErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: build participant session store: %v\n", participantErr)
		os.Exit(1)
	}
	participantGate, gateErr := participant.NewGate(participantStore)
	if gateErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: build participant session gate: %v\n", gateErr)
		os.Exit(1)
	}
	// The /ws upgrade requires a session immediately, with no grace period:
	// this is the single change that stops a room UUID from being an answer
	// credential. The resolver runs before the upgrade, so a refusal is an
	// ordinary 403 rather than a socket that closes without explanation.
	wsHandler.SetParticipantResolver(participantGate.ResolveBinding)

	// The host-mediated effect broker (ADR 0003 §2.5). It shares the same
	// database handle as everything else: a receipt whose interaction lives in
	// another transaction is a receipt that can go missing.
	effectStore, effectStoreErr := effect.NewSQLStore(sqlDB)
	if effectStoreErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: build effect store: %v\n", effectStoreErr)
		os.Exit(1)
	}
	effectBroker, effectBrokerErr := effect.NewBroker(effectStore, hostAuthority)
	if effectBrokerErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: build effect broker: %v\n", effectBrokerErr)
		os.Exit(1)
	}

	// The three operability probes (CW-20260825-0066). The reporter is built
	// here, at the composition root, because it is the only place that holds
	// all five readiness dependencies at once — and because every one of them
	// is passed as the live object rather than a snapshot, so a probe reports
	// the process now instead of the process at boot.
	devFrontendURL := os.Getenv(envDevFrontendURL)
	healthReporter := health.NewReporter(
		health.WithDatabase(sqlDB),
		health.WithDefinitionRegistry(envSvc),
		health.WithRendererHost(server.RendererHostProbe(devFrontendURL)),
		// The delivery worker is a policy, not a goroutine: what readiness
		// asks is whether the installed DeliveryWorkerPolicy admits the
		// in-process caller-pull adapter. A build where it does not still
		// serves rooms and still collects answers — it just never hands an
		// outcome back to the caller.
		health.WithDeliveryWorker(func() health.DeliveryWorker {
			return health.DeliveryWorker{
				Authorized: interactionService.AuthorizesDeliveryWorker(roomflow.DeliveryWorker),
				Scope:      roomflow.DeliveryWorker.Scope,
			}
		}),
		health.WithRuntime(health.Runtime{ManagedResource: os.Getenv(envManagedResource)}),
		health.WithTelemetry(recorder),
	)
	// Record every kind this build cannot serve, once, while the answer is
	// complete: materialization has finished and host policy has been applied.
	// A capability health report naming a broken kind therefore points at a
	// trace that already holds the reason, rather than at an empty one that
	// only fills up after a caller trips over it.
	healthReporter.ObserveDefinitions(context.Background())

	// Use 127.0.0.1 to match the listener's actual bind so the URL
	// hint we log when triage creates a room resolves correctly even
	// on IPv6-preferring systems where "localhost" lands on ::1.
	roomURLBase := fmt.Sprintf("http://127.0.0.1:%d", *port)
	// The publisher-owned interaction packages this build hosts. Registration
	// is separate from the definition registration above because the two
	// answer different questions: extensions.RegisterAll says which
	// definitions exist, packages.RegisterAll says which of them bring their
	// own behavior (ADR 0003 §5).
	interactionPackages, err := packages.NewRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tangent: register interaction packages: %v\n", err)
		os.Exit(1)
	}
	mcpSrv, err := mcp.New(
		envSvc,
		dispatcher,
		roomMgr,
		roomURLBase,
		mcp.WithInteractionService(interactionService),
		mcp.WithHITLService(hitlService),
		mcp.WithInteractionPackages(interactionPackages),
		mcp.WithHealthReporter(healthReporter),
		mcp.WithTelemetry(recorder, telemetryStore),
	)
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
	if regErr := mcp.RegisterFormCollectOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register form-collect handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterDesignIterationOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register design-iteration handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterInterviewQuestionOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register interview-question handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterBlockDraftOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register block-draft handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterProseRevisionOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register prose-revision handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterOutputRenderOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register output-render handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterWhiteboardOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register whiteboard handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterDashboardOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register dashboard handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterFilePickerOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register file-picker handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterProgressPanelOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register progress-panel handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterWizardOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register wizard handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterDiffReviewOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register diff-review handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterSpreadsheetReviewOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register spreadsheet-review handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterApprovalQueueOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register approval-queue handler: %v\n", regErr)
		os.Exit(1)
	}
	if regErr := mcp.RegisterSynthesisNotesOnDispatcher(dispatcher, triageHandler); regErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: register synthesis-notes handler: %v\n", regErr)
		os.Exit(1)
	}

	// Rebuild every live room's UI from canonical records. The in-memory
	// pending map is empty at this point, so anything a browser sees after a
	// restart came from durable interactions, not from process-local state.
	restored, restoreErr := mcpSrv.RestoreRoomPresentations(context.Background())
	if restoreErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: restore room presentations: %v\n", restoreErr)
		os.Exit(1)
	}
	logger.Info("restored room presentations",
		"restored", restored.Restored,
		"missing_rooms", restored.MissingRooms,
		"undefinable", restored.Undefinable,
	)

	srv, err := server.New(server.Config{
		Port:           *port,
		DevFrontendURL: devFrontendURL,
		Logger:         logger,
		Envelope:       envSvc,
		MCP:            mcpSrv,
		WSHandler:      wsHandler,
		RoomManager:    roomMgr,
		HITL:           hitlService,
		Rooms:          mcpSrv,
		Participants:   participantGate,
		Effects:        effectBroker,
		EffectContext:  interactionService,
		Health:         healthReporter,
		Telemetry:      recorder,
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
