package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/testenv"
)

// A shutdown that fails still exits non-zero. httphost.Serve returns an error when a handler is
// still running after the grace period (its own tests cover that return); what is Tangent's is that
// the process reports it. A request whose body never finishes is such a handler: it ignores the
// stop, so the server cannot drain it. Observed from outside the process, where a supervisor sees it.
func TestAFailedShutdownStillExitsNonZero(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the real binary and waits out the ten-second grace period")
	}
	bin := filepath.Join(t.TempDir(), "tangent")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil { // #nosec G204 -- builds this package into a temp dir
		t.Fatalf("build: %v\n%s", err, out)
	}
	addr, err := testenv.FreeAddr()
	if err != nil {
		t.Fatal(err)
	}
	cmd := testenv.Command(t, bin, "serve")
	cfg := filepath.Join(cmd.Dir, "override.yaml")
	if err = os.WriteFile(cfg, []byte("httphost:\n  addr: \""+addr+"\"\nidentity:\n  bcrypt_cost: 4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(cmd.Env, "SUBSTRATE_CONFIG="+cfg)
	var logs lockedBuilder
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(logs.String(), "tangent listening") {
		if time.Now().After(deadline) {
			t.Fatalf("never listened\n%s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err = fmt.Fprintf(conn, "POST /api/login HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nX-Requested-With: test\r\nContent-Length: 100\r\n\r\n{", addr); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)

	if err = syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-exited:
	case <-time.After(40 * time.Second):
		t.Fatalf("tangent did not exit within 40s of SIGTERM\n%s", logs.String())
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() == 0 {
		t.Fatalf("a shutdown that left a handler running exited %v, want a non-zero status\n%s", err, logs.String())
	}
	if !strings.Contains(logs.String(), "still running") {
		t.Fatalf("the shutdown did not fail for the open handler\n%s", logs.String())
	}
}

type lockedBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuilder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuilder) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
