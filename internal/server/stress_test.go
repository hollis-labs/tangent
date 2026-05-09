package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

func TestStress_ConcurrentRoomsAcrossWorkflows(t *testing.T) {
	rg := newRig(t)
	defer rg.cleanup()

	type scenario struct {
		tool       string
		envelopeID string
		envelope   map[string]any
	}

	cases := []scenario{
		{
			tool:       "tangent.triage",
			envelopeID: "stress-triage",
			envelope: map[string]any{
				"v":    1,
				"id":   "stress-triage",
				"type": "tangent.triage",
				"data": map[string]any{"items": []any{"a"}},
			},
		},
		{
			tool:       "tangent.feedback",
			envelopeID: "stress-feedback",
			envelope: map[string]any{
				"v":    1,
				"id":   "stress-feedback",
				"type": "tangent.feedback",
				"data": map[string]any{
					"questions": []any{
						map[string]any{"id": "q1", "type": "text", "label": "Question"},
					},
				},
			},
		},
		{
			tool:       "tangent.design-iteration",
			envelopeID: "stress-design-a",
			envelope: map[string]any{
				"v":    1,
				"id":   "stress-design-a",
				"type": "tangent.design-iteration",
				"data": map[string]any{
					"variant_id": "variant-a",
					"html":       `<button id="hero-a">A</button>`,
					"prompts": []any{
						map[string]any{"id": "hero-a", "kind": "click-region", "label": "Hero A", "selector": "#hero-a"},
					},
				},
			},
		},
		{
			tool:       "tangent.design-iteration",
			envelopeID: "stress-design-b",
			envelope: map[string]any{
				"v":    1,
				"id":   "stress-design-b",
				"type": "tangent.design-iteration",
				"data": map[string]any{
					"variant_id": "variant-b",
					"html":       `<button id="hero-b">B</button>`,
					"prompts": []any{
						map[string]any{"id": "hero-b", "kind": "click-region", "label": "Hero B", "selector": "#hero-b"},
					},
				},
			},
		},
	}

	type result struct {
		body string
		err  error
	}
	results := make(chan result, len(cases))
	var wg sync.WaitGroup

	for _, tc := range cases {
		tc := tc
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			done := make(chan result, 1)
			go func() {
				res, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
					Name:      tc.tool,
					Arguments: map[string]any{"envelope": tc.envelope},
				})
				if err != nil {
					done <- result{err: err}
					return
				}
				done <- result{body: textOf(res)}
			}()

			rm := awaitRoomByEnvelopeID(t, rg.mgr, tc.envelopeID, 3*time.Second)
			conn, _, err := websocket.Dial(context.Background(), rg.wsURL(rm.ID), nil)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "done")

			frame := readFrame(t, conn, 3*time.Second)
			if frame["envelopeId"] != tc.envelopeID {
				results <- result{err: fmt.Errorf("room %s got envelope %v, want %s", rm.ID, frame["envelopeId"], tc.envelopeID)}
				return
			}
			writeFrame(t, conn, responseFrame(tc))
			results <- <-done
		}()
	}

	wg.Wait()
	close(results)

	seen := map[string]bool{}
	for res := range results {
		if res.err != nil {
			t.Fatalf("stress call error: %v", res.err)
		}
		var parsed struct {
			EnvelopeID string `json:"envelopeId"`
		}
		if err := json.Unmarshal([]byte(res.body), &parsed); err != nil {
			t.Fatalf("unmarshal stress response: %v", err)
		}
		if seen[parsed.EnvelopeID] {
			t.Fatalf("duplicate response for %q", parsed.EnvelopeID)
		}
		seen[parsed.EnvelopeID] = true
	}
	if len(seen) != len(cases) {
		t.Fatalf("seen %d responses, want %d", len(seen), len(cases))
	}
}

func awaitRoomByEnvelopeID(t *testing.T, mgr *room.Manager, envelopeID string, timeout time.Duration) *room.Room {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, id := range mgr.IDs() {
			rm, ok := mgr.Get(id)
			if ok && rm.MetaCopy()["envelopeID"] == envelopeID {
				return rm
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("room for envelope %q did not appear within %v", envelopeID, timeout)
	return nil
}

func responseFrame(tc struct {
	tool       string
	envelopeID string
	envelope   map[string]any
}) map[string]any {
	payload := map[string]any{"workflow": tc.envelopeID}
	switch tc.tool {
	case "tangent.feedback":
		payload = map[string]any{
			"answers": []any{
				map[string]any{"questionId": "q1", "value": tc.envelopeID},
			},
		}
	case "tangent.design-iteration":
		data := tc.envelope["data"].(map[string]any)
		prompts := data["prompts"].([]any)
		firstPrompt := prompts[0].(map[string]any)
		payload = map[string]any{
			"variant_id":  data["variant_id"],
			"action_id":   firstPrompt["id"],
			"action_kind": "click-region",
			"value":       firstPrompt["label"],
		}
	}
	return map[string]any{
		"type":       "response",
		"envelopeId": tc.envelopeID,
		"response": map[string]any{
			"v":          1,
			"envelopeId": tc.envelopeID,
			"kind":       "data",
			"status":     "submitted",
			"payload":    payload,
		},
	}
}
