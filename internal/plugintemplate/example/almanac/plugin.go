// Package almanac is the plugin adapter that puts Almanac's records on a
// Tangent board (CW-20260910-0035).
//
// # Who calls what
//
// Every line here is a consequence of one decision (Tesseract
// `agents_drive_tangent_apps_are_called`):
//
//   - An **agent** is the caller. It calls one tool and passes filters. It
//     never shapes a payload.
//   - This **plugin** holds the Almanac dependency, in one file. Userland is
//     where a dependency belongs.
//   - **Almanac** is unchanged: an engine that is called and returns. It never
//     initiates a Tangent interaction.
//   - **Tangent core** stays domain-free. Nothing outside this package knows
//     Almanac exists, and that is checkable with one grep.
//
// The board itself is `tangent.app-board`, a domain-free kind this plugin does not own.
// board.go is where a Almanac status becomes a column and a Almanac tag becomes
// a badge, and that mapping is mechanical from end to end — a model asked to
// shape this payload would be doing a `for` loop expensively and occasionally
// wrong.
//
// # The process boundary
//
// Production builds this adapter into a native protocol-2 subprocess wrapper.
// Its owning application's writes originate in that child, outside Tangent's
// process. The host reviews the emitted manifest before loading it and never
// asks the running child to widen its tool or route registrations.
//
// Tangent holds no Almanac credential and grants no new identity. Application
// writes remain authorized by Almanac's own policy. The local MCP callback path
// is not a scoped host-service or broker grant.
//
// # What a sync is
//
// Two directions in one press, and neither of them is Tangent deciding
// anything:
//
//   - **Push.** The participant stages a card into another column. That is
//     recorded in the interaction's draft revision — staged intent, not a
//     decision — until they press Sync. The press is the decision.
//     This plugin then applies what is mechanical in Almanac's own terms and
//     hands the rest back to the agent as a work list.
//   - **Pull.** Almanac is re-queried with the board's original filters and the
//     board is replaced with fresh cards.
//
// The button reaches this plugin over its own HTTP route (ADR 0007 §4), so it
// costs no agent turn.
package almanac

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	plugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"

	tangentplugin "github.com/hollis-labs/tangent/pkg/plugin"
)

// ID is the plugin identifier, and the name that appears in host logs.
const ID = "tangent.plugin.almanac"

// Tool names. Both are in the `tangent.` namespace because every tool this
// build serves is, and internal/smoke's documentation gate matches that
// spelling — a plugin tool is a shipped tool, so adding one without documenting
// it in a file `documentedToolFiles` names fails the build.
//
// They are named for the application rather than for the kind they render on.
// An agent choosing between two boards is choosing between two applications.
const (
	// OpenTool opens a board. One call, filters in, a room URL out.
	OpenTool = "tangent.almanac_board"
	// SyncTool is the same sync the button performs, for an agent that wants
	// it in a turn it is already spending.
	SyncTool = "tangent.almanac_board_sync"
)

// SyncPath is the plugin-served route the board's Sync button POSTs to. It is
// under tangentplugin.RoutePrefix, which is what keeps it from colliding with the
// host's own routes or the SPA.
const SyncPath = tangentplugin.RoutePrefix + "almanac-board/sync"

// EnvelopeType is the domain-free kind this plugin supplies content to.
//
// It is CONTRIBUTED BY ANOTHER PLUGIN, not by this one, and that separation is
// load-bearing. When this board needs something the kind cannot express, the
// shortest path is to add it here — where you are already typing — and a
// domain-free kind quietly acquires one application's concept. The change
// belongs in the kind's own plugin, additively, with a manifest VERSION bump —
// an optional field still moves contract_digest, and ADR 0003 §3 makes that a
// version rather than a revision.
const EnvelopeType = "tangent.app-board"

// Plugin is the Almanac board adapter.
type Plugin struct {
	client *Client

	mu     sync.Mutex
	host   Host
	status plugin.PluginStatus
}

// Host is the narrow slice of the Tangent plugin host this plugin needs. It is
// declared here, at the consumer, so the plugin depends on what it uses rather
// than on the concrete host type.
type Host interface {
	RegisterMCPTool(tangentplugin.MCPTool) error
	RegisterHTTPRoute(tangentplugin.HTTPRoute) error
	Tools() (tangentplugin.ToolCaller, error)
}

