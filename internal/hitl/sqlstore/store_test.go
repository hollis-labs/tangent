package sqlstore_test

import (
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/mesh/core/hitl/hitltest"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/hitl/sqlstore"
)

func newStore(t *testing.T) hitl.Store {
	t.Helper()
	db, err := tangentdb.Open(filepath.Join(t.TempDir(), "tangent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tangentdb.Close(db) })
	if err := tangentdb.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	return sqlstore.New(db)
}

func TestStoreContract(t *testing.T) { hitltest.RunStoreContract(t, newStore) }

// TestServiceConformance runs the lib's Service over the SQLite store through
// every conformance scenario.
func TestServiceConformance(t *testing.T) {
	svc := hitl.NewService(newStore(t), hitl.Options{})
	hitltest.RunConformance(t, hitltest.NewServiceAdapter(svc), hitltest.ServiceCaps()...)
}

// TestSchemaTerminalStatesMatchLib walks the lib's states and checks the
// table's outcome/state CHECK agrees with State.IsTerminal, so a lib change
// can't silently break Swap.
func TestSchemaTerminalStatesMatchLib(t *testing.T) {
	db, err := tangentdb.Open(filepath.Join(t.TempDir(), "tangent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tangentdb.Close(db) })
	if err := tangentdb.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	insert := func(id string, state hitl.State, outcome any) error {
		_, err := db.Exec(`INSERT INTO hitl_store_records
(item_id, caller_scope, idempotency_key, digest, kind, request, state, revision, created_at, updated_at, outcome)
VALUES (?, 's', ?, 'd', 'approval', '{}', ?, 1, 't', 't', ?)`, id, id, string(state), outcome)
		return err
	}
	for _, state := range hitl.AllStates() {
		terminal := state.IsTerminal()
		withOutcome, without := insert("o-"+string(state), state, `{}`), insert("n-"+string(state), state, nil)
		if terminal && (withOutcome != nil || without == nil) {
			t.Errorf("%s is terminal in the lib: want outcome accepted and missing outcome rejected, got %v / %v", state, withOutcome, without)
		}
		if !terminal && (withOutcome == nil || without != nil) {
			t.Errorf("%s is nonterminal in the lib: want outcome rejected and missing outcome accepted, got %v / %v", state, withOutcome, without)
		}
	}
}
