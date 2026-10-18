package gohitl_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	tangenthitl "github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/hitl/gohitl"
	"github.com/hollis-labs/tangent/internal/interaction"
)

func outboxKinds(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT kind FROM event_outbox ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var kinds []string
	for rows.Next() {
		var kind string
		if err = rows.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, kind)
	}
	return kinds
}

// An approval a native tool enqueued before the flag was flipped is withdrawn
// through the lib path: the lib finds it under the caller's derived scope,
// recovers its kind from the request, and the one approval.resolved is emitted.
func TestPreFlipNativeApprovalCanBeWithdrawnThroughTheLibAndEmitsOnce(t *testing.T) {
	ctx := context.Background()
	db, err := tangentdb.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tangentdb.Close(db) })
	if err = tangentdb.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	envelopes, err := envelope.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = extensions.RegisterHITLItem(envelopes); err != nil {
		t.Fatal(err)
	}
	store := interaction.NewStore(db)
	store.SetTransitionObserver(tangenthitl.EventObserver{})
	catalog := interaction.NewEnvelopeDefinitionCatalog(envelopes, "gohitl-test", interaction.WithRetainedMaterialStore(store))
	interactions, err := interaction.NewService(store, catalog,
		interaction.WithSurfaceAccessPolicy(tangenthitl.SurfaceAccessPolicy{}),
		interaction.WithPrivilegedActorPolicy(tangenthitl.ExpiryPolicy{}))
	if err != nil {
		t.Fatal(err)
	}
	native, err := tangenthitl.NewService(interactions)
	if err != nil {
		t.Fatal(err)
	}
	caller := interaction.ActorBinding{Scope: "standalone-local:app", PrincipalRef: "agent", Authority: "standalone-local", Assurance: "loopback-unverified"}
	handle, err := native.Enqueue(ctx, tangenthitl.EnqueueInput{Request: enqueueDoc("app", "pre-flip"), Caller: caller})
	if err != nil {
		t.Fatal(err)
	}

	wire := gohitl.New(db, catalog)
	withdrawDoc := mustJSON(map[string]any{
		"contract_version": "1.0", "item_id": handle.ItemID, "caller": map[string]any{"application_id": "app", "principal_ref": "agent"},
	})
	if body, withdrawErr := wire.Withdraw(ctx, caller, withdrawDoc); withdrawErr != nil {
		t.Fatalf("withdraw of a pre-flip native item through the lib: %v (%s)", withdrawErr, body)
	}
	if kinds := outboxKinds(t, db); len(kinds) != 2 || kinds[0] != "approval.requested" || kinds[1] != "approval.resolved" {
		t.Fatalf("outbox = %v, want exactly [approval.requested approval.resolved]", kinds)
	}
	var resolved string
	if err = db.QueryRow(`SELECT json_extract(envelope_json, '$.payload.outcome') FROM event_outbox WHERE kind = 'approval.resolved'`).Scan(&resolved); err != nil || resolved != "withdrawn" {
		t.Fatalf("resolved outcome = %q, %v", resolved, err)
	}
}
