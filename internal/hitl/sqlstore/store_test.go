package sqlstore_test

import (
	"context"
	"database/sql"
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
