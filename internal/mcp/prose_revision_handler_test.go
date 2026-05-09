package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProseRevision_PersistsExplicitSuggestionOutcomes(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "prose-revision")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	done := make(chan advanceResult, 1)
	go func() {
		done <- callProseRevision(t, rg, roomID, "rev-1", "review")
	}()

	frame := readWSFrame(t, conn, 3*time.Second)
	if frame["envelopeId"] != "rev-1" {
		t.Fatalf("envelopeId = %v, want rev-1", frame["envelopeId"])
	}
	writeWSFrame(t, conn, map[string]any{
		"type":       "response",
		"envelopeId": "rev-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "rev-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"lens":        "review",
				"revision_id": "opening-pass",
				"block_id":    "intro",
				"outcomes": []any{
					map[string]any{"suggestion_id": "s1", "decision": "accept"},
					map[string]any{"suggestion_id": "s2", "decision": "comment", "comment": "Keep the example, just shorten it."},
				},
				"general_comment": "Keep the structure fix, skip the extra flourish.",
			},
			"completedAt": time.Now().UTC().Format(time.RFC3339),
		},
	})

	res := <-done
	if res.err != nil {
		t.Fatalf("prose revision transport err: %v", res.err)
	}
	if res.result.IsError {
		t.Fatalf("prose revision IsError=true: %s", extractText(t, res.result))
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
		ProseRevisionOutcomes []struct {
			RevisionID string `json:"revision_id"`
			Lens       string `json:"lens"`
			Outcomes   []struct {
				SuggestionID string `json:"suggestion_id"`
				Decision     string `json:"decision"`
				Comment      string `json:"comment"`
			} `json:"outcomes"`
		} `json:"prose_revision_outcomes"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if len(state.ProseRevisionOutcomes) != 1 {
		t.Fatalf("prose_revision_outcomes len = %d, want 1", len(state.ProseRevisionOutcomes))
	}
	if got := state.ProseRevisionOutcomes[0].Lens; got != "review" {
		t.Fatalf("lens = %q, want review", got)
	}
	if got := state.ProseRevisionOutcomes[0].Outcomes[1].Decision; got != "comment" {
		t.Fatalf("decision = %q, want comment", got)
	}
}

func TestProseRevision_InputValidation(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.prose_revision",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   "rev-invalid-1",
				"type": "tangent.prose-revision",
				"data": map[string]any{
					"lens":        "review",
					"source_text": "Missing suggestions should fail.",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("prose_revision: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	if got := extractText(t, res); got == "" {
		t.Fatal("expected validation error body")
	}
}

func callProseRevision(
	t *testing.T,
	rg *sessionRig,
	roomID string,
	envelopeID string,
	lens string,
) advanceResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.prose_revision",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   envelopeID,
				"type": "tangent.prose-revision",
				"data": map[string]any{
					"lens":        lens,
					"revision_id": "opening-pass",
					"block_id":    "intro",
					"source_text": "Original opening paragraph.",
					"suggestions": []any{
						map[string]any{"id": "s1", "label": "Lead with the claim", "suggested_text": "Lead with the main claim."},
						map[string]any{"id": "s2", "label": "Trim the example", "suggested_text": "Keep one example instead of two."},
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
