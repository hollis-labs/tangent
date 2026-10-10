// Package pluginhost is Tangent's implementation of the portfolio plugin
// framework's Host contract (github.com/hollis-labs/libs/plugin-mcp/plugin-sdk), narrowed to
// the registration surfaces Tangent actually honors.
//
// # The rule
//
// ADR 0007 §4 states it as one sentence, and everything in this package is a
// consequence of it:
//
//	The SDK says what a plugin may offer. The ADR 0003 manifest says what the
//	host will let it do. A registration without a manifest is refused.
//
// So RegisterUIComponent for an envelope component does not accept a
// description of a kind. It accepts a *name*, resolves the manifest this host
// ships for that name, and refuses when there is none — the same posture
// EnvelopeRouter already takes toward a kind no manifest classifies. A plugin
// cannot hand over manifest bytes of its own, because a manifest a plugin
// authored would be a plugin deciding its own trust class.
//
// # The three surfaces this host honors
//
// RegisterUIComponent (this file) contributes an interaction kind.
// RegisterMCPTool (mcp.go) contributes an agent-callable tool.
// RegisterHTTPRoute (http.go) contributes a browser route, so a button in a
// room can do work without spending an agent turn.
//
// The last two are Tangent extensions to the SDK's base Host contract, which
// the SDK itself names as the intended path — it carries no tool or route
// registration method, and says host applications may add their own. Each one
// refuses by name rather than accommodating: a plugin cannot shadow a tool, sit
// outside the tool namespace, mount outside the reserved route prefix, or gate
// a route on a capability no participant can hold. The reasoning for each lives
// beside the code that enforces it.
//
// # What a plugin cannot author
//
// Trust class, granted capabilities, effective assurance, and asset digest are
// reserved to the host by ADR 0003, and a plugin-supplied UIComponent is not a
// second way to claim them. `Props` is the only free-form field on the SDK's
// UIComponent, so reservedProps below refuses the keys that would be an attempt
// — loudly, rather than by ignoring them, because a plugin that believes it
// raised its own trust class and was silently downgraded is worse than one that
// failed to load.
//
// # Deliberately not implemented
//
// Each of these is a decision rather than an omission. They fall into two
// groups with different lifetimes, and conflating them is what dated ADR 0007
// §4 twice: a SURFACE this host refuses to grow is a boundary statement (§4),
// while a MODE this host does not run yet is a question of the plugin model
// (ADR 0008) and expected to change.
//
// Surfaces, refused (ADR 0007 §4):
//
//   - RegisterCRUDHandler. The agent is the owning application's client and
//     applies every write itself, so nothing needs a CRUD surface in Tangent's
//     process. ADR 0007's risk section names this exact instance: implementing
//     a surface because the SDK offers it rather than because a consumer needs
//     it is how the boundary rots.
//   - Global configuration methods lack a calling owner and remain refused.
//     CW-20261003-0063 supports reviewed settings on exact owner-scoped handles.
//
// Lifecycle enable/disable and reload are host-owned controls, not SDK config.
//
// Not yet, per the model (ADR 0008) — these are the ones expected to move:
//
// Installed plugins already run through the shared protocol-2 subprocess driver.
//   - Runtime asset loading. This host ships every renderer in ui_dist. ADR 0008
//     §3 adopts Nanite's model, in which the browser dynamic-imports a plugin's
//     own bundle, so this is a not-yet rather than a refusal — ADR 0007 §4 stated
//     it as an exclusion and that was an agent's framing, reversed. Its open
//     question 1 is the real constraint: core-trusted requires an empty
//     asset_digest, and a separately served bundle has one.
//   - Signature verification. Out of scope by Chrispian's direction,
//     2026-09-09, unchanged. trust.assurance is unchanged and nothing here
//     relaxes it. First-party only.
//
// Every unimplemented surface returns ErrSurfaceNotHonored rather than nil. A
// registration that silently succeeds and does nothing is the failure mode
// worth spending an error on.
package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	driver "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
	plugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/pluginconfig"
)

