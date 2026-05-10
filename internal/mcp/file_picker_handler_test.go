package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFilePicker_SubmitReopenAndPersistState(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "file-picker")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	firstDone := make(chan advanceResult, 1)
	go func() {
		firstDone <- callFilePicker(t, rg, roomID, "picker-1", map[string]any{
			"picker_id": "picker-1",
			"browse_roots": []any{
				map[string]any{"root_id": "workspace", "label": "Workspace", "path": "/tmp/workspace"},
			},
			"files": []any{
				map[string]any{
					"artifact_id":   "artifact-1",
					"name":          "spec.md",
					"uri":           "artifact://artifact-1",
					"root_id":       "workspace",
					"relative_path": "docs/spec.md",
				},
				map[string]any{
					"artifact_id":   "artifact-2",
					"name":          "readme.md",
					"uri":           "artifact://artifact-2",
					"root_id":       "workspace",
					"relative_path": "README.md",
				},
			},
		})
	}()

	awaitPendingRoom(t, rg.mgr, roomID, 2*time.Second)
	firstFrame := readWSFrame(t, conn, 3*time.Second)
	firstEnvelope, _ := firstFrame["envelope"].(map[string]any)
	firstData, _ := firstEnvelope["data"].(map[string]any)
	if got := firstData["picker_id"]; got != "picker-1" {
		t.Fatalf("picker_id = %v, want picker-1", got)
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "picker-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "picker-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"picker_id": "picker-1",
				"selected_refs": []any{
					map[string]any{
						"artifact_id":   "artifact-1",
						"name":          "spec.md",
						"uri":           "artifact://artifact-1",
						"root_id":       "workspace",
						"relative_path": "docs/spec.md",
					},
				},
				"query_state": map[string]any{
					"current_root_id": "workspace",
				},
			},
		},
	})

	firstRes := <-firstDone
	if firstRes.err != nil {
		t.Fatalf("first file-picker transport err: %v", firstRes.err)
	}
	if firstRes.result.IsError {
		t.Fatalf("first file-picker IsError=true: %s", extractText(t, firstRes.result))
	}
	var accepted struct {
		Status  string `json:"status"`
		Payload struct {
			Outcome             string `json:"outcome"`
			SelectionRevisionID string `json:"selection_revision_id"`
		} `json:"payload"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, firstRes.result)), &accepted); decodeErr != nil {
		t.Fatalf("unmarshal accepted file-picker response: %v", decodeErr)
	}
	if accepted.Payload.Outcome != "accepted" {
		t.Fatalf("accepted outcome = %q, want accepted", accepted.Payload.Outcome)
	}
	if accepted.Payload.SelectionRevisionID != "picker-1-rev-001" {
		t.Fatalf("selection_revision_id = %q, want picker-1-rev-001", accepted.Payload.SelectionRevisionID)
	}

	secondDone := make(chan advanceResult, 1)
	go func() {
		secondDone <- callFilePicker(t, rg, roomID, "picker-2", map[string]any{
			"picker_id": "picker-1",
			"browse_roots": []any{
				map[string]any{"root_id": "workspace", "label": "Workspace", "path": "/tmp/workspace"},
			},
		})
	}()

	awaitPendingRoom(t, rg.mgr, roomID, 2*time.Second)
	secondFrame := readWSFrame(t, conn, 3*time.Second)
	secondEnvelope, _ := secondFrame["envelope"].(map[string]any)
	secondData, _ := secondEnvelope["data"].(map[string]any)
	selectedRefs, _ := secondData["selected_refs"].([]any)
	if got := len(selectedRefs); got != 1 {
		t.Fatalf("reopened selected_refs len = %d, want 1", got)
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "cancel",
		"envelopeId": "picker-2",
	})
	secondRes := <-secondDone
	if secondRes.err != nil {
		t.Fatalf("second file-picker transport err: %v", secondRes.err)
	}
	if secondRes.result.IsError {
		t.Fatalf("second file-picker IsError=true: %s", extractText(t, secondRes.result))
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
		FilePicker *struct {
			PickerID     string `json:"picker_id"`
			SelectedRefs []struct {
				ArtifactID string `json:"artifact_id"`
			} `json:"selected_refs"`
			SelectionRevisions []struct {
				SelectionRevisionID string `json:"selection_revision_id"`
				SelectedCount       int    `json:"selected_count"`
			} `json:"selection_revisions"`
		} `json:"file_picker"`
	}
	if decodeErr := json.Unmarshal([]byte(extractText(t, getRes)), &state); decodeErr != nil {
		t.Fatalf("unmarshal session_get: %v", decodeErr)
	}
	if state.FilePicker == nil {
		t.Fatal("file_picker is nil")
	}
	if state.FilePicker.PickerID != "picker-1" {
		t.Fatalf("picker_id = %q, want picker-1", state.FilePicker.PickerID)
	}
	if len(state.FilePicker.SelectedRefs) != 1 || state.FilePicker.SelectedRefs[0].ArtifactID != "artifact-1" {
		t.Fatalf("selected_refs = %#v", state.FilePicker.SelectedRefs)
	}
	if len(state.FilePicker.SelectionRevisions) != 1 || state.FilePicker.SelectionRevisions[0].SelectedCount != 1 {
		t.Fatalf("selection_revisions = %#v", state.FilePicker.SelectionRevisions)
	}
	if revisionID := state.FilePicker.SelectionRevisions[0].SelectionRevisionID; revisionID != "picker-1-rev-001" {
		t.Fatalf("selection_revision_id = %q, want picker-1-rev-001", revisionID)
	}
}

func TestFilePicker_InvalidSubmitReturnsRejectedPayloadAndPreservesAcceptedState(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "file-picker")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	done := make(chan advanceResult, 1)
	go func() {
		done <- callFilePicker(t, rg, roomID, "picker-invalid-1", map[string]any{
			"picker_id": "picker-invalid",
			"browse_roots": []any{
				map[string]any{"root_id": "workspace", "label": "Workspace", "path": "/tmp/workspace"},
			},
		})
	}()

	awaitPendingRoom(t, rg.mgr, roomID, 2*time.Second)
	_ = readWSFrame(t, conn, 3*time.Second)
	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "picker-invalid-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "picker-invalid-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"picker_id":     "picker-invalid",
				"selected_refs": []any{},
			},
		},
	})

	res := <-done
	if res.err != nil {
		t.Fatalf("invalid file-picker transport err: %v", res.err)
	}
	if res.result.IsError {
		t.Fatalf("invalid file-picker IsError=true: %s", extractText(t, res.result))
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
		t.Fatalf("unmarshal rejected file-picker response: %v", decodeErr)
	}
	if rejected.Status != "partial" {
		t.Fatalf("rejected status = %q, want partial", rejected.Status)
	}
	if rejected.Payload.Outcome != "rejected" {
		t.Fatalf("rejected outcome = %q, want rejected", rejected.Payload.Outcome)
	}
	if len(rejected.Payload.Errors) != 1 || rejected.Payload.Errors[0].Code != "EMPTY_SELECTION" {
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
		FilePicker *struct {
			SelectedRefs       []any `json:"selected_refs"`
			SelectionRevisions []any `json:"selection_revisions"`
		} `json:"file_picker"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if state.FilePicker == nil {
		t.Fatal("file_picker is nil")
	}
	if len(state.FilePicker.SelectedRefs) != 0 {
		t.Fatalf("selected_refs len = %d, want 0", len(state.FilePicker.SelectedRefs))
	}
	if len(state.FilePicker.SelectionRevisions) != 0 {
		t.Fatalf("selection_revisions len = %d, want 0", len(state.FilePicker.SelectionRevisions))
	}
}

func callFilePicker(
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
		Name: "tangent.file-picker",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   envelopeID,
				"type": "tangent.file-picker",
				"data": data,
				"meta": map[string]any{
					"roomID": roomID,
				},
			},
		},
	})
	return advanceResult{result: res, err: err}
}
