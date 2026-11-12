package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDatabaseCheckAndBackupCreateNothingUnexpected(t *testing.T) {
	data := t.TempDir()
	path := filepath.Join(data, "tangent.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE t(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	env := func(k string) string {
		if k == "TANGENT_DATA_DIR" {
			return data
		}
		return ""
	}
	var out bytes.Buffer
	ctx := context.Background()
	if err = databaseCommand(ctx, []string{"check"}, env, &out); err != nil || !strings.Contains(out.String(), "integrity ok") {
		t.Fatalf("check: %v\n%s", err, out.String())
	}
	dst := filepath.Join(t.TempDir(), "b.db")
	if err = databaseCommand(ctx, []string{"backup", "--to", dst}, env, &out); err != nil {
		t.Fatal(err)
	}
	if err = databaseCommand(ctx, []string{"backup", "--to", dst}, env, &out); err == nil {
		t.Fatal("a backup overwrote an existing file")
	}
	if err = databaseCommand(ctx, []string{"check", "--db", filepath.Join(data, "absent.db")}, env, &out); err == nil {
		t.Fatal("a missing database was checked")
	}
	if _, err = os.Stat(filepath.Join(data, "absent.db")); err == nil {
		t.Fatal("a check created the database it was asked about")
	}
}
