package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

func waitReplacement(t *testing.T, child *ChildPlugin, oldPID int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for child.pid() == 0 || child.pid() == oldPID {
		if time.Now().After(deadline) {
			t.Fatal("replacement did not activate")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRestartGetsFreshHealthGate(t *testing.T) {
	host, _ := newHost(t)
	spec := echoSpec(t, buildEchoPlugin(t))
	spec.Env = append(spec.Env, "ECHO_PLUGIN_HEALTH=unhealthy")
	child := NewChildPlugin(spec, nil, nil)
	t.Cleanup(func() { _ = child.Unload() })
	if err := host.Load(child); err != nil {
		t.Fatal(err)
	}
	child.probeHealth(context.Background())
	req := subprocess.MCPCallRequest{ToolName: "tangent.echo"}
	if _, err := child.MCPCallTool(context.Background(), req); !errors.Is(err, ErrPluginUnhealthy) {
		t.Fatalf("gate: %v", err)
	}
	old := child.pid()
	if err := killCurrent(child); err != nil {
		t.Fatal(err)
	}
	waitReplacement(t, child, old)
	if _, err := child.MCPCallTool(context.Background(), req); err != nil {
		t.Fatalf("old health verdict crossed generation: %v", err)
	}
}

func TestRegistrationRefusalStopsChild(t *testing.T) {
	binary := buildEchoPlugin(t)
	for _, surface := range []string{"tool", "route"} {
		t.Run(surface, func(t *testing.T) {
			host, _ := newHost(t)
			var tools []MCPTool
			var routes []HTTPRoute
			if surface == "tool" {
				tools = []MCPTool{{Name: "", InputSchema: objectSchema()}}
			} else {
				route := validRoute()
				route.Path = "invalid"
				routes = []HTTPRoute{route}
			}
			child := NewChildPlugin(echoSpec(t, binary), tools, routes)
			t.Cleanup(func() { _ = child.Unload() })
			if err := host.Load(child); err == nil {
				t.Fatal("invalid registration accepted")
			}
			if child.pid() != 0 || runningCount(t, binary) != 0 {
				t.Fatal("registration refusal left child alive")
			}
		})
	}
}

func TestEnvironmentInheritanceAndPluginPWD(t *testing.T) {
	t.Setenv("ECHO_PLUGIN_PARENT_ONLY", "inherited-value")
	host, _ := newHost(t)
	spec := echoSpec(t, buildEchoPlugin(t))
	spec.WorkDir = t.TempDir()
	spec.Env = append(spec.Env, "PWD=wrong-directory")
	child := NewChildPlugin(spec, nil, nil)
	t.Cleanup(func() { _ = child.Unload() })
	if err := host.Load(child); err != nil {
		t.Fatal(err)
	}
	result, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{ToolName: "tangent.echo"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Inherited string `json:"inherited"`
		PWD       string `json:"pwd"`
	}
	if err := json.Unmarshal(result.Content, &got); err != nil {
		t.Fatal(err)
	}
	if got.Inherited != "inherited-value" || got.PWD != spec.WorkDir {
		t.Fatalf("environment: %+v", got)
	}
}

func TestStartupContextCancellationDoesNotStopChild(t *testing.T) {
	_, svc := newHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	host, err := New(ctx, slog.New(slog.DiscardHandler), svc)
	if err != nil {
		t.Fatal(err)
	}
	child := NewChildPlugin(echoSpec(t, buildEchoPlugin(t)), nil, nil)
	t.Cleanup(func() { _ = child.Unload() })
	if err := host.Load(child); err != nil {
		t.Fatal(err)
	}
	cancel()
	old := child.pid()
	if err := killCurrent(child); err != nil {
		t.Fatal(err)
	}
	waitReplacement(t, child, old)
	if _, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{ToolName: "tangent.echo"}); err != nil {
		t.Fatalf("startup cancellation stopped serving: %v", err)
	}
}

func TestPluginStderrIsRedactedBeforeCrashLog(t *testing.T) {
	_, svc := newHost(t)
	var logs lockedLogBuffer
	host, err := New(context.Background(), slog.New(slog.NewTextHandler(&logs, nil)), svc)
	if err != nil {
		t.Fatal(err)
	}
	spec := echoSpec(t, buildEchoPlugin(t))
	spec.Env = append(spec.Env, "ECHO_PLUGIN_STDERR=TOKEN=credential-value")
	child := NewChildPlugin(spec, nil, nil)
	t.Cleanup(func() { _ = child.Unload() })
	if err := host.Load(child); err != nil {
		t.Fatal(err)
	}
	old := child.pid()
	if err := killCurrent(child); err != nil {
		t.Fatal(err)
	}
	waitReplacement(t, child, old)
	// Disable joins the observer before inspecting its logger output.
	if err := child.Unload(); err != nil {
		t.Fatal(err)
	}
	text := logs.String()
	if !strings.Contains(text, "level=WARN") || !strings.Contains(text, "child crashed") || !strings.Contains(text, "[redacted]") || strings.Contains(text, "credential-value") {
		t.Fatalf("crash diagnostic: %s", text)
	}
}

func TestDiagnosticCredentialPatterns(t *testing.T) {
	for _, text := range []string{"TOKEN=credential-value", "api_key: credential-value", "Bearer credential-value", "https://user:credential-value@example.test", "sk-credential-value", "github_pat_credentialvalue"} {
		got := redactPluginDiagnostic(text)
		if strings.Contains(got, "credential") || !strings.Contains(got, "[redacted]") {
			t.Errorf("redaction %q => %q", text, got)
		}
	}
}

type lockedLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *lockedLogBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }
