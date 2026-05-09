package mcp_test

import (
	"context"
	"encoding/json"
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
			BoardID string `json:"board_id"`
			Notes   string `json:"notes"`
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
	if state.Whiteboard.Notes != "seed notes" {
		t.Fatalf("notes = %q, want seed notes", state.Whiteboard.Notes)
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
