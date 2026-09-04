package health

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/telemetry"
)

// Criterion 5: a health failure names where the rest of its story is, in an
// identifier that is safe to publish in the same document as everything else
// here.

// TestFailingChecksCarryACorrelationIdentifier is the contract. A passing check
// carries none, because a passing check has no incident; a failing one carries
// the trace its own history is filed under and the tool that reads it.
func TestFailingChecksCarryACorrelationIdentifier(t *testing.T) {
	t.Parallel()
	reporter := NewReporter(
		WithDatabase(migratedDB(t)),
		WithDefinitionRegistry(availableRegistry(t)),
		// No renderer host and no delivery worker: two failures, one pass, so
		// the presence and the absence of the link are both asserted in one
		// report.
		WithTelemetry(telemetry.New()),
	)
	report := reporter.Readiness(context.Background())

	database := checkNamed(t, report, CheckDatabase)
	if database.Status != StatusPass {
		t.Fatalf("database check = %+v, want pass", database)
	}
	if database.Correlation != nil {
		t.Fatalf("a passing check carried a correlation block: %+v", database.Correlation)
	}

	renderer := checkNamed(t, report, CheckRendererHost)
	if renderer.Status != StatusFail {
		t.Fatalf("renderer check = %+v, want fail", renderer)
	}
	if renderer.Correlation == nil {
		t.Fatal("a failing check carried no correlation identifier")
	}
	if renderer.Correlation.TraceID != telemetry.TraceForCheck(CheckRendererHost).String() {
		t.Fatalf("correlation trace = %q, want the renderer_host check trace",
			renderer.Correlation.TraceID)
	}
	if renderer.Correlation.Tool != telemetryTool {
		t.Fatalf("correlation names %q as the reader", renderer.Correlation.Tool)
	}
	if renderer.Correlation.Subject != CheckRendererHost {
		t.Fatalf("correlation subject = %q", renderer.Correlation.Subject)
	}
}

// TestReadinessObservesTransitionsRatherThanSamples is why a supervisor
// polling every few seconds does not fill the audit table.
func TestReadinessObservesTransitionsRatherThanSamples(t *testing.T) {
	t.Parallel()
	sink := &recordingSink{}
	reporter := NewReporter(
		WithDatabase(migratedDB(t)),
		WithDefinitionRegistry(availableRegistry(t)),
		WithDeliveryWorker(func() DeliveryWorker { return DeliveryWorker{Authorized: true} }),
		WithTelemetry(telemetry.New(telemetry.WithSink(sink))),
	)
	for range 5 {
		reporter.Readiness(context.Background())
	}
	// One failing check (renderer host is unwired), observed once — not five
	// times, and not once per passing check either.
	if len(sink.records) != 1 {
		t.Fatalf("five probes produced %d observations, want 1", len(sink.records))
	}
	observation := sink.records[0]
	if observation.Name != telemetry.EventReadinessDegraded {
		t.Fatalf("observation = %q", observation.Name)
	}
	if observation.Code != CheckRendererHost {
		t.Fatalf("observation code = %q, want the check that changed", observation.Code)
	}
	if observation.TraceID != telemetry.TraceForCheck(CheckRendererHost).String() {
		t.Fatalf("observation trace = %q, want the check's own trace", observation.TraceID)
	}
}

