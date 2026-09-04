package telemetry

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Instrument names. They are OpenTelemetry-shaped (dotted, lowercase, no
// units in the name) so the same strings serve the in-process registry and the
// OTel meter without translation.
const (
	// MetricPresentationDuration measures admission → presentation: how long a
	// caller's request waited before a human could see it. Source: the
	// interaction record's own created_at against the presentation
	// acknowledgement (internal/roomflow's disposition).
	MetricPresentationDuration = "tangent.presentation.duration"
	// MetricResolutionDuration measures presentation → resolution: how long
	// the human took. Source: the same disposition, against the presentation
	// instant it established.
	MetricResolutionDuration = "tangent.resolution.duration"
	// MetricDeliveryDuration measures resolution → terminal outcome delivered
	// to the caller. This is delivery lag.
	MetricDeliveryDuration = "tangent.delivery.duration"

	// MetricConnections counts attachments, dimensioned by whether the
	// attachment replaced its own predecessor. A reconnect is exactly
	// `replaced=true`.
	MetricConnections = "tangent.connection.attachments"
	// MetricDisconnections counts detachments.
	MetricDisconnections = "tangent.connection.detachments"
	// MetricPresentationRefusals counts participant actions the surface
	// declined. A stale client is a failed presentation compare-and-set or a
	// resolver-lease refusal, and both land here dimensioned by which.
	MetricPresentationRefusals = "tangent.presentation.refusals"

	// MetricDeliveryAttempts counts delivery outcomes by delivery state, which
	// is how retries become visible: a retryable_failure followed by a
	// delivered is one retried delivery.
	MetricDeliveryAttempts = "tangent.delivery.attempts"
	// MetricDraftRefusals counts refused draft revisions — draft conflicts.
	MetricDraftRefusals = "tangent.draft.refusals"
	// MetricRendererUnavailable counts definitions this build cannot serve,
	// dimensioned by materialization state.
	MetricRendererUnavailable = "tangent.renderer.unavailable"
	// MetricCapabilityDenials counts refusals in both capability namespaces,
	// dimensioned by namespace and by the denying rule where the host can tell
	// trust class from host policy.
	MetricCapabilityDenials = "tangent.capability.denials"

	// MetricReadinessChanges counts readiness checks changing verdict. It is a
	// transition counter, not a sample counter: a check that has been failing
	// for an hour contributes one.
	MetricReadinessChanges = "tangent.readiness.changes"
	// MetricInteractions counts lifecycle observations by state.
	MetricInteractions = "tangent.interactions"
	// MetricAttributesDropped counts attributes the redaction allowlist
	// refused. It is the canary: a non-zero value means a call site tried to
	// record something this package would not carry, and a test asserts it is
	// zero across the shipped paths.
	MetricAttributesDropped = "tangent.telemetry.attributes_dropped"
)

const (
	// maxSeriesPerInstrument bounds distinct dimension sets per instrument.
	// Every dimension in use is drawn from a closed vocabulary, so the real
	// cardinality is small; the bound exists because a bound that only holds
	// because the input is small is not a bound.
	maxSeriesPerInstrument = 128
	// overflowSeries is where observations go once an instrument is full. It
	// is a visible bucket rather than a dropped sample: losing the dimensions
	// is acceptable, losing the count is not.
	overflowSeries = "overflow"
)

// durationBucketsMS are the histogram boundaries, in milliseconds.
//
// They span a human's attention rather than a machine's: the fast end resolves
// a presentation that should be instant, and the slow end has to hold a person
// who left a decision open over lunch. A 45-second compatibility window and a
// 30-second resolver lease both fall inside, which is what makes the
// distribution readable against the timeouts it interacts with.
var durationBucketsMS = []float64{
	5, 10, 25, 50, 100, 250, 500,
	1_000, 2_500, 5_000, 10_000, 30_000, 45_000,
	60_000, 300_000, 1_800_000,
}

// Registry is the always-on in-process metric store.
//
// It exists because OpenTelemetry is off by default here (see otel.go): a
// build with no SDK installed still has to be able to answer criterion 2's
// questions, and "install a collector first" is not an answer during an
// incident on a single-user loopback host. Everything recorded here is also
// mirrored to the OTel instruments, so turning OTel on adds an exporter rather
// than adding instrumentation.
//
// It is bounded, in-memory, and lost on restart. That is deliberate: the
// durable record of what happened is telemetry_events, and a counter that
// tried to be durable would be a second, weaker copy of it.
type Registry struct {
	mu         sync.Mutex
	counters   map[string]map[string]int64
	histograms map[string]map[string]*histogram
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		counters:   map[string]map[string]int64{},
		histograms: map[string]map[string]*histogram{},
	}
}

type histogram struct {
	count   int64
	sumMS   float64
	minMS   float64
	maxMS   float64
	buckets []int64
}

