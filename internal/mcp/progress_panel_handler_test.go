package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProgressPanel_SubmitReopenAndPersistState(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "progress-panel")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	firstDone := make(chan advanceResult, 1)
	go func() {
		firstDone <- callProgressPanel(t, rg, roomID, "progress-1", map[string]any{
			"panel_id": "panel-1",
			"items": []any{
				map[string]any{"item_id": "item-1", "label": "Scan repo", "status": "running"},
				map[string]any{"item_id": "item-2", "label": "Write summary", "status": "queued"},
			},
			"summary": map[string]any{
				"current_status": "running",
				"headline":       "1 running",
			},
		})
	}()
	select {
	case early := <-firstDone:
		t.Fatalf("progress-panel returned before ws frame: err=%v isError=%v body=%s", early.err, early.result != nil && early.result.IsError, extractText(t, early.result))
	case <-time.After(150 * time.Millisecond):
	}

	firstFrame := readWSFrame(t, conn, 3*time.Second)
	firstEnvelope, _ := firstFrame["envelope"].(map[string]any)
	firstData, _ := firstEnvelope["data"].(map[string]any)
	if got := firstData["panel_id"]; got != "panel-1" {
		t.Fatalf("panel_id = %v, want panel-1", got)
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "progress-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "progress-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"panel_id":         "panel-1",
				"item_id":          "item-2",
				"status":           "running",
				"summary":          "Started drafting the final summary.",
				"checkpoint_label": "Summary started",
			},
		},
	})

	firstRes := <-firstDone
	if firstRes.err != nil {
		t.Fatalf("first progress-panel transport err: %v", firstRes.err)
	}
	if firstRes.result.IsError {
		t.Fatalf("first progress-panel IsError=true: %s", extractText(t, firstRes.result))
	}
	var accepted struct {
		Status  string `json:"status"`
		Payload struct {
			Outcome      string `json:"outcome"`
			ItemID       string `json:"item_id"`
			Status       string `json:"status"`
			UpdateID     string `json:"update_id"`
			CheckpointID string `json:"checkpoint_id"`
		} `json:"payload"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, firstRes.result)), &accepted); decodeErr != nil {
		t.Fatalf("unmarshal accepted progress-panel response: %v", decodeErr)
	}
	if accepted.Payload.Outcome != "accepted" {
		t.Fatalf("accepted outcome = %q, want accepted", accepted.Payload.Outcome)
	}
	if accepted.Payload.ItemID != "item-2" || accepted.Payload.Status != "running" {
		t.Fatalf("accepted payload = %#v", accepted.Payload)
	}
	if accepted.Payload.UpdateID != "panel-1-update-001" {
		t.Fatalf("update_id = %q, want panel-1-update-001", accepted.Payload.UpdateID)
	}
	if accepted.Payload.CheckpointID != "panel-1-checkpoint-001" {
		t.Fatalf("checkpoint_id = %q, want panel-1-checkpoint-001", accepted.Payload.CheckpointID)
	}

	secondDone := make(chan advanceResult, 1)
	go func() {
		secondDone <- callProgressPanel(t, rg, roomID, "progress-2", map[string]any{
			"panel_id": "panel-1",
			"items": []any{
				map[string]any{"item_id": "item-1", "label": "Scan repo", "status": "running"},
				map[string]any{"item_id": "item-2", "label": "Write summary", "status": "queued"},
			},
		})
	}()
	select {
	case early := <-secondDone:
		t.Fatalf("reopen progress-panel returned before ws frame: err=%v isError=%v body=%s", early.err, early.result != nil && early.result.IsError, extractText(t, early.result))
	case <-time.After(150 * time.Millisecond):
	}

	secondFrame := readWSFrame(t, conn, 3*time.Second)
	secondEnvelope, _ := secondFrame["envelope"].(map[string]any)
	secondData, _ := secondEnvelope["data"].(map[string]any)
	items, _ := secondData["items"].([]any)
	if got := len(items); got != 2 {
		t.Fatalf("reopened items len = %d, want 2", got)
	}
	itemTwo := items[1].(map[string]any)
	if got := itemTwo["status"]; got != "running" {
		t.Fatalf("reopened item-2 status = %v, want running", got)
	}
	summary, _ := secondData["summary"].(map[string]any)
	if got := summary["last_checkpoint_id"]; got != "panel-1-checkpoint-001" {
		t.Fatalf("reopened summary.last_checkpoint_id = %v, want panel-1-checkpoint-001", got)
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "cancel",
		"envelopeId": "progress-2",
	})
	secondRes := <-secondDone
	if secondRes.err != nil {
		t.Fatalf("second progress-panel transport err: %v", secondRes.err)
	}
	if secondRes.result.IsError {
		t.Fatalf("second progress-panel IsError=true: %s", extractText(t, secondRes.result))
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
		ProgressPanel *struct {
			Items []struct {
				ItemID string `json:"item_id"`
				Status string `json:"status"`
			} `json:"items"`
			Updates []struct {
				UpdateID string `json:"update_id"`
				Kind     string `json:"kind"`
			} `json:"updates"`
			Checkpoints []struct {
				CheckpointID string `json:"checkpoint_id"`
			} `json:"checkpoints"`
			Summary *struct {
				LastUpdateID        string `json:"last_update_id"`
				LastCheckpointID    string `json:"last_checkpoint_id"`
				LastCheckpointLabel string `json:"last_checkpoint_label"`
			} `json:"summary"`
		} `json:"progress_panel"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if state.ProgressPanel == nil {
		t.Fatal("progress_panel is nil")
	}
	if len(state.ProgressPanel.Updates) != 2 {
		t.Fatalf("updates len = %d, want 2", len(state.ProgressPanel.Updates))
	}
	if len(state.ProgressPanel.Checkpoints) != 1 {
		t.Fatalf("checkpoints len = %d, want 1", len(state.ProgressPanel.Checkpoints))
	}
	if state.ProgressPanel.Summary == nil || state.ProgressPanel.Summary.LastUpdateID != "panel-1-update-001" {
		t.Fatalf("summary = %#v, want last_update_id panel-1-update-001", state.ProgressPanel.Summary)
	}
	if state.ProgressPanel.Summary.LastCheckpointLabel != "Summary started" {
		t.Fatalf("last_checkpoint_label = %q, want Summary started", state.ProgressPanel.Summary.LastCheckpointLabel)
	}
}

