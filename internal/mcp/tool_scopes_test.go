package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
	libauth "github.com/hollis-labs/libs/security/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/access"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
)

// heldScopes is a stand-in for the principal the host's guard would have
// stamped: tests set what the caller holds and the authorizer asks the way the
// host's does.
type heldScopes struct {
	mu     sync.Mutex
	scopes []string
}

func (h *heldScopes) set(scopes ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scopes = scopes
}

func (h *heldScopes) authorize(_ context.Context, _, scope string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if (libauth.Principal{Verified: true, Scopes: h.scopes}).HasScope(scope) {
		return nil
	}
	return errors.New("scope not held")
}

func goldenToolNames(t *testing.T, file string) []string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + file) // #nosec G304 -- a constant name inside the package
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(raw))
}

// TestEveryPublishedToolHasExactlyOneScope: the published names
// (testdata/tool_names.txt, recorded from the build that was running before
// the port) and the scope table cover each other. A tool added without a scope
// would be unreachable, so this fails it here instead of in someone's session;
// a scope kept for a tool that no longer exists fails too.
func TestEveryPublishedToolHasExactlyOneScope(t *testing.T) {
	published := goldenToolNames(t, "tool_names.txt")
	table := tangentmcp.HostToolScopes()
	var missing, stale []string
	for _, name := range published {
		if scope, ok := table[name]; !ok || scope == "" {
			missing = append(missing, name)
		}
	}
	inPublished := map[string]bool{}
	for _, name := range published {
		inPublished[name] = true
	}
	for name := range table {
		if !inPublished[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(missing) > 0 || len(stale) > 0 {
		t.Fatalf("scope table and published names disagree: published without a scope %v; scoped but not published %v", missing, stale)
	}
}

// TestNoToolNeedsTheDecisionScope: deciding is a person's, at a user session,
// through the browser API. If a tool could need tangent.resolve, an agent key
// granted it would be deciding.
func TestNoToolNeedsTheDecisionScope(t *testing.T) {
	for name, scope := range tangentmcp.HostToolScopes() {
		if scope == access.Resolve {
			t.Errorf("tool %s needs %s", name, access.Resolve)
		}
		if !strings.HasPrefix(scope, "tangent.") {
			t.Errorf("tool %s has scope %q", name, scope)
		}
	}
}

// TestCallingAToolNeedsItsScope drives the middleware over a real SDK session.
func TestCallingAToolNeedsItsScope(t *testing.T) {
	var held heldScopes
	client, srv, _, cleanup := connectServer(t, tangentmcp.WithAuthorizer(held.authorize))
	defer cleanup()
	ctx := context.Background()

	held.set(access.View)
	if res, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "tangent_workflow_list"}); err != nil || res.IsError {
		t.Fatalf("a view holder was refused a view tool: %v %+v", err, res)
	}
	refused, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "tangent_triage", Arguments: map[string]any{}})
	if err != nil || !refused.IsError || !strings.Contains(extractText(t, refused), "forbidden") {
		t.Fatalf("a view holder reached a submit tool: %v %+v", err, refused)
	}
	// The refusal names neither the scope nor whether the tool exists.
	missing, _ := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "tangent_no_such_tool"})
	if missing == nil || !missing.IsError || extractText(t, missing) != extractText(t, refused) {
		t.Fatalf("an unknown tool answers differently from a refused one: %+v vs %+v", missing, refused)
	}

	// The deprecated alias is authorized as the tool it names.
	if res, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "tangent.list_workflows"}); err != nil || res.IsError {
		t.Fatalf("alias of a view tool refused to a view holder: %v %+v", err, res)
	}
	held.set(access.Submit)
	if res, _ := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "tangent.list_workflows"}); res == nil || !res.IsError {
		t.Fatal("the alias of a view tool was open to a submit holder")
	}
	_ = srv
}

// TestAServerNobodyAuthorizesRefusesEverything: no authorizer is not an open
// server. (The package's own tests default to allow; this opts out of that.)
func TestAServerNobodyAuthorizesRefusesEverything(t *testing.T) {
	client, _, _, cleanup := connectServer(t, tangentmcp.WithAuthorizer(nil))
	defer cleanup()
	res, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "tangent_workflow_list"})
	if err != nil || !res.IsError {
		t.Fatalf("a server with no authorizer answered: %v %+v", err, res)
	}
}

