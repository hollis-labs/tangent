package smoke_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/pluginpkg"
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
// An empty installed set is a FAILURE rather than a skip. This suite installs
// the first-party plugins itself, so nothing to compare means the install broke
// — and a gate that skipped on its own setup failing is how one rots.

// TestEveryPluginContributedToolReachesTheShippedBinary derives the plugin
// tool set from internal/plugins and asserts the binary advertises every one.
func TestEveryPluginContributedToolReachesTheShippedBinary(t *testing.T) {
	endpoint, pluginDir := bootShippedBinaryWithPluginDir(t)
	contributed := installedPluginToolNames(t, pluginDir)
	if len(contributed) == 0 {
		t.Fatal("no installed plugin declares an MCP tool: this suite installs the " +
			"first-party plugins before booting, so an empty set means the install " +
			"itself failed rather than that there is nothing to check")
	}

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

// installedPluginToolNames reads the manifests of the plugins this suite
// installed and returns the tool names they declare.
//
// # Both sides are still derived, from different places
//
// It used to load the compiled-in roster onto a throwaway host. There is no
// roster now (CW-20260911-0070) — plugins are installed — so the expected set
// comes from the install directory's own manifests, and the actual set from
// asking the running binary for tools/list.
//
// That is a stronger comparison than the old one, not a weaker substitute. The
// old check compared two views of one compiled artifact. This one compares what
// was INSTALLED against what a separately spawned process ended up advertising,
// so everything between — discovery, the protocol check, the spawn, the
// handshake, the host's registration — is inside the assertion. A plugin that
// installs and does not serve now fails here.
func installedPluginToolNames(t *testing.T, root string) []string {
	t.Helper()
	installed, rejected, err := pluginpkg.Scan(root)
	if err != nil {
		t.Fatalf("pluginpkg.Scan: %v", err)
	}
	for _, reject := range rejected {
		t.Errorf("%s did not install cleanly: %v", reject.Dir, reject.Reason)
	}
	var names []string
	for _, entry := range installed {
		for _, tool := range entry.Manifest.Tools {
			names = append(names, tool.Name)
		}
	}
	sort.Strings(names)
	return names
}
