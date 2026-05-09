package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWhiteboard_PersistsSeedStateAndRendersEnvelope(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "whiteboard")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	done := make(chan advanceResult, 1)
	go func() {
		done <- callWhiteboard(t, rg, roomID, "whiteboard-1", map[string]any{
			"document": map[string]any{
				"pages": []any{
					map[string]any{"id": "page:1"},
				},
			},
			"session": map[string]any{"currentPageId": "page:1"},
		})
	}()

	select {
	case early := <-done:
		if early.err != nil {
			t.Fatalf("whiteboard returned early transport err: %v", early.err)
		}
		if early.result != nil && early.result.IsError {
			t.Fatalf("whiteboard returned early IsError=true: %s", extractText(t, early.result))
		}
		t.Fatal("whiteboard returned before envelope dispatch")
	case <-time.After(100 * time.Millisecond):
	}

	awaitPendingRoom(t, rg.mgr, roomID, 2*time.Second)
	frame := readWSFrame(t, conn, 3*time.Second)
	if frame["envelopeId"] != "whiteboard-1" {
		t.Fatalf("envelopeId = %v, want whiteboard-1", frame["envelopeId"])
	}
	envelope, _ := frame["envelope"].(map[string]any)
	data, _ := envelope["data"].(map[string]any)
	if data["board_id"] != "board-1" {
		t.Fatalf("board_id = %v, want board-1", data["board_id"])
	}
	scene, _ := data["scene"].(map[string]any)
	if _, ok := scene["document"]; !ok {
		t.Fatalf("scene document missing: %+v", scene)
	}
	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "whiteboard-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "whiteboard-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"board_id": "board-1",
				"scene": map[string]any{
					"document": map[string]any{
						"pages": []any{
							map[string]any{"id": "page:1"},
							map[string]any{"id": "shape:1"},
						},
					},
				},
				"assets": []any{
					map[string]any{"artifact_id": "artifact-1", "source": "artifact://artifact-1"},
				},
				"notes": "submitted board",
				"selection_summary": map[string]any{
					"count": 1,
					"ids":   []any{"shape:1"},
					"types": []any{"geo"},
				},
			},
			"completedAt": time.Now().UTC().Format(time.RFC3339),
		},
	})

	res := <-done
	if res.err != nil {
		t.Fatalf("whiteboard transport err: %v", res.err)
	}
	if res.result.IsError {
		t.Fatalf("whiteboard IsError=true: %s", extractText(t, res.result))
	}
	toolResp := decodeWhiteboardToolResult(t, res.result)
	if got := toolResp.Payload.BoardID; got != "board-1" {
		t.Fatalf("result payload board_id = %q, want board-1", got)
	}
	if toolResp.Payload.RevisionID == "" {
		t.Fatal("result payload revision_id is empty")
	}
	if got := toolResp.Payload.Notes; got != "submitted board" {
		t.Fatalf("result payload notes = %q, want submitted board", got)
	}
	if toolResp.Payload.SelectionSummary == nil || toolResp.Payload.SelectionSummary.Count != 1 {
		t.Fatalf("selection_summary = %+v, want count=1", toolResp.Payload.SelectionSummary)
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
		Whiteboard *struct {
			BoardID         string `json:"board_id"`
			Notes           string `json:"notes"`
			RevisionHistory []struct {
				RevisionID string `json:"revision_id"`
			} `json:"revision_history"`
		} `json:"whiteboard"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if state.Whiteboard == nil {
		t.Fatal("whiteboard is nil")
	}
	if state.Whiteboard.BoardID != "board-1" {
		t.Fatalf("board_id = %q, want board-1", state.Whiteboard.BoardID)
	}
	if state.Whiteboard.Notes != "submitted board" {
		t.Fatalf("notes = %q, want submitted board", state.Whiteboard.Notes)
	}
	if got := len(state.Whiteboard.RevisionHistory); got != 1 {
		t.Fatalf("revision_history len = %d, want 1", got)
	}
	if got := state.Whiteboard.RevisionHistory[0].RevisionID; got != toolResp.Payload.RevisionID {
		t.Fatalf("revision_history[0].revision_id = %q, want %q", got, toolResp.Payload.RevisionID)
	}
}

func TestWhiteboard_SubmitReopenAndHistoryStayRevisionAware(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "whiteboard")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	firstDone := make(chan advanceResult, 1)
	go func() {
		firstDone <- callWhiteboard(t, rg, roomID, "whiteboard-1", map[string]any{
			"document": map[string]any{
				"pages": []any{
					map[string]any{"id": "page:1"},
				},
			},
		})
	}()

	awaitPendingRoom(t, rg.mgr, roomID, 2*time.Second)
	_ = readWSFrame(t, conn, 3*time.Second)
	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "whiteboard-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "whiteboard-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"board_id": "board-1",
				"scene": map[string]any{
					"document": map[string]any{
						"pages": []any{
							map[string]any{"id": "page:1"},
							map[string]any{"id": "shape:1", "type": "geo"},
						},
					},
				},
				"notes": "revision one",
			},
		},
	})
	firstRes := <-firstDone
	if firstRes.err != nil {
		t.Fatalf("first whiteboard transport err: %v", firstRes.err)
	}
	if firstRes.result.IsError {
		t.Fatalf("first whiteboard IsError=true: %s", extractText(t, firstRes.result))
	}
	firstToolResp := decodeWhiteboardToolResult(t, firstRes.result)
	if firstToolResp.Payload.RevisionID == "" {
		t.Fatal("first revision_id is empty")
	}

	secondDone := make(chan advanceResult, 1)
	go func() {
		secondDone <- callWhiteboard(t, rg, roomID, "whiteboard-2", map[string]any{
			"document": map[string]any{
				"pages": []any{
					map[string]any{"id": "page:stale"},
				},
			},
		})
	}()

	awaitPendingRoom(t, rg.mgr, roomID, 2*time.Second)
	secondFrame := readWSFrame(t, conn, 3*time.Second)
	secondEnvelope, _ := secondFrame["envelope"].(map[string]any)
	secondData, _ := secondEnvelope["data"].(map[string]any)
	secondScene, _ := secondData["scene"].(map[string]any)
	secondDocument, _ := secondScene["document"].(map[string]any)
	secondPages, _ := secondDocument["pages"].([]any)
	if len(secondPages) != 2 {
		t.Fatalf("reopened scene pages len = %d, want 2", len(secondPages))
	}
	if got := secondData["notes"]; got != "revision one" {
		t.Fatalf("reopened notes = %v, want revision one", got)
	}
	if got := secondData["revision_id"]; got != firstToolResp.Payload.RevisionID {
		t.Fatalf("reopened revision_id = %v, want %q", got, firstToolResp.Payload.RevisionID)
	}
	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "whiteboard-2",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "whiteboard-2",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"board_id": "board-1",
				"scene": map[string]any{
					"document": map[string]any{
						"pages": []any{
							map[string]any{"id": "page:1"},
							map[string]any{"id": "shape:1", "type": "geo"},
							map[string]any{"id": "shape:2", "type": "arrow"},
						},
					},
				},
				"notes": "revision two",
				"export_refs": []any{
					map[string]any{
						"artifact_id": "artifact-export-1",
						"kind":        "png",
						"mime_type":   "image/png",
						"uri":         "artifact://artifact-export-1",
					},
				},
			},
		},
	})

	secondRes := <-secondDone
	if secondRes.err != nil {
		t.Fatalf("second whiteboard transport err: %v", secondRes.err)
	}
	if secondRes.result.IsError {
		t.Fatalf("second whiteboard IsError=true: %s", extractText(t, secondRes.result))
	}
	secondToolResp := decodeWhiteboardToolResult(t, secondRes.result)
	if secondToolResp.Payload.RevisionID == "" || secondToolResp.Payload.RevisionID == firstToolResp.Payload.RevisionID {
		t.Fatalf("second revision_id = %q, want a new revision id", secondToolResp.Payload.RevisionID)
	}
	if len(secondToolResp.Payload.ExportRefs) != 1 {
		t.Fatalf("export_refs len = %d, want 1", len(secondToolResp.Payload.ExportRefs))
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
		Whiteboard *struct {
			Notes           string `json:"notes"`
			RevisionHistory []struct {
				RevisionID string `json:"revision_id"`
			} `json:"revision_history"`
		} `json:"whiteboard"`
		EnvelopesHistory []struct {
			Response *struct {
				Payload *struct {
					RevisionID string `json:"revision_id"`
				} `json:"payload"`
			} `json:"response"`
		} `json:"envelopes_history"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get reopen: %v", err)
	}
	if state.Whiteboard == nil {
		t.Fatal("whiteboard state missing")
	}
	if got := state.Whiteboard.Notes; got != "revision two" {
		t.Fatalf("latest notes = %q, want revision two", got)
	}
	if got := len(state.Whiteboard.RevisionHistory); got != 2 {
		t.Fatalf("revision_history len = %d, want 2", got)
	}
	if got := state.Whiteboard.RevisionHistory[0].RevisionID; got != firstToolResp.Payload.RevisionID {
		t.Fatalf("revision_history[0].revision_id = %q, want %q", got, firstToolResp.Payload.RevisionID)
	}
	if got := state.Whiteboard.RevisionHistory[1].RevisionID; got != secondToolResp.Payload.RevisionID {
		t.Fatalf("revision_history[1].revision_id = %q, want %q", got, secondToolResp.Payload.RevisionID)
	}
	if got := len(state.EnvelopesHistory); got != 2 {
		t.Fatalf("envelopes_history len = %d, want 2", got)
	}
	if got := state.EnvelopesHistory[1].Response.Payload.RevisionID; got != secondToolResp.Payload.RevisionID {
		t.Fatalf("history response revision_id = %q, want %q", got, secondToolResp.Payload.RevisionID)
	}
}

