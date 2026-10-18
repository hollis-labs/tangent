package sqlstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"

	tangenthitl "github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/hitl/sqlstore"
	"github.com/hollis-labs/tangent/internal/interaction"
)

// nativeService is Tangent's own HITL service over the same database and the
// same definition catalog the store uses, with the approval event observer
// installed as the running service installs it.
func nativeService(t *testing.T, db *sql.DB) *tangenthitl.Service {
	t.Helper()
	store := interaction.NewStore(db)
	store.SetTransitionObserver(tangenthitl.EventObserver{})
	interactions, err := interaction.NewService(
		store, newCatalog(t, db),
		interaction.WithSurfaceAccessPolicy(tangenthitl.SurfaceAccessPolicy{}),
		interaction.WithPrivilegedActorPolicy(tangenthitl.ExpiryPolicy{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	service, err := tangenthitl.NewService(interactions)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func storeCreated(t *testing.T, store *sqlstore.Store, id string) hitl.Record {
	t.Helper()
	t0 := time.Now().UTC().Truncate(time.Second)
	request, _ := json.Marshal(map[string]any{
		"contract_version": "1.0", "kind": "approval", "idempotency_key": id, "title": "T", "summary": "S",
		"request": "Approve?", "source": map[string]any{"application_id": "app", "agent_id": "agent"},
	})
	rec := hitl.Record{ItemID: id, CallerScope: "standalone-local:app", IdempotencyKey: id, Digest: "d", Kind: "approval",
		Request: request, State: hitl.StatePresented, Revision: 1, CreatedAt: t0, UpdatedAt: t0}
	if _, created, err := store.Create(context.Background(), rec); err != nil || !created {
		t.Fatalf("Create = %v, %v", created, err)
	}
	return rec
}

func dbRevision(t *testing.T, db *sql.DB, id string) int64 {
	t.Helper()
	var revision int64
	if err := db.QueryRow(`SELECT revision FROM interactions WHERE id = ?`, id).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

// A row the go-hitl store created must be a HITL item to Tangent: it shows in
// the inbox and Tangent's own Resolve (the operator's path) resolves it, with
// the approval events and the delivery obligation the native path writes.
func TestStoreCreatedRowResolvesThroughTangentsOwnPath(t *testing.T) {
	ctx := context.Background()
	db, lib := newDBStore(t)
	native := nativeService(t, db)
	storeCreated(t, lib.(*sqlstore.Store), "item-store-1")

	inbox, err := native.Inbox(ctx)
	if err != nil || len(inbox.Pending) != 1 || inbox.Pending[0].ItemID != "item-store-1" {
		t.Fatalf("inbox = %+v, %v; the store-created item must be pending", inbox, err)
	}
	pending := inbox.Pending[0]
	if pending.PresentedProjectionRevision == nil {
		t.Fatal("a presented store row must carry a presentation revision")
	}
	if _, err = native.Resolve(ctx, tangenthitl.ResolveInput{
		ItemID: "item-store-1", ExpectedRevision: dbRevision(t, db, "item-store-1"),
		PresentedProjectionRevision: *pending.PresentedProjectionRevision,
		Response:                    json.RawMessage(`{"kind":"approval","decision":"approved"}`),
	}); err != nil {
		t.Fatalf("Tangent's Resolve could not resolve a store-created row: %v", err)
	}

	// The lib sees the outcome the native path recorded.
	got, err := lib.Get(ctx, "item-store-1")
	if err != nil || got.State != hitl.StateResolved {
		t.Fatalf("lib Get = %+v, %v", got, err)
	}
	if resolved, ok := got.Outcome.(hitl.Resolved); !ok || resolved.Resolution.Response.Decision != "approved" {
		t.Fatalf("outcome = %#v", got.Outcome)
	}

	// Approval events: one requested (from the store), one resolved (from the
	// native observer), never doubled.
	var kinds []string
	rows, err := db.Query(`SELECT kind FROM event_outbox ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind string
		if err = rows.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, kind)
	}
	if len(kinds) != 2 || kinds[0] != "approval.requested" || kinds[1] != "approval.resolved" {
		t.Fatalf("outbox kinds = %v, want [approval.requested approval.resolved]", kinds)
	}
	var deliveries int
	if err = db.QueryRow(`SELECT COUNT(*) FROM resolution_deliveries`).Scan(&deliveries); err != nil || deliveries != 1 {
		t.Fatalf("resolution deliveries = %d, %v; the native resolve queues exactly one", deliveries, err)
	}
}

// The store resolving a row (lib Respond) leaves the same durable delivery
// rows the native resolve leaves.
func TestStoreSwapQueuesTheSameDeliveryObligations(t *testing.T) {
	ctx := context.Background()
	db, lib := newDBStore(t)
	rec := storeCreated(t, lib.(*sqlstore.Store), "item-swap-1")
	now := time.Now().UTC().Truncate(time.Second)
	next := rec
	next.State, next.Revision = hitl.StateResolved, 2
	next.UpdatedAt = now
	next.Outcome = hitl.Resolved{ItemID: rec.ItemID, InteractionRevision: 2, Resolution: hitl.ResolutionRecord{
		ResolutionID: "res-1", Response: hitl.Response{Kind: "approval", Decision: "denied"},
		Participant: hitl.Participant{PrincipalRef: "local-operator", Assurance: "asserted"}, ResolvedAt: now, InteractionRevision: 2,
	}}
	if err := lib.Swap(ctx, rec.ItemID, 1, next); err != nil {
		t.Fatal(err)
	}
	var deliveries, events int
	_ = db.QueryRow(`SELECT COUNT(*) FROM resolution_deliveries WHERE resolution_id = 'res-1' AND lifecycle_state = 'queued'`).Scan(&deliveries)
	_ = db.QueryRow(`SELECT COUNT(*) FROM delivery_events`).Scan(&events)
	if deliveries != 1 || events != 1 {
		t.Fatalf("deliveries=%d delivery_events=%d, want 1 and 1", deliveries, events)
	}

	// A cancel queues a terminal notification.
	cancelRec := storeCreated(t, lib.(*sqlstore.Store), "item-swap-2")
	cancelNext := cancelRec
	cancelNext.State, cancelNext.Revision = hitl.StateCanceled, 2
	cancelNext.UpdatedAt = now
	cancelNext.Outcome = hitl.Canceled{ItemID: cancelRec.ItemID, InteractionRevision: 2, Cause: hitl.CauseCallerWithdrawn, TerminatedAt: now}
	if err := lib.Swap(ctx, cancelRec.ItemID, 1, cancelNext); err != nil {
		t.Fatal(err)
	}
	var notifications int
	_ = db.QueryRow(`SELECT COUNT(*) FROM terminal_notifications WHERE interaction_id = 'item-swap-2' AND terminal_cause = 'caller_withdrawn'`).Scan(&notifications)
	if notifications != 1 {
		t.Fatalf("terminal notifications = %d, want 1", notifications)
	}
}

// The lib revision is interactions.revision - 3. This pins it for the rows the
// native path writes (staged at 3, presented at 4, resolved at 5) and for the
// rows the store writes (presented at 4, resolved at 5), so neither path can
// drift from the offset the store applies.
func TestRevisionOffsetHoldsForNativeAndStoreRows(t *testing.T) {
	ctx := context.Background()
	db, lib := newDBStore(t)
	native := nativeService(t, db)

	request, _ := json.Marshal(map[string]any{
		"contract_version": tangenthitl.ContractVersion, "kind": "approval", "idempotency_key": "native-1", "title": "T",
		"summary": "S", "request": "Approve?", "source": map[string]any{"application_id": "app", "agent_id": "agent"},
	})
	caller := interaction.ActorBinding{Scope: "standalone-local:app", PrincipalRef: "agent", Authority: "standalone-local", Assurance: "loopback-unverified"}
	handle, err := native.Enqueue(ctx, tangenthitl.EnqueueInput{Request: request, Caller: caller})
	if err != nil {
		t.Fatal(err)
	}
	assertLib := func(id string, wantDB int64, wantState hitl.State) {
		t.Helper()
		if got := dbRevision(t, db, id); got != wantDB {
			t.Fatalf("%s interactions.revision = %d, want %d", id, got, wantDB)
		}
		rec, getErr := lib.Get(ctx, id)
		if getErr != nil {
			t.Fatalf("lib Get %s: %v", id, getErr)
		}
		if rec.Revision != wantDB-3 || rec.State != wantState {
			t.Fatalf("%s lib = rev %d state %s, want rev %d state %s", id, rec.Revision, rec.State, wantDB-3, wantState)
		}
	}
	// native: staged is interactions revision 3 = lib revision 0
	assertLib(handle.ItemID, 3, hitl.StateStaged)
	presented, err := native.Present(ctx, tangenthitl.PresentInput{ItemID: handle.ItemID, ExpectedRevision: handle.Revision, PresentedProjectionRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	assertLib(handle.ItemID, 4, hitl.StatePresented)
	if _, err = native.Resolve(ctx, tangenthitl.ResolveInput{
		ItemID: handle.ItemID, ExpectedRevision: presented.Revision, PresentedProjectionRevision: 1,
		Response: json.RawMessage(`{"kind":"approval","decision":"denied"}`),
	}); err != nil {
		t.Fatal(err)
	}
	assertLib(handle.ItemID, 5, hitl.StateResolved)

	// store: presented at interactions revision 4 = lib revision 1, then the
	// native resolve lands on 5 = lib 2.
	storeCreated(t, lib.(*sqlstore.Store), "item-offset-1")
	assertLib("item-offset-1", 4, hitl.StatePresented)
	if _, err = native.Resolve(ctx, tangenthitl.ResolveInput{
		ItemID: "item-offset-1", ExpectedRevision: 4, PresentedProjectionRevision: 1,
		Response: json.RawMessage(`{"kind":"approval","decision":"approved"}`),
	}); err != nil {
		t.Fatal(err)
	}
	assertLib("item-offset-1", 5, hitl.StateResolved)
}
