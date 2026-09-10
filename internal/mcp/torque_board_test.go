package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/plugins/torqueboard"
)

// CW-20260910-0031 — the Torque board, end to end, through the real surface.
//
// # Why this test exists in this package and not beside the plugin
//
// The plugin's own tests fake Tangent, and a fake is only as good as the shape
// its author believed. One of them believed an interaction's `request_snapshot`
// was the whole envelope; it is the envelope's `data` block, with the envelope
// retained separately as `external_refs.legacy_envelope`. Every unit test
// passed against that belief, and the plugin would have failed on the first
// real sync.
//
// So this drives the whole chain against the real one: the real MCP tool
// surface, the real durable interaction substrate, a real room with a real
// browser socket on it, and the shipped plugin reached through the tools it
// actually registered. The only fake is Torque, which is the one thing that
// genuinely is not Tangent.

// TestTorqueBoard_OpenStageSync is the whole feature in one pass: an agent's
// single call opens a board, the participant stages a move, the sync applies it
// to Torque and replaces the board with fresh cards.
func TestTorqueBoard_OpenStageSync(t *testing.T) {
	torque := startFakeTorqueAPI(t)
	rg := newSessionRigWithTorque(t, torque)
	defer rg.cleanup()
	ctx := context.Background()

	// 1. One tool call, filters in, a room out. The agent shapes no payload.
	var opened torqueboard.OpenResult
	callPluginTool(t, rg, "tangent.torque_board", map[string]any{
		"statuses": []string{"todo", "doing"},
		"title":    "Torque — dogfood",
	}, &opened)
	if opened.RoomID == "" || opened.BoardID == "" {
		t.Fatalf("open = %+v, want a room and a board handle", opened)
	}
	if opened.Cards != 2 {
		t.Fatalf("open sent %d cards, want the two the fake Torque holds", opened.Cards)
	}

	// 2. A browser attaches and is shown the board.
	conn, _, err := websocket.Dial(ctx, rg.wsURL(opened.RoomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	firstEnvelopeID := awaitBoardEnvelope(t, conn, "")
	if firstEnvelopeID == "" {
		t.Fatal("the board never reached the browser")
	}

	// 3. The participant stages a card into another column. This is a draft
	//    frame, exactly as ui/src/components/envelopes/AppBoard.tsx sends one:
	//    non-terminal, no resolver lease, nothing written to Torque.
	writeWSFrame(t, conn, map[string]any{
		"type": "draft", "envelopeId": firstEnvelopeID, "draftRevision": 1,
		"draft": map[string]any{
			"board_id":       opened.BoardID,
			"filters":        map[string][]string{},
			"staged_changes": map[string]any{"CW-1": map[string]any{"column_id": "doing"}},
		},
	})
	// The draft has to have landed durably before the sync reads it. The
	// protocol acknowledges a draft by recording it, not by answering a frame,
	// so this waits on the record — which is also what the sync reads.
	waitForDrafts(t, rg, opened.RoomID, 1)

	if applied := torque.applied(); len(applied) != 0 {
		t.Fatalf("staging wrote to Torque before the sync: %+v; a draft is not a decision", applied)
	}

	// 4. Sync. One call, both directions.
	var synced torqueboard.SyncResult
	callPluginTool(t, rg, "tangent.torque_board_sync",
		map[string]any{"room_id": opened.RoomID}, &synced)

	applied := torque.applied()
	if len(applied) != 1 || applied[0].TaskID != "CW-1" || applied[0].Status != "doing" {
		t.Fatalf("torque transitions = %+v, want the staged move applied", applied)
	}
	if len(synced.Applied) != 1 || synced.Applied[0].From != "todo" || synced.Applied[0].To != "doing" {
		t.Errorf("sync result = %+v, want the move reported", synced.Applied)
	}
	if len(synced.Failed) != 0 {
		t.Errorf("sync failed changes = %+v, want none", synced.Failed)
	}
	if synced.Cards != 2 {
		t.Errorf("sync sent %d cards, want the re-queried set", synced.Cards)
	}

	// 5. The participant is shown the replacement, with the card in its new
	//    column and no staged marker — because the change is real now.
	secondEnvelopeID := awaitBoardEnvelope(t, conn, firstEnvelopeID)
	if secondEnvelopeID == "" {
		t.Fatal("the refreshed board never reached the browser")
	}
}

// TestTorqueBoard_SyncReportsATorqueOutageAndLeavesTheBoardAlone is the
// confirmed expected behavior: Torque going down is the plugin's problem to
// report, and every other Tangent surface keeps working — including the board
// the participant is looking at.
func TestTorqueBoard_SyncReportsATorqueOutageAndLeavesTheBoardAlone(t *testing.T) {
	torque := startFakeTorqueAPI(t)
	rg := newSessionRigWithTorque(t, torque)
	defer rg.cleanup()

	var opened torqueboard.OpenResult
	callPluginTool(t, rg, "tangent.torque_board", map[string]any{}, &opened)

	torque.server.Close()

	result := callBoardTool(t, rg, "tangent.torque_board_sync",
		map[string]any{"room_id": opened.RoomID})
	if !result.IsError {
		t.Fatal("sync succeeded with Torque down")
	}
	if body := extractText(t, result); !strings.Contains(body, "torque is unavailable") {
		t.Errorf("refusal = %s, want the outage named", body)
	}

	// The rest of the surface is untouched.
	if healthy := callBoardTool(t, rg, "tangent.list_workflows", map[string]any{}); healthy.IsError {
		t.Errorf("tangent.list_workflows broke after a Torque outage: %s", extractText(t, healthy))
	}
	// And so is the board: nothing was withdrawn, so the room still has it.
	snapshot := getSurface(t, rg, opened.RoomID)
	open := 0
	for _, record := range snapshot.Interactions {
		if record.State != "canceled" && record.State != "resolved" {
			open++
		}
	}
	if open != 1 {
		t.Errorf("open interactions = %d, want the board left exactly as it was", open)
	}
}

// TestTorqueBoard_ToolsAreOnTheSurface: the registration is what makes an
// agent's single call possible, and it goes through the same tools/list every
// other tool does.
func TestTorqueBoard_ToolsAreOnTheSurface(t *testing.T) {
	torque := startFakeTorqueAPI(t)
	rg := newSessionRigWithTorque(t, torque)
	defer rg.cleanup()

	listed, err := rg.mcpClient.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	found := map[string]bool{}
	for _, tool := range listed.Tools {
		found[tool.Name] = true
	}
	for _, name := range []string{"tangent.torque_board", "tangent.torque_board_sync"} {
		if !found[name] {
			t.Errorf("%s is not advertised; no agent could call it", name)
		}
	}
}

// TestTorqueBoard_RefusesArgumentsItsOwnSchemaRejects proves a plugin tool is
// validated like any other, at the surface, before the plugin sees anything.
func TestTorqueBoard_RefusesArgumentsItsOwnSchemaRejects(t *testing.T) {
	torque := startFakeTorqueAPI(t)
	rg := newSessionRigWithTorque(t, torque)
	defer rg.cleanup()

	result := callBoardTool(t, rg, "tangent.torque_board_sync", map[string]any{})
	if !result.IsError {
		t.Fatal("a sync with no room_id was accepted")
	}
	if body := extractText(t, result); !strings.Contains(body, "validation-failed") {
		t.Errorf("refusal = %s, want the schema refusal", body)
	}
}

// ── Harness ─────────────────────────────────────────────────────────────────

// newSessionRigWithTorque builds the standard durable rig with the shipped
// Torque board plugin pointed at a fake Torque.
//
// It points the plugin by setting the environment variable the plugin reads
// itself, before the rig loads it. That is not a shortcut around the plugin's
// configuration — it IS the plugin's configuration, so this test exercises the
// same resolution production does.
func newSessionRigWithTorque(t *testing.T, torque *fakeTorqueAPI) *sessionRig {
	t.Helper()
	t.Setenv(torqueboard.BaseURLEnv, torque.server.URL)
	return newSessionRigWith(t, sessionRigOptions{durable: true, window: testWindow})
}

func callBoardTool(
	t *testing.T,
	rg *sessionRig,
	name string,
	arguments map[string]any,
) *mcpsdk.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: name, Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("%s transport: %v", name, err)
	}
	return result
}

