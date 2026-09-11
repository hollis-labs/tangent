package telemetry

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// The redaction floor.
//
// ADR 0002 §8 lists what may appear in a log, a metric, a span, an event name,
// or an error string, and what may never. This file is that rule expressed as
// a type rather than as a convention, because a convention is a thing every
// future call site has to remember and a type is not.
//
// Three structural decisions do the work:
//
//  1. **There is no free-text attribute.** Not `message`, not `detail`, not
//     `reason`, not `error`. The allowlist below contains no such key, so
//     there is nowhere for `err.Error()`, a terminal reason, a close reason, a
//     participant's words, or a driver error carrying a database path to go.
//     A caller that wants to say why something failed says it as a *code*.
//  2. **String values come from closed vocabularies.** Every allowlisted
//     string key declares either an exact permitted set or an identifier
//     shape. A value that does not match is replaced by `unclassified`, never
//     truncated into the record. Charset alone would not be enough — an
//     absolute filesystem path passes any reasonable identifier charset — so
//     the keys that could plausibly receive one (there are none) do not exist
//     rather than being filtered.
//  3. **Unknown keys are dropped and counted.** A future call site that
//     invents `error_message` does not leak; it produces a dropped-attribute
//     count that a test asserts is zero.
//
// The consequence to be honest about: telemetry here is less descriptive than
// a log line would be. That is the trade ADR 0002 §8 already made, and the
// operator action strings in internal/health are where the *explanation* is
// supposed to live — bounded, host-authored, and never interpolated from
// anything a caller or a participant supplied.

const (
	// maxAttributeValueBytes bounds one string attribute. It is far below
	// internal/health's 240-byte detail ceiling on purpose: a health detail is
	// a host-authored sentence, and an attribute is a token.
	maxAttributeValueBytes = 64

	// maxAttributes bounds one event's attribute set. An unbounded set is an
	// unbounded row.
	maxAttributes = 12

	// unclassified is what a value that fails its vocabulary becomes. It is a
	// deliberate, visible loss: a reader sees that something was refused
	// rather than seeing a plausible-looking value that was silently mangled.
	unclassified = "unclassified"
)

// Allowlisted attribute keys. Every key that may ever appear in a telemetry
// event or a metric dimension is here, and adding one is a deliberate act with
// a vocabulary attached.
const (
	// AttrMode is the completion mode a caller asked for: wait or async.
	AttrMode = "mode"
	// AttrState is an interaction lifecycle state.
	AttrState = "state"
	// AttrTerminalCause is the authorized cause behind a terminal disposition.
	AttrTerminalCause = "terminal_cause"
	// AttrRefusal classifies a refusal into the reason class a reader acts on.
	// It is never a sentence; see refusalVocabulary.
	AttrRefusal = "refusal"
	// AttrNamespace separates the two capability namespaces, which ADR 0003
	// §2.5 and ADR 0004 §2 require never be merged.
	AttrNamespace = "capability_namespace"
	// AttrDeniedBy separates a trust-class denial from a host-policy denial.
	// They are lifted by different acts and must stay apart.
	AttrDeniedBy = "denied_by"
	// AttrCapability is a capability id — an effect capability or an
	// object-access capability, disambiguated by AttrNamespace.
	AttrCapability = "capability"
	// AttrMaterializationState is a definition's materialization state.
	AttrMaterializationState = "materialization_state"
	// AttrIsolation is where the renderer runs.
	AttrIsolation = "renderer_isolation"
	// AttrRole is a connection's role on its surface.
	AttrRole = "role"
	// AttrClientKind labels the sort of client behind an attachment.
	//
	// It is the one attribute whose value originates in a query parameter the
	// browser supplies, so its vocabulary is closed tightly: anything the host
	// does not itself publish becomes `unclassified` rather than reaching a
	// metric dimension a client could otherwise choose.
	AttrClientKind = "client_kind"
	// AttrReplaced marks an attachment that replaced its own predecessor — a
	// reconnect rather than a new client.
	AttrReplaced = "replaced"
	// AttrLeaseInherited marks a reconnect that inherited its predecessor's
	// resolver lease.
	AttrLeaseInherited = "lease_inherited"
	// AttrRevision is a durable record revision.
	AttrRevision = "revision"
	// AttrPresentedRevision is the presentation revision a frame named.
	AttrPresentedRevision = "presented_revision"
	// AttrExpectedRevision is the presentation revision the server held.
	AttrExpectedRevision = "expected_revision"
	// AttrAttempt is a delivery attempt number.
	AttrAttempt = "attempt"
	// AttrDeliveryState is a delivery's own lifecycle state, which is
	// independent from its interaction's.
	AttrDeliveryState = "delivery_state"
	// AttrConnections counts live attachments on a surface.
	AttrConnections = "connections"
	// AttrCount is a plain cardinal — how many definitions, how many rows.
	AttrCount = "count"
	// AttrProbe names which health question an observation belongs to.
	AttrProbe = "probe"
	// AttrVerdict is a health check's own pass/warn/fail answer. It is a
	// separate key from AttrState because the two vocabularies are different
	// and folding a `warn` into an interaction state would make both unreadable.
	AttrVerdict = "verdict"
	// AttrTransport names the channel an observation was made on.
	AttrTransport = "transport"
)

