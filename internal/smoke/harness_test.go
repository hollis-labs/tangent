package smoke_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/smoke"
)

// The harness boots the *shipped binary*, not an in-process server.
//
// That is the whole point of the exercise. An in-process server assembled by a
// test is a surface the test chose; `./tangent` is the surface the deployment
// serves, wired by cmd/tangent, and the two have diverged before — the base
// tool surface without the durable substrate is 30, the shipped one is not.
// Deriving the reference by asking the binary means the expected tool count is
// never written down anywhere, so it cannot go stale.

const (
	// bootBudget bounds how long the shipped binary gets to answer liveness.
	// A first boot applies migrations against a fresh temp database, so it is
	// generous; exceeding it is a real failure, not a slow machine.
	bootBudget = 45 * time.Second

	// shutdownBudget bounds a graceful stop before the harness escalates.
	shutdownBudget = 10 * time.Second
)

var (
	buildOnce   sync.Once
	builtBinary string
	buildErr    error
)

// tangentBinary builds cmd/tangent once per test binary and returns its path.
func tangentBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		// The binary outlives one test's TempDir, so it goes in the test
		// binary's own scratch directory rather than a per-test one.
		dir, err := os.MkdirTemp("", "tangent-smoke-build-")
		if err != nil {
			buildErr = err
			return
		}
		builtBinary = filepath.Join(dir, "tangent")
		// #nosec G204 -- every argument is a literal or a path this function
		// created under os.MkdirTemp.
		build := exec.Command("go", "build", "-o", builtBinary, "./cmd/tangent")
		build.Dir = repoRoot()
		if output, err := build.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build ./cmd/tangent: %w\n%s", err, output)
		}
	})
	if buildErr != nil {
		t.Fatalf("build shipped binary: %v", buildErr)
	}
	return builtBinary
}

func repoRoot() string {
	// internal/smoke -> internal -> repo root.
	return filepath.Clean(filepath.Join("..", ".."))
}

// bootShippedBinary starts ./tangent against a database inside t.TempDir on a
// port reserved for this test, and returns the endpoint serving it.
//
// Two safety properties are asserted rather than assumed, because a smoke
// check that ran against a developer's real installation once is a smoke check
// nobody runs again:
//
//   - TANGENT_DB_PATH is always set and always inside t.TempDir(), so the
//     process cannot fall back to ~/.tangent/tangent.db.
//   - The port is reserved by this test, never the default 7842, so a running
//     local instance is neither contacted nor displaced.
//
// Cleanup signals the pid this function started and nothing else.
// bootShippedBinary boots the binary with the first-party plugins installed.
// Most callers want only the endpoint; the plugin surface test also needs to
// know where they were installed, which is what the WithPluginDir variant is
// for. The dir is not on smoke.Endpoint because that is a production type and
// this is a fact about a test's own temp directory.
func bootShippedBinary(t *testing.T) smoke.Endpoint {
	t.Helper()
	endpoint, _ := bootShippedBinaryWithPluginDir(t)
	return endpoint
}

func bootShippedBinaryWithPluginDir(t *testing.T) (smoke.Endpoint, string) {
	t.Helper()
	binary := tangentBinary(t)

	root := t.TempDir()
	dbPath := filepath.Join(root, "smoke", "tangent.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		t.Fatalf("create smoke database directory: %v", err)
	}
	if !strings.HasPrefix(dbPath, root) {
		t.Fatalf("refusing to boot: database path %q escapes the test temp root", dbPath)
	}
	port := reservePort(t)
	if port == 7842 {
		t.Fatalf("refusing to boot: reserved the default port 7842, which a local instance may own")
	}
	pluginDir := installFirstPartyPlugins(t, binary, root)

	command := exec.Command(binary) // #nosec G204 -- the binary is the one this test just built.
	command.Env = append(os.Environ(),
		"TANGENT_DB_PATH="+dbPath,
		"TANGENT_PLUGIN_DIR="+pluginDir,
		"TANGENT_HTTP_PORT="+strconv.Itoa(port),
		// An unset dev frontend URL keeps the embedded bundle in play, which
		// is what readiness reports on as the renderer host.
		"TANGENT_DEV_FRONTEND_URL=",
	)
	var logs strings.Builder
	command.Stdout = &logs
	command.Stderr = &logs
	if err := command.Start(); err != nil {
		t.Fatalf("start shipped binary: %v", err)
	}
	t.Cleanup(func() {
		if command.Process == nil {
			return
		}
		// Signal this pid only. Never a pattern-matched kill: a smoke check
		// that can take down a process it did not start is a worse outage than
		// the one it was written to find.
		_ = command.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = command.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(shutdownBudget):
			_ = command.Process.Kill()
			<-done
		}
	})

	endpoint := smoke.NewEndpoint(fmt.Sprintf("http://127.0.0.1:%d", port))
	deadline := time.Now().Add(bootBudget)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		listening := endpoint.Listening(ctx)
		cancel()
		if listening {
			return endpoint, pluginDir
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("shipped binary never answered liveness at %s within %s; logs:\n%s",
		endpoint.BaseURL, bootBudget, logs.String())
	return smoke.Endpoint{}, ""
}

