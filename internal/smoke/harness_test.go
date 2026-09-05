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
func bootShippedBinary(t *testing.T) smoke.Endpoint {
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

	command := exec.Command(binary) // #nosec G204 -- the binary is the one this test just built.
	command.Env = append(os.Environ(),
		"TANGENT_DB_PATH="+dbPath,
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
			return endpoint
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("shipped binary never answered liveness at %s within %s; logs:\n%s",
		endpoint.BaseURL, bootBudget, logs.String())
	return smoke.Endpoint{}
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
