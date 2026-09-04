package telemetry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Nothing here stubs the thing it is testing. The redaction assertions run
// real attributes through the real allowlist, the store assertions run against
// a real SQLite database with the real migration applied, and the leak
// assertion plants distinctive tokens in every position a caller could reach
// and sweeps everything the package can emit.

// --- correlation -------------------------------------------------------------

func TestTraceIsDerivedAndStable(t *testing.T) {
	caller := "standalone-local:anonymous"
	key := "workflow:tangent.triage:env-1"

	first := TraceFor(caller, key)
	second := TraceFor(caller, key)
	if first != second {
		t.Fatalf("the same invocation derived two traces: %s vs %s", first, second)
	}
	if !first.Valid() {
		t.Fatal("a real caller scope and idempotency key derived the zero trace")
	}
	// A retry is the same interaction, so it must be the same trace. This is
	// the property that makes the identity reconstructible rather than
	// remembered.
	if TraceForRecord(caller, key, "interaction-a") != first {
		t.Fatal("reconstruction from the durable record produced a different trace")
	}
	if TraceForRecord(caller, key, "interaction-b") != first {
		t.Fatal("the trace depends on the interaction id, so a record read cannot reproduce it")
	}
	if TraceFor(caller, "workflow:tangent.triage:env-2") == first {
		t.Fatal("two different invocations share a trace")
	}
	if TraceFor("gateway:other", key) == first {
		t.Fatal("two callers share a trace for the same key")
	}
}

func TestTraceWithoutIdempotencyFallsBackToInteraction(t *testing.T) {
	fallback := TraceForRecord("", "", "interaction-x")
	if !fallback.Valid() {
		t.Fatal("a record with no idempotency identity derived no trace at all")
	}
	if fallback != TraceForRecord("", "", "interaction-x") {
		t.Fatal("the fallback is not stable")
	}
	if fallback == TraceForRecord("", "", "interaction-y") {
		t.Fatal("two interactions collapsed onto one fallback trace")
	}
}

func TestParseTraceIDRoundTrips(t *testing.T) {
	original := TraceForKind("tangent.triage")
	parsed, ok := ParseTraceID(original.String())
	if !ok || parsed != original {
		t.Fatalf("round trip failed: %v %v", parsed, ok)
	}
	for _, bad := range []string{"", "not-hex", "deadbeef", strings.Repeat("0", 32)} {
		if _, ok := ParseTraceID(bad); ok {
			t.Fatalf("accepted %q as a trace id", bad)
		}
	}
}

// --- redaction ---------------------------------------------------------------

// TestAllowlistDropsEverythingItDoesNotName is the structural half of the
// redaction rule: a key that is not in the allowlist has nowhere to go.
func TestAllowlistDropsEverythingItDoesNotName(t *testing.T) {
	kept, dropped := sanitizeAttrs([]Attr{
		String("error_message", "sqlite: unable to open /Users/someone/.tangent/tangent.db"),
		String("terminal_reason", "the participant said no"),
		String("payload", `{"answer":"secret"}`),
		String("path", "/etc/passwd"),
		String("url", "http://127.0.0.1:7842/r/abc"),
		String("session_id", "cookie-value"),
		String("handle_id", "handle-1"),
		String(AttrMode, "wait"),
	})
	if dropped != 7 {
		t.Fatalf("dropped %d attributes, want 7", dropped)
	}
	if len(kept) != 1 || kept[0].Key != AttrMode || kept[0].str != "wait" {
		t.Fatalf("allowlist kept %+v", kept)
	}
}

// TestVocabularyReplacesRatherThanTruncates is the value half. A value outside
// its key's closed set is replaced whole, so a report can never contain the
// first sixty-four bytes of something forbidden.
func TestVocabularyReplacesRatherThanTruncates(t *testing.T) {
	kept, _ := sanitizeAttrs([]Attr{
		String(AttrMode, "wait; and also /Users/someone/secret.txt"),
		String(AttrState, "presented"),
		String(AttrRefusal, "the presentation this response was rendered from is stale"),
	})
	byKey := map[string]string{}
	for _, attr := range kept {
		byKey[attr.Key] = attr.str
	}
	if byKey[AttrMode] != unclassified {
		t.Fatalf("mode = %q, want it replaced whole", byKey[AttrMode])
	}
	if byKey[AttrState] != "presented" {
		t.Fatalf("a legitimate value was refused: %q", byKey[AttrState])
	}
	if byKey[AttrRefusal] != unclassified {
		t.Fatalf("refusal = %q, want it replaced whole", byKey[AttrRefusal])
	}
}

