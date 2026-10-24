package boot

import (
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/artifacts"
	"github.com/hollis-labs/tangent/internal/docs"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/turns"
)

// The browser Inbox lists items from other authorities only on surfaces the policy reserves, so a
// reserved operator surface missing from the policy would silently hide everything sent to it.
func TestEveryReservedOperatorSurfaceIsCovered(t *testing.T) {
	policy := reservedSurfacePolicy()
	for _, id := range []string{hitl.DefaultSurfaceID, turns.DefaultSurfaceID, docs.DefaultSurfaceID, artifacts.DefaultSurfaceID} {
		if policy.Authorize(id, "") {
			t.Errorf("surface %q is not reserved: items an authenticated producer sends there stay hidden from the operator", id)
		}
	}
}

// The other direction: a surface the host does not reserve must stay open to generic callers and
// therefore stay private to its producer in the browser Inbox. A prefix match would break this.
func TestOrdinarySurfacesAreNotReserved(t *testing.T) {
	policy := reservedSurfacePolicy()
	for _, id := range []string{"some-producer-surface", "surface_docs_default_copy", strings.ToUpper(docs.DefaultSurfaceID), ""} {
		if !policy.Authorize(id, "") {
			t.Errorf("surface %q reads as reserved; it would be listed in the operator's browser", id)
		}
	}
}
