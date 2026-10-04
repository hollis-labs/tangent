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
	pluginDir := installFirstPartyPlugins(t, binary, root)
	// Resolve/build/install can take long enough for the kernel to reuse a
	// released ephemeral port. Choose it only after staging, just before spawn.
	port := reservePort(t)
	if port == 7842 {
		t.Fatalf("refusing to boot: reserved the default port 7842, which a local instance may own")
	}

	command := exec.Command(binary) // #nosec G204 -- the binary is the one this test just built.
	command.Env = append(os.Environ(),
		"HOME="+root,
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

// firstPartyPlugins are the plugins Tangent documents as its own. Their code
// lives in github.com/hollis-labs/tangent-plugins (CW-20260930-0102), one
// module each; the Makefile's PLUGINS names the same three.
var firstPartyPlugins = []string{"runner", "tesseract", "torque"}

// pluginsVersionFile pins the tangent-plugins release this Tangent documents
// and tests against. The Makefile's install-plugins reads the same file.
const pluginsVersionFile = "tangent-plugins.version"

var (
	pluginBuildOnce sync.Once
	pluginStaging   map[string]string
	pluginBuildErr  error
)

// stagedFirstPartyPlugins builds each first-party plugin once per test
// process and returns, per plugin, an installable directory: the binary beside
// the plugin.yaml it emits.
//
// It builds the PINNED MODULE VERSION by default — `go install
// github.com/hollis-labs/tangent-plugins/<p>/cmd/tangent-plugin-<p>@<version>`,
// exactly what `make install-plugins` does — so the gate measures what a user
// installs, not a checkout that happens to be nearby. TANGENT_PLUGINS_SRC names
// a tangent-plugins checkout to build from instead, for changing a plugin and
// the document that names its tools in the same sitting.
//
// The pinned path needs the module proxy on a cold cache; that is the accepted
// cost of the plugins living in their own repository.
func stagedFirstPartyPlugins(t *testing.T) map[string]string {
	t.Helper()
	pluginBuildOnce.Do(func() {
		pluginStaging, pluginBuildErr = buildFirstPartyPlugins()
	})
	if pluginBuildErr != nil {
		t.Fatalf("build first-party plugins: %v", pluginBuildErr)
	}
	return pluginStaging
}

// Extra independently shipped plugins can be tested before their first release
// by building a local checkout with TANGENT_PLUGINS_SRC.
func smokePlugins() []string {
	names := append([]string{}, firstPartyPlugins...)
	for _, name := range strings.Fields(os.Getenv("TANGENT_SMOKE_EXTRA_PLUGINS")) {
		if name == "" || strings.ContainsAny(name, "/\\.") {
			continue
		}
		names = append(names, name)
	}
	return names
}

func buildFirstPartyPlugins() (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(), pluginsVersionFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pluginsVersionFile, err)
	}
	version := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(version, "v") {
		return nil, fmt.Errorf("%s holds %q, not a module version", pluginsVersionFile, version)
	}
	source := os.Getenv("TANGENT_PLUGINS_SRC")

	root, err := os.MkdirTemp("", "tangent-smoke-plugins-")
	if err != nil {
		return nil, err
	}
	staged := map[string]string{}
	for _, name := range smokePlugins() {
		dir := filepath.Join(root, "tangent.plugin."+name)
		if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o750); err != nil {
			return nil, err
		}
		binaryName := "tangent-plugin-" + name
		built := filepath.Join(dir, "bin", binaryName)

		var build *exec.Cmd
		if source != "" {
			// #nosec G204 -- the checkout is the developer's own override.
			build = exec.Command("go", "build", "-o", built, "./cmd/"+binaryName)
			build.Dir = filepath.Join(source, name)
		} else {
			module := "github.com/hollis-labs/tangent-plugins/" + name + "/cmd/" + binaryName
			// #nosec G204 -- a fixed module path at the pinned version.
			build = exec.Command("go", "install", module+"@"+version)
			build.Env = append(os.Environ(), "GOBIN="+filepath.Join(dir, "bin"))
		}
		if output, err := build.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("build %s: %w\n%s", binaryName, err, output)
		}

		// The plugin emits its own plugin.yaml, so the tool names and schemas
		// have one source — the plugin's code — rather than a hand-written copy.
		manifest, err := exec.Command(built, "--manifest").Output() // #nosec G204 -- just built above.
		if err != nil {
			return nil, fmt.Errorf("%s --manifest: %w", binaryName, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), manifest, 0o600); err != nil {
			return nil, err
		}
		staged[name] = dir
	}
	return staged, nil
}

// installFirstPartyPlugins installs the first-party plugins into a plugin
// directory this test owns, and returns it.
//
// # Why smoke boots a COMPLETE install rather than a bare binary
//
// Plugins are installed, not compiled in. A bare `./tangent` therefore serves a
// smaller MCP surface than a user has, and every document that names a
// plugin's tools would fail the documentation gate against it. Dropping those
// tools from the documented set would measure an artifact nobody runs: the
// product is Tangent AND its first-party plugins, and `make install-plugins` is
// how a user gets both. So these tests install them the way a user does and
// measure that — build (or fetch) the plugin, emit its manifest, install it,
// discover it, spawn it, and advertise its tools.
//
// The plugins' tools are still documented in Tangent's own documents while they
// are first-party. A tool rename in a plugin therefore lands as a tangent-plugins
// release plus a Tangent change that bumps tangent-plugins.version and the
// documents together; the gate is what forces that pairing.
func installFirstPartyPlugins(t *testing.T, tangentBinary, root string) string {
	t.Helper()
	pluginDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginDir, 0o750); err != nil {
		t.Fatalf("create plugin directory: %v", err)
	}
	for _, name := range smokePlugins() {
		staged := stagedFirstPartyPlugins(t)[name]
		// Installed through the real command, not by copying files here. If
		// `tangent plugin install` is broken, these tests should fail.
		install := exec.Command(tangentBinary, "plugin", "install", staged) // #nosec G204 -- both are this test's own paths.
		install.Env = append(os.Environ(), "HOME="+root, "TANGENT_PLUGIN_DIR="+pluginDir)
		if output, err := install.CombinedOutput(); err != nil {
			t.Fatalf("tangent plugin install %s: %v\n%s", name, err, output)
		}
	}
	return pluginDir
}