// Errors this host returns. They are distinguishable because the tests that
// hold the boundary need to assert *why* a registration was refused, not just
// that it was.
var (
	// ErrSurfaceNotHonored reports a registration surface the SDK offers and
	// this host deliberately does not implement.
	ErrSurfaceNotHonored = errors.New("pluginhost: registration surface not honored by this host")
	// ErrNoManifest reports an envelope component naming a kind this host
	// ships no ADR 0003 manifest for.
	ErrNoManifest = errors.New("pluginhost: envelope component has no interaction definition manifest")
	// ErrReservedProperty reports a component trying to author something ADR
	// 0003 reserves to the host.
	ErrReservedProperty = errors.New("pluginhost: component may not author a host-reserved property")
	// ErrInvalidComponent reports a structurally unusable UIComponent.
	ErrInvalidComponent = errors.New("pluginhost: invalid UI component")
	// ErrDuplicatePlugin reports a plugin id already loaded on this host.
	ErrDuplicatePlugin = errors.New("pluginhost: plugin already loaded")
)

// reservedProps are the UIComponent.Props keys a plugin may not set. Each names
// something ADR 0003 §2 reserves to the host: the trust class and the granted
// capabilities decide isolation and the capability ceiling, the assurance
// decides whether the class is grantable at all, and the digests are the
// content-addressed identity every downstream check compares against.
//
// The list is deliberately generous about spelling. A plugin author reaching
// for any of these has misunderstood the boundary, and the useful response is
// an error naming the field rather than a near-miss that slips through.
var reservedProps = []string{
	"trust_class", "trustClass",
	"granted_capabilities", "grantedCapabilities",
	"required_capabilities", "requiredCapabilities",
	"assurance", "trust",
	"asset_digest", "assetDigest",
	"manifest_digest", "manifestDigest",
	"contract_digest", "contractDigest",
	"binding_digest", "bindingDigest",
	"isolation",
	"ownership_class", "ownershipClass",
	"draft_custody", "draftCustody",
	"retention_class", "retentionClass",
}

// Host implements plugin.Host for Tangent. One Host serves the process; it is
// created at boot with the envelope service the resolved kinds register on.
type Host struct {
	hostInstance          string
	toolHostInstance      string
	generations           driver.MemoryGenerationStore
	capabilityGenerations driver.MemoryGenerationStore
	ctx                   context.Context
	logger                *slog.Logger
	envSvc                *envelope.Service

	mu           sync.Mutex
	ops          operationGate
	owners       map[string]*registrationOwner
	toolOwners   map[string]*registrationOwner
	routeOwners  map[string]*registrationOwner
	kindOwners   map[string]*registrationOwner
	toolRegistry ToolRegistry
	factories    map[string]func() (plugin.Plugin, error)
	intent       map[string]bool
	intentPath   string
	configStore  *pluginconfig.Store
	configApply  *configApplication
	faults       map[string]string
	// loaded is every plugin this host has loaded, by id.
	loaded map[string]plugin.Plugin
	// attempts is every Load call in the order it was made, refusals
	// included. It is what makes "which plugins loaded, which refused and
	// why" answerable (CW-20260910-0036) instead of inferred from a tool
	// list. See lifecycle.go.
	attempts []*loadAttempt
	// release is closed by UnloadAll. Every in-flight plugin dispatch selects
	// on it, so shutdown does not wait out a hung handler's budget. See
	// isolation.go.
	release chan struct{}
	// unloaded records that UnloadAll has run, so a second call is a no-op
	// rather than a second close of release.
	unloaded bool
	// budget is what one plugin dispatch is bounded to. New sets it to
	// dispatchBudget and production never changes it; it is a field so a test
	// can shrink it to something a test can wait for.
	budget time.Duration
	// kinds maps a contributed envelope kind to the NAME OF THE COMPONENT that
	// claimed it. kindOwners separately records the exact scoped load owner. It
	// exists so a second claim on the same kind is refused here, with a
	// readable message, rather than racing the registry's duplicate check.
	kinds map[string]string
	// tools is every plugin-contributed MCP tool, by wire name. See mcp.go —
	// the host records them and internal/mcp installs them, because plugins
	// load before the MCP server exists.
	tools map[string]MCPTool
	// routes is every plugin-contributed HTTP route, by ServeMux pattern. See
	// http.go, and the same two-step reason.
	routes map[string]HTTPRoute
	// toolCaller is the host's own MCP tool surface, which is how a plugin
	// drives Tangent. Nil until the composition root attaches it; see tools.go.
	toolCaller       ToolCaller
	toolCapabilities *toolCapabilityBoundary
	// contributeKind is the ADR 0007 §4 door: it resolves a kind's manifest and
	// installs it, or refuses. New sets it to extensions.RegisterContributedKind
	// and production never changes it.
	//
	// It is a field for the same reason budget is, and for one more. Since
	// CW-20260911-0036 this host ships NO plugin-contributed kind —
	// `tangent.app-board` was never one, and RegisterAll owns it now — so the
	// door's success path has no shipped input to test it with. A test supplies
	// a fixture kind through this field and the path stays held. The refusals
	// in RegisterUIComponent all fire before the door is reached, so they are
	// tested against the real one.
	contributeKind func(*envelope.Service, string) error
}

