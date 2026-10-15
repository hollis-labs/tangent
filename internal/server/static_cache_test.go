package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func testSPA() http.Handler {
	return spaHandlerFS(fstest.MapFS{
		"index.html":          {Data: []byte("<html>app</html>")},
		"assets/index-abc.js": {Data: []byte("console.log(1)")},
		"favicon.svg":         {Data: []byte("<svg/>")},
	})
}

func serve(h http.Handler, path, ifNoneMatch string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStaticCacheHeaders(t *testing.T) {
	h := testSPA()
	cases := []struct {
		path, cache string
	}{
		{"/assets/index-abc.js", "public, max-age=31536000, immutable"},
		{"/", "no-cache"},
		{"/inbox", "no-cache"}, // client route falls back to index.html
		{"/favicon.svg", "no-cache"},
	}
	for _, c := range cases {
		rec := serve(h, c.path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", c.path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != c.cache {
			t.Errorf("%s: Cache-Control %q, want %q", c.path, got, c.cache)
		}
		if rec.Header().Get("ETag") == "" {
			t.Errorf("%s: no ETag", c.path)
		}
	}
}

func TestStaticETagRevalidates(t *testing.T) {
	h := testSPA()
	etag := serve(h, "/assets/index-abc.js", "").Header().Get("ETag")
	rec := serve(h, "/assets/index-abc.js", etag)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("matching If-None-Match: status %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 carried a body")
	}
	if serve(h, "/assets/index-abc.js", `"other"`).Code != http.StatusOK {
		t.Errorf("non-matching If-None-Match should return 200")
	}
}

func TestStaticMissingAssetIsNotCached(t *testing.T) {
	rec := serve(testSPA(), "/assets/gone.js", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != "" {
		t.Errorf("404 must not carry a long-lived Cache-Control")
	}
}

// Two handlers over different filesystems must not share ETags, or a changed
// index.html would be answered with a false 304.
func TestStaticETagIsPerHandler(t *testing.T) {
	a := spaHandlerFS(fstest.MapFS{"index.html": {Data: []byte("<html>one</html>")}})
	b := spaHandlerFS(fstest.MapFS{"index.html": {Data: []byte("<html>two</html>")}})
	etagA := serve(a, "/", "").Header().Get("ETag")
	etagB := serve(b, "/", "").Header().Get("ETag")
	if etagA == "" || etagA == etagB {
		t.Fatalf("ETags a=%q b=%q, want distinct non-empty", etagA, etagB)
	}
	if code := serve(b, "/", etagA).Code; code != http.StatusOK {
		t.Errorf("handler b answered a's ETag with %d, want 200", code)
	}
}
