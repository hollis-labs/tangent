package almanac

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	"github.com/hollis-labs/tangent/internal/plugins/appboard"
)

// These tests hold both flows end to end, against a fake Almanac and the REAL
// plugin host.
//
// The host is real on purpose. A plugin that registers against a hand-written
// fake host proves only that the fake accepts it; the host is where a tool name
// outside the `tangent.` namespace, a route outside the plugin prefix, a kind
// with no ADR 0003 manifest, or an unloaded dependency are refused. "It loads"
// is the claim, so load it.
//
// The Almanac fake is at the wire — a real HTTP server — because what is being
// checked is the bytes this plugin sends. A fake that intercepted at a Go
// interface one layer in would let a wrong request body pass, and a wrong
// request body against a real service is the failure both existing application
// plugins actually hit.

// ── Loading on the real host ────────────────────────────────────────────────

// loadOnRealHost builds a host carrying the shipped kinds, loads the kind's own
// plugin, then loads this one.
func loadOnRealHost(t *testing.T, client *Client) (*Plugin, *pluginhost.Host) {
	t.Helper()
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if regErr := extensions.RegisterAll(svc); regErr != nil {
		t.Fatalf("RegisterAll: %v", regErr)
	}
	host, err := pluginhost.New(context.Background(), slog.New(slog.DiscardHandler), svc)
	if err != nil {
		t.Fatalf("pluginhost.New: %v", err)
	}
	// The dependency this plugin declares. Loading it first is not test setup —
	// the host refuses a plugin whose stated dependency is not already loaded,
	// which is the check that turns a missing kind into a boot failure instead
	// of a failure at the first tool call.
	if loadErr := host.Load(appboard.New()); loadErr != nil {
		t.Fatalf("load the kind's plugin: %v", loadErr)
	}
	board := NewWithClient(client)
	if loadErr := host.Load(board); loadErr != nil {
		t.Fatalf("load almanac: %v", loadErr)
	}
	return board, host
}

// TestLoadRegistersBothToolsAndTheSyncRoute is this plugin's whole surface, and
// it is worth pinning because the route's capability is a decision rather than a
// default.
func TestLoadRegistersBothToolsAndTheSyncRoute(t *testing.T) {
	fake := startFakeAlmanac(t, nil)
	_, host := loadOnRealHost(t, NewClient(fake.server.URL, ""))

	names := map[string]bool{}
	for _, tool := range host.MCPTools() {
		names[tool.Name] = true
	}
	if !names[OpenTool] || !names[SyncTool] {
		t.Errorf("registered tools = %v, want both %s and %s", names, OpenTool, SyncTool)
	}

	var mounted bool
	for _, route := range host.HTTPRoutes() {
		if route.Path == SyncPath {
			mounted = true
			if route.Method != http.MethodPost {
				t.Errorf("sync route method = %s, want POST", route.Method)
			}
			if route.Capability == "" {
				t.Error("the sync route carries no capability; the ADR 0004 §7 check has nothing to read")
			}
		}
	}
	if !mounted {
		t.Errorf("no route at %s; the board's Sync button has nothing to POST to", SyncPath)
	}
}

// ── The fake Tangent tool surface ───────────────────────────────────────────

type toolCall struct {
	Name      string
	Arguments map[string]any
}

type fakeTools struct {
	mu      sync.Mutex
	calls   []toolCall
	answers map[string]pluginhost.ToolResult
}

