package telemetry

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

// The store assertions run against a real SQLite file with the real migration
// applied. An append-only guarantee proved against a fake is a statement about
// the fake.

func newStore(t *testing.T) (*SQLStore, *sql.DB) {
	t.Helper()
	database, err := tangentdb.Open(t.TempDir() + "/tangent.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if migrateErr := tangentdb.RunMigrations(database); migrateErr != nil {
		t.Fatalf("migrate: %v", migrateErr)
	}
	t.Cleanup(func() { _ = tangentdb.Close(database) })
	store, err := NewSQLStore(database)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store, database
}

// TestTraceReadsOneInvocationInOrder is criterion 1's query: one identity, and
// everything any boundary observed about it, oldest first.
func TestTraceReadsOneInvocationInOrder(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	trace := TraceFor("standalone-local:anonymous", "workflow:tangent.triage:env-1")
	other := TraceFor("standalone-local:anonymous", "workflow:tangent.triage:env-2")

	base := time.Unix(1_772_000_000, 0).UTC()
	recorder := New(WithSink(store), WithClock(func() time.Time { return base }))
	for i, name := range []string{
		EventInteractionAdmitted, EventInteractionPresented, EventInteractionResolved,
	} {
		recorder.Emit(ctx, Event{
			Name:        name,
			Outcome:     OutcomeOK,
			Correlation: Correlation{Trace: trace, Span: NewSpanID(), InteractionID: "interaction-1"},
			At:          base.Add(time.Duration(i) * time.Second),
			Duration:    time.Duration(i+1) * 100 * time.Millisecond,
		})
	}
	recorder.Emit(ctx, Event{
		Name:        EventInteractionAdmitted,
		Outcome:     OutcomeOK,
		Correlation: Correlation{Trace: other, Span: NewSpanID(), InteractionID: "interaction-2"},
		At:          base,
	})

	records, err := store.Trace(ctx, trace, 0)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("trace returned %d observations, want 3", len(records))
	}
	want := []string{EventInteractionAdmitted, EventInteractionPresented, EventInteractionResolved}
	for i, record := range records {
		if record.Name != want[i] {
			t.Fatalf("observation %d = %q, want %q", i, record.Name, want[i])
		}
		if record.TraceID != trace.String() {
			t.Fatalf("observation %d carried trace %q", i, record.TraceID)
		}
		if record.DurationMS == nil {
			t.Fatalf("observation %d lost its duration", i)
		}
	}

	// The other invocation is a different trace and must not be swept in.
	byInteraction, err := store.Query(ctx, Query{InteractionID: "interaction-2"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(byInteraction) != 1 {
		t.Fatalf("interaction query returned %d, want 1", len(byInteraction))
	}
}

// TestObservationsAreAppendOnly proves the schema's own guarantee. DELETE is
// deliberately permitted (retention); UPDATE is not.
func TestObservationsAreAppendOnly(t *testing.T) {
	store, database := newStore(t)
	ctx := context.Background()
	recorder := New(WithSink(store))
	recorder.Emit(ctx, Event{
		Name:        EventPresentationRefused,
		Outcome:     OutcomeRefused,
		Code:        "stale_presentation",
		Correlation: Correlation{Trace: TraceForRoom("room-1"), Span: NewSpanID(), RoomID: "room-1"},
	})
	_, err := database.Exec(`UPDATE telemetry_events SET outcome = 'ok'`)
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("update error = %v, want the append-only trigger", err)
	}
	if _, deleteErr := database.Exec(`DELETE FROM telemetry_events`); deleteErr != nil {
		t.Fatalf("retention delete refused: %v", deleteErr)
	}
}

// TestAggregateSurvivesTheProcess is why criterion 4 needs a table rather than
// a counter: the in-process registry answers "what has this process seen", and
// after a restart that is nothing.
func TestAggregateSurvivesTheProcess(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	recorder := New(WithSink(store))
	for range 3 {
		recorder.Emit(ctx, Event{
			Name:        EventPresentationRefused,
			Outcome:     OutcomeRefused,
			Code:        "resolver_lease_held",
			Correlation: Correlation{Trace: TraceForRoom("room-1"), Span: NewSpanID()},
		})
	}
	// A second recorder is a second process as far as the registry is
	// concerned: it starts empty and the durable aggregate does not.
	fresh := New(WithSink(store))
	if len(fresh.Metrics().Snapshot().Counters) != 0 {
		t.Fatal("a fresh registry was not empty")
	}
	rows, err := store.Aggregate(ctx, time.Time{})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(rows) != 1 || rows[0].Count != 3 || rows[0].Code != "resolver_lease_held" {
		t.Fatalf("aggregate = %+v", rows)
	}
}

func TestPruneBoundsRetention(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	recorder := New(WithSink(store))
	for i := range 10 {
		recorder.Emit(ctx, Event{
			Name:        EventConnectionAttached,
			Outcome:     OutcomeOK,
			Correlation: Correlation{Trace: TraceForRoom("room-1"), Span: NewSpanID()},
			At:          now.Add(-time.Duration(i) * 24 * time.Hour),
		})
	}
	pruned, err := store.Prune(ctx, now.Add(-5*24*time.Hour), 0)
	if err != nil {
		t.Fatalf("prune by age: %v", err)
	}
	// Ten observations, one per day going back; the cutoff is exclusive, so the
	// four strictly older than five days go and the one exactly at the boundary
	// stays.
	if pruned != 4 {
		t.Fatalf("age prune removed %d, want 4", pruned)
	}
	pruned, err = store.Prune(ctx, time.Time{}, 2)
	if err != nil {
		t.Fatalf("prune by count: %v", err)
	}
	if pruned != 4 {
		t.Fatalf("count prune removed %d, want 4", pruned)
	}
	remaining, err := store.Query(ctx, Query{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("%d observations remain, want 2", len(remaining))
	}
}

// TestStoreErrorsNeverCarryTheDriverMessage closes the leak internal/health
// found first: a SQLite error string names the database file.
func TestStoreErrorsNeverCarryTheDriverMessage(t *testing.T) {
	store, database := newStore(t)
	if err := tangentdb.Close(database); err != nil {
		t.Fatalf("close: %v", err)
	}
	ctx := context.Background()
	appendErr := store.Append(ctx, Record{
		EventID: "e1", TraceID: TraceForRoom("r").String(), SpanID: NewSpanID().String(),
		Name: EventConnectionAttached, Outcome: string(OutcomeOK), OccurredAt: time.Now().UTC(),
	})
	if appendErr == nil {
		t.Fatal("append against a closed database succeeded")
	}
	_, queryErr := store.Query(ctx, Query{})
	for _, err := range []error{appendErr, queryErr} {
		if err == nil {
			continue
		}
		for _, forbidden := range []string{".db", "/Users/", "/var/", "/tmp/", "sqlite"} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("store error carried %q: %v", forbidden, err)
			}
		}
	}
}
