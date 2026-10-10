package plugins

import (
	"bytes"
	"context"
	gobuild "go/build"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/pluginintent"
)

func TestInstalledTruncatedCredentialIsAbsentFromTangentLog(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tangent.plugin.echo")
	t.Setenv("GOPATH", gobuild.Default.GOPATH)
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "bin", "runme")
	build := exec.Command("go", "build", "-p", "2", "-o", binary, "../pluginhost/testdata/echoplugin") //nolint:gosec // test-owned output and fixed source
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	writeEchoManifest(t, dir, binary)
	if _, err := pluginintent.Set(context.Background(), pluginintent.Path(root), "tangent.plugin.echo", true); err != nil {
		t.Fatal(err)
	}
	raw := strings.Repeat("p", 501) + "PASSWORD=S4CRCUT\nFINAL CAUSE: failure\n"
	raw += strings.Repeat("p", 4600-len(raw))
	t.Setenv("ECHO_PLUGIN_STDERR", raw)
	t.Setenv("ECHO_PLUGIN_INIT_ERROR", "1")
	service, err := envelope.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = extensions.RegisterAll(service); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))
	host, err := LoadInstalled(context.Background(), logger, service, root, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.UnloadAll() })
	text := log.String()
	if !strings.Contains(text, "installed plugin failed to load") || !strings.Contains(text, "FINAL CAUSE") || strings.Contains(text, "S4CRCUT") || strings.Contains(text, "WORD=") {
		t.Fatalf("tangent log: %s", text)
	}
}
