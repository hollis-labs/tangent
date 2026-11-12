package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// The document policy is on the SPA's responses: the index page and a deep link the SPA routes on
// the client (which the host answers with the index page). The policy is what makes network.fetch
// a barrier for renderers and stops untrusted preview scripts; losing it was a silent change, so
// the header is asserted on the real handler chain, not on the middleware alone.
func TestTheDocumentPolicyIsOnTheSPAResponses(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>spa</title>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	base, _ := startedWith(t, nil, assets)

	for _, path := range []string{"/", "/index.html", "/r/room-1", "/inbox/some/deep/link", "/assets/app.js", "/api/setup", "/healthz"} {
		res, err := (&http.Client{Timeout: 10 * time.Second}).Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()

		csp := res.Header.Get("Content-Security-Policy")
		if csp == "" {
			t.Errorf("GET %s (%d): no Content-Security-Policy", path, res.StatusCode)
			continue
		}
		for _, want := range []string{"script-src 'self' 'sha256-", "connect-src 'self' ws://", "object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "form-action 'self'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("GET %s: policy %q lacks %q", path, csp, want)
			}
		}
		if strings.Contains(csp, "default-src") || strings.Contains(csp, "unsafe-eval") {
			t.Errorf("GET %s: policy %q has default-src or unsafe-eval", path, csp)
		}
		for _, header := range []string{"Permissions-Policy", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy", "Cross-Origin-Opener-Policy", "Cross-Origin-Resource-Policy"} {
			if res.Header.Get(header) == "" {
				t.Errorf("GET %s: no %s", path, header)
			}
		}
	}
}

// A request the Host check refuses carries the headers too.
func TestAHostRefusalCarriesTheSecurityHeaders(t *testing.T) {
	base, _ := started(t, nil)
	req, _ := http.NewRequest(http.MethodGet, base+"/", nil)
	req.Host = "evil.example.test"
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusMisdirectedRequest {
		t.Logf("status %d for a foreign Host", res.StatusCode)
	}
	if res.Header.Get("Content-Security-Policy") == "" {
		t.Errorf("a refused request (%d) has no Content-Security-Policy", res.StatusCode)
	}
}
