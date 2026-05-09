package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSpreadsheetReview_SubmitReopenAndPersistState(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "spreadsheet-review")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	firstDone := make(chan advanceResult, 1)
	go func() {
		firstDone <- callSpreadsheetReview(t, rg, roomID, "spreadsheet-1", map[string]any{
			"table_id": "table-1",
			"title":    "Spreadsheet review",
			"columns": []any{
				map[string]any{"id": "name", "label": "Name"},
				map[string]any{"id": "status", "label": "Status"},
			},
			"rows": []any{
				map[string]any{"id": "row-1", "name": "Alpha", "status": "open"},
				map[string]any{"id": "row-2", "name": "Beta", "status": "closed"},
			},
			"row_actions": []any{
				map[string]any{"id": "approve", "label": "Approve"},
			},
		})
	}()

	select {
	case early := <-firstDone:
		if early.err != nil {
			t.Fatalf("spreadsheet-review returned early transport err: %v", early.err)
		}
		if early.result != nil && early.result.IsError {
			t.Fatalf("spreadsheet-review returned early IsError=true: %s", extractText(t, early.result))
		}
		t.Fatal("spreadsheet-review returned before envelope dispatch")
	case <-time.After(100 * time.Millisecond):
	}

	awaitPendingRoom(t, rg.mgr, roomID, 2*time.Second)
	firstFrame := readWSFrame(t, conn, 3*time.Second)
	if firstFrame["envelopeId"] != "spreadsheet-1" {
		t.Fatalf("envelopeId = %v, want spreadsheet-1", firstFrame["envelopeId"])
	}
	firstEnvelope, _ := firstFrame["envelope"].(map[string]any)
	firstData, _ := firstEnvelope["data"].(map[string]any)
	firstRows, _ := firstData["rows"].([]any)
	if len(firstRows) != 2 {
		t.Fatalf("rows len = %d, want 2", len(firstRows))
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "spreadsheet-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "spreadsheet-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"table_id":         "table-1",
				"selected_row_ids": []any{"row-1"},
				"selected_rows": []any{
					map[string]any{"id": "row-1", "name": "Alpha", "status": "open"},
				},
				"query_state": map[string]any{
					"search": "Alpha",
				},
				"notes":     "first pass",
				"action_id": "approve",
				"saved_views": []any{
					map[string]any{
						"name": "Open rows",
						"query_state": map[string]any{
							"filters": []any{
								map[string]any{"column_id": "status", "op": "eq", "value": "open"},
							},
						},
					},
				},
			},
		},
	})

	firstRes := <-firstDone
	if firstRes.err != nil {
		t.Fatalf("first spreadsheet-review transport err: %v", firstRes.err)
	}
	if firstRes.result.IsError {
		t.Fatalf("first spreadsheet-review IsError=true: %s", extractText(t, firstRes.result))
	}
	var firstToolResp struct {
		Payload struct {
			TableID        string           `json:"table_id"`
			SelectedRowIDs []string         `json:"selected_row_ids"`
			QueryState     map[string]any   `json:"query_state"`
			ActionID       string           `json:"action_id"`
			SelectedRows   []map[string]any `json:"selected_rows"`
		} `json:"payload"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, firstRes.result)), &firstToolResp); decodeErr != nil {
		t.Fatalf("unmarshal first spreadsheet-review response: %v", decodeErr)
	}
	if firstToolResp.Payload.TableID != "table-1" {
		t.Fatalf("table_id = %q, want table-1", firstToolResp.Payload.TableID)
	}
	if len(firstToolResp.Payload.SelectedRowIDs) != 1 || firstToolResp.Payload.SelectedRowIDs[0] != "row-1" {
		t.Fatalf("selected_row_ids = %#v, want [row-1]", firstToolResp.Payload.SelectedRowIDs)
	}
	if firstToolResp.Payload.ActionID != "approve" {
		t.Fatalf("action_id = %q, want approve", firstToolResp.Payload.ActionID)
	}

	secondDone := make(chan advanceResult, 1)
	go func() {
		secondDone <- callSpreadsheetReview(t, rg, roomID, "spreadsheet-2", map[string]any{
			"table_id": "table-1",
			"columns": []any{
				map[string]any{"id": "name", "label": "Name"},
				map[string]any{"id": "status", "label": "Status"},
			},
			"rows": []any{
				map[string]any{"id": "row-1", "name": "Alpha", "status": "open"},
				map[string]any{"id": "row-2", "name": "Beta", "status": "closed"},
			},
			"query_state": map[string]any{
				"search": "stale",
			},
			"notes": "stale note",
		})
	}()

	awaitPendingRoom(t, rg.mgr, roomID, 2*time.Second)
	secondFrame := readWSFrame(t, conn, 3*time.Second)
	secondEnvelope, _ := secondFrame["envelope"].(map[string]any)
	secondData, _ := secondEnvelope["data"].(map[string]any)
	if got := secondData["notes"]; got != "first pass" {
		t.Fatalf("reopened notes = %v, want first pass", got)
	}
	secondQueryState, _ := secondData["query_state"].(map[string]any)
	if got := secondQueryState["search"]; got != "Alpha" {
		t.Fatalf("reopened search = %v, want Alpha", got)
	}
	secondSelectedIDs, _ := secondData["selected_row_ids"].([]any)
	if len(secondSelectedIDs) != 1 || secondSelectedIDs[0] != "row-1" {
		t.Fatalf("reopened selected_row_ids = %#v, want [row-1]", secondSelectedIDs)
	}
	secondSavedViews, _ := secondData["saved_views"].([]any)
	if len(secondSavedViews) != 1 {
		t.Fatalf("reopened saved_views len = %d, want 1", len(secondSavedViews))
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "cancel",
		"envelopeId": "spreadsheet-2",
	})
	secondRes := <-secondDone
	if secondRes.err != nil {
		t.Fatalf("second spreadsheet-review transport err: %v", secondRes.err)
	}
	if secondRes.result.IsError {
		t.Fatalf("second spreadsheet-review IsError=true: %s", extractText(t, secondRes.result))
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
		SpreadsheetReview *struct {
			TableID        string   `json:"table_id"`
			Notes          string   `json:"notes"`
			ActionID       string   `json:"action_id"`
			SelectedRowIDs []string `json:"selected_row_ids"`
			SavedViews     []struct {
				Name string `json:"name"`
			} `json:"saved_views"`
			QueryState map[string]any `json:"query_state"`
		} `json:"spreadsheet_review"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, getRes)), &state); decodeErr != nil {
		t.Fatalf("unmarshal session_get: %v", decodeErr)
	}
	if state.SpreadsheetReview == nil {
		t.Fatal("spreadsheet_review is nil")
	}
	if state.SpreadsheetReview.TableID != "table-1" {
		t.Fatalf("table_id = %q, want table-1", state.SpreadsheetReview.TableID)
	}
	if state.SpreadsheetReview.Notes != "first pass" {
		t.Fatalf("notes = %q, want first pass", state.SpreadsheetReview.Notes)
	}
	if state.SpreadsheetReview.ActionID != "approve" {
		t.Fatalf("action_id = %q, want approve", state.SpreadsheetReview.ActionID)
	}
	if len(state.SpreadsheetReview.SelectedRowIDs) != 1 || state.SpreadsheetReview.SelectedRowIDs[0] != "row-1" {
		t.Fatalf("selected_row_ids = %#v, want [row-1]", state.SpreadsheetReview.SelectedRowIDs)
	}
	if len(state.SpreadsheetReview.SavedViews) != 1 || state.SpreadsheetReview.SavedViews[0].Name != "Open rows" {
		t.Fatalf("saved_views = %#v, want Open rows", state.SpreadsheetReview.SavedViews)
	}
}

func callSpreadsheetReview(
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
		Name: "tangent.spreadsheet-review",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   envelopeID,
				"type": "tangent.spreadsheet-review",
				"data": data,
				"meta": map[string]any{
					"roomID": roomID,
				},
			},
		},
	})
	return advanceResult{result: res, err: err}
}
