package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	plugin "github.com/hollis-labs/plugin-sdk"
	"github.com/hollis-labs/plugin-sdk/subprocess"

	"github.com/hollis-labs/tangent/internal/authz"
)

// These tests run a REAL child process over the real wire.
//
// A fake transport would prove the host's Go code talks to itself. What this
// task is about is a plugin holding its own dependency in its own process, so
// the thing worth testing is a process — spawned, handshaken, dispatched to,
// and killed. `testdata/echoplugin` is the smallest plugin that makes that
// observable; it is built by these tests and shipped in no build.

// buildEchoPlugin compiles the test plugin and returns its path.
func buildEchoPlugin(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "echoplugin")
	// #nosec G204 -- every argument is a literal but `binary`, this test's own
	// t.TempDir() path.
	build := exec.Command("go", "build", "-o", binary, "./testdata/echoplugin")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build echoplugin: %v", err)
	}
	return binary
}

func echoSpec(t *testing.T, binary string) ChildSpec {
	t.Helper()
	root := t.TempDir()
	return ChildSpec{
		ID:       "tangent.plugin.echo",
		Command:  binary,
		DataDir:  filepath.Join(root, "data"),
		CacheDir: filepath.Join(root, "cache"),
		Env:      []string{"ECHO_PLUGIN_SECRET=from-the-childs-own-environment"},
	}
}

