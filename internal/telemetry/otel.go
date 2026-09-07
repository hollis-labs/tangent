package telemetry

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// What was chosen for OpenTelemetry, and why.
//
// Tangent depends on the OpenTelemetry **API** and on nothing else from that
// ecosystem. No SDK, no OTLP exporter, no gRPC, no protobuf. The dependency
// added by this file is five small modules that were already in the module
// graph, and the shipped binary opens no socket and starts no exporter.
//
// That is the whole of "optional and off by default", and it is off in two
// independent ways:
//
//  1. OpenTelemetry's own default providers are no-ops. With no SDK installed
//     — which is every shipped build — `tracer.Start` and every instrument
//     record cost a nil-ish method call and produce nothing.
//  2. The bridge is additionally gated behind an explicit opt-in
//     (`WithOpenTelemetry`, wired from `TANGENT_OTEL` at the composition
//     root). Without it Tangent never touches the global providers at all.
//
// The second gate is not belt-and-braces. `otel.GetTracerProvider()` is
// process-global, so a future dependency that installs an SDK for its own
// reasons would otherwise silently start receiving Tangent's spans — including
// on a machine whose operator never asked for telemetry to leave the box. On a
// single-user loopback host that default is wrong, and the flag is what makes
// the decision the operator's.
//
// What an embedder does to turn it on: add the SDK and an exporter to their
// own module, install them with `otel.SetTracerProvider` / `otel.SetMeterProvider`
// before Tangent records anything, and construct the recorder with
// `WithOpenTelemetry(true)`. No instrumentation changes — every span and every
// instrument in this package already exists.
//
// What is honestly *not* here: no shipped build exports a span anywhere, so
// the OTel half of this task has been written and compiled but never observed
// end-to-end against a collector. The in-process registry is what the shipped
// binary actually answers criterion 2 from.

// instrumentationScope names this instrumentation to a collector. It is the
// import path, which is the OTel convention and the only string a collector
// needs to attribute a span to the code that produced it.
const instrumentationScope = "github.com/hollis-labs/tangent/internal/telemetry"

// otelBridge mirrors observations onto the OpenTelemetry global providers.
// The zero value is disabled and safe to use.
type otelBridge struct {
	enabled bool

	once  sync.Once
	mu    sync.Mutex
	trace trace.Tracer
	meter metric.Meter

	counters   map[string]metric.Int64Counter
	histograms map[string]metric.Float64Histogram
}

func (b *otelBridge) init() {
	b.once.Do(func() {
		b.trace = otel.GetTracerProvider().Tracer(instrumentationScope)
		b.meter = otel.GetMeterProvider().Meter(instrumentationScope)
		b.counters = map[string]metric.Int64Counter{}
		b.histograms = map[string]metric.Float64Histogram{}
	})
}

// span records one observation as an OpenTelemetry span.
//
// The span is created under a *remote parent* built from Tangent's own derived
// trace id and the observation's span id. That is the join: an exported span
// carries the same trace id every telemetry_events row for this invocation
// carries, so a collector and the durable audit table agree about which
// invocation they are describing. The exported span's own id is the SDK's —
// the OTel API deliberately does not let instrumentation choose one — so
// Tangent's span id is its parent rather than its identity, and
// `tangent.event` names which observation it is.
func (b *otelBridge) span(ctx context.Context, event Event, attrs []Attr) {
	if !b.enabled || !event.Correlation.Trace.Valid() {
		return
	}
	b.init()
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID(event.Correlation.Trace),
		SpanID:     trace.SpanID(event.Correlation.Span),
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	if !parent.IsValid() {
		return
	}
	start := event.At
	options := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithTimestamp(start),
		trace.WithAttributes(keyValues(event, attrs)...),
	}
	if upstream := event.Correlation.Upstream; upstream.Valid() {
		// A link, not a parent: the observation is filed under Tangent's
		// derived trace, and the caller's trace is joined to it without
		// either one adopting the other's identity.
		flags := trace.TraceFlags(0)
		if upstream.Sampled {
			flags = trace.FlagsSampled
		}
		options = append(options, trace.WithLinks(trace.Link{
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
				TraceID:    trace.TraceID(upstream.Trace),
				SpanID:     trace.SpanID(upstream.Span),
				TraceFlags: flags,
				Remote:     true,
			}),
			Attributes: []attribute.KeyValue{attribute.String("tangent.link", "upstream_gateway")},
		}))
	}
	_, span := b.trace.Start(trace.ContextWithSpanContext(ctx, parent), event.Name, options...)
	switch event.Outcome {
	case OutcomeFailed:
		// The description is the typed code, never a message. codes.Error with
		// an interpolated string is the most common way an OTel integration
		// leaks, and it is closed here by having nothing to interpolate.
		span.SetStatus(codes.Error, event.Code)
	case OutcomeRefused, OutcomeOK:
		span.SetStatus(codes.Ok, "")
	}
	span.End(trace.WithTimestamp(start.Add(event.Duration)))
}

