package mcp_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
)

// introducedAfterRename lists tools that never had an old name, so they have
// no alias. Empty at the time of the rename.
var introducedAfterRename = map[string]bool{}

func listedNames(t *testing.T, client *mcpsdk.ClientSession) (names map[string]*mcpsdk.Tool) {
	t.Helper()
	names = map[string]*mcpsdk.Tool{}
	for tool, err := range client.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatalf("list tools: %v", err)
		}
		names[tool.Name] = tool
	}
	return names
}

// TestHostToolNamesMatchTheCharset fails if any non-alias tool name leaves
// ^[a-z][a-z0-9_]*$. Strict function-calling clients and the tether gateway
// reject anything else, and the gateway never normalizes a name.
func TestHostToolNamesMatchTheCharset(t *testing.T) {
	client, srv, _, cleanup := connectServer(t)
	defer cleanup()

	registered := srv.ToolNames()
	if len(registered) == 0 {
		t.Fatal("server registered no tools")
	}
	for _, name := range registered {
		if !tangentmcp.ToolNamePattern.MatchString(name) {
			t.Errorf("registered tool %q does not match %s", name, tangentmcp.ToolNamePattern)
		}
		if !strings.HasPrefix(name, "tangent_") {
			t.Errorf("registered tool %q lacks the tangent_ prefix", name)
		}
	}
	for name := range listedNames(t, client) {
		if !tangentmcp.ToolNamePattern.MatchString(name) {
			t.Errorf("tools/list advertises %q, which does not match %s", name, tangentmcp.ToolNamePattern)
		}
	}
}

// TestLegacyToolAliases pins the compatibility contract: every tool has
// exactly one deprecated alias, the alias is callable but unlisted by default,
// it routes to the same handler, and it is listed (marked DEPRECATED) only on
// opt-in.
func TestLegacyToolAliases(t *testing.T) {
	client, srv, _, cleanup := connectServer(t)
	defer cleanup()

	registered := srv.ToolNames()
	aliases := srv.LegacyToolNames()
	wantAliases := make([]string, 0, len(registered))
	for _, name := range registered {
		legacy, ok := tangentmcp.LegacyToolName(name)
		if !ok {
			if !introducedAfterRename[name] {
				t.Errorf("tool %q has no deprecated alias; a renamed tool needs a legacyToolNames entry, "+
					"a new one belongs in introducedAfterRename", name)
			}
			continue
		}
		if tangentmcp.ToolNamePattern.MatchString(legacy) {
			t.Errorf("alias %q of %q matches the charset; an alias exists only for a name that does not", legacy, name)
		}
		wantAliases = append(wantAliases, legacy)
	}
	sort.Strings(wantAliases)
	if strings.Join(aliases, ",") != strings.Join(wantAliases, ",") {
		t.Errorf("LegacyToolNames() = %v, want %v", aliases, wantAliases)
	}

	listed := listedNames(t, client)
	for _, alias := range aliases {
		if _, found := listed[alias]; found {
			t.Errorf("alias %q is in the default tools/list", alias)
		}
	}

	call := func(name string) string {
		result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name})
		if err != nil {
			t.Fatalf("call %s: %v", name, err)
		}
		if result.IsError {
			t.Fatalf("call %s: tool error %+v", name, result.Content)
		}
		return extractText(t, result)
	}
	if got, want := call("tangent.list_workflows"), call("tangent_workflow_list"); got != want {
		t.Errorf("alias result differs from the new name:\n alias: %s\n new:   %s", got, want)
	}
}

func TestLegacyToolAliasesAreListedOnOptIn(t *testing.T) {
	client, srv, _, cleanup := connectServer(t, tangentmcp.WithLegacyToolNamesListed())
	defer cleanup()

	listed := listedNames(t, client)
	for _, alias := range srv.LegacyToolNames() {
		tool, found := listed[alias]
		if !found {
			t.Errorf("alias %q is missing from tools/list despite the opt-in", alias)
			continue
		}
		if !strings.HasPrefix(tool.Description, "DEPRECATED alias of tangent_") {
			t.Errorf("alias %q description %q does not announce the deprecation", alias, tool.Description)
		}
	}
	for _, name := range srv.ToolNames() {
		if _, found := listed[name]; !found {
			t.Errorf("tool %q is missing from tools/list", name)
		}
	}
}
