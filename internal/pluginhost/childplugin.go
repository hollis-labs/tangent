package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	plugin "github.com/hollis-labs/plugin-sdk"
	"github.com/hollis-labs/plugin-sdk/subprocess"

	"github.com/hollis-labs/tangent/internal/envelope"
)

// hostVersionForChildren is what a plugin is told this host is at init. It is
// the release version every other surface advertises, aliased here rather than
// re-spelled so a plugin and an MCP client cannot be told different things.
const hostVersionForChildren = envelope.HostVersion

// A subprocess plugin, seen by this host as an ordinary plugin.
//
// # Subprocess mode needs no new host surface, and that is the design
//
// The host already dispatches a contributed tool through
// `subprocess.MCPHandler` and a contributed route through
// `subprocess.HTTPHandler` — the SDK's own interfaces, named for the wire they
// were designed for. A compiled-in plugin satisfies them directly. A
// ChildPlugin satisfies them by forwarding across that wire.
//
// So everything the host already enforces applies unchanged and by
// construction, rather than by a second implementation remembering to: the
// manifest requirement on a contributed kind, the tool-name and route-path
// rules, the ADR 0004 §7 capability check on a route, the reserved-property
// refusals, the panic containment and dispatch budget in isolation.go, and the
// reverse-order unload in lifecycle.go. None of them know a child exists.
//
// ADR 0003's reservations hold at the same door for the same reason. A
// subprocess plugin cannot author its own trust class, capabilities, assurance
// or asset digest, because it does not get to author a UIComponent's Props any
// more than a compiled-in one does — it names a kind and the host resolves the
// manifest.
//
// # What a child may reach
//
// The wire, and nothing else. There is no handle to the database, the room
// manager or the interaction service to pass across a pipe, which makes
// `GetService` staying unimplemented a property of the transport here rather
// than a rule to enforce. A child drives Tangent the way a compiled-in plugin
// does, through `pluginhost.ToolCaller` — an in-process MCP client with the
// authority any local caller has and no more.
//
// # The HTTPHandler asymmetry, recorded because it is a real constraint
//
// `plugin.UIComponent.Handler` is documented compiled-in-only: an
// `http.Handler` cannot cross a pipe. `subprocess.HTTPHandler` can, because it
// is request-in/response-out with no streaming and no hijack. Both shipped
// route users are a plain POST — the Torque and Tesseract Sync buttons — so the
// constraint costs nothing today. A future route that needs to stream is a
// route that needs a different mechanism, and it should be refused rather than
// quietly served by a compiled-in special case.

const (
	// maxChildRestarts bounds automatic recovery from a crash (CW-20260911-0068).
	// Mirrors Nanite's shape (internal/plugin/subprocess/manager.go) rather than
	// inventing a second restart policy: a plugin that crashes on every call is
	// a plugin an unbounded restart loop would spawn forever, so recovery is a
	// bounded number of attempts, not a guarantee.
	maxChildRestarts = 3

	// restartInitialBackoff and restartMaxBackoff bound the delay before each
	// restart attempt, doubling each time up to the ceiling — same shape and
	// same numbers as Nanite's DefaultManagerConfig.
	restartInitialBackoff = 1 * time.Second
	restartMaxBackoff     = 30 * time.Second
	restartBackoffFactor  = 2.0

	// healthProbeCacheTTL coalesces repeated probes within one
	// tangent.health_report call. Readiness and the raw plugin inventory each
	// ask independently (internal/mcp/health_tool.go), and without this a
	// single MCP call would send plugin/health to every child twice.
	healthProbeCacheTTL = 1 * time.Second
)

// ErrPluginUnhealthy reports that a subprocess plugin's last plugin/health
// answer was unhealthy and this host's health gate refused the call rather
// than forwarding it into a plugin already known not to be working.
//
// Distinguishable from ErrChildGone on purpose (CW-20260911-0069): the
// process is alive and answering the wire, it just said it is not fit to
// serve. A caller that only checks for ErrChildGone would otherwise read an
// unhealthy plugin as a working one that returned a plain error.
var ErrPluginUnhealthy = errors.New("pluginhost: subprocess plugin reported unhealthy")