// New builds a Host over the envelope service that plugin-contributed kinds
// register on. envSvc is required: a host that cannot register a kind cannot
// honor the one surface it implements, and discovering that at the first
// registration rather than at boot helps nobody.
func New(ctx context.Context, logger *slog.Logger, envSvc *envelope.Service) (*Host, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}
	if envSvc == nil {
		return nil, fmt.Errorf("pluginhost: envelope service is required")
	}
	epoch, err := driver.NewHostInstance()
	if err != nil {
		return nil, fmt.Errorf("pluginhost: host epoch: %w", err)
	}
	toolEpoch, err := driver.NewHostInstance()
	if err != nil {
		return nil, fmt.Errorf("pluginhost: native tool epoch: %w", err)
	}
	return &Host{
		hostInstance:     epoch,
		toolHostInstance: toolEpoch,
		ops:              make(operationGate, 1),
		ctx:              ctx,
		logger:           logger,
		envSvc:           envSvc,
		loaded:           map[string]plugin.Plugin{},
		owners:           map[string]*registrationOwner{},
		toolOwners:       map[string]*registrationOwner{},
		routeOwners:      map[string]*registrationOwner{},
		kindOwners:       map[string]*registrationOwner{},
		factories:        map[string]func() (plugin.Plugin, error){},
		intent:           map[string]bool{}, faults: map[string]string{},
		kinds:   map[string]string{},
		tools:   map[string]MCPTool{},
		routes:  map[string]HTTPRoute{},
		release: make(chan struct{}),
		budget:  dispatchBudget,

		contributeKind: extensions.RegisterContributedKind,
	}, nil
}

// HostInstance is the immutable epoch shared by this process's plugin controllers.
func (h *Host) HostInstance() string { return h.hostInstance }

// Load loads one plugin adapter: it records the plugin, then calls Load so
// the plugin can register what it offers. A registration failure fails the
// load — a half-registered plugin is not a state this host keeps.
//
// Every call is recorded in attempt order, refusals included, so Inventory can
// answer "which loaded, which refused and why" from the host's own bookkeeping
// rather than from a plugin's self-report.
//
// A panic inside the plugin's own Load or identity methods is recovered and
// returned as an error. The boot still fails — LoadShipped has no partial mode
// — but it fails naming the plugin instead of crashing the process, which is
// the difference between a line to read and a stack to decipher.
//
// ChildPlugin implements this same contract using the shared subprocess lifecycle;
// registration and dispatch policy remain here.
func (h *Host) Load(p plugin.Plugin) error {
	h.ops.Lock()
	defer h.ops.Unlock()
	return h.load(p)
}

func (h *Host) load(p plugin.Plugin) (err error) {
	if p == nil {
		return fmt.Errorf("pluginhost: plugin is nil")
	}
	var id string
	var owned bool
	var owner *registrationOwner
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("pluginhost: load %s: panicked: %v", identify(id), recovered)
		}
		if err != nil {
			if owner != nil {
				err = &pluginDiagnosticError{cause: err, text: scrubConfigText(err.Error(), owner.config.Secrets)}
			}
			// The plugin itself, not nil: a refused attempt should still carry
			// the name and version it managed to report, and safeString keeps a
			// plugin that panics describing itself from taking the record with
			// it. Inventory never reads a refused attempt's Status.
			if owned {
				_ = h.unload(id)
			}
			h.recordAttempt(p, id, err)
		}
	}()

	id = p.ID()
	if id == "" {
		return fmt.Errorf("pluginhost: plugin has no id")
	}

	h.mu.Lock()
	if _, exists := h.loaded[id]; exists {
		h.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrDuplicatePlugin, id)
	}
	// Dependencies are resolved against what is already loaded. Load order is
	// the caller's to choose; this only refuses a plugin whose stated
	// dependency is absent, which is cheaper to debug at boot than at use.
	for _, dep := range p.Dependencies() {
		if _, ok := h.loaded[dep]; !ok {
			h.mu.Unlock()
			return fmt.Errorf("pluginhost: %s depends on %s, which is not loaded", id, dep)
		}
	}
	if h.unloaded {
		h.mu.Unlock()
		return ErrHostShuttingDown
	}
	owner = newRegistrationOwner(h, id)
	h.owners[id] = owner
	delete(h.faults, id)
	if _, known := h.intent[id]; !known {
		h.intent[id] = true
	}
	h.loaded[id] = p
	owned = true
	h.mu.Unlock()

	if err = h.prepareOwnerConfig(owner); err != nil {
		return err
	}
	if err = h.prepareToolCapabilityOwner(owner); err != nil {
		return err
	}
	if loadErr := p.Load(&ownedHost{Host: h, owner: owner}); loadErr != nil {
		return fmt.Errorf("pluginhost: load %s: %w", id, loadErr)
	}
	h.recordAttempt(p, id, nil)
	h.markOwnerConfig(p, owner)
	h.logger.Info("pluginhost: plugin loaded", "plugin", id, "version", scrubConfigText(p.Version(), owner.config.Secrets))
	return nil
}

