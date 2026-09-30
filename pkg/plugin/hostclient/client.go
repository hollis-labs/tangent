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
// `pkg/plugin`'s `ToolCaller` (the in-process form lives in
// `internal/pluginhost`) is defined as
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

	gmcpclient "github.com/hollis-labs/go-mcp/client"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/pkg/plugin"
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

// poolServerName is the single entry this client registers in its go-mcp/client
// pool — there is exactly one host to reach, but the pool is still the right
// tool: it gives connect-on-first-use, one retry on a recoverable error, and
// leak-prevention-on-error for free, matching the client package every other
// app in this portfolio's go-mcp migration converged onto (CW-20260917-0018).
const poolServerName = "tangent"

// Client is a plugin's connection to Tangent's tool surface.
//
// It satisfies the same one-method shape `plugin.ToolCaller` does, so plugin
// code written against the in-process caller works unchanged out of process.
type Client struct {
	pool *gmcpclient.Pool
}

// New returns a client for the host named by TANGENT_MCP_URL.
//
// # Connecting lazily is required, not an optimization
//
// The host loads plugins BEFORE the MCP and HTTP servers exist — a
// plugin-contributed kind has to be in the registry `mcp.New` reads, so plugins
// necessarily come first. A plugin that dialed its host at startup would dial a
// port nothing is listening on yet, and the failure would look like a
// misconfigured URL rather than an ordering problem. Registering a pool entry
// does not dial; go-mcp/client dials on first use, which is after boot by
// construction.
func New() (*Client, error) {
	url := os.Getenv(URLEnv)
	if url == "" {
		return nil, ErrNoHost
	}
	pool := gmcpclient.NewPool(gmcpclient.WithIdentity("tangent-plugin", "0.1.0"))
	if err := pool.Register(poolServerName, gmcpclient.ServerConfig{
		Transport: gmcpclient.TransportHTTP,
		URL:       url,
	}); err != nil {
		return nil, fmt.Errorf("hostclient: register %s: %w", url, err)
	}
	return &Client{pool: pool}, nil
}

// Compile-time proof this is interchangeable with the in-process caller. Plugin
// code written against plugin.ToolCaller runs unchanged out of process,
// which is what makes the migration a change of wiring rather than a rewrite.
var _ plugin.ToolCaller = (*Client)(nil)

// CallTool calls one Tangent tool.
func (c *Client) CallTool(ctx context.Context, name string, arguments any) (plugin.ToolResult, error) {
	encoded, err := toArgumentMap(arguments)
	if err != nil {
		return plugin.ToolResult{}, err
	}
	response, _, err := c.pool.CallTool(ctx, poolServerName, name, encoded)
	if err != nil {
		return plugin.ToolResult{}, fmt.Errorf("hostclient: call %s: %w", name, err)
	}

	result := plugin.ToolResult{IsError: response.IsError}
	if len(response.Content) > 0 {
		if text, ok := response.Content[0].(*mcpsdk.TextContent); ok {
			result.Content = json.RawMessage(text.Text)
		}
	}
	return result, nil
}

// Close ends the pooled connection, if one was established.
func (c *Client) Close() error {
	return c.pool.Close()
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
