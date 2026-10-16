package formsecret

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const canary = "SECRETCANARY-5d21b0aa"

func schemaData(t *testing.T) map[string]any {
	t.Helper()
	var data map[string]any
	err := json.Unmarshal([]byte(`{"form_id":"f","schema":{
	  "fields":[{"id":"api_key","type":"password","secret":true},{"id":"name","type":"text"}],
	  "sections":[{"id":"creds","repeatable":true,"fields":[{"id":"token","secret":true},{"id":"label"}]},
	              {"id":"one","fields":[{"id":"pw","secret":true}]}]}}`), &data)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func answers() map[string]any {
	return map[string]any{
		"api_key": canary, "name": "visible",
		"creds": []any{map[string]any{"token": canary + "-0", "label": "a"}, map[string]any{"token": canary + "-1", "label": "b"}},
		"one":   map[string]any{"pw": canary + "-pw"},
	}
}

func TestFromEnvelopeDataFindsTopAndSectionSecrets(t *testing.T) {
	f := FromEnvelopeData(schemaData(t))
	if !f.Top["api_key"] || f.Top["name"] || !f.Sections["creds"]["token"] || !f.Sections["one"]["pw"] || f.Empty() {
		t.Fatalf("fields = %+v", f)
	}
	if !FromEnvelopeData(map[string]any{"schema": map[string]any{"fields": []any{map[string]any{"id": "x"}}}}).Empty() {
		t.Fatal("a form without secret fields must be empty")
	}
}

func TestRedactRemovesEverySecretAndKeepsTheRest(t *testing.T) {
	f := FromEnvelopeData(schemaData(t))
	in := answers()
	out, secrets := f.Redact(in, time.Unix(5, 0), "actor")
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), canary) {
		t.Fatalf("canary survived redaction: %s", raw)
	}
	if len(secrets) != 4 || secrets["api_key"] != canary || secrets["creds[1].token"] != canary+"-1" || secrets["one.pw"] != canary+"-pw" {
		t.Fatalf("secrets = %v", secrets)
	}
	if out["name"] != "visible" || !IsTombstone(out["api_key"]) {
		t.Fatalf("out = %v", out)
	}
	if strings.Contains(string(mustJSON(in)), "redacted") {
		t.Fatal("input must not be mutated")
	}
	tomb := out["api_key"].(map[string]any)
	if tomb["policy_ref"] != PolicyRef || tomb["content_digest"] != NoDigest {
		t.Fatalf("tombstone = %v", tomb)
	}
	// round trip
	back := secrets.Restore(out)
	if string(mustJSON(back)) != string(mustJSON(in)) {
		t.Fatalf("restore = %s, want %s", mustJSON(back), mustJSON(in))
	}
}

func TestEmptySecretAnswerStaysEmpty(t *testing.T) {
	f := FromEnvelopeData(schemaData(t))
	out, secrets := f.Redact(map[string]any{"api_key": ""}, time.Now(), "a")
	if out["api_key"] != "" || len(secrets) != 0 {
		t.Fatalf("out=%v secrets=%v", out, secrets)
	}
}

func TestBlankDraftEmptiesSecretsInAnswersSavedDraftsAndTemplates(t *testing.T) {
	f := FromEnvelopeData(schemaData(t))
	draft := mustJSON(map[string]any{
		"answers":      answers(),
		"saved_drafts": []any{map[string]any{"id": "d", "answers": answers()}},
		"templates":    []any{map[string]any{"id": "t", "answers": answers()}},
	})
	out := f.BlankDraft(draft)
	if strings.Contains(string(out), canary) {
		t.Fatalf("canary in draft: %s", out)
	}
	var back map[string]any
	if err := json.Unmarshal(out, &back); err != nil || back["answers"].(map[string]any)["name"] != "visible" || back["answers"].(map[string]any)["api_key"] != "" {
		t.Fatalf("draft = %s (%v)", out, err)
	}
}

