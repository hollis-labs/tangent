package server

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The document policy, and the one cross-language constant it depends on.
//
// As with the SPA-side suite, the honest scope has to be stated: **no browser
// runs in these tests.** What is asserted is that the policy Tangent hands the
// browser denies what it claims to deny and permits what the shipped
// application needs. Real-browser verification is
// docs/manual-tests/renderer-sandbox-e2e.md.

func policyFor(t *testing.T, host, devFrontendURL string) map[string][]string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Host = host
	out := map[string][]string{}
	for _, clause := range strings.Split(contentSecurityPolicy(request, devFrontendURL), ";") {
		fields := strings.Fields(clause)
		if len(fields) == 0 {
			continue
		}
		out[fields[0]] = fields[1:]
	}
	return out
}

// The shim's bytes are the hash, and the hash is in two languages. This is the
// test that keeps them one fact.
func TestSandboxFrameShimMatchesTheSPA(t *testing.T) {
	t.Parallel()
	shim := shimSource(t)
	sum := sha256.Sum256([]byte(shim))
	want := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
	if sandboxFrameShimSHA256 != want {
		t.Fatalf("the sandbox shim changed and the CSP hash did not follow.\n"+
			"  internal/server/csp.go:      %s\n"+
			"  recomputed from the SPA:     %s\n"+
			"Update both this constant and SANDBOX_FRAME_SHIM_SHA256 in "+
			"ui/src/lib/sandbox-frame.ts. Until they match, the shim does not run and "+
			"tangent.design-iteration presents no interactive regions.",
			sandboxFrameShimSHA256, want)
	}

	// And the SPA's own copy of the same constant.
	const modulePath = "../../ui/src/lib/sandbox-frame.ts"
	module, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatalf("read %s: %v", modulePath, err)
	}
	if !strings.Contains(string(module), `SANDBOX_FRAME_SHIM_SHA256 = "`+want+`"`) {
		t.Fatalf("%s does not carry the recomputed hash %s", modulePath, want)
	}
}

func TestTheDocumentPolicyEnforcesWhatItClaims(t *testing.T) {
	t.Parallel()
	csp := policyFor(t, "127.0.0.1:7842", "")

	// network.fetch. This directive is the whole reason that capability stopped
	// being `MediationDeclared` — see effect.MediationFor.
	connect := csp["connect-src"]
	if len(connect) != 3 || connect[0] != "'self'" {
		t.Fatalf("connect-src = %v, want 'self' plus this host's own websocket schemes", connect)
	}
	for _, source := range connect {
		if strings.Contains(source, "*") || strings.HasPrefix(source, "http") {
			t.Errorf("connect-src admits %q, which is not this origin", source)
		}
	}
	if connect[1] != "ws://127.0.0.1:7842" || connect[2] != "wss://127.0.0.1:7842" {
		t.Errorf("connect-src websocket sources = %v, want this host's", connect[1:])
	}

	// The shim hash, so an `<iframe srcdoc>` — which inherits this policy —
	// can still run Tangent's own bootstrap.
	script := strings.Join(csp["script-src"], " ")
	if !strings.Contains(script, "'"+sandboxFrameShimSHA256+"'") {
		t.Errorf("script-src = %q, want the sandbox shim hash: an srcdoc frame inherits "+
			"this policy, so without it the design-iteration shim cannot run", script)
	}
	if strings.Contains(script, "'unsafe-inline'") || strings.Contains(script, "'unsafe-eval'") {
		t.Errorf("script-src = %q, which admits arbitrary inline script", script)
	}

	// The one external origin in the policy, and the one directive it is
	// deliberately absent from. tangent.whiteboard loads tldraw's fonts and
	// icons from a CDN; admitting them as passive subresources while refusing
	// the scripted fetch beside them is the whole distinction network.fetch
	// names.
	if got := strings.Join(csp["font-src"], " "); !strings.Contains(got, "https://cdn.tldraw.com") {
		t.Errorf("font-src = %q, want tldraw's CDN: without it the whiteboard loses its typography", got)
	}
	if strings.Contains(strings.Join(connect, " "), "tldraw") {
		t.Error("connect-src admits tldraw's CDN; a scripted fetch to an external origin is " +
			"exactly the network.fetch capability the whiteboard manifest does not declare")
	}

	for directive, want := range map[string]string{
		"object-src":      "'none'",
		"base-uri":        "'none'",
		"frame-ancestors": "'none'",
		"form-action":     "'self'",
	} {
		if got := strings.Join(csp[directive], " "); got != want {
			t.Errorf("%s = %q, want %q", directive, got, want)
		}
	}

	// The directive that is deliberately absent, asserted so its absence is a
	// decision rather than an oversight. See the note in csp.go: a `frame-src`
	// arriving by `default-src` fallback risks blocking `about:srcdoc` frames,
	// which would break tangent.design-iteration outright.
	if _, present := csp["default-src"]; present {
		t.Error("default-src is set; it supplies a frame-src by fallback, and browsers " +
			"disagree about whether that blocks an about:srcdoc frame")
	}
	if _, present := csp["frame-src"]; present {
		t.Error("frame-src is set; see the note in csp.go about srcdoc frames")
	}
}

