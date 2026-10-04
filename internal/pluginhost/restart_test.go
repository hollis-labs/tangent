package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

// These tests hold CW-20260911-0068 (restart policy) and CW-20260911-0069
// (health probing) — filed once Torque and Tesseract stopped being a
// subprocess-mode prototype and became how this host runs every plugin it
// ships (CW-20260911-0070 removed the compiled-in roster entirely).

// pid reads the callable process through the shared lifecycle.
func (p *ChildPlugin) pid() int {
	p.mu.Lock()
	l := p.lifecycle
	p.mu.Unlock()
	if l == nil {
		return 0
	}
	proc := l.Current()
	if proc == nil {
		return 0
	}
	return proc.Pid()
}
func killCurrent(p *ChildPlugin) error {
	p.mu.Lock()
	l := p.lifecycle
	p.mu.Unlock()
	if l == nil {
		return errors.New("no lifecycle")
	}
	proc := l.Current()
	if proc == nil {
		return errors.New("no process installed")
	}
	return proc.Kill()
}

// runningCount counts live processes whose command line contains needle.
// pgrep -f matches the whole command line, and a test binary's own tempdir
// path is unique enough that this cannot match an unrelated process.
func runningCount(t *testing.T, needle string) int {
	t.Helper()
	// #nosec G204 -- needle is this test's own t.TempDir()-scoped binary path, not caller input.
	out, err := exec.Command("pgrep", "-f", needle).Output()
	if err != nil {
		// pgrep exits 1 with empty output when nothing matches — that is
		// "zero", not a test failure.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return 0
		}
		t.Fatalf("pgrep: %v", err)
	}
	lines := strings.FieldsFunc(strings.TrimSpace(string(out)), func(r rune) bool { return r == '\n' })
	return len(lines)
}

// TestACrashedChildIsAutomaticallyRestarted is CW-20260911-0068's whole claim:
// a plugin that dies on its own comes back without an operator or a Tangent
// restart, and a caller reaches the new process transparently.
func TestACrashedChildIsAutomaticallyRestarted(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)

	child := NewChildPlugin(echoSpec(t, binary),
		[]MCPTool{{Name: "tangent.echo", Description: "Echo.", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		nil)
	if err := host.Load(child); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { _ = child.Unload() })

	firstPID := child.pid()
	if err := killCurrent(child); err != nil {
		t.Fatalf("kill: %v", err)
	}

	// restartInitialBackoff (1s) plus a handshake; bounded generously so a
	// slow CI runner does not make this flaky.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		result, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{
			ToolName: "tangent.echo", Arguments: map[string]any{"say": "back"},
		})
		if err == nil {
			if !strings.Contains(string(result.Content), "back") {
				t.Fatalf("content = %s", result.Content)
			}
			if second := child.pid(); second == firstPID || second == 0 {
				t.Fatalf("pid after restart = %d, want a different, non-zero pid (was %d)", second, firstPID)
			}
			if got := child.Restarts(); got != 1 {
				t.Errorf("Restarts() = %d, want 1", got)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the plugin never came back after its process was killed")
}

// TestARestartLoopGivesUpAfterMaxAttempts holds the other half of the
// decision: a plugin that keeps crashing is not restarted forever.
func TestARestartLoopGivesUpAfterMaxAttempts(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)

	child := NewChildPlugin(echoSpec(t, binary), nil, nil)
	if err := host.Load(child); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { _ = child.Unload() })

	lastPID := child.pid()
	for attempt := 0; attempt < maxChildRestarts; attempt++ {
		if err := killCurrent(child); err != nil {
			t.Fatalf("kill %d: %v", attempt, err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			if time.Now().After(deadline) {
				t.Fatalf("restart attempt %d never landed", attempt+1)
			}
			if pid := child.pid(); pid != 0 && pid != lastPID {
				lastPID = pid
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if got := child.Restarts(); got != maxChildRestarts {
		t.Fatalf("Restarts() = %d, want %d", got, maxChildRestarts)
	}

	// One more crash: this one must NOT come back.
	if err := killCurrent(child); err != nil {
		t.Fatalf("final kill: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if pid := child.pid(); pid != 0 && pid != lastPID {
			t.Fatalf("a restart happened after the cap was reached (Restarts()=%d)", child.Restarts())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := child.Restarts(); got != maxChildRestarts {
		t.Fatalf("Restarts() after the final crash = %d, want it to stay at the cap %d", got, maxChildRestarts)
	}
	if !strings.Contains(child.Status().LastError, "exhausted") {
		t.Errorf("LastError = %q, want it to say the restart budget is exhausted", child.Status().LastError)
	}
	if _, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{ToolName: "tangent.echo"}); err == nil {
		t.Error("a call to a permanently-crashed plugin succeeded")
	} else if !errors.Is(err, ErrChildGone) {
		t.Errorf("err = %v, want ErrChildGone", err)
	}
}

// TestUnloadDuringRestartBackoffDoesNotResurrectTheChild is CW-20260911-0068
// bullet 4: a restart in flight while the host is unloading this plugin must
// not spawn a child after the host decided to stop it.
func TestUnloadDuringRestartBackoffDoesNotResurrectTheChild(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)

	child := NewChildPlugin(echoSpec(t, binary), nil, nil)
	if err := host.Load(child); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Belt and suspenders alongside the explicit Unload below: if an earlier
	// assertion in this test fails first, this still ends the process rather
	// than leaking it into the rest of the test run.
	t.Cleanup(func() { _ = child.Unload() })

	// binary's path is unique to this test's t.TempDir(), so counting live
	// processes whose command line names it is an unambiguous count of how
	// many instances of THIS plugin are running.
	if got := runningCount(t, binary); got != 1 {
		t.Fatalf("running instances before kill = %d, want 1", got)
	}

	if err := killCurrent(child); err != nil {
		t.Fatalf("kill: %v", err)
	}
	// Unload races the supervisor's restartInitialBackoff (1s) sleep. Calling
	// it immediately, well inside that window, is the scenario bullet 4 names.
	if err := child.Unload(); err != nil {
		t.Fatalf("Unload: %v", err)
	}

	// Wait past the full backoff and a generous handshake budget. If the race
	// were lost, a second instance would be running by now and nothing would
	// ever stop it (no supervisor is watching a plugin the host unloaded).
	time.Sleep(restartInitialBackoff + 3*time.Second)
	if got := runningCount(t, binary); got != 0 {
		t.Errorf("running instances after Unload raced a pending restart = %d, want 0 "+
			"(a child was spawned after the host decided to stop this plugin)", got)
	}
	if got := child.Restarts(); got != 0 {
		t.Errorf("Restarts() = %d, want 0 — the aborted attempt must not count as a completed one", got)
	}
}

// TestProbeHealthReportsWhatThePluginSaid is CW-20260911-0069's basic claim:
// the host can ask a live child whether it is fit to serve, not just whether
// it exists.
func TestProbeHealthReportsWhatThePluginSaid(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)

	spec := echoSpec(t, binary)
	spec.Env = append(spec.Env, "ECHO_PLUGIN_HEALTH=on the way to a bad dependency")
	child := NewChildPlugin(spec, nil, nil)
	if err := host.Load(child); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { _ = child.Unload() })

	health := child.probeHealth(context.Background())
	if health.ok {
		t.Fatal("probeHealth reported healthy for a plugin that answered unhealthy")
	}
	if health.message != "on the way to a bad dependency" {
		t.Errorf("message = %q, want the plugin's own message", health.message)
	}
	if !health.reachable {
		t.Error("reachable = false, want true — the plugin answered, it just said no")
	}
}

// TestHealthGateRefusesAnUnhealthyPluginByDefault is the "default to gate"
// decision: once a probe has reported unhealthy, a caller's tool call is
// refused with a distinguishable error until a later probe says otherwise.
func TestHealthGateRefusesAnUnhealthyPluginByDefault(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)

	spec := echoSpec(t, binary)
	spec.Env = append(spec.Env, "ECHO_PLUGIN_HEALTH=unhealthy")
	child := NewChildPlugin(spec,
		[]MCPTool{{Name: "tangent.echo", Description: "Echo.", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		nil)
	if err := host.Load(child); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { _ = child.Unload() })

	// Before any probe, an unhealthy plugin still serves — this host has no
	// evidence yet to refuse on.
	if _, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{ToolName: "tangent.echo"}); err != nil {
		t.Fatalf("a call before any health probe was refused: %v", err)
	}

	child.probeHealth(context.Background())

	if _, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{ToolName: "tangent.echo"}); err == nil {
		t.Fatal("a call to a plugin known unhealthy was not refused")
	} else if !errors.Is(err, ErrPluginUnhealthy) {
		t.Errorf("err = %v, want ErrPluginUnhealthy", err)
	} else if errors.Is(err, ErrChildGone) {
		t.Error("an unhealthy-but-alive refusal must not also read as ErrChildGone")
	}
}

