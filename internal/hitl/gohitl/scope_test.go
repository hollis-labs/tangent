package gohitl_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/hitl/gohitl"
	"github.com/hollis-labs/tangent/internal/interaction"
)

func newWire(t *testing.T) *gohitl.Wire {
	t.Helper()
	db, err := tangentdb.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tangentdb.Close(db) })
	if err = tangentdb.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	envelopes, err := envelope.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = extensions.RegisterHITLItem(envelopes); err != nil {
		t.Fatal(err)
	}
	return gohitl.New(db, interaction.NewEnvelopeDefinitionCatalog(envelopes, "gohitl-test",
		interaction.WithRetainedMaterialStore(interaction.NewStore(db))))
}

func enqueueDoc(app, key string) []byte {
	doc, _ := json.Marshal(map[string]any{
		"contract_version": "1.0", "kind": "approval", "idempotency_key": key, "title": "T", "summary": "S",
		"request": "Approve?", "source": map[string]any{"application_id": app, "agent_id": "agent"},
	})
	return doc
}

func getDoc(app, itemID string) []byte {
	doc, _ := json.Marshal(map[string]any{
		"contract_version": "1.0", "item_id": itemID, "caller": map[string]any{"application_id": app, "principal_ref": "agent"},
	})
	return doc
}

// Two verified producers that assert the same application id are different
// callers: the lib only sees the asserted id, so the wire compares the derived
// scope. One cannot read the other's item, and the same idempotency key from
// each is two items.
func TestVerifiedProducersAssertingTheSameAppIDAreIsolated(t *testing.T) {
	ctx := context.Background()
	wire := newWire(t)
	replyA, err := wire.Enqueue(ctx, "gateway:producer-a", enqueueDoc("shared-app", "k1"))
	if err != nil {
		t.Fatalf("enqueue a: %v (%s)", err, replyA)
	}
	var a struct {
		ItemID string `json:"item_id"`
	}
	_ = json.Unmarshal(replyA, &a)

	if _, err = wire.Get(ctx, "gateway:producer-a", getDoc("shared-app", a.ItemID)); err != nil {
		t.Fatalf("the owner must read its item: %v", err)
	}
	body, err := wire.Get(ctx, "gateway:producer-b", getDoc("shared-app", a.ItemID))
	if err == nil || !strings.Contains(string(body), "not_found") {
		t.Fatalf("producer b read producer a's item: err=%v body=%s", err, body)
	}
	if _, err = wire.Withdraw(ctx, "gateway:producer-b", mustJSON(map[string]any{
		"contract_version": "1.0", "item_id": a.ItemID, "caller": map[string]any{"application_id": "shared-app", "principal_ref": "agent"},
	})); err == nil {
		t.Fatal("producer b withdrew producer a's item")
	}

	replyB, err := wire.Enqueue(ctx, "gateway:producer-b", enqueueDoc("shared-app", "k1"))
	if err != nil {
		t.Fatalf("enqueue b: %v (%s)", err, replyB)
	}
	var b struct {
		ItemID string `json:"item_id"`
	}
	_ = json.Unmarshal(replyB, &b)
	if b.ItemID == "" || b.ItemID == a.ItemID {
		t.Fatalf("the same idempotency key from another verified producer must be its own item, got %q vs %q", b.ItemID, a.ItemID)
	}
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}
