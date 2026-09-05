package telemetry

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// The correlation identity, and why it is derived rather than propagated.
//
// One caller invocation crosses four boundaries: an MCP tool call, a browser
// WebSocket, the durable store, and a delivery back to the caller's own
// request. Only one of those is a call stack. The browser owns its own
// process, the store outlives every process, and delivery can happen minutes
// after the transport that asked for it has gone. A context-propagated trace
// id survives none of that.
//
// So the identity is computed from facts that are already durable and already
// invariant for the life of the request:
//
//	trace_id = SHA-256("tangent/interaction-trace/v1" ‖ caller_scope ‖ idempotency_key)[:16]
//
// Both inputs are columns on `interactions`. That gives four properties no
// propagated id has:
//
//   - **Reconstructible.** Any process, after any restart, holding only the
//     interaction record can recompute the identity. Nothing has to have been
//     carried.
//   - **Stable across retries.** An idempotent retry of the same invocation is
//     the same interaction, and lands in the same trace — which is the honest
//     answer, not a coincidence.
//   - **Boundary-free.** The browser is never asked to carry a trace header,
//     so nothing about the identity depends on a client honoring it.
//   - **Non-disclosing.** It is a one-way digest of two values the holder of
//     the record already has, so possessing a trace id reveals nothing and
//     grants nothing.
//
// What it is *not* is a claim that a distributed trace stitches itself
// together across the browser. It does not. The browser's own work — render,
// participant action, submit — is not in the trace as browser spans; what is
// in the trace is the server-side observation of each of those, correlated by
// the identity the server recomputed. See docs/architecture.md.

// traceDomain namespaces the derivation so a trace id can never collide with
// a digest computed for some other purpose over the same two strings.
const traceDomain = "tangent/interaction-trace/v1"

// TraceID is a W3C-shaped 16-byte trace identifier.
type TraceID [16]byte

// SpanID is a W3C-shaped 8-byte span identifier.
type SpanID [8]byte

// String renders the trace id as 32 lowercase hex characters. A zero trace id
// renders as the all-zero string, which W3C treats as invalid and which reads
// in a report as "no correlation was established" rather than as a real trace.
func (t TraceID) String() string { return hex.EncodeToString(t[:]) }

// Valid reports whether the trace id is non-zero.
func (t TraceID) Valid() bool { return t != TraceID{} }

// String renders the span id as 16 lowercase hex characters.
func (s SpanID) String() string { return hex.EncodeToString(s[:]) }

// Valid reports whether the span id is non-zero.
func (s SpanID) Valid() bool { return s != SpanID{} }

// TraceFor derives the correlation identity of one caller invocation.
//
// Both inputs are host-derived: the caller scope is assigned by
// internal/authz from admission facts and can never be spelled by a caller,
// and the idempotency key is composed by the adapter that owns the workflow
// identity. Neither is a secret and neither is recoverable from the digest.
func TraceFor(callerScope, idempotencyKey string) TraceID {
	callerScope = strings.TrimSpace(callerScope)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if callerScope == "" && idempotencyKey == "" {
		return TraceID{}
	}
	sum := sha256.Sum256([]byte(traceDomain + "\x00" + callerScope + "\x00" + idempotencyKey))
	var id TraceID
	copy(id[:], sum[:len(id)])
	return id
}

// TraceForRecord derives the identity from a durable interaction's own
// correlation columns. It is the reconstruction path: a WebSocket frame, a
// delivery attempt, and a health report all reach the same trace by loading
// the record rather than by having been handed anything.
func TraceForRecord(callerScope, idempotencyKey, interactionID string) TraceID {
	if id := TraceFor(callerScope, idempotencyKey); id.Valid() {
		return id
	}
	// A record whose idempotency identity is unreadable still deserves a
	// stable trace. Falling back to the interaction id keeps every observation
	// of one interaction together; it simply cannot join a retry of the same
	// logical request, which is the honest consequence of not knowing the
	// request's identity.
	return TraceFor("interaction", interactionID)
}

