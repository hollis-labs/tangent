package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	manifest "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/pluginconfig"
	"github.com/hollis-labs/tangent/pkg/plugin"
)

type noCredentialFixture struct{}

func (noCredentialFixture) Get(context.Context, string) (string, error) {
	return "", pluginconfig.ErrSecret
}
func (noCredentialFixture) Set(context.Context, string, string) error { return pluginconfig.ErrSecret }
func (noCredentialFixture) Delete(context.Context, string) error      { return pluginconfig.ErrSecret }

func TestDisabledReviewedManifestIsConfigurableWithoutSpawn(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "tangent.plugin.echo")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "bin", "runme")
	build := exec.Command("go", "build", "-p", "2", "-o", binary, "../pluginhost/testdata/echoplugin") //nolint:gosec // Fixed test fixture and test-owned output path.
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v %s", err, out)
	}
	writeEchoManifest(t, dir, binary)
	raw, err := os.ReadFile(filepath.Join(dir, plugin.ManifestName)) // #nosec G304 -- fixed manifest name beneath this test's owned fixture directory.
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := plugin.DecodeManifest(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	decoded.Config = manifest.Config{Fields: map[string]manifest.Field{"channel": {Type: "string", Default: "initial"}}}
	var encoded bytes.Buffer
	if err = plugin.EncodeManifest(&encoded, decoded.Manifest, decoded.Bindings); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, plugin.ManifestName), encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(root, ".state"), 0700); err != nil {
		t.Fatal(err)
	}
	intent, _ := json.Marshal(map[string]bool{"tangent.plugin.echo": false})
	if err = os.WriteFile(filepath.Join(root, ".state", "enabled.json"), intent, 0600); err != nil {
		t.Fatal(err)
	}
	scope := pluginconfig.Scope{Kind: "client", ID: "fixture"}
	store, err := pluginconfig.Open(ctx, filepath.Join(root, ".state", "config"), noCredentialFixture{}, []pluginconfig.Scope{scope})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := envelope.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	host, err := LoadInstalled(ctx, slog.New(slog.DiscardHandler), svc, root, false, WithConfigStore(store))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.UnloadAll(); _ = store.Close() })
	if _, loaded := host.GetPlugin("tangent.plugin.echo"); loaded {
		t.Fatal("disabled entry spawned")
	}
	snapshot, err := store.Read(ctx, "tangent.plugin.echo", scope)
	if err != nil || snapshot.Values["channel"].Value != "initial" {
		t.Fatal("reviewed schema not available", err)
	}
	changed, validation, err := store.Save(ctx, "tangent.plugin.echo", scope, snapshot.Revision, pluginconfig.Changes{Set: map[string]any{"channel": "next"}})
	if err != nil || !validation.Valid || !changed.PendingRestart {
		t.Fatal("disabled settings save refused", err)
	}
	if err = host.ApplyConfig(ctx, "tangent.plugin.echo", changed.Revision); !errors.Is(err, pluginconfig.ErrRefused) {
		t.Fatal("settings apply implicitly enabled plugin", err)
	}
}
