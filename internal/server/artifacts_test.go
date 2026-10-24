package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/artifacts"
)

// fakeArtifacts serves one revision file and nothing else.
type fakeArtifacts struct {
	ArtifactsService
	rev  artifacts.Revision
	path string
	err  error
}

func (f fakeArtifacts) OpenRevision(_ context.Context, _ string, _ int) (artifacts.Revision, *os.File, error) {
	if f.err != nil {
		return artifacts.Revision{}, nil, f.err
	}
	file, err := os.Open(f.path)
	return f.rev, file, err
}

func contentRequest(t *testing.T, svc ArtifactsService, query string) *httptest.ResponseRecorder {
	t.Helper()
	handler := newArtifactsHTTPHandler(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/artifacts/items/abc/revisions/1/content"+query, nil)
	req.SetPathValue("itemID", "abc")
	req.SetPathValue("revision", "1")
	rec := httptest.NewRecorder()
	// The mux-wide security middleware sets these first; the content route must
	// replace them or no artifact could ever be framed.
	rec.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
	rec.Header().Set("X-Frame-Options", "DENY")
	handler.content(rec, req)
	return rec
}

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestArtifactContentHTMLRunsSandboxedWithNoNetwork(t *testing.T) {
	path := writeTemp(t, "a.html", "<h1>x</h1>")
	rec := contentRequest(t, fakeArtifacts{
		rev:  artifacts.Revision{Revision: 1, Filename: "a.html", MediaType: "text/html", CreatedAt: time.Now()},
		path: path,
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"sandbox allow-scripts", "connect-src 'none'", "frame-ancestors 'self'", "form-action 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "allow-same-origin") {
		t.Errorf("CSP grants same-origin: %q", csp)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want SAMEORIGIN", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if rec.Body.String() != "<h1>x</h1>" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestArtifactContentOtherTypesRunNothing(t *testing.T) {
	path := writeTemp(t, "s.svg", "<svg xmlns='http://www.w3.org/2000/svg'><script>alert(1)</script></svg>")
	rec := contentRequest(t, fakeArtifacts{
		rev:  artifacts.Revision{Revision: 1, Filename: "s.svg", MediaType: "image/svg+xml", CreatedAt: time.Now()},
		path: path,
	}, "")
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "sandbox;") || strings.Contains(csp, "script-src") || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("passive CSP = %q", csp)
	}
}

func TestArtifactContentDownloadIsAnAttachment(t *testing.T) {
	path := writeTemp(t, "a.html", "<p>x</p>")
	rec := contentRequest(t, fakeArtifacts{
		rev:  artifacts.Revision{Revision: 1, Filename: "mock page.html", MediaType: "text/html", CreatedAt: time.Now()},
		path: path,
	}, "?download=1")
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") || !strings.Contains(got, "mock page.html") {
		t.Fatalf("Content-Disposition = %q", got)
	}
}

func TestArtifactContentErrors(t *testing.T) {
	if rec := contentRequest(t, fakeArtifacts{err: artifacts.ErrNotFound}, ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing revision status = %d, want 404", rec.Code)
	}
	handler := newArtifactsHTTPHandler(fakeArtifacts{})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.SetPathValue("itemID", "abc")
	req.SetPathValue("revision", "zero")
	rec := httptest.NewRecorder()
	handler.content(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad revision status = %d, want 400", rec.Code)
	}
}

func TestWriteArtifactsErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{artifacts.ErrNotFound, http.StatusNotFound},
		{artifacts.ErrTooLarge, http.StatusRequestEntityTooLarge},
		{artifacts.ErrInvalidRequest, http.StatusBadRequest},
		{artifacts.ErrTerminalConflict, http.StatusConflict},
		{artifacts.ErrStaleRevision, http.StatusConflict},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		writeArtifactsError(rec, tc.err)
		if rec.Code != tc.code {
			t.Errorf("%v -> %d, want %d", tc.err, rec.Code, tc.code)
		}
	}
}
