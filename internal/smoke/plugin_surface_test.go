package smoke_test

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/plugins"
)

// This file is the plugin half of the derived-surface rule (CW-20260910-0029).
//
// The documentation gate already asks the *shipped binary* what it serves,
// which is what keeps a number from being typed into prose. A plugin tool needs
// the same treatment from the other direction: it is registered by a plugin
// rather than by a line in internal/mcp, so "did it actually reach the binary"
// is a question `go test ./internal/mcp` cannot answer. An in-process server a
// test assembled can carry a plugin tool while the shipped one does not — a
// missed option at the composition root is exactly that shape of bug, and it
// would look like everything working.
//
// So both sides are derived: the expected set by loading the shipped plugin set
// onto a throwaway host, the actual set by asking `./tangent` for tools/list.
// Neither is written down here, and the check keeps meaning something as
// plugins are added.
//
// When no shipped plugin contributes a tool the check is vacuous and says so
// rather than passing quietly, because a vacuous check that reads as a passing
// one is how this gate would rot.

// TestEveryPluginContributedToolReachesTheShippedBinary derives the plugin
// tool set from internal/plugins and asserts the binary advertises every one.
func TestEveryPluginContributedToolReachesTheShippedBinary(t *testing.T) {
	contributed := shippedPluginToolNames(t)
	if len(contributed) == 0 {
		t.Skip("no shipped plugin contributes an MCP tool; nothing to compare " +
			"(this becomes a real check the moment one does)")
	}

	endpoint := bootShippedBinary(t)
	surface, finding := endpoint.StreamableSurface(context.Background())
	if finding != nil {
		t.Fatalf("derive shipped surface:\n%s", finding)
	}
	shipped := map[string]bool{}
	for _, name := range surface.Names {
		shipped[name] = true
	}

	var missing []string
	for _, name := range contributed {
		if !shipped[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("the shipped binary does not advertise plugin-contributed tool(s) %s; "+
			"the plugin registered them and the composition root did not install them (surface: %s)",
			strings.Join(missing, ", "), surface.Describe())
	}
}

// shippedPluginToolNames loads the compiled-in plugin set onto a throwaway
// host and returns the tool names it contributed.
//
// It goes through plugins.LoadShipped rather than assembling a registry of its
// own, for the same reason cmd/tangent-dump-types does: a second way to
// enumerate the plugin set is a second thing that can describe a different
// build from the one that ships.
func shippedPluginToolNames(t *testing.T) []string {
	t.Helper()
	ctx := context.Background()

	envSvc, err := envelope.New(ctx)
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if regErr := extensions.RegisterAll(envSvc); regErr != nil {
		t.Fatalf("extensions.RegisterAll: %v", regErr)
	}
	host, err := plugins.LoadShipped(ctx, slog.New(slog.DiscardHandler), envSvc)
	if err != nil {
		t.Fatalf("plugins.LoadShipped: %v", err)
	}
	names := make([]string, 0, len(host.MCPTools()))
	for _, tool := range host.MCPTools() {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}
