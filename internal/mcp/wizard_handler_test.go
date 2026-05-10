package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type wizardCallToolResult struct {
	result *mcpsdk.CallToolResult
	err    error
}

func TestWizardHandlerPersistsPartialAndReopens(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "wizard")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	done := make(chan wizardCallToolResult, 1)
	go func() {
		res, callErr := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
			Name: "tangent.wizard",
			Arguments: map[string]any{
				"envelope": map[string]any{
					"v":    1,
					"id":   "wizard-1",
					"type": "tangent.wizard",
					"meta": map[string]any{"roomID": roomID},
					"data": map[string]any{
						"wizard_id": "wizard-1",
						"title":     "Release wizard",
						"steps": []map[string]any{
							{
								"step_id": "step-scope",
								"title":   "Scope",
								"kind":    "form",
								"fields": map[string]any{
									"fields": []map[string]any{
										{"field_id": "scope", "label": "Scope", "kind": "textarea"},
									},
								},
								"branches": []map[string]any{
									{"branch_id": "review", "label": "Review", "target_step_id": "step-review"},
								},
							},
							{"step_id": "step-review", "title": "Review", "kind": "review"},
						},
						"current_step_id": "step-scope",
					},
				},
			},
		})
		done <- wizardCallToolResult{result: res, err: callErr}
	}()

	select {
	case early := <-done:
		t.Fatalf("wizard returned before ws frame: err=%v isError=%v body=%s", early.err, early.result != nil && early.result.IsError, extractText(t, early.result))
	case <-time.After(200 * time.Millisecond):
	}

	frame := readWSFrame(t, conn, 3*time.Second)
	if frame["envelopeId"] != "wizard-1" {
		t.Fatalf("envelopeId = %v, want wizard-1", frame["envelopeId"])
	}
	envelope := frame["envelope"].(map[string]any)
	data := envelope["data"].(map[string]any)
	if got := data["wizard_id"]; got != "wizard-1" {
		t.Fatalf("wizard_id = %v, want wizard-1", got)
	}

	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "wizard-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "wizard-1",
			"kind":       "data",
			"status":     "partial",
			"payload": map[string]any{
				"wizard_id":       "wizard-1",
				"title":           "Release wizard",
				"current_step_id": "step-review",
				"steps":           data["steps"],
				"progress": []map[string]any{
					{
						"step_id":     "step-scope",
						"status":      "completed",
						"revision_id": "rev-001",
						"response":    map[string]any{"scope": "wizard envelope"},
						"summary":     "Scope saved",
					},
				},
				"branch_selections": []map[string]any{
					{"step_id": "step-scope", "option_id": "review"},
				},
				"summary": map[string]any{
					"status":               "in_progress",
					"completed_step_count": 1,
					"total_step_count":     2,
					"current_step_id":      "step-review",
				},
			},
		},
	})

	res := <-done
	if res.err != nil {
		t.Fatalf("wizard transport err: %v", res.err)
	}
	if res.result == nil || res.result.IsError {
		t.Fatalf("wizard result IsError=%v body=%s", res.result != nil && res.result.IsError, extractText(t, res.result))
	}

	getRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent.session_get",
		Arguments: map[string]any{"roomID": roomID},
	})
	if err != nil {
		t.Fatalf("session_get: %v", err)
	}
	var state struct {
		Wizard struct {
			CurrentStepID string `json:"current_step_id"`
			Progress      []struct {
				StepID string `json:"step_id"`
				Status string `json:"status"`
			} `json:"progress"`
		} `json:"wizard"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if got := state.Wizard.CurrentStepID; got != "step-review" {
		t.Fatalf("current_step_id = %q, want step-review", got)
	}
	if got := len(state.Wizard.Progress); got != 1 {
		t.Fatalf("progress len = %d, want 1", got)
	}
}
