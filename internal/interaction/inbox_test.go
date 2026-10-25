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
		// The gateway item (index 2) is the operator's too: the verified gateway is how the
		// operator's own agents reach them, so it is listed with the local ones.
		ids = append(ids, created.Interaction.ID)
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
	if err != nil || len(entries) != 3 {
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
	if err != nil || len(listed) != 2 {
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
	if _, err := service.BrowserInboxItem(ctx, foreign); err != nil {
		t.Fatalf("a verified-gateway interaction is the operator's and must open, got %v", err)
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

// reservedInboxPolicy reserves one surface for host services, like the docs and
// artifact inbox surfaces: writing there needs a capability only the host holds.
type reservedInboxPolicy struct{ reserved string }

func (p reservedInboxPolicy) Authorize(surfaceID, capability string) bool {
	return surfaceID != p.reserved || capability == "host-capability"
}

func (reservedInboxPolicy) AuthorizeInboxRead(string) bool { return true }

// An agent that reaches Tangent through the authenticated gateway is the operator's own agent, and
// what it sends is addressed to the operator. The operator's browser must list it wherever it lands:
// on a reserved inbox surface (docs, artifacts) and on a room's own surface (triage, diff review,
// forms), or a whole class of agent requests is invisible (CW-20261025-0002). The gateway authority
// is host-assigned from an authenticated token, so a local caller cannot claim it.
func TestBrowserInboxListsGatewayItemsOnEverySurface(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	service, err := NewService(store, testDefinitionCatalog{},
		WithSurfaceAccessPolicy(reservedInboxPolicy{reserved: "reserved-inbox"}),
		WithPrivilegedActorPolicy(testPrivilegedActorPolicy{}),
		WithDeliveryWorkerPolicy(testDeliveryWorkerPolicy{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	reserved := createTestSurface(t, store, "reserved-inbox")
	generic := createTestSurface(t, store, "generic-surface")
	mk := func(surface SurfaceRecord, scope string) string {
		// A distinct idempotency key per surface: the same key from one scope is a replay.
		created, createErr := store.CreateInteraction(ctx, testInteractionParams(surface.ID, scope, surface.ID+"|"+scope, "request"))
		if createErr != nil {
			t.Fatal(createErr)
		}
		return created.Interaction.ID
	}
	gatewayReserved := mk(reserved, "gateway:tether-gateway")
	gatewayGeneric := mk(generic, "gateway:tether-gateway")
	localGeneric := mk(generic, "standalone-local:a")

	entries, err := service.BrowserInbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, entry := range entries {
		listed[entry.Interaction.ID] = true
	}
	for name, id := range map[string]string{
		"a gateway item on a reserved operator surface": gatewayReserved,
		"a gateway item on a room's own surface":        gatewayGeneric,
		"a local item":                                  localGeneric,
	} {
		if !listed[id] {
			t.Errorf("%s is not listed", name)
		}
		// The keyed read used when one item is opened must agree with the list.
		if _, openErr := service.BrowserInboxItem(ctx, id); openErr != nil {
			t.Errorf("opening %s: %v", name, openErr)
		}
	}
	if _, err = service.BrowserInboxItem(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown id = %v, want ErrNotFound", err)
	}
}
