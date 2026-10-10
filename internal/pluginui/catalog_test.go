package pluginui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/registry"
)

func fixture(t *testing.T, isolation string) (manifest.Manifest, string, []Declaration) {
	t.Helper()
	dir := t.TempDir()
	payloads := map[string][]byte{"bin/plugin": []byte("owned test executable"), "ui/index.js": []byte("export function Panel(){return null};export function Widget(){return null}"), "ui/style.css": []byte(".owned-panel{color:var(--fg)}")}
	var files []manifest.ArtifactFile
	for _, name := range []string{"bin/plugin", "ui/index.js", "ui/style.css"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if name == "bin/plugin" {
			mode = 0700
		}
		if err := os.WriteFile(filepath.Join(dir, name), payloads[name], mode); err != nil {
			t.Fatal(err)
		}
		files = append(files, manifest.ArtifactFile{Path: name, SHA256: strings.TrimPrefix(registry.BundleDigest(payloads[name]), "sha256:"), Executable: name == "bin/plugin"})
	}
	tree, err := manifest.TreeDigest(files)
	if err != nil {
		t.Fatal(err)
	}
	m := manifest.Manifest{SchemaVersion: 2, ID: "sample.panel", Name: "Owned sample", Version: "1.0.0", Protocol: 2, Runtime: "subprocess", Server: manifest.Server{Runtime: "binary", Entry: "bin/plugin", Engines: map[string]manifest.HostRange{"binary": {Min: "1.0.0"}}}, UI: &manifest.UI{Bundle: "ui/index.js", Stylesheet: "ui/style.css", Isolation: isolation}, Hosts: map[string]manifest.HostRange{"tangent": {Min: "1.0.0"}}, Artifact: manifest.Artifact{Files: files, TreeSHA256: tree}}
	var raw bytes.Buffer
	if err := manifest.Encode(&raw, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return m, dir, []Declaration{{Key: "panel", Kind: "panel", Export: "Panel", Region: "right", Title: "Sample panel", Priority: 10}, {Key: "widget", Kind: "widget", Export: "Widget", Region: "right", Title: "Sample widget", Priority: 0}}
}

func TestImmutablePublicationAndExactOwnerWithdrawal(t *testing.T) {
	m, dir, decls := fixture(t, "main-origin")
	prepared, err := Prepare(m, dir, decls)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := New("actual-host-instance")
	if err != nil {
		t.Fatal(err)
	}
	var live atomic.Bool
	live.Store(true)
	if err := catalog.Publish(prepared, "generation-1", false, live.Load); err == nil {
		t.Fatal("ambient main-origin authority accepted implicitly")
	}
	if err := catalog.Publish(prepared, "generation-1", true, live.Load); err != nil {
		t.Fatal(err)
	}
	initial := catalog.Response()
	if err := initial.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(initial.Contributions["panel"]) != 1 || len(initial.Contributions["widget"]) != 1 {
		t.Fatal("actual declarations absent")
	}
	digest := strings.TrimPrefix(initial.Plugins[m.ID].BundleVersion, "sha256:")
	original, ok := catalog.Artifact(m.ID, "generation-1", digest, "bundle.js")
	if !ok {
		t.Fatal("admitted artifact absent")
	}
	if err := os.WriteFile(filepath.Join(dir, m.UI.Bundle), []byte("changed mutable installation"), 0600); err != nil {
		t.Fatal(err)
	}
	delivered, ok := catalog.Artifact(m.ID, "generation-1", digest, "bundle.js")
	if !ok || !bytes.Equal(delivered, original) {
		t.Fatal("mutable installation changed admitted bytes")
	}
	delivered[0] = 'X'
	again, _ := catalog.Artifact(m.ID, "generation-1", digest, "bundle.js")
	if !bytes.Equal(again, original) {
		t.Fatal("caller mutated custody")
	}
	if catalog.Response().Revision != initial.Revision {
		t.Fatal("unchanged snapshot revision moved")
	}
	live.Store(false)
	ended := catalog.Response()
	if len(ended.Plugins) != 0 || ended.Revision <= initial.Revision {
		t.Fatal("ended owner remained active")
	}
	if _, ok := catalog.Artifact(m.ID, "generation-1", digest, "bundle.js"); ok {
		t.Fatal("ended owner artifact delivered")
	}
	catalog.Withdraw(m.ID, "generation-1")
	live.Store(true)
	if err := catalog.Publish(prepared, "generation-2", true, live.Load); err != nil {
		t.Fatal(err)
	}
	catalog.Withdraw(m.ID, "generation-1")
	if catalog.Response().Plugins[m.ID].OwnerGeneration != "generation-2" {
		t.Fatal("late old cleanup removed replacement")
	}
}

func TestIntegrityAndIsolationRefusals(t *testing.T) {
	t.Run("modified bytes", func(t *testing.T) {
		m, dir, ds := fixture(t, "main-origin")
		if err := os.WriteFile(filepath.Join(dir, m.UI.Bundle), []byte("different"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Prepare(m, dir, ds); err == nil {
			t.Fatal("digest mismatch accepted")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		m, dir, ds := fixture(t, "main-origin")
		name := filepath.Join(dir, m.UI.Bundle)
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../bin/plugin", name); err != nil {
			t.Fatal(err)
		}
		if _, err := Prepare(m, dir, ds); err == nil {
			t.Fatal("symlink accepted")
		}
	})
	t.Run("frame never parent fallback", func(t *testing.T) {
		m, dir, ds := fixture(t, "sandboxed-frame")
		p, err := Prepare(m, dir, ds)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := New("host")
		if c.Publish(p, "generation", true, func() bool { return true }) == nil {
			t.Fatal("frame substituted parent execution")
		}
	})
	t.Run("invalid declarations", func(t *testing.T) {
		m, dir, ds := fixture(t, "main-origin")
		ds[1].Key = ds[0].Key
		if _, err := Prepare(m, dir, ds); err == nil {
			t.Fatal("collision accepted")
		}
		ds[1].Key = "other"
		ds[1].Region = "unknown"
		if _, err := Prepare(m, dir, ds); err == nil {
			t.Fatal("unknown region accepted")
		}
	})
}

func TestRegistryMetadataIsDetached(t *testing.T) {
	m, dir, ds := fixture(t, "main-origin")
	p, err := Prepare(m, dir, ds)
	if err != nil {
		t.Fatal(err)
	}
	ds[0].Title = "changed declaration"
	c, _ := New("host")
	if err := c.Publish(p, "generation", true, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	first := c.Response()
	first.Contributions["panel"][registry.QualifiedKey(m.ID, "panel")] = registry.Contribution{}
	next := c.Response()
	var meta struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(next.Contributions["panel"][registry.QualifiedKey(m.ID, "panel")].Metadata, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Title != "Sample panel" {
		t.Fatal("caller changed reviewed metadata")
	}
}
