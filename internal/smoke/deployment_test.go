package smoke_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/smoke"
)

// The environment-coupled half of the smoke check.
//
// Everything below reads a machine this repository does not own: a running
// Tangent, the Cerberus resource that supervises it, the Tether catalog that
// publishes it. None of it can run in CI, and none of it may run by accident,
// so all of it is behind one explicit gate:
//
//	TANGENT_SMOKE_ENV=1 make smoke
//
// The gate is opt-in rather than auto-detected on purpose. "Probe the live
// deployment if one happens to be listening" is how a test suite acquires a
// dependency nobody declared, and how a green CI run starts meaning something
// different on a laptop.
//
// Everything here is read-only. It starts no supervised process, stops none,
// deploys nothing, refreshes no catalog, and writes to no state directory the
// operator owns. The gateway arm runs the real gateway binary against a
// temporary catalog mirroring the operator's entry, so the process under test
// is the real one while the state it touches is disposable.

const (
	envGate     = "TANGENT_SMOKE_ENV"
	envURL      = "TANGENT_SMOKE_URL"
	envResource = "TANGENT_SMOKE_RESOURCE"
	envCatalog  = "TANGENT_SMOKE_CATALOG"
	envGateway  = "TANGENT_SMOKE_GATEWAY"
)

func requireEnvGate(t *testing.T) {
	t.Helper()
	if os.Getenv(envGate) != "1" {
		t.Skipf("environment-coupled check; set %s=1 to run it against a live deployment", envGate)
	}
}

func deployedURL() string {
	if url := os.Getenv(envURL); url != "" {
		return url
	}
	return "http://127.0.0.1:7842"
}

func managedResource() string {
	if resource := os.Getenv(envResource); resource != "" {
		return resource
	}
	return "tangent-dev"
}

func catalogRoot(t *testing.T) string {
	t.Helper()
	if root := os.Getenv(envCatalog); root != "" {
		return root
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve the Tether catalog root: %v", err)
	}
	return filepath.Join(home, ".tether", "catalog")
}

