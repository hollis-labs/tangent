package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	manifest "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/tangent/internal/envelope"
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

func TestPluginConfigHTTPGuardsCASSecretPresenceAndApply(t *testing.T) {
	ctx := context.Background()
	svc, err := envelope.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	host, err := pluginhost.New(ctx, nil, svc)
	if err != nil {
		t.Fatal(err)
	}
	scope := pluginconfig.Scope{Kind: "client", ID: "test-owner"}
	store, err := pluginconfig.Open(ctx, filepath.Join(t.TempDir(), "config"), &browserConfigKeys{values: map[string]string{}}, []pluginconfig.Scope{scope})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.UnloadAll(); _ = store.Close() })
	if err = store.Register(ctx, "live-route", manifest.Config{Fields: map[string]manifest.Field{"channel": {Type: "string", Default: "initial"}}, Secrets: map[string]manifest.Secret{"token": {}}}); err != nil {
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
	app := startGuardedAppWith(t, func(cfg *server.Config) { cfg.PluginHost = host; cfg.PluginConfig = store })
	cookie := app.openDocument(t, "/")
	request := func(method, path, body, origin string, credential bool) (int, []byte) {
		t.Helper()
		req, e := http.NewRequest(method, app.baseURL+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if credential {
			req.AddCookie(cookie)
		}
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		return res.StatusCode, raw
	}
	path := "/api/plugin-management/live-route/config"
	query := "?scope_kind=client&scope_id=test-owner"
	if code, _ := request("GET", path+query, "", "", false); code != 403 {
		t.Fatalf("unguarded read: %d", code)
	}
	if code, _ := request("POST", path+"/save", `{}`, "https://foreign.invalid", true); code != 403 {
		t.Fatalf("foreign update: %d", code)
	}
	code, raw := request("GET", path+query, "", "", true)
	if code != 200 {
		t.Fatalf("read: %d", code)
	}
	var before pluginconfig.Snapshot
	if err = json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"scope": scope, "revision": before.Revision, "changes": pluginconfig.Changes{Set: map[string]any{"channel": "next", "token": "synthetic-browser-key"}, Unset: []string{}}})
	if code, _ = request("POST", path+"/save", string(payload), app.baseURL, false); code != 403 {
		t.Fatalf("unguarded write: %d", code)
	}
	code, raw = request("POST", path+"/save", string(payload), app.baseURL, true)
	if code != 200 || strings.Contains(string(raw), "synthetic-browser-key") {
		t.Fatalf("save refused or disclosed secret: status=%d safe=%s", code, strings.ReplaceAll(string(raw), "synthetic-browser-key", "[redacted]"))
	}
	var saved pluginconfig.Snapshot
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.PendingRestart || saved.Values["token"].SecretPresent == nil || !*saved.Values["token"].SecretPresent {
		t.Fatal("missing saved presence/pending")
	}
	if code, _ = request("POST", path+"/save", string(payload), app.baseURL, true); code != 409 {
		t.Fatalf("stale CAS: %d", code)
	}
	payload, _ = json.Marshal(map[string]any{"scope": scope, "revision": saved.Revision, "changes": pluginconfig.Changes{Set: map[string]any{}, Unset: []string{}}})
	code, raw = request("POST", path+"/apply", string(payload), app.baseURL, true)
	if code != 200 {
		t.Fatalf("apply: %d", code)
	}
	var applied pluginconfig.Snapshot
	if err = json.Unmarshal(raw, &applied); err != nil {
		t.Fatal(err)
	}
	if applied.PendingRestart || strings.Contains(string(raw), "synthetic-browser-key") {
		t.Fatal("activation did not preserve secret boundary")
	}
	if code, _ = request("GET", path+"?scope_kind=project&scope_id=unowned", "", "", true); code == 200 {
		t.Fatal("browser invented scope")
	}
}
