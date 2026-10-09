package smoke_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/health"
	"github.com/hollis-labs/tangent/internal/smoke"
)

// TestShippedBuildServesBothTransports is the CI-safe half of the smoke check.
//
// It depends on nothing outside this repository: no Cerberus resource, no
// Tether catalog, no running instance, no port anyone else owns. It builds the
// binary, boots it against a database in a temp directory on a port it
// reserved, and asks it the four questions a consumer asks — is it alive, is
// it ready, what does each transport advertise, and does a read-only tool call
// come back.
//
// The transport-parity assertion is the one that earns its keep. `/mcp` and
// `/sse` are the same registry behind two handlers, so they cannot legitimately
// differ; when they do, one of them is serving something else, and the Tether
// gateway dials `/sse` — the one nobody probes by hand.
func TestShippedBuildServesBothTransports(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the shipped binary; skipped under -short")
	}
	endpoint := bootShippedBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var findings smoke.Findings
	findings.Add(endpoint.Liveness(ctx))

	readiness, finding := endpoint.Readiness(ctx)
	findings.Add(finding)
	for _, degraded := range smoke.DegradedChecks(readiness) {
		t.Logf("degraded: %s", degraded)
	}

	_, finding = endpoint.CapabilitySummary(ctx)
	findings.Add(finding)

	direct, finding := endpoint.StreamableSurface(ctx)
	findings.Add(finding)

	legacy, finding := endpoint.LegacySSESurface(ctx)
	findings.Add(finding)

	// Parity, not a count. The reference is whatever the binary just said, so
	// there is no number here to go stale.
	findings.Add(smoke.CompareSurface(direct, legacy, "legacy /sse"))

	if len(findings) > 0 {
		report, _ := endpoint.CallHealthReport(ctx)
		for _, record := range report.Plugins.Plugins {
			if record.Error != "" {
				t.Logf("plugin %s refused: %s", record.ID, record.Error)
			}
		}
		t.Fatalf("shipped build smoke failed:\n%s", findings)
	}

	t.Logf("shipped build advertises %s over /mcp and %s over /sse",
		direct.Describe(), legacy.Describe())

	// The surface has to actually contain the tools this check reasons about,
	// or a future refactor could satisfy parity with an empty registry.
	for _, required := range []string{smoke.ReadOnlyProbeTool, "tangent.list_workflows"} {
		if !contains(direct.Names, required) {
			t.Errorf("shipped build does not advertise %s; the smoke check cannot probe what it cannot call", required)
		}
	}
	if readiness.Status != health.SummaryOK {
		t.Errorf("readiness on a fresh temp database = %q, want %q", readiness.Status, health.SummaryOK)
	}
}

// TestReadOnlyToolCallAnswersOnBothTransports drives the one read-only tool
// call over each transport.
//
// It is `tangent.health_report` for the reason smoke.ReadOnlyProbeTool gives:
// it is the only candidate whose answer distinguishes CAPABILITY_UNHEALTHY
// from a transport that merely carried bytes. Calling it over `/sse` as well
// as `/mcp` covers the path a gateway-fronted agent actually takes, which has
// no HTTP client for /readyz.
func TestReadOnlyToolCallAnswersOnBothTransports(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the shipped binary; skipped under -short")
	}
	endpoint := bootShippedBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	httpReadiness, finding := endpoint.Readiness(ctx)
	if finding != nil {
		t.Fatalf("readiness probe failed:\n%s", finding)
	}

	direct, finding := endpoint.CallHealthReport(ctx)
	if finding != nil {
		t.Fatalf("read-only tool call over /mcp failed:\n%s", finding)
	}
	legacy, finding := endpoint.CallHealthReportOverSSE(ctx)
	if finding != nil {
		t.Fatalf("read-only tool call over /sse failed:\n%s", finding)
	}

	// One health implementation, reported over three channels. Two answers
	// that can disagree is the defect internal/health closed; this keeps it
	// closed across the transports as well as within the process.
	if direct.Readiness.Status != httpReadiness.Status {
		t.Errorf("%s over /mcp reports readiness %q, /readyz reports %q",
			smoke.ReadOnlyProbeTool, direct.Readiness.Status, httpReadiness.Status)
	}
	if legacy.Readiness.Status != httpReadiness.Status {
		t.Errorf("%s over /sse reports readiness %q, /readyz reports %q",
			smoke.ReadOnlyProbeTool, legacy.Readiness.Status, httpReadiness.Status)
	}
	if direct.HostVersion == "" || direct.HostVersion != legacy.HostVersion {
		t.Errorf("host version over /mcp = %q, over /sse = %q; want one non-empty value",
			direct.HostVersion, legacy.HostVersion)
	}
	if direct.CapabilitySummary == nil || legacy.CapabilitySummary == nil {
		t.Fatalf("%s returned no capability summary (mcp=%v sse=%v)",
			smoke.ReadOnlyProbeTool, direct.CapabilitySummary != nil, legacy.CapabilitySummary != nil)
	}
	if direct.CapabilitySummary.ManagedDefinitions != legacy.CapabilitySummary.ManagedDefinitions {
		t.Errorf("managed definition count differs by transport: /mcp %d, /sse %d",
			direct.CapabilitySummary.ManagedDefinitions, legacy.CapabilitySummary.ManagedDefinitions)
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
	if direct.Plugins.Loaded != legacy.Plugins.Loaded {
		t.Errorf("loaded plugin count differs by transport: /mcp %d, /sse %d",
			direct.Plugins.Loaded, legacy.Plugins.Loaded)
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
	_, finding = endpoint.LegacySSESurface(ctx)
	findings.Add(finding)

	if len(findings) != 3 {
		t.Fatalf("expected liveness, /mcp and /sse to each report a finding, got:\n%s", findings)
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
