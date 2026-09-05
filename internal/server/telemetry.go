package server

import (
	"context"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/effect"
	"github.com/hollis-labs/tangent/internal/telemetry"
)

// Capability denials are recorded at the transports, not inside authz.
//
// internal/authz.Authorize is a pure function with no context, no clock, and
// no dependencies — which is exactly why every transport can route its
// refusals through it. Threading a recorder into it would make the one
// decision point in the system depend on an observation side effect, so the
// observation is made where the refusal becomes a response instead: here, in
// internal/ws, and in internal/mcp.
//
// The cost of that choice, stated plainly: a refusal that some future code
// path takes from authz.Authorize without reporting it is invisible to the
// counter. The three call sites that turn a refusal into a wire response are
// covered, and a fourth would have to be added deliberately.

// reportObjectAccessDenial records a browser request refused for lack of an
// object-access capability.
//
// It carries the capability being exercised, which is a host-published name,
// and nothing about the session — not its id, not its hash, not what it does
// hold. ADR 0004 §6.5 fixes the *message* a refusal may carry for exactly the
// reason ADR 0002 §8 fixes what telemetry may carry, and the two rules meet
// here.
func reportObjectAccessDenial(
	ctx context.Context,
	recorder *telemetry.Recorder,
	capability authz.Capability,
	transport string,
) {
	recorder.Emit(ctx, telemetry.Event{
		Name:    telemetry.EventCapabilityDenied,
		Outcome: telemetry.OutcomeRefused,
		Code:    "forbidden",
		// Filed under a fixed subject trace rather than an invocation's. A
		// refused request never named an interaction the host was willing to
		// confirm exists — that is the refusal — so there is nothing to
		// correlate it to, and grouping every gate denial under one stable
		// trace is what makes "is something hammering the browser API"
		// answerable.
		Correlation: telemetry.Correlation{
			Trace: telemetry.TraceForCheck("participant_session"),
			Span:  telemetry.NewSpanID(),
		},
		Attrs: []telemetry.Attr{
			telemetry.String(telemetry.AttrNamespace, "object-access"),
			telemetry.String(telemetry.AttrCapability, string(capability)),
			telemetry.String(telemetry.AttrTransport, transport),
		},
	})
}

// reportEffectRefusal records one host-mediated effect the broker declined.
//
// The receipt is already the durable, payload-free record of the decision
// (ADR 0003 §2.5), so this adds one thing the receipt cannot: the denial is
// counted in the same instrument as an object-access denial, dimensioned by
// namespace, so an operator can ask "what is being refused" once instead of
// twice. Everything else is read off the receipt — never off the content, the
// handle, or the request body.
//
// The handle id is deliberately not recorded. It is on the receipt, where it
// is the scope an effect was performed against, and it is capability-adjacent
// material that has no business in a metric dimension or a span attribute.
func reportEffectRefusal(
	ctx context.Context,
	recorder *telemetry.Recorder,
	receipt effect.Receipt,
	binding effect.Binding,
	ownerScope string,
) {
	if receipt.Decision != effect.DecisionRefused {
		return
	}
	recorder.Emit(ctx, telemetry.Event{
		Name:    telemetry.EventCapabilityDenied,
		Outcome: telemetry.OutcomeRefused,
		Code:    telemetry.Code(receipt.Code),
		Correlation: telemetry.Correlation{
			Trace:             telemetry.TraceForKind(binding.Kind),
			Span:              telemetry.NewSpanID(),
			InteractionID:     receipt.InteractionID,
			DefinitionKind:    binding.Kind,
			DefinitionVersion: binding.Version,
			CallerScope:       ownerScope,
			ParticipantScope:  receipt.ParticipantScope,
		},
		Attrs: []telemetry.Attr{
			telemetry.String(telemetry.AttrNamespace, "effect"),
			telemetry.String(telemetry.AttrCapability, string(receipt.Capability)),
			telemetry.String(telemetry.AttrDeniedBy, deniedBy(receipt, binding)),
			telemetry.String(telemetry.AttrTrustClass, receipt.TrustClass),
			telemetry.String(telemetry.AttrIsolation, string(receipt.Isolation)),
			telemetry.String(telemetry.AttrTransport, "browser-api"),
		},
	})
}

// deniedBy separates a trust-class refusal from a host-policy refusal.
//
// The two must stay apart: widening host policy lifts one of them and does
// nothing at all to the other, and an operator who is told only
// "capability_denied" will try the wrong fix first. internal/health already
// reports the split at materialization time, where the registry holds both
// denial sets; at *request* time the pinned binding carries only `required`
// and `granted`, so the split is recomputed here from the pinned trust class's
// own ceiling — the same ceiling materialization applied.
//
// That recomputation is exact for the shipped classes and is the honest
// answer for a pinned class this build no longer implements: an unknown class
// has no profile, so nothing is attributable to it and the denial is reported
// as host policy rather than guessed.
func deniedBy(receipt effect.Receipt, binding effect.Binding) string {
	switch receipt.Code {
	case effect.CodeCapabilityUndeclared:
		return "undeclared"
	case effect.CodeCapabilityDenied:
		profile, ok := definition.TrustProfileFor(definition.TrustClass(binding.TrustClass))
		if ok && !profile.Permits(string(receipt.Capability)) {
			return "trust-class"
		}
		return "host-policy"
	}
	if !effect.Known(receipt.Capability) {
		return "unknown-capability"
	}
	return "host-policy"
}
