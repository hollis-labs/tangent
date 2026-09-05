package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/telemetry"
)

// This is the call-site half of the redaction guarantee.
//
// internal/telemetry's own tests prove the allowlist refuses what it does not
// name. They cannot prove that no shipped call site hands it something
// forbidden in a slot that would accept it — an identifier-shaped secret in an
// identifier field, say. Only running the real path can, so this test runs one
// complete workflow through the real MCP server, the real durable substrate,
// the real room, and a real WebSocket, with a distinctive token planted in
// every position criterion 3 names, and then sweeps everything the process can
// hand out.

const leakToken = "ZZLEAKCANARY"

// plantedPositions is criterion 3's list plus the four ADR 0004 additions,
// each expressed as the thing a real caller or participant would actually put
// there.
func plantedPositions() map[string]string {
	return map[string]string{
		"request payload":     "review the " + leakToken + " rollout",
		"participant text":    "approved because " + leakToken,
		"code":                "function " + leakToken + "() { return 1 }",
		"screenshot":          "data:image/png;base64," + leakToken + "AAAA",
		"file path":           "/Users/operator/" + leakToken + "/notes.md",
		"secret":              "sk-live-" + leakToken,
		"full response":       `{"answers":{"scope":"` + leakToken + `"}}`,
		"session cookie":      "tangent_participant=" + leakToken,
		"session cookie hash": "sha256:" + leakToken,
		"effect handle id":    "handle-" + leakToken,
		"capability material": "grant:" + leakToken,
		"room url":            "http://127.0.0.1:7842/r/" + leakToken,
	}
}

// TestTelemetryCarriesNoPlantedTokenAcrossTheWholePath is the leak sweep and
// the correlation assertion in one run, because they are the same run: the
// thing that has to be correlated is the thing that must not leak.
func TestTelemetryCarriesNoPlantedTokenAcrossTheWholePath(t *testing.T) {
	planted := plantedPositions()

	// The database path itself carries the token, so a driver error echoed
	// anywhere fails this test. internal/health found this leak first and its
	// probe closes it; telemetry has strictly more places to reproduce it.
	root := filepath.Join(t.TempDir(), leakToken+"-state")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create planted state directory: %v", err)
	}
	rg := newSessionRigWith(t, sessionRigOptions{
		durable: true,
		window:  30 * time.Second,
		dbPath:  filepath.Join(root, "tangent.db"),
	})
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "leak-"+leakToken)
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	// form-collect is the fixture with the widest surface: a schema, free-text
	// answers, and a notes field, so the planted values reach the request
	// snapshot, the presented envelope, and the immutable resolution.
	fixture := namedFixture(t, "tangent.form-collect")
	const envelopeID = "leak-envelope-1"
	data := map[string]any{
		"form_id": "form-1",
		"title":   planted["request payload"],
		"schema": map[string]any{"fields": []any{
			map[string]any{
				"field_id": "scope",
				"label":    planted["code"],
				"kind":     "text",
			},
		}},
	}
	done := make(chan advanceResult, 1)
	go func() { done <- callWorkflow(t, rg, fixture, roomID, envelopeID, nil, data) }()

	frame := readWSFrame(t, conn, 10*time.Second)
	if frame["envelopeId"] != envelopeID {
		t.Fatalf("envelopeId = %v, want %s", frame["envelopeId"], envelopeID)
	}
	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": envelopeID,
		"revision":   frame["revision"],
		"response": map[string]any{
			"v": 1, "envelopeId": envelopeID, "kind": "data", "status": "submitted",
			"completedAt": fixedCompletedAt,
			"payload": map[string]any{
				"form_id":   "form-1",
				"action_id": "submit",
				"answers":   map[string]any{"scope": planted["participant text"]},
				"notes":     planted["full response"],
			},
		},
	})

	result := <-done
	if result.err != nil {
		t.Fatalf("workflow transport err: %v", result.err)
	}
	if result.result.IsError {
		t.Fatalf("workflow IsError=true: %s", extractText(t, result.result))
	}

	// --- what telemetry recorded -------------------------------------------

	records := awaitObservations(t, rg, envelopeID)
	encoded, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("marshal observations: %v", err)
	}
	sweepEverything(t, "telemetry_events", string(encoded), planted)

	snapshot, err := json.Marshal(rg.recorder.Metrics().Snapshot())
	if err != nil {
		t.Fatalf("marshal metric snapshot: %v", err)
	}
	sweepEverything(t, "metric snapshot", string(snapshot), planted)

	queried := callTool(t, rg, "tangent.telemetry_query", map[string]any{
		"interaction_id":    singleInteractionID(t, rg, envelopeID),
		"include_metrics":   true,
		"include_aggregate": true,
	})
	sweepEverything(t, "tangent.telemetry_query", queried, planted)

	reported := callTool(t, rg, "tangent.health_report", map[string]any{})
	// The health report is swept for the planted token only. Its operator
	// actions legitimately name commands and probe paths, and CW-20260825-0066
	// already owns the shape assertions for that document.
	if strings.Contains(reported, leakToken) {
		t.Fatalf("tangent.health_report carried the planted token:\n%s", reported)
	}

	// The canary. A non-zero dropped-attribute count means a shipped call site
	// tried to record something the allowlist refused — which did not leak,
	// but is a bug in that call site.
	for _, series := range rg.recorder.Metrics().Snapshot().Counters[telemetry.MetricAttributesDropped] {
		if series.Value != 0 {
			t.Fatalf("a shipped call site produced a refused attribute: %+v", series)
		}
	}
}

