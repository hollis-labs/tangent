package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"
	_ "modernc.org/sqlite"
)

// dump is every schema object of db as "type|name\nsql\n--\n", sorted, without bookkeeping.
func dump(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT type, name, sql FROM sqlite_master
		WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\' AND name NOT IN ('schema_migrations','version_unique','goose_db_version') AND sql IS NOT NULL
		ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var typ, name, s string
		if err := rows.Scan(&typ, &name, &s); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s|%s\n%s\n--\n", typ, name, s)
	}
	return b.String()
}

func openNew(t *testing.T, path string) *store.Store {
	t.Helper()
	cfg := store.Config{Driver: store.DriverSQLite, SQLite: store.SQLiteConfig{Path: path, CreateParentDir: true}}
	st, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fsys, root := Migrations()
	if err := migrate.Ensure(context.Background(), cfg, st, migrate.Source{FS: fsys, Root: root}); err != nil {
		t.Fatal(err)
	}
	return st
}

// The new schema must stay equal, object for object and character for character, to the schema
// the pre-Substrate binary ended at (testdata/legacy_schema.txt, written from its 24 migrations).
func TestSchemaEqualsLegacy(t *testing.T) {
	st := openNew(t, filepath.Join(t.TempDir(), "new.db"))
	want, err := os.ReadFile("testdata/legacy_schema.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := dump(t, st.DB()); got != string(want) {
		t.Fatal("the schema differs from testdata/legacy_schema.txt (the pre-Substrate schema)")
	}
}

// makeLegacy builds a pre-Substrate database: the legacy schema, its bookkeeping row, and a few
// rows written with the triggers in place, as the old binary wrote them.
func makeLegacy(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	raw, err := os.ReadFile("testdata/legacy_schema.txt")
	if err != nil {
		t.Fatal(err)
	}
	var triggersAndViews, indexes, rest []string
	for _, block := range strings.Split(string(raw), "\n--\n") {
		head, body, ok := strings.Cut(block, "\n")
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(head, "trigger|") || strings.HasPrefix(head, "view|"):
			triggersAndViews = append(triggersAndViews, body)
		case strings.HasPrefix(head, "index|"):
			indexes = append(indexes, body)
		default:
			rest = append(rest, body)
		}
	}
	for _, s := range append(append(rest, indexes...), triggersAndViews...) {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	for _, s := range []string{
		`CREATE TABLE schema_migrations (version uint64, dirty bool)`,
		`CREATE UNIQUE INDEX version_unique ON schema_migrations (version)`,
		`INSERT INTO schema_migrations (version, dirty) VALUES (24, 0)`,
		`INSERT INTO surfaces (id, owner_scope, lifecycle_state, created_at, updated_at) VALUES ('s1','operator:local','active','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		// The arrival trigger writes inbox_order for each interaction: a copy that ran with the
		// triggers in place would write the row a second time.
		`INSERT INTO interactions (id, surface_id, caller_scope, caller_authority, caller_assurance, idempotency_key, surface_sequence, request_snapshot, lifecycle_state, created_at, updated_at)
		 VALUES ('i1','s1','standalone-local:x','standalone-local','local','k1',1,'{"a":1}','submitted','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z'),
		        ('i2','s1','standalone-local:x','standalone-local','local','k2',2,'{}','submitted','2026-01-01T00:00:01Z','2026-01-01T00:00:01Z')`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
}

func TestMigrateLegacy(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "old.db"), filepath.Join(dir, "new.db")
	makeLegacy(t, from)
	before, err := fingerprint(from)
	if err != nil {
		t.Fatal(err)
	}

	rep, err := MigrateLegacy(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows["interactions"] != 2 || rep.Rows["inbox_order"] != 2 || rep.Rows["surfaces"] != 1 {
		t.Fatalf("counts: %v", rep.Rows)
	}
	after, err := fingerprint(from)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("the old database was modified")
	}
	if _, err := os.Stat(from + "-shm"); err == nil {
		t.Fatal("the old database grew a -shm file")
	}
	st := openNew(t, to) // idempotent: already migrated, nothing to apply
	var seq int
	if err := st.DB().QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 'inbox_order'`).Scan(&seq); err != nil || seq != 2 {
		t.Fatalf("inbox_order sequence = %d, %v", seq, err)
	}
	// The triggers are live in the new file.
	if _, err := st.DB().Exec(`UPDATE interactions SET revision = 5 WHERE id = 'i1'`); err == nil {
		t.Fatal("the revision trigger is not in place")
	}
	if _, err := MigrateLegacy(context.Background(), from, to); err == nil {
		t.Fatal("a second run overwrote or accepted an existing target")
	}
}

func TestMigrateLegacyRefusals(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "old.db"), filepath.Join(dir, "new.db")
	makeLegacy(t, from)
	db, err := sql.Open("sqlite", from)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE schema_migrations SET version = 23`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err = MigrateLegacy(context.Background(), from, to); err == nil || !strings.Contains(err.Error(), "version 23") {
		t.Fatalf("an old schema version was accepted: %v", err)
	}
	if _, err = os.Stat(to); err == nil {
		t.Fatal("a refused migration left a target file")
	}
	if _, err = MigrateLegacy(context.Background(), from, from); err == nil {
		t.Fatal("from == to accepted")
	}
}

func TestMigrateLegacyChangedDuringMigration(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "old.db"), filepath.Join(dir, "new.db")
	makeLegacy(t, from)
	afterChecksHook = func() {
		db, err := sql.Open("sqlite", from)
		if err == nil {
			_, _ = db.Exec(`INSERT INTO surfaces (id, owner_scope, lifecycle_state, created_at, updated_at) VALUES ('s2','operator:local','active','2026-01-02T00:00:00Z','2026-01-02T00:00:00Z')`)
			_ = db.Close()
		}
	}
	t.Cleanup(func() { afterChecksHook = nil })
	if _, err := MigrateLegacy(context.Background(), from, to); err == nil || !strings.Contains(err.Error(), "changed during") {
		t.Fatalf("a change during the migration was not caught: %v", err)
	}
	if _, err := os.Stat(to); err == nil {
		t.Fatal("a failed migration left a target file")
	}
	if _, err := os.Stat(to + ".migrating"); err == nil {
		t.Fatal("a failed migration left its temporary file")
	}
}

func TestMigrateLegacyIsRestartableAfterACrash(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "old.db"), filepath.Join(dir, "new.db")
	makeLegacy(t, from)
	if err := os.WriteFile(to+".migrating", []byte("half a database from a crashed run"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateLegacy(context.Background(), from, to); err != nil {
		t.Fatalf("a stale temporary file blocked the migration: %v", err)
	}
}

func TestRefuseLegacy(t *testing.T) {
	dir := t.TempDir()
	if err := RefuseLegacy(filepath.Join(dir, "absent.db")); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "old.db")
	makeLegacy(t, old)
	if err := RefuseLegacy(old); err == nil || !strings.Contains(err.Error(), "migrate-data") {
		t.Fatalf("a legacy database was not refused: %v", err)
	}
	fresh := filepath.Join(dir, "new.db")
	openNew(t, fresh)
	if err := RefuseLegacy(fresh); err != nil {
		t.Fatalf("a Substrate-era database was refused: %v", err)
	}
}