// TestDeployedTangentMatchesShippedBuild is the operator check.
//
// It answers the four questions in the order an incident asks them: is
// anything serving, does the supervisor agree, is what is serving the build we
// have, and can it do the work. Each answer is classified into one of the four
// modes, so a failed run says which of four different things to go fix instead
// of returning a bare non-zero.
//
// Run it before and after `cerberus resource deploy <resource>`: the surface
// comparison is what proves a restart actually picked up the binary on disk,
// which is the one thing a supervisor's own `running` cannot tell you.
func TestDeployedTangentMatchesShippedBuild(t *testing.T) {
	requireEnvGate(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// The reference always comes from the binary this checkout builds, never
	// from a number. Booting it on a reserved port against a temp database
	// leaves the live deployment untouched.
	reference, finding := shippedSurface(ctx, t)
	if finding != nil {
		t.Fatalf("could not derive the shipped build's surface:\n%s", finding)
	}
	t.Logf("shipped build (this checkout) advertises %s", reference.Describe())

	deployed := smoke.NewEndpoint(deployedURL())
	var findings smoke.Findings

	// 1. Is anything serving, and does the supervisor agree? This is the arm
	//    that catches `status: running` with nothing listening.
	listening := deployed.Listening(ctx)
	if status, ok := supervisorStatus(t, managedResource()); ok {
		t.Logf("supervisor reports %s status=%q; MCP listening=%v", managedResource(), status, listening)
		findings.Add(smoke.ClassifySupervisor(smoke.SupervisorState{
			Resource: managedResource(), Status: status, Listening: listening, Address: deployed.BaseURL,
		}))
	} else {
		t.Logf("no cerberus binary on PATH; the supervisor-vs-listener arm did not run")
	}
	findings.Add(deployed.Liveness(ctx))
	if !listening {
		// Nothing further is meaningful, and every later probe would report
		// the same outage three more times.
		t.Fatalf("deployment smoke failed:\n%s", findings)
	}

	// 2. Is what is serving the build we have?
	direct, finding := deployed.StreamableSurface(ctx)
	findings.Add(finding)
	if finding == nil {
		t.Logf("deployed /mcp advertises %s", direct.Describe())
		findings.Add(smoke.CompareSurface(reference, direct, "deployed /mcp"))
	}
	legacy, finding := deployed.LegacySSESurface(ctx)
	findings.Add(finding)
	if finding == nil {
		t.Logf("deployed /sse advertises %s", legacy.Describe())
		findings.Add(smoke.CompareSurface(reference, legacy, "deployed /sse"))
	}

	// 3. Can it do the work? Readiness, per-kind capability, and the one
	//    read-only tool call, which is the only one of the three a
	//    gateway-fronted agent can reach.
	readiness, finding := deployed.Readiness(ctx)
	findings.Add(finding)
	for _, degraded := range smoke.DegradedChecks(readiness) {
		// Reported, not escalated. A degraded host is serving.
		t.Logf("degraded: %s", degraded)
	}
	_, finding = deployed.CapabilitySummary(ctx)
	findings.Add(finding)
	report, finding := deployed.CallHealthReport(ctx)
	findings.Add(finding)
	if finding == nil {
		t.Logf("%s over deployed /mcp: host_version=%s readiness=%s",
			smoke.ReadOnlyProbeTool, report.HostVersion, report.Readiness.Status)
	}

	// 4. Is Tangent still published where the gateway looks for it?
	entry, err := smoke.ReadCatalogEntry(catalogRoot(t), "tangent")
	if err != nil {
		t.Errorf("read Tether catalog entry: %v", err)
	} else {
		findings.Add(smoke.ClassifyCatalogEntry(entry, deployed.BaseURL))
	}

	if len(findings) > 0 {
		t.Fatalf("deployment smoke failed:\n%s", findings)
	}
}

// TestTetherGatewayStillPublishesTangent drives the real gateway binary.
//
// It is gated twice — TANGENT_SMOKE_ENV plus a `mux` on PATH — because it
// starts a process. That process is given a temporary catalog carrying a copy
// of the operator's own Tangent entry, so the gateway dials the live
// deployment over the transport the operator configured while writing its
// session state somewhere disposable. Nothing under ~/.tether is modified.
//
// This is criterion 4's "disappears from mux" arm: a gateway that answers
// tools/list with no Tangent tools is UPSTREAM_ABSENT, and one that answers
// with a surface the shipped build does not have is CATALOG_STALE.
func TestTetherGatewayStillPublishesTangent(t *testing.T) {
	requireEnvGate(t)
	gateway := os.Getenv(envGateway)
	if gateway == "" {
		gateway = "mux"
	}
	resolved, err := exec.LookPath(gateway)
	if err != nil {
		t.Skipf("no %s gateway binary on PATH: %v", gateway, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	reference, finding := shippedSurface(ctx, t)
	if finding != nil {
		t.Fatalf("could not derive the shipped build's surface:\n%s", finding)
	}

	entry, err := smoke.ReadCatalogEntry(catalogRoot(t), "tangent")
	if err != nil {
		t.Fatalf("read Tether catalog entry: %v", err)
	}
	if classified := smoke.ClassifyCatalogEntry(entry, deployedURL()); classified != nil {
		t.Fatalf("the operator's catalog entry cannot reach this deployment:\n%s", classified)
	}

	root := t.TempDir()
	if mkErr := os.MkdirAll(filepath.Join(root, "mcp-servers"), 0o750); mkErr != nil {
		t.Fatalf("create disposable catalog: %v", mkErr)
	}
	global := fmt.Sprintf("version: 0.1.0\ncatalog:\n  defaults:\n    state_db: %q\n    workspace_root: %q\n    temp_root: %q\n",
		filepath.Join(root, "state", "tether.db"), filepath.Join(root, "workspaces"), filepath.Join(root, "tmp"))
	if writeErr := os.WriteFile(filepath.Join(root, "global.yaml"), []byte(global), 0o600); writeErr != nil {
		t.Fatalf("write disposable global catalog: %v", writeErr)
	}
	mirrored := fmt.Sprintf("id: tangent\ntransport: %s\nurl: %s\nenabled: true\n", entry.Transport, entry.URL)
	if writeErr := os.WriteFile(filepath.Join(root, "mcp-servers", "tangent.yaml"), []byte(mirrored), 0o600); writeErr != nil {
		t.Fatalf("write mirrored Tangent entry: %v", writeErr)
	}

	// #nosec G204,G702 -- the gateway binary is resolved from PATH (operator-chosen,
	// defaulting to `mux`), every other argument is a literal or this test's own
	// temp directory, and the subcommand is the read-only stdio proxy.
	command := exec.CommandContext(ctx, resolved, "--catalog", root, "mcp", "--proxy", "--only", "tangent")
	command.Env = append(os.Environ(), "HOLLIS_OTEL_DISABLED=1")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("gateway stdin: %v", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("gateway stdout: %v", err)
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if command.Process != nil {
			// This pid, and only this pid.
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	})

	client := &jsonRPCPipe{in: stdin, out: bufio.NewReader(stdout), stderr: &stderr}
	client.call(t, 1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "tangent-deployment-smoke", "version": "1"},
	})
	client.notify(t, "notifications/initialized", map[string]any{})

	listed := client.call(t, 2, "tools/list", map[string]any{})
	var names []string
	if result, ok := listed["result"].(map[string]any); ok {
		if tools, ok := result["tools"].([]any); ok {
			for _, raw := range tools {
				if tool, ok := raw.(map[string]any); ok {
					if name, ok := tool["name"].(string); ok {
						names = append(names, name)
					}
				}
			}
		}
	}
	observed := smoke.NewSurface(names)
	t.Logf("gateway advertises %s for the tangent upstream", observed.Describe())

	var findings smoke.Findings
	findings.Add(smoke.ClassifyGatewayDiscovery(reference, observed, "tether gateway"))

	// One read-only tool call through the gateway, so discovery is not the
	// only thing proven. A gateway can list a tool it can no longer forward.
	if observed.Count() > 0 {
		called := client.call(t, 3, "tools/call", map[string]any{
			"name": smoke.ReadOnlyProbeTool, "arguments": map[string]any{},
		})
		encoded, err := json.Marshal(called["result"])
		if err != nil {
			t.Fatalf("re-encode gateway tool result: %v", err)
		}
		var result struct {
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(encoded, &result); err != nil || result.IsError {
			findings.Add(&smoke.Finding{
				Mode:   smoke.ModeCapabilityUnhealthy,
				Check:  "tether gateway " + smoke.ReadOnlyProbeTool,
				Detail: fmt.Sprintf("the gateway forwarded the call but the host declined it: %s", string(encoded)),
				Action: "Read the deployment's own /readyz; the gateway is fine and the host is not.",
			})
		}
	}

	if len(findings) > 0 {
		t.Fatalf("gateway smoke failed:\n%s\ngateway stderr:\n%s", findings, stderr.String())
	}
}

// shippedSurface boots the binary this checkout builds and asks it what it
// advertises. It is the reference every comparison is made against.
func shippedSurface(ctx context.Context, t *testing.T) (smoke.Surface, *smoke.Finding) {
	t.Helper()
	return bootShippedBinary(t).StreamableSurface(ctx)
}

// supervisorStatus reads the supervisor's own word for the resource.
//
// It shells out to the read-only `cerberus resource status`, parses the
// `Status:` line, and reports whether it managed to. It never runs deploy,
// reload, stop, or remove: this check observes a deployment, it does not
// change one.
func supervisorStatus(t *testing.T, resource string) (string, bool) {
	t.Helper()
	binary, err := exec.LookPath("cerberus")
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "resource", "status", resource) // #nosec G204 -- a read-only status subcommand with an operator-supplied resource id.
	output, err := command.CombinedOutput()
	if err != nil {
		t.Logf("cerberus resource status %s failed: %v\n%s", resource, err, output)
		return "", false
	}
	for _, line := range strings.Split(string(output), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "Status:"); ok {
			return strings.TrimSpace(value), true
		}
	}
	t.Logf("cerberus resource status %s printed no Status line:\n%s", resource, output)
	return "", false
}