// pluginHealth is what this host currently believes about one child beyond
// "the process exists" — that question is wire.go's ErrChildGone and needs no
// probe. It starts healthy (checked is zero), matching the SDK's own
// healthy-by-default posture for a plugin that never answers plugin/health at
// all: this host has run no probe yet, so there is nothing to contradict
// "healthy" until tangent.health_report asks once.
type pluginHealth struct {
	ok        bool
	message   string
	checked   time.Time
	reachable bool // false when the probe call itself failed rather than the plugin answering {ok:false}
}

// ChildPluginOption configures a ChildPlugin beyond its spec and declared
// surfaces.
type ChildPluginOption func(*ChildPlugin)

// WithHealthGate controls whether a cached unhealthy plugin/health answer
// refuses a caller's tool or route call (ErrPluginUnhealthy) rather than
// forwarding it. Enabled by default: CW-20260911-0069's decision is that an
// operator opts OUT on purpose, rather than a build silently dispatching into
// a plugin it already has evidence is not working.
func WithHealthGate(enabled bool) ChildPluginOption {
	return func(p *ChildPlugin) { p.healthGate = enabled }
}

// ChildPlugin is a subprocess plugin presented as a plugin.Plugin.
type ChildPlugin struct {
	spec  ChildSpec
	tools []MCPTool
	route []HTTPRoute

	// healthGate is read without a lock: it is set once in NewChildPlugin and
	// never written again, so there is nothing for a lock to protect.
	healthGate bool

	mu     sync.Mutex
	proc   *child
	status plugin.PluginStatus
	// restarts counts every automatic restart attempt this plugin has made,
	// successful or not — the same cumulative counter Nanite's Restarts()
	// reports. It never resets, including across a subsequent successful
	// restart, so an operator reading it sees the plugin's whole history.
	restarts int
	// stopping is closed by Unload to tell an in-flight restart not to spawn a
	// new process after the host decided to stop this plugin. Created once in
	// NewChildPlugin: a crash mid-backoff must not resurrect a plugin that
	// Unload already told to go away (CW-20260911-0068 bullet 4).
	stopping chan struct{}
	stopOnce sync.Once

	healthMu sync.Mutex
	health   pluginHealth
}

// NewChildPlugin describes a subprocess plugin without spawning it.
//
// The tools and routes it contributes are supplied by the caller rather than
// asked of the child: the HOST MANIFEST IS AUTHORITATIVE. A plugin does not
// declare at runtime what it registers — it is told what it was registered for
// and asked to service it. A child that answers a tool call for a name nobody
// registered reaches nothing.
//
// # subprocess.MethodListTools is deliberately never called
//
// The protocol offers `mcp/list_tools`, a way for a child to answer for itself
// what it provides. This host declines it, and the omission is a decision
// rather than an unfinished edge.
//
// Nanite built the runtime-declaration alternative — plugins calling
// `register()` at load — and discarded it; ADR 0008 §3 adopts the ruling that
// replaced it. A host that asks a child what it registers has made the child
// the authority on its own surface, which means a plugin can widen what it
// serves between two boots of the same host, and nothing reviewed the
// difference. Declaring it host-side means the answer to "what does this build
// serve" is in this build.
//
// If a later change wants a child to advertise itself, that is a reversal of
// ADR 0008 §3 and wants its own record — not a call added here because the
// method was sitting unused in the protocol.
func NewChildPlugin(spec ChildSpec, tools []MCPTool, routes []HTTPRoute, opts ...ChildPluginOption) *ChildPlugin {
	p := &ChildPlugin{
		spec: spec, tools: tools, route: routes,
		healthGate: true,
		stopping:   make(chan struct{}),
		health:     pluginHealth{ok: true},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *ChildPlugin) ID() string { return p.spec.ID }

func (p *ChildPlugin) Name() string {
	if info := p.info(); info.Name != "" {
		return info.Name
	}
	return p.spec.ID
}

func (p *ChildPlugin) Version() string {
	if info := p.info(); info.Version != "" {
		return info.Version
	}
	return "unknown"
}

func (p *ChildPlugin) Description() string {
	if info := p.info(); info.Description != "" {
		return info.Description
	}
	return "subprocess plugin " + p.spec.ID
}

// Dependencies returns none. A subprocess plugin's dependencies are in its own
// process — which is the entire point of the mode — so it has nothing to
// declare to this host about load order.
func (p *ChildPlugin) Dependencies() []string { return nil }

// Load spawns the child, completes the SDK handshake, and registers what this
// plugin was described as contributing.
//
// The child is spawned BEFORE anything is registered, so a plugin that cannot
// start contributes nothing rather than contributing a tool that dispatches
// into a process that is not there.
func (p *ChildPlugin) Load(host plugin.Host) error {
	if host == nil {
		return fmt.Errorf("pluginhost: %s: host is nil", p.spec.ID)
	}
	tangentHost, ok := host.(interface {
		RegisterMCPTool(MCPTool) error
		RegisterHTTPRoute(HTTPRoute) error
	})
	if !ok {
		return fmt.Errorf(
			"pluginhost: %s: host does not offer Tangent's registration surfaces; got %T",
			p.spec.ID, host)
	}

	proc, err := startChild(context.Background(), p.spec, hostVersionForChildren)
	if err != nil {
		p.fail(err)
		return err
	}
	p.mu.Lock()
	p.proc = proc
	p.mu.Unlock()

	for _, tool := range p.tools {
		tool.Handler = p
		if err := tangentHost.RegisterMCPTool(tool); err != nil {
			return p.failAndStop(err)
		}
	}
	for _, route := range p.route {
		route.Handler = p
		if err := tangentHost.RegisterHTTPRoute(route); err != nil {
			return p.failAndStop(err)
		}
	}

	p.mu.Lock()
	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now().UTC()}
	p.mu.Unlock()

	// Watch this process for the rest of its life, including every process a
	// restart replaces it with (superviseRestarts re-arms itself). Started
	// only once registration has fully succeeded: a child that failed
	// registration was already stopped by failAndStop above, and a plugin
	// that never finished loading is not one this host auto-restarts.
	go p.superviseRestarts(proc)
	return nil
}

