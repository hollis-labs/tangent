package smoke_test

import (
	"os/exec"
	"strings"
	"testing"
)

// This test holds the import boundary between Tangent's host and the plugins
// written against it (CW-20260930-0102). It asserts structure — which packages
// a closure reaches — not a count and not a file's content. The plugins
// themselves live in github.com/hollis-labs/tangent-plugins, where Go's
// internal rule already keeps them out of tangent/internal.

const internalPrefix = "github.com/hollis-labs/tangent/internal/"

// goListDeps returns the transitive import closure of pattern, as `go list
// -deps` reports it from the module root.
func goListDeps(t *testing.T, pattern string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pattern) // #nosec G204 -- the binary is go and pattern is a literal from this file.
	cmd.Dir = repoRoot()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", pattern, err, out)
	}
	return strings.Fields(string(out))
}

// TestPublicPluginSurfaceIsALeaf is the half Go does not enforce. The internal
// rule stops a package OUTSIDE this module importing internal/, but pkg/plugin
// is inside it and may import internal/ freely — and then a plugin in another
// module importing pkg/plugin would compile Tangent's host internals without a
// single visible import of them, and pin them through the tangent version it
// requires. So the public surface's whole closure must stay clear of internal/.
func TestPublicPluginSurfaceIsALeaf(t *testing.T) {
	deps := goListDeps(t, "./pkg/plugin/...")
	if len(deps) == 0 {
		t.Fatal("go list -deps ./pkg/plugin/... listed nothing; the check would pass vacuously")
	}
	for _, dep := range deps {
		if strings.HasPrefix(dep, internalPrefix) {
			t.Errorf("pkg/plugin reaches %s; the public plugin surface must not depend on host internals", dep)
		}
	}
}
