package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDesignIteration_ThreeIterationsSameRoom(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "design-iteration")

	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	for i := 1; i <= 3; i++ {
		variantID := "variant-" + string(rune('0'+i))
		envelopeID := "design-env-" + string(rune('0'+i))
		done := make(chan advanceResult, 1)
		go func() {
			done <- callDesignIteration(t, rg, roomID, envelopeID, variantID)
		}()

		frame := readWSFrame(t, conn, 3*time.Second)
		if frame["envelopeId"] != envelopeID {
			t.Fatalf("envelopeId = %v, want %s", frame["envelopeId"], envelopeID)
		}
		writeWSFrame(t, conn, map[string]any{
			"type":       "response",
			"envelopeId": envelopeID,
			"response": map[string]any{
				"v":          1,
				"envelopeId": envelopeID,
				"kind":       "data",
				"status":     "submitted",
				"payload": map[string]any{
					"variant_id":  variantID,
					"action_id":   "hero",
					"action_kind": "click-region",
					"value":       "primary card",
				},
			},
		})

		res := <-done
		if res.err != nil {
			t.Fatalf("design iteration %d transport err: %v", i, res.err)
		}
		if res.result.IsError {
			t.Fatalf("design iteration %d IsError=true: %s", i, extractText(t, res.result))
		}
		var resp struct {
			Payload struct {
				VariantID  string `json:"variant_id"`
				ActionID   string `json:"action_id"`
				ActionKind string `json:"action_kind"`
			} `json:"payload"`
		}
		if decodeErr := json.Unmarshal([]byte(extractText(t, res.result)), &resp); decodeErr != nil {
			t.Fatalf("unmarshal design iteration response: %v", decodeErr)
		}
		if resp.Payload.VariantID != variantID {
			t.Fatalf("variant_id = %q, want %q", resp.Payload.VariantID, variantID)
		}
		if resp.Payload.ActionID != "hero" || resp.Payload.ActionKind != "click-region" {
			t.Fatalf("unexpected action payload: %+v", resp.Payload)
		}
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
		EnvelopesHistory []struct {
			EnvelopeID string `json:"envelope_id"`
			Type       string `json:"type"`
		} `json:"envelopes_history"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if len(state.EnvelopesHistory) != 3 {
		t.Fatalf("history len = %d, want 3", len(state.EnvelopesHistory))
	}
	for _, item := range state.EnvelopesHistory {
		if item.Type != "tangent.design-iteration" {
			t.Fatalf("history type = %q, want tangent.design-iteration", item.Type)
		}
	}
}

func callDesignIteration(
	t *testing.T,
	rg *sessionRig,
	roomID string,
	envelopeID string,
	variantID string,
) advanceResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.design-iteration",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   envelopeID,
				"type": "tangent.design-iteration",
				"data": map[string]any{
					"variant_id": variantID,
					"html":       `<button id="hero">Hero</button>`,
					"prompts": []any{
						map[string]any{
							"id":       "hero",
							"kind":     "click-region",
							"label":    "Hero",
							"selector": "#hero",
						},
					},
				},
				"meta": map[string]any{
					"roomID": roomID,
				},
			},
		},
	})
	return advanceResult{result: res, err: err}
}