func TestIdentifierShapeRejectsPathsAndSentences(t *testing.T) {
	for _, forbidden := range []string{
		"/Users/someone/.tangent/tangent.db",
		"http://127.0.0.1:7842/r/room-1",
		"the participant declined",
		"C:\\Users\\someone\\secret",
		strings.Repeat("a", maxAttributeValueBytes+1),
		"has space",
	} {
		if got := boundIdentifier(forbidden); got != "" {
			t.Fatalf("boundIdentifier(%q) = %q, want it dropped", forbidden, got)
		}
	}
	if boundIdentifier("tangent.form-collect") != "tangent.form-collect" {
		t.Fatal("a legitimate kind was dropped")
	}
	if boundLabel("standalone-local:anonymous") != "standalone-local:anonymous" {
		t.Fatal("a legitimate scope was dropped")
	}
	if boundLabel("http://host/path") != "" {
		t.Fatal("a URL passed the label check")
	}
}

// TestCodeNeverCarriesAMessage is the single most important assertion in the
// package: a future call site that reaches for err.Error() gets a code, not a
// message. A SQLite error string carries the database file path.
func TestCodeNeverCarriesAMessage(t *testing.T) {
	leak := "sqlite: unable to open database file /Users/someone/.tangent/tangent.db"
	if got := Code(leak); got != unclassified {
		t.Fatalf("Code(%q) = %q", leak, got)
	}
	if got := Code("effect_capability_denied"); got != "effect_capability_denied" {
		t.Fatalf("a real refusal code was rewritten to %q", got)
	}
	if got := CodeForError(errWrapped{}, nil); got != unclassified {
		t.Fatalf("an unclassified error produced %q", got)
	}
	if got := CodeForError(context.DeadlineExceeded, nil); got != "wait_timeout" {
		t.Fatalf("a deadline produced %q", got)
	}
}

type errWrapped struct{}

func (errWrapped) Error() string {
	return "open /Users/someone/.tangent/tangent.db: permission denied"
}

