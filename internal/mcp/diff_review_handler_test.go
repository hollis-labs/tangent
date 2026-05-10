package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDiffReview_SubmitReopenAndPersistState(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "diff-review")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	firstDone := make(chan advanceResult, 1)
	go func() {
		firstDone <- callDiffReview(t, rg, roomID, "diff-1", map[string]any{
			"review_id": "review-1",
			"files": []any{
				map[string]any{
					"id":   "file-1",
					"path": "pkg/app.go",
					"hunks": []any{
						map[string]any{"id": "hunk-1", "header": "@@ -1,2 +1,2 @@"},
					},
				},
				map[string]any{
					"id":   "file-2",
					"path": "ui/view.tsx",
					"hunks": []any{
						map[string]any{"id": "hunk-2", "header": "@@ -2,3 +2,3 @@"},
					},
				},
			},
			"before_ref": map[string]any{"artifact_id": "artifact-before", "name": "before.patch"},
			"after_ref":  map[string]any{"artifact_id": "artifact-after", "name": "after.patch"},
		})
	}()

	awaitPendingRoom(t, rg.mgr, roomID, 2*time.Second)
	firstFrame := readWSFrame(t, conn, 3*time.Second)
	firstEnvelope, _ := firstFrame["envelope"].(map[string]any)
	firstData, _ := firstEnvelope["data"].(map[string]any)
	if got := firstData["review_id"]; got != "review-1" {
		t.Fatalf("review_id = %v, want review-1", got)
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "diff-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "diff-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"review_id":    "review-1",
				"current_file": "file-2",
				"filter_state": map[string]any{"search": "ui", "decision": "pending"},
				"decisions": []any{
					map[string]any{"file_id": "file-1", "hunk_id": "hunk-1", "decision": "accept"},
					map[string]any{"file_id": "file-2", "hunk_id": "hunk-2", "decision": "reject", "action_id": "follow-up"},
				},
				"comments": map[string]any{
					"file-2::hunk-2": "Needs another pass.",
				},
				"summary": map[string]any{
					"export_name": "review-1-summary.md",
				},
				"export_refs": []any{
					map[string]any{"name": "review-1-summary.md", "kind": "summary"},
				},
			},
		},
	})

	firstRes := <-firstDone
	if firstRes.err != nil {
		t.Fatalf("first diff-review transport err: %v", firstRes.err)
	}
	if firstRes.result.IsError {
		t.Fatalf("first diff-review IsError=true: %s", extractText(t, firstRes.result))
	}

	secondDone := make(chan advanceResult, 1)
	go func() {
		secondDone <- callDiffReview(t, rg, roomID, "diff-2", map[string]any{
			"review_id": "review-1",
			"files": []any{
				map[string]any{"id": "file-1", "path": "stale"},
			},
		})
	}()

	awaitPendingRoom(t, rg.mgr, roomID, 2*time.Second)
	secondFrame := readWSFrame(t, conn, 3*time.Second)
	secondEnvelope, _ := secondFrame["envelope"].(map[string]any)
	secondData, _ := secondEnvelope["data"].(map[string]any)
	if got := secondData["current_file"]; got != "file-2" {
		t.Fatalf("reopened current_file = %v, want file-2", got)
	}
	secondComments, _ := secondData["comments"].(map[string]any)
	if got := secondComments["file-2::hunk-2"]; got != "Needs another pass." {
		t.Fatalf("reopened comment = %v, want Needs another pass.", got)
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "cancel",
		"envelopeId": "diff-2",
	})
	secondRes := <-secondDone
	if secondRes.err != nil {
		t.Fatalf("second diff-review transport err: %v", secondRes.err)
	}
	if secondRes.result.IsError {
		t.Fatalf("second diff-review IsError=true: %s", extractText(t, secondRes.result))
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
		DiffReview *struct {
			ReviewID    string            `json:"review_id"`
			CurrentFile string            `json:"current_file"`
			Comments    map[string]string `json:"comments"`
			Decisions   []struct {
				FileID   string `json:"file_id"`
				HunkID   string `json:"hunk_id"`
				Decision string `json:"decision"`
			} `json:"decisions"`
			Summary *struct {
				ExportName string `json:"export_name"`
			} `json:"summary"`
		} `json:"diff_review"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, getRes)), &state); decodeErr != nil {
		t.Fatalf("unmarshal session_get: %v", decodeErr)
	}
	if state.DiffReview == nil {
		t.Fatal("diff_review is nil")
	}
	if state.DiffReview.ReviewID != "review-1" {
		t.Fatalf("review_id = %q, want review-1", state.DiffReview.ReviewID)
	}
	if state.DiffReview.CurrentFile != "file-2" {
		t.Fatalf("current_file = %q, want file-2", state.DiffReview.CurrentFile)
	}
	if state.DiffReview.Comments["file-2::hunk-2"] != "Needs another pass." {
		t.Fatalf("comments = %#v", state.DiffReview.Comments)
	}
	if len(state.DiffReview.Decisions) != 2 {
		t.Fatalf("decisions len = %d, want 2", len(state.DiffReview.Decisions))
	}
	if state.DiffReview.Summary == nil || state.DiffReview.Summary.ExportName != "review-1-summary.md" {
		t.Fatalf("summary = %#v, want export_name review-1-summary.md", state.DiffReview.Summary)
	}
}

func callDiffReview(
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
		Name: "tangent.diff-review",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   envelopeID,
				"type": "tangent.diff-review",
				"data": data,
				"meta": map[string]any{
					"roomID": roomID,
				},
			},
		},
	})
	return advanceResult{result: res, err: err}
}