// Unload ends the child process.
//
// Unlike a compiled-in plugin's Unload — which drops state and unregisters
// nothing, because this host's registries are boot-time — this one genuinely
// releases something: the process, and with it the application dependency the
// plugin holds. That difference is the task's whole value, and it is why
// CW-20260910-0036's "a plugin that ignores its context leaks for the life of
// the process" stops being true for a subprocess plugin. There is a boundary to
// kill now.
//
// The tool and route it registered stay registered and keep dispatching, into a
// plugin that now answers "not loaded". That part is unchanged: this host
// cannot attribute a registration to a plugin, so it has nothing to select for
// removal, and a readable refusal beats a surface that answers nothing.
func (p *ChildPlugin) Unload() error {
	p.mu.Lock()
	proc := p.proc
	p.proc = nil
	p.status = plugin.PluginStatus{}
	p.mu.Unlock()

	// Closed before stop() so a restart supervisor's very next observation of
	// this process exiting — which stop() is about to cause — is guaranteed to
	// see stopping already closed (Go's memory model: a close happens before
	// any receive that returns because of it, and this close strictly precedes
	// the exit stop() below will produce). That ordering is what stops a
	// crash-and-restart cycle from resurrecting a plugin Unload just told to
	// go away.
	p.stopOnce.Do(func() { close(p.stopping) })

	if proc == nil {
		return nil
	}
	return proc.stop()
}

func (p *ChildPlugin) Status() plugin.PluginStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// MCPCallTool forwards one tool call across the wire.
func (p *ChildPlugin) MCPCallTool(
	ctx context.Context,
	request subprocess.MCPCallRequest,
) (subprocess.MCPCallResult, error) {
	raw, err := p.gatedDispatch(ctx, subprocess.MethodMCPCallTool, request)
	if err != nil {
		return subprocess.MCPCallResult{}, err
	}
	var result subprocess.MCPCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return subprocess.MCPCallResult{}, fmt.Errorf(
			"pluginhost: %s: decode tool result: %w", p.spec.ID, err)
	}
	return result, nil
}

// HTTPHandle forwards one route request across the wire.
func (p *ChildPlugin) HTTPHandle(
	ctx context.Context,
	request subprocess.HTTPRequest,
) (subprocess.HTTPResponse, error) {
	raw, err := p.gatedDispatch(ctx, subprocess.MethodHTTPHandle, request)
	if err != nil {
		return subprocess.HTTPResponse{}, err
	}
	var response subprocess.HTTPResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return subprocess.HTTPResponse{}, fmt.Errorf(
			"pluginhost: %s: decode route response: %w", p.spec.ID, err)
	}
	return response, nil
}

