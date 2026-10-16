package room

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	envelopes "github.com/hollis-labs/libs/ui/ui-go/envelopes"

	"github.com/hollis-labs/tangent/internal/formsecret"
)

const secretCanary = "SECRETCANARY-5d21b0aa"

func secretFormEnvelope(id string) *envelopes.Envelope {
	return &envelopes.Envelope{
		V: 1, ID: id, Type: formsecret.FormCollectType,
		Data: map[string]any{
			"form_id": "secret-probe",
			"schema": map[string]any{"fields": []any{
				map[string]any{"id": "api_key", "type": "password", "secret": true},
				map[string]any{"id": "name", "type": "text"},
			}},
		},
	}
}

// A secret answer must not reach the canonical authority, and a draft must not
// carry it either; the waiting caller still gets the value exactly once.
func TestSecretAnswersNeverReachTheDispositionOrADraft(t *testing.T) {
	rm := NewManager(nil).Create(nil)
	t.Cleanup(func() { rm.Close("test done") })
	dial := newWSDialer(t)
	disposition := newStubDisposition()

	pair := dial()
	conn := attach(t, rm, pair, AttachOptions{ClientID: "tab-a", Label: "tab a"})
	if err := rm.Present(secretFormEnvelope("form-secret-1"), nil, disposition); err != nil {
		t.Fatalf("present: %v", err)
	}
	revision := readEnvelopeRevision(t, pair, "form-secret-1")

	if err := rm.HandleDraftFrom(conn, "form-secret-1", 1,
		json.RawMessage(`{"answers":{"api_key":"`+secretCanary+`","name":"visible"}}`)); err != nil {
		t.Fatalf("draft: %v", err)
	}
	select {
	case draft := <-disposition.drafts:
		if strings.Contains(draft.payload, secretCanary) || !strings.Contains(draft.payload, "visible") {
			t.Fatalf("draft payload = %s: the secret must be emptied and the rest kept", draft.payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("draft never reached the authority")
	}

	resp := &envelopes.Response{
		V: 1, EnvelopeID: "form-secret-1", Kind: envelopes.ResponseKindData, Status: envelopes.ResponseStatusSubmitted,
		Payload: map[string]any{"form_id": "secret-probe", "answers": map[string]any{"api_key": secretCanary, "name": "visible"}},
	}
	if err := rm.HandleResponseFrom(conn, "form-secret-1", revision, resp); err != nil {
		t.Fatalf("response: %v", err)
	}
	stored := <-disposition.resolved
	raw, _ := json.Marshal(stored)
	if strings.Contains(string(raw), secretCanary) {
		t.Fatalf("the secret reached Disposition.Resolve: %s", raw)
	}
	answers := stored.Payload.(map[string]any)["answers"].(map[string]any)
	if !formsecret.IsTombstone(answers["api_key"]) || answers["name"] != "visible" {
		t.Fatalf("stored answers = %v", answers)
	}

	// The vault, not the store, holds the value, and gives it up once.
	frame := json.RawMessage(raw)
	first, ok := formsecret.Deliver("form-secret-1", frame)
	if !ok || !strings.Contains(string(first), secretCanary) {
		t.Fatalf("first delivery = %s ok=%v", first, ok)
	}
	if again, ok := formsecret.Deliver("form-secret-1", frame); ok || strings.Contains(string(again), secretCanary) {
		t.Fatalf("second delivery must be tombstones only: %s", again)
	}
}

func TestSecretFreeFormsAndOtherKindsPassThrough(t *testing.T) {
	plain := &envelopes.Envelope{V: 1, ID: "p", Type: formsecret.FormCollectType,
		Data: map[string]any{"schema": map[string]any{"fields": []any{map[string]any{"id": "a", "type": "text"}}}}}
	resp := &envelopes.Response{Payload: map[string]any{"answers": map[string]any{"a": "x"}}}
	if got, secrets := redactSecretAnswers(plain, resp); got != resp || secrets != nil {
		t.Fatalf("a form without secrets must be untouched: %v %v", got, secrets)
	}
	other := &envelopes.Envelope{V: 1, ID: "o", Type: "triage"}
	if got, secrets := redactSecretAnswers(other, resp); got != resp || secrets != nil {
		t.Fatal("another envelope kind must be untouched")
	}
}
