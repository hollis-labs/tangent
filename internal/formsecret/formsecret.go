// Package formsecret keeps secret-flagged form-collect answers out of every
// persisted place (CW-20261016-0176).
//
// A form field carrying "secret": true is read once from the participant's
// response, replaced in that response by a RedactionTombstone before anything
// is stored (the resolution, the room's form state and the v0.12 envelope
// projection, drafts, saved drafts, templates), and parked in an in-memory,
// one-shot Vault. The caller that is waiting on the interaction takes the real
// value from the Vault exactly once; every later read, and any read after a
// restart or the Vault's TTL, sees only the tombstone.
package formsecret

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FormCollectType is the envelope type this package understands.
const FormCollectType = "tangent.form-collect"

// PolicyRef names the redaction policy in a tombstone.
const PolicyRef = "form.secret-field"

// Tombstone is the RedactionTombstone shape of ADR 0002 section 2
// (internal/db.RedactionTombstone), one per redacted answer.
type Tombstone struct {
	Redacted      bool   `json:"redacted"`
	PolicyRef     string `json:"policy_ref"`
	At            string `json:"at"`
	ContentDigest string `json:"content_digest"`
	ActorRef      string `json:"actor_ref"`
}

// IsTombstone reports whether v is a tombstone produced by this package.
func IsTombstone(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	redacted, _ := m["redacted"].(bool)
	policy, _ := m["policy_ref"].(string)
	return redacted && policy == PolicyRef
}

// Fields names the secret fields of one form schema.
type Fields struct {
	// Top holds the ids of secret fields at the top level of the schema.
	Top map[string]bool
	// Sections maps a section id to the ids of its secret fields. A section's
	// answer is one row object, or a list of rows when the section repeats.
	Sections map[string]map[string]bool
}

// Empty reports whether the schema has no secret field.
func (f Fields) Empty() bool { return len(f.Top) == 0 && len(f.Sections) == 0 }

// FromEnvelopeData reads the secret fields from a form-collect envelope's data
// (the map under "data"), looking at data.schema.fields and
// data.schema.sections[].fields.
func FromEnvelopeData(data map[string]any) Fields {
	out := Fields{Top: map[string]bool{}, Sections: map[string]map[string]bool{}}
	schema, _ := data["schema"].(map[string]any)
	collect := func(raw any) map[string]bool {
		ids := map[string]bool{}
		list, _ := raw.([]any)
		for _, item := range list {
			field, _ := item.(map[string]any)
			if secret, _ := field["secret"].(bool); secret {
				if id, _ := field["id"].(string); id != "" {
					ids[id] = true
				}
			}
		}
		return ids
	}
	out.Top = collect(schema["fields"])
	sections, _ := schema["sections"].([]any)
	for _, raw := range sections {
		section, _ := raw.(map[string]any)
		id, _ := section["id"].(string)
		if ids := collect(section["fields"]); id != "" && len(ids) > 0 {
			out.Sections[id] = ids
		}
	}
	return out
}

// Secrets are the values taken out of one answers object, keyed the way the
// answers were: "field" for a top-level field, "section.field" for a
// section's, and "section[2].field" for a repeatable section's row.
type Secrets map[string]string

// Redact returns a deep copy of answers with every secret value replaced by a
// tombstone, and the removed values. salt scopes the digests (use the
// interaction or envelope id) so the same secret digests differently per form.
func (f Fields) Redact(answers map[string]any, salt string, at time.Time, actor string) (map[string]any, Secrets) {
	secrets := Secrets{}
	out := cloneMap(answers)
	tomb := func(path string, value any) any {
		text, _ := value.(string)
		if text == "" {
			return value // nothing was typed: nothing to hide, and an empty answer stays empty
		}
		secrets[path] = text
		sum := sha256.Sum256([]byte(salt + "\x00" + path + "\x00" + text))
		return map[string]any{
			"redacted": true, "policy_ref": PolicyRef, "at": at.UTC().Format(time.RFC3339),
			"content_digest": "sha256:" + hex.EncodeToString(sum[:]), "actor_ref": actor,
		}
	}
	for id := range f.Top {
		if v, ok := out[id]; ok {
			out[id] = tomb(id, v)
		}
	}
	for section, ids := range f.Sections {
		switch rows := out[section].(type) {
		case map[string]any:
			for id := range ids {
				if v, ok := rows[id]; ok {
					rows[id] = tomb(section+"."+id, v)
				}
			}
		case []any:
			for i, raw := range rows {
				row, _ := raw.(map[string]any)
				for id := range ids {
					if v, ok := row[id]; ok {
						row[id] = tomb(section+"["+strconv.Itoa(i)+"]."+id, v)
					}
				}
			}
		}
	}
	return out, secrets
}

// Blank returns a deep copy of answers with every secret field emptied. It is
// for places that must never hold a value, not even a digest of one: drafts,
// saved drafts and templates.
func (f Fields) Blank(answers map[string]any) map[string]any {
	out := cloneMap(answers)
	for id := range f.Top {
		if _, ok := out[id]; ok {
			out[id] = ""
		}
	}
	for section, ids := range f.Sections {
		switch rows := out[section].(type) {
		case map[string]any:
			for id := range ids {
				if _, ok := rows[id]; ok {
					rows[id] = ""
				}
			}
		case []any:
			for _, raw := range rows {
				row, _ := raw.(map[string]any)
				for id := range ids {
					if _, ok := row[id]; ok {
						row[id] = ""
					}
				}
			}
		}
	}
	return out
}

