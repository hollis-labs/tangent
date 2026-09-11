// Package hostclient is how a subprocess plugin calls back into Tangent.
//
// # Why a plugin needs this at all
//
// The plugin wire is one-directional. `libs/plugin-sdk/subprocess` says so —
// "the subprocess does not initiate requests" — and its server never
// constructs one. A plugin receives `mcp/call_tool` and answers; it cannot ask
// the host for anything over that pipe.
//
// Both application plugins need to ask. Opening a board is
// `tangent.session_create`; syncing one is `tangent.surface_get` and
// `tangent.session_advance`. Those are the feature, not an edge.
//
// # Why this is the front door rather than a workaround
//
// `internal/pluginhost/tools.go` defines its in-process `ToolCaller` as
// granting "no authority a local MCP caller does not already have… the same
// host-assigned caller identity every direct MCP caller resolves to". A child
// connecting to `/mcp` does not approximate that equivalence — it IS a local
// MCP caller, so the claim needs no argument and nothing maintains its truth.
//
// And a subprocess plugin is already a local process: it can reach `/mcp`
// whether or not the host intends it to. Nothing is granted by choosing this.
// The only thing the choice decides is whether the intended path is the real
// one, or whether there is a second private channel beside a public one the
// child can reach anyway — which is the shape `RegisterCRUDHandler` and
// `GetService` are already refused for.
//
// The convergence is worth naming: an agent drives Tangent through MCP, and
// now a plugin does too. A plugin is another client of the service rather than
// a privileged insider with its own door.
package hostclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/pluginhost"
)

// URLEnv names the environment variable carrying Tangent's MCP endpoint.
//
// It arrives in the environment rather than in the SDK's InitParams for the
// same reason the host sends an empty config map: the host says WHERE things
// are, and a plugin reads its own environment. Neither is configuration the
// host holds on a plugin's behalf, which is what keeps ADR 0005 §3.1's secret
// boundary true by construction.
const URLEnv = "TANGENT_MCP_URL"

// ErrNoHost reports that a plugin was started without being told where its host
// is. It is returned rather than defaulted: a plugin that guessed a URL would
// either reach the wrong Tangent or fail with a connection error that says
// nothing about the real cause.
var ErrNoHost = errors.New("hostclient: " + URLEnv + " is not set")

// Client is a plugin's connection to Tangent's tool surface.
//
// It satisfies the same one-method shape `pluginhost.ToolCaller` does, so plugin
// code written against the in-process caller works unchanged out of process.
type Client struct {
	url string

	mu      sync.Mutex
	session *mcpsdk.ClientSession
}

// New returns a client for the host named by TANGENT_MCP_URL.
func New() (*Client, error) {
	url := os.Getenv(URLEnv)
	if url == "" {
		return nil, ErrNoHost
	}
	return &Client{url: url}, nil
}

// Compile-time proof this is interchangeable with the in-process caller. Plugin
// code written against pluginhost.ToolCaller runs unchanged out of process,
// which is what makes the migration a change of wiring rather than a rewrite.
var _ pluginhost.ToolCaller = (*Client)(nil)

// CallTool calls one Tangent tool.
//
// # Connecting lazily is required, not an optimization
//
// The host loads plugins BEFORE the MCP and HTTP servers exist — a
// plugin-contributed kind has to be in the registry `mcp.New` reads, so plugins
// necessarily come first. A plugin that dialed its host at startup would dial a
// port nothing is listening on yet, and the failure would look like a
// misconfigured URL rather than an ordering problem.
//
// So the session is established on first use, which is after boot by
// construction. The compiled-in plugins already resolve their caller at
// dispatch for exactly this reason.
func (c *Client) CallTool(ctx context.Context, name string, arguments any) (pluginhost.ToolResult, error) {
	session, err := c.connect(ctx)
	if err != nil {
		return pluginhost.ToolResult{}, err
	}

	encoded, err := toArgumentMap(arguments)
	if err != nil {
		return pluginhost.ToolResult{}, err
	}
	response, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: name, Arguments: encoded,
	})
	if err != nil {
		// A failed call drops the session so the next call reconnects. A host
		// that restarted, or a connection that broke, should cost one call
		// rather than every later one — the same property
		// internal/pluginhost/wire.go holds on the other side of the pipe.
		c.reset(session)
		return pluginhost.ToolResult{}, fmt.Errorf("hostclient: call %s: %w", name, err)
	}

	result := pluginhost.ToolResult{IsError: response.IsError}
	if len(response.Content) > 0 {
		if text, ok := response.Content[0].(*mcpsdk.TextContent); ok {
			result.Content = json.RawMessage(text.Text)
		}
	}
	return result, nil
}

func (c *Client) connect(ctx context.Context) (*mcpsdk.ClientSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		return c.session, nil
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name: "tangent-plugin", Version: "0.1.0",
	}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: c.url}, nil)
	if err != nil {
		return nil, fmt.Errorf("hostclient: connect to %s: %w", c.url, err)
	}
	c.session = session
	return session, nil
}

func (c *Client) reset(stale *mcpsdk.ClientSession) {
	c.mu.Lock()
	if c.session == stale {
		c.session = nil
	}
	c.mu.Unlock()
	_ = stale.Close()
}

// Close ends the session, if one was established.
func (c *Client) Close() error {
	c.mu.Lock()
	session := c.session
	c.session = nil
	c.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.Close()
}

// toArgumentMap converts a caller's arguments into the map the MCP SDK wants,
// so plugin code can pass the same struct or map it passes in-process.
func toArgumentMap(arguments any) (map[string]any, error) {
	if arguments == nil {
		return map[string]any{}, nil
	}
	if already, ok := arguments.(map[string]any); ok {
		return already, nil
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return nil, fmt.Errorf("hostclient: encode arguments: %w", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, fmt.Errorf("hostclient: decode arguments: %w", err)
	}
	return decoded, nil
}
