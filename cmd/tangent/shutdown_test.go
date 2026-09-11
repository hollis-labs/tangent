package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

// This file guards one property of `run`: a shutdown that fails still exits
// non-zero.
//
// # What it does NOT claim, and why that matters
//
// An earlier draft of this test asserted that a shutdown exceeding its deadline
// leaves the database ownership claim releasable — on the theory that the
// os.Exit `run` used to call skipped the deferred closer and therefore leaked
// the lock. **That theory was wrong, and this test is what caught it: it passed
// against the buggy build.**
//
// The claim is an advisory flock on a file descriptor, and
// `db.AcquireOwnership` says so in its own doc comment — "exiting is equally
// effective: the kernel drops the lock with the file descriptor". Every other
// resource the closer releases dies with the process too. So there was no
// observable leak to reproduce, and a test asserting one would have passed for
// a reason unrelated to the fix and failed later for a reason unrelated to the
// bug.
//
// What a hung `/sse` stream actually costs is the ten seconds: `srv.Shutdown`
// waits for it, so a restart inside that window hits an ownership conflict.
// That is CW-20260909-0045, it is about an unbounded wait rather than a skipped
// defer, and **this change does not close it**.
//
// # What this test does guard
//
// The exit code, which is the half a naive fix breaks. "Replace os.Exit with a
// bare return" would run the cleanup and silently start reporting success for a
// shutdown that failed. That is observable, it is a genuine regression risk the
// next simplification will reach for, and it is worth eleven seconds.
//
// It deliberately asserts nothing about the *shape* of main — no counting of
// os.Exit calls, no reading of this package's source. The property is observed
// from outside the process, which is where it matters.
func TestAFailedShutdownStillExitsNonZero(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the real binary and waits out a ten-second shutdown deadline")
	}

	binary := buildTangent(t)
	root := t.TempDir()
	dbPath := filepath.Join(root, "tangent.db")
	port := reserveTestPort(t)

	command := exec.Command(binary) // #nosec G204 -- the binary this test just built.
	command.Env = append(os.Environ(),
		"TANGENT_DB_PATH="+dbPath,
		"TANGENT_HTTP_PORT="+strconv.Itoa(port),
		"TANGENT_DEV_FRONTEND_URL=",
	)
	var logs strings.Builder
	command.Stdout = &logs
	command.Stderr = &logs
	if err := command.Start(); err != nil {
		t.Fatalf("start tangent: %v", err)
	}
	defer func() {
		// Belt and braces: if an assertion below fails early, do not leave a
		// process holding the lock on a temp database.
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForReady(t, baseURL, &logs)

	// Hold an /sse stream open and never read it to completion. This is the
	// connection `srv.Shutdown` will wait for.
	streamCtx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	request, err := http.NewRequestWithContext(streamCtx, http.MethodGet, baseURL+"/sse", nil)
	if err != nil {
		t.Fatalf("build /sse request: %v", err)
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Origin", baseURL)
	stream, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("open /sse: %v", err)
	}
	defer func() { _ = stream.Body.Close() }()

	if signalErr := command.Process.Signal(syscall.SIGTERM); signalErr != nil {
		t.Fatalf("SIGTERM: %v", signalErr)
	}

	// Generous: the shutdown deadline in cmd/tangent is ten seconds, and this
	// test is specifically about what happens *after* it expires.
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()

	var waitErr error
	select {
	case waitErr = <-exited:
	case <-time.After(40 * time.Second):
		t.Fatalf("tangent did not exit within 40s of SIGTERM; logs:\n%s", logs.String())
	}

	// The property. A shutdown that exceeded its deadline must still tell its
	// supervisor it failed.
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		t.Fatalf("tangent exited %v, want a non-zero status: a shutdown that "+
			"exceeded its deadline reported success. Logs:\n%s", waitErr, logs.String())
	}
	if exitErr.ExitCode() == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}

	// And the successor can boot — which is true here because the kernel
	// dropped the flock when the process exited, NOT because the deferred
	// closer ran. Asserted so a future change that moves ownership to something
	// the kernel does not clean up (a lease, a row, a remote claim) fails here
	// rather than in an operator's restart loop.
	ownership, err := tangentdb.AcquireOwnership(dbPath, tangentdb.RoleServer, "shutdown test successor")
	if err != nil {
		t.Fatalf("a successor cannot acquire ownership after the previous process "+
			"exited: %v\nLogs:\n%s", err, logs.String())
	}
	if releaseErr := ownership.Release(); releaseErr != nil {
		t.Errorf("release: %v", releaseErr)
	}
}

// buildTangent compiles this package into the test's temp directory. It builds
// rather than reusing a checked-in binary so the test is about the tree it runs
// in.
func buildTangent(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tangent-shutdown-test")
	// #nosec G204 -- every argument is a literal but `binary`, which is this
	// test's own t.TempDir() path.
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}
	return binary
}

// reserveTestPort asks the kernel for a free port and hands it back. There is a
// race between closing the listener and the binary binding it; nothing in this
// repository has a way to avoid that without threading a listener through the
// boot path, and a collision fails loudly rather than silently.
func reserveTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close reserved listener: %v", err)
	}
	if port == 7842 {
		t.Fatalf("reserved the default port 7842, which a local instance may own")
	}
	return port
}

// waitForReady polls until the server answers, so the test does not SIGTERM a
// process that has not finished booting — which would pass for the wrong reason.
func waitForReady(t *testing.T, baseURL string, logs *strings.Builder) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(baseURL + "/healthz") // #nosec G107 -- loopback, test-owned port.
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode < 500 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("tangent did not become ready within 30s; logs:\n%s", logs.String())
}
