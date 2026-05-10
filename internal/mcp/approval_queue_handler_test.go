package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestApprovalQueue_SubmitReopenAndPersistState(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "approval-queue")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	firstDone := make(chan advanceResult, 1)
	go func() {
		firstDone <- callApprovalQueue(t, rg, roomID, "approval-1", map[string]any{
			"queue_id": "queue-1",
			"items": []any{
				map[string]any{
					"id":          "item-1",
					"title":       "Update dependency",
					"summary":     "Low-risk patch release",
					"description": "Bump package A",
					"action_options": []any{
						map[string]any{"id": "merge", "label": "Merge"},
					},
				},
				map[string]any{
					"id":          "item-2",
					"title":       "Enable feature flag",
					"summary":     "Needs rollout window",
					"description": "Flip to 100%",
				},
			},
		})
	}()

	awaitPendingRoom(t, rg.mgr, roomID, 2*time.Second)
	firstFrame := readWSFrame(t, conn, 3*time.Second)
	firstEnvelope, _ := firstFrame["envelope"].(map[string]any)
	firstData, _ := firstEnvelope["data"].(map[string]any)
	if got := firstData["queue_id"]; got != "queue-1" {
		t.Fatalf("queue_id = %v, want queue-1", got)
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "approval-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "approval-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"queue_id":      "queue-1",
				"current_index": 1,
				"notes":         "first pass",
				"decisions": []any{
					map[string]any{
						"item_id":   "item-1",
						"decision":  "accept",
						"action_id": "merge",
						"comment":   "safe",
					},
					map[string]any{
						"item_id":      "item-2",
						"decision":     "defer",
						"defer_reason": "window",
					},
				},
				"export_refs": []any{
					map[string]any{
						"name":           "queue-1-audit.json",
						"created_at":     "2026-05-09T00:00:00Z",
						"item_count":     2,
						"decision_count": 2,
					},
				},
			},
		},
	})

	firstRes := <-firstDone
	if firstRes.err != nil {
		t.Fatalf("approval-queue transport err: %v", firstRes.err)
	}
	if firstRes.result.IsError {
		t.Fatalf("approval-queue IsError=true: %s", extractText(t, firstRes.result))
	}

	secondDone := make(chan advanceResult, 1)
	go func() {
		secondDone <- callApprovalQueue(t, rg, roomID, "approval-2", map[string]any{
			"queue_id": "queue-1",
			"items": []any{
				map[string]any{"id": "item-1", "title": "stale"},
				map[string]any{"id": "item-2", "title": "stale"},
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
	if got := secondData["current_index"]; got != float64(1) {
		t.Fatalf("reopened current_index = %v, want 1", got)
	}
	secondDecisions, _ := secondData["decisions"].([]any)
	if len(secondDecisions) != 2 {
		t.Fatalf("reopened decisions len = %d, want 2", len(secondDecisions))
	}
	secondItems, _ := secondData["items"].([]any)
	if len(secondItems) != 2 {
		t.Fatalf("reopened items len = %d, want 2", len(secondItems))
	}
	firstItem, _ := secondItems[0].(map[string]any)
	if got := firstItem["title"]; got != "Update dependency" {
		t.Fatalf("reopened first item title = %v, want Update dependency", got)
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "cancel",
		"envelopeId": "approval-2",
	})
	secondRes := <-secondDone
	if secondRes.err != nil {
		t.Fatalf("second approval-queue transport err: %v", secondRes.err)
	}
	if secondRes.result.IsError {
		t.Fatalf("second approval-queue IsError=true: %s", extractText(t, secondRes.result))
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
		ApprovalQueue *struct {
			QueueID      string `json:"queue_id"`
			CurrentIndex int    `json:"current_index"`
			Notes        string `json:"notes"`
			Decisions    []struct {
				ItemID   string `json:"item_id"`
				Decision string `json:"decision"`
			} `json:"decisions"`
			ExportRefs []struct {
				Name string `json:"name"`
			} `json:"export_refs"`
		} `json:"approval_queue"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, getRes)), &state); decodeErr != nil {
		t.Fatalf("unmarshal session_get: %v", decodeErr)
	}
	if state.ApprovalQueue == nil {
		t.Fatal("approval_queue is nil")
	}
	if state.ApprovalQueue.QueueID != "queue-1" {
		t.Fatalf("queue_id = %q, want queue-1", state.ApprovalQueue.QueueID)
	}
	if state.ApprovalQueue.CurrentIndex != 1 {
		t.Fatalf("current_index = %d, want 1", state.ApprovalQueue.CurrentIndex)
	}
	if state.ApprovalQueue.Notes != "first pass" {
		t.Fatalf("notes = %q, want first pass", state.ApprovalQueue.Notes)
	}
	if len(state.ApprovalQueue.Decisions) != 2 {
		t.Fatalf("decisions len = %d, want 2", len(state.ApprovalQueue.Decisions))
	}
	if len(state.ApprovalQueue.ExportRefs) != 1 || state.ApprovalQueue.ExportRefs[0].Name != "queue-1-audit.json" {
		t.Fatalf("export_refs = %#v, want queue-1-audit.json", state.ApprovalQueue.ExportRefs)
	}
}

func callApprovalQueue(
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
		Name: "tangent.approval-queue",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   envelopeID,
				"type": "tangent.approval-queue",
				"data": data,
				"meta": map[string]any{
					"roomID": roomID,
				},
			},
		},
	})
	return advanceResult{result: res, err: err}
}