// TestNoObservationCarriesAPlantedToken is the leak sweep.
//
// A distinctive token is planted in every position a call site could put one:
// each correlation field, each attribute key the allowlist knows, an
// unallowlisted key, and the code. The emitted record — the exact bytes the
// audit table stores and the query surface returns — is then swept for the
// token and for the shapes ADR 0002 §8 forbids outright.
//
// What this test does *not* prove, stated plainly: the correlation fields are
// identifier slots, and an identifier-shaped secret placed in one is
// indistinguishable by shape from the server-issued UUID that belongs there.
// A connection id, an interaction id, and an effect handle id are all UUIDs.
// The defense against that is not the type — it is that no call site writes
// one, which is asserted end to end in internal/mcp's leak test against the
// real workflow path rather than against a constructed event here.
func TestNoObservationCarriesAPlantedToken(t *testing.T) {
	const token = "ZZLEAKCANARY"
	planted := map[string]string{
		"payload":      "answer=" + token,
		"participant":  "the operator wrote " + token,
		"code":         "/* " + token + " */ console.log(1)",
		"screenshot":   "data:image/png;base64," + token,
		"path":         "/Users/" + token + "/.tangent/tangent.db",
		"secret":       "Bearer " + token,
		"response":     `{"payload":{"notes":"` + token + `"}}`,
		"cookie":       "tangent_participant=" + token,
		"cookie_hash":  "sha256:" + token,
		"handle_id":    "handle-" + token,
		"capability":   "file.read_scoped?key=" + token,
		"room_url":     "http://127.0.0.1:7842/r/" + token,
		"terminal":     "the caller withdrew because " + token,
		"driver_error": "sqlite: /var/folders/" + token + "/tangent.db is locked",
	}

	recorder := New()
	attrs := []Attr{
		// Every planted value tried through an allowlisted key…
		String(AttrMode, planted["payload"]),
		String(AttrState, planted["participant"]),
		String(AttrRefusal, planted["terminal"]),
		String(AttrCapability, planted["capability"]),
		String(AttrTrustClass, planted["path"]),
		// …and through keys the allowlist does not name.
		String("error_message", planted["driver_error"]),
		String("response_payload", planted["response"]),
		String("session_cookie", planted["cookie"]),
		String("session_hash", planted["cookie_hash"]),
		String("effect_handle_id", planted["handle_id"]),
		String("room_url", planted["room_url"]),
		String("screenshot", planted["screenshot"]),
	}
	event := Event{
		Name:    EventInteractionResolved,
		Outcome: OutcomeOK,
		Code:    planted["driver_error"],
		Correlation: Correlation{
			Trace:             TraceFor("standalone-local:anonymous", "workflow:k:e"),
			Span:              NewSpanID(),
			SurfaceID:         planted["path"],
			InteractionID:     planted["secret"],
			RoomID:            planted["room_url"],
			EnvelopeID:        planted["cookie"],
			ConnectionID:      planted["handle_id"] + "/../" + planted["secret"],
			DefinitionKind:    planted["code"],
			DefinitionVersion: planted["screenshot"],
			CallerScope:       planted["response"],
			ParticipantScope:  planted["participant"],
		},
		Attrs: attrs,
		At:    time.Unix(1_772_000_000, 0).UTC(),
	}
	event.Outcome = OutcomeOK
	event.Correlation = event.Correlation.sanitized()
	kept, _ := sanitizeAttrs(event.Attrs)
	event.Attrs = kept
	event.Code = Code(event.Code)
	record := recordFor(event)

	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	sweep(t, "audit record", string(encoded), token)

	// The metric snapshot is the other thing a report hands out.
	recorder.Emit(context.Background(), event)
	snapshot, err := json.Marshal(recorder.Metrics().Snapshot())
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	sweep(t, "metric snapshot", string(snapshot), token)
}

// sweep asserts that text carries neither the planted token nor any of the
// shapes ADR 0002 §8 forbids regardless of where they came from.
func sweep(t *testing.T, what, text, token string) {
	t.Helper()
	if strings.Contains(text, token) {
		t.Fatalf("%s carried the planted token:\n%s", what, text)
	}
	for _, forbidden := range []string{
		".db", "/Users/", "/var/", "/tmp/", "sqlite", "http://", "https://",
		"Bearer", "data:image", "tangent_participant", "console.log",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("%s carried %q:\n%s", what, forbidden, text)
		}
	}
}

// TestDroppedAttributesAreCounted proves the canary works: the whole redaction
// guarantee rests on being able to notice a call site that tried.
func TestDroppedAttributesAreCounted(t *testing.T) {
	recorder := New()
	recorder.Emit(context.Background(), Event{
		Name:        EventInteractionAdmitted,
		Outcome:     OutcomeOK,
		Correlation: Correlation{Trace: TraceForKind("tangent.triage")},
		Attrs:       []Attr{String("error_message", "anything at all")},
	})
	series := recorder.Metrics().Snapshot().Counters[MetricAttributesDropped]
	if len(series) != 1 || series[0].Value != 1 {
		t.Fatalf("dropped-attribute canary = %+v, want one drop recorded", series)
	}
}

