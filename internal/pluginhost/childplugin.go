package pluginhost

import (
	"context"
	"encoding/json"
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

// ChildPlugin is a subprocess plugin presented as a plugin.Plugin.
type ChildPlugin struct {
	spec  ChildSpec
	tools []MCPTool
	route []HTTPRoute

	mu     sync.Mutex
	proc   *child
	status plugin.PluginStatus
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
func NewChildPlugin(spec ChildSpec, tools []MCPTool, routes []HTTPRoute) *ChildPlugin {
	return &ChildPlugin{spec: spec, tools: tools, route: routes}
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
	raw, err := p.dispatch(ctx, subprocess.MethodMCPCallTool, request)
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
	raw, err := p.dispatch(ctx, subprocess.MethodHTTPHandle, request)
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
