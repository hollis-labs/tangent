package main

import (
	"io"
	"net/http"
	"testing"
	"testing/fstest"
	"time"
)

// The SPA's cache policy is the framework's (libs webui under httphost); what Tangent owes is that
// it reaches the browser through its own chain (Host check, security headers, identity) intact.
// Hashed assets are immutable, the document and client routes revalidate, and a missing asset is
// never cached as if it existed.
func TestTheSPAIsCachedAsTheFrameworkDecidesThroughTheRealChain(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":          {Data: []byte("<html>app</html>")},
		"assets/index-abc.js": {Data: []byte("console.log(1)")},
		"favicon.svg":         {Data: []byte("<svg/>")},
	}
	base, _ := startedWith(t, nil, assets)
	get := func(path, ifNoneMatch string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		return res
	}
	for path, want := range map[string]string{
		"/assets/index-abc.js": "public, max-age=31536000, immutable",
		"/":                    "no-cache",
		"/inbox":               "no-cache",
		"/favicon.svg":         "no-cache",
	} {
		res := get(path, "")
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, res.StatusCode)
		}
		if got := res.Header.Get("Cache-Control"); got != want {
			t.Errorf("%s: Cache-Control %q, want %q", path, got, want)
		}
	}
	missing := get("/assets/gone.js", "")
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("a missing asset answered %d, want 404", missing.StatusCode)
	}
	if got := missing.Header.Get("Cache-Control"); got == "public, max-age=31536000, immutable" {
		t.Errorf("a missing asset carries the immutable policy: %q", got)
	}
}
