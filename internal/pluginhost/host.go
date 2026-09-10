// Package pluginhost is Tangent's implementation of the portfolio plugin
// framework's Host contract (github.com/hollis-labs/plugin-sdk), narrowed to
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
// Each of these is recorded as excluded in ADR 0007 §4, so absence here is a
// decision rather than an omission:
//
//   - RegisterCRUDHandler. The agent is the owning application's client and
//     applies every write itself, so nothing needs a CRUD surface in Tangent's
//     process. ADR 0007's risk section names this exact instance: implementing
//     a surface because the SDK offers it rather than because a consumer needs
//     it is how the boundary rots.
//   - Subprocess plugins. The SDK supports them; this host does not spawn them.
//     Compiled-in, first-party plugins only.
//   - Signature verification. Out of scope by Chrispian's direction,
//     2026-09-09. trust.assurance is unchanged and nothing here relaxes it.
//   - Runtime asset loading. A plugin ships no renderer bundle into the
//     browser; the React renderer is compiled into ui_dist with the release,
//     which is what core-trusted's empty-asset_digest requirement assumes.
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

	plugin "github.com/hollis-labs/plugin-sdk"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
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
	ctx    context.Context
	logger *slog.Logger
	envSvc *envelope.Service

	mu sync.Mutex
	// loaded is every plugin this host has loaded, by id.
	loaded map[string]plugin.Plugin
	// kinds maps a contributed envelope kind to the NAME OF THE COMPONENT that
	// claimed it — not to a plugin id, which this host cannot know because
	// plugin_sdk.Host passes no caller identity to a registration call. It
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
	toolCaller ToolCaller
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
	return &Host{
		ctx:    ctx,
		logger: logger,
		envSvc: envSvc,
		loaded: map[string]plugin.Plugin{},
		kinds:  map[string]string{},
		tools:  map[string]MCPTool{},
		routes: map[string]HTTPRoute{},
	}, nil
}

// Load loads one compiled-in plugin: it records the plugin, then calls Load so
// the plugin can register what it offers. A registration failure fails the
// load — a half-registered plugin is not a state this host keeps.
//
// There is no subprocess path here, and adding one is a separate decision with
// its own isolation questions (ADR 0007 §4).
func (h *Host) Load(p plugin.Plugin) error {
	if p == nil {
		return fmt.Errorf("pluginhost: plugin is nil")
	}
	id := p.ID()
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
	h.loaded[id] = p
	h.mu.Unlock()

	if err := p.Load(h); err != nil {
		// The plugin comes back off the host so GetPlugin cannot hand another
		// plugin a dependency that never finished loading.
		//
		// Any kind it DID register before failing stays in the envelope
		// registry: go-envelopes' registry is boot-time and has no removal, and
		// faking a rollback here would report a registry state that is not the
		// one in force. It does not matter in practice because the only caller
		// is LoadShipped, and a plugin that fails to load fails the boot — but
		// a reader deserves the real reason rather than a cleanup loop that
		// looks like it undoes something.
		//
		// Any tool or route it registered stays recorded for a second reason,
		// and it is worth stating rather than leaving to be inferred: this host
		// cannot attribute a registration to a plugin at all (the SDK passes no
		// caller identity), so there is nothing to select for removal. Both
		// maps are drained by a caller that only ever runs after a successful
		// LoadShipped, so an entry from a failed load is never installed.
		h.mu.Lock()
		delete(h.loaded, id)
		h.mu.Unlock()
		return fmt.Errorf("pluginhost: load %s: %w", id, err)
	}
	h.logger.Info("pluginhost: plugin loaded", "plugin", id, "version", p.Version())
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
	if component, claimed := h.kinds[kind]; claimed {
		h.mu.Unlock()
		// Named by component, not by plugin: plugin_sdk.Host carries no caller
		// identity, so this host genuinely does not know which plugin is
		// calling and will not guess in an error message.
		return fmt.Errorf(
			"pluginhost: %s is already contributed by component %q", kind, component)
	}
	h.mu.Unlock()

	// The manifest requirement, and the only place it is enforced.
	if err := extensions.RegisterContributedKind(h.envSvc, kind); err != nil {
		if errors.Is(err, extensions.ErrNotContributable) {
			return fmt.Errorf(
				"%w: %s (a kind is contributable only when this host ships its manifest under "+
					"internal/envelope/extensions/packages/)", ErrNoManifest, kind)
		}
		return fmt.Errorf("pluginhost: register %s: %w", kind, err)
	}

	h.mu.Lock()
	h.kinds[kind] = component.Name
	h.mu.Unlock()
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

// GetConfig is not implemented. Plugin configuration has no owner in Tangent
// yet, and returning an empty string would be indistinguishable from a
// configured empty value.
func (h *Host) GetConfig(key string) (string, error) {
	return "", fmt.Errorf("%w: GetConfig(%q)", ErrSurfaceNotHonored, key)
}

// SetConfig is not implemented, for the same reason as GetConfig.
func (h *Host) SetConfig(key string, _ string) error {
	return fmt.Errorf("%w: SetConfig(%q)", ErrSurfaceNotHonored, key)
}

// RegisterConfigSchema is not implemented. It exists so a frontend can render a
// settings form; Tangent has no plugin settings surface to render one into.
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
