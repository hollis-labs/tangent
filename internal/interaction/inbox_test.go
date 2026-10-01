package interaction

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

func TestBrowserInboxOrdersAcrossSurfacesAndPreservesReplies(t *testing.T) {
	ctx := context.Background()
	store, database := openTestStore(t)
	service := newTestService(t, store)
	// Identical clocks demonstrate ordering comes from transactional arrival,
	// not timestamps, surface-local sequences, or lexicographic UUIDs.
	instant := time.Now().UTC()
	store.now = func() time.Time { return instant }
	ids := []string{}
	for index, scope := range []string{"standalone-local:a", "standalone-local:b", "gateway:foreign", "standalone-local:c"} {
		surface := createTestSurface(t, store, "inbox-surface-"+scope)
		params := testInteractionParams(surface.ID, scope, scope, "request")
		created, err := store.CreateInteraction(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		if index != 2 {
			ids = append(ids, created.Interaction.ID)
		}
		if index == 0 {
			presented, err := store.AdvanceInteraction(ctx, AdvanceInteractionParams{
				InteractionID: created.Interaction.ID, ExpectedRevision: created.Interaction.Revision,
				To: InteractionStatePresented, PresentedProjectionRevision: 9,
				ParticipantScope: "operator:local", ParticipantRef: "local-operator",
				ParticipantAuthority: "local", ParticipantAssurance: "loopback-unverified",
				ConnectionID: "connection-1", Authority: "loopback-unverified",
			})
			if err != nil {
				t.Fatal(err)
			}
			resolution := resolutionParams(presented, nil)
			resolution.ResponsePayload = json.RawMessage(`{"reply":"Keep this answer for later"}`)
			if _, err := store.ResolveInteraction(ctx, resolution); err != nil {
				t.Fatal(err)
			}
		}
	}
	entries, err := service.BrowserInbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(ids) {
		t.Fatalf("visible entries = %d, want %d", len(entries), len(ids))
	}
	for index, entry := range entries {
		if entry.Interaction.ID != ids[index] {
			t.Fatalf("entry %d out of order", index)
		}
		if index > 0 && entry.Sequence <= entries[index-1].Sequence {
			t.Fatal("arrival order not monotonic")
		}
	}
	if entries[0].Resolution == nil || string(entries[0].Resolution.ResponsePayload) != `{"reply":"Keep this answer for later"}` {
		t.Fatal("confirmed reply missing")
	}
	reopened, err := tangentdb.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	restarted := newTestService(t, NewStore(reopened))
	again, err := restarted.BrowserInbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Resolution == nil || again[0].Sequence != entries[0].Sequence || string(again[0].Resolution.ResponsePayload) != string(entries[0].Resolution.ResponsePayload) {
		t.Fatal("restart lost response or FIFO identity")
	}
}
