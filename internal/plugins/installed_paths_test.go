package plugins

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/pluginhost"
)

func TestRelativeInstallRootProducesAbsoluteChildDirectories(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tangent.plugin.echo")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "runme")
	build := exec.Command("go", "build", "-o", binary, "../pluginhost/testdata/echoplugin") //nolint:gosec // test-owned binary and literal fixture source
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	manifest := "id: tangent.plugin.echo\nname: Echo\nversion: 0.1.0\nprotocol: 2\nentrypoint: runme\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, root)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = extensions.RegisterAll(svc); err != nil {
		t.Fatal(err)
	}
	host, err := LoadInstalled(context.Background(), slog.New(slog.DiscardHandler), svc, relative, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.UnloadAll() })
	loaded, ok := host.GetPlugin("tangent.plugin.echo")
	if !ok {
		t.Fatalf("relative root failed: %+v", host.Inventory(context.Background()))
	}
	result, err := loaded.(*pluginhost.ChildPlugin).MCPCallTool(context.Background(), subprocess.MCPCallRequest{ToolName: "tangent.echo"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		DataDir string `json:"data_dir"`
		PWD     string `json:"pwd"`
	}
	if err := json.Unmarshal(result.Content, &got); err != nil {
		t.Fatal(err)
	}
	if got.DataDir != filepath.Join(dir, "data") || got.PWD != dir {
		t.Fatalf("relative install root leaked into child: %+v", got)
	}
}
