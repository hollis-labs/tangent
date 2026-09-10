package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	plugin "github.com/hollis-labs/plugin-sdk"
	"github.com/hollis-labs/plugin-sdk/subprocess"

	"github.com/hollis-labs/tangent/internal/envelope"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	"github.com/hollis-labs/tangent/internal/room"
)

// These tests hold CW-20260910-0029: a plugin's MCP tool is an ordinary tool
// on this surface, and the three things it must never be able to do to that
// surface are shadow a tool, skip validation, or take the server down.

// errTorqueUnreachable stands in for the shape a real app plugin fails with:
// its application is down, and every other Tangent surface keeps working.
var errTorqueUnreachable = errors.New("torque is unreachable")

// pluginToolFunc adapts a function to the SDK's MCPHandler.
type pluginToolFunc func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error)

func (f pluginToolFunc) MCPCallTool(
	ctx context.Context, req subprocess.MCPCallRequest,
) (subprocess.MCPCallResult, error) {
	return f(ctx, req)
}

// connectWithPluginTools builds a Tangent MCP server carrying the supplied
// plugin tools and returns a connected in-memory client session.
//
// It deliberately does NOT reuse connect()'s long extension registration: the
// behavior under test is tool registration and dispatch, which is independent
// of which envelope kinds are in the registry.
func connectWithPluginTools(
	t *testing.T,
	tools []pluginhost.MCPTool,
) (*mcpsdk.ClientSession, func()) {
	t.Helper()
	session, cleanup, err := tryConnectWithPluginTools(t, tools)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	return session, cleanup
}

func tryConnectWithPluginTools(
	t *testing.T,
	tools []pluginhost.MCPTool,
) (*mcpsdk.ClientSession, func(), error) {
	t.Helper()
	ctx := context.Background()

	envSvc, err := envelope.New(ctx)
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	srv, err := tangentmcp.New(
		envSvc, envelope.NewDispatcher(envSvc), room.NewManager(nil), "",
		tangentmcp.WithPluginTools(tools),
	)
	if err != nil {
		return nil, nil, err
	}

	serverT, clientT := mcpsdk.NewInMemoryTransports()
	serverSession, err := srv.MCP().Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tangent-test", Version: "v0.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatalf("client.Connect: %v", err)
	}
	return clientSession, func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	}, nil
}

func echoTool(handler pluginToolFunc) pluginhost.MCPTool {
	return pluginhost.MCPTool{
		Name:        "tangent.stub_board",
		Description: "a stub plugin tool",
		InputSchema: json.RawMessage(
			`{"type":"object","properties":{"filter":{"type":"string"}},"required":["filter"]}`),
		Handler: handler,
	}
}

// TestPluginToolIsListedAndCallable is the whole point of the surface: an
// agent's single call reaches plugin code, with no host change per plugin.
func TestPluginToolIsListedAndCallable(t *testing.T) {
	var seen subprocess.MCPCallRequest
	session, done := connectWithPluginTools(t, []pluginhost.MCPTool{
		echoTool(func(_ context.Context, req subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
			seen = req
			return subprocess.MCPCallResult{Content: json.RawMessage(`{"rows":2}`)}, nil
		}),
	})
	defer done()
	ctx := context.Background()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var found *mcpsdk.Tool
	for _, tool := range listed.Tools {
		if tool.Name == "tangent.stub_board" {
			found = tool
		}
	}
	if found == nil {
		t.Fatal("tangent.stub_board is not in tools/list; a plugin tool that is not advertised " +
			"cannot be called by any agent")
	}
	if found.Description != "a stub plugin tool" {
		t.Errorf("tool description = %q, want the plugin's own", found.Description)
	}

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "tangent.stub_board",
		Arguments: map[string]any{"filter": "status=todo"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool reported an error: %s", extractText(t, result))
	}
	if got := extractText(t, result); got != `{"rows":2}` {
		t.Errorf("tool result = %q, want the plugin's own content verbatim", got)
	}
	if seen.ToolName != "tangent.stub_board" {
		t.Errorf("plugin saw tool_name %q", seen.ToolName)
	}
	if seen.Arguments["filter"] != "status=todo" {
		t.Errorf("plugin saw arguments %v, want the caller's", seen.Arguments)
	}
	// The participant session id is capability material and the MCP transports
	// are stateless; a synthesized value here would be a fact about nothing.
	if seen.SessionID != "" {
		t.Errorf("plugin was handed session id %q; it must be empty", seen.SessionID)
	}
}

// TestPluginToolArgumentsAreValidatedAgainstItsOwnSchema: the untyped
// registration path means nothing validates for us. A plugin that declares a
// schema and then receives something else has a decorative schema.
func TestPluginToolArgumentsAreValidatedAgainstItsOwnSchema(t *testing.T) {
	called := false
	session, done := connectWithPluginTools(t, []pluginhost.MCPTool{
		echoTool(func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
			called = true
			return subprocess.MCPCallResult{Content: json.RawMessage(`{}`)}, nil
		}),
	})
	defer done()

	result, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent.stub_board",
		Arguments: map[string]any{"filter": 7},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("a call violating the plugin's schema succeeded")
	}
	if called {
		t.Error("the plugin handler ran on arguments its own schema rejects")
	}
	if body := extractText(t, result); !strings.Contains(body, "validation-failed") {
		t.Errorf("refusal = %s, want the validation-failed code", body)
	}
}