// Count increments a counter series.
func (r *Registry) Count(instrument string, attrs []Attr, delta int64) {
	if r == nil || instrument == "" {
		return
	}
	series := seriesKey(attrs)
	r.mu.Lock()
	defer r.mu.Unlock()
	set, ok := r.counters[instrument]
	if !ok {
		set = map[string]int64{}
		r.counters[instrument] = set
	}
	if _, exists := set[series]; !exists && len(set) >= maxSeriesPerInstrument {
		series = overflowSeries
	}
	set[series] += delta
}

// Observe records one duration sample, in milliseconds.
func (r *Registry) Observe(instrument string, attrs []Attr, milliseconds float64) {
	if r == nil || instrument == "" || milliseconds < 0 {
		return
	}
	series := seriesKey(attrs)
	r.mu.Lock()
	defer r.mu.Unlock()
	set, ok := r.histograms[instrument]
	if !ok {
		set = map[string]*histogram{}
		r.histograms[instrument] = set
	}
	entry, exists := set[series]
	if !exists {
		if len(set) >= maxSeriesPerInstrument {
			series = overflowSeries
			entry = set[series]
		}
		if entry == nil {
			entry = &histogram{
				buckets: make([]int64, len(durationBucketsMS)+1),
				minMS:   milliseconds,
				maxMS:   milliseconds,
			}
			set[series] = entry
		}
	}
	entry.count++
	entry.sumMS += milliseconds
	if milliseconds < entry.minMS || entry.count == 1 {
		entry.minMS = milliseconds
	}
	if milliseconds > entry.maxMS {
		entry.maxMS = milliseconds
	}
	entry.buckets[bucketIndex(milliseconds)]++
}

func bucketIndex(milliseconds float64) int {
	for i, boundary := range durationBucketsMS {
		if milliseconds <= boundary {
			return i
		}
	}
	return len(durationBucketsMS)
}

// CounterSeries is one counter's value under one dimension set.
type CounterSeries struct {
	Dimensions string `json:"dimensions"`
	Value      int64  `json:"value"`
}

// HistogramSeries is one duration distribution under one dimension set.
type HistogramSeries struct {
	Dimensions string  `json:"dimensions"`
	Count      int64   `json:"count"`
	SumMS      float64 `json:"sum_ms"`
	MinMS      float64 `json:"min_ms"`
	MaxMS      float64 `json:"max_ms"`
	// BucketsMS pairs each boundary with its cumulative-exclusive count; the
	// final entry counts everything above the last boundary.
	BucketsMS map[string]int64 `json:"buckets_ms"`
}

// Snapshot is the whole registry, in a shape a report can render.
//
// Every string in it is either an instrument name, a dimension drawn from a
// closed vocabulary, or a number. There is nothing in a snapshot that could
// carry content, which is what makes it safe to return over MCP.
type Snapshot struct {
	Counters   map[string][]CounterSeries   `json:"counters"`
	Histograms map[string][]HistogramSeries `json:"histograms"`
}

// Snapshot renders the registry.
func (r *Registry) Snapshot() Snapshot {
	snapshot := Snapshot{
		Counters:   map[string][]CounterSeries{},
		Histograms: map[string][]HistogramSeries{},
	}
	if r == nil {
		return snapshot
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for instrument, set := range r.counters {
		series := make([]CounterSeries, 0, len(set))
		for dimensions, value := range set {
			series = append(series, CounterSeries{Dimensions: dimensions, Value: value})
		}
		sort.Slice(series, func(i, j int) bool { return series[i].Dimensions < series[j].Dimensions })
		snapshot.Counters[instrument] = series
	}
	for instrument, set := range r.histograms {
		series := make([]HistogramSeries, 0, len(set))
		for dimensions, entry := range set {
			series = append(series, HistogramSeries{
				Dimensions: dimensions,
				Count:      entry.count,
				SumMS:      entry.sumMS,
				MinMS:      entry.minMS,
				MaxMS:      entry.maxMS,
				BucketsMS:  renderBuckets(entry.buckets),
			})
		}
		sort.Slice(series, func(i, j int) bool { return series[i].Dimensions < series[j].Dimensions })
		snapshot.Histograms[instrument] = series
	}
	return snapshot
}

func renderBuckets(counts []int64) map[string]int64 {
	rendered := make(map[string]int64, len(counts))
	for i, boundary := range durationBucketsMS {
		if counts[i] == 0 {
			continue
		}
		rendered["le_"+strconv.FormatFloat(boundary, 'f', -1, 64)] = counts[i]
	}
	if counts[len(counts)-1] != 0 {
		rendered["gt_max"] = counts[len(counts)-1]
	}
	return rendered
}

// seriesKey renders a sanitized attribute set as a stable dimension string.
// Attributes arrive sorted from sanitizeAttrs, so the same dimensions always
// produce the same key.
func seriesKey(attrs []Attr) string {
	if len(attrs) == 0 {
		return ""
	}
	var builder strings.Builder
	for i, attr := range attrs {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(attr.Key)
		builder.WriteByte('=')
		switch attr.kind {
		case attrString:
			builder.WriteString(attr.str)
		case attrInt:
			builder.WriteString(strconv.FormatInt(attr.num, 10))
		case attrBool:
			builder.WriteString(strconv.FormatBool(attr.flag))
		}
	}
	return builder.String()
}
