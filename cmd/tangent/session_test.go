package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/participant"
)

// TestSessionRevokeCLI exercises `tangent session revoke` against a temp
// database: by id prefix, then --all, printing counts only.
func TestSessionRevokeCLI(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tangent.db")
	database, err := tangentdb.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if migrateErr := tangentdb.RunMigrations(database); migrateErr != nil {
		t.Fatalf("migrate: %v", migrateErr)
	}
	store, err := participant.NewStore(database)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ctx := context.Background()
	var ids []string
	var cookies []string
	for range 3 {
		session, cookie, mintErr := store.Mint(ctx, participant.DefaultTemplate)
		if mintErr != nil {
			t.Fatalf("mint: %v", mintErr)
		}
		ids = append(ids, session.ID)
		cookies = append(cookies, cookie)
	}
	_ = tangentdb.Close(database)

	run := func(args ...string) (string, string, int) {
		var stdout, stderr bytes.Buffer
		code := runSessionCommand(args, dbPath, &stdout, &stderr)
		return stdout.String(), stderr.String(), code
	}

	// Refusals: no selector, both selectors, a short or non-hex prefix.
	for _, args := range [][]string{
		{"revoke"},
		{"revoke", "--all", "--id-prefix", ids[0][:8]},
		{"revoke", "--id-prefix", "abc"},
		{"revoke", "--id-prefix", "zzzzzzzz"},
		{"revoke", "--id-prefix", "%%%%%%%%"},
	} {
		if _, _, code := run(args...); code != 2 {
			t.Errorf("tangent session %v exit = %d, want 2", args, code)
		}
	}

	stdout, stderr, code := run("revoke", "--id-prefix", ids[0][:8])
	if code != 0 {
		t.Fatalf("revoke by prefix exit = %d; stderr: %s", code, stderr)
	}
	if strings.TrimSpace(stdout) != "revoked 1 session(s)" {
		t.Fatalf("revoke by prefix printed %q", stdout)
	}

	// Revoking the same prefix again finds nothing live.
	if stdout, _, _ = run("revoke", "--id-prefix", ids[0][:8]); strings.TrimSpace(stdout) != "revoked 0 session(s)" {
		t.Fatalf("second revoke printed %q", stdout)
	}

	stdout, stderr, code = run("revoke", "--all")
	if code != 0 {
		t.Fatalf("revoke --all exit = %d; stderr: %s", code, stderr)
	}
	if strings.TrimSpace(stdout) != "revoked 2 session(s)" {
		t.Fatalf("revoke --all printed %q", stdout)
	}

	// list shows id prefixes and states, never a cookie or a full id.
	stdout, _, code = run("list")
	if code != 0 {
		t.Fatalf("list exit = %d", code)
	}
	if strings.Count(stdout, "revoked") != 3 {
		t.Fatalf("list did not report three revoked sessions:\n%s", stdout)
	}
	for i := range ids {
		if strings.Contains(stdout, ids[i]) || strings.Contains(stdout, cookies[i]) {
			t.Fatalf("list printed a full session id or cookie:\n%s", stdout)
		}
	}

	// Every session is now refused.
	database, err = tangentdb.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = tangentdb.Close(database) }()
	store, err = participant.NewStore(database)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	for _, cookie := range cookies {
		if _, lookupErr := store.Lookup(ctx, cookie); lookupErr == nil {
			t.Fatal("a revoked session still resolves")
		}
	}
}
