package plugins

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
)

func TestInstalledTruncatedCredentialIsAbsentFromTangentLog(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tangent.plugin.echo")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "runme")
	build := exec.Command("go", "build", "-p", "2", "-o", binary, "../pluginhost/testdata/echoplugin") //nolint:gosec // test-owned output and fixed source
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	manifest := "id: tangent.plugin.echo\nname: Echo\nversion: 0.1.0\nprotocol: 2\nentrypoint: runme\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o600); err != nil {
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
