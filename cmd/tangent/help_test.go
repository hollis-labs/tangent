package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runTangent runs the built binary against a throwaway database path with a
// deadline, so a regression that starts a server fails the test instead of
// hanging it.
func runTangent(t *testing.T, binary string, args ...string) (stdout, stderr string, code int, dbDir string) {
	t.Helper()
	dbDir = t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...) // #nosec G204 -- the binary this test just built.
	command.Env = append(os.Environ(),
		"TANGENT_DB_PATH="+filepath.Join(dbDir, "tangent.db"),
		"TANGENT_HTTP_PORT=0",
		"TANGENT_DEV_FRONTEND_URL=",
	)
	var out, errOut strings.Builder
	command.Stdout = &out
	command.Stderr = &errOut
	err := command.Run()
	if ctx.Err() != nil {
		t.Fatalf("tangent %v did not exit within 15s (it started a server?); stderr:\n%s", args, errOut.String())
	}
	code = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run tangent %v: %v", args, err)
	}
	return out.String(), errOut.String(), code, dbDir
}

func TestSubcommandHelpPrintsUsageWithoutTouchingTheDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the real binary")
	}
	binary := buildTangent(t)

	for _, args := range [][]string{{"docs", "--help"}, {"docs", "-h"}, {"help"}} {
		stdout, stderr, code, dbDir := runTangent(t, binary, args...)
		if code != 0 {
			t.Errorf("tangent %v exit = %d, want 0; stderr:\n%s", args, code, stderr)
		}
		if !strings.Contains(stdout, "Usage") {
			t.Errorf("tangent %v printed no usage on stdout:\n%s", args, stdout)
		}
		entries, _ := os.ReadDir(dbDir)
		if len(entries) != 0 {
			t.Errorf("tangent %v created files in the DB dir (opened the database or took the owner lock): %v", args, entries)
		}
	}
}

func TestUnknownSubcommandFailsWithoutStartingAServer(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the real binary")
	}
	binary := buildTangent(t)

	_, stderr, code, dbDir := runTangent(t, binary, "docs")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, `unknown command "docs"`) {
		t.Errorf("stderr does not name the unknown command:\n%s", stderr)
	}
	entries, _ := os.ReadDir(dbDir)
	if len(entries) != 0 {
		t.Errorf("created files in the DB dir: %v", entries)
	}
}
