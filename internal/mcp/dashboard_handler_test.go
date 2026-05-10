package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDashboard_ReopenAndPersistState(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "dashboard")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	firstDone := make(chan advanceResult, 1)
	go func() {
		firstDone <- callDashboard(t, rg, roomID, "dashboard-1", map[string]any{
			"dashboard_id": "dashboard-1",
			"title":        "Ops dashboard",
			"tiles": []any{
				map[string]any{
					"tile_id": "tile-open",
					"kind":    "room_count",
					"title":   "Open rooms",
					"value":   "4",
				},
			},
			"layout": []any{
				map[string]any{"tile_id": "tile-open", "x": 0, "y": 0, "w": 2, "h": 1},
			},
			"summary": map[string]any{
				"headline":          "4 open rooms",
				"active_room_count": 4,
			},
		})
	}()

	select {
	case early := <-firstDone:
		t.Fatalf("dashboard returned before ws frame: err=%v isError=%v body=%s", early.err, early.result != nil && early.result.IsError, extractText(t, early.result))
	case <-time.After(200 * time.Millisecond):
	}

	firstFrame := readWSFrame(t, conn, 3*time.Second)
	firstEnvelope, _ := firstFrame["envelope"].(map[string]any)
	firstData, _ := firstEnvelope["data"].(map[string]any)
	if got := firstData["dashboard_id"]; got != "dashboard-1" {
		t.Fatalf("dashboard_id = %v, want dashboard-1", got)
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "dashboard-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "dashboard-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"dashboard_id": "dashboard-1",
				"action":       "refresh",
				"query_state": map[string]any{
					"search": "open",
					"scope":  "active",
					"sort": []any{
						map[string]any{"field": "updated_at", "direction": "desc"},
					},
				},
			},
		},
	})

	firstRes := <-firstDone
	if firstRes.err != nil {
		t.Fatalf("first dashboard transport err: %v", firstRes.err)
	}
	if firstRes.result.IsError {
		t.Fatalf("first dashboard IsError=true: %s", extractText(t, firstRes.result))
	}
	var accepted struct {
		Payload struct {
			Outcome    string `json:"outcome"`
			Action     string `json:"action"`
			SnapshotID string `json:"snapshot_id"`
		} `json:"payload"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, firstRes.result)), &accepted); decodeErr != nil {
		t.Fatalf("unmarshal accepted dashboard response: %v", decodeErr)
	}
	if accepted.Payload.Outcome != "accepted" {
		t.Fatalf("accepted outcome = %q, want accepted", accepted.Payload.Outcome)
	}
	if accepted.Payload.Action != "refresh" {
		t.Fatalf("accepted action = %q, want refresh", accepted.Payload.Action)
	}
	if accepted.Payload.SnapshotID != "dashboard-1-snapshot-001" {
		t.Fatalf("snapshot_id = %q, want dashboard-1-snapshot-001", accepted.Payload.SnapshotID)
	}

	secondDone := make(chan advanceResult, 1)
	go func() {
		secondDone <- callDashboard(t, rg, roomID, "dashboard-2", map[string]any{
			"dashboard_id": "dashboard-1",
			"tiles":        []any{},
		})
	}()

	select {
	case early := <-secondDone:
		t.Fatalf("dashboard reopen returned before ws frame: err=%v isError=%v body=%s", early.err, early.result != nil && early.result.IsError, extractText(t, early.result))
	case <-time.After(200 * time.Millisecond):
	}

	secondFrame := readWSFrame(t, conn, 3*time.Second)
	secondEnvelope, _ := secondFrame["envelope"].(map[string]any)
	secondData, _ := secondEnvelope["data"].(map[string]any)
	if got := secondData["title"]; got != "Ops dashboard" {
		t.Fatalf("reopened title = %v, want Ops dashboard", got)
	}
	secondQueryState, _ := secondData["query_state"].(map[string]any)
	if got := secondQueryState["search"]; got != "open" {
		t.Fatalf("reopened query_state.search = %v, want open", got)
	}
	reopenedTiles, _ := secondData["tiles"].([]any)
	if got := len(reopenedTiles); got != 1 {
		t.Fatalf("reopened tiles len = %d, want 1", got)
	}
	history, _ := secondData["snapshot_history"].([]any)
	if got := len(history); got != 1 {
		t.Fatalf("reopened snapshot_history len = %d, want 1", got)
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "cancel",
		"envelopeId": "dashboard-2",
	})
	secondRes := <-secondDone
	if secondRes.err != nil {
		t.Fatalf("second dashboard transport err: %v", secondRes.err)
	}
	if secondRes.result.IsError {
		t.Fatalf("second dashboard IsError=true: %s", extractText(t, secondRes.result))
	}

	getRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent.session_get",
		Arguments: map[string]any{"roomID": roomID},
	})
	if err != nil {
		t.Fatalf("session_get: %v", err)
	}
	if getRes.IsError {
		t.Fatalf("session_get IsError=true: %s", extractText(t, getRes))
	}
	var state struct {
		Dashboard *struct {
			DashboardID string `json:"dashboard_id"`
			Title       string `json:"title"`
			Tiles       []struct {
				TileID string `json:"tile_id"`
			} `json:"tiles"`
		} `json:"dashboard"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, getRes)), &state); decodeErr != nil {
		t.Fatalf("unmarshal session_get: %v", decodeErr)
	}
	if state.Dashboard == nil {
		t.Fatal("dashboard is nil")
	}
	if state.Dashboard.DashboardID != "dashboard-1" {
		t.Fatalf("dashboard_id = %q, want dashboard-1", state.Dashboard.DashboardID)
	}
	if state.Dashboard.Title != "Ops dashboard" {
		t.Fatalf("title = %q, want Ops dashboard", state.Dashboard.Title)
	}
	if len(state.Dashboard.Tiles) != 1 || state.Dashboard.Tiles[0].TileID != "tile-open" {
		t.Fatalf("tiles = %#v", state.Dashboard.Tiles)
	}
}