// TraceForKind is the trace every observation about one interaction *kind* is
// filed under — a renderer this build cannot serve, a capability its trust
// class refuses.
//
// A kind is not an invocation, so it gets its own trace rather than borrowing
// the trace of whichever caller happened to trip over it first. That is what
// lets a health report naming a broken kind point at a trace that accumulates
// every time the kind is refused, instead of at one caller's bad afternoon.
func TraceForKind(kind string) TraceID { return TraceFor("definition", kind) }

// TraceForRoom is the trace connection lifecycle for one surface is filed
// under.
//
// A connection is not an invocation either: attaching, reconnecting, and
// detaching happen whether or not there is work on the surface, and forcing
// them into an interaction's trace would file a browser refresh under whatever
// request happened to be open. The room id is the stable thing they share.
func TraceForRoom(roomID string) TraceID { return TraceFor("room", roomID) }

// TraceForCheck is the trace one named host subject's history is filed under —
// a readiness check, or a gate that refuses requests naming no interaction. A
// failing health report publishes it so a reader can find the rest of that
// subject's story.
func TraceForCheck(check string) TraceID { return TraceFor("health", check) }

// ParseTraceID reads a trace id back from its rendered form. It is how a
// caller holding the identifier a health report published reaches the
// observations filed under it.
func ParseTraceID(value string) (TraceID, bool) {
	value = strings.TrimSpace(value)
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != len(TraceID{}) {
		return TraceID{}, false
	}
	var id TraceID
	copy(id[:], raw)
	return id, id.Valid()
}

// NewSpanID mints a random span identifier. Span ids are per-observation and
// deliberately not derived: two observations of the same interaction are
// different spans, and deriving them would make them collide.
func NewSpanID() SpanID {
	var id SpanID
	if _, err := rand.Read(id[:]); err != nil {
		// crypto/rand does not fail on any supported platform. If it somehow
		// did, an invalid span id is the correct degradation: the observation
		// still records its trace and its facts, and only the span-level
		// parentage is lost.
		return SpanID{}
	}
	if !id.Valid() {
		// The all-zero draw is invalid under W3C. Refusing it costs one bit of
		// entropy and avoids emitting a span nothing will accept.
		id[0] = 1
	}
	return id
}

// Correlation is the identity and the dimensions one observation is filed
// under.
//
// Every field is either a Tangent identifier or a host-assigned label. There
// is deliberately no field for a payload, a participant's words, a path, a
// session, an effect handle, or a URL — an observation cannot carry one
// because there is nowhere to put it.
type Correlation struct {
	Trace  TraceID
	Span   SpanID
	Parent SpanID

	SurfaceID     string
	InteractionID string
	RoomID        string
	// EnvelopeID is the caller's own envelope identifier.
	//
	// It is caller-chosen, which makes it the one correlation field that is
	// not host-derived. It is recorded anyway, bounded and charset-restricted
	// like every other identifier, because it is the handle a caller uses to
	// find its own work. A caller that puts a secret in an envelope id has
	// already put it in the immutable request snapshot, the legacy room
	// projection, and the pending receipt it was handed back; telemetry does
	// not widen that exposure, and refusing to record it here would cost the
	// correlation without closing anything.
	EnvelopeID   string
	ConnectionID string

	DefinitionKind    string
	DefinitionVersion string

	CallerScope      string
	ParticipantScope string
}

// WithSpan returns a copy carrying a freshly minted span id, parented to this
// correlation's current span.
func (c Correlation) WithSpan() Correlation {
	child := c
	child.Parent = c.Span
	child.Span = NewSpanID()
	return child
}

// sanitized returns the correlation with every field bounded and
// charset-checked. It is applied on the emission path so a dimension that
// somehow acquired free text is dropped rather than stored.
func (c Correlation) sanitized() Correlation {
	c.SurfaceID = boundIdentifier(c.SurfaceID)
	c.InteractionID = boundIdentifier(c.InteractionID)
	c.RoomID = boundIdentifier(c.RoomID)
	c.EnvelopeID = boundIdentifier(c.EnvelopeID)
	c.ConnectionID = boundIdentifier(c.ConnectionID)
	c.DefinitionKind = boundIdentifier(c.DefinitionKind)
	c.DefinitionVersion = boundIdentifier(c.DefinitionVersion)
	c.CallerScope = boundLabel(c.CallerScope)
	c.ParticipantScope = boundLabel(c.ParticipantScope)
	return c
}