// RegisterUIComponent is the registration surface for an interaction kind, and
// the only one of the three that the SDK's base Host contract declares.
//
// For an envelope component, component.ID is the kind's wire name. The host
// resolves the ADR 0003 manifest this host ships for that name and refuses the
// registration when there is none.
//
// Every other component type is refused. `widget`, `action`, `workflow` and
// `view` describe host surfaces Tangent does not have, and accepting them to be
// accommodating would mean four registration points that record something
// nothing ever reads.
func (h *Host) RegisterUIComponent(component plugin.UIComponent) error {
	return h.registerUIComponent(component, nil)
}
func (h *Host) registerUIComponent(component plugin.UIComponent, owner *registrationOwner) error {
	if component.Type != plugin.UIComponentTypeEnvelope {
		return fmt.Errorf(
			"%w: UI component type %q (this host registers %q only)",
			ErrSurfaceNotHonored, component.Type, plugin.UIComponentTypeEnvelope)
	}
	kind := component.ID
	if kind == "" {
		return fmt.Errorf("%w: envelope component has no id naming its kind", ErrInvalidComponent)
	}
	// Compiled-in only, but a Handler would be a server-rendered surface, and
	// Tangent renders envelopes in its own React tree from a manifest-declared
	// entry. Accepting one would create a second rendering path that no trust
	// class describes.
	if component.Handler != nil {
		return fmt.Errorf(
			"%w: %s supplies an http.Handler; Tangent renders envelopes from the "+
				"manifest's renderer.entry, not from a plugin-served handler",
			ErrSurfaceNotHonored, kind)
	}
	for _, reserved := range reservedProps {
		if _, present := component.Props[reserved]; present {
			return fmt.Errorf(
				"%w: %s sets %q; ADR 0003 reserves it to the host and the manifest is where it is decided",
				ErrReservedProperty, kind, reserved)
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if owner != nil && !h.currentOwnerLocked(owner) {
		return ErrPluginNotLoaded
	}
	if component, claimed := h.kinds[kind]; claimed {
		// The component label describes the collision; the scoped handle owns it.
		return fmt.Errorf(
			"pluginhost: %s is already contributed by component %q", kind, component)
	}

	// The manifest requirement, and the only place it is enforced.
	if err := h.contributeKind(h.envSvc, kind); err != nil {
		if errors.Is(err, extensions.ErrNotContributable) {
			return fmt.Errorf(
				"%w: %s (a kind is contributable only when this host ships its manifest under "+
					"internal/envelope/extensions/packages/)", ErrNoManifest, kind)
		}
		return fmt.Errorf("pluginhost: register %s: %w", kind, err)
	}

	h.kinds[kind] = component.Name
	if owner != nil {
		h.kindOwners[kind] = owner
	}
	h.logger.Info("pluginhost: envelope kind contributed", "kind", kind, "component", component.Name)
	return nil
}

// ContributedKinds returns the wire names this host has registered through the
// plugin path, sorted. Boot logs it, and the drift tests read it to check that
// the plugin door installed what the registration table says it should.
func (h *Host) ContributedKinds() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.kinds))
	for kind := range h.kinds {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

// GetPlugin retrieves another loaded plugin by id.
func (h *Host) GetPlugin(id string) (plugin.Plugin, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.loaded[id]
	return p, ok
}

// Logger provides a logger for the plugin.
func (h *Host) Logger() plugin.Logger { return slogAdapter{logger: h.logger} }

// Context returns the host's execution context.
func (h *Host) Context() context.Context { return h.ctx }

// ── Surfaces this host deliberately does not implement ──────────────────────
//
// ADR 0007 §4 requires the exclusions to be recorded rather than left as
// absence. Returning an error is that record, in the one place a plugin author
// will actually encounter it.

// RegisterCRUDHandler is not implemented. The agent is the owning application's
// client (ADR 0007 §6): it supplies the records and applies every write through
// tools it already holds. A CRUD surface here would be the first write path
// originating in Tangent's process, which is the test §6 says this pattern must
// keep passing.
func (h *Host) RegisterCRUDHandler(resourceType string, _ plugin.CRUDHandler) error {
	return fmt.Errorf(
		"%w: RegisterCRUDHandler(%q) — the application's agent is its client; "+
			"no write to an owning application originates in Tangent's process",
		ErrSurfaceNotHonored, resourceType)
}

// RegisterEventHook is not implemented. Tangent has no plugin-visible event bus,
// and inventing one to satisfy the interface would be a host surface decided by
// an SDK method signature.
func (h *Host) RegisterEventHook(eventTypes []string, _ plugin.EventHook) error {
	return fmt.Errorf("%w: RegisterEventHook(%v)", ErrSurfaceNotHonored, eventTypes)
}

// GetService is not implemented. It is an untyped escape hatch into host
// internals; a plugin that could reach the database or the room manager through
// it would make every boundary in this repository advisory.
func (h *Host) GetService(name string) (interface{}, error) {
	return nil, fmt.Errorf("%w: GetService(%q)", ErrSurfaceNotHonored, name)
}

// GetConfig on the global host is refused. Only a current owner-scoped handle
// can access that plugin's reviewed, detached incarnation configuration.
func (h *Host) GetConfig(key string) (string, error) {
	return "", fmt.Errorf("%w: GetConfig(%q)", ErrSurfaceNotHonored, key)
}

// SetConfig on the global host has no caller identity and remains refused.
func (h *Host) SetConfig(key string, _ string) error {
	return fmt.Errorf("%w: SetConfig(%q)", ErrSurfaceNotHonored, key)
}

// RegisterConfigSchema on the global host is refused. Scoped declarations must
// agree with the reviewed installed manifest; they cannot create new keys.
func (h *Host) RegisterConfigSchema(_ []plugin.ConfigFieldDef) error {
	return fmt.Errorf("%w: RegisterConfigSchema", ErrSurfaceNotHonored)
}

// RegisterConnector is not implemented. An outbound connector is an egress path
// Tangent does not grant a plugin.
func (h *Host) RegisterConnector(name string, _ plugin.Connector) error {
	return fmt.Errorf("%w: RegisterConnector(%q)", ErrSurfaceNotHonored, name)
}

// RegisterProvider is not implemented. Tangent is not a runtime and does not
// make model calls (ADR 0005 §1, re-adopted by ADR 0007 §1).
func (h *Host) RegisterProvider(name string, _ interface{}) error {
	return fmt.Errorf("%w: RegisterProvider(%q)", ErrSurfaceNotHonored, name)
}

// RegisterCLIAdapter is not implemented. A PTY or subprocess bridge is an agent
// runtime concern, and Tangent does not start, supervise or replace an agent
// process.
func (h *Host) RegisterCLIAdapter(name string, _ interface{}) error {
	return fmt.Errorf("%w: RegisterCLIAdapter(%q)", ErrSurfaceNotHonored, name)
}

// slogAdapter presents Tangent's *slog.Logger as the SDK's Logger.
type slogAdapter struct{ logger *slog.Logger }

func (a slogAdapter) Debug(msg string, kv ...interface{}) { a.logger.Debug(msg, kv...) }
func (a slogAdapter) Info(msg string, kv ...interface{})  { a.logger.Info(msg, kv...) }
func (a slogAdapter) Warn(msg string, kv ...interface{})  { a.logger.Warn(msg, kv...) }
func (a slogAdapter) Error(msg string, kv ...interface{}) { a.logger.Error(msg, kv...) }
func (a slogAdapter) With(kv ...interface{}) plugin.Logger {
	return slogAdapter{logger: a.logger.With(kv...)}
}

// Compile-time proof that this type is the SDK's Host. Without it the
// interface could drift away from the implementation and nothing would notice
// until a plugin was handed a host it could not use.
var _ plugin.Host = (*Host)(nil)