// TestASubprocessPluginLoadsDispatchesAndStops is the task's whole claim, end
// to end: a plugin in its own process, reached over a pipe, ended on request.
func TestASubprocessPluginLoadsDispatchesAndStops(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)

	child := NewChildPlugin(echoSpec(t, binary),
		[]MCPTool{{
			Name:        "tangent.echo",
			Description: "Echo the arguments back.",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		[]HTTPRoute{{
			Method:     http.MethodPost,
			Path:       RoutePrefix + "echo/say",
			Capability: authz.Draft,
		}},
	)

	if err := host.Load(child); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(host.MCPTools()); got != 1 {
		t.Fatalf("contributed tools = %d, want 1", got)
	}
	if got := len(host.HTTPRoutes()); got != 1 {
		t.Fatalf("contributed routes = %d, want 1", got)
	}

	// The child introduced itself, and the host reports what it said rather
	// than what the spec guessed.
	if child.Name() != "Echo" || child.Version() != "0.1.0" {
		t.Errorf("identity = %q %q, want the values plugin/init returned",
			child.Name(), child.Version())
	}

	result, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{
		ToolName:  "tangent.echo",
		Arguments: map[string]any{"say": "hello"},
	})
	if err != nil {
		t.Fatalf("MCPCallTool: %v", err)
	}
	var echoed struct {
		Tool          string         `json:"tool"`
		Arguments     map[string]any `json:"arguments"`
		ConfigSize    int            `json:"config_size"`
		DataDir       string         `json:"data_dir"`
		Host          string         `json:"host"`
		SecretFromEnv string         `json:"secret_from_env"`
	}
	if decodeErr := json.Unmarshal(result.Content, &echoed); decodeErr != nil {
		t.Fatalf("decode echo: %v", decodeErr)
	}
	if echoed.Tool != "tangent.echo" || echoed.Arguments["say"] != "hello" {
		t.Errorf("the child did not receive the call it was sent: %+v", echoed)
	}
	if echoed.DataDir == "" {
		t.Error("the child got no DataDir; a plugin treats that as fatal for persistence")
	}
	if echoed.Host == "" {
		t.Error("the child was not told what host it is talking to")
	}

	// THE SECRET BOUNDARY, ON THE WIRE.
	//
	// The host sent an empty config map, and the child got its secret from its
	// own environment. That is ADR 0005 §3.1 true by construction: there is no
	// host-side config store to leak, because the host never holds one. A
	// change that starts plumbing config through InitParams fails here.
	if echoed.ConfigSize != 0 {
		t.Errorf("the host sent %d config entries; it must send none, or it is holding "+
			"a plugin's configuration and can hold a plugin's secret", echoed.ConfigSize)
	}
	if echoed.SecretFromEnv != "from-the-childs-own-environment" {
		t.Errorf("the child did not read its own environment: %q", echoed.SecretFromEnv)
	}

	// The route crosses the same wire.
	response, err := child.HTTPHandle(context.Background(), subprocess.HTTPRequest{
		Method: http.MethodPost, Path: RoutePrefix + "echo/say", Body: []byte(`{"x":1}`),
	})
	if err != nil {
		t.Fatalf("HTTPHandle: %v", err)
	}
	if response.Status != http.StatusOK {
		t.Fatalf("route status = %d, want 200 (%s)", response.Status, response.Body)
	}
	var routed struct {
		Path string `json:"path"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(response.Body, &routed); err != nil {
		t.Fatalf("decode route response: %v", err)
	}
	if routed.Path != RoutePrefix+"echo/say" || routed.Body != `{"x":1}` {
		t.Errorf("the child did not receive the request it was sent: %+v", routed)
	}

	// And it stops. This is what a compiled-in plugin cannot do: there is a
	// process to end, so ending it actually releases the dependency.
	pid := child.proc.cmd.Process.Pid
	if err := host.Unload(child.ID()); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	if alive(pid) {
		t.Errorf("pid %d survived Unload", pid)
	}
	if _, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{
		ToolName: "tangent.echo",
	}); err == nil {
		t.Error("a call to an unloaded subprocess plugin succeeded")
	}
}

// TestOneSlowCallDoesNotKillTheConnection is the defect this wire exists not to
// reproduce.
//
// Nanite's client serializes write-then-read under one mutex and, on a read
// timeout, closes the underlying reader to unblock the abandoned read — which
// permanently kills the connection (its CW-20260902-0062). Every later call on
// a healthy plugin then fails.
//
// Correlating by id removes the reason to do that: the caller that gave up
// deregisters its own waiter and returns, the reader goroutine is untouched,
// and the next call is unaffected.
func TestOneSlowCallDoesNotKillTheConnection(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)

	child := NewChildPlugin(echoSpec(t, binary),
		[]MCPTool{
			{Name: "tangent.echo", Description: "Echo.", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "tangent.echo_slow", Description: "Never answers.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		}, nil)
	if err := host.Load(child); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { _ = child.Unload() })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := child.MCPCallTool(ctx, subprocess.MCPCallRequest{ToolName: "tangent.echo_slow"}); err == nil {
		t.Fatal("the call that never answers returned success")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow call err = %v, want a deadline", err)
	}

	// The connection is still good. This is the assertion that fails against
	// the close-the-reader approach.
	result, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{
		ToolName: "tangent.echo", Arguments: map[string]any{"say": "still here"},
	})
	if err != nil {
		t.Fatalf("a later call failed after one call timed out — the timeout killed the "+
			"connection: %v", err)
	}
	if !strings.Contains(string(result.Content), "still here") {
		t.Errorf("content = %s", result.Content)
	}
}

// TestAChildThatDiesFailsItsCallersRatherThanHangingThem. A plugin that
// crashes, or is killed out from under the host, must not leave a caller
// waiting out a budget for an answer that is never coming.
func TestAChildThatDiesFailsItsCallersRatherThanHangingThem(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)

	child := NewChildPlugin(echoSpec(t, binary),
		[]MCPTool{{Name: "tangent.echo_slow", Description: "Never answers.", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		nil)
	if err := host.Load(child); err != nil {
		t.Fatalf("Load: %v", err)
	}

	failed := make(chan error, 1)
	go func() {
		_, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{
			ToolName: "tangent.echo_slow",
		})
		failed <- err
	}()

	// Let the call get out on the wire before the process goes.
	time.Sleep(200 * time.Millisecond)
	if err := child.proc.kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}

	select {
	case err := <-failed:
		if !errors.Is(err, ErrChildGone) {
			t.Fatalf("err = %v, want ErrChildGone", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a caller was left waiting after its plugin's process died")
	}
}

// TestAPluginThatCannotSpawnContributesNothing. A failed Load must leave no
// tool dispatching into a process that is not there.
func TestAPluginThatCannotSpawnContributesNothing(t *testing.T) {
	host, _ := newHost(t)
	child := NewChildPlugin(
		ChildSpec{ID: "tangent.plugin.absent", Command: filepath.Join(t.TempDir(), "no-such-binary")},
		[]MCPTool{{Name: "tangent.absent", Description: "x", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		nil)

	if err := host.Load(child); err == nil {
		t.Fatal("Load succeeded for a plugin whose binary does not exist")
	}
	if got := len(host.MCPTools()); got != 0 {
		t.Errorf("a failed spawn contributed %d tools", got)
	}
	if _, held := host.GetPlugin(child.ID()); held {
		t.Error("a plugin that failed to spawn is on the roster")
	}
	if child.Status().LastError == "" {
		t.Error("the failure was not recorded on the plugin's status")
	}
}

// TestUnloadAllStopsEveryChild is the boot shutdown path's requirement: every
// child process is ended, in bounded time.
func TestUnloadAllStopsEveryChild(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)

	pids := make([]int, 0, 2)
	for _, id := range []string{"tangent.plugin.echo-a", "tangent.plugin.echo-b"} {
		spec := echoSpec(t, binary)
		spec.ID = id
		child := NewChildPlugin(spec, nil, nil)
		if err := host.Load(child); err != nil {
			t.Fatalf("Load %s: %v", id, err)
		}
		pids = append(pids, child.proc.cmd.Process.Pid)
	}

	started := time.Now()
	if err := host.UnloadAll(); err != nil {
		t.Fatalf("UnloadAll: %v", err)
	}
	// Bounded: the per-child budget exists so N children cannot sum past the
	// host's own shutdown deadline. Two healthy children should be far inside
	// it — they exit on their own stdin closing, well before any kill.
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("UnloadAll took %s for two healthy children", elapsed)
	}
	for _, pid := range pids {
		if alive(pid) {
			t.Errorf("pid %d survived UnloadAll", pid)
		}
	}
}

// alive reports whether a pid is still a live process. Signal 0 checks for
// existence without delivering anything.
func alive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// A reaped child is a zombie until Wait returns, and this host always
	// Waits, so a live answer here means genuinely running.
	return process.Signal(syscall.Signal(0)) == nil
}

var _ plugin.Plugin = (*ChildPlugin)(nil)