// count mirrors a counter increment.
func (b *otelBridge) count(ctx context.Context, instrument string, attrs []Attr, delta int64) {
	if !b.enabled {
		return
	}
	b.init()
	b.mu.Lock()
	counter, ok := b.counters[instrument]
	if !ok {
		created, err := b.meter.Int64Counter(instrument)
		if err != nil {
			// A meter that refuses an instrument is a misconfigured provider,
			// not a Tangent fault. The in-process registry already has the
			// value; dropping the mirror is the correct degradation.
			b.mu.Unlock()
			return
		}
		counter = created
		b.counters[instrument] = counter
	}
	b.mu.Unlock()
	counter.Add(ctx, delta, metric.WithAttributes(attributeSet(attrs)...))
}

// observe mirrors a duration sample, in milliseconds.
func (b *otelBridge) observe(ctx context.Context, instrument string, attrs []Attr, milliseconds float64) {
	if !b.enabled {
		return
	}
	b.init()
	b.mu.Lock()
	histogram, ok := b.histograms[instrument]
	if !ok {
		created, err := b.meter.Float64Histogram(instrument,
			metric.WithUnit("ms"),
			metric.WithExplicitBucketBoundaries(durationBucketsMS...))
		if err != nil {
			b.mu.Unlock()
			return
		}
		histogram = created
		b.histograms[instrument] = histogram
	}
	b.mu.Unlock()
	histogram.Record(ctx, milliseconds, metric.WithAttributes(attributeSet(attrs)...))
}

// keyValues renders an event's correlation and attributes as span attributes.
//
// Only the correlation fields that are set are emitted, and each one has
// already passed the identifier or label check. There is no branch here that
// can produce a key outside this list.
func keyValues(event Event, attrs []Attr) []attribute.KeyValue {
	correlation := event.Correlation
	values := make([]attribute.KeyValue, 0, 12+len(attrs))
	values = append(values,
		attribute.String("tangent.event", event.Name),
		attribute.String("tangent.outcome", string(event.Outcome)),
		attribute.String("tangent.span_id", correlation.Span.String()),
	)
	if event.Code != "" {
		values = append(values, attribute.String("tangent.code", event.Code))
	}
	for key, value := range map[string]string{
		"tangent.surface_id":         correlation.SurfaceID,
		"tangent.interaction_id":     correlation.InteractionID,
		"tangent.room_id":            correlation.RoomID,
		"tangent.envelope_id":        correlation.EnvelopeID,
		"tangent.connection_id":      correlation.ConnectionID,
		"tangent.definition.kind":    correlation.DefinitionKind,
		"tangent.definition.version": correlation.DefinitionVersion,
		"tangent.caller_scope":       correlation.CallerScope,
		"tangent.participant_scope":  correlation.ParticipantScope,
	} {
		if value != "" {
			values = append(values, attribute.String(key, value))
		}
	}
	if correlation.Upstream.Valid() {
		values = append(values,
			attribute.String("tangent."+AttrUpstreamTraceID, correlation.Upstream.Trace.String()),
			attribute.String("tangent."+AttrUpstreamSpanID, correlation.Upstream.Span.String()),
		)
	}
	return append(values, attributeSet(attrs)...)
}

func attributeSet(attrs []Attr) []attribute.KeyValue {
	values := make([]attribute.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		switch attr.kind {
		case attrString:
			if attr.str != "" {
				values = append(values, attribute.String("tangent."+attr.Key, attr.str))
			}
		case attrInt:
			values = append(values, attribute.Int64("tangent."+attr.Key, attr.num))
		case attrBool:
			values = append(values, attribute.Bool("tangent."+attr.Key, attr.flag))
		}
	}
	return values
}