func newFakeTools() *fakeTools {
	return &fakeTools{answers: map[string]pluginhost.ToolResult{}}
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
	if unmarshalErr := json.Unmarshal(encoded, &decoded); unmarshalErr != nil {
		return pluginhost.ToolResult{}, unmarshalErr
	}
	f.mu.Lock()
	f.calls = append(f.calls, toolCall{Name: name, Arguments: decoded})
	answer, ok := f.answers[name]
	f.mu.Unlock()
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

// wired loads the plugin on the real host and attaches a fake tool surface, the
// way the composition root attaches the real one after the MCP server exists.
func wired(t *testing.T, fake *fakeAlmanac) (*Plugin, *fakeTools) {
	t.Helper()
	board, host := loadOnRealHost(t, NewClient(fake.server.URL, ""))
	tools := newFakeTools()
	if err := host.AttachToolCaller(tools); err != nil {
		t.Fatalf("AttachToolCaller: %v", err)
	}
	return board, tools
}

// ── The fake Almanac ───────────────────────────────────────────────────────

type fakeAlmanac struct {
	server  *httptest.Server
	mu      sync.Mutex
	records []Record
	// lastListQuery is the raw query of the most recent list call, which is how
	// the request-bytes assertions read what went out.
	lastListQuery string
	applied       []string
}

func startFakeAlmanac(t *testing.T, records []Record) *fakeAlmanac {
	t.Helper()
	fake := &fakeAlmanac{records: records}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/records", func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.lastListQuery = r.URL.RawQuery
		out := fake.records
		fake.mu.Unlock()
		// Honoring `limit` matters: truncation is detected by asking for one
		// record more than the board will show, so a fake that ignored the
		// parameter would make every truncation assertion vacuous.
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if limit, err := strconv.Atoi(raw); err == nil && limit >= 0 && limit < len(out) {
				out = out[:limit]
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"records": out})
	})
	mux.HandleFunc("POST /api/v1/records/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Status string `json:"status"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := r.PathValue("id")
		fake.mu.Lock()
		fake.applied = append(fake.applied, id+"->"+body.Status)
		for index := range fake.records {
			if fake.records[index].ID == id {
				fake.records[index].Status = body.Status
			}
		}
		fake.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

// records builds a record set large enough for the card limit to bite.
func records(count int) []Record {
	out := make([]Record, 0, count)
	for index := 0; index < count; index++ {
		out = append(out, Record{
			ID:     fmt.Sprintf("REC-%03d", index),
			Title:  fmt.Sprintf("Record %d", index),
			Status: "open",
			Tags:   []string{"alpha"},
		})
	}
	return out
}

// ── Opening ─────────────────────────────────────────────────────────────────

// TestOpenQueriesThenCreatesARoomThenPresents is the open flow, and the order is
// the assertion: the room is not created until Almanac has answered, so an
// outage leaves no empty room behind.
func TestOpenQueriesThenCreatesARoomThenPresents(t *testing.T) {
	fake := startFakeAlmanac(t, records(3))
	board, tools := wired(t, fake)
	tools.answer("tangent.session_create", `{"roomID":"room-1","url":"http://127.0.0.1:7842/r/room-1"}`)

	result, err := board.Open(context.Background(), OpenInput{Tags: []string{"alpha"}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if result.RoomID != "room-1" || result.Cards != 3 {
		t.Fatalf("Open = %+v, want the created room and one card per record", result)
	}
	if got := tools.names(); len(got) != 2 ||
		got[0] != "tangent.session_create" || got[1] != "tangent.session_advance" {
		t.Fatalf("tool calls = %v, want create then advance", got)
	}
	if !strings.Contains(fake.lastListQuery, "tags=alpha") {
		t.Errorf("Almanac query = %q, want the caller's filters passed through", fake.lastListQuery)
	}

	advance, _ := tools.called("tangent.session_advance")
	completion, _ := advance.Arguments["completion"].(map[string]any)
	if completion["mode"] != "async" {
		t.Errorf("completion = %v; a board must not block the call across a human decision", completion)
	}
	envelopeArg, _ := advance.Arguments["envelope"].(map[string]any)
	if envelopeArg["type"] != EnvelopeType {
		t.Errorf("envelope type = %v, want the domain-free board kind", envelopeArg["type"])
	}
	meta, _ := envelopeArg["meta"].(map[string]any)
	if _, ok := meta[metaFiltersKey]; !ok {
		t.Errorf("envelope meta = %v, want the originating filters so a sync can re-query", meta)
	}
	data, _ := envelopeArg["data"].(map[string]any)
	syncBlock, _ := data["sync"].(map[string]any)
	if syncBlock["enabled"] != true || syncBlock["endpoint"] != SyncPath {
		t.Errorf("sync block = %v, want the board pointed at this plugin's route", syncBlock)
	}
	if scope, _ := syncBlock["scope"].(string); !strings.Contains(scope, "Filters below narrow this set") {
		t.Errorf("scope = %q; the board must say its filters are a view over what was sent", scope)
	}
}

// TestOpenReportsAnOutageAndCreatesNothing is the expected behavior when the
// application is down: this plugin reports it, and every other Tangent surface
// is untouched.
func TestOpenReportsAnOutageAndCreatesNothing(t *testing.T) {
	fake := startFakeAlmanac(t, nil)
	board, tools := wired(t, fake)
	fake.server.Close()

	if _, err := board.Open(context.Background(), OpenInput{}); err == nil {
		t.Fatal("Open succeeded with Almanac down")
	} else if !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("Open = %v, want the outage named distinguishably", err)
	}
	if names := tools.names(); len(names) != 0 {
		t.Errorf("tool calls = %v; an outage must not leave a room behind", names)
	}
}

// ── A bounded card set says it was bounded ──────────────────────────────────

// TestTheListAsksForOneMoreRecordThanItWillShow pins the request bytes, not the
// behavior they produce. Truncation detection rests entirely on that extra row,
// so a refactor that tidied the +1 away would leave every other test green and
// silently restore the defect.
func TestTheListAsksForOneMoreRecordThanItWillShow(t *testing.T) {
	fake := startFakeAlmanac(t, records(5))
	board, tools := wired(t, fake)
	tools.answer("tangent.session_create", `{"roomID":"room-1","url":"u"}`)

	if _, err := board.Open(context.Background(), OpenInput{Limit: 3}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !strings.Contains(fake.lastListQuery, "limit=4") {
		t.Errorf("Almanac query = %q, want limit=4 for a board of 3", fake.lastListQuery)
	}
}

// TestABoundedCardSetSaysItWasBounded: a board that sent a subset must not let
// it read as the whole set, and a complete set must not carry the warning —
// a warning that always fires teaches the participant to ignore it.
func TestABoundedCardSetSaysItWasBounded(t *testing.T) {
	fake := startFakeAlmanac(t, records(10))
	board, tools := wired(t, fake)
	tools.answer("tangent.session_create", `{"roomID":"room-1","url":"u"}`)

	cut, err := board.Open(context.Background(), OpenInput{Limit: 4})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if cut.Cards != 4 || !cut.Truncated {
		t.Fatalf("Open = {cards:%d truncated:%v}, want 4 cards and the cut reported",
			cut.Cards, cut.Truncated)
	}
	if !strings.Contains(cut.Scope, "and there are more") ||
		!strings.Contains(cut.Scope, "NOT the whole set") {
		t.Errorf("scope = %q, want it to say the set was cut", cut.Scope)
	}

	whole, err := board.Open(context.Background(), OpenInput{Limit: 40})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if whole.Truncated || strings.Contains(whole.Scope, "there are more") {
		t.Errorf("a complete set claimed to be cut: %q", whole.Scope)
	}
}

// TestTheCardLimitIsBoundedAtBothEnds pins the numbers the tool schema
// advertises, so the two cannot drift.
func TestTheCardLimitIsBoundedAtBothEnds(t *testing.T) {
	if got := clampCards(0); got != DefaultCards {
		t.Errorf("clampCards(0) = %d, want the default", got)
	}
	if got := clampCards(5000); got != MaximumCards {
		t.Errorf("clampCards(5000) = %d, want the ceiling", got)
	}
	if !strings.Contains(string(openToolSchema), strconv.Itoa(MaximumCards)) {
		t.Errorf("the open tool schema does not advertise the ceiling of %d", MaximumCards)
	}
}

// ── Syncing ─────────────────────────────────────────────────────────────────

// surfaceWithBoard renders the two payloads a real surface carries, and they are
// DIFFERENT: the canonical request snapshot is the envelope's `data` block,
// while the whole envelope is retained separately as the presentation artifact.
// A fake that put the envelope in both would hide a decode bug that only appears
// against a real surface.
func surfaceWithBoard(t *testing.T, staged map[string]string, cards []Record) string {
	t.Helper()
	data := BuildBoard("board-1", "Almanac", cards, ActiveStatuses,
		Source{App: "almanac"},
		&Sync{Enabled: true, Endpoint: SyncPath, Label: "Sync", StageLabel: "Move to"},
		"2026-09-11T00:00:00Z")
	envelopeMap := boardEnvelope("env-1", "Almanac", data,
		ListFilters{Statuses: ActiveStatuses}, nil)

	requestSnapshot, err := json.Marshal(envelopeMap["data"])
	if err != nil {
		t.Fatalf("encode request snapshot: %v", err)
	}
	presented, err := json.Marshal(envelopeMap)
	if err != nil {
		t.Fatalf("encode presented envelope: %v", err)
	}

	stagedChanges := map[string]map[string]string{}
	for cardID, columnID := range staged {
		stagedChanges[cardID] = map[string]string{"column_id": columnID}
	}
	draft, err := json.Marshal(map[string]any{"staged_changes": stagedChanges})
	if err != nil {
		t.Fatalf("encode draft: %v", err)
	}

	payload, err := json.Marshal(map[string]any{
		"interactions": []map[string]any{
			{
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
			},
		},
		"drafts": []map[string]any{
			{"interaction_id": "int-1", "revision": 2, "payload": json.RawMessage(draft)},
		},
	})
	if err != nil {
		t.Fatalf("encode surface: %v", err)
	}
	return string(payload)
}

// TestSyncPushesThenPullsThenReplacesTheBoard is the requirement in one test:
// both directions, one call, no agent turn. The ORDER is the contract — pushing
// after the re-query would show the participant the state before their own
// change.
func TestSyncPushesThenPullsThenReplacesTheBoard(t *testing.T) {
	seed := records(3)
	fake := startFakeAlmanac(t, seed)
	board, tools := wired(t, fake)
	tools.answer("tangent.surface_get",
		surfaceWithBoard(t, map[string]string{"REC-000": "review"}, seed))

	result, err := board.Sync(context.Background(), SyncInput{RoomID: "room-1"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	want := []string{"tangent.surface_get", "tangent.interaction_cancel", "tangent.session_advance"}
	got := tools.names()
	if len(got) != len(want) {
		t.Fatalf("tool calls = %v, want %v", got, want)
	}
	for index, name := range want {
		if got[index] != name {
			t.Fatalf("tool calls = %v, want %v", got, want)
		}
	}
	cancel, _ := tools.called("tangent.interaction_cancel")
	if cancel.Arguments["cause"] != "caller_withdrawn" {
		t.Errorf("cancel cause = %v; the participant did not cancel anything",
			cancel.Arguments["cause"])
	}
	if result.Cards != 3 {
		t.Errorf("cards = %d, want the re-queried set", result.Cards)
	}
	if len(result.Applied) != 1 || result.Applied[0].RecordID != "REC-000" {
		t.Errorf("result.applied = %+v, want REC-000 moved", result.Applied)
	}
	if len(fake.applied) != 1 {
		t.Errorf("Almanac writes = %v, want exactly the staged move", fake.applied)
	}
	// The staged move is mechanical by the scaffold's placeholder rule, so it
	// is applied here rather than handed back. Change mechanical() and this
	// assertion changes with it — which is the point of asserting it.
	if len(result.Requests) != 0 {
		t.Errorf("requests = %+v; a mechanical move is applied, not handed back", result.Requests)
	}
}

// TestABoardLeftStagedAndNeverSyncedChangesNothing is what makes "a draft is not
// a decision" real rather than verbal.
func TestABoardLeftStagedAndNeverSyncedChangesNothing(t *testing.T) {
	seed := records(3)
	fake := startFakeAlmanac(t, seed)
	board, tools := wired(t, fake)
	tools.answer("tangent.session_create", `{"roomID":"room-1","url":"u"}`)

	if _, err := board.Open(context.Background(), OpenInput{}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	// The participant stages moves in the draft. Nothing calls Sync.
	_ = surfaceWithBoard(t, map[string]string{"REC-000": "review"}, seed)

	if len(fake.applied) != 0 {
		t.Errorf("Almanac writes = %v; staging is intent, and the press is the decision",
			fake.applied)
	}
}

// TestSyncRefusesABoardThisPluginDidNotOpen. A room may hold a board of the same
// kind that some other plugin put there; re-querying it with filters this plugin
// invented would silently replace someone else's surface.
func TestSyncRefusesABoardThisPluginDidNotOpen(t *testing.T) {
	fake := startFakeAlmanac(t, nil)
	board, tools := wired(t, fake)
	tools.answer("tangent.surface_get", `{
		"interactions": [{
			"interaction_id": "int-1", "state": "presented", "revision": 1,
			"request_snapshot": {"board_id": "other"},
			"definition_binding": {"kind": "`+EnvelopeType+`"}
		}]
	}`)

	if _, err := board.Sync(context.Background(), SyncInput{RoomID: "room-1"}); err == nil {
		t.Fatal("Sync accepted a board this plugin did not open")
	} else if !strings.Contains(err.Error(), metaFiltersKey) {
		t.Errorf("Sync = %v, want the missing filters named", err)
	}
}
