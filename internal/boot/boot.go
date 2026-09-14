// Package boot assembles a Tangent server process: the single-writer
// ownership claim, the database, migrations, every constructed service, and
// the HTTP/MCP/WebSocket mux. It is the one entry point that can boot a
// Tangent, extracted out of cmd/tangent/main.go so a second binary
// (cmd/tangent-app, the desktop shell) can boot the same graph without
// reimplementing it, and so the smoke harness exercises the real
// construction instead of a bespoke subset.
//
// Boot covers "acquire ownership" through "build the mux" — everything
// cmd/tangent/main.go used to do inline before it could call Listen/Serve.
// It deliberately does not listen, serve, or handle signals: those stay
// CLI-transport concerns owned by whichever binary calls Boot, since a
// desktop app's shutdown path (hide the window, or quit) is not a CLI's
// (a signal channel and a graceful-shutdown timeout).
//
// Boot is also never the path for the operator maintenance one-shots
// (--db-check, --db-backup, --migrate-only, and siblings in
// cmd/tangent/maintenance.go). Those deliberately do not run migrations
// first — an operator reaching for restore/check/repair when the schema is
// the problem must not have it silently migrated — which is the opposite of
// what Boot does. They keep their own small ownership-acquire-and-open
// preamble in cmd/tangent rather than sharing this one.
package boot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/hollis-labs/tangent/internal/channel"
	"github.com/hollis-labs/tangent/internal/channelpane"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/effect"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/health"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/interactionpkg"
	"github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/packages"
	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	"github.com/hollis-labs/tangent/internal/pluginpkg"
	"github.com/hollis-labs/tangent/internal/plugins"
	"github.com/hollis-labs/tangent/internal/relay"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/roomflow"
	"github.com/hollis-labs/tangent/internal/server"
	"github.com/hollis-labs/tangent/internal/telemetry"
	"github.com/hollis-labs/tangent/internal/turns"
	tangentws "github.com/hollis-labs/tangent/internal/ws"
)

// Config is everything Boot needs that a caller might reasonably vary.
// Every field mirrors an environment variable cmd/tangent has always read;
// Boot itself reads no environment — the caller resolves env/flags and
// passes the result, so cmd/tangent-app can supply its own values without
// route through cmd/tangent's flag set.
type Config struct {
	// DBPath is the database file path. Empty means the package default
	// (see internal/db).
	DBPath string

	// Port is the TCP port the constructed server will bind when Listen is
	// called. Boot does not listen; it only threads Port into the
	// constructed server.Config and the room URL base the MCP tool surface
	// logs and hands back to callers.
	Port int

	// DevFrontendURL, when non-empty, makes the server reverse-proxy
	// non-API requests to a Vite dev server instead of serving the embedded
	// SPA. Empty in every production build.
	DevFrontendURL string

	// ManagedResource names the managed-runtime resource that owns this
	// process's lifecycle, when one does. Health reports use it to
	// recommend a concrete restart command. Empty is normal and supported.
	ManagedResource string

	// OTel opts this process into the OpenTelemetry bridge. See
	// internal/telemetry/otel.go for why this is off by default.
	OTel bool

	// OwnerLabel is the "command" recorded in the single-writer lock's
	// advisory metadata, and what a refusal quotes back to whatever else is
	// trying to acquire ownership. Boot always acquires as
	// tangentdb.RoleServer — Role selection is not a caller decision here,
	// because Boot is never used for a maintenance one-shot — but the label
	// is, so a refusal names the actual binary and mode holding the
	// database ("tangent serve" vs. a future "tangent-app") rather than a
	// generic string. Empty falls back to "tangent serve".
	OwnerLabel string
	// PluginDir is the installed-plugin root. Empty resolves through
	// pluginpkg.DefaultRoot, which honors TANGENT_PLUGIN_DIR and otherwise
	// uses ~/.tangent/plugins. It is a field so a test can point a boot at a
	// directory it owns rather than at the operator's real plugins.
	PluginDir string

	// Logger receives every constructed service's structured logs. Nil
	// defaults to slog.Default(). Boot does not call slog.SetDefault —
	// that is a process-global side effect the caller owns.
	Logger *slog.Logger
}

