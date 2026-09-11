package effect

import (
	"errors"
	"time"
)

// Decision is the two-valued outcome of an effect request. There is no third
// value: an effect that partially happened is a granted effect whose receipt
// records what it produced.
type Decision string

const (
	// DecisionGranted means the effect was performed.
	DecisionGranted Decision = "granted"
	// DecisionRefused means it was not, and Code says which rule refused it.
	DecisionRefused Decision = "refused"
)

// Refusal codes. Every refusal in this package is one of these, and a
// transport never invents another: the SPA maps them onto the one red
// `role="alert"` surface `ConnectionStatus.describeServerError` owns
// (docs/room-validation-affordances.md), and a code with no case there falls
// through to a safe default rather than to a second error pattern.
//
// None of them names a path, a root, a scope, a capability the principal lacks,
// or a session. ADR 0004 §6.5 makes that a rule about authorization failures;
// it reads the same here, because "which of my capabilities is missing" is a
// probe whichever namespace it is asked about.
const (
	// CodeNotAuthorized is the object-access precondition failing: the
	// principal does not hold the authz capability this effect requires on
	// this object. It is also what a cross-realm handle returns.
	CodeNotAuthorized = "effect_not_authorized"
	// CodeCapabilityUndeclared means the definition never declared this
	// effect in required_capabilities. A renderer cannot acquire an effect by
	// asking for it at runtime.
	CodeCapabilityUndeclared = "effect_capability_undeclared"
	// CodeCapabilityDenied means the definition declared it and host policy
	// did not grant it, so it is absent from granted_capabilities.
	CodeCapabilityDenied = "effect_capability_denied"
	// CodeUnavailable means this build ships no executor for the capability.
	CodeUnavailable = "effect_unavailable"
	// CodeHandleUnknown means no live handle for this request. It covers a
	// handle that never existed, one belonging to another participant realm,
	// and one minted for another interaction — deliberately one code, so the
	// refusal cannot be used to enumerate handles.
	CodeHandleUnknown = "effect_handle_unknown"
	// CodeHandleExpired means the handle's window closed.
	CodeHandleExpired = "effect_handle_expired"
	// CodeHandleExhausted means its granted-use budget is spent.
	CodeHandleExhausted = "effect_handle_exhausted"
	// CodeIntentMissing means no participant intent accompanied the request.
	CodeIntentMissing = "effect_intent_missing"
	// CodeIdempotencyConflict means the idempotency key was already used for a
	// materially different request.
	CodeIdempotencyConflict = "effect_idempotency_conflict"
	// CodePathRefused covers every filesystem refusal that a caller must not
	// be able to tell apart: a traversal, a symlink escape, a malformed
	// reference, and a file that is not there. Splitting them would turn a
	// refusal into an existence oracle for paths outside the root.
	CodePathRefused = "effect_path_refused"
	// CodeRootUnmediated means no authority registered that root. It is
	// separate from CodePathRefused because root ids are host-published: a
	// renderer already knows which roots exist, so saying "not that one"
	// discloses nothing and saves a confusing debugging session.
	CodeRootUnmediated = "effect_root_unmediated"
	// CodeTooLarge means the content exceeded the mediated ceiling.
	CodeTooLarge = "effect_too_large"
	// CodeInvalidRequest means the request was not well-formed — an unknown
	// capability id, a missing handle where one is required, a negative
	// budget.
	CodeInvalidRequest = "effect_invalid_request"
)

