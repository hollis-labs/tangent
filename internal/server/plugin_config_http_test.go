package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk"
	manifest "github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/manifest"

	"github.com/hollis-labs/tangent/internal/pluginconfig"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	"github.com/hollis-labs/tangent/internal/server"
)

type browserConfigKeys struct{ values map[string]string }

func (k *browserConfigKeys) Get(_ context.Context, key string) (string, error) {
	return k.values[key], nil
}

func (k *browserConfigKeys) Set(_ context.Context, key, value string) error {
	k.values[key] = value
	return nil
}

func (k *browserConfigKeys) Delete(_ context.Context, key string) error {
	delete(k.values, key)
	return nil
}

// Plugin configuration over HTTP: the guards (only the owner's session reads or writes it, from
// the page's own origin), compare-and-swap on the revision, a secret that is only ever reported as
// present, and an apply that checks the saved revision.
func TestPluginConfigHTTPGuardsCASSecretPresenceAndApply(t *testing.T) {
	ctx := context.Background()
	host, err := pluginhost.New(ctx, nil, newEnvelope(t))
	if err != nil {
		t.Fatal(err)
	}
	scope := pluginconfig.Scope{Kind: "client", ID: "test-owner"}
	store, err := pluginconfig.Open(ctx, filepath.Join(t.TempDir(), "config"), &browserConfigKeys{values: map[string]string{}}, []pluginconfig.Scope{scope})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.UnloadAll(); _ = store.Close() })
	if err = store.Register(ctx, "live-route", manifest.Config{
		Fields:  map[string]manifest.Field{"channel": {Type: "string", Default: "initial"}},
		Secrets: map[string]manifest.Secret{"token": {}},
	}); err != nil {
		t.Fatal(err)
	}
	if err = host.ConfigureConfig(store); err != nil {
		t.Fatal(err)
	}
	if err = host.RegisterFactory("live-route", func() (sdk.Plugin, error) { return &liveRoutePlugin{}, nil }); err != nil {
		t.Fatal(err)
	}
	if err = host.SetEnabled(ctx, "live-route", true); err != nil {
		t.Fatal(err)
	}
	app := startPluginApp(t, func(cfg *server.Config) { cfg.PluginHost = host; cfg.PluginConfig = store })

	secret := "browser-" + "fixture-" + "value"
	path := "/api/plugin-management/live-route/config"
	query := "?scope_kind=client&scope_id=test-owner"
	request := func(method, path, body, caller string, headers map[string]string) (int, []byte) {
		t.Helper()
		res, raw := app.do(t, method, path, caller, body, headers)
		return res.StatusCode, raw
	}

	for name, caller := range map[string]string{"no credential": "", "an agent key": callerAgent} {
		if code, _ := request("GET", path+query, "", caller, nil); code != http.StatusForbidden {
			t.Errorf("a read with %s = %d, want 403", name, code)
		}
		if code, _ := request("POST", path+"/save", `{}`, caller, nil); code != http.StatusForbidden {
			t.Errorf("a write with %s = %d, want 403", name, code)
		}
	}
	if code, _ := request("POST", path+"/save", `{}`, callerOwner, map[string]string{"Origin": "https://foreign.invalid"}); code != http.StatusForbidden {
		t.Fatalf("a write from a foreign origin = %d, want 403", code)
	}
	code, raw := request("GET", path+query, "", callerOwner, nil)
	if code != http.StatusOK {
		t.Fatalf("read = %d", code)
	}
	var before pluginconfig.Snapshot
	if err = json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"scope": scope, "revision": before.Revision, "changes": pluginconfig.Changes{Set: map[string]any{"channel": "next", "token": secret}, Unset: []string{}}})
	code, raw = request("POST", path+"/save", string(payload), callerOwner, nil)
	if code != http.StatusOK || strings.Contains(string(raw), secret) {
		t.Fatalf("save refused or disclosed the secret: status=%d disclosed=%t", code, strings.Contains(string(raw), secret))
	}
	var saved pluginconfig.Snapshot
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.PendingRestart || saved.Values["token"].SecretPresent == nil || !*saved.Values["token"].SecretPresent {
		t.Fatal("the save did not report the secret as present and the change as pending")
	}
	if _, raw = request("GET", path+query, "", callerOwner, nil); strings.Contains(string(raw), secret) {
		t.Fatal("a later read disclosed the secret")
	}
	if code, _ = request("POST", path+"/save", string(payload), callerOwner, nil); code != http.StatusConflict {
		t.Fatalf("a save against a stale revision = %d, want 409", code)
	}
	payload, _ = json.Marshal(map[string]any{"scope": scope, "revision": saved.Revision, "changes": pluginconfig.Changes{Set: map[string]any{}, Unset: []string{}}})
	code, raw = request("POST", path+"/apply", string(payload), callerOwner, nil)
	if code != http.StatusOK {
		t.Fatalf("apply = %d", code)
	}
	var applied pluginconfig.Snapshot
	if err = json.Unmarshal(raw, &applied); err != nil {
		t.Fatal(err)
	}
	if applied.PendingRestart || strings.Contains(string(raw), secret) {
		t.Fatal("applying left the change pending or disclosed the secret")
	}
	if code, _ = request("GET", path+"?scope_kind=project&scope_id=unowned", "", callerOwner, nil); code == http.StatusOK {
		t.Fatal("the browser named a scope the host does not own")
	}
}