// gatedDispatch is what a caller-facing surface (MCPCallTool, HTTPHandle)
// goes through, so a tool and a route cannot disagree about whether this
// child is fit to call. It refuses on the last CACHED plugin/health verdict
// rather than probing fresh on every call: a probe is a round trip into the
// child, and CW-20260911-0069's whole point is that this host does not spend
// one on every dispatch — only tangent.health_report (via probeHealth) ever
// advances it. probeHealth itself calls dispatch directly, never this, or an
// unhealthy plugin could never be probed back to healthy.
func (p *ChildPlugin) gatedDispatch(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if p.healthGate {
		p.healthMu.Lock()
		last := p.health
		p.healthMu.Unlock()
		if !last.checked.IsZero() && !last.ok {
			return nil, fmt.Errorf("pluginhost: %s: %w: %s", p.spec.ID, ErrPluginUnhealthy, last.message)
		}
	}
	return p.dispatch(ctx, method, params)
}

// dispatch is the one place a call reaches the child, so "is it running" is
// answered once rather than at each surface.
func (p *ChildPlugin) dispatch(ctx context.Context, method string, params any) (json.RawMessage, error) {
	p.mu.Lock()
	proc := p.proc
	p.mu.Unlock()
	if proc == nil {
		return nil, fmt.Errorf("pluginhost: %s is not loaded", p.spec.ID)
	}
	return proc.call(ctx, method, params)
}

// probeHealth asks the child plugin/health, on demand — this host runs no
// background ticker (CW-20260911-0069's decision: a probe nobody asked for is
// a second thing holding shutdown and a second thing to bound). The result is
// cached for healthProbeCacheTTL so tangent.health_report's two independent
// readers (Readiness and the raw plugin inventory) do not each pay for a
// separate round trip.
//
// The SDK answers plugin/health {ok:true} by default for a plugin that never
// implemented subprocess.HealthChecker — that default lives in
// subprocess.Serve, not on this wire, so a genuinely healthy answer and a
// plugin that never implemented the check are indistinguishable here. This
// host reports what it was told rather than resolving that ambiguity, which
// is an accepted, documented limitation rather than an oversight.
func (p *ChildPlugin) probeHealth(ctx context.Context) pluginHealth {
	p.healthMu.Lock()
	if !p.health.checked.IsZero() && time.Since(p.health.checked) < healthProbeCacheTTL {
		cached := p.health
		p.healthMu.Unlock()
		return cached
	}
	p.healthMu.Unlock()

	// The RPC itself runs with no lock held. healthMu only ever protects a
	// map read/write, never an I/O wait — otherwise an ordinary tool or route
	// dispatch's cache check in gatedDispatch would block for as long as this
	// probe takes, coupling an unrelated fast path to a slow one. The cost is
	// that two callers racing past the cache check above can each send one
	// plugin/health — bounded and rare, and far cheaper than serializing
	// dispatch on it.
	var result pluginHealth
	raw, err := p.dispatch(ctx, subprocess.MethodHealth, nil)
	switch {
	case err != nil:
		result = pluginHealth{ok: false, message: err.Error(), checked: time.Now().UTC(), reachable: false}
	default:
		var health subprocess.HealthResult
		if decodeErr := json.Unmarshal(raw, &health); decodeErr != nil {
			result = pluginHealth{
				ok: false, message: "malformed plugin/health response: " + decodeErr.Error(),
				checked: time.Now().UTC(), reachable: false,
			}
		} else {
			result = pluginHealth{ok: health.OK, message: health.Message, checked: time.Now().UTC(), reachable: true}
		}
	}

	p.healthMu.Lock()
	p.health = result
	p.healthMu.Unlock()
	return result
}

// Restarts reports how many automatic restart attempts this plugin has made
// (CW-20260911-0068), for lifecycle.go's Inventory to surface without every
// other Plugin implementation needing to grow a method it can never answer.
func (p *ChildPlugin) Restarts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.restarts
}

