package telemetry

import (
	"context"
	"encoding/hex"
	"strings"
)

// Upstream trace context, and why it is a link rather than the identity.
//
// A gateway in front of Tangent (Tether's `mux` proxy today) can carry a W3C
// trace context for the tool call it forwards. That context is genuinely
// useful: it is the only thing that joins Tangent's observation of a call to
// the caller's own trace of having made it. It is also exactly the kind of
// header correlation.go refuses to make the *identity*: it is asserted by the
// transport, it is absent from the durable record, and it would survive none
// of the boundaries the derived identity is built to survive.
//
// So an upstream trace is recorded beside the derived identity, never in
// place of it. Every observation emitted while the upstream context is on the
// request's context.Context carries the upstream trace and span as record
// attributes and as an OpenTelemetry span link, and the trace id Tangent files
// the observation under is still the derived one.

// Record-only attribute keys for the upstream trace context. They are written
// by recordFor from Correlation.Upstream and are deliberately absent from the
// attribute allowlist in attributes.go: an event that tries to pass them as
// Attrs has them dropped, so the only way a trace id reaches a record is
// through a parsed W3C traceparent, and a trace id can never become a metric
// dimension (metricDimensions reads Attrs, not Correlation).
const (
	AttrUpstreamTraceID = "upstream_trace_id"
	AttrUpstreamSpanID  = "upstream_span_id"
)

// UpstreamTrace is a parsed W3C traceparent: the caller's trace and the span
// that made the call into Tangent, plus whether that trace was sampled.
type UpstreamTrace struct {
	Trace   TraceID
	Span    SpanID
	Sampled bool
}

// Valid reports whether the upstream context names a real trace and span.
func (u UpstreamTrace) Valid() bool { return u.Trace.Valid() && u.Span.Valid() }

// ParseTraceparent reads a W3C Trace Context `traceparent` header value:
//
//	version "-" trace-id "-" parent-id "-" trace-flags
//	00        -  32 lowercase hex  -  16 lowercase hex  -  2 lowercase hex
//
// It accepts version 00 exactly, which is the only version defined and the
// only one Tether's gateway emits. A future version is rejected rather than
// guessed at, because a wrong parse would file an observation under a
// fabricated link. The all-zero trace id and all-zero span id are invalid per
// the specification and are rejected. Uppercase hex is rejected too: the
// specification requires lowercase, and every emitter in the portfolio uses
// the OpenTelemetry formatter, which does.
func ParseTraceparent(value string) (UpstreamTrace, bool) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return UpstreamTrace{}, false
	}
	for _, part := range parts[1:] {
		if !lowercaseHex(part) {
			return UpstreamTrace{}, false
		}
	}
	var upstream UpstreamTrace
	traceRaw, _ := hex.DecodeString(parts[1])
	spanRaw, _ := hex.DecodeString(parts[2])
	flagsRaw, _ := hex.DecodeString(parts[3])
	copy(upstream.Trace[:], traceRaw)
	copy(upstream.Span[:], spanRaw)
	upstream.Sampled = flagsRaw[0]&0x01 == 0x01
	if !upstream.Valid() {
		return UpstreamTrace{}, false
	}
	return upstream, true
}

func lowercaseHex(value string) bool {
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

type upstreamTraceKey struct{}

// ContextWithUpstreamTrace attaches an upstream trace context to ctx. Every
// Emit made with a context derived from it records the upstream trace beside
// the derived identity. An invalid upstream is not attached.
func ContextWithUpstreamTrace(ctx context.Context, upstream UpstreamTrace) context.Context {
	if !upstream.Valid() {
		return ctx
	}
	return context.WithValue(ctx, upstreamTraceKey{}, upstream)
}

// UpstreamTraceFromContext returns the upstream trace context attached by
// ContextWithUpstreamTrace, if any.
func UpstreamTraceFromContext(ctx context.Context) (UpstreamTrace, bool) {
	if ctx == nil {
		return UpstreamTrace{}, false
	}
	upstream, ok := ctx.Value(upstreamTraceKey{}).(UpstreamTrace)
	return upstream, ok && upstream.Valid()
}