// reservePort takes an ephemeral port from the kernel and releases it, so the
// booted process binds a port nothing else on this machine was using.
func reservePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release reserved port: %v", err)
	}
	return port
}

// installFirstPartyPlugins builds the first-party plugins and installs them
// into a directory this test owns, then returns it.
//
// # Why smoke boots a COMPLETE install rather than a bare binary
//
// Since CW-20260911-0070 plugins are installed, not compiled in. A bare
// `./tangent` therefore serves a smaller MCP surface than a user has — four
// tools smaller, measured — and every document that names a plugin's tools
// would fail the documentation gate against it.
//
// Two ways to resolve that, and the choice matters more than it looks.
//
// The first is to stop expecting those tools: drop them from the documented
// set, and let the gate measure the binary alone. That would be measuring an
// artifact nobody runs. The product is Tangent AND its first-party plugins;
// `make install-plugins` is how a user gets both, and a gate that asserted
// against something narrower would go green while the thing being shipped was
// broken.
//
// The second, taken here, is to install them the way a user does and measure
// that. It costs a build per smoke run and it buys the stronger property: these
// tests now prove the whole path — build the plugin, emit its manifest, install
// it, discover it, spawn it, and advertise its tools — rather than proving that
// a binary links what it was compiled with. The migration is not something this
// suite asserts; it is the thing it runs on.
//
// A plugin's tools are still documented in Tangent's own documents, and that
// stays right while the plugins are first-party and versioned here. A
// third-party plugin would document its own tools and the gate would have to
// learn the difference — which is out of scope (ADR 0008 §4) and would want its
// own decision rather than a widened glob.
func installFirstPartyPlugins(t *testing.T, tangentBinary, root string) string {
	t.Helper()
	pluginDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginDir, 0o750); err != nil {
		t.Fatalf("create plugin directory: %v", err)
	}

	for _, name := range []string{"torque", "tesseract"} {
		staging := filepath.Join(root, "staging", name)
		if err := os.MkdirAll(staging, 0o750); err != nil {
			t.Fatalf("create staging directory: %v", err)
		}
		binaryName := "tangent-plugin-" + name
		built := filepath.Join(staging, binaryName)

		// #nosec G204 -- every argument is a literal but the temp paths this
		// test just created.
		build := exec.Command("go", "build", "-o", built, "../../cmd/"+binaryName)
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			t.Fatalf("build %s: %v", binaryName, err)
		}

		// The plugin emits its own plugin.yaml, so the tool names and schemas
		// have one source — the Go package — rather than a hand-written copy.
		manifest, err := exec.Command(built, "--manifest").Output() // #nosec G204 -- just built above.
		if err != nil {
			t.Fatalf("%s --manifest: %v", binaryName, err)
		}
		if err := os.WriteFile(filepath.Join(staging, "plugin.yaml"), manifest, 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}

		// Installed through the real command, not by copying files here. If
		// `tangent plugin install` is broken, these tests should fail.
		install := exec.Command(tangentBinary, "plugin", "install", staging) // #nosec G204 -- both are this test's own paths.
		install.Env = append(os.Environ(), "TANGENT_PLUGIN_DIR="+pluginDir)
		if output, err := install.CombinedOutput(); err != nil {
			t.Fatalf("tangent plugin install %s: %v\n%s", name, err, output)
		}
	}
	return pluginDir
}