func TestWhiteboard_InputValidation(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.whiteboard",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   "whiteboard-invalid-1",
				"type": "tangent.whiteboard",
				"data": map[string]any{},
			},
		},
	})
	if err != nil {
		t.Fatalf("whiteboard: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	if got := extractText(t, res); got == "" {
		t.Fatal("expected validation error body")
	}
}

func TestWhiteboard_SubmitContractRejectsMissingScene(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "whiteboard")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	done := make(chan advanceResult, 1)
	go func() {
		done <- callWhiteboard(t, rg, roomID, "whiteboard-invalid-submit-1", map[string]any{
			"document": map[string]any{
				"pages": []any{
					map[string]any{"id": "page:1"},
				},
			},
		})
	}()

	awaitPendingRoom(t, rg.mgr, roomID, 2*time.Second)
	_ = readWSFrame(t, conn, 3*time.Second)
	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "whiteboard-invalid-submit-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "whiteboard-invalid-submit-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"board_id": "board-1",
			},
		},
	})

	res := <-done
	if res.err != nil {
		t.Fatalf("whiteboard invalid submit transport err: %v", res.err)
	}
	if !res.result.IsError {
		t.Fatal("expected IsError=true for invalid submit contract")
	}
	if got := extractText(t, res.result); got == "" || !strings.Contains(got, "validation-failed") {
		t.Fatalf("expected validation-failed error body, got %s", got)
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
		Whiteboard *struct {
			Notes           string `json:"notes"`
			RevisionHistory []struct {
				RevisionID string `json:"revision_id"`
			} `json:"revision_history"`
		} `json:"whiteboard"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get invalid submit: %v", err)
	}
	if state.Whiteboard == nil {
		t.Fatal("whiteboard state missing")
	}
	if got := state.Whiteboard.Notes; got != "seed notes" {
		t.Fatalf("notes after invalid submit = %q, want seed notes", got)
	}
	if got := len(state.Whiteboard.RevisionHistory); got != 0 {
		t.Fatalf("revision_history len after invalid submit = %d, want 0", got)
	}
}

func callWhiteboard(
	t *testing.T,
	rg *sessionRig,
	roomID string,
	envelopeID string,
	scene map[string]any,
) advanceResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.whiteboard",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   envelopeID,
				"type": "tangent.whiteboard",
				"data": map[string]any{
					"board_id": "board-1",
					"title":    "Whiteboard",
					"intent":   "Map the layout",
					"scene":    scene,
					"assets": []any{
						map[string]any{"artifact_id": "artifact-1", "source": "artifact://artifact-1"},
					},
					"notes":     "seed notes",
					"tool_mode": "draw",
				},
				"meta": map[string]any{
					"roomID": roomID,
				},
			},
		},
	})
	return advanceResult{result: res, err: err}
}

type whiteboardToolResult struct {
	Payload struct {
		BoardID          string `json:"board_id"`
		RevisionID       string `json:"revision_id"`
		Notes            string `json:"notes"`
		SelectionSummary *struct {
			Count int `json:"count"`
		} `json:"selection_summary"`
		ExportRefs []struct {
			ArtifactID string `json:"artifact_id"`
		} `json:"export_refs"`
	} `json:"payload"`
}

func decodeWhiteboardToolResult(t *testing.T, res *mcpsdk.CallToolResult) whiteboardToolResult {
	t.Helper()
	var payload whiteboardToolResult
	if err := json.Unmarshal([]byte(extractText(t, res)), &payload); err != nil {
		t.Fatalf("unmarshal whiteboard tool result: %v", err)
	}
	return payload
}