// Attr is one allowlisted key with a bounded scalar value.
type Attr struct {
	Key string
	// exactly one of the three is meaningful, selected by kind.
	kind attrKind
	str  string
	num  int64
	flag bool
}

type attrKind uint8

const (
	attrString attrKind = iota
	attrInt
	attrBool
)

// String builds a string attribute. The value is checked against the key's
// vocabulary at emission, not here, so a call site cannot be surprised by a
// panic during an incident.
func String(key, value string) Attr { return Attr{Key: key, kind: attrString, str: value} }

// Int builds a numeric attribute. Numbers are always safe: ADR 0002 §8 permits
// counts, sizes, revisions, and durations without qualification.
func Int(key string, value int64) Attr { return Attr{Key: key, kind: attrInt, num: value} }

// Bool builds a boolean attribute.
func Bool(key string, value bool) Attr { return Attr{Key: key, kind: attrBool, flag: value} }

// vocabularies maps each allowlisted string key to the values it may carry.
//
// A nil vocabulary means "identifier shape" — bounded, charset-restricted, and
// used only for host-published names such as a capability id or a trust class,
// where enumerating every value here would duplicate a table that already
// exists elsewhere and would go stale.
var vocabularies = map[string][]string{
	AttrMode:          {"wait", "async"},
	AttrNamespace:     {"effect", "object-access"},
	AttrDeniedBy:      {"trust-class", "host-policy", "undeclared", "unknown-capability"},
	AttrRole:          {"resolver", "observer"},
	AttrClientKind:    {"browser", "native", "cli", "agent"},
	AttrProbe:         {"liveness", "readiness", "capability"},
	AttrVerdict:       {"pass", "warn", "fail"},
	AttrTransport:     {"mcp", "websocket", "browser-api", "in-process"},
	AttrRefusal:       refusalVocabulary,
	AttrDeliveryState: {"queued", "delivering", "delivered", "acknowledged", "retryable_failure", "terminal_failure"},
	AttrState: {
		"submitted", "validated", "staged", "presented", "in_progress",
		"resolved", "canceled", "expired", "failed", "superseded",
	},
	AttrTerminalCause: {
		"caller_withdrawn", "caller_canceled", "participant_canceled",
		"administrator_canceled", "surface_policy",
	},
	AttrMaterializationState: {
		"registered", "resolved", "verified", "materialized",
		"available", "unavailable", "incompatible", "quarantined",
	},
	AttrIsolation: {"main-origin", "host-primitive", "sandboxed-frame", "external-surface"},
}

