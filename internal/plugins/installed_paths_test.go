package plugins

import (
	"context"
	"encoding/json"
	gobuild "go/build"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	tangentplugin "github.com/hollis-labs/tangent/pkg/plugin"
)

func TestRelativeInstallRootProducesAbsoluteChildDirectories(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tangent.plugin.echo")
	t.Setenv("GOPATH", gobuild.Default.GOPATH)
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "bin", "runme")
	build := exec.Command("go", "build", "-o", binary, "../pluginhost/testdata/echoplugin") //nolint:gosec // test-owned binary and literal fixture source
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	writeEchoManifest(t, dir, binary)
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
	if got.DataDir != filepath.Join(root, ".state", "tangent.plugin.echo", "data") || !strings.HasPrefix(got.PWD, filepath.Join(root, ".runtime")+string(filepath.Separator)) {
		t.Fatalf("relative install root leaked into child: %+v", got)
	}
}

func TestKindReferenceRequiresAvailableApprovedDefinition(t *testing.T) {
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	refs := []tangentplugin.KindRef{{Kind: "tangent.app-board", Package: "tangent.appboard", Version: "0.3"}}
	if err := resolveKinds(svc, refs); err == nil {
		t.Fatal("unregistered host kind was treated as available")
	}
	if err := extensions.RegisterAll(svc); err != nil {
		t.Fatal(err)
	}
	if err := resolveKinds(svc, refs); err != nil {
		t.Fatal(err)
	}
	refs[0].Version = "0.2"
	if err := resolveKinds(svc, refs); err == nil {
		t.Fatal("wrong approved definition version accepted")
	}
}