// New returns an unloaded plugin pointed at the local Almanac.
//
// The environment is read here rather than through the host: GetConfig is
// deliberately unimplemented, and a plugin reading its own environment is
// userland doing userland's job.
func New() *Plugin {
	return &Plugin{client: NewClient(os.Getenv(BaseURLEnv), os.Getenv(TokenEnv))}
}

// NewWithClient returns a plugin over a supplied client. Tests use it;
// production goes through New.
func NewWithClient(client *Client) *Plugin { return &Plugin{client: client} }

func (p *Plugin) ID() string      { return ID }
func (p *Plugin) Name() string    { return "Almanac board" }
func (p *Plugin) Version() string { return "0.2.0-dev" }

func (p *Plugin) Description() string {
	return "Puts Almanac's records on a tangent.app-board: one agent call opens it, and a sync " +
		"button pushes staged changes back and pulls fresh cards down without an agent turn."
}

// Dependencies returns none. The kind this plugin fills is host plumbing,
// installed by extensions.RegisterAll before any plugin loads, and a plugin
// does not declare a dependency on the host.
//
// Name a plugin here only when this one genuinely needs ANOTHER PLUGIN loaded
// first. The host refuses a plugin whose stated dependency is not already
// loaded, which turns that into a boot failure instead of a failure at the
// first tool call.
func (p *Plugin) Dependencies() []string { return nil }

// Load registers the two tools and the one route.
//
// The host is Tangent's, not the SDK's base contract: RegisterMCPTool and
// RegisterHTTPRoute are extensions, which the SDK names as the way a host adds
// its own registration surfaces. A plugin that finds neither is running on a
// host that cannot serve it, and saying so at load beats discovering it when
// someone presses a button.
func (p *Plugin) Load(host plugin.Host) error {
	if host == nil {
		return fmt.Errorf("almanac: host is nil")
	}
	tangentHost, ok := host.(Host)
	if !ok {
		return fmt.Errorf(
			"almanac: host does not offer Tangent's plugin registration surfaces "+
				"(RegisterMCPTool, RegisterHTTPRoute); got %T", host)
	}

	if err := tangentHost.RegisterMCPTool(tangentplugin.MCPTool{
		Name:        OpenTool,
		Description: openToolDescription,
		InputSchema: openToolSchema,
		Handler:     p,
	}); err != nil {
		return p.failLoad(err)
	}
	if err := tangentHost.RegisterMCPTool(tangentplugin.MCPTool{
		Name:        SyncTool,
		Description: syncToolDescription,
		InputSchema: syncToolSchema,
		Handler:     p,
	}); err != nil {
		return p.failLoad(err)
	}
	// `draft` rather than `submit`: ADR 0004 §7's participant row is
	// {view, draft, resolve, cancel}, and a sync is the participant applying
	// what they staged — authoring, not settling. The board interaction is
	// still pending afterwards, which is the test for whether `resolve` would
	// have been the right word.
	if err := tangentHost.RegisterHTTPRoute(tangentplugin.HTTPRoute{
		Method:     http.MethodPost,
		Path:       SyncPath,
		Capability: tangentplugin.CapabilityDraft,
		Handler:    p,
	}); err != nil {
		return p.failLoad(err)
	}

	p.mu.Lock()
	p.host = tangentHost
	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now().UTC()}
	p.mu.Unlock()
	return nil
}

func (p *Plugin) failLoad(err error) error {
	p.mu.Lock()
	p.status.LastError = err.Error()
	p.mu.Unlock()
	return err
}

// Unload drops the plugin's own state. It unregisters nothing, which is the
// host's stated contract (internal/pluginhost/lifecycle.go) and not a choice
// each plugin makes: this host's registries are boot-time, and a plugin that
// should not be present is one that is not loaded at boot.
func (p *Plugin) Unload() error {
	p.mu.Lock()
	p.host = nil
	p.status = plugin.PluginStatus{}
	p.mu.Unlock()
	return nil
}

