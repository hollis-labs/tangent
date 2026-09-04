package telemetry

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Event names. A closed vocabulary, because an event name is exported as a
// span name and a metric dimension, and ADR 0002 §8's rule covers event names
// as explicitly as it covers log lines: a name interpolated from anything a
// caller supplied is a leak with a different shape.
const (
	// EventInteractionAdmitted is one caller invocation acquiring — or
	// recovering — its durable identity. It is the root observation of a
	// trace.
	EventInteractionAdmitted = "interaction.admitted"
	// EventInteractionPresented is the interaction becoming visible to a
	// participant. Its duration is admission → presentation.
	EventInteractionPresented = "interaction.presented"
	// EventInteractionResolved is the participant's immutable answer landing.
	// Its duration is presentation → resolution.
	EventInteractionResolved = "interaction.resolved"
	// EventInteractionCanceled is an authorized cancellation.
	EventInteractionCanceled = "interaction.canceled"
	// EventInteractionPending is a caller's transport giving up before the
	// human answered. It is not a failure and is recorded as an ok outcome.
	EventInteractionPending = "interaction.pending"
	// EventInteractionRefused is an invocation Tangent declined — an identity
	// reused with a different request, or a definition it cannot serve.
	EventInteractionRefused = "interaction.refused"

	// EventPresentationRefused is a participant action the surface declined:
	// a failed presentation compare-and-set, or a resolver lease held by
	// another live connection. These are the two things "a stale client"
	// actually is.
	EventPresentationRefused = "presentation.refused"

	// EventConnectionAttached is one client attaching to a surface. The
	// `replaced` attribute is what makes a reconnect distinguishable from a
	// new client.
	EventConnectionAttached = "connection.attached"
	// EventConnectionDetached is one client leaving.
	EventConnectionDetached = "connection.detached"

	// EventDraftRefused is a draft revision the store declined — a revision
	// conflict, a terminal interaction, or a non-respondable state.
	EventDraftRefused = "draft.refused"

	// EventDeliveryRecorded is a terminal outcome handed back to the caller.
	// Its duration is the delivery lag: resolution recorded → outcome
	// delivered.
	EventDeliveryRecorded = "delivery.recorded"
	// EventDeliveryFailed is a delivery journal write that did not land.
	EventDeliveryFailed = "delivery.failed"

	// EventCapabilityDenied is a refusal in either capability namespace. The
	// namespace and, where the host can tell them apart, the denying rule are
	// attributes; they are never collapsed.
	EventCapabilityDenied = "capability.denied"

	// EventReadinessDegraded is a readiness check that stopped passing, or
	// started passing again. It is emitted on *transitions* rather than on
	// every probe: a supervisor polls readiness on a timer, and one row per
	// poll would bury the moment something changed under thousands of rows
	// saying it had not.
	EventReadinessDegraded = "readiness.changed"

	// EventRendererUnavailable is a definition this build cannot serve —
	// incompatible, quarantined, or unavailable. It is emitted once per kind
	// at boot and again whenever a submission is refused for it.
	EventRendererUnavailable = "renderer.unavailable"
)

// eventNames is the enforcement half of the vocabulary above.
var eventNames = map[string]bool{
	EventInteractionAdmitted:  true,
	EventInteractionPresented: true,
	EventInteractionResolved:  true,
	EventInteractionCanceled:  true,
	EventInteractionPending:   true,
	EventInteractionRefused:   true,
	EventPresentationRefused:  true,
	EventConnectionAttached:   true,
	EventConnectionDetached:   true,
	EventDraftRefused:         true,
	EventDeliveryRecorded:     true,
	EventDeliveryFailed:       true,
	EventCapabilityDenied:     true,
	EventReadinessDegraded:    true,
	EventRendererUnavailable:  true,
}

// Outcome is what happened, in three values.
//
// It is deliberately not the pass/warn/fail health vocabulary: a refusal is a
// correct answer the system gave on purpose, and folding it into "fail" is how
// an operator learns to ignore the failure count.
type Outcome string

const (
	// OutcomeOK means the operation did what it was asked.
	OutcomeOK Outcome = "ok"
	// OutcomeRefused means a rule declined it. Nothing is broken.
	OutcomeRefused Outcome = "refused"
	// OutcomeFailed means it did not complete for a reason nobody chose.
	OutcomeFailed Outcome = "failed"
)

