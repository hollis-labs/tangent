package smoke_test

import (
	"context"
	"io/fs"
	"strconv"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/health"
	"github.com/hollis-labs/tangent/internal/smoke"
	"github.com/hollis-labs/tangent/internal/webui"
)

// TestShippedBuildServesTheMCPSurface is the CI-safe half of the smoke check.
//
// It depends on nothing outside this repository: no Cerberus resource, no
// Tether catalog, no running instance, no port anyone else owns. It builds the
// binary, boots it against a database in a temp directory on a port it
// reserved, and asks it the four questions a consumer asks — is it alive, is
// it ready, what does /mcp advertise, and does a read-only tool call come
// back. (The legacy /sse transport is gone with the port; /mcp is the one
// transport, so there is no parity to check.)
func TestShippedBuildServesTheMCPSurface(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the shipped binary; skipped under -short")
	}
	endpoint := bootShippedBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var findings smoke.Findings
	findings.Add(endpoint.Liveness(ctx))

	readiness, finding := endpoint.Readiness(ctx)
	if !onlyUnbuiltRenderer(t, readiness) {
		findings.Add(finding)
	}
	for _, degraded := range smoke.DegradedChecks(readiness) {
		t.Logf("degraded: %s", degraded)
	}

	_, finding = endpoint.CapabilitySummary(ctx)
	findings.Add(finding)

	direct, finding := endpoint.StreamableSurface(ctx)
	findings.Add(finding)

	if len(findings) > 0 {
		report, _ := endpoint.CallHealthReport(ctx)
		for _, record := range report.Plugins.Plugins {
			if record.Error != "" {
				t.Logf("plugin %s refused: %s", record.ID, record.Error)
			}
		}
		t.Fatalf("shipped build smoke failed:\n%s", findings)
	}

	t.Logf("shipped build advertises %s over /mcp", direct.Describe())

	// The surface has to actually contain the tools this check reasons about,
	// or a future refactor could satisfy parity with an empty registry.
	for _, required := range []string{smoke.ReadOnlyProbeTool, "tangent_workflow_list"} {
		if !contains(direct.Names, required) {
			t.Errorf("shipped build does not advertise %s; the smoke check cannot probe what it cannot call", required)
		}
	}
	if readiness.Status != health.SummaryOK && !onlyUnbuiltRenderer(t, readiness) {
		t.Errorf("readiness on a fresh temp database = %q, want %q", readiness.Status, health.SummaryOK)
	}
}

// TestReadOnlyToolCallAnswersOverMCP drives the one read-only tool call.
//
// It is `tangent_health_get` for the reason smoke.ReadOnlyProbeTool gives:
// it is the only candidate whose answer distinguishes CAPABILITY_UNHEALTHY
// from a transport that merely carried bytes. It is also the path a
// gateway-fronted agent takes, which has no HTTP client for /api/health/ready.
func TestReadOnlyToolCallAnswersOverMCP(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the shipped binary; skipped under -short")
	}
	endpoint := bootShippedBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	httpReadiness, finding := endpoint.Readiness(ctx)
	if finding != nil && !onlyUnbuiltRenderer(t, httpReadiness) {
		t.Fatalf("readiness probe failed:\n%s", finding)
	}

	direct, finding := endpoint.CallHealthReport(ctx)
	if finding != nil && !onlyUnbuiltRenderer(t, direct.Readiness) {
		t.Fatalf("read-only tool call over /mcp failed:\n%s", finding)
	}

	// One health implementation, reported over two channels. Two answers
	// that can disagree is the defect internal/health closed; this keeps it
	// closed across the transports as well as within the process.
	if direct.Readiness.Status != httpReadiness.Status {
		t.Errorf("%s over /mcp reports readiness %q, /api/health/ready reports %q",
			smoke.ReadOnlyProbeTool, direct.Readiness.Status, httpReadiness.Status)
	}
	if direct.HostVersion == "" {
		t.Error("the host version over /mcp is empty")
	}
	if direct.CapabilitySummary == nil {
		t.Fatalf("%s returned no capability summary", smoke.ReadOnlyProbeTool)
	}
	// Plugin legibility, proved against the shipped binary (CW-20260910-0036).
	// A tool list cannot answer this. A plugin may register no tool at all — a
	// kind-only plugin registers none by construction — and a plugin that
	// REFUSED to load registers none either, which is the case an operator most
	// needs told apart from a healthy one.
	if direct.Plugins.Loaded == 0 {
		t.Errorf("%s reports no loaded plugins, but this build ships them; "+
			"the inventory is what makes a plugin visible when its tools cannot", smoke.ReadOnlyProbeTool)
	}
	if direct.Plugins.Refused != 0 {
		t.Errorf("%s reports %d refused plugin(s): %+v",
			smoke.ReadOnlyProbeTool, direct.Plugins.Refused, direct.Plugins.Plugins)
	}
	if direct.Plugins.Attribution == "" {
		t.Error("the plugin inventory does not say its contributed lists are host-wide")
	}
	t.Logf("%s: readiness=%s managed_definitions=%d usable=%d plugins_loaded=%d kinds=%v",
		smoke.ReadOnlyProbeTool, direct.Readiness.Status,
		direct.CapabilitySummary.ManagedDefinitions, direct.CapabilitySummary.Usable,
		direct.Plugins.Loaded, direct.Plugins.ContributedKinds)
}

// TestProcessDownIsDistinguishableFromEverythingElse points the probes at a
// port nothing is serving.
//
// This is the "process down" arm of the four-mode contract, and it is checked
// against a port this test reserved and released rather than by stopping
// anything: a smoke check must never have to take a deployment down to prove
// it can tell when one is down.
func TestProcessDownIsDistinguishableFromEverythingElse(t *testing.T) {
	endpoint := smoke.NewEndpoint("http://127.0.0.1:" + strconv.Itoa(reservePort(t)))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if endpoint.Listening(ctx) {
		t.Skip("the reserved port was taken between release and probe")
	}
	if endpoint.PortOpen(ctx) {
		t.Skip("the reserved port was taken between release and probe")
	}

	var findings smoke.Findings
	findings.Add(endpoint.Liveness(ctx))
	_, finding := endpoint.StreamableSurface(ctx)
	findings.Add(finding)

	if len(findings) != 2 {
		t.Fatalf("expected liveness and /mcp to each report a finding, got:\n%s", findings)
	}
	for _, found := range findings {
		if found.Mode != smoke.ModeProcessDown {
			t.Errorf("probe against a closed port classified as %s, want %s:\n%s",
				found.Mode, smoke.ModeProcessDown, found)
		}
	}
}

func contains(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// onlyUnbuiltRenderer reports that the one thing failing is the renderer host, in a build whose
// frontend was never embedded. The SPA is built by the frontend repository and embedded into
// internal/webui/dist for a release; a backend-only build has none, and that is expected here.
func onlyUnbuiltRenderer(t *testing.T, report health.ReadinessReport) bool {
	t.Helper()
	sub, err := webui.Assets()
	if err != nil {
		return false
	}
	if _, err = fs.Stat(sub, "index.html"); err == nil {
		return false
	}
	failed := 0
	for _, check := range report.Checks {
		if check.Status == health.StatusFail {
			failed++
			if check.Name != health.CheckRendererHost {
				return false
			}
		}
	}
	if failed > 0 {
		t.Log("the renderer host check fails: this build embeds no frontend (expected for a backend-only build)")
	}
	return failed > 0
}