func TestDashboard_InvalidSubmitReturnsRejectedPayloadAndPreservesState(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "dashboard")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	done := make(chan advanceResult, 1)
	go func() {
		done <- callDashboard(t, rg, roomID, "dashboard-invalid-1", map[string]any{
			"dashboard_id": "dashboard-invalid",
			"title":        "Ops dashboard",
			"tiles": []any{
				map[string]any{"tile_id": "tile-open", "kind": "room_count", "title": "Open rooms"},
			},
		})
	}()

	select {
	case early := <-done:
		t.Fatalf("invalid dashboard returned before ws frame: err=%v isError=%v body=%s", early.err, early.result != nil && early.result.IsError, extractText(t, early.result))
	case <-time.After(200 * time.Millisecond):
	}

	_ = readWSFrame(t, conn, 3*time.Second)
	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "dashboard-invalid-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "dashboard-invalid-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"dashboard_id": "dashboard-invalid",
				"action":       "sideways",
			},
		},
	})

	res := <-done
	if res.err != nil {
		t.Fatalf("invalid dashboard transport err: %v", res.err)
	}
	if res.result.IsError {
		t.Fatalf("invalid dashboard IsError=true: %s", extractText(t, res.result))
	}

	var rejected struct {
		Payload struct {
			Outcome string `json:"outcome"`
			Errors  []struct {
				Code string `json:"code"`
			} `json:"errors"`
		} `json:"payload"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, res.result)), &rejected); decodeErr != nil {
		t.Fatalf("unmarshal rejected dashboard response: %v", decodeErr)
	}
	if rejected.Payload.Outcome != "rejected" {
		t.Fatalf("rejected outcome = %q, want rejected", rejected.Payload.Outcome)
	}
	if len(rejected.Payload.Errors) != 1 || rejected.Payload.Errors[0].Code != "INVALID_ACTION" {
		t.Fatalf("rejected errors = %#v, want INVALID_ACTION", rejected.Payload.Errors)
	}

	getRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent.session_get",
		Arguments: map[string]any{"roomID": roomID},
	})
	if err != nil {
		t.Fatalf("session_get: %v", err)
	}
	if getRes.IsError {
		t.Fatalf("session_get IsError=true: %s", extractText(t, getRes))
	}
	var state struct {
		Dashboard *struct {
			SnapshotHistory []struct {
				SnapshotID string `json:"snapshot_id"`
			} `json:"snapshot_history"`
		} `json:"dashboard"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, getRes)), &state); decodeErr != nil {
		t.Fatalf("unmarshal session_get: %v", decodeErr)
	}
	if state.Dashboard == nil {
		t.Fatal("dashboard is nil")
	}
	if len(state.Dashboard.SnapshotHistory) != 0 {
		t.Fatalf("snapshot_history len = %d, want 0", len(state.Dashboard.SnapshotHistory))
	}
}

func callDashboard(
	t *testing.T,
	rg *sessionRig,
	roomID string,
	envelopeID string,
	data map[string]any,
) advanceResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.dashboard",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   envelopeID,
				"type": "tangent.dashboard",
				"data": data,
				"meta": map[string]any{
					"roomID": roomID,
				},
			},
		},
	})
	return advanceResult{result: res, err: err}
}
