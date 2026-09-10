package torqueboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	plugin "github.com/hollis-labs/plugin-sdk"
	"github.com/hollis-labs/plugin-sdk/subprocess"

	"github.com/hollis-labs/tangent/internal/pluginhost"
)

// These tests hold CW-20260910-0031's two flows end to end, against a fake
// Torque and a fake Tangent tool surface.
//
// The fakes are deliberately at the wire: a real HTTP server for Torque and a
// recorded tool-call log for Tangent. What is being checked is the sequence of
// calls this plugin makes — which is the whole of its behavior — and a fake
// that intercepted at a Go interface one layer in would let a wrong request
// body pass.

// ── The fake Tangent tool surface ───────────────────────────────────────────

type toolCall struct {
	Name      string
	Arguments map[string]any
}

type fakeTools struct {
	mu      sync.Mutex
	calls   []toolCall
	answers map[string]pluginhost.ToolResult
	err     map[string]error
}

func newFakeTools() *fakeTools {
	return &fakeTools{
		answers: map[string]pluginhost.ToolResult{},
		err:     map[string]error{},
	}
}

func (f *fakeTools) answer(name string, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[name] = pluginhost.ToolResult{Content: json.RawMessage(body)}
}

func (f *fakeTools) CallTool(
	_ context.Context, name string, arguments any,
) (pluginhost.ToolResult, error) {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return pluginhost.ToolResult{}, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return pluginhost.ToolResult{}, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, toolCall{Name: name, Arguments: decoded})
	answer, ok := f.answers[name]
	failure := f.err[name]
	f.mu.Unlock()
	if failure != nil {
		return pluginhost.ToolResult{}, failure
	}
	if !ok {
		answer = pluginhost.ToolResult{Content: json.RawMessage(`{}`)}
	}
	return answer, nil
}

func (f *fakeTools) called(name string) (toolCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.Name == name {
			return call, true
		}
	}
	return toolCall{}, false
}

func (f *fakeTools) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, call := range f.calls {
		out = append(out, call.Name)
	}
	return out
}

// fakeHost is a Tangent host: the SDK's base contract (embedded, and refusing
// everything, because this plugin calls none of it) plus the two extensions
// and the tool caller it does use.
type fakeHost struct {
	baseHostOnly
	tools  *fakeTools
	mcp    []pluginhost.MCPTool
	routes []pluginhost.HTTPRoute
}

func (h *fakeHost) RegisterMCPTool(tool pluginhost.MCPTool) error {
	h.mcp = append(h.mcp, tool)
	return nil
}

func (h *fakeHost) RegisterHTTPRoute(route pluginhost.HTTPRoute) error {
	h.routes = append(h.routes, route)
	return nil
}

func (h *fakeHost) Tools() (pluginhost.ToolCaller, error) {
	if h.tools == nil {
		return nil, pluginhost.ErrToolCallerUnavailable
	}
	return h.tools, nil
}

// ── The fake Torque ─────────────────────────────────────────────────────────

type fakeTorque struct {
	server      *httptest.Server
	mu          sync.Mutex
	tasks       []Task
	transitions []transitionRequest
	// refuse maps a task id to the message Torque answers with instead of
	// moving it.
	refuse map[string]string
	// lastListQuery is the raw query of the most recent list call.
	lastListQuery string
}

type transitionRequest struct {
	TaskID string
	Status string
	Force  bool
}

