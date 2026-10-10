package server_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	sdk "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	"github.com/hollis-labs/tangent/internal/server"
)

type liveRoutePlugin struct{ marker string }

func (*liveRoutePlugin) ID() string               { return "live-route" }
func (*liveRoutePlugin) Name() string             { return "Route fixture" }
func (*liveRoutePlugin) Version() string          { return "1" }
func (*liveRoutePlugin) Description() string      { return "Fixture" }
func (*liveRoutePlugin) Dependencies() []string   { return nil }
func (*liveRoutePlugin) Status() sdk.PluginStatus { return sdk.PluginStatus{} }
func (*liveRoutePlugin) Unload() error            { return nil }
func (p *liveRoutePlugin) Load(h sdk.Host) error {
	return h.(interface {
		RegisterHTTPRoute(pluginhost.HTTPRoute) error
	}).RegisterHTTPRoute(syncRoute(func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
		return subprocess.HTTPResponse{Status: 200, Body: []byte(p.marker)}, nil
	}))
}

func TestRuntimePluginRoutesAndManagementRetainParticipantGuards(t *testing.T) {
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	host, err := pluginhost.New(context.Background(), nil, svc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.UnloadAll() })
	if err = host.ConfigureIntent(filepath.Join(t.TempDir(), "intent.json")); err != nil {
		t.Fatal(err)
	}
	factory := func() (sdk.Plugin, error) { return &liveRoutePlugin{marker: "replacement"}, nil }
	if err = host.RegisterFactory("live-route", factory); err != nil {
		t.Fatal(err)
	}
	app := startGuardedAppWith(t, func(cfg *server.Config) { cfg.PluginHost = host })
	cookie := app.openDocument(t, "/")
	request := func(path string, credential bool) int {
		t.Helper()
		req, e := http.NewRequest(http.MethodPost, app.baseURL+path, nil)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Origin", app.baseURL)
		if credential {
			req.AddCookie(cookie)
		}
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	if got := request("/api/plugin-management/live-route/enable", false); got == 200 {
		t.Fatal("unguarded lifecycle action")
	}
	if got := request("/api/plugin-management/live-route/enable", true); got != 200 {
		t.Fatalf("enable:%d", got)
	}
	if got := request("/api/plugins/stub/sync", false); got == 200 {
		t.Fatal("dynamic route skipped participant guard")
	}
	if got := request("/api/plugins/stub/sync", true); got != 200 {
		t.Fatalf("live route:%d", got)
	}
	if got := request("/api/plugin-management/live-route/disable", true); got != 200 {
		t.Fatalf("disable:%d", got)
	}
	if got := request("/api/plugins/stub/sync", true); got != 404 {
		t.Fatalf("removed route:%d", got)
	}
	if got := request("/api/plugin-management/live-route/enable", true); got != 200 {
		t.Fatalf("re-enable:%d", got)
	}
	if got := request("/api/plugin-management/live-route/reload", true); got != 200 {
		t.Fatalf("reload:%d", got)
	}
	if got := request("/api/plugins/stub/sync", true); got != 200 {
		t.Fatalf("replacement route:%d", got)
	}
}
