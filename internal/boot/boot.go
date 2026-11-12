// Package boot assembles Tangent's service graph: every constructed service,
// the MCP tools on the host's server, and the route table the host serves.
//
// The database, the HTTP host, identity and the MCP transport belong to the
// Substrate modules; Boot takes the database handle and the MCP server from
// them and builds what is Tangent's own on top. It does not listen, serve, own
// the database file, or read the environment: the caller resolves those and
// passes the result.
package boot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/hollis-labs/tangent/internal/replymail"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	gmcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tangent/internal/access"
	"github.com/hollis-labs/tangent/internal/artifacts"
	"github.com/hollis-labs/tangent/internal/channel"
	"github.com/hollis-labs/tangent/internal/channelpane"
	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/docs"
	"github.com/hollis-labs/tangent/internal/effect"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/health"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/hitl/gohitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/interactionpkg"
	"github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/packages"
	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/pluginconfig"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	"github.com/hollis-labs/tangent/internal/pluginpkg"
	"github.com/hollis-labs/tangent/internal/plugins"
	"github.com/hollis-labs/tangent/internal/relay"
	"github.com/hollis-labs/tangent/internal/retention"
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
	// DB is the host's database handle (the database module's single writer).
	// Boot never closes it.
	DB *sql.DB

	// DBPath is the database file's path, which the retention reporter names.
	DBPath string

	// ArtifactDir is where artifact revision files live (ADR 0015). Empty
	// means an `artifacts` directory beside the database.
	ArtifactDir string

	// D2Bin is the d2 command used to draw D2 artifacts on the server. Empty means look for `d2` on
	// PATH; if there is none, D2 artifacts are stored and shown as source.
	D2Bin string

	// MCPServer is the SDK server the mcp module publishes; the tools register
	// on it. Required.
	MCPServer *gmcpserver.Server

	// Authorizer decides, per tool call, whether the caller holds the tool's
	// scope. Required: with none every call is refused.
	Authorizer mcp.Authorizer

	// Callers names which keys are gateway producers and which of those may
	// attest the session they forward for.
	Callers mcp.Callers

	// Verifier re-checks a bearer token (identity's Verify). Browser sessions
	// are revalidated through it for as long as a room stays open. Required.
	Verifier participant.Verifier

	// HITLBackend is the value of TANGENT_HITL_BACKEND (gohitl.EnvBackend):
	// "gohitl" runs the go-hitl Service over Tangent's tables behind the
	// tangent_hitl_* tools (CW-20261016-0153); empty or "native" keeps
	// Tangent's own service. Off by default; an unknown value fails Boot.
	HITLBackend string

	// RoomURLBase is the origin the room URLs the tools hand back are built on.
	RoomURLBase string

	// ManagedResource names the managed-runtime resource that owns this
	// process's lifecycle, when one does. Health reports use it to
	// recommend a concrete restart command. Empty is normal and supported.
	ManagedResource string

	// OTel opts this process into the OpenTelemetry bridge. See
	// internal/telemetry/otel.go for why this is off by default.
	OTel bool

	// PluginDir is the installed-plugin root. Empty resolves through
	// pluginpkg.DefaultRoot, which honors TANGENT_PLUGIN_DIR and otherwise
	// uses ~/.tangent/plugins. It is a field so a test can point a boot at a
	// directory it owns rather than at the operator's real plugins.
	PluginDir string
	// ConfigScopes are host-selected client/environment/project overlays. The
	// default client scope is local; browser/plugin requests cannot add scopes.
	ConfigScopes []pluginconfig.Scope
	// ConfigSecrets allows an embedder to supply its genuine keychain service.
	// Tests use synthetic owned backends; production defaults to OSKeychain.
	ConfigSecrets pluginconfig.Secrets

	// DisablePluginHealthGate turns off the health gate: normally, a
	// subprocess plugin's cached plugin/health verdict can refuse a caller's
	// tool or route call (CW-20260911-0069). Named for the opt-out rather than
	// the feature so the zero value — what any caller gets that never sets
	// this field, including cmd/tangent-app and every test boot — is the safe
	// default: gate ON. An operator turns it off on purpose; nothing turns it
	// off by omission.
	DisablePluginHealthGate bool

	// ListLegacyToolNames makes tools/list also include the deprecated
	// tangent.<name> aliases of the renamed MCP tools. Off by default: the
	// aliases stay callable either way, but listing a name outside [a-z0-9_]
	// breaks strict clients. See docs/mcp-tool-names.md.
	ListLegacyToolNames bool

	// Logger receives every constructed service's structured logs. Nil
	// defaults to slog.Default(). Boot does not call slog.SetDefault —
	// that is a process-global side effect the caller owns.
	Logger *slog.Logger

	// EventsDisabled is the kill switch for the typed event stream
	// (TANGENT_EVENTS_DISABLED=1, resolved by the caller). The durable journal,
	// relay, retention job and typed SSE frames are not started, so the legacy
	// revision pings are all a browser sees. Approval lifecycle transitions
	// still write their outbox rows (the observer is part of the transition
	// transaction), so turning the stream back on drains whatever accrued.
	EventsDisabled bool

	// ReplyMailSender overrides the Tether sender built from TANGENT_TETHER_*
	// (tests only).
	ReplyMailSender replymail.Sender

	// eventsRelayTick overrides the relay's polling interval; tests only.
	eventsRelayTick time.Duration
	// eventsNoCommitHook leaves the relay to its tick alone; tests only, to
	// prove delivery never depends on the post-commit wake.
	eventsNoCommitHook bool
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
	Docs        *docs.Service
	Artifacts   *artifacts.Service

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

