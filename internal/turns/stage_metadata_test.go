package turns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/interaction"
)

func stageRequest(t *testing.T, publication bool) map[string]any {
	t.Helper()
	var req map[string]any
	if err := json.Unmarshal(turnRequestJSON("actual-session", "actual-turn", "question", "Source question", "Original\n  **body** 😺\n"), &req); err != nil {
		t.Fatal(err)
	}
	req["contract_version"] = "1.1"
	req["annotations"] = []any{map[string]any{
		"schema_version": 1, "stage_id": "summarize", "stage_version": "1",
		"kind": "summary", "summary": map[string]any{"text": req["summary"]},
	}}
	req["stage_trace"] = []any{map[string]any{
		"stage_id": "summarize", "stage_version": "1", "outcome": "passed", "duration_ms": 12,
	}}
	source := map[string]any{
		"schema_version": 1, "origin": "routed", "endpoint_ref": "test-endpoint",
		"channel": "test-channel", "message_id": "publication-id", "sequence": 42,
		"sender_urn":  "msg://session/local/actual-session",
		"attribution": map[string]any{"kind": "question", "confidence": "exact"},
	}
	if publication {
		source["origin"], source["sender_urn"] = "publication", "msg://agent/local/test-sender"
		delete(source, "attribution")
		delete(req, "session_id")
		delete(req, "turn_id")
		delete(req, "options")
		req["kind"] = "checkpoint"
	}
	req["source"].(map[string]any)["agent_id"] = source["sender_urn"]
	req["source_message"] = source
	return req
}

func encodeStageRequest(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestStageMetadataDurableOriginalAndPublicationRefusal(t *testing.T) {
	for _, publication := range []bool{false, true} {
		t.Run(map[bool]string{false: "routed", true: "publication"}[publication], func(t *testing.T) {
			svc, interactions, _ := newTestService(t)
			ctx := context.Background()
			req := stageRequest(t, publication)
			raw := encodeStageRequest(t, req)
			handle, err := svc.Enqueue(ctx, EnqueueInput{Request: raw, Caller: testCaller("test")})
			if err != nil {
				t.Fatal(err)
			}
			// A fresh service reads the committed original and typed metadata.
			reopened, err := NewService(interactions)
			if err != nil {
				t.Fatal(err)
			}
			view, err := reopened.InspectTurn(ctx, handle.ItemID)
			if err != nil {
				t.Fatal(err)
			}
			if view.Content != req["content"] || view.Summary != req["summary"] || view.ContractVersion != "1.1" || len(view.Annotations) != 1 || len(view.StageTrace) != 1 || view.SourceMessage.MessageID != "publication-id" || view.Replyable == publication {
				t.Fatalf("committed view changed the original or provenance: %#v", view)
			}
			if publication {
				if view.SessionID != "" || view.TurnID != "" {
					t.Fatal("invented runtime identity")
				}
				_, err = reopened.Reply(ctx, ReplyInput{ItemID: view.ItemID, ExpectedRevision: view.Revision, ResponseText: "must not route"})
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("publication reply: %v", err)
				}
				after, inspectErr := reopened.InspectTurn(ctx, view.ItemID)
				if inspectErr != nil || !reflect.DeepEqual(after, view) {
					t.Fatalf("refusal changed durable item: %v", inspectErr)
				}
				if _, err = reopened.Dismiss(ctx, DismissInput{ItemID: view.ItemID, ExpectedRevision: view.Revision}); err != nil {
					t.Fatalf("publication dismissal: %v", err)
				}
			} else {
				if _, err = reopened.Reply(ctx, ReplyInput{ItemID: view.ItemID, ExpectedRevision: view.Revision, ResponseText: "explicit operator answer"}); err != nil {
					t.Fatalf("routed reply: %v", err)
				}
			}
		})
	}
}