func (o Outcome) valid() bool {
	switch o {
	case OutcomeOK, OutcomeRefused, OutcomeFailed:
		return true
	}
	return false
}

// Typed codes this package will record. Every one of them is a constant
// defined somewhere in the tree — effect refusal codes, WebSocket wire error
// codes, and the store's own sentinel classes — and the set is closed for the
// same reason the attribute vocabularies are.
//
// It is duplicated here rather than imported so internal/telemetry depends on
// nothing it observes. A telemetry package that imported internal/effect,
// internal/room, internal/interaction, and internal/authz would be un-importable
// from any of them, which is exactly where the call sites are.
var knownCodes = map[string]bool{
	// internal/effect refusal codes (ADR 0003 §2.5).
	"effect_not_authorized":        true,
	"effect_capability_undeclared": true,
	"effect_capability_denied":     true,
	"effect_unavailable":           true,
	"effect_handle_unknown":        true,
	"effect_handle_expired":        true,
	"effect_handle_exhausted":      true,
	"effect_intent_missing":        true,
	"effect_idempotency_conflict":  true,
	"effect_path_refused":          true,
	"effect_root_unmediated":       true,
	"effect_too_large":             true,
	"effect_invalid_request":       true,

	// internal/ws wire error codes.
	"resolver_lease_held": true,
	"stale_presentation":  true,
	"room_closed":         true,
	"not_authorized":      true,

	// internal/authz's two refusal shapes (ADR 0004 §5). They are kept apart
	// because the 403/404 split is the whole point of that contract.
	"forbidden": true,
	"not_found": true,

	// internal/interaction store and service sentinels.
	"revision_conflict":    true,
	"idempotency_conflict": true,
	"terminal":             true,
	"not_respondable":      true,
	"invalid_record":       true,
	"unauthorized":         true,
	"wait_timeout":         true,

	// internal/definition materialization refusals (ADR 0003 §8 C7).
	"definition_unavailable": true,
	"definition_not_found":   true,
	"incompatible":           true,
	"quarantined":            true,
	"unavailable":            true,

	// upstream envelope error codes reused by ADR 0003 §8 C7.
	"unsupported-type":      true,
	"unsupported-version":   true,
	"validation-failed":     true,
	"capability-denied":     true,
	"component-load-failed": true,

	// internal/health check names. A readiness observation's code is the check
	// that changed, which is what makes the row answer "which dependency" with
	// no message at all.
	"database":            true,
	"migrations":          true,
	"definition_registry": true,
	"renderer_host":       true,
	"delivery_worker":     true,
	"participant_session": true,

	// The honest answer when nothing above matched.
	unclassified: true,
}

// Code normalizes a typed code onto the closed set.
//
// A value that is not a known code becomes `unclassified` rather than being
// recorded. That is the single most important line in this package: it is what
// stops a future call site from passing `err.Error()` — which on SQLite
// carries the database file path — into an audit row.
func Code(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	if knownCodes[code] {
		return code
	}
	return unclassified
}

// CodeForError classifies a Go error without ever reading its message.
//
// The classification is by sentinel identity, which is why every branch here
// uses errors.Is against a caller-supplied table rather than string matching.
// Nothing in this package can see internal/interaction's sentinels, so the
// caller supplies the pairing; what this function guarantees is that the
// fallback is a code and never a message.
func CodeForError(err error, table []ErrorCode) string {
	if err == nil {
		return ""
	}
	for _, entry := range table {
		if entry.Sentinel != nil && errors.Is(err, entry.Sentinel) {
			return Code(entry.Code)
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "wait_timeout"
	}
	return unclassified
}

// ErrorCode pairs a sentinel with the typed code it is recorded as. Call sites
// build the table from their own package's sentinels, so the mapping lives
// next to the errors it classifies.
type ErrorCode struct {
	Sentinel error
	Code     string
}

// Event is one observation.
//
// It carries no message field, and adding one would be the defect this package
// exists to prevent. What happened is the name; how it went is the outcome;
// why is the code.
type Event struct {
	Name        string
	Outcome     Outcome
	Code        string
	Correlation Correlation
	// Duration is the measured span of the operation. Zero means the
	// observation is instantaneous and no duration is recorded.
	Duration time.Duration
	Attrs    []Attr
	// At is when it happened. Zero means "now", resolved by the recorder's
	// clock so a test can pin it.
	At time.Time
}
