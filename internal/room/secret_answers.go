package room

import (
	"encoding/json"
	"time"

	envelopes "github.com/hollis-labs/libs/ui/ui-go/envelopes"

	"github.com/hollis-labs/tangent/internal/formsecret"
)

// redactSecretAnswers replaces secret-flagged form answers in resp with
// tombstones and returns the removed values. resp must already be a private
// copy. A response for any other envelope kind, or a form with no secret
// field, comes back unchanged with no secrets.
func redactSecretAnswers(env *envelopes.Envelope, envelopeID string, resp *envelopes.Response) (*envelopes.Response, formsecret.Secrets) {
	if env == nil || resp == nil || env.Type != formsecret.FormCollectType {
		return resp, nil
	}
	fields := formsecret.FromEnvelopeData(env.Data)
	if fields.Empty() {
		return resp, nil
	}
	payload, ok := payloadMap(resp.Payload)
	if !ok {
		return resp, nil
	}
	redacted, secrets := fields.RedactPayload(payload, envelopeID, time.Now(), "form-secret")
	next := *resp
	next.Payload = redacted
	return &next, secrets
}

// restoreSecretAnswers returns a copy of resp with the secrets put back, for
// the one in-memory hand-off to a blocked legacy caller.
func restoreSecretAnswers(resp *envelopes.Response, secrets formsecret.Secrets) *envelopes.Response {
	payload, ok := payloadMap(resp.Payload)
	if !ok {
		return resp
	}
	next := *resp
	restored := make(map[string]any, len(payload))
	for k, v := range payload {
		restored[k] = v
	}
	if answers, ok := payload["answers"].(map[string]any); ok {
		restored["answers"] = secrets.Restore(answers)
	}
	next.Payload = restored
	return &next
}

func payloadMap(payload any) (map[string]any, bool) {
	switch typed := payload.(type) {
	case map[string]any:
		return typed, true
	case nil:
		return nil, false
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, false
	}
	return out, out != nil
}
