package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tangent/internal/access"
	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	"github.com/hollis-labs/tangent/internal/server"
)

// A plugin route is a button in a room that does work with no agent turn, and it is not a side door
// around the guards every other browser route carries. The host's own guard (cookie session, key
// scopes, CSRF header) is the real host's: cmd/tangent/plugin_http_test.go asserts it there. These
// tests drive the route table behind a stand-in for the principal the host's guard stamps, chosen
// per request by the callerHeader, so what is asserted is the routes' own checks.

const callerHeader = "X-Test-Caller"

const (
	callerOwner = "owner"
	callerAgent = "agent"
)

func principalFor(caller string) (libauth.Principal, bool) {
	future := time.Now().Add(time.Hour)
	switch caller {
	case callerOwner:
		return libauth.Principal{Verified: true, ID: "user:usr_owner", Kind: "user", SessionID: "ses_owner", Scopes: []string{"*"}, ExpiresAt: future}, true
	case callerAgent:
		return libauth.Principal{Verified: true, ID: "key:k_agent", Kind: "service", Display: "agent", Scopes: []string{"*"}, TokenID: "k_agent", ExpiresAt: future}, true
	}
	return libauth.Principal{}, false
}

type lockedLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLogs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

type pluginApp struct {
	base  string
	table *server.Table
	logs  *lockedLogs
}

func newEnvelope(t *testing.T) *envelope.Service {
	t.Helper()
	service, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	return service
}

func startPluginApp(t *testing.T, mutate func(*server.Config)) *pluginApp {
	t.Helper()
	logs := &lockedLogs{}
	cfg := server.Config{
		Envelope:     newEnvelope(t),
		Participants: participant.NewGate(nil),
		Logger:       slog.New(slog.NewTextHandler(logs, nil)),
	}
	mutate(&cfg)
	table, err := server.New(cfg)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	handler := table.Handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := principalFor(r.Header.Get(callerHeader)); ok {
			r = r.WithContext(libauth.WithPrincipal(r.Context(), p))
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return &pluginApp{base: srv.URL, table: table, logs: logs}
}

func (a *pluginApp) do(t *testing.T, method, path, caller, body string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, a.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if caller != "" {
		req.Header.Set(callerHeader, caller)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return res, raw
}

// pluginRouteFunc adapts a function to the SDK's HTTPHandler.
type pluginRouteFunc func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error)

func (f pluginRouteFunc) HTTPHandle(ctx context.Context, req subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
	return f(ctx, req)
}

func syncRoute(handler pluginRouteFunc) pluginhost.HTTPRoute {
	return pluginhost.HTTPRoute{Method: http.MethodPost, Path: "/api/plugins/stub/sync", Capability: authz.Draft, Handler: handler}
}

func withRoute(handler pluginRouteFunc) func(*server.Config) {
	return func(cfg *server.Config) { cfg.PluginRoutes = []pluginhost.HTTPRoute{syncRoute(handler)} }
}

func okRoute(called *bool) pluginRouteFunc {
	return func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
		*called = true
		return subprocess.HTTPResponse{Status: http.StatusOK}, nil
	}
}

const syncPath = "/api/plugins/stub/sync"

// The requirement itself: the signed-in owner presses the button, the work happens, no agent turn.
func TestPluginRouteWorksForTheSignedInOwner(t *testing.T) {
	var seen subprocess.HTTPRequest
	app := startPluginApp(t, withRoute(func(_ context.Context, req subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
		seen = req
		return subprocess.HTTPResponse{Status: http.StatusOK, Body: []byte(`{"applied":3}`)}, nil
	}))
	res, body := app.do(t, http.MethodPost, syncPath+"?scope=active", callerOwner, `{"decisions":[]}`, map[string]string{
		"Cookie": "identity_session=capability-material", "Authorization": "Bearer capability-material",
	})
	if res.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != `{"applied":3}` {
		t.Fatalf("the owner's call = %d %s, want 200 and the plugin's own body", res.StatusCode, body)
	}
	if seen.Method != http.MethodPost || seen.Path != syncPath || seen.Query["scope"] != "active" || string(seen.Body) != `{"decisions":[]}` {
		t.Errorf("the plugin saw %+v, want the caller's request", seen)
	}
	// The credential that admitted the request is not the plugin's to hold.
	if seen.SessionID != "" {
		t.Errorf("the plugin was handed session id %q", seen.SessionID)
	}
	for name := range seen.Headers {
		if strings.EqualFold(name, "Cookie") || strings.EqualFold(name, "Authorization") {
			t.Errorf("the plugin was handed the %s header", name)
		}
	}
	if seen.Headers["Content-Type"] != "application/json" {
		t.Errorf("the plugin saw headers %v, want Content-Type among them", seen.Headers)
	}
}

// A plugin route is not a way to reach the process without a user session, and an agent's key is
// not one, whatever scopes it holds.
func TestPluginRouteIsRefusedWithoutASessionAndForAnAgentKey(t *testing.T) {
	var called bool
	app := startPluginApp(t, withRoute(okRoute(&called)))
	for name, caller := range map[string]string{"no credential": "", "an agent key": callerAgent} {
		res, _ := app.do(t, http.MethodPost, syncPath, caller, "{}", nil)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("a plugin route with %s = %d, want 403", name, res.StatusCode)
		}
	}
	if called {
		t.Fatal("the plugin handler ran for a caller that is not the signed-in owner")
	}
}

