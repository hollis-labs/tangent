package mcp_test

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	logs := captureLogs(t)
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

	// Nowhere on disk, and not in the process logs.
	assertNoCanaryInTables(t, rg.db, formSecretCanary)
	if strings.Contains(logs.String(), formSecretCanary) {
		t.Fatalf("the secret is in the process logs:\n%s", logs.String())
	}
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
				t.Fatalf("scan %s.%s: %v", name, col, err)
			}
			if n != 0 {
				t.Fatalf("the secret is stored in %s.%s (%d rows)", name, col, n)
			}
		}
	}
}

// A request must not seed a secret field: it would be stored in the request
// and in the room's form state before anyone typed anything.
func TestFormCollectRejectsAPrefilledSecret(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: 5 * time.Second, dbPath: filepath.Join(t.TempDir(), "tangent.db")})
	defer rg.cleanup()
	roomID, _ := createSession(t, rg, "form-secret-prefill")

	fixture := namedFixture(t, "tangent_form_collect")
	data := map[string]any{
		"form_id": "secret-probe",
		"schema": map[string]any{"fields": []any{
			map[string]any{"id": "api_key", "label": "API key", "type": "password", "secret": true},
		}},
		"answers": map[string]any{"api_key": formSecretCanary},
	}
	result := callWorkflow(t, rg, fixture, roomID, "form-secret-prefill-envelope", nil, data)
	if result.err == nil && (result.result == nil || !result.result.IsError) {
		t.Fatal("a prefilled secret must be rejected")
	}
	var text string
	if result.result != nil {
		text = extractText(t, result.result)
	}
	if !strings.Contains(text, "answers.api_key") || !strings.Contains(text, "must not be prefilled") {
		t.Fatalf("error should name the field path:\n%s", text)
	}
	if strings.Contains(text, formSecretCanary) {
		t.Fatalf("the error must not echo the secret:\n%s", text)
	}
	assertNoCanaryInTables(t, rg.db, formSecretCanary)
}

// lockedBuffer is a log sink safe for the server goroutines that write to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogs routes the process log (and slog's default handler) into a buffer
// for the rest of the test.
func captureLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	sink := &lockedBuffer{}
	previous := log.Writer()
	log.SetOutput(sink)
	t.Cleanup(func() { log.SetOutput(previous) })
	return sink
}

// answerSecretForm presents a form-collect through the real path, sends a draft
// and then the submission over a WebSocket, and returns the text the caller got.
// In async mode the caller instead collects the outcome with
// tangent_interaction_await. The whole run is checked for the canary on disk.
func answerSecretForm(
	t *testing.T, rg *sessionRig, roomID, envelopeID string, async bool,
	data map[string]any, answers map[string]any,
) string {
	t.Helper()
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	fixture := namedFixture(t, "tangent_form_collect")
	var completion map[string]any
	if async {
		completion = map[string]any{"mode": "async"}
	}
	done := make(chan advanceResult, 1)
	go func() { done <- callWorkflow(t, rg, fixture, roomID, envelopeID, completion, data) }()
	frame := readWSFrame(t, conn, 10*time.Second)
	if frame["envelopeId"] != envelopeID {
		t.Fatalf("envelopeId = %v, want %s", frame["envelopeId"], envelopeID)
	}
	writeWSFrame(t, conn, map[string]any{
		"type": "draft", "envelopeId": envelopeID, "draftRevision": 1,
		"draft": map[string]any{"answers": answers},
	})
	time.Sleep(200 * time.Millisecond)
	writeWSFrame(t, conn, map[string]any{
		"type": "response", "envelopeId": envelopeID, "revision": frame["revision"],
		"response": map[string]any{
			"v": 1, "envelopeId": envelopeID, "kind": "data", "status": "submitted",
			"completedAt": fixedCompletedAt,
			"payload":     map[string]any{"form_id": data["form_id"], "answers": answers},
		},
	})
	result := <-done
	if result.err != nil || result.result.IsError {
		t.Fatalf("workflow: err=%v result=%+v", result.err, result.result)
	}
	if !async {
		return extractText(t, result.result)
	}
	// The pending receipt carries no answer; collect the outcome by await.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		out := callTool(t, rg, "tangent_interaction_await", map[string]any{
			"interaction_id":  singleInteractionIDIn(t, rg, roomID, envelopeID),
			"requester_scope": roomflow.DefaultCaller.Scope, "maximum_wait_ms": 2000,
		})
		if strings.Contains(out, `"state":"resolved"`) {
			return out
		}
	}
	t.Fatal("the async outcome never resolved")
	return ""
}