// Receipt is the durable, typed record of one effect request — granted or
// refused.
//
// It is what ADR 0003 §2.5's `granted_capabilities` becomes at runtime: the
// binding says what a renderer *could* do, and a receipt says what it *did*,
// under whose intent, against which handle, at which pinned binding digest.
//
// It deliberately carries no content, no path, no root id, and no session.
// A receipt is written on every request including every refusal, so it is the
// densest audit surface in the process and the one most likely to be read out
// loud. Everything ADR 0002 §8 forbids in a log line is therefore forbidden
// here too, whether or not anyone logs it.
type Receipt struct {
	// ID names this receipt.
	ID string `json:"receipt_id"`
	// Capability is the effect requested.
	Capability Capability `json:"capability"`
	// Decision and Code are the outcome. Code is empty when granted.
	Decision Decision `json:"decision"`
	Code     string   `json:"code,omitempty"`
	// Mediation records how much of this capability the host enforces, so a
	// reader of an audit trail can tell a barrier from a declaration without
	// consulting a table elsewhere.
	Mediation Mediation `json:"mediation"`
	// Isolation is where the renderer ran, and TrustClass is the class the definition was
	// granted and the place that class runs.
	//
	// They are on the receipt because without them Mediation is not
	// interpretable. `clipboard.write` with `mediation: host` is a true
	// statement about a sandboxed frame and a false one about Tangent's own
	// tree, and an audit row that carried only the answer would be
	// indistinguishable between the two. Recording the isolation is what makes
	// the mediation column readable a year later.
	// TrustClass is NOT WRITTEN on receipts minted after ADR 0009 removed the
	// class, and is empty on them. It is kept on the struct because receipts
	// are immutable: rows written before that ADR recorded a real decision
	// under the model in force at the time, and this is what reads them back.
	// Migration 0010's own note applies unchanged — an empty string reads as
	// "not recorded", and rewriting history to claim otherwise would be worse.
	TrustClass string    `json:"renderer_trust_class,omitempty"`
	Isolation  Isolation `json:"renderer_isolation,omitempty"`
	// HandleID is the scope the effect was requested against, when one
	// applies.
	HandleID string `json:"handle_id,omitempty"`
	// InteractionID is the object the effect attached to.
	InteractionID string `json:"interaction_id"`
	// ParticipantScope is the acting realm. A scope, never a session.
	ParticipantScope string `json:"participant_scope"`
	// BindingDigest pins the definition binding whose grant was consulted.
	BindingDigest string `json:"binding_digest,omitempty"`
	// IdempotencyKey is the caller's replay key.
	IdempotencyKey string `json:"idempotency_key"`
	// IntentControlID and IntentRevision record the participant intent: which
	// control produced the request, and which presented revision it acted on.
	IntentControlID string `json:"intent_control_id,omitempty"`
	IntentRevision  int64  `json:"intent_revision,omitempty"`
	// IssuedAt is when the decision was made.
	IssuedAt time.Time `json:"issued_at"`
	// Bytes and ContentSHA256 describe the effect's output without carrying
	// it. A digest is an identity, not a payload.
	Bytes         int64  `json:"bytes,omitempty"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
	MediaType     string `json:"media_type,omitempty"`
	// Replayed marks a receipt returned from an earlier identical request
	// rather than freshly decided.
	Replayed bool `json:"replayed,omitempty"`
}

// Result is what [Broker.Request] returns: the receipt, and the content the
// effect produced.
//
// Content is deliberately outside the receipt. The receipt is the durable row
// and the safe-to-log projection; the content is neither, and keeping them in
// one struct is how a payload ends up in an audit table.
type Result struct {
	Receipt Receipt
	Content []byte
}

// refusalFor maps a package error onto its refusal code. It is the single
// translation, so a new sentinel cannot reach a transport uncoded.
func refusalFor(err error) string {
	switch {
	case errors.Is(err, ErrHandleUnknown), errors.Is(err, ErrHandleMismatch):
		return CodeHandleUnknown
	case errors.Is(err, ErrHandleExpired):
		return CodeHandleExpired
	case errors.Is(err, ErrHandleExhausted):
		return CodeHandleExhausted
	case errors.Is(err, ErrRootUnmediated):
		return CodeRootUnmediated
	case errors.Is(err, ErrTooLarge):
		return CodeTooLarge
	case errors.Is(err, ErrPathInvalid), errors.Is(err, ErrPathEscapes), errors.Is(err, ErrNotRegularFile):
		return CodePathRefused
	default:
		return CodeInvalidRequest
	}
}