// TestEveryDeclaredAttributeIsAllowlisted closes the gap the end-to-end leak
// sweep found the hard way: a key can be declared as a constant, used at a
// call site, and silently dropped because it was never added to a vocabulary.
// That never leaked — dropping is the safe direction — but it is a bug in the
// call site, and finding it in an integration test rather than here is a
// waste of everyone's afternoon.
func TestEveryDeclaredAttributeIsAllowlisted(t *testing.T) {
	declared := []string{
		AttrMode, AttrState, AttrTerminalCause, AttrRefusal, AttrNamespace,
		AttrDeniedBy, AttrCapability, AttrMaterializationState, AttrTrustClass,
		AttrIsolation, AttrRole, AttrClientKind, AttrVerdict,
	}
	for _, key := range declared {
		if !allowedStringKey(key) {
			t.Fatalf("string attribute %q is declared but not allowlisted", key)
		}
	}
	numeric := []string{
		AttrReplaced, AttrLeaseInherited, AttrRevision, AttrPresentedRevision,
		AttrExpectedRevision, AttrAttempt, AttrConnections, AttrCount,
	}
	for _, key := range numeric {
		if !allowedNumericKeys[key] {
			t.Fatalf("numeric attribute %q is declared but not allowlisted", key)
		}
	}
	// The two enumerated keys that are neither: they carry strings drawn from
	// closed sets and are covered above, so this is only a reminder that the
	// list has to grow with the constants.
	for _, key := range []string{AttrDeliveryState, AttrProbe, AttrTransport} {
		if !allowedStringKey(key) {
			t.Fatalf("string attribute %q is declared but not allowlisted", key)
		}
	}
}

// --- metrics -----------------------------------------------------------------

func TestHistogramBucketsAndDimensions(t *testing.T) {
	registry := NewRegistry()
	dimensions := []Attr{String(AttrMode, "wait")}
	registry.Observe(MetricPresentationDuration, dimensions, 7)
	registry.Observe(MetricPresentationDuration, dimensions, 7_000)
	registry.Observe(MetricPresentationDuration, dimensions, 4_000_000)

	series := registry.Snapshot().Histograms[MetricPresentationDuration]
	if len(series) != 1 {
		t.Fatalf("histogram series = %+v", series)
	}
	entry := series[0]
	if entry.Count != 3 || entry.MinMS != 7 || entry.MaxMS != 4_000_000 {
		t.Fatalf("histogram = %+v", entry)
	}
	if entry.BucketsMS["gt_max"] != 1 {
		t.Fatalf("the over-ceiling sample was not counted: %+v", entry.BucketsMS)
	}
	if entry.Dimensions != "mode=wait" {
		t.Fatalf("dimensions = %q", entry.Dimensions)
	}
}

func TestSeriesCardinalityIsBounded(t *testing.T) {
	registry := NewRegistry()
	// Numbers are never metric dimensions on the emission path, but the
	// registry must hold its bound against a caller that gets it wrong.
	for i := range maxSeriesPerInstrument * 2 {
		registry.Count(MetricInteractions, []Attr{Int(AttrRevision, int64(i))}, 1)
	}
	series := registry.Snapshot().Counters[MetricInteractions]
	if len(series) > maxSeriesPerInstrument+1 {
		t.Fatalf("cardinality bound broken: %d series", len(series))
	}
	var overflow int64
	for _, entry := range series {
		if entry.Dimensions == overflowSeries {
			overflow = entry.Value
		}
	}
	if overflow == 0 {
		t.Fatal("samples past the bound were dropped rather than counted as overflow")
	}
}

// TestNilRecorderIsSafe is why no call site has a nil check.
func TestNilRecorderIsSafe(t *testing.T) {
	var recorder *Recorder
	recorder.Emit(context.Background(), Event{Name: EventInteractionAdmitted})
	if recorder.Metrics() != nil {
		t.Fatal("a nil recorder produced a registry")
	}
	if recorder.Now().IsZero() {
		t.Fatal("a nil recorder produced no clock")
	}
}

// TestUnknownEventNamesAreRefused keeps the event vocabulary closed. An event
// name is exported as a span name, and a name assembled from anything a caller
// supplied is a leak with a different shape.
func TestUnknownEventNamesAreRefused(t *testing.T) {
	recorder := New()
	recorder.Emit(context.Background(), Event{
		Name:        "interaction." + "ZZLEAK",
		Outcome:     OutcomeOK,
		Correlation: Correlation{Trace: TraceForKind("k")},
	})
	if len(recorder.Metrics().Snapshot().Counters) != 0 {
		t.Fatal("an unknown event name was recorded")
	}
}