// The check that stops a drive-by POST from a page the owner has open covers plugin routes too.
func TestPluginRouteIsOriginGuarded(t *testing.T) {
	var called bool
	app := startPluginApp(t, withRoute(okRoute(&called)))
	for name, headers := range map[string]map[string]string{
		"a cross-site fetch": {"Sec-Fetch-Site": "cross-site"},
		"a foreign Origin":   {"Origin": "https://evil.example.test"},
	} {
		res, _ := app.do(t, http.MethodPost, syncPath, callerOwner, "{}", headers)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s to a plugin route = %d, want 403", name, res.StatusCode)
		}
	}
	if called {
		t.Fatal("the plugin handler ran for a cross-origin request")
	}
}

// The table the host maps onto its scope guard names the plugin's route with the scope its
// capability needs, so a route the check cannot see does not exist.
func TestPluginRouteAppearsInTheRouteTableWithItsScope(t *testing.T) {
	var called bool
	app := startPluginApp(t, withRoute(okRoute(&called)))
	for _, route := range app.table.Routes() {
		if route.Pattern == "POST "+syncPath {
			if route.Scope != access.Draft || route.UserOnly {
				t.Errorf("the plugin route is registered at scope %q (user-only %t), want %s and not user-only", route.Scope, route.UserOnly, access.Draft)
			}
			return
		}
	}
	t.Fatal("the plugin route is not in the route table; the host would leave it unmapped")
}

// A plugin that errors or panics gives the SPA a refusal to show, never a stack trace or a dead
// process.
func TestPluginRouteFailureRendersARefusal(t *testing.T) {
	for name, test := range map[string]struct {
		handler pluginRouteFunc
		want    string
	}{
		"an error is the plugin's own reason": {func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
			return subprocess.HTTPResponse{}, errors.New("torque is unreachable")
		}, "torque is unreachable"},
		"a panic is contained": {func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
			panic("a plugin defect")
		}, "panicked"},
	} {
		t.Run(name, func(t *testing.T) {
			app := startPluginApp(t, withRoute(test.handler))
			res, body := app.do(t, http.MethodPost, syncPath, callerOwner, "{}", nil)
			var refusal struct{ Code, Message string }
			if err := json.Unmarshal(body, &refusal); res.StatusCode != http.StatusBadGateway || err != nil ||
				refusal.Code != "plugin_error" || !strings.Contains(refusal.Message, test.want) {
				t.Fatalf("a failing plugin route = %d %s, want 502 plugin_error naming %q", res.StatusCode, body, test.want)
			}
			if after, _ := app.do(t, http.MethodGet, "/api/health/capability", callerOwner, "", nil); after.StatusCode == http.StatusBadGateway {
				t.Errorf("the server stopped serving after a plugin failure: %d", after.StatusCode)
			}
		})
	}
}

// The outbound half of the credential boundary: a plugin that could set a cookie could overwrite a
// session.
func TestPluginRouteCannotSetACookie(t *testing.T) {
	app := startPluginApp(t, withRoute(func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
		return subprocess.HTTPResponse{Status: http.StatusOK, Body: []byte(`{}`), Headers: map[string]string{
			"Set-Cookie": "identity_session=forged; Path=/", "Content-Type": "application/json",
		}}, nil
	}))
	res, _ := app.do(t, http.MethodPost, syncPath, callerOwner, "{}", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Set-Cookie"); got != "" {
		t.Errorf("a plugin set a cookie: %q", got)
	}
	if got := res.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q; an allowed header should have survived", got)
	}
	if !strings.Contains(app.logs.String(), "plugin route response headers dropped") {
		t.Error("the dropped header was not logged; a plugin author would have no way to learn why")
	}
}

// A button is not an upload endpoint.
func TestPluginRouteBodyIsCapped(t *testing.T) {
	var called bool
	app := startPluginApp(t, withRoute(okRoute(&called)))
	res, _ := app.do(t, http.MethodPost, syncPath, callerOwner, strings.Repeat("x", 64*1024), nil)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", res.StatusCode)
	}
	if called {
		t.Error("an oversized body reached the plugin")
	}
}

// pluginhost refuses a route no browser could reach when it is registered; the server checks again
// where routes are mounted, because a route that got past would answer 403 forever and look like a
// plugin bug.
func TestServerRefusesAPluginRouteNoCallerCouldReach(t *testing.T) {
	noop := func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
		return subprocess.HTTPResponse{Status: http.StatusOK}, nil
	}
	submit := syncRoute(noop)
	submit.Capability = authz.Submit
	outside := syncRoute(noop)
	outside.Path = "/api/hitl/sync"
	for name, test := range map[string]struct {
		route pluginhost.HTTPRoute
		want  string
	}{
		"a capability a signed-in user never holds": {submit, "submit"},
		"no capability at all":                      {func() pluginhost.HTTPRoute { r := syncRoute(noop); r.Capability = ""; return r }(), "gated on"},
		"a path outside the reserved prefix":        {outside, "reserved"},
	} {
		_, err := server.New(server.Config{Envelope: newEnvelope(t), Participants: participant.NewGate(nil), PluginRoutes: []pluginhost.HTTPRoute{test.route}})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: server.New = %v, want a refusal naming %q", name, err, test.want)
		}
	}
}

// The user check is what authorizes the routes, so mounting them with none would publish them
// guarded by nothing but their scope.
func TestServerRefusesPluginRoutesWithNoUserGate(t *testing.T) {
	var called bool
	_, err := server.New(server.Config{Envelope: newEnvelope(t), PluginRoutes: []pluginhost.HTTPRoute{syncRoute(okRoute(&called))}})
	if err == nil || !strings.Contains(err.Error(), "participant gate") {
		t.Fatalf("server.New = %v, want the missing gate named", err)
	}
}
