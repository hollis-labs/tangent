package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigRequestPreservesNumbersAndRefusesAmbiguousJSON(t *testing.T) {
	raw := `{"scope":{"kind":"client","id":"owner"},"revision":"r","changes":{"set":{"limit":9007199254740991},"unset":[]}}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(raw))
	input, err := decodePluginConfigRequest(httptest.NewRecorder(), req)
	if err != nil {
		t.Fatal(err)
	}
	number, ok := input.Changes.Set["limit"].(json.Number)
	if !ok || number.String() != "9007199254740991" {
		t.Fatalf("numeric precision lost: %v", input.Changes.Set)
	}
	for _, bad := range []string{strings.Replace(raw, `"revision":"r"`, `"revision":"r","revision":"s"`, 1), strings.Replace(raw, `9007199254740991`, `1,"limit":2`, 1), strings.Replace(raw, `"changes"`, `"credentials":"secret","changes"`, 1), raw + ` {}`, strings.Repeat(" ", maximumPluginRequestBytes) + raw} {
		req = httptest.NewRequest("POST", "/", strings.NewReader(bad))
		if _, err = decodePluginConfigRequest(httptest.NewRecorder(), req); err == nil {
			t.Fatal("ambiguous request accepted")
		}
	}
}
