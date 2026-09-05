package smoke_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/health"
	"github.com/hollis-labs/tangent/internal/smoke"
)

// The four modes have to be distinguishable, not merely all non-zero. These
// tests are the contract for that: each drives one classifier into one mode
// with fabricated inputs, so the mapping is verified without a live
// deployment, a supervisor, or a catalog anyone else owns.

// TestSupervisorRunningButNotListeningIsProcessDown covers the gap that cost
// an incident.
//
// `cerberus resource status tangent-dev` reports `running` from supervisor
// bookkeeping. It does not dial the port. So `running` and `nothing is
// listening` are compatible answers, and an operator who queries only the
// supervisor concludes the process is fine. This is checked with a fabricated
// SupervisorState rather than by stopping anything, because proving a check
// can detect an outage must not require causing one.
func TestSupervisorRunningButNotListeningIsProcessDown(t *testing.T) {
	finding := smoke.ClassifySupervisor(smoke.SupervisorState{
		Resource:  "tangent-dev",
		Status:    "running",
		Listening: false,
		Address:   "http://127.0.0.1:7842",
	})
	if finding == nil {
		t.Fatal("supervisor `running` with nothing listening produced no finding")
	}
	if finding.Mode != smoke.ModeProcessDown {
		t.Fatalf("mode = %s, want %s", finding.Mode, smoke.ModeProcessDown)
	}
	rendered := finding.String()
	for _, want := range []string{"FAIL [PROCESS_DOWN]", "bookkeeping, not a live probe", "cerberus resource deploy tangent-dev"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("finding does not say %q:\n%s", want, rendered)
		}
	}
}

// TestSupervisorAgreementProducesNoFinding keeps the check from crying wolf on
// a healthy deployment.
func TestSupervisorAgreementProducesNoFinding(t *testing.T) {
	if finding := smoke.ClassifySupervisor(smoke.SupervisorState{
		Resource: "tangent-dev", Status: "running", Listening: true, Address: "http://127.0.0.1:7842",
	}); finding != nil {
		t.Fatalf("a running, listening resource produced a finding:\n%s", finding)
	}
}

// TestUnmanagedProcessHoldingThePortIsCatalogStale is the inverse discrepancy:
// something serves, but not the thing the supervisor owns, so a deploy will
// not replace it.
func TestUnmanagedProcessHoldingThePortIsCatalogStale(t *testing.T) {
	finding := smoke.ClassifySupervisor(smoke.SupervisorState{
		Resource: "tangent-dev", Status: "stopped", Listening: true, Address: "http://127.0.0.1:7842",
	})
	if finding == nil || finding.Mode != smoke.ModeCatalogStale {
		t.Fatalf("stopped-but-serving classified as %v, want %s", finding, smoke.ModeCatalogStale)
	}
}

// TestMissingCatalogEntryIsUpstreamAbsent is the "Tangent disappeared from
// mux" shape as it appears on disk.
func TestMissingCatalogEntryIsUpstreamAbsent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mcp-servers"), 0o750); err != nil {
		t.Fatalf("create catalog: %v", err)
	}
	entry, err := smoke.ReadCatalogEntry(root, "tangent")
	if err != nil {
		t.Fatalf("ReadCatalogEntry: %v", err)
	}
	if entry.Found {
		t.Fatal("an empty catalog reported an entry")
	}
	finding := smoke.ClassifyCatalogEntry(entry, "http://127.0.0.1:7842")
	if finding == nil || finding.Mode != smoke.ModeUpstreamAbsent {
		t.Fatalf("missing entry classified as %v, want %s", finding, smoke.ModeUpstreamAbsent)
	}
	if !strings.Contains(finding.String(), "FAIL [UPSTREAM_ABSENT]") {
		t.Errorf("finding does not lead with the mode:\n%s", finding)
	}
}

// TestCatalogEntryPointingElsewhereIsCatalogStale covers the entry that exists
// and is wrong — a different incident from the entry that does not exist.
func TestCatalogEntryPointingElsewhereIsCatalogStale(t *testing.T) {
	root := writeCatalogEntry(t, "id: tangent\ntransport: sse\nurl: http://127.0.0.1:9999/sse\nenabled: true\n")
	entry, err := smoke.ReadCatalogEntry(root, "tangent")
	if err != nil {
		t.Fatalf("ReadCatalogEntry: %v", err)
	}
	finding := smoke.ClassifyCatalogEntry(entry, "http://127.0.0.1:7842")
	if finding == nil || finding.Mode != smoke.ModeCatalogStale {
		t.Fatalf("misdirected entry classified as %v, want %s", finding, smoke.ModeCatalogStale)
	}
}

