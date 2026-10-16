package log

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	streamhub "github.com/hollis-labs/libs/ui/ui-go/streamhub"
)

// stubOutbox creates the agreed event_outbox schema. The real table is
// migration 0021 (the emitter's); this stub is only for the relay's tests.
func stubOutbox(t *testing.T, l *Log) {
	t.Helper()
	if _, err := l.db.Exec(`CREATE TABLE IF NOT EXISTS event_outbox (
		event_id TEXT PRIMARY KEY, stream TEXT NOT NULL, kind TEXT NOT NULL,
		subject_type TEXT, subject_id TEXT, envelope_json TEXT NOT NULL,
		created_at TEXT NOT NULL, appended_at TEXT)`); err != nil {
		t.Fatal(err)
	}
}

func enqueue(t *testing.T, l *Log, id string) {
	t.Helper()
	ev := envelope(id, "approval", "item-"+id)
	if _, err := l.db.Exec(`INSERT INTO event_outbox (event_id, stream, kind, subject_type, subject_id, envelope_json, created_at)
		VALUES (?, 'tangent/approvals', ?, 'approval', ?, ?, ?)`,
		id, ev.Name, "item-"+id, string(ev.Data), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func journalCount(t *testing.T, l *Log) int {
	t.Helper()
	var n int
	if err := l.db.QueryRow(`SELECT COUNT(*) FROM stream_log_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRelayDeliversInOrderAndStampsAppendedAt(t *testing.T) {
	ctx := context.Background()
	l, d := newLog(t)
	stubOutbox(t, l)
	for i := range 5 {
		enqueue(t, l, fmt.Sprintf("evt_%d", i))
	}
	r := NewRelay(d, l, time.Hour)
	n, err := r.Drain(ctx)
	if err != nil || n != 5 {
		t.Fatalf("Drain = %d, %v", n, err)
	}
	i := 0
	want := streamhub.Seq(1)
	for rec, err := range l.After(ctx, "tangent/approvals", 0) {
		if err != nil {
			t.Fatal(err)
		}
		if rec.Seq != want || rec.Name != "approval.requested" {
			t.Fatalf("record %d = %+v", i, rec)
		}
		i++
		want++
	}
	var open int
	_ = d.QueryRow(`SELECT COUNT(*) FROM event_outbox WHERE appended_at IS NULL`).Scan(&open)
	if i != 5 || open != 0 {
		t.Fatalf("journal=%d open outbox=%d", i, open)
	}
	if n, _ := r.Drain(ctx); n != 0 {
		t.Fatalf("second drain delivered %d", n)
	}
}

// A crash after Append but before appended_at is stamped replays the row on
// restart; the event_id dedupe keeps the journal at one row.
func TestRelayCrashRetryGivesExactlyOneRow(t *testing.T) {
	ctx := context.Background()
	l, d := newLog(t)
	stubOutbox(t, l)
	enqueue(t, l, "evt_crash")
	enqueue(t, l, "evt_next")

	crash := errors.New("simulated crash")
	r := NewRelay(d, l, time.Hour)
	r.afterAppend = func(id string) error {
		if id == "evt_crash" {
			return crash
		}
		return nil
	}
	if _, err := r.Drain(ctx); !errors.Is(err, crash) {
		t.Fatalf("Drain err = %v, want the simulated crash", err)
	}
	if got := journalCount(t, l); got != 1 {
		t.Fatalf("after crash journal has %d rows, want 1", got)
	}
	var open int
	_ = d.QueryRow(`SELECT COUNT(*) FROM event_outbox WHERE appended_at IS NULL`).Scan(&open)
	if open != 2 {
		t.Fatalf("open outbox rows = %d, want 2 (crash left both unstamped)", open)
	}

	// Restart: a fresh relay over the same db.
	if n, err := NewRelay(d, l, time.Hour).Drain(ctx); err != nil || n != 2 {
		t.Fatalf("recovery Drain = %d, %v", n, err)
	}
	if got := journalCount(t, l); got != 2 {
		t.Fatalf("after recovery journal has %d rows, want 2", got)
	}
	h, _ := l.Head(ctx, "tangent/approvals")
	if h.Latest != 2 {
		t.Fatalf("head = %+v; a replayed append must not burn a seq", h)
	}
}

func TestRelayRunWakesAfterCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l, d := newLog(t)
	stubOutbox(t, l)
	wake, unsub := l.Subscribe()
	defer unsub()

	r := NewRelay(d, l, time.Hour) // the tick must not be what delivers it
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	enqueue(t, l, "evt_wake")
	r.Wake()
	select {
	case <-wake:
	case <-time.After(10 * time.Second):
		t.Fatal("journal subscriber never woke")
	}
	if got := journalCount(t, l); got != 1 {
		t.Fatalf("journal rows = %d", got)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
}
