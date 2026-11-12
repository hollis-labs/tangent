// Package testdb opens a migrated Tangent database for a test: the schema the app ships, in a
// file the test owns.
package testdb

import (
	"context"
	"database/sql"
	"testing"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"

	"github.com/hollis-labs/tangent/internal/persistence"
)

// Open opens and migrates the database at path and closes it when the test ends.
func Open(t testing.TB, path string) *sql.DB {
	t.Helper()
	db, _ := OpenStore(t, path, true)
	return db
}

// OpenUnmigrated opens the database at path and applies nothing.
func OpenUnmigrated(t testing.TB, path string) *sql.DB {
	t.Helper()
	db, _ := OpenStore(t, path, false)
	return db
}

// OpenStore is Open with the store, and says whether to migrate.
func OpenStore(t testing.TB, path string, migrated bool) (*sql.DB, *store.Store) {
	t.Helper()
	cfg := store.Config{Driver: store.DriverSQLite, SQLite: store.SQLiteConfig{Path: path, CreateParentDir: true}}
	st, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if migrated {
		fsys, root := persistence.Migrations()
		if err = migrate.Ensure(context.Background(), cfg, st, migrate.Source{FS: fsys, Root: root}); err != nil {
			t.Fatalf("run migrations: %v", err)
		}
	}
	return st.DB(), st
}
