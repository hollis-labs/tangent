package log

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	streamhub "github.com/hollis-labs/libs/ui/ui-go/streamhub"
	"github.com/hollis-labs/libs/ui/ui-go/streamhub/hubtest"

	"github.com/hollis-labs/tangent/internal/db"
)

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.RunMigrations(d); err != nil {
		_ = d.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func newLog(t *testing.T, opts ...Option) (*Log, *sql.DB) {
	t.Helper()
	d := openDB(t, filepath.Join(t.TempDir(), "t.db"))
	l, err := Open(context.Background(), d, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, d
}

// The lib's acceptance suite, including reopen over the same file.
func TestConformance(t *testing.T) {
	for _, page := range []int{7, 128} {
		t.Run(fmt.Sprintf("page%d", page), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "conf.db")
			hubtest.Conformance(t, hubtest.Factory{
				New: func(t *testing.T) streamhub.Log {
					d := openDB(t, path)
					// A fresh database per subtest run: clear what a previous
					// subtest left behind.
					for _, q := range []string{
						`DELETE FROM stream_log_events`, `DELETE FROM stream_log_gaps`, `DELETE FROM stream_log_streams`,
					} {
						if _, err := d.Exec(q); err != nil {
							t.Fatal(err)
						}
					}
					l, err := Open(context.Background(), d, WithPageSize(page))
					if err != nil {
						t.Fatal(err)
					}
					return l
				},
				Reopen: func(t *testing.T, l streamhub.Log) streamhub.Log {
					_ = l.Close()
					d := openDB(t, path)
					re, err := Open(context.Background(), d, WithPageSize(page))
					if err != nil {
						t.Fatal(err)
					}
					return re
				},
			})
		})
	}
}

func TestEpochFreshPerDatabaseAndOnReset(t *testing.T) {
	ctx := context.Background()
	a, _ := newLog(t)
	b, _ := newLog(t)
	ea, _ := a.Epoch(ctx)
	eb, _ := b.Epoch(ctx)
	if ea == "" || ea == eb {
		t.Fatalf("epochs a=%q b=%q; want distinct non-empty", ea, eb)
	}
	if _, err := a.Append(ctx, "s", streamhub.Event{Name: "k"}); err != nil {
		t.Fatal(err)
	}
	e2, err := a.Reset(ctx)
	if err != nil || e2 == ea {
		t.Fatalf("Reset epoch = %q (was %q), err %v", e2, ea, err)
	}
	if h, _ := a.Head(ctx, "s"); h != (streamhub.Head{}) {
		t.Fatalf("head after reset = %+v", h)
	}
	// Reopening keeps the minted epoch.
	d := a.db
	re, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := re.Epoch(ctx); got != e2 {
		t.Fatalf("epoch after reopen = %q, want %q", got, e2)
	}
}

func envelope(id, subjType, subjID string) streamhub.Event {
	raw, _ := json.Marshal(map[string]any{
		"id": id, "kind": "approval.requested",
		"subject": map[string]string{"type": subjType, "id": subjID},
	})
	return streamhub.Event{Name: "approval.requested", Data: raw}
}

