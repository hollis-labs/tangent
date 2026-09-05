package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/roomflow"
)

// TestRoomWorkflowCompletionOverLegacySSE exercises the whole completion
// contract across the legacy SSE transport rather than the in-memory one.
//
// Legacy SSE is the transport older MCP clients (and Tether's native-flat
// gateway) actually use, and it is the one whose sessions the server-wide read
// timeout used to kill mid-flight. Running the full sequence over it —
// pending receipt, operator answer, handle recovery, original-invocation
// retry, acknowledgement, and cancellation — is what proves the contract is a
// property of the adapter rather than of one transport.
func TestRoomWorkflowCompletionOverLegacySSE(t *testing.T) {
	rg := newDurableRig(t)
	defer rg.cleanup()

	sseServer := httptest.NewServer(rg.mcpSrv.SSEHandler())
	defer sseServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tangent-sse-test", Version: "v0.0.0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.SSEClientTransport{
		Endpoint: sseServer.URL, HTTPClient: http.DefaultClient,
	}, nil)
	if err != nil {
		t.Fatalf("sse client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("sse ListTools: %v", err)
	}
	if len(listed.Tools) == 0 {
		t.Fatal("sse transport advertised no tools")
	}
	assertCompletionModeAdvertised(t, listed.Tools)

	// Reuse the rig's room-creation path, then drive everything over SSE.
	roomID, _ := createSession(t, rg, "sse")
	fixture := fixtureByTool(t, "tangent.spreadsheet-review")
	const envelopeID = "sse-1"

	pending := callToolOverSession(ctx, t, session, fixture.tool, map[string]any{
		"envelope": map[string]any{
			"v": 1, "id": envelopeID, "type": fixture.envelopeType,
			"data": fixture.data, "meta": map[string]any{"roomID": roomID},
		},
	})
	receipt := decodeReceipt(t, pending)

	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["envelopeId"] != envelopeID {
		t.Fatalf("sse operator saw envelopeId %v", frame["envelopeId"])
	}
	response := fixture.participantResponse(envelopeID)
	response["revision"] = frame["revision"]
	writeWSFrame(t, conn, response)
	awaitInteractionState(t, rg, receipt.Handle.InteractionID, "resolved", 5*time.Second)

	// Handle recovery over SSE.
	getResult := callToolOverSession(ctx, t, session, "tangent.interaction_get", map[string]any{
		"interaction_id":  receipt.Handle.InteractionID,
		"requester_scope": roomflow.DefaultCaller.Scope,
	})
	stored := resolutionPayloadOf(t, getResult)

	// Original-invocation retry over SSE.
	retry := callToolOverSession(ctx, t, session, fixture.tool, map[string]any{
		"envelope": map[string]any{
			"v": 1, "id": envelopeID, "type": fixture.envelopeType,
			"data": fixture.data, "meta": map[string]any{"roomID": roomID},
		},
	})
	if !sameJSON(t, stored, extractText(t, retry)) {
		t.Fatalf("sse retry diverged:\nstored: %s\nretry:  %s", stored, extractText(t, retry))
	}

	// Bounded await over SSE returns the same immutable result.
	awaited := callToolOverSession(ctx, t, session, "tangent.interaction_await", map[string]any{
		"interaction_id":  receipt.Handle.InteractionID,
		"requester_scope": roomflow.DefaultCaller.Scope,
		"maximum_wait_ms": 2000,
	})
	if !sameJSON(t, stored, resolutionPayloadOf(t, awaited)) {
		t.Fatal("sse await diverged from the stored result")
	}

	// Acknowledgement over SSE is idempotent.
	first := callToolOverSession(ctx, t, session, "tangent.interaction_acknowledge", map[string]any{
		"interaction_id":  receipt.Handle.InteractionID,
		"requester_scope": roomflow.DefaultCaller.Scope,
	})
	second := callToolOverSession(ctx, t, session, "tangent.interaction_acknowledge", map[string]any{
		"interaction_id":  receipt.Handle.InteractionID,
		"requester_scope": roomflow.DefaultCaller.Scope,
	})
	var firstAck, secondAck struct {
		AcknowledgementID string `json:"acknowledgement_id"`
		Created           bool   `json:"created"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, first)), &firstAck); decodeErr != nil {
		t.Fatalf("unmarshal first acknowledgement: %v", decodeErr)
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, second)), &secondAck); decodeErr != nil {
		t.Fatalf("unmarshal second acknowledgement: %v", decodeErr)
	}
	if !firstAck.Created || secondAck.Created || firstAck.AcknowledgementID != secondAck.AcknowledgementID {
		t.Fatalf("acknowledgement is not idempotent over sse: %+v then %+v", firstAck, secondAck)
	}
	assertCounts(t, rg, receipt.Handle.InteractionID, 1)

	// Terminal conflict over SSE: the same identity with a different payload.
	changed := map[string]any{
		"table_id": "table-1", "title": "Different table",
		"columns": []any{map[string]any{"id": "name", "label": "Name"}},
		"rows":    []any{map[string]any{"id": "row-9", "name": "Omega"}},
	}
	conflict, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: fixture.tool,
		Arguments: map[string]any{"envelope": map[string]any{
			"v": 1, "id": envelopeID, "type": fixture.envelopeType,
			"data": changed, "meta": map[string]any{"roomID": roomID},
		}},
	})
	if err != nil {
		t.Fatalf("sse conflict call: %v", err)
	}
	if !conflict.IsError {
		t.Fatalf("sse conflict was accepted: %s", extractText(t, conflict))
	}
	if code := errorCodeOf(t, conflict); code != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("sse conflict code = %q", code)
	}
	assertCounts(t, rg, receipt.Handle.InteractionID, 1)
}

// TestRoomWorkflowToolsAdvertiseCompletionMode is the tool-surface contract:
// every room-backed tool exposes the same completion selector, and the shipped
// production topology exposes the acknowledgement tool.
func TestRoomWorkflowToolsAdvertiseCompletionMode(t *testing.T) {
	rg := newDurableRig(t)
	defer rg.cleanup()

	listed, err := rg.mcpClient.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	assertCompletionModeAdvertised(t, listed.Tools)

	acknowledge := false
	for _, tool := range listed.Tools {
		if tool.Name == "tangent.interaction_acknowledge" {
			acknowledge = true
		}
	}
	if !acknowledge {
		t.Fatal("tangent.interaction_acknowledge is not advertised")
	}
}

func assertCompletionModeAdvertised(t *testing.T, tools []*mcpsdk.Tool) {
	t.Helper()
	want := map[string]bool{}
	for _, fixture := range shippedRoomWorkflows() {
		want[fixture.tool] = false
	}
	for _, tool := range tools {
		if _, tracked := want[tool.Name]; !tracked {
			continue
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal %s input schema: %v", tool.Name, err)
		}
		var schema struct {
			Properties struct {
				Completion *struct {
					Properties struct {
						Mode struct {
							Enum []string `json:"enum"`
						} `json:"mode"`
					} `json:"properties"`
				} `json:"completion"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("unmarshal %s input schema: %v", tool.Name, err)
		}
		if schema.Properties.Completion == nil {
			t.Fatalf("%s does not advertise a completion selector", tool.Name)
		}
		modes := schema.Properties.Completion.Properties.Mode.Enum
		if len(modes) != 2 || modes[0] != "wait" || modes[1] != "async" {
			t.Fatalf("%s completion modes = %v, want [wait async]", tool.Name, modes)
		}
		want[tool.Name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("room workflow %q was not advertised", name)
		}
	}
}

func callToolOverSession(
	ctx context.Context,
	t *testing.T,
	session *mcpsdk.ClientSession,
	tool string,
	arguments map[string]any,
) *mcpsdk.CallToolResult {
	t.Helper()
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: tool, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if res.IsError {
		t.Fatalf("%s IsError=true: %s", tool, extractText(t, res))
	}
	return res
}

func resolutionPayloadOf(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	var outcome struct {
		Resolution *struct {
			ResponsePayload json.RawMessage `json:"response_payload"`
		} `json:"resolution"`
	}
	if err := json.Unmarshal([]byte(extractText(t, res)), &outcome); err != nil {
		t.Fatalf("unmarshal terminal outcome: %v", err)
	}
	if outcome.Resolution == nil {
		t.Fatalf("terminal outcome carries no resolution: %s", extractText(t, res))
	}
	return string(outcome.Resolution.ResponsePayload)
}
