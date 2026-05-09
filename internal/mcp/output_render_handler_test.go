package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOutputRender_PersistsFinalOutputAndRendersEnvelope(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "output-render")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	done := make(chan advanceResult, 1)
	go func() {
		done <- callOutputRender(t, rg, roomID, "output-1", "# Final draft\n\nTight final paragraph.")
	}()

	frame := readWSFrame(t, conn, 3*time.Second)
	if frame["envelopeId"] != "output-1" {
		t.Fatalf("envelopeId = %v, want output-1", frame["envelopeId"])
	}
	envelope, _ := frame["envelope"].(map[string]any)
	data, _ := envelope["data"].(map[string]any)
	if data["filename"] != "final-draft.md" {
		t.Fatalf("filename = %v, want final-draft.md", data["filename"])
	}
	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "output-1",
		"response": map[string]any{
			"v":           1,
			"envelopeId":  "output-1",
			"kind":        "ack",
			"status":      "submitted",
			"completedAt": time.Now().UTC().Format(time.RFC3339),
		},
	})

	res := <-done
	if res.err != nil {
		t.Fatalf("output render transport err: %v", res.err)
	}
	if res.result.IsError {
		t.Fatalf("output render IsError=true: %s", extractText(t, res.result))
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
		FinalOutput *struct {
			Markdown  string `json:"markdown"`
			Filename  string `json:"filename"`
			Format    string `json:"format"`
			WordCount int    `json:"word_count"`
		} `json:"final_output"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if state.FinalOutput == nil {
		t.Fatal("final_output is nil")
	}
	if state.FinalOutput.Format != "markdown" {
		t.Fatalf("format = %q, want markdown", state.FinalOutput.Format)
	}
	if state.FinalOutput.WordCount == 0 {
		t.Fatal("word_count should be populated")
	}
}

func TestOutputRender_InputValidation(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.output_render",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   "output-invalid-1",
				"type": "tangent.output-render",
				"data": map[string]any{},
			},
		},
	})
	if err != nil {
		t.Fatalf("output_render: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	if got := extractText(t, res); got == "" {
		t.Fatal("expected validation error body")
	}
}

func callOutputRender(
	t *testing.T,
	rg *sessionRig,
	roomID string,
	envelopeID string,
	markdown string,
) advanceResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.output_render",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   envelopeID,
				"type": "tangent.output-render",
				"data": map[string]any{
					"title":    "Final draft",
					"markdown": markdown,
					"filename": "final-draft.md",
					"format":   "markdown",
					"summary":  "Final polished copy.",
				},
				"meta": map[string]any{
					"roomID": roomID,
				},
			},
		},
	})
	return advanceResult{result: res, err: err}
}