// TestDisabledCatalogEntryIsUpstreamAbsent — present but switched off is still
// "not among what the gateway serves".
func TestDisabledCatalogEntryIsUpstreamAbsent(t *testing.T) {
	root := writeCatalogEntry(t, "id: tangent\ntransport: sse\nurl: http://127.0.0.1:7842/sse\nenabled: false\n")
	entry, err := smoke.ReadCatalogEntry(root, "tangent")
	if err != nil {
		t.Fatalf("ReadCatalogEntry: %v", err)
	}
	finding := smoke.ClassifyCatalogEntry(entry, "http://127.0.0.1:7842")
	if finding == nil || finding.Mode != smoke.ModeUpstreamAbsent {
		t.Fatalf("disabled entry classified as %v, want %s", finding, smoke.ModeUpstreamAbsent)
	}
}

// TestMatchingCatalogEntryProducesNoFinding pins the pass case, including the
// `/sse` suffix a correct entry carries.
func TestMatchingCatalogEntryProducesNoFinding(t *testing.T) {
	root := writeCatalogEntry(t, "id: tangent\ntransport: sse\nurl: http://127.0.0.1:7842/sse\nenabled: true\n")
	entry, err := smoke.ReadCatalogEntry(root, "tangent")
	if err != nil {
		t.Fatalf("ReadCatalogEntry: %v", err)
	}
	if finding := smoke.ClassifyCatalogEntry(entry, "http://127.0.0.1:7842"); finding != nil {
		t.Fatalf("a correct catalog entry produced a finding:\n%s", finding)
	}
}

// TestGatewayAdvertisingNoTangentToolsIsUpstreamAbsent is the discovery-time
// form of the same disappearance: the gateway answered, and has nothing from
// this upstream.
func TestGatewayAdvertisingNoTangentToolsIsUpstreamAbsent(t *testing.T) {
	reference := smoke.NewSurface([]string{"tangent.health_report", "tangent.list_workflows"})
	finding := smoke.ClassifyGatewayDiscovery(reference, smoke.NewSurface(nil), "tether gateway")
	if finding == nil || finding.Mode != smoke.ModeUpstreamAbsent {
		t.Fatalf("empty gateway surface classified as %v, want %s", finding, smoke.ModeUpstreamAbsent)
	}
}

// TestGatewayAdvertisingAStaleSurfaceIsCatalogStale is criterion 4's other
// half: the gateway is still serving Tangent, from a list that no longer
// matches the build.
//
// This is not hypothetical. While this check was being written, `mux` reported
// Tangent with a cached tool_count of 25 against a shipped build advertising a
// surface twice that size.
func TestGatewayAdvertisingAStaleSurfaceIsCatalogStale(t *testing.T) {
	reference := smoke.NewSurface([]string{"tangent.health_report", "tangent.list_workflows", "tangent.hitl_get"})
	observed := smoke.NewSurface([]string{"tangent.list_workflows"})
	finding := smoke.ClassifyGatewayDiscovery(reference, observed, "tether gateway")
	if finding == nil || finding.Mode != smoke.ModeCatalogStale {
		t.Fatalf("stale gateway surface classified as %v, want %s", finding, smoke.ModeCatalogStale)
	}
	rendered := finding.String()
	for _, want := range []string{"FAIL [CATALOG_STALE]", "tangent.health_report", "absent there"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("finding does not say %q:\n%s", want, rendered)
		}
	}
}

// TestIdenticalSurfacesProduceNoFinding — a matching surface is the pass case,
// and the digest is what makes the comparison cheap to print.
func TestIdenticalSurfacesProduceNoFinding(t *testing.T) {
	names := []string{"tangent.health_report", "tangent.list_workflows"}
	first := smoke.NewSurface(names)
	second := smoke.NewSurface([]string{names[1], names[0]})
	if first.Digest != second.Digest {
		t.Fatalf("digest depends on input order: %q vs %q", first.Digest, second.Digest)
	}
	if finding := smoke.CompareSurface(first, second, "legacy /sse"); finding != nil {
		t.Fatalf("identical surfaces produced a finding:\n%s", finding)
	}
}

// TestFailedReadinessCheckIsCapabilityUnhealthy keeps a broken dependency out
// of the PROCESS_DOWN bucket. The process is answering; restarting it is the
// wrong action, and the finding carries the report's own action instead.
func TestFailedReadinessCheckIsCapabilityUnhealthy(t *testing.T) {
	report := health.ReadinessReport{
		Status: health.SummaryUnavailable,
		Probe:  health.ProbeReadiness,
		Checks: []health.Check{
			{Name: health.CheckDatabase, Status: health.StatusPass, Detail: "reachable"},
			{
				Name: health.CheckMigrations, Status: health.StatusFail,
				Detail: "schema at version 10, binary expects 12",
				Action: "Run `tangent --migrate-only` before serving.",
			},
		},
	}
	finding := smoke.ClassifyReadiness(report, "http://127.0.0.1:7842")
	if finding == nil || finding.Mode != smoke.ModeCapabilityUnhealthy {
		t.Fatalf("failed readiness check classified as %v, want %s", finding, smoke.ModeCapabilityUnhealthy)
	}
	rendered := finding.String()
	for _, want := range []string{"FAIL [CAPABILITY_UNHEALTHY]", "readiness: migrations", "tangent --migrate-only"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("finding does not say %q:\n%s", want, rendered)
		}
	}
}

