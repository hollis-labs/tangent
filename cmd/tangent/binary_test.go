package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/testenv"
)

// The built binary, on a throwaway data directory (testenv.Command gives it a clean environment
// whose HOME and XDG variables are inside a temp directory), answers health, refuses an anonymous
// API call and an unknown Host, creates its data files private, and stops on SIGTERM.
func TestBinaryServesAndStopsOnSIGTERM(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "tangent")
	build := exec.Command("go", "build", "-o", bin, ".") //nolint:gosec // builds this package into a temp dir
	if out, err := build.CombinedOutput(); err != nil {
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
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid // the only process this test signals
	exited := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		select {
		case <-exited:
		default:
			_ = syscall.Kill(pid, syscall.SIGKILL)
			<-exited
		}
	})
	ready := make(chan struct{})
	var tail strings.Builder
	go func() {
		sc := bufio.NewScanner(stderr)
		closed := false
		for sc.Scan() {
			tail.WriteString(sc.Text() + "\n")
			if !closed && strings.Contains(sc.Text(), "tangent listening") {
				closed = true
				close(ready)
			}
		}
	}()
	select {
	case <-ready:
	case <-exited:
		t.Fatalf("exited before listening: %v\n%s", waitErr, tail.String())
	case <-time.After(30 * time.Second):
		t.Fatalf("never listened\n%s", tail.String())
	}

	get := func(path, host string) int {
		req, _ := http.NewRequestWithContext(context.Background(), "GET", "http://"+addr+path, nil)
		if host != "" {
			req.Host = host
		}
		res, derr := http.DefaultClient.Do(req)
		if derr != nil {
			t.Fatal(derr)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		return res.StatusCode
	}
	if got := get("/healthz", ""); got != 200 {
		t.Errorf("/healthz = %d", got)
	}
	if got := get("/api/identity/profile", ""); got != 401 {
		t.Errorf("anonymous /api/identity/profile = %d, want 401", got)
	}
	if got := get("/healthz", "rebind.example.test"); got != 403 {
		t.Errorf("unknown Host = %d, want 403", got)
	}
	data := filepath.Join(cmd.Dir, "data_home", "tangent")
	for name, want := range map[string]os.FileMode{"": 0o700, "tangent.db": 0o600, "identity-signing.key": 0o600} {
		fi, serr := os.Stat(filepath.Join(data, name))
		if serr != nil {
			t.Errorf("%s: %v", name, serr)
			continue
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s mode %v, want %v", name, fi.Mode().Perm(), want)
		}
	}

	if err = syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
		if waitErr != nil {
			t.Fatalf("SIGTERM did not end cleanly: %v\n%s", waitErr, tail.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("SIGTERM did not stop the process\n%s", tail.String())
	}
}
