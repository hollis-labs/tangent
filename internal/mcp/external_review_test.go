package mcp_test

import (
	"context"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestExternalReviewCreatesADurableAsyncRequest(t *testing.T) {
	rg := newDurableRig(t)
	defer rg.cleanup()
	roomID, _ := createSession(t, rg, "External review")
	data := map[string]any{
		"review_id": "review-1", "title": "Review a resource", "content_markdown": "Original body", "summary": "Agent notes",
		"resource":   map[string]any{"url": "https://example.com/review/1", "revision": "original-commit"},
		"action_url": "/api/plugins/example-review/action", "state_url": "/api/plugins/example-review/state",
		"actions": []any{map[string]any{"id": "approve", "label": "Approve"}},
	}
	result, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.session_advance", Arguments: map[string]any{
			"roomID": roomID, "completion": map[string]any{"mode": "async"},
			"envelope": map[string]any{"v": 1, "id": "external-review-1", "type": "tangent.external-review", "data": data},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal(extractText(t, result))
	}
	receipt := decodeReceipt(t, result)
	if receipt.Handle.InteractionID == "" || receipt.Handle.RoomID != roomID || receipt.Handle.EnvelopeID != "external-review-1" {
		t.Fatalf("missing retained request: %+v", receipt)
	}
	cancelInteractionForTest(t, rg, roomID, receipt.Handle.InteractionID)
	// External URLs are resource links, never routes the renderer may execute.
	data["action_url"] = "https://example.com/action"
	refused, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.session_advance", Arguments: map[string]any{
			"roomID": roomID, "completion": map[string]any{"mode": "async"},
			"envelope": map[string]any{"v": 1, "id": "external-review-invalid", "type": "tangent.external-review", "data": data},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !refused.IsError {
		t.Fatal("accepted an external action route")
	}
}