// refusalVocabulary is the closed set of reason classes a refusal may be filed
// under. It is a classification, never an explanation: "the presentation this
// frame was rendered from is no longer current" is a sentence for a human and
// `stale_presentation` is what telemetry records.
var refusalVocabulary = []string{
	"stale_presentation",
	"resolver_lease_held",
	"room_closed",
	"not_authorized",
	"not_found",
	"revision_conflict",
	"idempotency_conflict",
	"terminal",
	"not_respondable",
	"definition_unavailable",
	"wait_timeout",
	"invalid_record",
	unclassified,
}

// identifierKeys are the allowlisted string keys whose values are host-published
// names rather than a closed set this file can enumerate.
var identifierKeys = map[string]bool{
	AttrCapability: true,
}

// allowedStringKeys is derived rather than declared so a key can never be in
// one table and missing from the other.
func allowedStringKey(key string) bool {
	if _, ok := vocabularies[key]; ok {
		return true
	}
	return identifierKeys[key]
}

// allowedNumericKeys are the allowlisted integer and boolean keys.
var allowedNumericKeys = map[string]bool{
	AttrRevision:          true,
	AttrPresentedRevision: true,
	AttrExpectedRevision:  true,
	AttrAttempt:           true,
	AttrConnections:       true,
	AttrCount:             true,
	AttrReplaced:          true,
	AttrLeaseInherited:    true,
}

// sanitizeAttrs applies the allowlist. It returns the surviving attributes in
// key order — stable ordering makes a metric dimension set a deterministic
// string — and the number that were dropped.
//
// Dropping is silent to the caller by design. A telemetry call that returned
// an error would either be ignored or, worse, checked and turned into a log
// line containing the thing that was refused.
func sanitizeAttrs(attrs []Attr) ([]Attr, int) {
	if len(attrs) == 0 {
		return nil, 0
	}
	kept := make([]Attr, 0, len(attrs))
	seen := make(map[string]bool, len(attrs))
	dropped := 0
	for _, attr := range attrs {
		if len(kept) >= maxAttributes {
			dropped++
			continue
		}
		if seen[attr.Key] {
			dropped++
			continue
		}
		switch attr.kind {
		case attrString:
			if !allowedStringKey(attr.Key) {
				dropped++
				continue
			}
			attr.str = vocabularyValue(attr.Key, attr.str)
		case attrInt, attrBool:
			if !allowedNumericKeys[attr.Key] {
				dropped++
				continue
			}
		}
		seen[attr.Key] = true
		kept = append(kept, attr)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Key < kept[j].Key })
	return kept, dropped
}

// vocabularyValue maps a string value onto its key's permitted set.
func vocabularyValue(key, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if permitted, ok := vocabularies[key]; ok {
		for _, candidate := range permitted {
			if value == candidate {
				return value
			}
		}
		return unclassified
	}
	if identifier := boundIdentifier(value); identifier != "" {
		return identifier
	}
	return unclassified
}

// boundIdentifier accepts a Tangent identifier or host-published name and
// rejects everything else.
//
// The charset excludes whitespace, quotes, path separators, and every
// URL-structural character, so an assembled room URL, a filesystem path, and a
// sentence all fail rather than being truncated into the record. A value that
// fails is dropped entirely: ADR 0002 §8's rule is about what may appear at
// all, and half of a forbidden string is still part of one.
func boundIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxAttributeValueBytes || !utf8.ValidString(value) {
		return ""
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_':
		default:
			return ""
		}
	}
	return value
}

// boundLabel accepts a host-assigned scope or authority label. It permits `:`
// and `@`, which scope grammar uses (`standalone-local:anonymous`), and
// nothing else that boundIdentifier does not.
func boundLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxAttributeValueBytes || !utf8.ValidString(value) {
		return ""
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_', r == ':', r == '@':
		default:
			return ""
		}
	}
	return value
}