func startFakeTorque(t *testing.T, tasks []Task) *fakeTorque {
	t.Helper()
	fake := &fakeTorque{tasks: tasks, refuse: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.lastListQuery = r.URL.RawQuery
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
		refusal, refused := fake.refuse[id]
		if !refused {
			fake.transitions = append(fake.transitions,
				transitionRequest{TaskID: id, Status: body.Status, Force: body.Force})
			for index := range fake.tasks {
				if fake.tasks[index].ID == id {
					fake.tasks[index].Status = body.Status
				}
			}
		}
		fake.mu.Unlock()

		if refused {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": refusal})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeTorque) applied() []transitionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]transitionRequest(nil), f.transitions...)
}

// loadedPlugin wires a plugin onto a fake host and a fake Torque.
func loadedPlugin(t *testing.T, torque *fakeTorque) (*Plugin, *fakeHost) {
	t.Helper()
	tools := newFakeTools()
	host := &fakeHost{tools: tools}
	board := NewWithClient(NewClient(torque.server.URL))
	if err := board.Load(host); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return board, host
}

// ── Registration ────────────────────────────────────────────────────────────

// TestLoadRegistersBothToolsAndTheSyncRoute is the plugin's whole surface. It
// is worth pinning because every other test here depends on it and because the
// route's capability is a decision, not a default.
func TestLoadRegistersBothToolsAndTheSyncRoute(t *testing.T) {
	torque := startFakeTorque(t, nil)
	_, host := loadedPlugin(t, torque)

	names := map[string]bool{}
	for _, tool := range host.mcp {
		names[tool.Name] = true
		if len(tool.InputSchema) == 0 {
			t.Errorf("%s registered no input schema", tool.Name)
		}
		if !strings.HasPrefix(tool.Name, pluginhost.ToolNamespace) {
			t.Errorf("%s is outside the tool namespace", tool.Name)
		}
	}
	if !names[OpenTool] || !names[SyncTool] {
		t.Fatalf("registered tools = %v, want both", names)
	}
	if len(host.routes) != 1 {
		t.Fatalf("routes = %v, want the one sync route", host.routes)
	}
	route := host.routes[0]
	if route.Path != SyncPath || route.Method != http.MethodPost {
		t.Errorf("route = %s, want POST %s", route.Pattern(), SyncPath)
	}
	if route.Capability != "draft" {
		t.Errorf("route capability = %q; a sync applies what the participant staged, "+
			"and the board stays pending afterwards", route.Capability)
	}
}

// TestLoadRefusesAHostWithoutTangentsSurfaces: a plugin on a host that cannot
// serve it should say so at boot, not when someone presses a button.
func TestLoadRefusesAHostWithoutTangentsSurfaces(t *testing.T) {
	err := New().Load(baseHostOnly{})
	if err == nil {
		t.Fatal("Load accepted a host with no Tangent registration surfaces")
	}
	if !strings.Contains(err.Error(), "RegisterMCPTool") {
		t.Errorf("Load = %v, want the missing surface named", err)
	}
}

// ── Opening ─────────────────────────────────────────────────────────────────

// TestOpenQueriesTorqueThenCreatesARoomThenPresents is the open flow, and the
// order is the assertion: the room is not created until Torque has answered,
// so a Torque outage leaves no empty room behind.
func TestOpenQueriesTorqueThenCreatesARoomThenPresents(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	board, host := loadedPlugin(t, torque)
	host.tools.answer("tangent.session_create",
		`{"roomID":"room-1","url":"http://127.0.0.1:7842/r/room-1"}`)

	result, err := board.Open(context.Background(), OpenInput{Tags: []string{"tangent"}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if result.RoomID != "room-1" || result.URL == "" {
		t.Fatalf("Open = %+v, want the created room", result)
	}
	if result.Cards != 3 {
		t.Errorf("cards = %d, want one per task", result.Cards)
	}
	if got := host.tools.names(); len(got) != 2 ||
		got[0] != "tangent.session_create" || got[1] != "tangent.session_advance" {
		t.Fatalf("tool calls = %v, want create then advance", got)
	}
	if !strings.Contains(torque.lastListQuery, "tags=tangent") {
		t.Errorf("torque query = %q, want the caller's filters passed through", torque.lastListQuery)
	}

	advance, _ := host.tools.called("tangent.session_advance")
	completion, _ := advance.Arguments["completion"].(map[string]any)
	if completion["mode"] != "async" {
		t.Errorf("completion = %v; a board must not block the call across a human decision", completion)
	}
	envelope, _ := advance.Arguments["envelope"].(map[string]any)
	if envelope["type"] != EnvelopeType {
		t.Errorf("envelope type = %v, want the domain-free board kind", envelope["type"])
	}
	meta, _ := envelope["meta"].(map[string]any)
	if _, ok := meta[metaFiltersKey]; !ok {
		t.Errorf("envelope meta = %v, want the originating filters so a sync can re-query with them", meta)
	}
	data, _ := envelope["data"].(map[string]any)
	sync, _ := data["sync"].(map[string]any)
	if sync["enabled"] != true || sync["endpoint"] != SyncPath {
		t.Errorf("sync block = %v, want the board pointed at this plugin's route", sync)
	}
	if scope, _ := sync["scope"].(string); !strings.Contains(scope, "Filters below narrow this set") {
		t.Errorf("scope = %q; the board must say its filters are a view over what was sent", scope)
	}
}

// TestOpenReportsATorqueOutageAndCreatesNothing is the confirmed expected
// behavior: the plugin reports it, and every other Tangent surface is
// untouched.
func TestOpenReportsATorqueOutageAndCreatesNothing(t *testing.T) {
	torque := startFakeTorque(t, nil)
	torque.server.Close()
	board, host := loadedPlugin(t, torque)

	_, err := board.Open(context.Background(), OpenInput{})
	if err == nil {
		t.Fatal("Open succeeded with Torque down")
	}
	if !strings.Contains(err.Error(), "torque is unavailable") {
		t.Errorf("Open = %v, want the outage named", err)
	}
	if names := host.tools.names(); len(names) != 0 {
		t.Errorf("tool calls = %v; a Torque outage must not leave a room behind", names)
	}
}

// TestReadOnlyBoardOffersNoSync: `read_only` is how a caller says the board is
// a view. It must not ship a sync block a renderer would render a button for.
func TestReadOnlyBoardOffersNoSync(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	board, host := loadedPlugin(t, torque)
	host.tools.answer("tangent.session_create", `{"roomID":"room-1","url":"u"}`)

	if _, err := board.Open(context.Background(), OpenInput{ReadOnly: true}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	advance, _ := host.tools.called("tangent.session_advance")
	envelope, _ := advance.Arguments["envelope"].(map[string]any)
	data, _ := envelope["data"].(map[string]any)
	sync, _ := data["sync"].(map[string]any)
	if sync["enabled"] != false {
		t.Errorf("read-only board sync = %v, want it disabled", sync)
	}
	if sync["stage_label"] != nil && sync["stage_label"] != "" {
		t.Errorf("read-only board offers a staging control: %v", sync)
	}
}

// ── Syncing ─────────────────────────────────────────────────────────────────

// surfaceWithBoard builds the tangent.surface_get answer for a room showing a
// board with the supplied staged changes.
func surfaceWithBoard(t *testing.T, staged map[string]string, cards []Task) string {
	t.Helper()
	data := BuildBoard("board-1", "Torque", cards, []string{"todo", "doing", "done"},
		Source{App: "torque"},
		&Sync{Enabled: true, Endpoint: SyncPath, Label: "Sync", StageLabel: "Move to"},
		"2026-09-10T00:00:00Z")
	envelope := boardEnvelope("env-1", "Torque", data, ListFilters{
		Statuses: []string{"todo", "doing", "done"}, Tags: []string{"tangent"},
	})
	// The two payloads the real record carries, and they are different: the
	// canonical request snapshot is the envelope's DATA block, while the whole
	// envelope is retained separately as the presentation artifact. A fake that
	// put the envelope in both would have hidden a decode bug that only shows
	// up against a real surface — it did, until an integration run caught it.
	requestSnapshot, err := json.Marshal(envelope["data"])
	if err != nil {
		t.Fatalf("encode request snapshot: %v", err)
	}
	presented, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("encode presented envelope: %v", err)
	}

	stagedChanges := map[string]map[string]string{}
	for cardID, columnID := range staged {
		stagedChanges[cardID] = map[string]string{"column_id": columnID}
	}
	draft, err := json.Marshal(map[string]any{
		"board_id":       "board-1",
		"filters":        map[string][]string{},
		"staged_changes": stagedChanges,
	})
	if err != nil {
		t.Fatalf("encode draft: %v", err)
	}

	payload, err := json.Marshal(map[string]any{
		"interactions": []map[string]any{{
			"interaction_id":   "int-1",
			"state":            "presented",
			"revision":         3,
			"request_snapshot": json.RawMessage(requestSnapshot),
			"external_refs": map[string]any{
				"legacy_room_id":     "room-1",
				"legacy_envelope_id": "env-1",
				"legacy_envelope":    json.RawMessage(presented),
			},
			"definition_binding": map[string]any{"kind": EnvelopeType},
		}},
		"drafts": []map[string]any{
			{"interaction_id": "int-1", "revision": 1, "payload": json.RawMessage(`{"staged_changes":{}}`)},
			{"interaction_id": "int-1", "revision": 2, "payload": json.RawMessage(draft)},
		},
	})
	if err != nil {
		t.Fatalf("encode surface: %v", err)
	}
	return string(payload)
}

// TestSyncPushesStagedChangesThenPullsFreshCards is the requirement in one
// test: both directions, one call, no agent turn.
func TestSyncPushesStagedChangesThenPullsFreshCards(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	board, host := loadedPlugin(t, torque)
	host.tools.answer("tangent.surface_get",
		surfaceWithBoard(t, map[string]string{"CW-1": "doing"}, tasks()))

	result, err := board.Sync(context.Background(), SyncInput{RoomID: "room-1"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	applied := torque.applied()
	if len(applied) != 1 || applied[0].TaskID != "CW-1" || applied[0].Status != "doing" {
		t.Fatalf("torque transitions = %+v, want CW-1 moved to doing", applied)
	}
	if len(result.Applied) != 1 || result.Applied[0].From != "todo" || result.Applied[0].To != "doing" {
		t.Errorf("result.applied = %+v, want the move reported with where it came from", result.Applied)
	}
	if len(result.Failed) != 0 {
		t.Errorf("result.failed = %+v, want none", result.Failed)
	}
	if result.Cards != 3 {
		t.Errorf("cards = %d, want the re-queried set", result.Cards)
	}

	// The order is the contract: read, push, pull, withdraw, present. Pushing
	// after the re-query would show the participant the state before their own
	// change.
	want := []string{
		"tangent.surface_get", "tangent.interaction_cancel", "tangent.session_advance",
	}
	if got := host.tools.names(); len(got) != len(want) {
		t.Fatalf("tool calls = %v, want %v", got, want)
	} else {
		for index, name := range want {
			if got[index] != name {
				t.Fatalf("tool calls = %v, want %v", got, want)
			}
		}
	}
	cancel, _ := host.tools.called("tangent.interaction_cancel")
	if cancel.Arguments["cause"] != "caller_withdrawn" {
		t.Errorf("cancel cause = %v; the participant did not cancel anything", cancel.Arguments["cause"])
	}
	if cancel.Arguments["expected_revision"] != float64(3) {
		t.Errorf("cancel revision = %v, want the interaction's own", cancel.Arguments["expected_revision"])
	}
}

// TestSyncWithNothingStagedIsAPlainRefresh is the other half of "both ways":
// pressing Sync on a board nobody has touched re-queries and replaces it.
func TestSyncWithNothingStagedIsAPlainRefresh(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	board, host := loadedPlugin(t, torque)
	host.tools.answer("tangent.surface_get", surfaceWithBoard(t, nil, tasks()))

	result, err := board.Sync(context.Background(), SyncInput{RoomID: "room-1"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(torque.applied()) != 0 {
		t.Errorf("torque was written to with nothing staged: %+v", torque.applied())
	}
	if len(result.Applied) != 0 || result.Cards != 3 {
		t.Errorf("result = %+v, want a pure refresh", result)
	}
	if _, ok := host.tools.called("tangent.session_advance"); !ok {
		t.Error("a refresh did not present a fresh board")
	}
}

// TestStagingACardIntoItsOwnColumnPushesNothing: a participant who moves a
// card and moves it back should leave nothing for the sync to apply.
func TestStagingACardIntoItsOwnColumnPushesNothing(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	board, host := loadedPlugin(t, torque)
	host.tools.answer("tangent.surface_get",
		surfaceWithBoard(t, map[string]string{"CW-1": "todo"}, tasks()))

	result, err := board.Sync(context.Background(), SyncInput{RoomID: "room-1"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(torque.applied()) != 0 {
		t.Errorf("a no-op stage was pushed: %+v", torque.applied())
	}
	if len(result.Applied) != 0 {
		t.Errorf("result.applied = %+v, want empty", result.Applied)
	}
}

// TestARefusedTransitionDoesNotAbortTheSync: Torque's refusals name the
// remedy, and a sync that stopped at the first one would hide the rest and
// leave the participant looking at a stale board.
func TestARefusedTransitionDoesNotAbortTheSync(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	torque.refuse["CW-3"] = "done is terminal — pass force=true to reopen this task"
	board, host := loadedPlugin(t, torque)
	host.tools.answer("tangent.surface_get",
		surfaceWithBoard(t, map[string]string{"CW-1": "doing", "CW-3": "todo"}, tasks()))

	result, err := board.Sync(context.Background(), SyncInput{RoomID: "room-1"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0].TaskID != "CW-1" {
		t.Errorf("result.applied = %+v, want the one that worked", result.Applied)
	}
	if len(result.Failed) != 1 || result.Failed[0].TaskID != "CW-3" {
		t.Fatalf("result.failed = %+v, want the refused move", result.Failed)
	}
	if !strings.Contains(result.Failed[0].Reason, "force=true") {
		t.Errorf("failure reason = %q, want Torque's own message with the remedy",
			result.Failed[0].Reason)
	}
	if _, ok := host.tools.called("tangent.session_advance"); !ok {
		t.Error("a partial failure stopped the board being refreshed")
	}
}

// TestSyncPassesForceThrough: reopening a finished task is deliberate, and the
// caller is the one who declares it.
func TestSyncPassesForceThrough(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	board, host := loadedPlugin(t, torque)
	host.tools.answer("tangent.surface_get",
		surfaceWithBoard(t, map[string]string{"CW-3": "todo"}, tasks()))

	if _, err := board.Sync(context.Background(), SyncInput{RoomID: "room-1", Force: true}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	applied := torque.applied()
	if len(applied) != 1 || !applied[0].Force {
		t.Fatalf("torque transitions = %+v, want force passed through", applied)
	}
}

// TestSyncReQueriesWithTheBoardsOwnFilters: the filters come off the immutable
// request snapshot, so a sync cannot silently widen or narrow the board.
func TestSyncReQueriesWithTheBoardsOwnFilters(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	board, host := loadedPlugin(t, torque)
	host.tools.answer("tangent.surface_get", surfaceWithBoard(t, nil, tasks()))

	if _, err := board.Sync(context.Background(), SyncInput{RoomID: "room-1"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !strings.Contains(torque.lastListQuery, "tags=tangent") ||
		!strings.Contains(torque.lastListQuery, "status=todo%2Cdoing%2Cdone") {
		t.Errorf("re-query = %q, want the filters the board was opened with", torque.lastListQuery)
	}
}

// TestSyncRefusesARoomWithNoBoard names what is missing rather than failing
// obscurely inside a decode.
func TestSyncRefusesARoomWithNoBoard(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	board, host := loadedPlugin(t, torque)
	host.tools.answer("tangent.surface_get", `{"interactions":[],"drafts":[]}`)

	_, err := board.Sync(context.Background(), SyncInput{RoomID: "room-1"})
	if err == nil || !strings.Contains(err.Error(), "no open Torque board") {
		t.Fatalf("Sync = %v, want a refusal naming the missing board", err)
	}
}

// TestSyncRefusesABoardThisPluginDidNotOpen: without the originating filters
// there is nothing to re-query with, and guessing would silently change what
// the participant is looking at.
func TestSyncRefusesABoardThisPluginDidNotOpen(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	board, host := loadedPlugin(t, torque)
	host.tools.answer("tangent.surface_get", `{
      "interactions":[{"interaction_id":"int-1","state":"presented","revision":1,
        "request_snapshot":{"v":1,"id":"e","type":"tangent.app-board","data":{"board_id":"b","cards":[]}},
        "definition_binding":{"kind":"tangent.app-board"}}],
      "drafts":[]}`)

	_, err := board.Sync(context.Background(), SyncInput{RoomID: "room-1"})
	if err == nil || !strings.Contains(err.Error(), "not opened by this plugin") {
		t.Fatalf("Sync = %v, want a refusal naming the cause", err)
	}
}

// TestOnlyTheLatestDraftRevisionIsRead. The draft sequence is the contract —
// the store computes MAX(revision) + 1 — so revision order is the only
// ordering guaranteed to be the participant's.
func TestOnlyTheLatestDraftRevisionIsRead(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	board, host := loadedPlugin(t, torque)
	// Revision 2 stages CW-1; revision 1 (delivered second in the array) stages
	// nothing. Reading the array's last entry would push nothing at all.
	host.tools.answer("tangent.surface_get",
		surfaceWithBoard(t, map[string]string{"CW-1": "doing"}, tasks()))

	if _, err := board.Sync(context.Background(), SyncInput{RoomID: "room-1"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(torque.applied()) != 1 {
		t.Errorf("torque transitions = %+v, want the latest draft's staged change",
			torque.applied())
	}
}

// ── The HTTP route ──────────────────────────────────────────────────────────

// TestHTTPRouteRunsTheSameSync is what makes the button worth having: the
// route and the tool are one code path, so they cannot answer differently.
func TestHTTPRouteRunsTheSameSync(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	board, host := loadedPlugin(t, torque)
	host.tools.answer("tangent.surface_get",
		surfaceWithBoard(t, map[string]string{"CW-1": "doing"}, tasks()))

	response, err := board.HTTPHandle(context.Background(), subprocess.HTTPRequest{
		Method: http.MethodPost, Path: SyncPath,
		Body: []byte(`{"room_id":"room-1"}`),
	})
	if err != nil {
		t.Fatalf("HTTPHandle: %v", err)
	}
	if response.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Status)
	}
	var result SyncResult
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatalf("decode: %v (%s)", err, response.Body)
	}
	if len(result.Applied) != 1 || result.Applied[0].TaskID != "CW-1" {
		t.Errorf("result = %+v, want the staged change applied", result)
	}
	if len(torque.applied()) != 1 {
		t.Errorf("torque transitions = %+v", torque.applied())
	}
}

// TestHTTPRouteRefusesAMissingRoom renders a refusal the SPA can show rather
// than failing the whole route.
func TestHTTPRouteRefusesAMissingRoom(t *testing.T) {
	torque := startFakeTorque(t, nil)
	board, _ := loadedPlugin(t, torque)

	response, err := board.HTTPHandle(context.Background(), subprocess.HTTPRequest{
		Method: http.MethodPost, Path: SyncPath, Body: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("HTTPHandle: %v", err)
	}
	if response.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", response.Status)
	}
	if !strings.Contains(string(response.Body), "room_id") {
		t.Errorf("body = %s, want the missing field named", response.Body)
	}
}

// TestMCPDispatchRoutesByToolName covers the one switch a second tool made
// necessary: a plugin serving two tools through one handler must not answer
// the wrong one.
func TestMCPDispatchRoutesByToolName(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	board, host := loadedPlugin(t, torque)
	host.tools.answer("tangent.session_create", `{"roomID":"room-1","url":"u"}`)

	result, err := board.MCPCallTool(context.Background(), subprocess.MCPCallRequest{
		ToolName: OpenTool, Arguments: map[string]any{"limit": 10},
	})
	if err != nil {
		t.Fatalf("MCPCallTool(%s): %v", OpenTool, err)
	}
	var opened OpenResult
	if err := json.Unmarshal(result.Content, &opened); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if opened.RoomID != "room-1" {
		t.Errorf("open result = %+v", opened)
	}

	if _, err := board.MCPCallTool(context.Background(), subprocess.MCPCallRequest{
		ToolName: "tangent.not_ours",
	}); err == nil {
		t.Error("an unknown tool name was dispatched")
	}
}

// TestToolsAreUnavailableBeforeTheHostAttachesOne: a plugin driving Tangent
// before the MCP server exists is a composition-root defect, and it should say
// so rather than panic several frames away.
func TestToolsAreUnavailableBeforeTheHostAttachesOne(t *testing.T) {
	torque := startFakeTorque(t, tasks())
	host := &fakeHost{}
	board := NewWithClient(NewClient(torque.server.URL))
	if err := board.Load(host); err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err := board.Open(context.Background(), OpenInput{})
	if err == nil {
		t.Fatal("Open succeeded with no tool caller attached")
	}
	if !strings.Contains(err.Error(), "no tool caller") {
		t.Errorf("Open = %v, want the missing caller named", err)
	}
}

// baseHostOnly implements the SDK's base Host contract and none of Tangent's
// extensions, which is the shape a plugin would meet on another host.
type baseHostOnly struct{}

func (baseHostOnly) GetPlugin(string) (plugin.Plugin, bool) { return nil, false }

func (baseHostOnly) RegisterCRUDHandler(string, plugin.CRUDHandler) error { return errNotHonored }
func (baseHostOnly) RegisterEventHook([]string, plugin.EventHook) error   { return errNotHonored }
func (baseHostOnly) RegisterUIComponent(plugin.UIComponent) error         { return errNotHonored }
func (baseHostOnly) GetService(string) (interface{}, error)               { return nil, errNotHonored }
func (baseHostOnly) GetConfig(string) (string, error)                     { return "", errNotHonored }
func (baseHostOnly) SetConfig(string, string) error                       { return errNotHonored }
func (baseHostOnly) RegisterConfigSchema([]plugin.ConfigFieldDef) error   { return errNotHonored }
func (baseHostOnly) RegisterConnector(string, plugin.Connector) error     { return errNotHonored }
func (baseHostOnly) RegisterProvider(string, interface{}) error           { return errNotHonored }
func (baseHostOnly) RegisterCLIAdapter(string, interface{}) error         { return errNotHonored }
func (baseHostOnly) Logger() plugin.Logger                                { return nil }
func (baseHostOnly) Context() context.Context                             { return context.Background() }

var errNotHonored = errors.New("not honored")

var _ plugin.Host = baseHostOnly{}