// TestUnusableKindsNameTheirOwnTrace links a capability failure to the boot
// observation that recorded why.
func TestUnusableKindsNameTheirOwnTrace(t *testing.T) {
	t.Parallel()
	quarantined := materialize(t, edited(t, "required_capabilities: []",
		"required_capabilities:\n  - id: file.read_scoped\n    optional: false"), hostPolicy())
	if quarantined.State != definition.StateQuarantined {
		t.Fatalf("fixture state = %q, want quarantined", quarantined.State)
	}
	sink := &recordingSink{}
	reporter := NewReporter(
		WithDefinitionRegistry(registry{materialized: []definition.Materialized{quarantined}}),
		WithTelemetry(telemetry.New(telemetry.WithSink(sink))),
	)
	reporter.ObserveDefinitions(context.Background())

	report := reporter.Capability(quarantined.Manifest.Kind)
	if report.Usable {
		t.Fatal("the quarantined fixture reported usable")
	}
	if report.Correlation == nil {
		t.Fatal("an unusable kind carried no correlation identifier")
	}
	want := telemetry.TraceForKind(quarantined.Manifest.Kind).String()
	if report.Correlation.TraceID != want {
		t.Fatalf("capability trace = %q, want %q", report.Correlation.TraceID, want)
	}
	if len(sink.records) != 1 || sink.records[0].TraceID != want {
		t.Fatalf("the boot observation is not filed under the trace the report publishes: %+v",
			sink.records)
	}
	// The split ADR 0003 §2.5 requires. This fixture's class permits the
	// capability and standalone host policy grants nothing, so it is a
	// host-policy denial — the one an operator *can* lift.
	if sink.records[0].Attributes["denied_by"] != "host-policy" {
		t.Fatalf("denial attribution = %v, want host-policy", sink.records[0].Attributes)
	}
}

// TestTrustDenialIsAttributedSeparatelyFromHostPolicy is the other half of the
// split, and the one that matters: widening host policy does nothing to a
// capability the renderer's trust class refused, so an observation that
// collapsed the two would send an operator to the wrong fix.
func TestTrustDenialIsAttributedSeparatelyFromHostPolicy(t *testing.T) {
	t.Parallel()
	source := strings.NewReplacer(
		"class: react-component", "class: sandboxed-frame",
		"trust_class: core-trusted", "trust_class: sandboxed-code",
		"required_capabilities: []",
		"required_capabilities:\n  - id: process.exec\n    optional: false",
	).Replace(fixtureManifest)
	item := materialize(t, source, hostPolicy())
	if item.State != definition.StateQuarantined {
		t.Fatalf("fixture state = %q, want quarantined", item.State)
	}

	sink := &recordingSink{}
	NewReporter(
		WithDefinitionRegistry(registry{materialized: []definition.Materialized{item}}),
		WithTelemetry(telemetry.New(telemetry.WithSink(sink))),
	).ObserveDefinitions(context.Background())

	if len(sink.records) != 1 {
		t.Fatalf("observations = %+v, want one", sink.records)
	}
	if sink.records[0].Attributes["denied_by"] != "trust-class" {
		t.Fatalf("denial attribution = %v, want trust-class", sink.records[0].Attributes)
	}
}

// TestCorrelationIdentifiersDiscloseNothing keeps criterion 5's word "safe"
// honest: the identifier is a digest of a name that is already in the report
// beside it.
func TestCorrelationIdentifiersDiscloseNothing(t *testing.T) {
	t.Parallel()
	reporter := NewReporter(
		WithDatabase(migratedDB(t)),
		WithDefinitionRegistry(availableRegistry(t)),
		WithTelemetry(telemetry.New()),
	)
	encoded, err := json.Marshal(reporter.Readiness(context.Background()))
	if err != nil {
		t.Fatalf("marshal readiness: %v", err)
	}
	// dbPathToken is planted in the database path by migratedDB. Its presence
	// anywhere in a report — including in a correlation block — is the leak
	// this package has always tested for, extended to the new fields.
	for _, forbidden := range []string{dbPathToken, ".db", "/var/folders", "/tmp/", "sqlite3"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("readiness report carried %q:\n%s", forbidden, encoded)
		}
	}
}

// recordingSink captures observations without a database, so a health test
// stays a health test.
type recordingSink struct{ records []telemetry.Record }

func (s *recordingSink) Append(_ context.Context, record telemetry.Record) error {
	s.records = append(s.records, record)
	return nil
}