// ownedCloser stops what Boot started, before the host closes the database.
type ownedCloser struct {
	// plugins is torn down before the configuration/keychain reference store.
	// Unload fences owners, withdraws registrations and joins subprocess work.
	//
	// First for the same reason the loopback closes before the database: a
	// plugin handler that nothing bounded is exactly what CW-20260909-0045
	// measured graceful shutdown waiting on, and a process still holding the
	// database when its successor boots is the cost.
	plugins *pluginhost.Host
	config  *pluginconfig.Store
	// loopback is the plugin host's in-process MCP session. It is closed
	// before the database because it can still be serving a plugin's tool
	// call, and it holds the SDK's reader goroutines — a process that forgot
	// it would leak them silently, which is exactly the class internal/mcp's
	// goleak tests exist to catch.
	loopback io.Closer
	// stopSweeper ends the HITL expiry sweeper, which reads the database.
	stopSweeper context.CancelFunc
	// stopEvents ends the event relay and retention job and waits for them;
	// both use the database.
	stopEvents func()
	// stopReplyMail ends the reply-mail worker; nil when it never started.
	stopReplyMail func()
}

func (c *ownedCloser) Close() error {
	var errs []error
	if c.stopSweeper != nil {
		c.stopSweeper()
	}
	if c.stopReplyMail != nil {
		c.stopReplyMail()
	}
	if c.stopEvents != nil {
		c.stopEvents()
	}
	if c.plugins != nil {
		if err := c.plugins.UnloadAll(); err != nil {
			errs = append(errs, fmt.Errorf("unload plugins: %w", err))
		}
	}
	if c.config != nil {
		if err := c.config.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close plugin config: %w", err))
		}
	}
	if c.loopback != nil {
		if err := c.loopback.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close plugin tool caller: %w", err))
		}
	}
	return errors.Join(errs...)
}

