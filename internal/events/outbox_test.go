package events

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openOutbox(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err = db.Exec(`CREATE TABLE event_outbox (
  event_id TEXT PRIMARY KEY, stream TEXT NOT NULL DEFAULT 'tangent/approvals', kind TEXT NOT NULL,
  subject_type TEXT, subject_id TEXT, envelope_json TEXT NOT NULL, created_at TEXT NOT NULL, appended_at TEXT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE event_outbox_skipped (id INTEGER PRIMARY KEY AUTOINCREMENT, item_id TEXT NOT NULL, kind TEXT NOT NULL, error_class TEXT NOT NULL, created_at TEXT NOT NULL, repaired_at TEXT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE event_outbox_state (id INTEGER PRIMARY KEY CHECK (id = 1), generation INTEGER NOT NULL CHECK (generation > 0), last_sequence INTEGER NOT NULL DEFAULT 0);
		INSERT INTO event_outbox_state (id, generation) VALUES (1, (random() & 9007199254740991) + 1)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestInsertPendingMarkAppended(t *testing.T) {
	ctx := context.Background()
	db := openOutbox(t)
	row := Row{EventID: "evt_" + "0123456789abcdef0123456789abcdef", Kind: "approval.requested", SubjectType: "approval", SubjectID: "i1",
		EnvelopeJSON: []byte(`{"id":"x"}`), CreatedAt: time.Unix(10, 0)}
	if ok, err := Insert(ctx, db, row); err != nil || !ok {
		t.Fatalf("first insert: ok=%v err=%v", ok, err)
	}
	if ok, err := Insert(ctx, db, row); err != nil || ok {
		t.Fatalf("repeat insert must be a no-op: ok=%v err=%v", ok, err)
	}
	pending, err := Pending(ctx, db, 10)
	if err != nil || len(pending) != 1 || pending[0].Stream != DefaultStream || pending[0].SubjectID != "i1" {
		t.Fatalf("pending = %+v err=%v", pending, err)
	}
	if err = MarkAppended(ctx, db, row.EventID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if pending, _ = Pending(ctx, db, 10); len(pending) != 0 {
		t.Fatalf("appended row still pending: %+v", pending)
	}
	if _, err = Insert(ctx, db, Row{}); err == nil {
		t.Fatal("empty row must be rejected")
	}
}
