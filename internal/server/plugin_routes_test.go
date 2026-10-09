package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"

	"github.com/hollis-labs/tangent/internal/authz"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	"github.com/hollis-labs/tangent/internal/server"
)

// These tests hold CW-20260910-0030. The claim a plugin route makes is that a
// button in a room can do work with no agent turn *and* that it is not a side
// door around the guards every other browser route carries. Both halves need a
// real, fully wired process to check — the class of bug this is guarding
// against (CW-20260907-0084) was invisible to every test that used a fake
// service with no participant gate configured.

// pluginRouteFunc adapts a function to the SDK's HTTPHandler.
type pluginRouteFunc func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error)

func (f pluginRouteFunc) HTTPHandle(
	ctx context.Context, req subprocess.HTTPRequest,
) (subprocess.HTTPResponse, error) {
	return f(ctx, req)
}

func syncRoute(handler pluginRouteFunc) pluginhost.HTTPRoute {
	return pluginhost.HTTPRoute{
		Method:     http.MethodPost,
		Path:       "/api/plugins/stub/sync",
		Capability: authz.Draft,
		Handler:    handler,
	}
}

// TestPluginRouteWorksForARealParticipantSession is the requirement itself,
// stated as Chrispian did: press the button, the work happens, no agent turn.
func TestPluginRouteWorksForARealParticipantSession(t *testing.T) {
	t.Parallel()
	var seen subprocess.HTTPRequest
	app := startGuardedAppWith(t, func(cfg *server.Config) {
		cfg.PluginRoutes = []pluginhost.HTTPRoute{syncRoute(
			func(_ context.Context, req subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
				seen = req
				return subprocess.HTTPResponse{
					Status: http.StatusOK,
					Body:   []byte(`{"applied":3}`),
				}, nil
			})}
	})

	cookie := app.openDocument(t, "/")
	if cookie == nil {
		t.Fatal("no participant session was minted")
	}

	request, err := http.NewRequest(http.MethodPost,
		app.baseURL+"/api/plugins/stub/sync?scope=active",
		strings.NewReader(`{"decisions":[]}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST plugin route: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/plugins/stub/sync with a minted session = %d, want 200",
			response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if strings.TrimSpace(string(body)) != `{"applied":3}` {
		t.Errorf("body = %q, want the plugin's own", body)
	}

	if seen.Method != http.MethodPost || seen.Path != "/api/plugins/stub/sync" {
		t.Errorf("plugin saw %s %s", seen.Method, seen.Path)
	}
	if seen.Query["scope"] != "active" {
		t.Errorf("plugin saw query %v, want the caller's", seen.Query)
	}
	if string(seen.Body) != `{"decisions":[]}` {
		t.Errorf("plugin saw body %q, want the caller's", seen.Body)
	}
	// The two things that must not cross the boundary: ADR 0004 §6.1 makes the
	// session id capability material, and a plugin holding the cookie could
	// replay a participant's authority against every other route on this
	// server.
	if seen.SessionID != "" {
		t.Errorf("plugin was handed session id %q; it must be empty", seen.SessionID)
	}
	for name := range seen.Headers {
		if strings.EqualFold(name, "Cookie") || strings.EqualFold(name, "Authorization") {
			t.Errorf("plugin was handed the %s header; it is the participant's credential", name)
		}
	}
	if seen.Headers["Content-Type"] != "application/json" {
		t.Errorf("plugin saw headers %v, want Content-Type among them", seen.Headers)
	}
}

// TestPluginRouteRequiresAParticipantSession: a plugin route is not a way to
// reach the process without one.
func TestPluginRouteRequiresAParticipantSession(t *testing.T) {
	t.Parallel()
	called := false
	app := startGuardedAppWith(t, func(cfg *server.Config) {
		cfg.PluginRoutes = []pluginhost.HTTPRoute{syncRoute(
			func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
				called = true
				return subprocess.HTTPResponse{Status: http.StatusOK}, nil
			})}
	})

	response := app.do(t, http.MethodPost, "/api/plugins/stub/sync", nil)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("POST a plugin route with no session = %d, want 403", response.StatusCode)
	}
	if called {
		t.Error("the plugin handler ran for an unauthenticated request")
	}
}

// TestPluginRouteIsOriginGuarded: the guard that stops a drive-by POST from
// any page the operator happens to have open covers plugin routes too, because
// they are registered through the same door.
func TestPluginRouteIsOriginGuarded(t *testing.T) {
	t.Parallel()
	called := false
	app := startGuardedAppWith(t, func(cfg *server.Config) {
		cfg.PluginRoutes = []pluginhost.HTTPRoute{syncRoute(
			func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
				called = true
				return subprocess.HTTPResponse{Status: http.StatusOK}, nil
			})}
	})
	cookie := app.openDocument(t, "/")
	if cookie == nil {
		t.Fatal("no participant session was minted")
	}

	crossSite := http.Header{}
	crossSite.Set("Sec-Fetch-Site", "cross-site")
	response := app.doWithHeaders(t, http.MethodPost, "/api/plugins/stub/sync", cookie, crossSite)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site POST to a plugin route = %d, want 403", response.StatusCode)
	}
	if called {
		t.Error("the plugin handler ran for a cross-site request")
	}
}

// TestPluginRouteAppearsInParticipantRoutes is what puts plugin routes inside
// TestEveryParticipantGuardedRouteUsesACapabilityParticipantsHold. A route the
// enumeration cannot see is a route no check covers.
func TestPluginRouteAppearsInParticipantRoutes(t *testing.T) {
	t.Parallel()
	app := startGuardedAppWith(t, func(cfg *server.Config) {
		cfg.PluginRoutes = []pluginhost.HTTPRoute{syncRoute(
			func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
				return subprocess.HTTPResponse{Status: http.StatusOK}, nil
			})}
	})
	for _, route := range app.server.ParticipantRoutes() {
		if route.Pattern == "POST /api/plugins/stub/sync" {
			if route.Capability != authz.Draft {
				t.Errorf("plugin route capability = %q, want the one it registered", route.Capability)
			}
			return
		}
	}
	t.Fatal("the plugin route is not in ParticipantRoutes(); no capability check covers it")
}

// TestPluginRouteFailureRendersARefusal: a plugin that errors or panics gives
// the SPA something to show, never a stack trace and never a dead process.
func TestPluginRouteFailureRendersARefusal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		handler pluginRouteFunc
		want    string
	}{
		{
			name: "an error is the plugin's own reason",
			handler: func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
				return subprocess.HTTPResponse{}, errPluginRefused
			},
			want: "torque is unreachable",
		},
		{
			name: "a panic is contained",
			handler: func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
				panic("a plugin defect")
			},
			want: "panicked",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			app := startGuardedAppWith(t, func(cfg *server.Config) {
				cfg.PluginRoutes = []pluginhost.HTTPRoute{syncRoute(test.handler)}
			})
			cookie := app.openDocument(t, "/")
			if cookie == nil {
				t.Fatal("no participant session was minted")
			}

			response := app.do(t, http.MethodPost, "/api/plugins/stub/sync", cookie)
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", response.StatusCode)
			}
			var refusal struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.NewDecoder(response.Body).Decode(&refusal); err != nil {
				t.Fatalf("decode refusal: %v", err)
			}
			if refusal.Code != "plugin_error" {
				t.Errorf("code = %q, want plugin_error", refusal.Code)
			}
			if !strings.Contains(refusal.Message, test.want) {
				t.Errorf("message = %q, want it to contain %q", refusal.Message, test.want)
			}

			// The process is still serving. A plugin defect is one route's
			// problem.
			healthy := app.do(t, http.MethodGet, "/api/hitl", cookie)
			defer func() { _ = healthy.Body.Close() }()
			if healthy.StatusCode != http.StatusOK {
				t.Errorf("GET /api/hitl after a plugin failure = %d, want 200", healthy.StatusCode)
			}
		})
	}
}

// TestPluginRouteCannotSetACookie is the outbound half of the credential
// boundary: a plugin that could set a cookie could mint or overwrite a
// participant session.
func TestPluginRouteCannotSetACookie(t *testing.T) {
	t.Parallel()
	app := startGuardedAppWith(t, func(cfg *server.Config) {
		cfg.PluginRoutes = []pluginhost.HTTPRoute{syncRoute(
			func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
				return subprocess.HTTPResponse{
					Status: http.StatusOK,
					Headers: map[string]string{
						"Set-Cookie":   "tangent_participant=forged; Path=/",
						"Content-Type": "application/json",
					},
					Body: []byte(`{}`),
				}, nil
			})}
	})
	cookie := app.openDocument(t, "/")
	if cookie == nil {
		t.Fatal("no participant session was minted")
	}

	response := app.do(t, http.MethodPost, "/api/plugins/stub/sync", cookie)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("Set-Cookie"); got != "" {
		t.Errorf("a plugin set a cookie: %q", got)
	}
	if got := response.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q; an allowed header should have survived", got)
	}
	if !strings.Contains(app.logs.String(), "plugin route response headers dropped") {
		t.Error("the dropped header was not logged; a plugin author would have no way to learn why")
	}
}

// TestPluginRouteBodyIsCapped: a button is not an upload endpoint.
func TestPluginRouteBodyIsCapped(t *testing.T) {
	t.Parallel()
	called := false
	app := startGuardedAppWith(t, func(cfg *server.Config) {
		cfg.PluginRoutes = []pluginhost.HTTPRoute{syncRoute(
			func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
				called = true
				return subprocess.HTTPResponse{Status: http.StatusOK}, nil
			})}
	})
	cookie := app.openDocument(t, "/")
	if cookie == nil {
		t.Fatal("no participant session was minted")
	}

	request, err := http.NewRequest(http.MethodPost, app.baseURL+"/api/plugins/stub/sync",
		strings.NewReader(strings.Repeat("x", 64*1024)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST oversized body: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", response.StatusCode)
	}
	if called {
		t.Error("an oversized body reached the plugin")
	}
}

// TestServerRefusesAPluginRouteNoParticipantCouldReach is the second check, at
// the layer that mounts. pluginhost already refuses this; a route that got past
// it would answer 403 forever and look like a plugin bug.
func TestServerRefusesAPluginRouteNoParticipantCouldReach(t *testing.T) {
	t.Parallel()
	unreachable := syncRoute(func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
		return subprocess.HTTPResponse{Status: http.StatusOK}, nil
	})
	unreachable.Capability = authz.Submit

	srv, err := server.New(server.Config{
		Envelope:     buildEnvelopeServiceForPluginRouteCheck(t),
		Participants: buildParticipantGateForPluginRouteCheck(t),
		PluginRoutes: []pluginhost.HTTPRoute{unreachable},
	})
	if err == nil {
		_ = srv
		t.Fatal("server.New accepted a plugin route gated on a capability no participant holds")
	}
	if !strings.Contains(err.Error(), "submit") {
		t.Errorf("server.New = %v, want the capability named", err)
	}
}

// TestServerRefusesPluginRoutesWithNoParticipantGate: the gate is what
// authorizes them, so mounting without one would publish them unguarded.
func TestServerRefusesPluginRoutesWithNoParticipantGate(t *testing.T) {
	t.Parallel()
	_, err := server.New(server.Config{
		Envelope: buildEnvelopeServiceForPluginRouteCheck(t),
		PluginRoutes: []pluginhost.HTTPRoute{syncRoute(
			func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
				return subprocess.HTTPResponse{Status: http.StatusOK}, nil
			})},
	})
	if err == nil {
		t.Fatal("server.New mounted plugin routes with no participant gate")
	}
	if !strings.Contains(err.Error(), "participant gate") {
		t.Errorf("server.New = %v, want the missing gate named", err)
	}
}

// errPluginRefused stands in for the shape a real app plugin fails with: the
// application it adapts is down, and every other Tangent surface keeps working.
var errPluginRefused = errors.New("torque is unreachable")

func buildEnvelopeServiceForPluginRouteCheck(t *testing.T) *envelope.Service {
	t.Helper()
	service, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	return service
}

func buildParticipantGateForPluginRouteCheck(t *testing.T) *participant.Gate {
	t.Helper()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "plugin-routes.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrateErr := tangentdb.RunMigrations(database); migrateErr != nil {
		t.Fatalf("run migrations: %v", migrateErr)
	}
	store, err := participant.NewStore(database)
	if err != nil {
		t.Fatalf("participant.NewStore: %v", err)
	}
	gate, err := participant.NewGate(store)
	if err != nil {
		t.Fatalf("participant.NewGate: %v", err)
	}
	return gate
}