// Boot constructs every service over cfg.DB, registers the tools on
// cfg.MCPServer, and returns the route table for the host to serve.
//
// On error, Boot has already stopped anything it started; the returned
// io.Closer is nil in that case, not partially valid.
func Boot(cfg Config) (*Services, *server.Table, io.Closer, error) {
	if cfg.DB == nil || cfg.MCPServer == nil || cfg.Verifier == nil {
		return nil, nil, nil, errors.New("boot: DB, MCPServer and Verifier are required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	sqlDB := cfg.DB
	closer := &ownedCloser{}
	release := func(err error) (*Services, *server.Table, io.Closer, error) {
		_ = closer.Close()
		return nil, nil, nil, err
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
	scopes := cfg.ConfigScopes
	if len(scopes) == 0 {
		scopes = []pluginconfig.Scope{{Kind: "client", ID: "local"}}
	}
	secrets := cfg.ConfigSecrets
	if secrets == nil {
		secrets = pluginconfig.OSKeychain{}
	}
	configStore, configErr := pluginconfig.Open(context.Background(), filepath.Join(pluginRoot, ".state", "config"), secrets, scopes)
	if configErr != nil {
		return release(fmt.Errorf("plugin config: %w", configErr))
	}
	closer.config = configStore
	pluginHost, pluginErr := plugins.LoadInstalled(
		context.Background(), logger, envSvc, pluginRoot, cfg.DisablePluginHealthGate, plugins.WithConfigStore(configStore))
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

	// Dispatcher is shared across transports. The MCP triage handler below
	// bridges it to a WebSocket-connected room.
	dispatcher := envelope.NewDispatcher(envSvc)
	// One store, shared: it persists the interaction records and, through
	// interaction.WithRetainedMaterialStore, the exact definition material
	// each record pins. Those have to be the same database — a pin whose
	// material lives somewhere the record's own transaction cannot see is
	// a pin that can go missing.
	interactionStore := interaction.NewStore(sqlDB)
	// Approval lifecycle transitions write their typed-event outbox row in the
	// same transaction as the journal row (CW-20261016-0143).
	interactionStore.SetTransitionObserver(hitl.EventObserver{})
	definitionCatalog := interaction.NewEnvelopeDefinitionCatalog(envSvc, mcp.HostVersion,
		interaction.WithRetainedMaterialStore(interactionStore))
	interactionService, err := interaction.NewService(
		interactionStore,
		definitionCatalog,
		interaction.WithSurfaceAccessPolicy(reservedSurfacePolicy()),
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
		interaction.WithPrivilegedActorPolicy(hitl.ExpiryPolicy{
			Next: interaction.NewEffectPrivilegedActorPolicy(hostAuthority),
		}),
		interaction.WithTelemetry(recorder),
	)
	if err != nil {
		return release(fmt.Errorf("build interaction service: %w", err))
	}
	hitlService, err := hitl.NewService(interactionService)
	if err != nil {
		return release(fmt.Errorf("build hitl service: %w", err))
	}
	sweepCtx, stopSweeper := context.WithCancel(context.Background())
	closer.stopSweeper = stopSweeper
	go hitlService.RunExpirySweeper(sweepCtx, time.Second, func(sweepErr error) {
		logger.Warn("hitl expiry sweep failed", "error", sweepErr)
	})
	hitlEvents, stopEvents, err := startEventStream(cfg, logger, sqlDB, interactionStore)
	if err != nil {
		return release(err)
	}
	closer.stopEvents = stopEvents
	closer.stopReplyMail = startReplyMail(cfg, logger, interactionStore)
	turnsService, err := turns.NewService(interactionService)
	if err != nil {
		return release(fmt.Errorf("build turns service: %w", err))
	}
	docsService, err := docs.NewService(interactionService, docs.NewReadStore(sqlDB))
	if err != nil {
		return release(fmt.Errorf("build docs service: %w", err))
	}
	artifactDir, err := resolveArtifactDir(cfg)
	if err != nil {
		return release(err)
	}
	var artifactOptions []artifacts.Option
	if d2 := artifacts.NewD2Renderer(cfg.D2Bin); d2 != nil {
		artifactOptions = append(artifactOptions, artifacts.WithRenderer("d2", d2))
		logger.Info("d2 renderer enabled", "bin", d2.Bin())
	} else {
		logger.Info("d2 renderer not available; D2 artifacts will show their source", "configured", cfg.D2Bin)
	}
	artifactsService, err := artifacts.NewService(interactionService, artifacts.NewStore(sqlDB, artifactDir), docs.NewReadStore(sqlDB), artifactOptions...)
	if err != nil {
		return release(fmt.Errorf("build artifacts service: %w", err))
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

	// The browser participant is the signed-in user (ADR 0004 §4, now
	// identity's): a room's authority rides on the user session, and a link
	// carries none. The /ws upgrade resolves it before the upgrade, so a
	// refusal is an ordinary 403.
	participantGate := participant.NewGate(cfg.Verifier)
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
		health.WithRendererHost(server.RendererHostProbe()),
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
		health.WithPlugins(func(ctx context.Context) health.PluginInventory {
			return pluginInventory(pluginHost.Inventory(ctx))
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
	roomURLBase := cfg.RoomURLBase
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
	// send/read path over the same two stores, never the tangent_relay_*
	// MCP surface. hitlService correlates HITL items to the channel's
	// agent by reading its already-public Inbox(), with no change to
	// internal/hitl's own contract.
	channelPaneService, err := channelpane.New(channelStore, relayStore, hitlService)
	if err != nil {
		return release(fmt.Errorf("construct channel pane service: %w", err))
	}

	// tangent_retention_get reads through this reporter so the MCP
	// transport holds no database handle (.golangci.transport.yml).
	retentionReporter, err := retention.NewReporter(sqlDB, cfg.DBPath)
	if err != nil {
		return release(fmt.Errorf("construct retention reporter: %w", err))
	}

	mcpOptions := []mcp.Option{
		mcp.WithServer(cfg.MCPServer),
		mcp.WithAuthorizer(cfg.Authorizer),
		mcp.WithCallers(cfg.Callers),
		mcp.WithInteractionService(interactionService),
		mcp.WithHITLService(hitlService),
		mcp.WithDocsService(docsService),
		mcp.WithArtifactsService(artifactsService),
		mcp.WithTurnsService(turnsService),
		mcp.WithInteractionPackages(interactionPackages),
		mcp.WithHealthReporter(healthReporter),
		mcp.WithTelemetry(recorder, telemetryStore),
		// tangent_retention_get. Read-only by design: the retention
		// operations are CLI commands, because erasure authority belongs
		// to the local user and MCP has no authenticated caller identity
		// to hold it.
		mcp.WithRetentionReporter(retentionReporter),
		// tangent_relay_*: the cooperative MCP inbox.
		mcp.WithRelay(channelStore, relayStore),
	}
	// reply_to is required unless an operator opts out for a rollout window
	// (TANGENT_REPLY_TO_OPTIONAL=1): agents need their prompts updated first.
	if os.Getenv("TANGENT_REPLY_TO_OPTIONAL") != "1" {
		mcpOptions = append(mcpOptions, mcp.WithReplyToRequired())
	}
	useGoHITL, backendErr := gohitl.Enabled(cfg.HITLBackend)
	if backendErr != nil {
		return release(backendErr)
	}
	if useGoHITL {
		// The go-hitl Service over Tangent's own tables behind tangent_hitl_*
		// (CW-20261016-0153). The operator's inbox and Resolve are unchanged.
		mcpOptions = append(mcpOptions, mcp.WithHITLWire(gohitl.New(sqlDB, definitionCatalog)))
		logger.Info("hitl: go-hitl backend enabled behind tangent_hitl_*", "env", gohitl.EnvBackend)
	}
	if cfg.ListLegacyToolNames {
		mcpOptions = append(mcpOptions, mcp.WithLegacyToolNamesListed())
	}
	mcpSrv, err := mcp.New(envSvc, dispatcher, roomMgr, roomURLBase, mcpOptions...)
	if err != nil {
		// MCP construction failure is fatal: the binary advertises an MCP
		// surface as part of its v0.1 contract, so booting without it
		// would silently strip a documented capability.
		return release(fmt.Errorf("build mcp server: %w", err))
	}
	if attachErr := pluginHost.AttachToolRegistry(mcpSrv); attachErr != nil {
		return release(fmt.Errorf("attach plugin tools: %w", attachErr))
	}
	// The plugin host's tool caller (CW-20260910-0031). A plugin drives Tangent
	// by calling the same tools an agent calls, in process, with the same
	// host-assigned caller identity — see internal/mcp/loopback.go for why it
	// goes over a real session rather than straight to a handler.
	//
	// It is attached here, after mcp.New, because that is the first moment the
	// tool surface exists; a plugin resolves it at dispatch time, so a plugin
	// that loaded earlier is not holding a nil.
	//
	// It acts as the plugin host: a service principal with no name (so the
	// unlabeled caller partition it always had) and the scopes a plugin may
	// use, which exclude the decision scope.
	loopback, err := mcpSrv.NewLoopbackCaller(libauth.WithPrincipal(context.Background(), access.PluginHostPrincipal()))
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
		{"dashboard", mcp.RegisterDashboardOnDispatcher},
		{"app-board", mcp.RegisterAppBoardOnDispatcher},
		{"external-review", mcp.RegisterExternalReviewOnDispatcher},
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

	table, err := server.New(server.Config{
		Logger:             logger,
		Envelope:           envSvc,
		WSHandler:          wsHandler,
		HITL:               hitlService,
		Turns:              turnsService,
		Docs:               docsService,
		Artifacts:          artifactsService,
		Rooms:              mcpSrv,
		Inbox:              interactionService,
		Channels:           channelPaneService,
		Participants:       participantGate,
		Effects:            effectBroker,
		EffectContext:      interactionService,
		Health:             healthReporter,
		Telemetry:          recorder,
		PluginHost:         pluginHost,
		PluginConfig:       configStore,
		PluginHostInstance: pluginHost.HostInstance(),
		HITLEvents:         hitlEvents,
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
		Docs:            docsService,
		Artifacts:       artifactsService,
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
	return services, table, closer, nil
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
			ID:              record.ID,
			Name:            record.Name,
			Version:         record.Version,
			Loaded:          record.Loaded,
			Enabled:         record.Enabled,
			State:           record.State,
			At:              record.At,
			Error:           record.Error,
			FailedAfterLoad: record.FailedAfterLoad,
			RuntimeError:    record.RuntimeError,
			Restarts:        record.Restarts,
			Exhausted:       record.Exhausted,
			Healthy:         record.Healthy,
			HealthMessage:   record.HealthMessage,
			HealthCheckedAt: record.HealthCheckedAt,
		})
	}
	return inventory
}

// reservedSurfacePolicy is every host-reserved operator surface, in one place.
//
// It refuses generic MCP callers write access to these surfaces, so only the host's own enqueue
// tools can put an item there. It no longer decides what the operator's browser lists: every item
// from the verified gateway is the operator's (interaction.Service.BrowserInbox, CW-20261025-0002).
// A new reserved surface must still be added here so callers cannot write to it;
// TestEveryReservedOperatorSurfaceIsCovered keeps the list honest.
func reservedSurfacePolicy() compoundSurfaceAccessPolicy {
	return compoundSurfaceAccessPolicy{
		hitl.SurfaceAccessPolicy{},
		turns.SurfaceAccessPolicy{},
		docs.SurfaceAccessPolicy{},
		artifacts.SurfaceAccessPolicy{},
	}
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

// AuthorizeInboxRead forwards only the pure read policy. It cannot satisfy
// any existing lifecycle capability gate. Each constituent policy must allow.
func (c compoundSurfaceAccessPolicy) AuthorizeInboxRead(surfaceID string) bool {
	for _, policy := range c {
		if reader, ok := policy.(interaction.InboxReadSurfacePolicy); ok {
			if !reader.AuthorizeInboxRead(surfaceID) {
				return false
			}
		} else if !policy.Authorize(surfaceID, "") {
			return false
		}
	}
	return true
}

// resolveArtifactDir returns the directory artifact revision files live in:
// the configured one, else an `artifacts` directory beside the database.
func resolveArtifactDir(cfg Config) (string, error) {
	if cfg.ArtifactDir != "" {
		return cfg.ArtifactDir, nil
	}
	if cfg.DBPath != "" {
		return filepath.Join(filepath.Dir(cfg.DBPath), "artifacts"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir for artifact directory: %w", err)
	}
	return filepath.Join(home, ".tangent", "artifacts"), nil
}
