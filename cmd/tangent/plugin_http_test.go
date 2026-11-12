package main

import (
	"net/http"
	"strings"
	"testing"
)

// The plugin routes and the plugin management routes behind the real host's guard: no credential is
// refused with 401, a key is refused as an agent however many scopes it holds, and the owner's
// cookie session is refused when the request is not the page's own (no X-Requested-With, or a
// foreign Origin). The route handlers' own checks are asserted in internal/server.
func TestPluginRoutesAndManagementAreGuardedByTheRealHost(t *testing.T) {
	h := newHost(t)
	key := h.key("plugin-agent", "*")
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/plugins/stub/sync"},
		{"GET", "/api/plugin-management"},
		{"GET", "/api/plugin-management/x/config"},
		{"POST", "/api/plugin-management/x/enable"},
		{"POST", "/api/plugin-management/x/config/save"},
	} {
		if got := h.call("", route.method, route.path); got != http.StatusUnauthorized {
			t.Errorf("%s %s with no credential = %d, want 401", route.method, route.path, got)
		}
		if route.path != "/api/plugins/stub/sync" {
			if got := h.call(key, route.method, route.path); got != http.StatusForbidden {
				t.Errorf("%s %s with an all-scopes key = %d, want 403", route.method, route.path, got)
			}
		}
	}

	post := func(path string, headers map[string]string) int {
		t.Helper()
		req, _ := http.NewRequest("POST", h.base+path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := h.owner.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	const path = "/api/plugin-management/x/enable"
	if got := post(path, map[string]string{"X-Requested-With": "test", "Origin": h.base}); got == http.StatusUnauthorized || got == http.StatusForbidden {
		t.Fatalf("the owner's own request was refused: %d", got)
	}
	if got := post(path, nil); got != http.StatusForbidden && got != http.StatusUnauthorized {
		t.Errorf("a cookie session POST without X-Requested-With = %d, want it refused", got)
	}
	if got := post(path, map[string]string{"X-Requested-With": "test", "Origin": "http://evil.example.test"}); got != http.StatusForbidden && got != http.StatusUnauthorized {
		t.Errorf("a cookie session POST from a foreign Origin = %d, want it refused", got)
	}
}
