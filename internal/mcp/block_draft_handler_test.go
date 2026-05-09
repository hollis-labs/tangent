package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBlockDraft_AcceptedBlocksPersistAndCurrentDraftReconstructs(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "block-draft")

	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	runDraft := func(envelopeID string, blockID string, content string, decision string, feedback string, editedText string) {
		done := make(chan advanceResult, 1)
		go func() {
			done <- callBlockDraft(t, rg, roomID, envelopeID, blockID, content)
		}()

		frame := readWSFrame(t, conn, 3*time.Second)
		if frame["envelopeId"] != envelopeID {
			t.Fatalf("envelopeId = %v, want %s", frame["envelopeId"], envelopeID)
		}
		payload := map[string]any{
			"decision": decision,
			"block_id": blockID,
			"mode":     "section",
		}
		if feedback != "" {
			payload["feedback"] = feedback
		}
		if editedText != "" {
			payload["edited_text"] = editedText
		}
		writeWSFrame(t, conn, map[string]any{
			"type":       "response",
			"envelopeId": envelopeID,
			"response": map[string]any{
				"v":           1,
				"envelopeId":  envelopeID,
				"kind":        "data",
				"status":      "submitted",
				"payload":     payload,
				"completedAt": time.Now().UTC().Format(time.RFC3339),
			},
		})

		res := <-done
		if res.err != nil {
			t.Fatalf("block draft transport err: %v", res.err)
		}
		if res.result.IsError {
			t.Fatalf("block draft IsError=true: %s", extractText(t, res.result))
		}
	}

	runDraft("draft-1", "intro", "Original intro paragraph.", "accept", "", "")
	runDraft("draft-2", "body", "Original body paragraph.", "revise", "Push harder on constraints.", "")
	runDraft("draft-3", "intro", "Original intro paragraph.", "inline_edit", "Tightened the opening.", "Revised intro paragraph.")
	runDraft("draft-4", "ending", "Closing section.", "accept", "", "")

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
		AcceptedDraftBlocks []struct {
			BlockID  string `json:"block_id"`
			Content  string `json:"content"`
			Decision string `json:"decision"`
		} `json:"accepted_draft_blocks"`
		CurrentDraft struct {
			BlockCount int `json:"block_count"`
			Blocks     []struct {
				BlockID string `json:"block_id"`
				Content string `json:"content"`
			} `json:"blocks"`
			Markdown string `json:"markdown"`
		} `json:"current_draft"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if len(state.AcceptedDraftBlocks) != 3 {
		t.Fatalf("accepted_draft_blocks len = %d, want 3", len(state.AcceptedDraftBlocks))
	}
	if state.CurrentDraft.BlockCount != 2 {
		t.Fatalf("current draft block_count = %d, want 2", state.CurrentDraft.BlockCount)
	}
	if got := state.CurrentDraft.Blocks[0].Content; got != "Revised intro paragraph." {
		t.Fatalf("current intro content = %q, want revised intro", got)
	}
	if got := state.CurrentDraft.Blocks[1].BlockID; got != "ending" {
		t.Fatalf("current draft second block_id = %q, want ending", got)
	}
}

func TestBlockDraft_InputValidation(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.block_draft",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   "draft-invalid-1",
				"type": "tangent.block-draft",
				"data": map[string]any{
					"content": "Missing block_id should fail.",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("block_draft: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	if got := extractText(t, res); got == "" {
		t.Fatal("expected validation error body")
	}
}

func callBlockDraft(
	t *testing.T,
	rg *sessionRig,
	roomID string,
	envelopeID string,
	blockID string,
	content string,
) advanceResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.block_draft",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   envelopeID,
				"type": "tangent.block-draft",
				"data": map[string]any{
					"block_id":     blockID,
					"mode":         "section",
					"label":        blockID,
					"content":      content,
					"rationale":    "Lead with the clearest point first.",
					"outline_hint": "Outline item",
				},
				"meta": map[string]any{
					"roomID": roomID,
				},
			},
		},
	})
	return advanceResult{result: res, err: err}
}
