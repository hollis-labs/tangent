package plugins

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/pluginhost"
)

func TestRawDroppedPluginDefaultOffAndExplicitIntentAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "tangent.plugin.echo")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "bin", "runme")
	build := exec.Command("go", "build", "-p", "2", "-o", binary, "../pluginhost/testdata/echoplugin") //nolint:gosec // Literal source and test-owned binary.
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	writeEchoManifest(t, dir, binary)
	boot := func() *pluginhost.Host {
		t.Helper()
		svc, err := envelope.New(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = extensions.RegisterAll(svc); err != nil {
			t.Fatal(err)
		}
		h, err := LoadInstalled(ctx, slog.New(slog.DiscardHandler), svc, root, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.UnloadAll() })
		return h
	}
	first := boot()
	if first.DesiredEnabled("tangent.plugin.echo") {
		t.Fatal("raw drop acquired intent")
	}
	if p, ok := first.GetPlugin("tangent.plugin.echo"); ok || p != nil {
		t.Fatal("raw drop spawned")
	}
	record := first.Inventory(ctx).Plugins[0]
	if record.Enabled || record.Loaded || record.State != "disabled" {
		t.Fatal("raw drop state", record)
	}
	if err := first.SetEnabled(ctx, "tangent.plugin.echo", true); err != nil {
		t.Fatal(err)
	}
	if p, ok := first.GetPlugin("tangent.plugin.echo"); !ok {
		t.Fatal("opt-in did not load")
	} else {
		if _, err := p.(*pluginhost.ChildPlugin).MCPCallTool(ctx, subprocess.MCPCallRequest{ToolName: "tangent.echo"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.UnloadAll(); err != nil {
		t.Fatal(err)
	}
	second := boot()
	if _, ok := second.GetPlugin("tangent.plugin.echo"); !ok {
		t.Fatal("existing enabled intent lost across boot")
	}
	if err := second.SetEnabled(ctx, "tangent.plugin.echo", false); err != nil {
		t.Fatal(err)
	}
	if err := second.UnloadAll(); err != nil {
		t.Fatal(err)
	}
	third := boot()
	if _, ok := third.GetPlugin("tangent.plugin.echo"); ok {
		t.Fatal("disabled intent lost across boot")
	}
	if err := third.Reload(ctx, "tangent.plugin.echo"); err == nil {
		t.Fatal("disabled reload enabled")
	}
	if err := third.SetEnabled(ctx, "tangent.plugin.echo", true); err != nil {
		t.Fatal(err)
	}
	before, _ := third.GetPlugin("tangent.plugin.echo")
	if err := third.Reload(ctx, "tangent.plugin.echo"); err != nil {
		t.Fatal(err)
	}
	after, ok := third.GetPlugin("tangent.plugin.echo")
	if !ok || before == after {
		t.Fatal("reload did not replace owner")
	}
}
