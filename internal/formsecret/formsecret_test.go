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

func storedFrame(t *testing.T, key string) (json.RawMessage, Secrets) {
	t.Helper()
	f := FromEnvelopeData(schemaData(t))
	redacted, secrets := f.Redact(answers(), time.Now(), "a")
	frame := mustJSON(map[string]any{"v": 1, "payload": map[string]any{"form_id": "f", "answers": redacted}})
	Default.Put(key, secrets)
	return frame, secrets
}

func TestDeliverRestoresOnceToTheCreatorThenFlagsUnavailable(t *testing.T) {
	key := Key("room-1", "env-d")
	stored, _ := storedFrame(t, key)
	first, status := Deliver(key, "owner", "owner", stored)
	if status != Delivered || strings.Count(string(first), canary) != 4 {
		t.Fatalf("first delivery = %s status=%v", first, status)
	}
	second, status := Deliver(key, "owner", "owner", stored)
	if status != Unavailable || strings.Contains(string(second), canary) || !strings.Contains(string(second), `"secret_unavailable":true`) {
		t.Fatalf("second delivery must flag the loss: %s status=%v", second, status)
	}
	if strings.Contains(string(stored), canary) {
		t.Fatal("the stored frame must never be modified")
	}
}

func TestDeliverRefusesAReaderWhoIsNotTheCreatorAndKeepsTheEntry(t *testing.T) {
	key := Key("room-1", "env-other")
	stored, _ := storedFrame(t, key)
	out, status := Deliver(key, "someone-else", "owner", stored)
	if status != Unavailable || strings.Contains(string(out), canary) {
		t.Fatalf("a stranger must not get the secret: %s status=%v", out, status)
	}
	if _, status = Deliver(key, "owner", "owner", stored); status != Delivered {
		t.Fatal("the stranger's read must not have consumed the creator's entry")
	}
}

func TestDeliverIsKeyedByRoomAndEnvelopeAndNeverTakesForANonSecretFrame(t *testing.T) {
	a, b := Key("room-a", "credentials"), Key("room-b", "credentials")
	storedA, _ := storedFrame(t, a)
	Default.Put(b, Secrets{"api_key": "OTHER-ROOM-SECRET"})
	got, status := Deliver(a, "o", "o", storedA)
	if status != Delivered || strings.Contains(string(got), "OTHER-ROOM-SECRET") {
		t.Fatalf("room a read must not see room b: %s", got)
	}
	// A frame with no tombstones does not consume the entry.
	plain := mustJSON(map[string]any{"payload": map[string]any{"answers": map[string]any{"name": "x"}}})
	if _, status = Deliver(b, "o", "o", plain); status != NotApplicable {
		t.Fatalf("status = %v, want NotApplicable", status)
	}
	if _, ok := Default.Take(b); !ok {
		t.Fatal("a non-secret read destroyed another interaction's entry")
	}
}

func TestRestoreSkipsAbsentSlots(t *testing.T) {
	out := Secrets{"gone": "x", "sec.gone": "y", "rows[3].t": "z"}.Restore(map[string]any{"name": "v"})
	if len(out) != 1 || out["name"] != "v" {
		t.Fatalf("restore invented keys: %v", out)
	}
}

func TestVaultSweepsOnTakeAndCapsItsSize(t *testing.T) {
	v := NewVault(time.Minute)
	v.max = 2
	now := time.Unix(100, 0)
	v.now = func() time.Time { return now }
	v.Put("a", Secrets{"k": "1"})
	now = now.Add(time.Second)
	v.Put("b", Secrets{"k": "2"})
	now = now.Add(time.Second)
	v.Put("c", Secrets{"k": "3"})
	if v.Len() != 2 {
		t.Fatalf("len = %d, want the cap of 2", v.Len())
	}
	if _, ok := v.Take("a"); ok {
		t.Fatal("the oldest entry should have been evicted")
	}
	now = now.Add(2 * time.Minute)
	if v.Len() != 0 {
		t.Fatal("expired entries must be swept on read, not only on Put")
	}
}

func TestPasswordTypedFieldsAreSecretWithoutTheFlag(t *testing.T) {
	var data map[string]any
	_ = json.Unmarshal([]byte(`{"schema":{"fields":[{"id":"pw","type":"password"}],"sections":[{"id":"s","fields":[{"id":"t","type":"password"}]}]}}`), &data)
	f := FromEnvelopeData(data)
	if !f.Top["pw"] || !f.Sections["s"]["t"] {
		t.Fatalf("fields = %+v", f)
	}
}

func TestNonStringSecretsAreRedactedToo(t *testing.T) {
	f := FromEnvelopeData(schemaData(t))
	out, secrets := f.Redact(map[string]any{"api_key": 123456, "one": map[string]any{"pw": []any{"a", "b"}}}, time.Now(), "a")
	raw := string(mustJSON(out))
	if strings.Contains(raw, "123456") || strings.Contains(raw, `"a","b"`) || len(secrets) != 2 {
		t.Fatalf("out=%s secrets=%v", raw, secrets)
	}
	back := secrets.Restore(out)
	if string(mustJSON(back)) != `{"api_key":123456,"one":{"pw":["a","b"]}}` {
		t.Fatalf("restore = %s", mustJSON(back))
	}
}

func TestValidateRefusesSecretsItCannotProtect(t *testing.T) {
	bad := map[string]string{
		"number":            `{"schema":{"fields":[{"id":"pin","type":"number","secret":true}]}}`,
		"select":            `{"schema":{"fields":[{"id":"x","type":"select","secret":true}]}}`,
		"no id":             `{"schema":{"fields":[{"type":"text","secret":true}]}}`,
		"anonymous section": `{"schema":{"sections":[{"fields":[{"id":"t","type":"text","secret":true}]}]}}`,
	}
	for name, raw := range bad {
		var data map[string]any
		_ = json.Unmarshal([]byte(raw), &data)
		if err := Validate(data); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
	if err := Validate(schemaData(t)); err != nil {
		t.Fatalf("a supported schema must pass: %v", err)
	}
}

func TestBlankDraftAndCloneFailClosed(t *testing.T) {
	f := FromEnvelopeData(schemaData(t))
	if got := f.BlankDraft(json.RawMessage(`{not json`)); string(got) != `{}` {
		t.Fatalf("an unreadable draft must carry nothing, got %s", got)
	}
	if got := cloneMap(map[string]any{"bad": make(chan int)}); len(got) != 0 {
		t.Fatalf("a failed clone must not alias the original: %v", got)
	}
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

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}
