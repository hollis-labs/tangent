package almanac

import (
	"context"
	"os"
	"testing"
)

// A live check against the real Almanac, off by default.
//
// FILL THIS IN FIRST, BEFORE THE MAPPING. It is here rather than in a scratch
// program because `internal/` packages cannot be imported from outside the
// module, and because the thing worth checking is THIS client's own bytes
// against the running service — not a curl that happens to resemble them.
//
// Both application plugins that came before this one were handed route names
// that turned out to be wrong, and in both cases only a call from this code
// would have caught it. An application's MCP tool descriptions, its README and a
// task's brief are all secondary sources. The service is the source.
//
// Run it with TANGENT_ALMANAC_LIVE_TEST=1. It is skipped otherwise, so `make test`
// never depends on a service being up. Keep it read-only unless a destructive
// step is opt-in behind its own variable and names its target explicitly — a
// test that picked its own write target will eventually pick a real record.
func TestLiveAlmanac(t *testing.T) {
	if os.Getenv("TANGENT_ALMANAC_LIVE_TEST") == "" {
		t.Skip("set TANGENT_ALMANAC_LIVE_TEST=1 to run against the real Almanac")
	}
	// Through the production constructor, so the environment override this
	// reads is the one the shipped plugin reads.
	client := New().client
	ctx := context.Background()

	if err := client.Health(ctx); err != nil {
		t.Fatalf("health against %s: %v", client.BaseURL(), err)
	}
	t.Logf("health ok at %s", client.BaseURL())

	filters := ListFilters{Statuses: ActiveStatuses}
	page, err := client.List(ctx, filters)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Records) > DefaultCards {
		t.Fatalf("list returned %d records past a cap of %d; the probe row leaked into the board",
			len(page.Records), DefaultCards)
	}
	t.Logf("list: %d record(s), more=%v", len(page.Records), page.More)
	// Print the sentence a participant will read. A scope line is prose, and
	// prose is checked by reading it.
	t.Logf("scope: %s", ScopeSentence(filters, page))
}
