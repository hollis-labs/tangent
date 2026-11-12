package settings

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	if s, err := Parse(nil); err != nil || len(s.AllowedHosts) != 0 {
		t.Fatalf("nil block: %v %v", s, err)
	}
	s, err := Parse(map[string]any{"allowed_hosts": []any{" Tangent.Example.Test ", "x.example.test:8443"}})
	if err != nil || len(s.AllowedHosts) != 2 || s.AllowedHosts[0] != "tangent.example.test" {
		t.Fatalf("hosts: %v %v", s, err)
	}
	for name, raw := range map[string]any{
		"unknown key": map[string]any{"allowed_host": []any{}},
		"not a map":   "x",
		"not a list":  map[string]any{"allowed_hosts": "a"},
		"not a host":  map[string]any{"allowed_hosts": []any{"a/b"}},
		"bad port":    map[string]any{"allowed_hosts": []any{"a:b:c"}},
		"non string":  map[string]any{"allowed_hosts": []any{1}},
	} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("%s was accepted", name)
		} else if !strings.Contains(err.Error(), "tangent") {
			t.Errorf("%s: the message does not name the key: %v", name, err)
		}
	}
}

func TestParseReplyMailKeys(t *testing.T) {
	s, err := Parse(map[string]any{"reply_to_optional": true, "tether_url": " https://tether.example.test ", "tether_sender": "msg://agent/x/y", "tether_token_ref": "file://tangent/tether-send-only"}) // #nosec G101 -- a reference, not a credential.
	if err != nil || s.TetherTokenRef != "file://tangent/tether-send-only" || !s.ReplyToOptional || s.TetherURL != "https://tether.example.test" || s.TetherSender != "msg://agent/x/y" {
		t.Fatalf("got %+v, %v", s, err)
	}
	if _, err = Parse(map[string]any{"reply_to_optional": "yes"}); err == nil {
		t.Fatal("a non-boolean reply_to_optional was accepted")
	}
	if _, err = Parse(map[string]any{"tether_url": 3}); err == nil {
		t.Fatal("a non-string tether_url was accepted")
	}
}