// TestAToolWithNoScopeIsUnreachable registers a tool straight on the SDK server
// the way nothing in the package does, and shows it cannot be called until it
// is scoped.
func TestAToolWithNoScopeIsUnreachable(t *testing.T) {
	var held heldScopes
	held.set("*")
	client, srv, _, cleanup := connectServer(t, tangentmcp.WithAuthorizer(held.authorize))
	defer cleanup()
	mcpsdk.AddTool(srv.MCP(), &mcpsdk.Tool{Name: "stray_tool", Description: "x"},
		func(context.Context, *mcpsdk.CallToolRequest, struct{}) (*mcpsdk.CallToolResult, any, error) {
			return &mcpsdk.CallToolResult{}, nil, nil
		})
	ctx := context.Background()
	if res, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "stray_tool"}); err != nil || !res.IsError {
		t.Fatalf("an unscoped tool was reachable by a holder of every scope: %v %+v", err, res)
	}
	srv.ScopeToolForTest("stray_tool")
	if res, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "stray_tool"}); err != nil || res.IsError {
		t.Fatalf("a scoped tool was refused: %v %+v", err, res)
	}
}

// TestAPluginToolIsScopedWhenItRegistersAndUnscopedWhenItIsWithdrawn: tools
// plugins register after the server started get their scope at registration,
// are refused to a caller without it, and are unreachable once withdrawn.
func TestAPluginToolIsScopedWhenItRegistersAndUnscopedWhenItIsWithdrawn(t *testing.T) {
	var held heldScopes
	client, srv, _, cleanup := connectServer(t, tangentmcp.WithAuthorizer(held.authorize))
	defer cleanup()
	ctx := context.Background()

	if err := srv.AddPluginTool(echoTool(func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
		return subprocess.MCPCallResult{Content: json.RawMessage(`{"ok":true}`)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if scope, ok := srv.ToolScope("tangent.stub_board"); !ok || scope != access.Plugin {
		t.Fatalf("plugin tool scope = %q, %t; want %q at registration", scope, ok, access.Plugin)
	}
	call := func() *mcpsdk.CallToolResult {
		res, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "tangent.stub_board", Arguments: map[string]any{"filter": "all"}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	held.set(access.View, access.Submit)
	if !call().IsError {
		t.Fatal("a caller without tangent.plugin reached a plugin tool")
	}
	held.set(access.Plugin)
	if call().IsError {
		t.Fatal("a caller with tangent.plugin was refused a plugin tool")
	}
	srv.RemovePluginTools("tangent.stub_board")
	if _, ok := srv.ToolScope("tangent.stub_board"); ok {
		t.Fatal("a withdrawn plugin tool kept its scope")
	}
	held.set("*")
	res, _ := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "tangent.stub_board", Arguments: map[string]any{"filter": "all"}})
	if res != nil && !res.IsError {
		t.Fatal("a withdrawn plugin tool was reachable")
	}
}

// The plugin host's in-process caller carries the host-assigned plugin principal, so the same
// per-tool scopes apply to it as to any caller: it may use what a plugin may and not decide.
func TestTheLoopbackCallerIsAuthorizedAsThePluginHost(t *testing.T) {
	_, srv, _, cleanup := connectServer(t, tangentmcp.WithAuthorizer(access.AuthorizeTool))
	defer cleanup()
	ctx := libauth.WithPrincipal(context.Background(), access.PluginHostPrincipal())
	loop, err := srv.NewLoopbackCaller(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = loop.Close() }()
	if res, callErr := loop.CallTool(context.Background(), "tangent_workflow_list", nil); callErr != nil || res.IsError {
		t.Fatalf("the plugin host was refused a view tool: %v %+v", callErr, res)
	}
	bare, err := srv.NewLoopbackCaller(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bare.Close() }()
	if res, err := bare.CallTool(context.Background(), "tangent_workflow_list", nil); err != nil || !res.IsError {
		t.Fatalf("a loopback caller with no principal was served: %v %+v", err, res)
	}
}
