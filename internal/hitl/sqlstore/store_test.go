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
