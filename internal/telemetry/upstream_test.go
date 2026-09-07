package telemetry

import (
	"context"
	"strings"
	"testing"
)

const (
	sampledTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	upstreamTraceHex   = "4bf92f3577b34da6a3ce929d0e0e4736"
	upstreamSpanHex    = "00f067aa0ba902b7"
)

func TestParseTraceparentAcceptsOnlyWellFormedVersion00(t *testing.T) {
	upstream, ok := ParseTraceparent(sampledTraceparent)
	if !ok {
		t.Fatal("a well-formed sampled traceparent must parse")
	}
	if upstream.Trace.String() != upstreamTraceHex || upstream.Span.String() != upstreamSpanHex || !upstream.Sampled {
		t.Fatalf("parsed %+v, want trace %s span %s sampled", upstream, upstreamTraceHex, upstreamSpanHex)
	}
	if unsampled, ok := ParseTraceparent("00-" + upstreamTraceHex + "-" + upstreamSpanHex + "-00"); !ok || unsampled.Sampled {
		t.Fatalf("flags 00 must parse as unsampled, got ok=%v %+v", ok, unsampled)
	}

	for name, bad := range map[string]string{
		"empty":            "",
		"three parts":      "00-" + upstreamTraceHex + "-" + upstreamSpanHex,
		"future version":   "01-" + upstreamTraceHex + "-" + upstreamSpanHex + "-01",
		"short trace":      "00-4bf92f3577b34da6a3ce929d0e0e47-" + upstreamSpanHex + "-01",
		"uppercase hex":    "00-4BF92F3577B34DA6A3CE929D0E0E4736-" + upstreamSpanHex + "-01",
		"non-hex":          "00-zzf92f3577b34da6a3ce929d0e0e4736-" + upstreamSpanHex + "-01",
		"all-zero trace":   "00-00000000000000000000000000000000-" + upstreamSpanHex + "-01",
		"all-zero span":    "00-" + upstreamTraceHex + "-0000000000000000-01",
		"a sentence":       "please trace this call for me thanks",
		"path-shaped":      "00-/Users/someone/private/file-" + upstreamSpanHex + "-01",
		"embedded newline": "00-" + upstreamTraceHex + "-" + upstreamSpanHex + "-01\ninjected",
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok := ParseTraceparent(bad); ok {
				t.Fatalf("%q must not parse, got %+v", bad, got)
			}
		})
	}
}

func TestEmitRecordsUpstreamTraceFromContextBesideTheDerivedIdentity(t *testing.T) {
	sink := &capturingSink{}
	recorder := New(WithSink(sink))
	upstream, _ := ParseTraceparent(sampledTraceparent)
	ctx := ContextWithUpstreamTrace(context.Background(), upstream)

	derived := TraceFor("standalone-local:test", "idem-1")
	recorder.Emit(ctx, Event{
		Name:        EventInteractionAdmitted,
		Correlation: Correlation{Trace: derived, InteractionID: "int-1"},
	})

	if len(sink.records) != 1 {
		t.Fatalf("recorded %d observations, want 1", len(sink.records))
	}
	record := sink.records[0]
	if record.TraceID != derived.String() {
		t.Fatalf("trace_id = %s, want the DERIVED identity %s; the upstream trace must never replace it", record.TraceID, derived)
	}
	if got := record.Attributes[AttrUpstreamTraceID]; got != upstreamTraceHex {
		t.Fatalf("%s = %v, want %s", AttrUpstreamTraceID, got, upstreamTraceHex)
	}
	if got := record.Attributes[AttrUpstreamSpanID]; got != upstreamSpanHex {
		t.Fatalf("%s = %v, want %s", AttrUpstreamSpanID, got, upstreamSpanHex)
	}
	// Payload safety: the only new values are fixed-width lowercase hex.
	for key, value := range record.Attributes {
		text, isString := value.(string)
		if !isString || !lowercaseHex(text) {
			t.Fatalf("attribute %s = %v is not lowercase hex", key, value)
		}
	}
}

func TestEmitWithoutUpstreamContextRecordsNoUpstreamAttributes(t *testing.T) {
	sink := &capturingSink{}
	recorder := New(WithSink(sink))
	recorder.Emit(context.Background(), Event{
		Name:        EventInteractionAdmitted,
		Correlation: Correlation{Trace: TraceFor("standalone-local:test", "idem-2")},
	})
	if len(sink.records) != 1 {
		t.Fatalf("recorded %d observations, want 1", len(sink.records))
	}
	if _, present := sink.records[0].Attributes[AttrUpstreamTraceID]; present {
		t.Fatal("no upstream context was attached, so no upstream attribute may appear")
	}
}

// The upstream keys are record-only. A call site that tries to smuggle a
// trace id in through Attrs has it dropped by the allowlist, which is what
// keeps a trace id out of the metric dimensions.
func TestUpstreamKeysAreNotAcceptedAsEventAttributes(t *testing.T) {
	sink := &capturingSink{}
	recorder := New(WithSink(sink))
	recorder.Emit(context.Background(), Event{
		Name:        EventInteractionAdmitted,
		Correlation: Correlation{Trace: TraceFor("standalone-local:test", "idem-3")},
		Attrs:       []Attr{String(AttrUpstreamTraceID, upstreamTraceHex)},
	})
	if _, present := sink.records[0].Attributes[AttrUpstreamTraceID]; present {
		t.Fatal("upstream_trace_id passed as an Attr must be dropped by the allowlist")
	}
	snapshot := recorder.Metrics().Snapshot()
	for _, list := range snapshot.Counters {
		for _, series := range list {
			if strings.Contains(series.Dimensions, AttrUpstreamTraceID) || strings.Contains(series.Dimensions, upstreamTraceHex) {
				t.Fatalf("a trace id must never become a metric dimension, got %q", series.Dimensions)
			}
		}
	}
}

func TestUpstreamContextHelpersRejectInvalidValues(t *testing.T) {
	ctx := ContextWithUpstreamTrace(context.Background(), UpstreamTrace{})
	if _, ok := UpstreamTraceFromContext(ctx); ok {
		t.Fatal("an invalid upstream must not be attached")
	}
	if _, ok := UpstreamTraceFromContext(context.Background()); ok {
		t.Fatal("a bare context carries no upstream")
	}
}

type capturingSink struct{ records []Record }

func (s *capturingSink) Append(_ context.Context, record Record) error {
	s.records = append(s.records, record)
	return nil
}