func (p *Plugin) Status() plugin.PluginStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// tools resolves the host's tool caller at dispatch time.
//
// Not at Load: plugins load before the MCP server exists, because a
// plugin-contributed envelope kind has to be in the registry the MCP server
// reads. Holding a handle from Load would mean holding nil.
func (p *Plugin) tools() (tangentplugin.ToolCaller, error) {
	p.mu.Lock()
	host := p.host
	p.mu.Unlock()
	if host == nil {
		return nil, fmt.Errorf("almanac: plugin is not loaded")
	}
	return host.Tools()
}

// MCPCallTool services both of this plugin's tools.
func (p *Plugin) MCPCallTool(
	ctx context.Context,
	request subprocess.MCPCallRequest,
) (subprocess.MCPCallResult, error) {
	switch request.ToolName {
	case OpenTool:
		var input OpenInput
		if err := decodeArguments(request.Arguments, &input); err != nil {
			return subprocess.MCPCallResult{}, err
		}
		result, err := p.Open(ctx, input)
		if err != nil {
			return subprocess.MCPCallResult{}, err
		}
		return jsonResult(result)
	case SyncTool:
		var input SyncInput
		if err := decodeArguments(request.Arguments, &input); err != nil {
			return subprocess.MCPCallResult{}, err
		}
		result, err := p.Sync(ctx, input)
		if err != nil {
			return subprocess.MCPCallResult{}, err
		}
		return jsonResult(result)
	default:
		return subprocess.MCPCallResult{}, fmt.Errorf(
			"almanac: no handler for tool %q", request.ToolName)
	}
}

// HTTPHandle services the board's Sync button.
//
// It is the same Sync the tool performs, reached without an agent turn. The
// request has already passed the same-origin guard, the participant session and
// the ADR 0004 §7 capability check by the time it arrives — the host mounts a
// plugin route through the door every other browser route uses, and the
// participant's session cookie does not cross into here.
func (p *Plugin) HTTPHandle(
	ctx context.Context,
	request subprocess.HTTPRequest,
) (subprocess.HTTPResponse, error) {
	if request.Path != SyncPath {
		return subprocess.HTTPResponse{}, fmt.Errorf(
			"almanac: no handler for route %s %s", request.Method, request.Path)
	}
	var input SyncInput
	if len(request.Body) > 0 {
		if err := json.Unmarshal(request.Body, &input); err != nil {
			return badRequest("the sync request body is not JSON: " + err.Error())
		}
	}
	if input.RoomID == "" {
		input.RoomID = request.Query["room_id"]
	}
	if input.RoomID == "" {
		return badRequest("sync needs a room_id")
	}

	result, err := p.Sync(ctx, input)
	if err != nil {
		// Returned as an error rather than a status: the host renders it as a
		// refusal the SPA can show, with this plugin's own message, which for
		// an outage is the sentence the operator actually needs.
		return subprocess.HTTPResponse{}, err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return subprocess.HTTPResponse{}, fmt.Errorf("almanac: encode sync result: %w", err)
	}
	return subprocess.HTTPResponse{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    body,
	}, nil
}

func badRequest(message string) (subprocess.HTTPResponse, error) {
	body, _ := json.Marshal(map[string]string{"code": "invalid_request", "message": message})
	return subprocess.HTTPResponse{
		Status:  http.StatusBadRequest,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    body,
	}, nil
}

// decodeArguments re-decodes the host's generic argument map into a typed
// input. The host has already validated it against the tool's own schema, so
// this cannot reject anything the schema admits; it is a shape conversion.
func decodeArguments(arguments map[string]any, into any) error {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("almanac: re-encode arguments: %w", err)
	}
	if err := json.Unmarshal(encoded, into); err != nil {
		return fmt.Errorf("almanac: decode arguments: %w", err)
	}
	return nil
}

func jsonResult(value any) (subprocess.MCPCallResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return subprocess.MCPCallResult{}, fmt.Errorf("almanac: encode result: %w", err)
	}
	return subprocess.MCPCallResult{Content: encoded}, nil
}

// Compile-time proof this satisfies the SDK contracts it registers against.
var (
	_ plugin.Plugin          = (*Plugin)(nil)
	_ subprocess.MCPHandler  = (*Plugin)(nil)
	_ subprocess.HTTPHandler = (*Plugin)(nil)
)
