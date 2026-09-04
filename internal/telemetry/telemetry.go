// Package telemetry gives one caller invocation a single identity that
// survives every boundary it crosses, and records what happened to it
// somewhere that is not the process log.
//
// # The problem
//
// A Tangent request is answered by a person. Between the MCP tool call and the
// caller receiving an outcome there is a browser, a WebSocket, a durable
// store, and a delivery that may happen after the transport that asked for it
// has gone. Before this package, each of those emitted its own slog line with
// its own idea of what to call the thing — `room`, `envelope`, `interaction`,
// `connection` — and nothing tied them together. Reconstructing one
// invocation meant grepping stderr and hoping the process had not restarted.
//
// # The three parts
//
//   - **Correlation** (correlation.go). A trace identity *derived* from the
//     caller scope and idempotency key rather than propagated, so any process
//     at any later time can recompute it from the durable record. Nothing has
//     to carry a header, which matters because the browser is not ours to
//     instrument and a restart is not a new request.
//   - **Redaction** (attributes.go). ADR 0002 §8 expressed as a type: a closed
//     key allowlist, closed value vocabularies, no free-text field anywhere,
//     and a dropped-attribute counter that a test asserts is zero. There is
//     nowhere in an observation to put a payload, a participant's words, a
//     path, a session, an effect handle, or an assembled URL.
//   - **Durability** (store.go). An append-only `telemetry_events` table, so
//     "what happened to this invocation" is a query and not an exercise in log
//     retention. Metrics live in memory (metrics.go) and are mirrored to
//     OpenTelemetry when a host opts in (otel.go).
//
// # What this package deliberately does not do
//
// It does not decide anything. No lifecycle depends on it, no authorization
// consults it, and a recorder that fails writes nothing and returns nothing.
// A telemetry write that could fail the operation it observes would be a worse
// defect than no telemetry at all, which is why the durable append is
// best-effort and why every constructor accepts a nil recorder.
package telemetry

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Sink is the durable destination for observations. *SQLStore satisfies it;
// the interface exists so a test can assert on what was recorded without a
// database, and so a host with no durable state still gets metrics.
type Sink interface {
	Append(ctx context.Context, record Record) error
}

// Record is one sanitized observation, in the shape the audit table stores.
//
// It is produced only by the recorder, from an Event that has already been
// through the allowlist. Nothing constructs one directly on a write path.
type Record struct {
	EventID      string `json:"event_id"`
	TraceID      string `json:"trace_id"`
	SpanID       string `json:"span_id"`
	ParentSpanID string `json:"parent_span_id,omitempty"`

	Name    string `json:"event_name"`
	Outcome string `json:"outcome"`
	Code    string `json:"code,omitempty"`

	OccurredAt time.Time `json:"occurred_at"`
	// DurationMS is nil for an instantaneous observation.
	DurationMS *int64 `json:"duration_ms,omitempty"`

	SurfaceID         string `json:"surface_id,omitempty"`
	InteractionID     string `json:"interaction_id,omitempty"`
	RoomID            string `json:"room_id,omitempty"`
	EnvelopeID        string `json:"envelope_id,omitempty"`
	ConnectionID      string `json:"connection_id,omitempty"`
	DefinitionKind    string `json:"definition_kind,omitempty"`
	DefinitionVersion string `json:"definition_version,omitempty"`
	CallerScope       string `json:"caller_scope,omitempty"`
	ParticipantScope  string `json:"participant_scope,omitempty"`

	Attributes map[string]any `json:"attributes,omitempty"`
}

// Recorder is the single way an operational fact becomes observable.
//
// The zero value is not usable; use New. A nil *Recorder is, deliberately:
// every method tolerates it, so a call site never needs a nil check and an
// embedder that wires no telemetry gets a silent no-op instead of a panic.
type Recorder struct {
	sink    Sink
	metrics *Registry
	otel    *otelBridge
	logger  *slog.Logger
	clock   func() time.Time
}

// Option configures a Recorder.
type Option func(*Recorder)

// WithSink installs the durable audit destination. Omitting it leaves metrics
// in memory and writes no audit rows, which is the honest state of a build
// with no database.
func WithSink(sink Sink) Option {
	return func(r *Recorder) {
		if sink != nil {
			r.sink = sink
		}
	}
}

// WithLogger installs the logger used for the one thing this package logs: a
// durable append that did not land. The message names the event and nothing
// else — never the driver error, which on SQLite carries the database path.
func WithLogger(logger *slog.Logger) Option {
	return func(r *Recorder) {
		if logger != nil {
			r.logger = logger
		}
	}
}

