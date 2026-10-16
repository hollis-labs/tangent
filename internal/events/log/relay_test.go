package log

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	streamhub "github.com/hollis-labs/libs/ui/ui-go/streamhub"
)

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

// A row the Log rejects forever must not stall the rows behind it: after
// maxFailures consecutive attempts it is quarantined (event_outbox_skipped,
// class relay_rejected) and the good row is delivered.
func TestRelayQuarantinesPoisonRowAndDeliversTheNext(t *testing.T) {
	ctx := context.Background()
	l, d := newLog(t)
	if _, err := d.Exec(`INSERT INTO event_outbox (event_id, stream, kind, subject_type, subject_id, envelope_json, created_at)
		VALUES ('evt_poison', '', 'approval.requested', 'approval', 'item-poison', '{}', ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	enqueue(t, l, "evt_good")

	r := NewRelay(d, l, time.Hour)
	r.maxFailures = 3
	r.minSpan = 0
	for attempt := 1; attempt < 3; attempt++ {
		n, err := r.Drain(ctx)
		if err == nil || n != 0 {
			t.Fatalf("attempt %d: Drain = %d, %v; want a stall on the poison row", attempt, n, err)
		}
		if got := journalCount(t, l); got != 0 {
			t.Fatalf("attempt %d: journal has %d rows, order must not be skipped yet", attempt, got)
		}
	}
	n, err := r.Drain(ctx)
	if err != nil || n != 1 {
		t.Fatalf("third attempt: Drain = %d, %v; want the good row delivered", n, err)
	}
	if got := journalCount(t, l); got != 1 {
		t.Fatalf("journal rows = %d, want 1", got)
	}

	var class, kind, item string
	if err := d.QueryRow(`SELECT error_class, kind, item_id FROM event_outbox_skipped`).Scan(&class, &kind, &item); err != nil {
		t.Fatal(err)
	}
	if class != ClassRelayRejected || kind != "approval.requested" || item != "item-poison" {
		t.Fatalf("skipped row = %q/%q/%q", class, kind, item)
	}
	var stamp string
	if err := d.QueryRow(`SELECT appended_at FROM event_outbox WHERE event_id = 'evt_poison'`).Scan(&stamp); err != nil ||
		!strings.HasPrefix(stamp, QuarantinedPrefix) {
		t.Fatalf("poison appended_at = %q, %v", stamp, err)
	}
	// Quarantined rows leave the pending set for good.
	if n, err := r.Drain(ctx); err != nil || n != 0 {
		t.Fatalf("after quarantine Drain = %d, %v", n, err)
	}
}

// Lock contention is not a rejection: a good row that keeps hitting SQLITE_BUSY
// (far more than maxFailures times, and past the minimum span) is never
// quarantined, and is delivered once the contention clears.
func TestRelayDoesNotQuarantineTransientFailures(t *testing.T) {
	ctx := context.Background()
	l, d := newLog(t)
	enqueue(t, l, "evt_busy")

	now := time.Now()
	r := NewRelay(d, l, time.Hour)
	r.maxFailures = 3
	r.minSpan = time.Minute
	r.clock = func() time.Time { return now }
	busy := true
	r.appendHook = func(string) error {
		if busy {
			return errors.New("database is locked (5) (SQLITE_BUSY)")
		}
		return nil
	}
	for i := range 12 {
		now = now.Add(30 * time.Second) // well past the minimum span
		if n, err := r.Drain(ctx); err == nil || n != 0 {
			t.Fatalf("attempt %d: Drain = %d, %v; want a retryable error", i, n, err)
		}
	}
	var skipped int
	_ = d.QueryRow(`SELECT COUNT(*) FROM event_outbox_skipped`).Scan(&skipped)
	var open int
	_ = d.QueryRow(`SELECT COUNT(*) FROM event_outbox WHERE appended_at IS NULL`).Scan(&open)
	if skipped != 0 || open != 1 {
		t.Fatalf("skipped=%d open=%d; a transient failure must not quarantine", skipped, open)
	}
	busy = false
	if n, err := r.Drain(ctx); err != nil || n != 1 {
		t.Fatalf("after contention Drain = %d, %v", n, err)
	}
}

// Even a real rejection waits for the minimum span: five fast failures are not
// enough.
func TestRelayQuarantineNeedsTheMinimumSpan(t *testing.T) {
	ctx := context.Background()
	l, d := newLog(t)
	if _, err := d.Exec(`INSERT INTO event_outbox (event_id, stream, kind, subject_type, subject_id, envelope_json, created_at)
		VALUES ('evt_poison', '', 'k', 'approval', 'i', '{}', ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	r := NewRelay(d, l, time.Hour)
	r.maxFailures = 3
	r.minSpan = time.Minute
	r.clock = func() time.Time { return now }
	for range 6 {
		if _, err := r.Drain(ctx); err == nil {
			t.Fatal("poison row should keep failing")
		}
	}
	var skipped int
	_ = d.QueryRow(`SELECT COUNT(*) FROM event_outbox_skipped`).Scan(&skipped)
	if skipped != 0 {
		t.Fatalf("quarantined after %d fast failures", 6)
	}
	now = now.Add(2 * time.Minute)
	if _, err := r.Drain(ctx); err != nil {
		t.Fatalf("Drain after the span = %v; want the poison row quarantined", err)
	}
	_ = d.QueryRow(`SELECT COUNT(*) FROM event_outbox_skipped`).Scan(&skipped)
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
}
