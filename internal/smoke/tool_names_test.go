package smoke_test

import (
	"context"
	"strings"
	"testing"

	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
)

// TestShippedToolNamesMatchTheCharset checks the whole advertised surface of the
// shipped binary, which unlike a bare test server includes the HITL, docs,
// turns, relay, inbox and interaction tools. Every advertised name must match
// ^[a-z][a-z0-9_]*$, and no deprecated alias may be advertised by default.
//
// The first-party plugins the harness installs contribute tools of their own
// (runner, tesseract, torque). Their names are chosen in tangent-plugins and
// are renamed there, so they are reported rather than failed on here; see
// docs/mcp-tool-names.md. Only that exact set is excused, so a host tool that
// regressed to a dotted name still fails.
func TestShippedToolNamesMatchTheCharset(t *testing.T) {
	endpoint := bootShippedBinary(t)

	surface, finding := endpoint.StreamableSurface(context.Background())
	if finding != nil {
		t.Fatalf("derive shipped surface:\n%s", finding)
	}
	for _, name := range surface.Names {
		if isPluginOwnedLegacyName(name) {
			t.Logf("plugin-contributed tool %q still has a legacy name; rename it in tangent-plugins", name)
			continue
		}
		if !tangentmcp.ToolNamePattern.MatchString(name) {
			t.Errorf("shipped tool %q does not match %s", name, tangentmcp.ToolNamePattern)
			continue
		}
		legacy, ok := tangentmcp.LegacyToolName(name)
		if !ok {
			continue // introduced after the rename: nothing to alias
		}
		for _, other := range surface.Names {
			if other == legacy {
				t.Errorf("deprecated alias %q is advertised by default", legacy)
			}
		}
	}
}

func isPluginOwnedLegacyName(name string) bool {
	for _, prefix := range []string{"tangent.runner_", "tangent.tesseract_", "tangent.torque_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
