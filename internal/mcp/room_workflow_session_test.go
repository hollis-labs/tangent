package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestDurableSessionOperationsRemainCorrect asserts the `tangent.session_*`
// surface still behaves the way callers depend on once workflows route through
// the durable adapter: a room reports the envelope it is presenting, history
// accumulates the accepted response, and the room list stays accurate.
func TestDurableSessionOperationsRemainCorrect(t *testing.T) {
	rg := newDurableRig(t)
	defer rg.cleanup()
	fixture := fixtureByTool(t, "tangent.triage")
	roomID, _ := createSession(t, rg, "session-ops")

	const envelopeID = "session-ops-1"
	receipt := decodeReceipt(t, callWorkflow(
		t, rg, fixture, roomID, envelopeID, map[string]any{"mode": "async"}, nil,
	).result)

	// While the interaction is open the room reports it as the current
	// envelope and the history row is a pending projection.
	state := readSessionState(t, rg, roomID)
	if state.Status != "active" || state.CurrentEnvelope == nil || state.CurrentEnvelope.ID != envelopeID {
		t.Fatalf("open session state = %+v", state)
	}
	if len(state.History) != 1 || state.History[0].Status != "pending" {
		t.Fatalf("open session history = %+v", state.History)
	}

	listed := readSessionList(t, rg, true)
	if summary, ok := listed[roomID]; !ok || summary != fixture.envelopeType {
		t.Fatalf("session_list current envelope type for %s = %q", roomID, listed[roomID])
	}

	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	frame := readWSFrame(t, conn, 3*time.Second)
	response := fixture.participantResponse(envelopeID)
	response["revision"] = frame["revision"]
	writeWSFrame(t, conn, response)
	awaitInteractionState(t, rg, receipt.Handle.InteractionID, "resolved", 3*time.Second)
	awaitLegacyProjection(t, rg, roomID, envelopeID, 3*time.Second)

	resolved := readSessionState(t, rg, roomID)
	if resolved.CurrentEnvelope != nil {
		t.Fatalf("resolved session still reports a current envelope: %+v", resolved.CurrentEnvelope)
	}
	if len(resolved.History) != 1 || resolved.History[0].Status != "submitted" {
		t.Fatalf("resolved session history = %+v", resolved.History)
	}
}

// TestDurableSessionCloseTerminalizesCanonicallyFirst asserts that closing a
// room — the one caller-side action permitted to end outstanding work —
// dispositions the canonical interactions under a named surface policy before
// the legacy room rows are torn down.
func TestDurableSessionCloseTerminalizesCanonicallyFirst(t *testing.T) {
	rg := newDurableRig(t)
	defer rg.cleanup()
	fixture := fixtureByTool(t, "tangent.triage")
	roomID, _ := createSession(t, rg, "session-close")

	const envelopeID = "session-close-1"
	receipt := decodeReceipt(t, callWorkflow(
		t, rg, fixture, roomID, envelopeID, map[string]any{"mode": "async"}, nil,
	).result)

	closeRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent.session_close",
		Arguments: map[string]any{"roomID": roomID, "status": "done"},
	})
	if err != nil {
		t.Fatalf("session_close: %v", err)
	}
	if closeRes.IsError {
		t.Fatalf("session_close IsError=true: %s", extractText(t, closeRes))
	}

	var surfaceState, closeReason string
	if err := rg.db.QueryRow(
		`SELECT lifecycle_state, COALESCE(close_reason, '') FROM surfaces WHERE id = ?`, roomID,
	).Scan(&surfaceState, &closeReason); err != nil {
		t.Fatalf("read surface: %v", err)
	}
	if surfaceState != "closed" || closeReason != "done" {
		t.Fatalf("surface after close = %q / %q", surfaceState, closeReason)
	}

	var state, cause string
	if err := rg.db.QueryRow(
		`SELECT lifecycle_state, COALESCE(terminal_cause, '') FROM interactions WHERE id = ?`,
		receipt.Handle.InteractionID,
	).Scan(&state, &cause); err != nil {
		t.Fatalf("read interaction: %v", err)
	}
	if state != "canceled" || cause != "surface_policy" {
		t.Fatalf("interaction after room close = %q / %q, want canceled / surface_policy", state, cause)
	}
	assertClosedRoomRow(t, rg.db, roomID, "done")

	// The cancellation is immutable and replays: retrying the original
	// invocation returns the v0.12 ack/cancelled shape rather than re-opening
	// the request on a closed room.
	retry := callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil)
	if retry.err != nil {
		t.Fatalf("retry after close transport err: %v", retry.err)
	}
	if retry.result.IsError {
		t.Fatalf("retry after close produced an MCP error: %s", extractText(t, retry.result))
	}
	var cancelled struct {
		Kind   string `json:"kind"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(extractText(t, retry.result)), &cancelled); err != nil {
		t.Fatalf("unmarshal retry result: %v", err)
	}
	if cancelled.Kind != "ack" || cancelled.Status != "cancelled" {
		t.Fatalf("retry after close = %+v, want the ack/cancelled shape", cancelled)
	}
}

type sessionStateView struct {
	Status          string `json:"status"`
	CurrentEnvelope *struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	} `json:"current_envelope"`
	History []struct {
		EnvelopeID string `json:"envelope_id"`
		Status     string `json:"status"`
	} `json:"envelopes_history"`
}

func readSessionState(t *testing.T, rg *sessionRig, roomID string) sessionStateView {
	t.Helper()
	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.session_get", Arguments: map[string]any{"roomID": roomID},
	})
	if err != nil {
		t.Fatalf("session_get: %v", err)
	}
	if res.IsError {
		t.Fatalf("session_get IsError=true: %s", extractText(t, res))
	}
	var state sessionStateView
	if err := json.Unmarshal([]byte(extractText(t, res)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	return state
}

func readSessionList(t *testing.T, rg *sessionRig, activeOnly bool) map[string]string {
	t.Helper()
	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.session_list", Arguments: map[string]any{"active_only": activeOnly},
	})
	if err != nil {
		t.Fatalf("session_list: %v", err)
	}
	if res.IsError {
		t.Fatalf("session_list IsError=true: %s", extractText(t, res))
	}
	var listed struct {
		Rooms []struct {
			ID                  string `json:"id"`
			CurrentEnvelopeType string `json:"current_envelope_type"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal([]byte(extractText(t, res)), &listed); err != nil {
		t.Fatalf("unmarshal session_list: %v", err)
	}
	out := map[string]string{}
	for _, entry := range listed.Rooms {
		out[entry.ID] = entry.CurrentEnvelopeType
	}
	return out
}
