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
	reopenedTiles, _ := secondData["tiles"].([]any)
	if got := len(reopenedTiles); got != 1 {
		t.Fatalf("reopened tiles len = %d, want 1", got)
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