// superviseRestarts watches one child and, on an unexpected exit, restarts it
// with backoff — up to maxChildRestarts cumulative attempts across this
// plugin's whole life. It re-arms itself on every successful restart, so one
// goroutine per plugin covers however many processes it goes through.
//
// Shape mirrors Nanite's waitForExit/attemptRestart
// (apps/nanite/internal/plugin/subprocess/manager.go) rather than inventing a
// second restart policy: one attempt per crash event, cumulative counter,
// give up permanently once the cap is reached or a restart's own spawn fails.
func (p *ChildPlugin) superviseRestarts(proc *child) {
	<-proc.exited

	// Was this exit Unload's doing? stopping is closed strictly before Unload
	// causes the process to exit (see Unload's comment), so if this exit was
	// intentional, stopping is already closed by the time exited fires.
	select {
	case <-p.stopping:
		return
	default:
	}

	p.mu.Lock()
	if p.proc != proc {
		// Superseded already — should not happen (this is the only goroutine
		// that installs a replacement) but a defensive check costs nothing.
		p.mu.Unlock()
		return
	}
	attempt := p.restarts
	p.mu.Unlock()

	if attempt >= maxChildRestarts {
		p.fail(fmt.Errorf("pluginhost: %s crashed and exhausted %d restart attempts%s",
			p.spec.ID, maxChildRestarts, proc.diagnostics()))
		return
	}

	select {
	case <-p.stopping:
		return
	case <-time.After(restartBackoff(attempt)):
	}

	ctx, cancel := context.WithTimeout(context.Background(), childHandshakeBudget)
	newProc, err := startChild(ctx, p.spec, hostVersionForChildren)
	cancel()

	p.mu.Lock()
	select {
	case <-p.stopping:
		// Unload ran while this restart was in flight. Installing newProc now
		// would spawn a child after the host decided to stop this plugin
		// (CW-20260911-0068 bullet 4), so undo the spawn instead.
		p.mu.Unlock()
		if err == nil {
			_ = newProc.stop()
		}
		return
	default:
	}
	p.restarts++
	if err != nil {
		p.mu.Unlock()
		p.fail(fmt.Errorf("pluginhost: %s restart attempt %d/%d failed: %w",
			p.spec.ID, attempt+1, maxChildRestarts, err))
		return
	}
	p.proc = newProc
	// A restart re-runs plugin/init and plugin/load, so the plugin's prior
	// in-memory state is gone — the same fresh start a fresh boot of this one
	// plugin would produce. Nothing here needs to tell a caller: the next
	// dispatch reads p.proc fresh and reaches the new process transparently.
	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now().UTC()}
	// A restarted process starts from a clean health slate — the last verdict
	// was about a process that no longer exists.
	p.mu.Unlock()
	p.healthMu.Lock()
	p.health = pluginHealth{ok: true}
	p.healthMu.Unlock()

	go p.superviseRestarts(newProc)
}

// restartBackoff returns the delay before restart attempt number `attempt`
// (0-indexed), doubling from restartInitialBackoff up to restartMaxBackoff —
// the same schedule Nanite's attemptRestart computes.
func restartBackoff(attempt int) time.Duration {
	backoff := restartInitialBackoff
	for i := 0; i < attempt; i++ {
		backoff = time.Duration(float64(backoff) * restartBackoffFactor)
		if backoff > restartMaxBackoff {
			return restartMaxBackoff
		}
	}
	return backoff
}

func (p *ChildPlugin) info() subprocess.InitResult {
	p.mu.Lock()
	proc := p.proc
	p.mu.Unlock()
	if proc == nil {
		return subprocess.InitResult{}
	}
	return proc.info()
}

func (p *ChildPlugin) fail(err error) {
	p.mu.Lock()
	p.status.LastError = err.Error()
	p.mu.Unlock()
}

// failAndStop records a registration failure and ends the process this Load
// already started, so a refused registration does not leave a child running
// with nothing routed to it.
func (p *ChildPlugin) failAndStop(err error) error {
	p.fail(err)
	if stopErr := p.Unload(); stopErr != nil {
		return fmt.Errorf("%w (and stopping the child failed: %w)", err, stopErr)
	}
	return err
}

// Compile-time proof this satisfies the SDK contracts it is dispatched through.
var (
	_ plugin.Plugin          = (*ChildPlugin)(nil)
	_ subprocess.MCPHandler  = (*ChildPlugin)(nil)
	_ subprocess.HTTPHandler = (*ChildPlugin)(nil)
)
