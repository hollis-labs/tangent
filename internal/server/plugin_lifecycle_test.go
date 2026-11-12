package server_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	sdk "github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"

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
		return subprocess.HTTPResponse{Status: http.StatusOK, Body: []byte(p.marker)}, nil
	}))
}

// A route a plugin contributes while the server runs, and the enable, disable and reload actions
// that change it, keep the guards: the actions decide, so only the owner's session may take them,
// and the route a plugin serves is the owner's and nobody else's.
func TestRuntimePluginRoutesAndManagementKeepTheirGuards(t *testing.T) {
	host, err := pluginhost.New(context.Background(), nil, newEnvelope(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.UnloadAll() })
	if err = host.ConfigureIntent(filepath.Join(t.TempDir(), "intent.json")); err != nil {
		t.Fatal(err)
	}
	if err = host.RegisterFactory("live-route", func() (sdk.Plugin, error) { return &liveRoutePlugin{marker: "replacement"}, nil }); err != nil {
		t.Fatal(err)
	}
	app := startPluginApp(t, func(cfg *server.Config) { cfg.PluginHost = host })
	call := func(path, caller string) int {
		t.Helper()
		res, _ := app.do(t, http.MethodPost, path, caller, "", nil)
		return res.StatusCode
	}
	const enable, disable, reload = "/api/plugin-management/live-route/enable", "/api/plugin-management/live-route/disable", "/api/plugin-management/live-route/reload"

	for name, caller := range map[string]string{"no credential": "", "an agent key": callerAgent} {
		if got := call(enable, caller); got != http.StatusForbidden {
			t.Errorf("enable with %s = %d, want 403", name, got)
		}
	}
	if got := call(enable, callerOwner); got != http.StatusOK {
		t.Fatalf("enable = %d", got)
	}
	for name, caller := range map[string]string{"no credential": "", "an agent key": callerAgent} {
		if got := call(syncPath, caller); got != http.StatusForbidden {
			t.Errorf("the plugin's live route with %s = %d, want 403", name, got)
		}
	}
	if got := call(syncPath, callerOwner); got != http.StatusOK {
		t.Fatalf("the plugin's live route = %d", got)
	}
	if got := call(disable, callerOwner); got != http.StatusOK {
		t.Fatalf("disable = %d", got)
	}
	if got := call(syncPath, callerOwner); got != http.StatusNotFound {
		t.Fatalf("the route of a disabled plugin = %d, want 404", got)
	}
	if got := call(enable, callerOwner); got != http.StatusOK {
		t.Fatalf("re-enable = %d", got)
	}
	if got := call(reload, callerOwner); got != http.StatusOK {
		t.Fatalf("reload = %d", got)
	}
	if got := call(syncPath, callerOwner); got != http.StatusOK {
		t.Fatalf("the replacement's route = %d", got)
	}
}