// WithClock pins the clock. Tests use it; production leaves it alone.
func WithClock(clock func() time.Time) Option {
	return func(r *Recorder) {
		if clock != nil {
			r.clock = clock
		}
	}
}

// WithOpenTelemetry enables the OpenTelemetry bridge. It is off by default and
// must be turned on explicitly, because the OTel providers are process-global
// and a host that never asked for telemetry to leave the machine must not
// start exporting because some other dependency installed an SDK. See otel.go.
func WithOpenTelemetry(enabled bool) Option {
	return func(r *Recorder) { r.otel.enabled = enabled }
}

// New builds a recorder. It never fails: a recorder missing a sink records
// metrics, and a recorder missing everything is a no-op that call sites do not
// have to branch on.
func New(options ...Option) *Recorder {
	recorder := &Recorder{
		metrics: NewRegistry(),
		otel:    &otelBridge{},
		logger:  slog.Default(),
		clock:   func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		if option != nil {
			option(recorder)
		}
	}
	return recorder
}

// Metrics returns the in-process registry. It is exposed so a health or MCP
// report can render a snapshot without this package depending on either.
func (r *Recorder) Metrics() *Registry {
	if r == nil {
		return nil
	}
	return r.metrics
}

// Now reads the recorder's clock. Call sites use it to stamp the start of an
// operation so the duration they later report and the instant the audit row
// carries come from one source.
func (r *Recorder) Now() time.Time {
	if r == nil {
		return time.Now().UTC()
	}
	return r.clock()
}

// instrumentsFor maps an event onto the metrics it feeds.
//
// It is a table rather than a switch inside Emit so that adding an event
// without deciding what it measures is a visible omission: an event with no
// row here records an audit fact and no metric, which is sometimes right and
// should never be accidental.
var instrumentsFor = map[string]struct {
	counter   string
	histogram string
}{
	EventInteractionAdmitted:  {counter: MetricInteractions},
	EventInteractionPresented: {counter: MetricInteractions, histogram: MetricPresentationDuration},
	EventInteractionResolved:  {counter: MetricInteractions, histogram: MetricResolutionDuration},
	EventInteractionCanceled:  {counter: MetricInteractions},
	EventInteractionPending:   {counter: MetricInteractions},
	EventInteractionRefused:   {counter: MetricInteractions},
	EventPresentationRefused:  {counter: MetricPresentationRefusals},
	EventConnectionAttached:   {counter: MetricConnections},
	EventConnectionDetached:   {counter: MetricDisconnections},
	EventDraftRefused:         {counter: MetricDraftRefusals},
	EventDeliveryRecorded:     {counter: MetricDeliveryAttempts, histogram: MetricDeliveryDuration},
	EventDeliveryFailed:       {counter: MetricDeliveryAttempts},
	EventCapabilityDenied:     {counter: MetricCapabilityDenials},
	EventReadinessDegraded:    {counter: MetricReadinessChanges},
	EventRendererUnavailable:  {counter: MetricRendererUnavailable},
}

// Emit records one observation.
//
// The order is metrics, then the OpenTelemetry mirror, then the durable
// append. Metrics first because they are the surface an incident reads and
// must not be lost to a database problem; the durable append last because it
// is the only step that can fail, and its failure must cost nothing that came
// before it.
//
// The append is synchronous. An asynchronous queue would keep the participant
// path a few hundred microseconds shorter and would introduce a goroutine, a
// shutdown ordering problem, and a window in which a crash loses the
// observation of the crash. On a single-user loopback host writing to a
// local WAL that is the wrong trade.
func (r *Recorder) Emit(ctx context.Context, event Event) {
	if r == nil || !eventNames[event.Name] {
		return
	}
	if !event.Outcome.valid() {
		event.Outcome = OutcomeOK
	}
	if event.At.IsZero() {
		event.At = r.clock()
	}
	if event.Duration < 0 {
		event.Duration = 0
	}
	event.Code = Code(event.Code)
	event.Correlation = event.Correlation.sanitized()
	if !event.Correlation.Span.Valid() {
		event.Correlation.Span = NewSpanID()
	}

	attrs, dropped := sanitizeAttrs(event.Attrs)
	event.Attrs = attrs

	dimensions := metricDimensions(event, attrs)
	if instruments, ok := instrumentsFor[event.Name]; ok {
		if instruments.counter != "" {
			r.metrics.Count(instruments.counter, dimensions, 1)
			r.otel.count(ctx, instruments.counter, dimensions, 1)
		}
		if instruments.histogram != "" && event.Duration > 0 {
			milliseconds := float64(event.Duration) / float64(time.Millisecond)
			r.metrics.Observe(instruments.histogram, dimensions, milliseconds)
			r.otel.observe(ctx, instruments.histogram, dimensions, milliseconds)
		}
	}
	if dropped > 0 {
		// The canary. A non-zero count means a call site tried to record
		// something the allowlist refused, which is a bug in that call site and
		// is asserted to be zero across the shipped paths.
		r.metrics.Count(MetricAttributesDropped, []Attr{String("event", event.Name)}, int64(dropped))
	}

	r.otel.span(ctx, event, attrs)

	if r.sink == nil {
		return
	}
	record := recordFor(event)
	// Detached from the caller's context on purpose. An observation of a
	// cancelled operation is exactly the observation worth keeping, and
	// inheriting the cancellation would drop it precisely when the timeout is
	// the thing being investigated.
	appendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), appendBudget)
	defer cancel()
	if err := r.sink.Append(appendCtx, record); err != nil {
		// Deliberately without the error. A SQLite error string carries the
		// database file path, and ADR 0002 §8 forbids it in every log as
		// firmly as in every span.
		r.logger.Debug("telemetry: durable observation not recorded", "event", event.Name)
	}
}

