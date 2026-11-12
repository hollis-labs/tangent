package dbtool_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hollis-labs/tangent/internal/dbtool"
)

func fixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`PRAGMA journal_mode=WAL;
		CREATE TABLE parent(id INTEGER PRIMARY KEY);
		CREATE TABLE child(id INTEGER PRIMARY KEY, p INTEGER REFERENCES parent(id));
		INSERT INTO parent VALUES (1); INSERT INTO child VALUES (1, 1);
		CREATE TABLE goose_db_version(id INTEGER PRIMARY KEY, version_id INTEGER, is_applied INTEGER);
		INSERT INTO goose_db_version(version_id, is_applied) VALUES (0,1),(2,1)`); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckIsReadOnlyAndReportsTheMigration(t *testing.T) {
	src := fixture(t)
	before, _ := os.ReadFile(src) // #nosec G304 -- a file in this test's temp directory.
	rep, err := dbtool.Check(context.Background(), src)
	if err != nil || !rep.OK() || rep.Migration != 2 {
		t.Fatalf("%+v, %v", rep, err)
	}
	after, _ := os.ReadFile(src) // #nosec G304 -- a file in this test's temp directory.
	if string(before) != string(after) {
		t.Fatal("a check changed the file")
	}
	if _, err = dbtool.Check(context.Background(), filepath.Join(t.TempDir(), "absent.db")); err == nil {
		t.Fatal("a missing file was checked")
	}
}

func TestBackupCopiesVerifiesAndRefusesToOverwrite(t *testing.T) {
	src := fixture(t)
	dst := filepath.Join(t.TempDir(), "sub", "backup.db")
	rep, err := dbtool.Backup(context.Background(), src, dst)
	if err != nil || !rep.OK() || rep.Migration != 2 {
		t.Fatalf("%+v, %v", rep, err)
	}
	info, err := os.Stat(dst)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup file: %v, %v", info, err)
	}
	if _, err = dbtool.Backup(context.Background(), src, dst); err == nil {
		t.Fatal("an existing backup was overwritten")
	}
	if _, err = dbtool.Backup(context.Background(), src, src); err == nil {
		t.Fatal("the source was accepted as the destination")
	}
}
