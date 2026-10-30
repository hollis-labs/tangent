package server

import (
	"encoding/json"
	"testing"
)

func TestWorkflowRowsGetATitleFromTheEnvelopeIntentOrFormID(t *testing.T) {
	refs := json.RawMessage(`{"legacy_envelope":{"title":"Release the held GUI tasks"}}`)
	cases := map[string]struct {
		snapshot string
		refs     json.RawMessage
		want     string
	}{
		"envelope title wins": {`{"form_id":"f","intent":"i"}`, refs, "Release the held GUI tasks"},
		"intent":              {`{"form_id":"f","intent":"Settle six questions"}`, nil, "Settle six questions"},
		"form id":             {`{"form_id":"gated-1"}`, nil, "gated-1"},
		"own title is kept":   {`{"title":"Own","intent":"i"}`, refs, "Own"},
		"nothing to name it":  {`{"x":1}`, nil, ""},
	}
	for name, tc := range cases {
		var got struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal(summarizeSnapshot(json.RawMessage(tc.snapshot), tc.refs), &got)
		if got.Title != tc.want {
			t.Errorf("%s: title = %q, want %q", name, got.Title, tc.want)
		}
	}
}
