package mcp_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/hollis-labs/tangent/internal/roomflow"
)

const formSecretCanary = "SECRETCANARY-5d21b0aa"

// TestFormSecretReachesTheCallerOnceAndIsStoredNowhere is the acceptance test
// for CW-20261016-0176. It runs the real path (MCP server, durable
// interaction, room, WebSocket) with a secret-flagged field, sends the canary
// as a draft and as the submitted answer, and then looks for it everywhere the
// process wrote: every text column of every table, and the database and WAL
// files.
func TestFormSecretReachesTheCallerOnceAndIsStoredNowhere(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tangent.db")
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: 30 * time.Second, dbPath: dbPath})
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "form-secret")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	fixture := namedFixture(t, "tangent_form_collect")
	const envelopeID = "form-secret-envelope"
	data := map[string]any{
		"form_id": "secret-probe",
		"schema": map[string]any{"fields": []any{
			map[string]any{"id": "api_key", "label": "API key", "type": "password", "secret": true},
			map[string]any{"id": "name", "label": "Name", "type": "text"},
		}},
	}
	done := make(chan advanceResult, 1)
	go func() { done <- callWorkflow(t, rg, fixture, roomID, envelopeID, nil, data) }()

	frame := readWSFrame(t, conn, 10*time.Second)
	if frame["envelopeId"] != envelopeID {
		t.Fatalf("envelopeId = %v, want %s", frame["envelopeId"], envelopeID)
	}
	writeWSFrame(t, conn, map[string]any{
		"type": "draft", "envelopeId": envelopeID, "draftRevision": 1,
		"draft": map[string]any{"answers": map[string]any{"api_key": formSecretCanary, "name": "draft-name"}},
	})
	time.Sleep(300 * time.Millisecond)
	writeWSFrame(t, conn, map[string]any{
		"type": "response", "envelopeId": envelopeID, "revision": frame["revision"],
		"response": map[string]any{
			"v": 1, "envelopeId": envelopeID, "kind": "data", "status": "submitted",
			"completedAt": fixedCompletedAt,
			"payload": map[string]any{
				"form_id": "secret-probe",
				"answers": map[string]any{"api_key": formSecretCanary, "name": "visible"},
			},
		},
	})

	result := <-done
	if result.err != nil || result.result.IsError {
		t.Fatalf("workflow: err=%v result=%+v", result.err, result.result)
	}
	text := extractText(t, result.result)
	if strings.Count(text, formSecretCanary) != 1 {
		t.Fatalf("the caller must receive the secret exactly once, got %d in:\n%s",
			strings.Count(text, formSecretCanary), text)
	}
	if !strings.Contains(text, "visible") {
		t.Fatalf("the non-secret answer is missing from the caller's result:\n%s", text)
	}

	// A second read of the same outcome sees only the tombstone.
	again := callTool(t, rg, "tangent_interaction_get", map[string]any{
		"interaction_id":  singleInteractionID(t, rg, envelopeID),
		"requester_scope": roomflow.DefaultCaller.Scope,
	})
	if strings.Contains(again, formSecretCanary) || !strings.Contains(again, "form.secret-field") {
		t.Fatalf("a later read must carry the tombstone only:\n%s", again)
	}

	// Nowhere on disk.
	assertNoCanaryInTables(t, rg.db, formSecretCanary)
	if _, err = rg.db.Exec(`PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	for _, path := range []string{dbPath, dbPath + "-wal"} {
		raw, readErr := os.ReadFile(path) //nolint:gosec // paths are the test's own temp database and WAL
		if readErr != nil {
			continue // a missing WAL after a clean checkpoint is fine
		}
		if strings.Contains(string(raw), formSecretCanary) {
			t.Fatalf("the secret is in %s", filepath.Base(path))
		}
	}
}

func assertNoCanaryInTables(t *testing.T, db *sql.DB, canary string) {
	t.Helper()
	tables, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err = tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	_ = tables.Close()
	for _, name := range names {
		columns, qErr := db.Query(`SELECT name FROM pragma_table_info(?)`, name)
		if qErr != nil {
			t.Fatalf("columns of %s: %v", name, qErr)
		}
		var cols []string
		for columns.Next() {
			var col string
			if err = columns.Scan(&col); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, col)
		}
		_ = columns.Close()
		for _, col := range cols {
			var n int
			//nolint:gosec // table and column names come from sqlite_master
			query := `SELECT COUNT(*) FROM "` + name + `" WHERE CAST("` + col + `" AS TEXT) LIKE ?`
			if err = db.QueryRow(query, "%"+canary+"%").Scan(&n); err != nil {
				continue
			}
			if n != 0 {
				t.Fatalf("the secret is stored in %s.%s (%d rows)", name, col, n)
			}
		}
	}
}
