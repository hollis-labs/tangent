package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	driver "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
)

func echoedIncarnation(t *testing.T, child *ChildPlugin) capability.RuntimeIdentity {
	t.Helper()
	result, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{ToolName: "tangent.echo"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Incarnation capability.RuntimeIdentity `json:"incarnation"`
		Grants      capability.GrantSet        `json:"grants"`
		Contract    int                        `json:"capability_contract"`
		Services    json.RawMessage            `json:"host_services"`
		Hooks       json.RawMessage            `json:"hooks_profile"`
	}
	if err := json.Unmarshal(result.Content, &got); err != nil {
		t.Fatal(err)
	}
	if got.Incarnation.Validate() != nil || got.Contract != 1 || got.Grants == nil || len(got.Grants) != 0 {
		t.Fatalf("invalid canonical Init: %+v", got)
	}
	if string(got.Services) != "null" || string(got.Hooks) != "null" {
		t.Fatalf("unexpected offers: %+v", got)
	}
	return got.Incarnation
}

func TestUnloadCancelsGenerationDispatch(t *testing.T) {
	host, _ := newHost(t)
	spec := echoSpec(t, buildEchoPlugin(t))
	marker := filepath.Join(t.TempDir(), "entered")
	spec.Env = append(spec.Env, "ECHO_PLUGIN_CALL_MARKER="+marker)
	child := NewChildPlugin(spec, nil, nil)
	if err := host.Load(child); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Unload() })
	pid := child.pid()
	result := make(chan error, 1)
	go func() {
		_, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{ToolName: "tangent.echo_slow"})
		result <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never entered dispatch")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The fixture deliberately ignores cancellation. The released SDK reports
	// its failed graceful drain; the driver still kills and reaps the child.
	// Keep that failure visible instead of claiming a clean plugin/unload.
	var disposal driver.DisposalReport
	if err := child.Unload(); !errors.As(err, &disposal) || disposal.Incomplete || len(disposal.Failures) != 1 || disposal.Failures[0].Step != "unload" || disposal.Failures[0].Cause == nil {
		t.Fatalf("uncooperative unload did not retain its completed-disposal failure: %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrChildGone) {
			t.Fatalf("revoked call: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("revoked generation held its caller")
	}
	if alive(pid) || child.Restarts() != 0 {
		t.Fatal("intentional stop left or restarted child")
	}
}

func TestRestartUsesFreshIncarnationAndSharedHostEpoch(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)
	child := NewChildPlugin(echoSpec(t, binary), nil, nil)
	if err := host.Load(child); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Unload() })
	first := echoedIncarnation(t, child)
	if first.HostInstance != host.hostInstance || first.OwnerID != child.ID() {
		t.Fatalf("wrong tuple: %+v", first)
	}
	if err := killCurrent(child); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for child.pid() == 0 || child.lifecycle.Status().Owner.OwnerGeneration == first.OwnerGeneration {
		if time.Now().After(deadline) {
			t.Fatal("restart did not activate")
		}
		time.Sleep(10 * time.Millisecond)
	}
	second := echoedIncarnation(t, child)
	if second.HostInstance != first.HostInstance || second.OwnerID != first.OwnerID || second.OwnerGeneration <= first.OwnerGeneration {
		t.Fatalf("restart reused or changed authority: %+v -> %+v", first, second)
	}
	if err := host.Unload(child.ID()); err != nil {
		t.Fatal(err)
	}
	recreated := NewChildPlugin(echoSpec(t, binary), nil, nil)
	if err := host.Load(recreated); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recreated.Unload() })
	third := echoedIncarnation(t, recreated)
	if third.HostInstance != first.HostInstance || third.OwnerGeneration <= second.OwnerGeneration {
		t.Fatalf("controller recreation reused generation: %+v", third)
	}
}

func TestHandshakeFailuresAreTerminalAndRegisterNothing(t *testing.T) {
	binary := buildEchoPlugin(t)
	for _, test := range []struct{ name, env string }{
		{"protocol", "ECHO_PLUGIN_PROTOCOL=1"},
		{"contract", "ECHO_PLUGIN_CONTRACT=0"},
		{"identity", "ECHO_PLUGIN_ID=tangent.plugin.other"},
		{"version", "ECHO_PLUGIN_VERSION=0.2.0"},
		{"init", "ECHO_PLUGIN_INIT_ERROR=1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			host, _ := newHost(t)
			spec := echoSpec(t, binary)
			spec.Env = append(spec.Env, test.env)
			child := NewChildPlugin(spec, []MCPTool{{Name: "tangent.echo", Description: "Echo", InputSchema: json.RawMessage(`{"type":"object"}`)}}, nil)
			t.Cleanup(func() { _ = child.Unload() })
			err := host.Load(child)
			var failure *driver.Failure
			if !errors.As(err, &failure) || failure.Stage != driver.StageLoad || failure.Retryable {
				t.Fatalf("failure lost typed terminal stage: %v", err)
			}
			if test.name == "identity" || test.name == "version" {
				var detail *driver.MismatchError
				if !errors.As(err, &detail) || !strings.Contains(err.Error(), detail.Expected) || !strings.Contains(err.Error(), detail.Actual) {
					t.Fatalf("refusal lost expected/actual detail: %v", err)
				}
			}
			if child.Restarts() != 0 || child.pid() != 0 || len(host.MCPTools()) != 0 {
				t.Fatal("failed handshake activated, retried or registered")
			}
			if got := runningCount(t, binary); got != 0 {
				t.Fatalf("failed handshake left %d children", got)
			}
		})
	}
}

// Serve declines reserved profiles before writing InitResult; the host offers
// none and must observe that decline, not invent a profile acknowledgement.
func TestServeDeclinesReservedProfileWithoutHostOffers(t *testing.T) {
	host, _ := newHost(t)
	spec := echoSpec(t, buildEchoPlugin(t))
	spec.Env = append(spec.Env, "ECHO_PLUGIN_PROFILE=1")
	child := NewChildPlugin(spec, nil, nil)
	if err := host.Load(child); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Unload() })
	info := child.info()
	if info.HooksProfileVersion != nil || info.ReverseRPCVersion != nil {
		t.Fatalf("unimplemented profile selected: %+v", info)
	}
	echoedIncarnation(t, child)
}
