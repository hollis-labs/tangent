package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/pluginhost"
)

// LoopbackCaller is an in-process MCP client session against this server. It
// is how a plugin drives Tangent (CW-20260910-0031).
//
// # Why a real session rather than a shortcut
//
// The tempting version of this file dispatches straight to a handler by name
// and skips the protocol. It would also skip the receiving middleware, the
// input-schema validation, and the caller-identity seam — three things every
// other caller goes through, and three ways a plugin could end up with an
// authority or a leniency no MCP client has. Going over the SDK's in-memory
// transport costs one goroutine pair and a JSON round trip, and buys the
// property worth having: **a plugin is a caller of the same surface, on the
// same terms.** It resolves to the same host-assigned caller identity every
// direct MCP caller does (see caller_identity.go), which is the honest answer —
// nothing here declares an application, so nothing should pretend to.
//
// # What it must not be used for
//
// A tool that waits. Every room-backed tool a plugin calls should pass
// completion mode "async": a `wait` would block this session's request until a
// human answered, and a plugin holding a synchronous call open across a human
// decision is a plugin holding an HTTP request open across a human decision.
type LoopbackCaller struct {
	client *mcpsdk.ClientSession
	server *mcpsdk.ServerSession
}

// NewLoopbackCaller connects a client session to this server over the SDK's
// in-memory transport.
//
// The returned caller owns two sessions and must be closed. The composition
// root closes it alongside the database handle; a caller that leaks it leaks
// the SDK's reader goroutines, which internal/mcp's goleak tests would notice
// and a production process would not.
func (s *Server) NewLoopbackCaller(ctx context.Context) (*LoopbackCaller, error) {
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := s.mcp.SDKServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: connect loopback server session: %w", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    implementationName + "-loopback",
		Version: implementationVersion,
	}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		return nil, fmt.Errorf("mcp: connect loopback client session: %w", err)
	}
	return &LoopbackCaller{client: clientSession, server: serverSession}, nil
}

// CallTool invokes one tool by name and returns its result.
//
// The distinction it preserves is the one a plugin needs: a transport failure
// is an error, and a tool that answered "no" is a ToolResult with IsError set.
// Collapsing them would make "Tangent is gone" and "that room does not exist"
// the same fact.
func (c *LoopbackCaller) CallTool(
	ctx context.Context,
	name string,
	arguments any,
) (pluginhost.ToolResult, error) {
	if c == nil || c.client == nil {
		return pluginhost.ToolResult{}, errors.New("mcp: loopback caller is closed")
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	result, err := c.client.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		return pluginhost.ToolResult{}, fmt.Errorf("mcp: call %s: %w", name, err)
	}
	return pluginhost.ToolResult{
		Content: loopbackContent(result),
		IsError: result.IsError,
	}, nil
}

// Close tears down both sessions.
func (c *LoopbackCaller) Close() error {
	if c == nil {
		return nil
	}
	var errs []error
	if c.client != nil {
		if err := c.client.Close(); err != nil {
			errs = append(errs, err)
		}
		c.client = nil
	}
	if c.server != nil {
		if err := c.server.Close(); err != nil {
			errs = append(errs, err)
		}
		c.server = nil
	}
	return errors.Join(errs...)
}

// loopbackContent extracts the JSON body a Tangent tool returns.
//
// StructuredContent is preferred where the tool declared an output schema, and
// the first text block otherwise — which is what every tool in this package
// produces, error results included (see toolErrorResult). An empty result
// becomes `{}` rather than nil so a plugin's json.Unmarshal has something
// valid to fail on rather than something absent to dereference.
func loopbackContent(result *mcpsdk.CallToolResult) json.RawMessage {
	if result == nil {
		return json.RawMessage(`{}`)
	}
	if result.StructuredContent != nil {
		if encoded, err := json.Marshal(result.StructuredContent); err == nil {
			return encoded
		}
	}
	for _, content := range result.Content {
		text, ok := content.(*mcpsdk.TextContent)
		if !ok || text.Text == "" {
			continue
		}
		return json.RawMessage(text.Text)
	}
	return json.RawMessage(`{}`)
}

// Compile-time proof the loopback caller is the surface the plugin host hands
// to plugins.
var _ pluginhost.ToolCaller = (*LoopbackCaller)(nil)
