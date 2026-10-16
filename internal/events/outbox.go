// Package events holds Tangent's transactional event outbox: the row type and
// insert helper shared by the producers (internal/hitl) and the relay that
// appends outbox rows to the durable log.
//
// A producer writes one Row per lifecycle transition inside the SAME database
// transaction as the journal row, so an event exists exactly when its
// transition committed. The relay reads pending rows in creation order, appends
// each to the log (deduped on EventID), and calls MarkAppended.
package events

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DefaultStream is the stream approval events belong to.
const DefaultStream = "tangent/approvals"

const timeLayout = time.RFC3339Nano

// Row is one event_outbox row.
type Row struct {
	// EventID is the producer-minted evt_ id and the global dedupe key.
	EventID string
	// Stream defaults to DefaultStream when empty.
	Stream      string
	Kind        string
	SubjectType string
	SubjectID   string
	// EnvelopeJSON is the complete runtimeevents.Event, without the
	// log-assigned recorded_at and cursor.
	EnvelopeJSON []byte
	CreatedAt    time.Time
	// AppendedAt is nil until the relay has appended the row to the log.
	AppendedAt *time.Time
}

// Execer is satisfied by *sql.Tx and *sql.DB. Producers pass their open *sql.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Queryer is satisfied by *sql.Tx and *sql.DB.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Insert writes r into event_outbox using q (normally the producer's open
// transaction). A repeated EventID is a no-op, so a retried producer step is
// safe. It reports whether a row was written.
func Insert(ctx context.Context, q Execer, r Row) (bool, error) {
	if r.EventID == "" || r.Kind == "" || len(r.EnvelopeJSON) == 0 {
		return false, fmt.Errorf("events: outbox row needs event id, kind and envelope")
	}
	stream := r.Stream
	if stream == "" {
		stream = DefaultStream
	}
	created := r.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	res, err := q.ExecContext(ctx, `INSERT INTO event_outbox
(event_id, stream, kind, subject_type, subject_id, envelope_json, created_at)
VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)
ON CONFLICT(event_id) DO NOTHING`,
		r.EventID, stream, r.Kind, r.SubjectType, r.SubjectID, string(r.EnvelopeJSON), created.UTC().Format(timeLayout))
	if err != nil {
		return false, fmt.Errorf("events: insert outbox row: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Pending returns up to limit rows not yet appended, oldest first.
func Pending(ctx context.Context, q Queryer, limit int) ([]Row, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.QueryContext(ctx, `SELECT event_id, stream, kind, COALESCE(subject_type, ''),
COALESCE(subject_id, ''), envelope_json, created_at FROM event_outbox
WHERE appended_at IS NULL ORDER BY rowid LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("events: list pending: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Row
	for rows.Next() {
		var r Row
		var env, created string
		if err = rows.Scan(&r.EventID, &r.Stream, &r.Kind, &r.SubjectType, &r.SubjectID, &env, &created); err != nil {
			return nil, err
		}
		r.EnvelopeJSON = []byte(env)
		if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
			return nil, fmt.Errorf("events: created_at %q: %w", created, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkAppended stamps a row as appended to the log. It is idempotent.
func MarkAppended(ctx context.Context, q Execer, eventID string, at time.Time) error {
	_, err := q.ExecContext(ctx, `UPDATE event_outbox SET appended_at = ? WHERE event_id = ? AND appended_at IS NULL`,
		at.UTC().Format(timeLayout), eventID)
	return err
}
