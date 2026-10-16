package sqlstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/mesh/core/hitl/hitltest"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/hitl/sqlstore"
	"github.com/hollis-labs/tangent/internal/interaction"
)

func newStore(t *testing.T) hitl.Store {
	t.Helper()
	_, store := newDBStore(t)
	return store
}

func newDBStore(t *testing.T) (*sql.DB, hitl.Store) {
	t.Helper()
	db, err := tangentdb.Open(filepath.Join(t.TempDir(), "tangent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tangentdb.Close(db) })
	if err := tangentdb.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	return db, sqlstore.New(db)
}

func TestStoreContract(t *testing.T) { hitltest.RunStoreContract(t, newStore) }

// TestServiceConformance runs the lib's Service over the SQLite store through
// every conformance scenario.
func TestServiceConformance(t *testing.T) {
	svc := hitl.NewService(newStore(t), hitl.Options{})
	hitltest.RunConformance(t, hitltest.NewServiceAdapter(svc), hitltest.ServiceCaps()...)
}

// TestRowsAreTangentInteractions checks the store writes real interaction
// rows that Tangent's own interaction store reads: presented at revision 4 (lib
// revision 1) and, once resolved, terminal with a resolutions row.
func TestRowsAreTangentInteractions(t *testing.T) {
	db, store := newDBStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	rec := hitl.Record{ItemID: "item-1", CallerScope: "app", IdempotencyKey: "k", Digest: "d", Kind: "approval",
		Request: []byte(`{"kind":"approval"}`), State: hitl.StatePresented, Revision: 1, CreatedAt: t0, UpdatedAt: t0}
	if _, created, err := store.Create(ctx, rec); err != nil || !created {
		t.Fatalf("Create = %v, %v", created, err)
	}
	row, err := interaction.NewStore(db).GetInteraction(ctx, "item-1")
	if err != nil || row.State != interaction.InteractionStatePresented || row.Revision != 4 {
		t.Fatalf("interaction = %+v, %v; want presented at revision 4", row, err)
	}
	next := rec
	next.State, next.Revision, next.UpdatedAt = hitl.StateResolved, 2, t0.Add(time.Second)
	next.Outcome = hitl.Resolved{ItemID: "item-1", InteractionRevision: 2, Resolution: hitl.ResolutionRecord{
		ResolutionID: "res-1", Response: hitl.Response{Kind: "approval", Decision: "approved"},
		Participant: hitl.Participant{Responder: &hitl.Responder{Kind: "human", Ref: "alice"}, Assurance: "asserted"},
		ResolvedAt:  next.UpdatedAt, InteractionRevision: 2}}
	if err = store.Swap(ctx, "item-1", 1, next); err != nil {
		t.Fatal(err)
	}
	row, err = interaction.NewStore(db).GetInteraction(ctx, "item-1")
	if err != nil || row.State != interaction.InteractionStateResolved || row.Revision != 5 || row.TerminalAt == nil {
		t.Fatalf("interaction = %+v, %v; want resolved at revision 5", row, err)
	}
	if res, resErr := interaction.NewStore(db).GetResolution(ctx, "item-1"); resErr != nil || res.ID != "res-1" || res.ParticipantRef != "alice" {
		t.Fatalf("resolution = %+v, %v", res, resErr)
	}
	got, err := store.Get(ctx, "item-1")
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := got.Outcome.(hitl.Resolved); !ok || r.Resolution.Participant.Responder == nil || r.Resolution.Participant.Responder.Kind != "human" {
		t.Fatalf("outcome = %#v; want the responder kind to round-trip", got.Outcome)
	}
}

// TestDueForExpirySkipsEveryTerminalState checks the expiry scan against the
// lib's own terminal states rather than a copied list.
func TestDueForExpirySkipsEveryTerminalState(t *testing.T) {
	_, store := newDBStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	due := t0.Add(-time.Hour)
	mk := func(id string) hitl.Record {
		return hitl.Record{ItemID: id, CallerScope: "app", IdempotencyKey: id, Digest: "d", Kind: "approval",
			Request: []byte(`{"kind":"approval"}`), State: hitl.StatePresented, Revision: 1, CreatedAt: t0, UpdatedAt: t0, ExpiresAt: &due}
	}
	outcomes := map[hitl.State]func(id string) hitl.Outcome{
		hitl.StateResolved: func(id string) hitl.Outcome {
			return hitl.Resolved{ItemID: id, InteractionRevision: 2, Resolution: hitl.ResolutionRecord{ResolutionID: "r-" + id,
				Response: hitl.Response{Kind: "approval", Decision: "approved"}, Participant: hitl.Participant{PrincipalRef: "p", Assurance: "asserted"},
				ResolvedAt: t0, InteractionRevision: 2}}
		},
		hitl.StateCanceled: func(id string) hitl.Outcome {
			return hitl.Canceled{ItemID: id, InteractionRevision: 2, Cause: hitl.CauseCallerWithdrawn, TerminatedAt: t0}
		},
		hitl.StateExpired: func(id string) hitl.Outcome {
			return hitl.Expired{ItemID: id, InteractionRevision: 2, PolicyRef: "request.expires_at", TerminatedAt: t0}
		},
	}
	if _, _, err := store.Create(ctx, mk("live")); err != nil {
		t.Fatal(err)
	}
	for state, outcome := range outcomes {
		id := string(state)
		rec := mk(id)
		if _, _, err := store.Create(ctx, rec); err != nil {
			t.Fatal(err)
		}
		next := rec
		next.State, next.Revision, next.Outcome = state, 2, outcome(id)
		if err := store.Swap(ctx, id, 1, next); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
	}
	got, err := store.DueForExpiry(ctx, t0, 0)
	if err != nil || len(got) != 1 || got[0].ItemID != "live" {
		t.Fatalf("DueForExpiry = %v, %v; want only the nonterminal item", got, err)
	}
}

// TestNativeInteractionPathKeepsTheRevisionOffset pins the constant offset
// against the rows Tangent's own store writes (the path tangent_hitl_enqueue
// takes): staged at revision 3, presented at 4 is lib revision 1, resolved at 5
// is lib revision 2, and a resolution written natively reads back as a lib
// Resolved outcome.
func TestNativeInteractionPathKeepsTheRevisionOffset(t *testing.T) {
	db, store := newDBStore(t)
	ctx := context.Background()
	native := interaction.NewStore(db)
	surface, err := native.CreateSurface(ctx, interaction.CreateSurfaceParams{ID: sqlstore.DefaultSurfaceID, OwnerScope: "operator:local"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.AdvanceSurface(ctx, interaction.AdvanceSurfaceParams{
		SurfaceID: surface.ID, ExpectedRevision: surface.Revision, To: interaction.SurfaceStateActive}); err != nil {
		t.Fatal(err)
	}
	created, err := native.CreateInteraction(ctx, interaction.CreateInteractionParams{
		ID: "native-1", SurfaceID: surface.ID, CallerScope: "app", CallerAuthority: "a", CallerAssurance: "b", IdempotencyKey: "k",
		RequestSnapshot: json.RawMessage(`{"kind":"approval"}`), Policy: json.RawMessage(`{"request_digest":"d"}`),
		Definition: interaction.DefinitionBinding{Publisher: "tangent", Kind: "hitl_item", Version: "v1", Revision: 1, Source: "test", Assurance: "x"},
	})
	if err != nil || created.Interaction.Revision != 3 {
		t.Fatalf("native create = %+v, %v; want staged at revision 3", created.Interaction, err)
	}
	presented, err := native.AdvanceInteraction(ctx, interaction.AdvanceInteractionParams{
		InteractionID: "native-1", ExpectedRevision: 3, To: interaction.InteractionStatePresented, PresentedProjectionRevision: 1,
		ParticipantScope: "operator:local", ParticipantRef: "op", ParticipantAuthority: "loopback", ParticipantAssurance: "unverified"})
	if err != nil || presented.Revision != 4 {
		t.Fatalf("native present = %+v, %v; want revision 4", presented, err)
	}
	got, err := store.Get(ctx, "native-1")
	if err != nil || got.State != hitl.StatePresented || got.Revision != 1 || got.Digest != "d" {
		t.Fatalf("lib view = %+v, %v; want presented at lib revision 1", got, err)
	}
	if _, err = native.ResolveInteraction(ctx, interaction.ResolveInteractionParams{
		InteractionID: "native-1", ExpectedInteractionRevision: 4, PresentedProjectionRevision: 1,
		ParticipantScope: "operator:local", ParticipantRef: "op", ParticipantAuthority: "loopback", ParticipantAssurance: "unverified",
		ResponseKind: "approval", ResponsePayload: json.RawMessage(`{"kind":"approval","decision":"approved"}`),
		Deliveries: []interaction.ResolutionDeliveryParams{{IdempotencyKey: "d", DestinationBinding: json.RawMessage(`{}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, "native-1")
	if err != nil || got.State != hitl.StateResolved || got.Revision != 2 {
		t.Fatalf("lib view = %+v, %v; want resolved at lib revision 2", got, err)
	}
	if r, ok := got.Outcome.(hitl.Resolved); !ok || r.InteractionRevision != 2 || r.Resolution.Response.Decision != "approved" ||
		r.Resolution.Participant.PrincipalRef != "op" {
		t.Fatalf("outcome = %#v; want a Resolved rebuilt from the native resolutions row", got.Outcome)
	}
}

// TestForeignSurfaceInteractionsAreInvisible checks that interactions on other
// surfaces (docs, turns) can't be read, swapped or replayed through the store.
func TestForeignSurfaceInteractionsAreInvisible(t *testing.T) {
	db, store := newDBStore(t)
	ctx := context.Background()
	native := interaction.NewStore(db)
	docs, err := native.CreateSurface(ctx, interaction.CreateSurfaceParams{ID: "surface_docs", OwnerScope: "operator:local"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.AdvanceSurface(ctx, interaction.AdvanceSurfaceParams{
		SurfaceID: docs.ID, ExpectedRevision: docs.Revision, To: interaction.SurfaceStateActive}); err != nil {
		t.Fatal(err)
	}
	if _, err = native.CreateInteraction(ctx, interaction.CreateInteractionParams{
		ID: "docs-1", SurfaceID: docs.ID, CallerScope: "app", CallerAuthority: "a", CallerAssurance: "b", IdempotencyKey: "shared-key",
		RequestSnapshot: json.RawMessage(`{"kind":"doc"}`),
		Definition:      interaction.DefinitionBinding{Publisher: "tangent", Kind: "docs", Version: "v1", Revision: 1, Source: "test", Assurance: "x"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Get(ctx, "docs-1"); !errors.Is(err, hitl.ErrNotFound) {
		t.Fatalf("Get(docs item) = %v, want ErrNotFound", err)
	}
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	next := hitl.Record{ItemID: "docs-1", CallerScope: "app", IdempotencyKey: "shared-key", Digest: "d", Kind: "approval",
		Request: []byte(`{"kind":"doc"}`), State: hitl.StateCanceled, Revision: 2, CreatedAt: t0, UpdatedAt: t0,
		Outcome: hitl.Canceled{ItemID: "docs-1", InteractionRevision: 2, Cause: hitl.CauseCallerWithdrawn, TerminatedAt: t0}}
	if err = store.Swap(ctx, "docs-1", 1, next); !errors.Is(err, hitl.ErrNotFound) {
		t.Fatalf("Swap(docs item) = %v, want ErrNotFound", err)
	}
	replay := next
	replay.ItemID, replay.State, replay.Revision, replay.Outcome = "hitl-1", hitl.StatePresented, 1, nil
	if got, created, createErr := store.Create(ctx, replay); createErr == nil || created || got.ItemID == "docs-1" {
		t.Fatalf("Create reusing a docs-surface key = %+v, %v, %v; want an error and no replay of the docs row", got, created, createErr)
	}
	row, err := native.GetInteraction(ctx, "docs-1")
	if err != nil || row.State != interaction.InteractionStateStaged || row.Revision != 3 {
		t.Fatalf("docs interaction changed: %+v, %v", row, err)
	}
}
