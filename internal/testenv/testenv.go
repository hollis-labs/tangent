// Package testenv keeps tests and the binaries they start away from the real data of the person
// running them. Tangent is in use on the machines these tests run on: a test that resolved the
// user's home or XDG directories would read and write the live instance's files.
//
// Every test process that boots the app calls Isolate from TestMain, and every test that
// starts the built binary goes through Command, which hands the child a clean environment whose
// HOME and every XDG variable point at a temporary directory. TestNoUnisolatedBinaryRuns (in
// this package) fails the build when a test file starts a process any other way.
package testenv

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Vars are the variables that decide where an app keeps its files.
var Vars = []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"}

// Env returns a clean environment for a child process: PATH from the parent, HOME and every XDG
// variable inside root, nothing else (so TANGENT_*, SUBSTRATE_CONFIG and secrets do not leak in).
func Env(root string, extra ...string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root}
	for _, v := range Vars[1:] {
		dir := filepath.Join(root, strings.ToLower(strings.TrimPrefix(v, "XDG_")))
		_ = os.MkdirAll(dir, 0o700)
		env = append(env, v+"="+dir)
	}
	return append(env, extra...)
}

// Command builds an exec.Cmd for the binary at path with an isolated environment and working
// directory rooted in a fresh temporary directory. Nothing else may start the built binary.
func Command(t *testing.T, path string, args ...string) *exec.Cmd {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command(path, args...) //nolint:gosec // the tests name the binary
	cmd.Env = Env(root)
	cmd.Dir = root
	return cmd
}

// Isolate points HOME and every XDG variable of this process at a temporary directory for the
// rest of the process and fails when any of them is outside the temp directory afterwards. Call
// it from TestMain; it returns the root and a cleanup.
func Isolate() (root string, cleanup func(), err error) {
	root, err = os.MkdirTemp("", "tangent-test-")
	if err != nil {
		return "", nil, err
	}
	for _, kv := range Env(root)[1:] {
		k, v, _ := strings.Cut(kv, "=")
		if err = os.Setenv(k, v); err != nil {
			return "", nil, err
		}
	}
	for _, k := range []string{"TANGENT_DATA_DIR", "SUBSTRATE_CONFIG"} {
		_ = os.Unsetenv(k)
	}
	if err = Check(); err != nil {
		return "", nil, err
	}
	return root, func() { _ = os.RemoveAll(root) }, nil
}

// Check fails when HOME or an XDG variable of this process is unset or outside the temp directory.
func Check() error {
	tmp := os.TempDir()
	for _, k := range Vars {
		v := os.Getenv(k)
		if v == "" || !strings.HasPrefix(filepath.Clean(v)+string(filepath.Separator), filepath.Clean(tmp)+string(filepath.Separator)) {
			return fmt.Errorf("testenv: %s=%q is not inside %s: refusing to run against real data", k, v, tmp)
		}
	}
	return nil
}

// FreeAddr returns a loopback address that is free right now. Inside the agent's port range
// (MIGRATION_PORT_BASE to +9) when that is set, else any free port.
func FreeAddr() (string, error) {
	if base, err := strconv.Atoi(os.Getenv("MIGRATION_PORT_BASE")); err == nil && base > 0 {
		for p := base + 1; p < base+10; p++ {
			addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(p))
			if ln, err := net.Listen("tcp", addr); err == nil {
				_ = ln.Close()
				return addr, nil
			}
		}
		return "", fmt.Errorf("testenv: no free port in %d-%d", base+1, base+9)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String(), nil
}