// appendBudget bounds one durable append. A telemetry write that can hang is a
// telemetry write that can stall a participant's submission.
const appendBudget = 2 * time.Second

// metricDimensions selects which attributes become metric labels.
//
// String attributes come from closed vocabularies and are safe to dimension
// on. Numeric attributes are not — a revision number as a label is unbounded
// cardinality — so they reach the audit row and stop there. The two booleans
// are included because two values is not cardinality.
//
// `event` and `outcome` are always present so a shared instrument stays
// self-describing: `tangent.interactions` without an event label would be one
// undifferentiated total.
func metricDimensions(event Event, attrs []Attr) []Attr {
	dimensions := make([]Attr, 0, len(attrs)+3)
	dimensions = append(dimensions,
		Attr{Key: "event", kind: attrString, str: event.Name},
		Attr{Key: "outcome", kind: attrString, str: string(event.Outcome)},
	)
	if event.Code != "" {
		dimensions = append(dimensions, Attr{Key: "code", kind: attrString, str: event.Code})
	}
	if kind := event.Correlation.DefinitionKind; kind != "" {
		dimensions = append(dimensions, Attr{Key: "kind", kind: attrString, str: kind})
	}
	for _, attr := range attrs {
		switch {
		case attr.kind == attrString && attr.str != "":
			dimensions = append(dimensions, attr)
		case attr.kind == attrBool:
			dimensions = append(dimensions, attr)
		}
	}
	return dimensions
}

func recordFor(event Event) Record {
	correlation := event.Correlation
	record := Record{
		EventID:           uuid.NewString(),
		TraceID:           correlation.Trace.String(),
		SpanID:            correlation.Span.String(),
		Name:              event.Name,
		Outcome:           string(event.Outcome),
		Code:              event.Code,
		OccurredAt:        event.At,
		SurfaceID:         correlation.SurfaceID,
		InteractionID:     correlation.InteractionID,
		RoomID:            correlation.RoomID,
		EnvelopeID:        correlation.EnvelopeID,
		ConnectionID:      correlation.ConnectionID,
		DefinitionKind:    correlation.DefinitionKind,
		DefinitionVersion: correlation.DefinitionVersion,
		CallerScope:       correlation.CallerScope,
		ParticipantScope:  correlation.ParticipantScope,
	}
	if correlation.Parent.Valid() {
		record.ParentSpanID = correlation.Parent.String()
	}
	if event.Duration > 0 {
		milliseconds := event.Duration.Milliseconds()
		record.DurationMS = &milliseconds
	}
	if len(event.Attrs) > 0 {
		record.Attributes = make(map[string]any, len(event.Attrs))
		for _, attr := range event.Attrs {
			switch attr.kind {
			case attrString:
				if attr.str != "" {
					record.Attributes[attr.Key] = attr.str
				}
			case attrInt:
				record.Attributes[attr.Key] = attr.num
			case attrBool:
				record.Attributes[attr.Key] = attr.flag
			}
		}
	}
	return record
}

// encodeAttributes renders an attribute map for storage. A map that cannot be
// encoded becomes `{}` rather than failing the append: the correlation columns
// are the load-bearing part of a row, and losing the attributes is a smaller
// loss than losing the row.
func encodeAttributes(attributes map[string]any) string {
	if len(attributes) == 0 {
		return "{}"
	}
	raw, err := json.Marshal(attributes)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