// TestUnusableKindIsCapabilityUnhealthy covers the per-kind half.
func TestUnusableKindIsCapabilityUnhealthy(t *testing.T) {
	report := health.CapabilitySummaryReport{
		Probe: health.ProbeCapability, Status: health.SummaryDegraded,
		ManagedDefinitions: 18, Usable: 17,
		Unusable: []health.CapabilityReport{{
			Kind: "tangent.whiteboard", State: "quarantined",
			Action: "Widen host capability policy or re-materialize the definition.",
		}},
	}
	finding := smoke.ClassifyCapabilitySummary(report, "http://127.0.0.1:7842")
	if finding == nil || finding.Mode != smoke.ModeCapabilityUnhealthy {
		t.Fatalf("unusable kind classified as %v, want %s", finding, smoke.ModeCapabilityUnhealthy)
	}
	if !strings.Contains(finding.String(), "tangent.whiteboard(quarantined)") {
		t.Errorf("finding does not name the kind and its state:\n%s", finding)
	}
}

// TestWarningReadinessCheckIsReportedNotEscalated keeps the smoke check from
// paging on a degraded-but-serving host. internal/health's position is that an
// operator paged for every warning stops reading them, and a smoke check that
// disagreed would make the two answers drift.
func TestWarningReadinessCheckIsReportedNotEscalated(t *testing.T) {
	report := health.ReadinessReport{
		Status: health.SummaryDegraded, Probe: health.ProbeReadiness,
		Checks: []health.Check{
			{Name: health.CheckDatabase, Status: health.StatusPass, Detail: "reachable"},
			{
				Name: health.CheckRendererHost, Status: health.StatusWarn,
				Detail: "placeholder page present with 0 assets",
				Action: "Rebuild the embedded frontend.",
			},
		},
	}
	if finding := smoke.ClassifyReadiness(report, "http://127.0.0.1:7842"); finding != nil {
		t.Fatalf("a warning escalated to a finding:\n%s", finding)
	}
	degraded := smoke.DegradedChecks(report)
	if len(degraded) != 1 || !strings.Contains(degraded[0], health.CheckRendererHost) {
		t.Fatalf("DegradedChecks = %v, want the renderer host warning", degraded)
	}
}

// TestPassingReadinessProducesNoFinding pins the pass case.
func TestPassingReadinessProducesNoFinding(t *testing.T) {
	report := health.ReadinessReport{
		Status: health.SummaryOK, Probe: health.ProbeReadiness,
		Checks: []health.Check{{Name: health.CheckDatabase, Status: health.StatusPass, Detail: "reachable"}},
	}
	if finding := smoke.ClassifyReadiness(report, "http://127.0.0.1:7842"); finding != nil {
		t.Fatalf("a passing readiness report produced a finding:\n%s", finding)
	}
}

// TestFindingsSummarizeDistinctModes is what an operator reads first: one line
// naming which of the four buckets this run landed in.
func TestFindingsSummarizeDistinctModes(t *testing.T) {
	var findings smoke.Findings
	findings.Add(&smoke.Finding{Mode: smoke.ModeCatalogStale, Check: "a", Detail: "b", Action: "c"})
	findings.Add(&smoke.Finding{Mode: smoke.ModeProcessDown, Check: "d", Detail: "e", Action: "f"})
	findings.Add(&smoke.Finding{Mode: smoke.ModeCatalogStale, Check: "g", Detail: "h", Action: "i"})
	findings.Add(nil)

	if len(findings) != 3 {
		t.Fatalf("Add(nil) appended a finding: %d", len(findings))
	}
	modes := findings.Modes()
	if len(modes) != 2 || modes[0] != smoke.ModeProcessDown || modes[1] != smoke.ModeCatalogStale {
		t.Fatalf("modes = %v, want declaration order [PROCESS_DOWN CATALOG_STALE]", modes)
	}
	if !strings.Contains(findings.String(), "3 finding(s); modes: PROCESS_DOWN, CATALOG_STALE") {
		t.Errorf("summary line missing:\n%s", findings)
	}
	if smoke.Findings(nil).String() != "PASS" {
		t.Error("no findings should render as PASS")
	}
}

func writeCatalogEntry(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "mcp-servers")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create catalog: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tangent.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write catalog entry: %v", err)
	}
	return root
}