// Services is the constructed service graph. Callers that only need to
// listen and serve can ignore it; internal/smoke and cmd/tangent-app use it
// to reach individual services directly (a health probe, a room lookup)
// without going through HTTP.
type Services struct {
	DB *sql.DB

	Envelope    *envelope.Service
	Dispatcher  *envelope.Dispatcher
	Interaction *interaction.Service
	HITL        *hitl.Service
	Turns       *turns.Service

	RoomManager     *room.Manager
	WSHandler       *tangentws.Handler
	ParticipantGate *participant.Gate
	EffectBroker    *effect.Broker
	HealthReporter  *health.Reporter
	MCP             *mcp.Server
	Packages        *interactionpkg.Registry
	TelemetryStore  *telemetry.SQLStore
	Telemetry       *telemetry.Recorder
	HostAuthority   effect.Authority
	RoomURLBase     string
}

// ownedCloser releases the database handle and the single-writer ownership
// claim, in that order. The order matters: releasing the lock before the
// handle is closed would let a second process open the database while this
// one might still flush a write.
type ownedCloser struct {
	db        *sql.DB
	ownership *tangentdb.Ownership
	// plugins is the plugin host. UnloadAll runs first on the way out: it
	// releases every in-flight plugin dispatch rather than waiting out the
	// dispatch budget, then lets each plugin drop its own state. It removes
	// nothing a plugin registered, which is the host's stated contract and not
	// an oversight — internal/pluginhost/lifecycle.go says why.
	//
	// First for the same reason the loopback closes before the database: a
	// plugin handler that nothing bounded is exactly what CW-20260909-0045
	// measured graceful shutdown waiting on, and a process still holding the
	// database when its successor boots is the cost.
	plugins *pluginhost.Host
	// loopback is the plugin host's in-process MCP session. It is closed
	// before the database because it can still be serving a plugin's tool
	// call, and it holds the SDK's reader goroutines — a process that forgot
	// it would leak them silently, which is exactly the class internal/mcp's
	// goleak tests exist to catch.
	loopback io.Closer
}

func (c *ownedCloser) Close() error {
	var errs []error
	if c.plugins != nil {
		if err := c.plugins.UnloadAll(); err != nil {
			errs = append(errs, fmt.Errorf("unload plugins: %w", err))
		}
	}
	if c.loopback != nil {
		if err := c.loopback.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close plugin tool caller: %w", err))
		}
	}
	if err := tangentdb.Close(c.db); err != nil {
		errs = append(errs, fmt.Errorf("close db: %w", err))
	}
	if err := c.ownership.Release(); err != nil {
		errs = append(errs, fmt.Errorf("release database ownership: %w", err))
	}
	return errors.Join(errs...)
}