func TestProgressPanel_InvalidSubmitReturnsRejectedPayloadAndPreservesState(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "progress-panel")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	done := make(chan advanceResult, 1)
	go func() {
		done <- callProgressPanel(t, rg, roomID, "progress-invalid-1", map[string]any{
			"panel_id": "panel-invalid",
			"items": []any{
				map[string]any{"item_id": "item-1", "label": "Scan repo", "status": "running"},
			},
		})
	}()
	select {
	case early := <-done:
		t.Fatalf("invalid progress-panel returned before ws frame: err=%v isError=%v body=%s", early.err, early.result != nil && early.result.IsError, extractText(t, early.result))
	case <-time.After(150 * time.Millisecond):
	}

	_ = readWSFrame(t, conn, 3*time.Second)
	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "progress-invalid-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "progress-invalid-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"panel_id": "panel-invalid",
				"item_id":  "item-missing",
				"status":   "running",
			},
		},
	})

	res := <-done
	if res.err != nil {
		t.Fatalf("invalid progress-panel transport err: %v", res.err)
	}
	if res.result.IsError {
		t.Fatalf("invalid progress-panel IsError=true: %s", extractText(t, res.result))
	}

	var rejected struct {
		Status  string `json:"status"`
		Payload struct {
			Outcome string `json:"outcome"`
			Errors  []struct {
				Code string `json:"code"`
			} `json:"errors"`
		} `json:"payload"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, res.result)), &rejected); decodeErr != nil {
		t.Fatalf("unmarshal rejected progress-panel response: %v", decodeErr)
	}
	if rejected.Status != "partial" {
		t.Fatalf("rejected status = %q, want partial", rejected.Status)
	}
	if rejected.Payload.Outcome != "rejected" {
		t.Fatalf("rejected outcome = %q, want rejected", rejected.Payload.Outcome)
	}
	if len(rejected.Payload.Errors) != 1 || rejected.Payload.Errors[0].Code != "UNKNOWN_ITEM" {
		t.Fatalf("rejected errors = %#v", rejected.Payload.Errors)
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
		ProgressPanel *struct {
			Items       []any `json:"items"`
			Updates     []any `json:"updates"`
			Checkpoints []any `json:"checkpoints"`
		} `json:"progress_panel"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, getRes)), &state); decodeErr != nil {
		t.Fatalf("unmarshal session_get: %v", decodeErr)
	}
	if state.ProgressPanel == nil {
		t.Fatal("progress_panel is nil")
	}
	if len(state.ProgressPanel.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(state.ProgressPanel.Items))
	}
	if len(state.ProgressPanel.Updates) != 0 {
		t.Fatalf("updates len = %d, want 0", len(state.ProgressPanel.Updates))
	}
	if len(state.ProgressPanel.Checkpoints) != 0 {
		t.Fatalf("checkpoints len = %d, want 0", len(state.ProgressPanel.Checkpoints))
	}
}

func TestProgressPanel_ReopenDifferentPanelIDDoesNotReusePersistedItems(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "progress-panel")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	firstDone := make(chan advanceResult, 1)
	go func() {
		firstDone <- callProgressPanel(t, rg, roomID, "progress-panel-a", map[string]any{
			"panel_id": "panel-a",
			"items": []any{
				map[string]any{"item_id": "item-1", "label": "Scan repo", "status": "running"},
			},
		})
	}()
	_ = readWSFrame(t, conn, 3*time.Second)
	writeWSFrame(t, conn, map[string]any{
		"type":       "cancel",
		"envelopeId": "progress-panel-a",
	})
	if res := <-firstDone; res.err != nil || res.result.IsError {
		t.Fatalf("seed progress-panel result: err=%v body=%s", res.err, extractText(t, res.result))
	}

	secondDone := make(chan advanceResult, 1)
	go func() {
		secondDone <- callProgressPanel(t, rg, roomID, "progress-panel-b", map[string]any{
			"panel_id": "panel-b",
			"items":    []any{},
			"updates":  []any{},
		})
	}()

	secondFrame := readWSFrame(t, conn, 3*time.Second)
	secondEnvelope, _ := secondFrame["envelope"].(map[string]any)
	secondData, _ := secondEnvelope["data"].(map[string]any)
	if got := secondData["panel_id"]; got != "panel-b" {
		t.Fatalf("reopened panel_id = %v, want panel-b", got)
	}
	items, _ := secondData["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("reopened items len = %d, want 0", len(items))
	}
	updates, _ := secondData["updates"].([]any)
	if len(updates) != 0 {
		t.Fatalf("reopened updates len = %d, want 0", len(updates))
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "cancel",
		"envelopeId": "progress-panel-b",
	})
	if res := <-secondDone; res.err != nil || res.result.IsError {
		t.Fatalf("reopen progress-panel result: err=%v body=%s", res.err, extractText(t, res.result))
	}
}

func callProgressPanel(
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
		Name: "tangent.progress-panel",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   envelopeID,
				"type": "tangent.progress-panel",
				"data": data,
				"meta": map[string]any{
					"roomID": roomID,
				},
			},
		},
	})
	return advanceResult{result: res, err: err}
}