func callPluginTool(t *testing.T, rg *sessionRig, name string, arguments map[string]any, into any) {
	t.Helper()
	result := callBoardTool(t, rg, name, arguments)
	if result.IsError {
		t.Fatalf("%s failed: %s", name, extractText(t, result))
	}
	if err := json.Unmarshal([]byte(extractText(t, result)), into); err != nil {
		t.Fatalf("decode %s result: %v (%s)", name, err, extractText(t, result))
	}
}

// awaitBoardEnvelope waits for an envelope frame whose id is not `excluding`.
func awaitBoardEnvelope(t *testing.T, conn *websocket.Conn, excluding string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		frame := readWSFrame(t, conn, time.Until(deadline))
		if frame["type"] != "envelope" {
			continue
		}
		id, _ := frame["envelopeId"].(string)
		if id != "" && id != excluding {
			return id
		}
	}
	return ""
}

// ── The fake Torque ─────────────────────────────────────────────────────────

type fakeTorqueAPI struct {
	server *httptest.Server
	mu     sync.Mutex
	tasks  []map[string]any
	moves  []torqueMove
}

type torqueMove struct {
	TaskID string
	Status string
	Force  bool
}

func startFakeTorqueAPI(t *testing.T) *fakeTorqueAPI {
	t.Helper()
	fake := &fakeTorqueAPI{tasks: []map[string]any{
		{
			"id": "CW-1", "title": "Honor MCPHandler", "status": "todo", "priority": 1,
			"kind": "agent", "executor": "cli", "description": "# Body\n\nmarkdown",
			"tags": []map[string]string{{"slug": "tangent"}},
		},
		{
			"id": "CW-2", "title": "Honor HTTPHandler", "status": "doing", "priority": 2,
			"kind": "agent", "executor": "cli",
			"tags": []map[string]string{{"slug": "tangent"}, {"slug": "plugins"}},
		},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", func(w http.ResponseWriter, _ *http.Request) {
		fake.mu.Lock()
		payload := map[string]any{"tasks": fake.tasks}
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
	mux.HandleFunc("POST /api/v1/tasks/{id}/transition", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Status string `json:"status"`
			Force  bool   `json:"force"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := r.PathValue("id")
		fake.mu.Lock()
		fake.moves = append(fake.moves, torqueMove{TaskID: id, Status: body.Status, Force: body.Force})
		for _, task := range fake.tasks {
			if task["id"] == id {
				task["status"] = body.Status
			}
		}
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeTorqueAPI) applied() []torqueMove {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]torqueMove(nil), f.moves...)
}