// TestOneTraceFollowsTheWholePath is criterion 1.
//
// Admission happens on an MCP goroutine, presentation and resolution on the
// WebSocket's, and delivery back on the caller's. None of them hands the next
// one a trace: each recomputes it from the durable record. The assertion is
// that they nevertheless agree.
func TestOneTraceFollowsTheWholePath(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: 30 * time.Second})
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "correlation")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	fixture := namedFixture(t, "tangent.triage")
	const envelopeID = "correlated-1"
	done := make(chan advanceResult, 1)
	go func() { done <- callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil) }()

	frame := readWSFrame(t, conn, 10*time.Second)
	response := fixture.participantResponse(envelopeID)
	response["revision"] = frame["revision"]
	writeWSFrame(t, conn, response)
	if result := <-done; result.err != nil || result.result.IsError {
		t.Fatalf("workflow did not complete: %v", result.err)
	}

	records := awaitObservations(t, rg, envelopeID)
	seen := map[string]string{}
	for _, record := range records {
		seen[record.Name] = record.TraceID
	}
	for _, name := range []string{
		telemetry.EventInteractionAdmitted,
		telemetry.EventInteractionPresented,
		telemetry.EventInteractionResolved,
		telemetry.EventDeliveryRecorded,
	} {
		if seen[name] == "" {
			t.Fatalf("no %s observation was recorded (saw %v)", name, seen)
		}
	}
	trace := seen[telemetry.EventInteractionAdmitted]
	for name, id := range seen {
		if id != trace {
			t.Fatalf("%s is filed under trace %s, admission under %s", name, id, trace)
		}
	}

	// The identity is derived, so it must be recomputable from the durable
	// record alone — with nothing propagated and no process state consulted.
	var callerScope, idempotencyKey string
	if readErr := rg.db.QueryRow(
		`SELECT caller_scope, idempotency_key FROM interactions WHERE legacy_envelope_id = ?`,
		envelopeID,
	).Scan(&callerScope, &idempotencyKey); readErr != nil {
		t.Fatalf("read interaction identity: %v", readErr)
	}
	if recomputed := telemetry.TraceFor(callerScope, idempotencyKey).String(); recomputed != trace {
		t.Fatalf("trace recomputed from the record = %s, recorded = %s", recomputed, trace)
	}

	// Connection lifecycle is filed under the room rather than the invocation,
	// which is the honest boundary: attaching is not part of a request.
	connections, err := rg.observed.Query(context.Background(), telemetry.Query{
		EventName: telemetry.EventConnectionAttached, RoomID: roomID,
	})
	if err != nil {
		t.Fatalf("query connection observations: %v", err)
	}
	if len(connections) == 0 {
		t.Fatal("the WebSocket attachment was never observed")
	}
	if connections[0].TraceID != telemetry.TraceForRoom(roomID).String() {
		t.Fatalf("attachment trace = %s, want the room trace", connections[0].TraceID)
	}
}

// --- helpers -----------------------------------------------------------------

// awaitObservations waits for the durable trail of one envelope. The delivery
// observation is written after the caller's result is already on the wire, so
// a test that read immediately would race it.
func awaitObservations(t *testing.T, rg *sessionRig, envelopeID string) []telemetry.Record {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		records, err := rg.observed.Query(context.Background(), telemetry.Query{
			InteractionID: singleInteractionID(t, rg, envelopeID), Limit: telemetry.MaxQueryLimit,
		})
		if err != nil {
			t.Fatalf("query observations: %v", err)
		}
		for _, record := range records {
			if record.Name == telemetry.EventDeliveryRecorded {
				return records
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no delivery observation within the deadline (saw %d observations)", len(records))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// sweepEverything asserts that text carries none of the planted values, none
// of the raw token, and none of the shapes ADR 0002 §8 forbids outright.
func sweepEverything(t *testing.T, what, text string, planted map[string]string) {
	t.Helper()
	if strings.Contains(text, leakToken) {
		t.Fatalf("%s carried the planted token:\n%s", what, text)
	}
	for position, value := range planted {
		if strings.Contains(text, value) {
			t.Fatalf("%s carried the planted %s:\n%s", what, position, text)
		}
	}
	for _, forbidden := range []string{
		".db", "/Users/", "/var/", "/tmp/", "sqlite",
		"http://", "https://", "data:image", "tangent_participant",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("%s carried %q:\n%s", what, forbidden, text)
		}
	}
}

func namedFixture(t *testing.T, tool string) workflowFixture {
	t.Helper()
	for _, fixture := range shippedRoomWorkflows() {
		if fixture.tool == tool {
			return fixture
		}
	}
	t.Fatalf("no shipped fixture for %s", tool)
	return workflowFixture{}
}

func callTool(t *testing.T, rg *sessionRig, name string, arguments map[string]any) string {
	t.Helper()
	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: name, Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s IsError=true: %s", name, extractText(t, res))
	}
	return extractText(t, res)
}