// Boot acquires single-writer ownership, opens and migrates the database,
// constructs every service, and returns a server ready for the caller to
// Listen and Serve.
//
// On error, Boot has already released anything it acquired — the returned
// io.Closer is nil in that case, not partially valid. A caller that gets
// back a *tangentdb.OwnershipConflict (via errors.As) knows another Tangent
// already holds this database; that is the fact cmd/tangent-app's
// adopt-or-boot decision is built on.
func Boot(cfg Config) (*Services, *server.Server, io.Closer, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ownerLabel := cfg.OwnerLabel
	if ownerLabel == "" {
		ownerLabel = "tangent serve"
	}

	ownership, err := tangentdb.AcquireOwnership(cfg.DBPath, tangentdb.RoleServer, ownerLabel)
	if err != nil {
		// *tangentdb.OwnershipConflict flows through unwrapped: it is
		// already the typed, errors.As-able signal a caller needs to tell
		// "something else holds this database" apart from every other way
		// Boot can fail.
		return nil, nil, nil, err
	}

	sqlDB, err := tangentdb.Open(cfg.DBPath)
	if err != nil {
		_ = ownership.Release()
		return nil, nil, nil, fmt.Errorf("open db: %w", err)
	}
	closer := &ownedCloser{db: sqlDB, ownership: ownership}
	release := func(err error) (*Services, *server.Server, io.Closer, error) {
		_ = closer.Close()
		return nil, nil, nil, err
	}

	if migrateErr := tangentdb.RunMigrations(sqlDB); migrateErr != nil {
		return release(fmt.Errorf("migrate db: %w", migrateErr))
	}

	// Correlation telemetry (CW-20260825-0078). It is constructed before
	// anything it observes, and it shares the one database handle: an
	// observation whose interaction lives in another transaction is an
	// observation that can go missing.
	//
	// Every consumer below takes the recorder as an optional dependency and
	// treats nil as a no-op, so nothing downstream can fail for want of
	// telemetry. The store's only failure mode is a nil database handle,
	// which is a build defect rather than an operator condition, so it is
	// fatal here for the same reason every other composition-root
	// construction is.
	telemetryStore, err := telemetry.NewSQLStore(sqlDB)
	if err != nil {
		return release(fmt.Errorf("build telemetry store: %w", err))
	}
	recorder := telemetry.New(
		telemetry.WithSink(telemetryStore),
		telemetry.WithLogger(logger),
		telemetry.WithOpenTelemetry(cfg.OTel),
	)
	// Retention is applied at boot rather than on a timer. The table
	// carries no content and its rows are small, so the only thing an
	// in-process sweeper would buy is a goroutine to shut down cleanly; a
	// restart is frequent enough on a single-user desktop tool to keep the
	// ceiling honest.
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
	// otherwise the standalone one, which registers no workspace root,
	// grants no host-mediated effect capability, and holds no
	// administrator (ADR 0004 §10). Composition is never mandatory;
	// standalone is the normal configuration, not a degraded one.
	//
	// It is constructed first because two things downstream are derived
	// from it and neither may be spelled independently: the definition
	// registry's grantable-capability set, and the privileged-actor policy.
	hostAuthority := effect.Standalone()

	// Boot context governs envelope-registry load. Cancellation tears down
	// the schema-compile loop cleanly; we rebind it once the service is up.
	bootCtx, cancelBoot := context.WithCancel(context.Background())
	envSvc, err := envelope.New(bootCtx, envelope.WithHostPolicy(definition.HostPolicy{
		HostVersion:     envelope.HostVersion,
		ProtocolVersion: envelope.ProtocolVersion,
		// This field has had no producer until now. It is the host half of
		// ADR 0003 §2.5's capability intersection, and it is empty for a
		// standalone Tangent — so a definition that requires a
		// non-optional capability is quarantined rather than served, which
		// is the fail-closed behavior §8 C7 requires.
		GrantableCapabilities: effect.GrantableCapabilityIDs(bootCtx, hostAuthority),
	}))
	cancelBoot()
	if err != nil {
		// Fail fast: a half-loaded registry would let unknown envelope
		// types through silently. Without an envelope substrate the rest
		// of Tangent is meaningless.
		return release(fmt.Errorf("load envelope registry: %w", err))
	}
	logger.Info("loaded envelope types", "count", envSvc.Len())

	// Plugin-extension registration. The Tangent-owned kinds are not in
	// go-envelopes core in v0.1.0; extensions.RegisterAll installs the
	// in-tree manifest fragments through the plugin extension API so
	// envelope validation in Dispatcher.Dispatch succeeds for them. The
	// same function backs cmd/tangent-dump-types, so the generated
	// TypeScript always describes the registry this server serves.
	if regErr := extensions.RegisterAll(envSvc); regErr != nil {
		return release(fmt.Errorf("register envelope extensions: %w", regErr))
	}
	logger.Info("registered tangent envelope extensions", "plugin", extensions.PluginID, "count", envSvc.Len())

	// The second door for interaction kinds (ADR 0007 §4). A plugin names a
	// kind; internal/pluginhost resolves the ADR 0003 manifest this host ships
	// for that name and refuses the registration if there is none. The plugin
	// authors nothing about what the kind may do — not its trust class, not its
	// capabilities, not its assurance.
	//
	// It runs after RegisterAll because a plugin-contributed kind is additive:
	// the host-package registry is complete before any plugin is consulted, and
	// a plugin failing to load cannot leave a host kind unregistered.
	//
	// Plugins are INSTALLED, not compiled in (CW-20260911-0070). What loads here
	// is whatever is under the plugin directory, so this host's binary links no
	// application's client code — which is the boundary ADR 0005 exists to
	// protect and the reason subprocess mode was built.
	pluginRoot := cfg.PluginDir
	if pluginRoot == "" {
		resolved, rootErr := pluginpkg.DefaultRoot()
		if rootErr != nil {
			return release(rootErr)
		}
		pluginRoot = resolved
	}
	pluginHost, pluginErr := plugins.LoadInstalled(context.Background(), logger, envSvc, pluginRoot)
	if pluginErr != nil {
		return release(fmt.Errorf("load installed plugins: %w", pluginErr))
	}
	// On the closer as soon as it exists, so a Boot that fails further down
	// still unloads what it loaded rather than leaving plugins holding state in
	// a process that is about to exit.
	closer.plugins = pluginHost
	logger.Info("loaded shipped plugins",
		"kinds", pluginHost.ContributedKinds(), "count", envSvc.Len())
	// What the plugins registered is installed further down, once the surfaces
	// that host them exist: MCP tools through mcp.WithPluginTools, HTTP routes
	// through server.Config.PluginRoutes. The two steps are not an accident of
	// ordering — a plugin-contributed envelope kind has to be in the registry
	// before mcp.New reads it, so plugins necessarily load first and the host
	// holds their declarations until there is something to install them onto.
	pluginTools := pluginHost.MCPTools()
	pluginRoutes := pluginHost.HTTPRoutes()

	// Dispatcher is shared across transports. The MCP triage handler below
	// bridges it to a WebSocket-connected room.
	dispatcher := envelope.NewDispatcher(envSvc)
	// One store, shared: it persists the interaction records and, through
	// interaction.WithRetainedMaterialStore, the exact definition material
	// each record pins. Those have to be the same database — a pin whose
	// material lives somewhere the record's own transaction cannot see is
	// a pin that can go missing.
	interactionStore := interaction.NewStore(sqlDB)
	interactionService, err := interaction.NewService(
		interactionStore,
		interaction.NewEnvelopeDefinitionCatalog(envSvc, mcp.HostVersion,
			interaction.WithRetainedMaterialStore(interactionStore)),
		interaction.WithSurfaceAccessPolicy(compoundSurfaceAccessPolicy{
			hitl.SurfaceAccessPolicy{},
			turns.SurfaceAccessPolicy{},
		}),
		// The in-process caller-pull adapter is the only actor allowed to
		// assert that a terminal outcome was delivered. Direct MCP callers
		// never hold that authority.
		interaction.WithDeliveryWorkerPolicy(roomflow.DeliveryWorkerPolicy{}),
		// ADR 0004 §Q10 assigned CW-20260825-0077 the choice of whether to
		// supply a real PrivilegedActorPolicy. The choice is: wire the
		// seam, keep the default deny. Over the standalone authority this
		// denies both host-policy and administrator questions — the same
		// answer the unwired fallback gave, now reached through a
		// construction that has a call site and a test.
		interaction.WithPrivilegedActorPolicy(
			interaction.NewEffectPrivilegedActorPolicy(hostAuthority)),
		interaction.WithTelemetry(recorder),
	)
	if err != nil {
		return release(fmt.Errorf("build interaction service: %w", err))
	}
	hitlService, err := hitl.NewService(interactionService)
	if err != nil {
		return release(fmt.Errorf("build hitl service: %w", err))
	}
	turnsService, err := turns.NewService(interactionService)
	if err != nil {
		return release(fmt.Errorf("build turns service: %w", err))
	}
	recovery, err := interactionService.RecoverAfterRestart(context.Background())
	if err != nil {
		return release(fmt.Errorf("recover durable interactions: %w", err))
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
	// browser tabs. Created before the MCP server so the triage handler
	// has somewhere to push.
	roomMgr := room.NewManager(sqlDB)
	if hydrateErr := roomMgr.Hydrate(context.Background()); hydrateErr != nil {
		return release(fmt.Errorf("hydrate rooms: %w", hydrateErr))
	}
	wsHandler := tangentws.New(roomMgr, logger)
	wsHandler.SetTelemetry(recorder)

	// The authenticated browser participant session (ADR 0004 §4). It is
	// what moves room authority off a URL that the product deliberately
	// publishes — returned by session_create, logged when a workflow opens
	// a room, pasted into agent transcripts — and onto a cookie that a
	// link cannot carry.
	participantStore, err := participant.NewStore(sqlDB)
	if err != nil {
		return release(fmt.Errorf("build participant session store: %w", err))
	}
	participantGate, err := participant.NewGate(participantStore)
	if err != nil {
		return release(fmt.Errorf("build participant session gate: %w", err))
	}
	// The /ws upgrade requires a session immediately, with no grace
	// period: this is the single change that stops a room UUID from being
	// an answer credential. The resolver runs before the upgrade, so a
	// refusal is an ordinary 403 rather than a socket that closes without
	// explanation.
	wsHandler.SetParticipantResolver(participantGate.ResolveBinding)

	// The host-mediated effect broker (ADR 0003 §2.5). It shares the same
	// database handle as everything else: a receipt whose interaction
	// lives in another transaction is a receipt that can go missing.
	effectStore, err := effect.NewSQLStore(sqlDB)
	if err != nil {
		return release(fmt.Errorf("build effect store: %w", err))
	}
	effectBroker, err := effect.NewBroker(effectStore, hostAuthority)
	if err != nil {
		return release(fmt.Errorf("build effect broker: %w", err))
	}

	// The three operability probes (CW-20260825-0066). The reporter is
	// built here, at the composition root, because it is the only place
	// that holds all five readiness dependencies at once — and because
	// every one of them is passed as the live object rather than a
	// snapshot, so a probe reports the process now instead of the process
	// at boot.
	healthReporter := health.NewReporter(
		health.WithDatabase(sqlDB),
		health.WithDefinitionRegistry(envSvc),
		health.WithRendererHost(server.RendererHostProbe(cfg.DevFrontendURL)),
		// The delivery worker is a policy, not a goroutine: what readiness
		// asks is whether the installed DeliveryWorkerPolicy admits the
		// in-process caller-pull adapter. A build where it does not still
		// serves rooms and still collects answers — it just never hands
		// an outcome back to the caller.
		health.WithDeliveryWorker(func() health.DeliveryWorker {
			return health.DeliveryWorker{
				Authorized: interactionService.AuthorizesDeliveryWorker(roomflow.DeliveryWorker),
				Scope:      roomflow.DeliveryWorker.Scope,
			}
		}),
		// Which plugins loaded, and which refused and why (CW-20260910-0036).
		// A function rather than a snapshot, like the two probes above: a
		// plugin unloaded on the way out should read as unloaded. The
		// adaptation is here rather than in internal/health so that package
		// keeps depending on vocabularies instead of on the plugin host's
		// lifecycle.
		health.WithPlugins(func() health.PluginInventory {
			return pluginInventory(pluginHost.Inventory())
		}),
		health.WithRuntime(health.Runtime{ManagedResource: cfg.ManagedResource}),
		health.WithTelemetry(recorder),
	)
	// Record every kind this build cannot serve, once, while the answer is
	// complete: materialization has finished and host policy has been
	// applied. A capability health report naming a broken kind therefore
	// points at a trace that already holds the reason, rather than at an
	// empty one that only fills up after a caller trips over it.
	healthReporter.ObserveDefinitions(context.Background())

	// Use 127.0.0.1 to match the listener's actual bind so the URL hint we
	// log when triage creates a room resolves correctly even on
	// IPv6-preferring systems where "localhost" lands on ::1.
	roomURLBase := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	// The publisher-owned interaction packages this build hosts.
	// Registration is separate from the definition registration above
	// because the two answer different questions: extensions.RegisterAll
	// says which definitions exist, packages.RegisterAll says which of
	// them bring their own behavior (ADR 0003 §5).
	interactionPackages, err := packages.NewRegistry()
	if err != nil {
		return release(fmt.Errorf("register interaction packages: %w", err))
	}

	// The cooperative MCP inbox (CW-20260906-0066): channels and the
	// exchange journal share this process's one *sql.DB, the same handle
	// everything else here does. relay.NewStore depends on channelStore for
	// its own validation, never the reverse.
	channelStore, err := channel.NewStore(sqlDB)
	if err != nil {
		return release(fmt.Errorf("construct channel store: %w", err))
	}
	relayStore, err := relay.NewStore(sqlDB, channelStore)
	if err != nil {
		return release(fmt.Errorf("construct relay store: %w", err))
	}

	// The minimal channel pane (CW-20260907-0017): the operator's own
	// send/read path over the same two stores, never the tangent.relay_*
	// MCP surface. hitlService correlates HITL items to the channel's
	// agent by reading its already-public Inbox(), with no change to
	// internal/hitl's own contract.
	channelPaneService, err := channelpane.New(channelStore, relayStore, hitlService)
	if err != nil {
		return release(fmt.Errorf("construct channel pane service: %w", err))
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
		// tangent.retention_status. Read-only by design: the retention
		// operations are CLI commands, because erasure authority belongs
		// to the local user and MCP has no authenticated caller identity
		// to hold it.
		mcp.WithMaintenance(sqlDB, cfg.DBPath),
		// tangent.relay_*: the cooperative MCP inbox.
		mcp.WithRelay(channelStore, relayStore),
		// Whatever the shipped plugins contributed (ADR 0007 §4). Empty is
		// normal; a name colliding with a host tool fails this call.
		mcp.WithPluginTools(pluginTools),
	)
	if err != nil {
		// MCP construction failure is fatal: the binary advertises an MCP
		// surface as part of its v0.1 contract, so booting without it
		// would silently strip a documented capability.
		return release(fmt.Errorf("build mcp server: %w", err))
	}
	// The plugin host's tool caller (CW-20260910-0031). A plugin drives Tangent
	// by calling the same tools an agent calls, in process, with the same
	// host-assigned caller identity — see internal/mcp/loopback.go for why it
	// goes over a real session rather than straight to a handler.
	//
	// It is attached here, after mcp.New, because that is the first moment the
	// tool surface exists; a plugin resolves it at dispatch time, so a plugin
	// that loaded earlier is not holding a nil.
	loopback, err := mcpSrv.NewLoopbackCaller(context.Background())
	if err != nil {
		return release(fmt.Errorf("build plugin tool caller: %w", err))
	}
	closer.loopback = loopback
	if attachErr := pluginHost.AttachToolCaller(loopback); attachErr != nil {
		return release(fmt.Errorf("attach plugin tool caller: %w", attachErr))
	}

	triageHandler := mcp.NewTriageHandler(roomMgr, logger, roomURLBase)
	registrations := []struct {
		name string
		fn   func(*envelope.Dispatcher, *mcp.TriageHandler) error
	}{
		{"triage", mcp.RegisterTriageOnDispatcher},
		{"feedback", mcp.RegisterFeedbackOnDispatcher},
		{"form-collect", mcp.RegisterFormCollectOnDispatcher},
		{"design-iteration", mcp.RegisterDesignIterationOnDispatcher},
		{"interview-question", mcp.RegisterInterviewQuestionOnDispatcher},
		{"block-draft", mcp.RegisterBlockDraftOnDispatcher},
		{"prose-revision", mcp.RegisterProseRevisionOnDispatcher},
		{"output-render", mcp.RegisterOutputRenderOnDispatcher},
		{"whiteboard", mcp.RegisterWhiteboardOnDispatcher},
		{"dashboard", mcp.RegisterDashboardOnDispatcher},
		{"app-board", mcp.RegisterAppBoardOnDispatcher},
		{"file-picker", mcp.RegisterFilePickerOnDispatcher},
		{"progress-panel", mcp.RegisterProgressPanelOnDispatcher},
		{"wizard", mcp.RegisterWizardOnDispatcher},
		{"diff-review", mcp.RegisterDiffReviewOnDispatcher},
		{"spreadsheet-review", mcp.RegisterSpreadsheetReviewOnDispatcher},
		{"approval-queue", mcp.RegisterApprovalQueueOnDispatcher},
		{"synthesis-notes", mcp.RegisterSynthesisNotesOnDispatcher},
	}
	for _, reg := range registrations {
		if regErr := reg.fn(dispatcher, triageHandler); regErr != nil {
			return release(fmt.Errorf("register %s handler: %w", reg.name, regErr))
		}
	}

	// Rebuild every live room's UI from canonical records. The in-memory
	// pending map is empty at this point, so anything a browser sees after
	// a restart came from durable interactions, not from process-local
	// state.
	restored, err := mcpSrv.RestoreRoomPresentations(context.Background())
	if err != nil {
		return release(fmt.Errorf("restore room presentations: %w", err))
	}
	logger.Info("restored room presentations",
		"restored", restored.Restored,
		"missing_rooms", restored.MissingRooms,
		"undefinable", restored.Undefinable,
	)

	srv, err := server.New(server.Config{
		Port:           cfg.Port,
		DevFrontendURL: cfg.DevFrontendURL,
		Logger:         logger,
		Envelope:       envSvc,
		MCP:            mcpSrv,
		WSHandler:      wsHandler,
		RoomManager:    roomMgr,
		HITL:           hitlService,
		Turns:          turnsService,
		Rooms:          mcpSrv,
		Channels:       channelPaneService,
		Participants:   participantGate,
		Effects:        effectBroker,
		EffectContext:  interactionService,
		Health:         healthReporter,
		Telemetry:      recorder,
		PluginRoutes:   pluginRoutes,
	})
	if err != nil {
		return release(err)
	}

	services := &Services{
		DB:              sqlDB,
		Envelope:        envSvc,
		Dispatcher:      dispatcher,
		Interaction:     interactionService,
		HITL:            hitlService,
		Turns:           turnsService,
		RoomManager:     roomMgr,
		WSHandler:       wsHandler,
		ParticipantGate: participantGate,
		EffectBroker:    effectBroker,
		HealthReporter:  healthReporter,
		MCP:             mcpSrv,
		Packages:        interactionPackages,
		TelemetryStore:  telemetryStore,
		Telemetry:       recorder,
		HostAuthority:   hostAuthority,
		RoomURLBase:     roomURLBase,
	}
	return services, srv, closer, nil
}

// pluginInventory adapts the plugin host's inventory into the health
// vocabulary. It is a field-for-field copy and it is deliberately dumb: the
// alternative is internal/health importing internal/pluginhost, which would
// make the health package depend on the plugin host's lifecycle to describe it.
//
// The attribution note travels with the document rather than being restated
// here, because it is a fact about the report and not about this translation.
func pluginInventory(source pluginhost.PluginInventory) health.PluginInventory {
	inventory := health.PluginInventory{
		Loaded:           source.Loaded,
		Refused:          source.Refused,
		Plugins:          make([]health.PluginRecord, 0, len(source.Plugins)),
		ContributedKinds: source.ContributedKinds,
		Tools:            source.Tools,
		Routes:           source.Routes,
		Attribution:      source.Attribution,
	}
	for _, record := range source.Plugins {
		inventory.Plugins = append(inventory.Plugins, health.PluginRecord{
			ID:      record.ID,
			Name:    record.Name,
			Version: record.Version,
			Loaded:  record.Loaded,
			Enabled: record.Enabled,
			At:      record.At,
			Error:   record.Error,
		})
	}
	return inventory
}

type compoundSurfaceAccessPolicy []interaction.SurfaceAccessPolicy

func (c compoundSurfaceAccessPolicy) Authorize(surfaceID string, capability string) bool {
	for _, p := range c {
		if !p.Authorize(surfaceID, capability) {
			return false
		}
	}
	return true
}