func TestAppendDedupesOnEventID(t *testing.T) {
	ctx := context.Background()
	l, d := newLog(t)
	first, err := l.Append(ctx, "tangent/approvals", envelope("evt_a", "approval", "item1"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := l.Append(ctx, "tangent/approvals", envelope("evt_a", "approval", "item1"))
	if err != nil {
		t.Fatal(err)
	}
	if again.Seq != first.Seq || !again.At.Equal(first.At) {
		t.Fatalf("retry = %+v, first = %+v", again, first)
	}
	var n int
	var st, sid string
	if err := d.QueryRow(`SELECT COUNT(*), MIN(subject_type), MIN(subject_id) FROM stream_log_events`).Scan(&n, &st, &sid); err != nil {
		t.Fatal(err)
	}
	if n != 1 || st != "approval" || sid != "item1" {
		t.Fatalf("rows=%d subject=%s/%s", n, st, sid)
	}
	if h, _ := l.Head(ctx, "tangent/approvals"); h.Latest != 1 {
		t.Fatalf("a deduped retry must not burn a seq: %+v", h)
	}
	if _, err := l.Append(ctx, "other", envelope("evt_a", "approval", "item1")); err == nil {
		t.Fatal("same event_id in another stream should be rejected")
	}
}

func TestRecordedAtAndSeqAreAssignedByTheLog(t *testing.T) {
	ctx := context.Background()
	fixed := time.Date(2026, 10, 16, 12, 0, 0, 123456789, time.UTC)
	l, _ := newLog(t, WithClock(func() time.Time { return fixed }))
	// A producer claiming its own seq/recorded_at is ignored: the log stores
	// the envelope verbatim but its own columns come from the log.
	ev := streamhub.Event{Name: "k", Data: []byte(`{"id":"evt_x","seq":99,"recorded_at":"1999-01-01T00:00:00Z"}`)}
	rec, err := l.Append(ctx, "s", ev)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Seq != 1 || !rec.At.Equal(fixed) {
		t.Fatalf("record = %+v", rec)
	}
}

func TestGapsForPrunedForeignEpochAndAheadCursors(t *testing.T) {
	ctx := context.Background()
	l, _ := newLog(t)
	for i := range 10 {
		if _, err := l.Append(ctx, "s", envelope(fmt.Sprintf("evt_%02d", i), "approval", "i")); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Trim(ctx, "s", streamhub.Retention{MaxRecords: 4}); err != nil {
		t.Fatal(err)
	}
	epoch, _ := l.Epoch(ctx)

	// pruned
	var perr error
	for _, err := range l.After(ctx, "s", 2) {
		perr = err
		break
	}
	frame, err := l.FrameForError(ctx, "s", perr)
	if err != nil || frame == nil {
		t.Fatalf("pruned frame = %+v, %v (after err %v)", frame, err, perr)
	}
	if *frame != (GapFrame{Reason: GapPruned, EarliestAvailable: 7, HighWater: 10, Epoch: epoch}) {
		t.Fatalf("pruned frame = %+v", *frame)
	}

	// ahead
	for _, err := range l.After(ctx, "s", 50) {
		perr = err
		break
	}
	frame, _ = l.FrameForError(ctx, "s", perr)
	if frame == nil || frame.Reason != GapCursorAhead || frame.HighWater != 10 || frame.EarliestAvailable != 7 {
		t.Fatalf("ahead frame = %+v", frame)
	}
	if !errors.Is(perr, streamhub.ErrCursorAhead) {
		t.Fatalf("ahead err = %v", perr)
	}

	// foreign epoch and unparsable cursors
	for _, c := range []string{"deadbeefdeadbeef:8", "garbage"} {
		after, g, rerr := l.Resolve(ctx, "s", c)
		if rerr != nil || g == nil || after != 0 {
			t.Fatalf("Resolve(%q) = %d, %+v, %v", c, after, g, rerr)
		}
		if *g != (GapFrame{Reason: GapForeignEpoch, EarliestAvailable: 7, HighWater: 10, Epoch: epoch}) {
			t.Fatalf("Resolve(%q) frame = %+v", c, *g)
		}
	}

	// a valid cursor resolves cleanly, empty means first connection
	after, g, err := l.Resolve(ctx, "s", FormatCursor(epoch, 8))
	if err != nil || g != nil || after != 8 {
		t.Fatalf("Resolve(valid) = %d, %+v, %v", after, g, err)
	}
	if after, g, _ = l.Resolve(ctx, "s", ""); g != nil || after != 0 {
		t.Fatalf("Resolve(empty) = %d, %+v", after, g)
	}

	// durable gap record
	recs, err := l.Gaps(ctx, "s")
	if err != nil || len(recs) != 1 || recs[0].FromSeq != 1 || recs[0].ThroughSeq != 6 || recs[0].Dropped != 6 {
		t.Fatalf("gap records = %+v, %v", recs, err)
	}
}

func TestTrimByAgeKeepsRecentAndRecordsRetention(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l, _ := newLog(t, WithClock(func() time.Time { return clock }))
	for i := range 3 {
		if _, err := l.Append(ctx, "s", envelope(fmt.Sprintf("evt_old%d", i), "approval", "i")); err != nil {
			t.Fatal(err)
		}
	}
	clock = clock.Add(399 * 24 * time.Hour)
	if _, err := l.Append(ctx, "s", envelope("evt_recent", "approval", "i")); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * 24 * time.Hour) // old rows are now 401d, recent is 2d
	if err := l.TrimAll(ctx, streamhub.Retention{MaxAge: RetentionAudit}); err != nil {
		t.Fatal(err)
	}
	h, _ := l.Head(ctx, "s")
	if h.PrunedThrough != 3 || h.Latest != 4 {
		t.Fatalf("head = %+v", h)
	}
	n := 0
	for _, err := range l.After(ctx, "s", 3) {
		if err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != 1 {
		t.Fatalf("after trim saw %d records, want 1", n)
	}
}

func TestConcurrentAppendOrderingAndSubscribe(t *testing.T) {
	ctx := context.Background()
	l, _ := newLog(t)
	wake, cancel := l.Subscribe()
	defer cancel()

	const workers, each = 8, 25
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				if _, err := l.Append(ctx, "s", envelope(fmt.Sprintf("evt_%d_%d", w, i), "approval", "i")); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	select {
	case <-wake:
	default:
		t.Fatal("subscriber was not woken")
	}
	var want streamhub.Seq = 1
	for r, err := range l.After(ctx, "s", 0) {
		if err != nil {
			t.Fatal(err)
		}
		if r.Seq != want {
			t.Fatalf("seq %d where %d expected", r.Seq, want)
		}
		want++
	}
	if want != workers*each+1 {
		t.Fatalf("read %d records, want %d", want-1, workers*each)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	c := FormatCursor("abc123", 42)
	e, s, err := ParseCursor(c)
	if err != nil || e != "abc123" || s != 42 {
		t.Fatalf("ParseCursor(%q) = %q, %d, %v", c, e, s, err)
	}
	for _, bad := range []string{"", ":", "abc:", ":5", "abc:x", "abc:-1", "abc:99999999999999999999999"} {
		if _, _, err := ParseCursor(bad); err == nil {
			t.Errorf("ParseCursor(%q) succeeded", bad)
		}
	}
}