func TestRedactPayloadAlsoBlanksSavedDraftsAndTemplates(t *testing.T) {
	f := FromEnvelopeData(schemaData(t))
	payload := map[string]any{"form_id": "f", "answers": answers(),
		"saved_drafts": []any{map[string]any{"answers": answers()}}, "templates": []any{map[string]any{"answers": answers()}}}
	out, secrets := f.RedactPayload(payload, time.Now(), "a")
	if strings.Contains(string(mustJSON(out)), canary) || len(secrets) != 4 {
		t.Fatalf("payload = %s secrets=%v", mustJSON(out), secrets)
	}
}

func TestVaultIsOneShotAndExpires(t *testing.T) {
	v := NewVault(time.Minute)
	now := time.Unix(100, 0)
	v.now = func() time.Time { return now }
	v.Put("e1", Secrets{"api_key": canary})
	if got, ok := v.Take("e1"); !ok || got["api_key"] != canary {
		t.Fatalf("first take = %v %v", got, ok)
	}
	if _, ok := v.Take("e1"); ok {
		t.Fatal("second take must find nothing")
	}
	v.Put("e2", Secrets{"api_key": canary})
	now = now.Add(2 * time.Minute)
	if _, ok := v.Take("e2"); ok {
		t.Fatal("an expired entry must not be delivered")
	}
}

func TestDeliverRestoresOnceThenOnlyTombstones(t *testing.T) {
	f := FromEnvelopeData(schemaData(t))
	redacted, secrets := f.Redact(answers(), time.Now(), "a")
	stored := mustJSON(map[string]any{"v": 1, "payload": map[string]any{"form_id": "f", "answers": redacted}})
	Default.Put("env-d", secrets)
	first, ok := Deliver("env-d", stored)
	if !ok || strings.Count(string(first), canary) != 4 {
		t.Fatalf("first delivery = %s ok=%v", first, ok)
	}
	second, ok := Deliver("env-d", stored)
	if ok || strings.Contains(string(second), canary) {
		t.Fatalf("second delivery must be the tombstones: %s", second)
	}
	if strings.Contains(string(stored), canary) {
		t.Fatal("the stored frame must never be modified")
	}
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func TestTombstoneCarriesNoDigestOfTheSecret(t *testing.T) {
	f := FromEnvelopeData(schemaData(t))
	out, _ := f.Redact(map[string]any{"api_key": "hunter2"}, time.Now(), "a")
	raw := string(mustJSON(out))
	if strings.Contains(raw, "sha256") || strings.Contains(raw, "hunter2") || !strings.Contains(raw, NoDigest) {
		t.Fatalf("tombstone = %s", raw)
	}
}

func TestPrefilledFindsASeededSecretAndNamesOnlyThePath(t *testing.T) {
	f := FromEnvelopeData(schemaData(t))
	cases := map[string]map[string]any{
		"answers.api_key":                 {"answers": map[string]any{"api_key": canary}},
		"answers.one.pw":                  {"answers": map[string]any{"one": map[string]any{"pw": canary}}},
		"answers.creds[1].token":          {"answers": map[string]any{"creds": []any{map[string]any{}, map[string]any{"token": canary}}}},
		"saved_drafts[0].answers.api_key": {"saved_drafts": []any{map[string]any{"answers": map[string]any{"api_key": canary}}}},
		"templates[0].answers.api_key":    {"templates": []any{map[string]any{"answers": map[string]any{"api_key": canary}}}},
	}
	for want, data := range cases {
		got := f.Prefilled(data)
		if got != want || strings.Contains(got, canary) {
			t.Errorf("Prefilled = %q, want %q", got, want)
		}
	}
	for name, data := range map[string]map[string]any{
		"empty secret":    {"answers": map[string]any{"api_key": ""}},
		"non-secret only": {"answers": map[string]any{"name": "visible"}},
		"no answers":      {},
	} {
		if got := f.Prefilled(data); got != "" {
			t.Errorf("%s: Prefilled = %q, want none", name, got)
		}
	}
}