// TestPluginToolErrorDoesNotBreakTheSurface: a plugin saying no is a tool
// result, and every other tool keeps working.
func TestPluginToolErrorDoesNotBreakTheSurface(t *testing.T) {
	session, done := connectWithPluginTools(t, []pluginhost.MCPTool{
		echoTool(func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
			return subprocess.MCPCallResult{}, errTorqueUnreachable
		}),
	})
	defer done()
	ctx := context.Background()

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "tangent.stub_board",
		Arguments: map[string]any{"filter": "status=todo"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("a plugin handler error was reported as success")
	}
	body := extractText(t, result)
	if !strings.Contains(body, "PLUGIN_FAILED") || !strings.Contains(body, "torque is unreachable") {
		t.Errorf("refusal = %s, want the plugin's own reason under PLUGIN_FAILED", body)
	}
	assertSurfaceStillWorks(t, session)
}

// TestPluginToolPanicIsContained is the "must not take down the host" clause.
// A plugin defect is one tool's problem, not every caller's outage.
func TestPluginToolPanicIsContained(t *testing.T) {
	session, done := connectWithPluginTools(t, []pluginhost.MCPTool{
		echoTool(func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
			panic("a plugin defect")
		}),
	})
	defer done()
	ctx := context.Background()

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "tangent.stub_board",
		Arguments: map[string]any{"filter": "status=todo"},
	})
	if err != nil {
		t.Fatalf("CallTool after a panicking plugin: %v", err)
	}
	if !result.IsError {
		t.Fatal("a panicking plugin was reported as success")
	}
	if body := extractText(t, result); !strings.Contains(body, "PLUGIN_PANICKED") {
		t.Errorf("refusal = %s, want PLUGIN_PANICKED — an operator should not have to tell a "+
			"plugin defect from a plugin refusal by reading prose", body)
	}
	assertSurfaceStillWorks(t, session)
}

// TestPluginToolReturningEnvelopesIsRefused: the SDK's result type carries
// envelopes and this host has no path for them. Accepting the field and
// dropping it would be a plugin believing it displayed something.
func TestPluginToolReturningEnvelopesIsRefused(t *testing.T) {
	session, done := connectWithPluginTools(t, []pluginhost.MCPTool{
		echoTool(func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
			return subprocess.MCPCallResult{
				Content:   json.RawMessage(`{}`),
				Envelopes: []plugin.EnvelopeOut{{}},
			}, nil
		}),
	})
	defer done()

	result, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent.stub_board",
		Arguments: map[string]any{"filter": "status=todo"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("a plugin emitting envelopes from a tool result was accepted silently")
	}
	if body := extractText(t, result); !strings.Contains(body, "does not emit envelopes") {
		t.Errorf("refusal = %s, want the reason named", body)
	}
}

// TestPluginToolCannotShadowAHostTool is the collision rule at the layer that
// knows the host surface. The SDK's registry is keyed by name and would have
// kept the plugin's handler silently: an agent calling tangent.session_create
// and reaching a plugin would look exactly like it working.
func TestPluginToolCannotShadowAHostTool(t *testing.T) {
	shadow := echoTool(func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
		return subprocess.MCPCallResult{Content: json.RawMessage(`{}`)}, nil
	})
	shadow.Name = "tangent.session_create"

	_, _, err := tryConnectWithPluginTools(t, []pluginhost.MCPTool{shadow})
	if err == nil {
		t.Fatal("a plugin tool shadowing tangent.session_create was accepted")
	}
	if !strings.Contains(err.Error(), "tangent.session_create") {
		t.Errorf("mcp.New = %v, want the colliding name in the message", err)
	}
}

// TestPluginToolsAppearInToolNames: internal/smoke compares the shipped
// surface against what the plugin host contributed, and it can only do that if
// the server reports its own names.
func TestPluginToolsAppearInToolNames(t *testing.T) {
	envSvc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	srv, err := tangentmcp.New(
		envSvc, envelope.NewDispatcher(envSvc), room.NewManager(nil), "",
		tangentmcp.WithPluginTools([]pluginhost.MCPTool{
			echoTool(func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
				return subprocess.MCPCallResult{Content: json.RawMessage(`{}`)}, nil
			}),
		}),
	)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	names := srv.ToolNames()
	var found bool
	for i, name := range names {
		if name == "tangent.stub_board" {
			found = true
		}
		if i > 0 && names[i-1] > name {
			t.Fatalf("ToolNames() is not sorted at %q", name)
		}
	}
	if !found {
		t.Errorf("ToolNames() = %v, want the plugin tool among them", names)
	}
	if !found || len(names) < 30 {
		t.Errorf("ToolNames() reported %d names; the host surface is missing from the registry", len(names))
	}
}

// assertSurfaceStillWorks calls a host tool to prove one plugin's failure left
// the rest of the surface intact.
func assertSurfaceStillWorks(t *testing.T, session *mcpsdk.ClientSession) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent.list_workflows",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("tangent.list_workflows after a plugin failure: %v", err)
	}
	if result.IsError {
		t.Fatalf("tangent.list_workflows reported an error after a plugin failure: %s",
			extractText(t, result))
	}
}