func TestTheDevProxyPolicyKeepsTheSandboxIntact(t *testing.T) {
	t.Parallel()
	csp := policyFor(t, "localhost:7842", "http://localhost:5173")

	script := strings.Join(csp["script-src"], " ")
	// Vite injects inline module scripts no hash can cover. A policy carrying
	// both a hash and 'unsafe-inline' ignores 'unsafe-inline' entirely, so the
	// hash is dropped rather than added alongside it.
	if !strings.Contains(script, "'unsafe-inline'") {
		t.Errorf("dev script-src = %q, want 'unsafe-inline' for Vite's preamble", script)
	}
	if strings.Contains(script, sandboxFrameShimSHA256) {
		t.Errorf("dev script-src = %q; a hash beside 'unsafe-inline' voids the "+
			"'unsafe-inline' and breaks the dev server", script)
	}

	connect := strings.Join(csp["connect-src"], " ")
	for _, want := range []string{"http://localhost:5173", "ws://localhost:5173"} {
		if !strings.Contains(connect, want) {
			t.Errorf("dev connect-src = %q, want %s for Vite HMR", connect, want)
		}
	}
	// The sandbox itself is unaffected: the frame's own policy carries the hash
	// and denies everything else, so untrusted scripts stay blocked in dev too.
	if got := strings.Join(csp["frame-ancestors"], " "); got != "'none'" {
		t.Errorf("dev frame-ancestors = %q", got)
	}
}

func TestPermissionsPolicyDeniesTheClipboardToEveryFrame(t *testing.T) {
	t.Parallel()
	policy := permissionsPolicy()
	// `(self)` is Tangent's own origin. An opaque-origin sandboxed frame is not
	// `self`, so this is what makes clipboard.write genuinely unavailable to a
	// sandboxed renderer while remaining available to the reviewed components
	// that use it today.
	if !strings.Contains(policy, "clipboard-write=(self)") {
		t.Errorf("Permissions-Policy = %q, want clipboard-write=(self)", policy)
	}
	for _, feature := range []string{
		"camera", "microphone", "geolocation", "usb", "serial", "payment",
		"display-capture", "clipboard-read", "idle-detection",
	} {
		if !strings.Contains(policy, feature+"=()") {
			t.Errorf("Permissions-Policy does not deny %s: %q", feature, policy)
		}
	}
}

// Every response, not every route. A header applied per route is a header the
// next route forgets.
func TestSecurityHeadersAreAppliedToEveryResponse(t *testing.T) {
	t.Parallel()
	handler := securityHeaders("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	for _, path := range []string{"/", "/api/rooms", "/healthz", "/r/room-1"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Host = "127.0.0.1:7842"
		handler.ServeHTTP(recorder, request)
		for _, header := range []string{
			"Content-Security-Policy", "Permissions-Policy", "Referrer-Policy",
			"X-Content-Type-Options", "X-Frame-Options",
			"Cross-Origin-Opener-Policy", "Cross-Origin-Resource-Policy",
		} {
			if recorder.Header().Get(header) == "" {
				t.Errorf("%s: no %s", path, header)
			}
		}
	}
}

// The one silent failure mode this design has, closed.
//
// The CSP hash is computed over the shim's *source* bytes. If a bundler ever
// rewrote that string — a minifier normalizing quotes, a transform re-escaping
// a template literal — the shipped bytes would stop matching the hash, the shim
// would not run, and `tangent.design-iteration` would render an inert preview
// with no error anywhere: CSP violations appear in the browser console, which
// no test reads. Nothing else in the build would notice.
//
// So the *embedded production bundle* is checked, not the source. Vite does not
// transform string contents today; this is what says so out loud, on every run,
// against the artifact that actually ships.
func TestTheShippedBundleCarriesTheHashedShimVerbatim(t *testing.T) {
	t.Parallel()
	dist, err := spaFileSystem()
	if err != nil {
		t.Fatalf("spaFileSystem: %v", err)
	}
	if _, statErr := fs.Stat(dist, "index.html"); statErr != nil {
		t.Skip("no SPA build embedded; `make build-ui` populates internal/server/ui_dist")
	}

	shim := shimSource(t)
	entries, err := fs.ReadDir(dist, "assets")
	if err != nil {
		t.Fatalf("read embedded assets: %v", err)
	}
	found := false
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".js") {
			continue
		}
		body, readErr := fs.ReadFile(dist, "assets/"+entry.Name())
		if readErr != nil {
			t.Fatalf("read %s: %v", entry.Name(), readErr)
		}
		if strings.Contains(string(body), shim) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no embedded bundle contains the sandbox shim verbatim.\n" +
			"Something in the frontend build is rewriting the string the CSP hash " +
			"covers. The shim will not execute, and design-iteration will present an " +
			"inert preview with no error outside the browser console.")
	}
}

// shimSource returns the exact bytes SANDBOX_FRAME_SHIM declares.
func shimSource(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile("../../ui/src/lib/sandbox-frame-shim.ts")
	if err != nil {
		t.Fatalf("read sandbox-frame-shim.ts: %v", err)
	}
	const anchor = "export const SANDBOX_FRAME_SHIM = `"
	start := strings.Index(string(source), anchor)
	if start < 0 {
		t.Fatal("sandbox-frame-shim.ts no longer declares SANDBOX_FRAME_SHIM as one template literal")
	}
	body := string(source)[start+len(anchor):]
	end := strings.LastIndex(body, "`")
	if end < 0 {
		t.Fatal("sandbox-frame-shim.ts: unterminated template literal")
	}
	return body[:end]
}