func TestStageMetadataInvalidAdmissionHasNoPersistence(t *testing.T) {
	cases := map[string]func(map[string]any){
		"summary mismatch":   func(r map[string]any) { r["summary"] = "different" },
		"unknown annotation": func(r map[string]any) { r["annotations"].([]any)[0].(map[string]any)["kind"] = "execute" },
		"too many annotations": func(r map[string]any) {
			a := r["annotations"].([]any)[0]
			r["annotations"] = make([]any, 17)
			for i := range r["annotations"].([]any) {
				r["annotations"].([]any)[i] = a
			}
		},
		"too many traces": func(r map[string]any) {
			a := r["stage_trace"].([]any)[0]
			r["stage_trace"] = make([]any, 17)
			for i := range r["stage_trace"].([]any) {
				r["stage_trace"].([]any)[i] = a
			}
		},
		"oversized identity": func(r map[string]any) {
			r["stage_trace"].([]any)[0].(map[string]any)["stage_id"] = strings.Repeat("a", 129)
		},
		"negative duration":   func(r map[string]any) { r["stage_trace"].([]any)[0].(map[string]any)["duration_ms"] = -1 },
		"fractional duration": func(r map[string]any) { r["stage_trace"].([]any)[0].(map[string]any)["duration_ms"] = 0.5 },
		"unsafe integer": func(r map[string]any) {
			r["stage_trace"].([]any)[0].(map[string]any)["duration_ms"] = int64(9007199254740992)
		},
		"successful failure code": func(r map[string]any) { r["stage_trace"].([]any)[0].(map[string]any)["failure_code"] = "stage_error" },
		"unknown failure code": func(r map[string]any) {
			x := r["stage_trace"].([]any)[0].(map[string]any)
			x["outcome"], x["failure_code"] = "failed", "raw provider secret"
		},
		"timeout disagreement": func(r map[string]any) {
			x := r["stage_trace"].([]any)[0].(map[string]any)
			x["outcome"], x["failure_code"] = "timed_out", "stage_error"
		},
		"sender mismatch": func(r map[string]any) {
			r["source_message"].(map[string]any)["sender_urn"] = "msg://session/local/other"
		},
		"kind mismatch": func(r map[string]any) {
			r["source_message"].(map[string]any)["attribution"].(map[string]any)["kind"] = "final"
		},
		"agent mismatch":      func(r map[string]any) { r["source"].(map[string]any)["agent_id"] = "invented-agent" },
		"missing routed turn": func(r map[string]any) { delete(r, "turn_id") },
		"1.0 with new fields": func(r map[string]any) { r["contract_version"] = "1.0" },
		"publication with runtime IDs": func(r map[string]any) {
			r["source_message"].(map[string]any)["origin"] = "publication"
			r["kind"] = "checkpoint"
		},
		"encoded unicode budget": func(r map[string]any) {
			text := strings.Repeat("😺", 600)
			r["summary"] = text
			a := r["annotations"].([]any)[0].(map[string]any)
			a["summary"] = map[string]any{"text": text}
			r["annotations"] = []any{a, a, a, a}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			svc, _, database := newTestService(t)
			req := stageRequest(t, false)
			mutate(req)
			_, err := svc.Enqueue(context.Background(), EnqueueInput{Request: encodeStageRequest(t, req), Caller: testCaller("test")})
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("admitted invalid request: %v", err)
			}
			var persisted int
			if err := database.QueryRow("SELECT count(*) FROM interactions").Scan(&persisted); err != nil {
				t.Fatal(err)
			}
			if persisted != 0 {
				t.Fatal("invalid admission persisted an interaction")
			}
		})
	}
}

func TestStageMetadataLegacyItemRead(t *testing.T) {
	raw := turnRequestJSON("legacy-session", "legacy-turn", "question", "Legacy", "Unmodified legacy body")
	view, err := buildItemView(interaction.InteractionRecord{RequestSnapshot: raw}, nil, interaction.DeliveryStateQueued, nil)
	if err != nil {
		t.Fatal(err)
	}
	if view.ContractVersion != "1.0" || !view.Replyable || view.Content != "Unmodified legacy body" || view.Annotations != nil || view.SourceMessage != nil {
		t.Fatalf("legacy read changed: %#v", view)
	}
}

func TestStageMetadataEncodedByteBoundary(t *testing.T) {
	req := stageRequest(t, false)
	entries := []any{}
	for i := range 16 {
		entries = append(entries, map[string]any{"schema_version": 1, "stage_id": fmt.Sprintf("stage-%d", i), "stage_version": "1", "kind": "summary", "summary": map[string]any{"text": "s"}})
	}
	req["annotations"] = entries
	metadataSize := func() int {
		return len(encodeStageRequest(t, map[string]any{"annotations": entries, "stage_trace": req["stage_trace"]}))
	}
	for n := 1; ; n++ {
		for _, entry := range entries {
			entry.(map[string]any)["summary"] = map[string]any{"text": strings.Repeat("s", n)}
		}
		if metadataSize() > 8192 {
			req["summary"] = strings.Repeat("s", n-1)
			for _, entry := range entries {
				entry.(map[string]any)["summary"] = map[string]any{"text": req["summary"]}
			}
			break
		}
	}
	last := entries[len(entries)-1].(map[string]any)
	last["stage_id"] = last["stage_id"].(string) + strings.Repeat("x", 8192-metadataSize())
	if metadataSize() != 8192 {
		t.Fatal("fixture is not at the exact encoded boundary")
	}
	svc, _, _ := newTestService(t)
	if _, err := svc.Enqueue(context.Background(), EnqueueInput{Request: encodeStageRequest(t, req), Caller: testCaller("test")}); err != nil {
		t.Fatalf("8192-byte metadata refused: %v", err)
	}
	last["stage_id"] = last["stage_id"].(string) + "x"
	req["idempotency_key"] = "oversized"
	if _, err := svc.Enqueue(context.Background(), EnqueueInput{Request: encodeStageRequest(t, req), Caller: testCaller("test")}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("8193-byte metadata admitted: %v", err)
	}
}
