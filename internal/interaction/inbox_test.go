package interaction

import (
	"context"
	"encoding/json"
	"errors"
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
	if err = restarted.HideBrowserInboxItem(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err = service.HideBrowserInboxItem(ctx, ids[1]); err == nil {
		t.Fatal("hid an active request")
	}
	entries, err = service.BrowserInbox(ctx)
	if err != nil || len(entries) != 2 {
		t.Fatalf("durable hidden inbox: %v, %v", entries, err)
	}
	hidden, err := service.HiddenInboxItems(ctx)
	if err != nil || !hidden[ids[0]] {
		t.Fatal("lost persistent visibility metadata")
	}

}

func TestBrowserInboxItemMatchesTheListedEntryAndHonorsVisibility(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	var visible, foreign string
	for index, scope := range []string{"standalone-local:a", "gateway:foreign"} {
		surface := createTestSurface(t, store, "item-surface-"+scope)
		created, err := store.CreateInteraction(ctx, testInteractionParams(surface.ID, scope, scope, "request"))
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			visible = created.Interaction.ID
			presented, err := store.AdvanceInteraction(ctx, AdvanceInteractionParams{
				InteractionID: visible, ExpectedRevision: created.Interaction.Revision,
				To: InteractionStatePresented, PresentedProjectionRevision: 9,
				ParticipantScope: "operator:local", ParticipantRef: "local-operator",
				ParticipantAuthority: "local", ParticipantAssurance: "loopback-unverified",
				ConnectionID: "connection-1", Authority: "loopback-unverified",
			})
			if err != nil {
				t.Fatal(err)
			}
			resolution := resolutionParams(presented, nil)
			resolution.ResponsePayload = json.RawMessage(`{"reply":"kept"}`)
			if _, err := store.ResolveInteraction(ctx, resolution); err != nil {
				t.Fatal(err)
			}
		} else {
			foreign = created.Interaction.ID
		}
	}
	listed, err := service.BrowserInbox(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed = %d, %v", len(listed), err)
	}
	keyed, err := service.BrowserInboxItem(ctx, visible)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(listed[0])
	got, _ := json.Marshal(keyed)
	if string(want) != string(got) {
		t.Fatalf("keyed entry differs from the listed one:\n%s\n%s", got, want)
	}
	if _, err := service.BrowserInboxItem(ctx, foreign); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a not-visible interaction must be not found, got %v", err)
	}
	if _, err := service.BrowserInboxItem(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id = %v", err)
	}
	if err := service.HideBrowserInboxItem(ctx, visible); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BrowserInboxItem(ctx, visible); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a hidden item must be not found, got %v", err)
	}
}