// Restore puts the secret values back into a deep copy of answers, replacing
// the tombstones Redact left. A path with no matching slot is skipped.
func (s Secrets) Restore(answers map[string]any) map[string]any {
	out := cloneMap(answers)
	for path, value := range s {
		setPath(out, path, value)
	}
	return out
}

// RedactPayload applies Redact to a form-collect payload ({"answers": {...}}),
// and Blank to the answers inside saved_drafts and templates. It returns a new
// payload map and the removed secrets.
func (f Fields) RedactPayload(payload map[string]any, salt string, at time.Time, actor string) (map[string]any, Secrets) {
	if f.Empty() {
		return payload, nil
	}
	out := cloneMap(payload)
	var secrets Secrets
	if answers, ok := out["answers"].(map[string]any); ok {
		out["answers"], secrets = f.Redact(answers, salt, at, actor)
	}
	for _, key := range []string{"saved_drafts", "templates"} {
		list, _ := out[key].([]any)
		for _, raw := range list {
			if item, _ := raw.(map[string]any); item != nil {
				if answers, ok := item["answers"].(map[string]any); ok {
					item["answers"] = f.Blank(answers)
				}
			}
		}
	}
	return out, secrets
}

// BlankDraft empties the secret fields of a draft frame ({"answers": {...}},
// and the answers of its saved drafts and templates).
func (f Fields) BlankDraft(draft json.RawMessage) json.RawMessage {
	if f.Empty() || len(draft) == 0 {
		return draft
	}
	var value map[string]any
	if json.Unmarshal(draft, &value) != nil {
		return draft
	}
	if answers, ok := value["answers"].(map[string]any); ok {
		value["answers"] = f.Blank(answers)
	}
	for _, key := range []string{"saved_drafts", "templates"} {
		list, _ := value[key].([]any)
		for _, raw := range list {
			if item, _ := raw.(map[string]any); item != nil {
				if answers, ok := item["answers"].(map[string]any); ok {
					item["answers"] = f.Blank(answers)
				}
			}
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return draft
	}
	return raw
}

// Vault holds secrets in memory until the waiting caller takes them. It is
// deliberately not persistent: a restart loses undelivered values, and the
// caller then sees only the tombstone and must ask again.
type Vault struct {
	mu    sync.Mutex
	ttl   time.Duration
	now   func() time.Time
	items map[string]entry
}

type entry struct {
	secrets Secrets
	expires time.Time
}

// NewVault returns a Vault whose entries expire after ttl.
func NewVault(ttl time.Duration) *Vault {
	return &Vault{ttl: ttl, now: time.Now, items: map[string]entry{}}
}

// Default is the process-wide vault: one Tangent process owns the waiting
// callers and the participants answering them.
var Default = NewVault(15 * time.Minute)

// Put parks secrets under key (the envelope id), replacing any earlier entry.
func (v *Vault) Put(key string, s Secrets) {
	if len(s) == 0 {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sweepLocked()
	v.items[key] = entry{secrets: s, expires: v.now().Add(v.ttl)}
}

// Drop removes the entry, if any.
func (v *Vault) Drop(key string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.items, key)
}

// Take removes and returns the entry: the caller gets the value once.
func (v *Vault) Take(key string) (Secrets, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.items[key]
	if !ok {
		return nil, false
	}
	delete(v.items, key)
	if !v.now().Before(e.expires) {
		return nil, false
	}
	return e.secrets, true
}

func (v *Vault) sweepLocked() {
	now := v.now()
	for k, e := range v.items {
		if !now.Before(e.expires) {
			delete(v.items, k)
		}
	}
}

// --- helpers ----------------------------------------------------------------

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return in
	}
	return out
}

// setPath writes value at "field", "section.field" or "section[i].field".
// Field and section ids are assumed not to contain "." or "[".
func setPath(answers map[string]any, path, value string) {
	if answers == nil {
		return
	}
	section, field, nested := strings.Cut(path, ".")
	if !nested {
		answers[path] = value
		return
	}
	if name, rest, repeated := strings.Cut(section, "["); repeated {
		n, err := strconv.Atoi(strings.TrimSuffix(rest, "]"))
		rows, _ := answers[name].([]any)
		if err != nil || n < 0 || n >= len(rows) {
			return
		}
		if row, _ := rows[n].(map[string]any); row != nil {
			row[field] = value
		}
		return
	}
	if row, _ := answers[section].(map[string]any); row != nil {
		row[field] = value
	}
}

// Deliver puts the parked secrets for key back into a stored response frame
// ({"payload":{"answers":{...}}}) and reports whether it did. It takes the
// entry, so only the first caller to read the outcome gets the values; every
// later read sees the stored tombstones. A response with nothing parked is
// returned unchanged.
func Deliver(key string, response json.RawMessage) (json.RawMessage, bool) {
	if key == "" || len(response) == 0 {
		return response, false
	}
	secrets, ok := Default.Take(key)
	if !ok {
		return response, false
	}
	var frame map[string]any
	if json.Unmarshal(response, &frame) != nil {
		return response, false
	}
	payload, _ := frame["payload"].(map[string]any)
	answers, _ := payload["answers"].(map[string]any)
	if answers == nil {
		return response, false
	}
	payload["answers"] = secrets.Restore(answers)
	raw, err := json.Marshal(frame)
	if err != nil {
		return response, false
	}
	return raw, true
}