func singleInteractionIDIn(t *testing.T, rg *sessionRig, roomID, envelopeID string) string {
	t.Helper()
	var id string
	if err := rg.db.QueryRow(
		`SELECT id FROM interactions WHERE legacy_room_id = ? AND legacy_envelope_id = ?`, roomID, envelopeID,
	).Scan(&id); err != nil {
		t.Fatalf("interaction for %s/%s: %v", roomID, envelopeID, err)
	}
	return id
}

func secretFormData(fields, sections []any) map[string]any {
	schema := map[string]any{}
	if fields != nil {
		schema["fields"] = fields
	}
	if sections != nil {
		schema["sections"] = sections
	}
	return map[string]any{"form_id": "secret-probe", "schema": schema}
}

func TestFormSecretFieldShapesAndAwaitPath(t *testing.T) {
	cases := []struct {
		name    string
		data    map[string]any
		answers map[string]any
		secrets []string
		async   bool
	}{
		{
			name:    "password type without the secret flag",
			data:    secretFormData([]any{map[string]any{"id": "pw", "type": "password", "label": "PW"}}, nil),
			answers: map[string]any{"pw": formSecretCanary + "-pw"},
			secrets: []string{formSecretCanary + "-pw"},
		},
		{
			name: "secret inside a repeatable section",
			data: secretFormData(nil, []any{map[string]any{"id": "creds", "repeatable": true, "fields": []any{
				map[string]any{"id": "token", "type": "text", "secret": true, "label": "Token"},
				map[string]any{"id": "label", "type": "text", "label": "Label"},
			}}}),
			answers: map[string]any{"creds": []any{
				map[string]any{"token": formSecretCanary + "-t0", "label": "a"},
				map[string]any{"token": formSecretCanary + "-t1", "label": "b"},
			}},
			secrets: []string{formSecretCanary + "-t0", formSecretCanary + "-t1"},
		},
		{
			name:    "outcome collected with tangent_interaction_await",
			data:    secretFormData([]any{map[string]any{"id": "api_key", "type": "text", "secret": true, "label": "Key"}}, nil),
			answers: map[string]any{"api_key": formSecretCanary + "-await"},
			secrets: []string{formSecretCanary + "-await"},
			async:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			dbPath := filepath.Join(t.TempDir(), "tangent.db")
			rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: 30 * time.Second, dbPath: dbPath})
			defer rg.cleanup()
			roomID, _ := createSession(t, rg, "secret-shapes")
			text := answerSecretForm(t, rg, roomID, "shape-envelope", tc.async, tc.data, tc.answers)
			for _, secret := range tc.secrets {
				if strings.Count(text, secret) != 1 {
					t.Fatalf("the caller must get %q exactly once, got %d in:\n%s", secret, strings.Count(text, secret), text)
				}
			}
			assertNoCanaryInTables(t, rg.db, formSecretCanary)
			if strings.Contains(logs.String(), formSecretCanary) {
				t.Fatalf("the secret is in the process logs:\n%s", logs.String())
			}
			if _, err := rg.db.Exec(`PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
				t.Fatalf("checkpoint: %v", err)
			}
			for _, path := range []string{dbPath, dbPath + "-wal"} {
				if raw, err := os.ReadFile(path); err == nil && strings.Contains(string(raw), formSecretCanary) { //nolint:gosec // the test's own temp files
					t.Fatalf("the secret is in %s", filepath.Base(path))
				}
			}
		})
	}
}

// A workflow's identity is (caller scope, kind, envelope id), not the room. A
// second request reusing the id, even from another room, is a retry of the same
// interaction: it gets the recorded outcome, which no longer holds the secret
// (already delivered), and says so. It never receives anyone's value.
func TestFormSecretRetryWithTheSameEnvelopeIDGetsTheTombstoneAndAFlag(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: 30 * time.Second, dbPath: filepath.Join(t.TempDir(), "tangent.db")})
	defer rg.cleanup()
	roomA, _ := createSession(t, rg, "room-a")
	roomB, _ := createSession(t, rg, "room-b")
	data := secretFormData([]any{map[string]any{"id": "api_key", "type": "text", "secret": true, "label": "Key"}}, nil)

	first := answerSecretForm(t, rg, roomA, "credentials", false, data, map[string]any{"api_key": formSecretCanary + "-A"})
	if strings.Count(first, formSecretCanary+"-A") != 1 {
		t.Fatalf("the first caller must get the secret once:\n%s", first)
	}
	retry := callWorkflow(t, rg, namedFixture(t, "tangent_form_collect"), roomB, "credentials", nil, data)
	if retry.err != nil || retry.result.IsError {
		t.Fatalf("retry: err=%v result=%+v", retry.err, retry.result)
	}
	text := extractText(t, retry.result)
	if strings.Contains(text, formSecretCanary) {
		t.Fatalf("a retry must not receive the secret again:\n%s", text)
	}
	if !strings.Contains(text, "secret_unavailable") || !strings.Contains(text, "form.secret-field") {
		t.Fatalf("a retry must see the tombstone and the unavailable flag:\n%s", text)
	}
	assertNoCanaryInTables(t, rg.db, formSecretCanary)
}

// A secret the server cannot protect is refused when the form is presented, not
// stored in clear later.
func TestFormCollectRejectsSecretsOfUnprotectableTypes(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: 5 * time.Second, dbPath: filepath.Join(t.TempDir(), "tangent.db")})
	defer rg.cleanup()
	roomID, _ := createSession(t, rg, "secret-types")
	fixture := namedFixture(t, "tangent_form_collect")
	data := secretFormData([]any{map[string]any{"id": "pin", "type": "number", "secret": true, "label": "PIN"}}, nil)
	result := callWorkflow(t, rg, fixture, roomID, "secret-number", nil, data)
	if result.err == nil && (result.result == nil || !result.result.IsError) {
		t.Fatal("a secret number field must be refused")
	}
	if result.result != nil && !strings.Contains(extractText(t, result.result), "secret field must be type text") {
		t.Fatalf("unexpected refusal: %s", extractText(t, result.result))
	}
}

// Agents sent fields keyed by "name" and sections without "fields". The host
// accepted them and the form crashed in the browser, so the operator could not
// review the item. The tool must refuse both shapes at submit.
func TestFormCollectRefusesAMalformedFieldSchema(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: 5 * time.Second, dbPath: filepath.Join(t.TempDir(), "tangent.db")})
	defer rg.cleanup()
	roomID, _ := createSession(t, rg, "malformed-schema")
	fixture := namedFixture(t, "tangent_form_collect")
	cases := map[string]map[string]any{
		"field keyed by name":         {"fields": []any{map[string]any{"name": "a", "type": "text", "label": "A"}}},
		"section without fields":      {"sections": []any{map[string]any{"id": "s", "title": "S"}}},
		"neither fields nor sections": {},
	}
	for name, schema := range cases {
		data := map[string]any{"form_id": "bad-" + strings.ReplaceAll(name, " ", "-"), "schema": schema}
		result := callWorkflow(t, rg, fixture, roomID, "bad-"+strings.ReplaceAll(name, " ", "-"), nil, data)
		if result.err == nil && (result.result == nil || !result.result.IsError) {
			t.Fatalf("%s: a malformed form schema must be refused", name)
		}
	}
}
