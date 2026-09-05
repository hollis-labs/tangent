package health

import (
	"context"
	"sync"

	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/telemetry"
)

// Linking a health failure to the rest of its story.
//
// A health report says what is wrong now. It does not say when it started,
// how often it has happened, or what a caller saw while it was happening —
// and those are the three questions an operator asks next. Before this, the
// only way to answer them was to grep stderr and hope the process had not
// restarted.
//
// So every non-passing answer carries a correlation identifier: the trace its
// own history is filed under, and the tool that reads it. The identifier is
// derived — `TraceForCheck(name)` for a readiness check, `TraceForKind(kind)`
// for a capability — so the same failing check always names the same trace,
// and successive failures accumulate under it rather than each one being an
// island.
//
// What a correlation identifier is *not* is a way back to a payload. It is a
// digest of a check name or a kind name, both of which are already in the
// report beside it. It grants nothing, discloses nothing, and is safe in the
// same document as everything else here — which is the point of criterion 5's
// word "safe".

// telemetryTool names the reader. It is a constant here rather than a string
// assembled at the call site so the report cannot advertise a tool that does
// not exist.
const telemetryTool = "tangent.telemetry_query"

// Correlation is where the rest of a failure's story is.
type Correlation struct {
	// TraceID is the 32-hex identity every observation about this check or
	// kind is filed under.
	TraceID string `json:"trace_id"`
	// Tool is what reads it.
	Tool string `json:"telemetry_tool"`
	// Subject is the check name or interaction kind this trace covers, so a
	// reader holding only the correlation block knows what it describes.
	Subject string `json:"subject"`
}

// correlationForCheck builds the link for one readiness check.
func correlationForCheck(name string) *Correlation {
	return &Correlation{
		TraceID: telemetry.TraceForCheck(name).String(),
		Tool:    telemetryTool,
		Subject: name,
	}
}

// correlationForKind builds the link for one interaction kind.
func correlationForKind(kind string) *Correlation {
	return &Correlation{
		TraceID: telemetry.TraceForKind(kind).String(),
		Tool:    telemetryTool,
		Subject: kind,
	}
}

// WithTelemetry installs the correlation recorder.
//
// The reporter emits on *transitions* only. A managed runtime polls readiness
// on a timer, and a row per poll would bury the moment a dependency changed
// under thousands of rows saying it had not — which is the same defect, in a
// different table, as a `/healthz` that returns 200 while the database is
// gone.
func WithTelemetry(recorder *telemetry.Recorder) Option {
	return func(r *Reporter) { r.telemetry = recorder }
}

// checkTransitions remembers the last verdict reported for each check, so a
// change can be told from a repeat.
type checkTransitions struct {
	mu   sync.Mutex
	last map[string]Status
}

// changed reports whether this check's verdict differs from the last one
// observed, and records the new one. The first observation of a passing check
// is deliberately not a change: a process that boots healthy should produce no
// rows at all.
func (t *checkTransitions) changed(name string, status Status) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil {
		t.last = map[string]Status{}
	}
	previous, seen := t.last[name]
	t.last[name] = status
	if !seen {
		return status != StatusPass
	}
	return previous != status
}

// observeChecks records the checks whose verdict changed.
func (r *Reporter) observeChecks(ctx context.Context, report ReadinessReport) {
	if r.telemetry == nil {
		return
	}
	for _, check := range report.Checks {
		if !r.transitions.changed(check.Name, check.Status) {
			continue
		}
		outcome := telemetry.OutcomeOK
		if check.Status == StatusFail {
			outcome = telemetry.OutcomeFailed
		}
		r.telemetry.Emit(ctx, telemetry.Event{
			Name:    telemetry.EventReadinessDegraded,
			Outcome: outcome,
			// The code is the check that changed. The detail and the operator
			// action stay in the report and never reach telemetry: they are
			// host-authored sentences, and ADR 0002 §8's rule is about the
			// class of field, not about who wrote it.
			Code: telemetry.Code(check.Name),
			Correlation: telemetry.Correlation{
				Trace: telemetry.TraceForCheck(check.Name),
				Span:  telemetry.NewSpanID(),
			},
			Attrs: []telemetry.Attr{
				telemetry.String(telemetry.AttrProbe, ProbeReadiness),
				telemetry.String(telemetry.AttrVerdict, string(check.Status)),
			},
		})
	}
}

// ObserveDefinitions records every interaction kind this build cannot serve.
//
// It is called once at boot, from the composition root, because that is when
// the answer is complete and stable: materialization has finished, the host
// policy has been applied, and nothing later changes a definition's state
// without a redeploy. Emitting it here means a health report naming a broken
// kind points at a trace that already has the boot-time reason in it, rather
// than at an empty trace that only fills up once a caller trips over the kind.
func (r *Reporter) ObserveDefinitions(ctx context.Context) {
	if r == nil || r.telemetry == nil || r.registry == nil {
		return
	}
	for _, materialized := range r.registry.MaterializedDefinitions() {
		if materialized.State.Servable() || materialized.Manifest == nil {
			continue
		}
		r.telemetry.Emit(ctx, telemetry.Event{
			Name:    telemetry.EventRendererUnavailable,
			Outcome: telemetry.OutcomeRefused,
			Code:    telemetry.Code(materialized.ErrorCode),
			Correlation: telemetry.Correlation{
				Trace:             telemetry.TraceForKind(materialized.Manifest.Kind),
				Span:              telemetry.NewSpanID(),
				DefinitionKind:    materialized.Manifest.Kind,
				DefinitionVersion: materialized.Manifest.Version,
			},
			Attrs: []telemetry.Attr{
				telemetry.String(telemetry.AttrMaterializationState, string(materialized.State)),
				telemetry.String(telemetry.AttrTrustClass, string(materialized.TrustClass)),
				telemetry.String(telemetry.AttrIsolation, string(materialized.Isolation)),
				telemetry.String(telemetry.AttrDeniedBy, deniedBy(materialized)),
				telemetry.String(telemetry.AttrProbe, ProbeCapability),
			},
		})
	}
}

// deniedBy separates the two denial rules at the one point where the host can
// tell them apart with certainty.
//
// At materialization the registry holds both sets — what the renderer's trust
// class refused, and what host policy refused — and internal/health already
// reports them as separate grants. Recording the split here, at boot, is what
// lets an operator see which of the two fixes applies without first
// reproducing the failure through a caller.
func deniedBy(materialized definition.Materialized) string {
	if len(materialized.TrustDeniedCapabilities) > 0 {
		return "trust-class"
	}
	if len(materialized.DeniedCapabilities) > 0 {
		return "host-policy"
	}
	return ""
}
