package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	manifest "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/tangent/internal/pluginintent"
	public "github.com/hollis-labs/tangent/pkg/plugin"
)

func cliPluginFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(executable) // #nosec G304 -- actual native test executable, never executed as a plugin.
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "bin", "runme"), payload, 0700); err != nil { // #nosec G306 G703 -- native executable bit required in this test-owned bundle.
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	files := []manifest.ArtifactFile{{Path: "bin/runme", SHA256: hex.EncodeToString(sum[:]), Executable: true}}
	digest, err := manifest.TreeDigest(files)
	if err != nil {
		t.Fatal(err)
	}
	m := manifest.Manifest{SchemaVersion: 2, ID: "fixture.plugin", Name: "Fixture", Version: "1.0.0", Protocol: 2, Runtime: "subprocess",
		Server: manifest.Server{Runtime: "binary", Entry: "bin/runme", Engines: map[string]manifest.HostRange{"binary": {Min: "1.0.0"}}},
		Hosts:  map[string]manifest.HostRange{"tangent": {Min: "1.0.0"}}, Artifact: manifest.Artifact{Files: files, TreeSHA256: digest}}
	var out bytes.Buffer
	if err = public.EncodeManifest(&out, m, public.TangentExtension{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, public.ManifestName), out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPluginInstallDefaultOffAndManualIntentSurvivesUpgrade(t *testing.T) {
	root, source := t.TempDir(), cliPluginFixture(t)
	if code := installPlugin([]string{source}, root); code != 0 {
		t.Fatal(code)
	}
	intent, err := pluginintent.Read(pluginintent.Path(root))
	if err != nil || intent["fixture.plugin"] {
		t.Fatal("install implicitly enabled", intent, err)
	}
	if code := setPluginIntent([]string{"unknown"}, root, true); code == 0 {
		t.Fatal("enabled an unknown installed ID")
	}
	if code := setPluginIntent([]string{"fixture.plugin"}, root, true); code != 0 {
		t.Fatal(code)
	}
	if code := installPlugin([]string{source}, root); code != 0 {
		t.Fatal(code)
	}
	intent, err = pluginintent.Read(pluginintent.Path(root))
	if err != nil || !intent["fixture.plugin"] {
		t.Fatal("upgrade silently disabled", err)
	}
	if code := setPluginIntent([]string{"fixture.plugin"}, root, false); code != 0 {
		t.Fatal(code)
	}
	if code := installPlugin([]string{source}, root); code != 0 {
		t.Fatal(code)
	}
	intent, err = pluginintent.Read(pluginintent.Path(root))
	if err != nil || intent["fixture.plugin"] {
		t.Fatal("upgrade silently enabled", err)
	}
	if code := installPlugin([]string{"--enable", source}, root); code != 0 {
		t.Fatal(code)
	}
	intent, err = pluginintent.Read(pluginintent.Path(root))
	if err != nil || !intent["fixture.plugin"] {
		t.Fatal("explicit install opt-in ineffective", err)
	}
}

func TestInstallConflictingFlagsAndIntentFailureDoNotEraseExistingDecisions(t *testing.T) {
	root, source := t.TempDir(), cliPluginFixture(t)
	if code := installPlugin([]string{"--enable", "--disable", source}, root); code != 2 {
		t.Fatal(code)
	}
	if _, err := os.Stat(filepath.Join(root, "fixture.plugin")); !os.IsNotExist(err) {
		t.Fatal("invalid command installed", err)
	}
	if _, err := pluginintent.Set(context.Background(), pluginintent.Path(root), "existing", true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pluginintent.Path(root)+".lock", []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	// A malformed boolean store is a refusal, never an excuse to reset it.
	if err := os.WriteFile(pluginintent.Path(root), []byte(`{"existing":"malformed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := installPlugin([]string{"--enable", source}, root); code != 1 {
		t.Fatal("intent failure hidden", code)
	}
	stored, err := os.ReadFile(pluginintent.Path(root))
	if err != nil || string(stored) != `{"existing":"malformed"}` {
		t.Fatal("existing state erased", err)
	}
}

func TestPluginListShowsOfflineIntentWithoutClaimingRuntime(t *testing.T) {
	root, source := t.TempDir(), cliPluginFixture(t)
	if code := installPlugin([]string{source}, root); code != 0 {
		t.Fatal(code)
	}
	var out, failures bytes.Buffer
	if code := listPluginsTo(root, &out, &failures); code != 0 {
		t.Fatal(code, failures.String())
	}
	if !strings.Contains(out.String(), "NEXT START") || !strings.Contains(out.String(), "false") {
		t.Fatal("default-off intent invisible", out.String())
	}
	if code := setPluginIntent([]string{"fixture.plugin"}, root, true); code != 0 {
		t.Fatal(code)
	}
	out.Reset()
	if code := listPluginsTo(root, &out, &failures); code != 0 {
		t.Fatal(code, failures.String())
	}
	if !strings.Contains(out.String(), "true") || strings.Contains(out.String(), "running") {
		t.Fatal("offline list invented runtime or lost intent", out.String())
	}
}