// TestHealthGateCanBeDisabled is the opt-out half: an operator who turns the
// gate off gets the pre-CW-20260911-0069 behavior back.
func TestHealthGateCanBeDisabled(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)

	spec := echoSpec(t, binary)
	spec.Env = append(spec.Env, "ECHO_PLUGIN_HEALTH=unhealthy")
	child := NewChildPlugin(spec,
		[]MCPTool{{Name: "tangent.echo", Description: "Echo.", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		nil, WithHealthGate(false))
	if err := host.Load(child); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { _ = child.Unload() })

	child.probeHealth(context.Background())

	if _, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{ToolName: "tangent.echo"}); err != nil {
		t.Fatalf("a call to a known-unhealthy plugin was refused with the gate disabled: %v", err)
	}
}

// TestInventoryReportsRestartsAndHealth is the observability half of both
// tickets: what tangent.health_report actually gets to show an operator.
func TestInventoryReportsRestartsAndHealth(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)

	spec := echoSpec(t, binary)
	spec.Env = append(spec.Env, "ECHO_PLUGIN_HEALTH=degraded upstream")
	child := NewChildPlugin(spec, nil, nil)
	if err := host.Load(child); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { _ = child.Unload() })

	inventory := host.Inventory(context.Background())
	record := inventory.Plugins[0]
	if record.Healthy == nil || *record.Healthy {
		t.Fatalf("Healthy = %v, want a probed false — Inventory itself must probe", record.Healthy)
	}
	if record.HealthMessage != "degraded upstream" {
		t.Errorf("HealthMessage = %q", record.HealthMessage)
	}
	if record.HealthCheckedAt.IsZero() {
		t.Error("HealthCheckedAt is zero after a probe ran")
	}
	if record.Restarts != 0 {
		t.Errorf("Restarts = %d, want 0 for a plugin that never crashed", record.Restarts)
	}

	firstPID := child.pid()
	if err := killCurrent(child); err != nil {
		t.Fatalf("kill: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && (child.pid() == firstPID || child.pid() == 0) {
		time.Sleep(100 * time.Millisecond)
	}

	after := host.Inventory(context.Background())
	if got := after.Plugins[0].Restarts; got != 1 {
		t.Errorf("Restarts after one crash = %d, want 1", got)
	}
}