// jsonRPCPipe is a minimal newline-delimited JSON-RPC client over a
// subprocess's stdio. The gateway speaks stdio; this is the smallest thing
// that can hold a conversation with it.
type jsonRPCPipe struct {
	in     io.Writer
	out    *bufio.Reader
	stderr *strings.Builder
}

func (p *jsonRPCPipe) notify(t *testing.T, method string, params any) {
	t.Helper()
	p.write(t, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (p *jsonRPCPipe) call(t *testing.T, id int, method string, params any) map[string]any {
	t.Helper()
	p.write(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	for {
		line, err := p.out.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read gateway response for %s: %v\ngateway stderr:\n%s", method, err, p.stderr.String())
		}
		var response map[string]any
		if err := json.Unmarshal(line, &response); err != nil {
			continue
		}
		responseID, ok := response["id"].(float64)
		if !ok || int(responseID) != id {
			continue
		}
		if rpcError := response["error"]; rpcError != nil {
			t.Fatalf("gateway JSON-RPC %s error: %#v\ngateway stderr:\n%s", method, rpcError, p.stderr.String())
		}
		return response
	}
}

func (p *jsonRPCPipe) write(t *testing.T, message map[string]any) {
	t.Helper()
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal gateway request: %v", err)
	}
	if _, err := fmt.Fprintf(p.in, "%s\n", raw); err != nil {
		t.Fatalf("write gateway request: %v\ngateway stderr:\n%s", err, p.stderr.String())
	}
}
