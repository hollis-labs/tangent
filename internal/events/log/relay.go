package log

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	streamhub "github.com/hollis-labs/libs/ui/ui-go/streamhub"
)

// DefaultRelayTick is how often a Relay polls when nothing wakes it.
const DefaultRelayTick = 2 * time.Second

// RetentionAudit is the R-audit window: 400 days, no tombstone.
const RetentionAudit = 400 * 24 * time.Hour

// Relay moves rows from the event_outbox table into the Log. Producers insert
// an outbox row (carrying the evt_ id) in the same transaction as the state
// change they report; the relay appends it and then stamps appended_at. A
// crash between the two replays the append on restart, and the unique event_id
// keeps it to one journal row (at-least-once delivery, idempotent append).
//
// Expected outbox schema (owned by the emitter, migration 0021):
//
//	event_outbox(event_id PK, stream, kind, subject_type, subject_id,
//	             envelope_json, created_at, appended_at NULL)
//
// Rows are read in insertion order (rowid), so the table must not be WITHOUT
// ROWID.
type Relay struct {
	db   *sql.DB
	log  *Log
	tick time.Duration
	// batch bounds one drain pass.
	batch int
	// afterAppend runs between a successful Append and the appended_at stamp;
	// tests use it to simulate a crash at the worst moment.
	afterAppend func(eventID string) error

	wakeCh chan struct{}
}

// NewRelay returns a Relay draining event_outbox into l. A zero tick uses
// DefaultRelayTick.
func NewRelay(db *sql.DB, l *Log, tick time.Duration) *Relay {
	if tick <= 0 {
		tick = DefaultRelayTick
	}
	return &Relay{db: db, log: l, tick: tick, batch: 256, wakeCh: make(chan struct{}, 1)}
}

// Wake asks the relay to drain now. Call it after the transaction that
// inserted the outbox row commits. It never blocks.
func (r *Relay) Wake() {
	select {
	case r.wakeCh <- struct{}{}:
	default:
	}
}

// Run drains on every Wake and every tick until ctx ends. It drains once on
// start so rows left by a crash are delivered.
func (r *Relay) Run(ctx context.Context) error {
	t := time.NewTicker(r.tick)
	defer t.Stop()
	for {
		if _, err := r.Drain(ctx); err != nil && ctx.Err() == nil {
			// A failed row is retried on the next wake or tick; the error is
			// transient by construction (SQLite busy, log closed).
			if errors.Is(err, streamhub.ErrClosed) {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.wakeCh:
		case <-t.C:
		}
	}
}

type outboxRow struct {
	eventID, stream, kind, subjectType, subjectID string
	envelope                                      []byte
}

// Drain appends every pending outbox row in order and returns how many it
// delivered. It stops at the first failure so order is never skipped.
func (r *Relay) Drain(ctx context.Context) (int, error) {
	delivered := 0
	for {
		rows, err := r.pending(ctx)
		if err != nil {
			return delivered, err
		}
		if len(rows) == 0 {
			return delivered, nil
		}
		for _, row := range rows {
			if err := r.deliver(ctx, row); err != nil {
				return delivered, err
			}
			delivered++
		}
		if len(rows) < r.batch {
			return delivered, nil
		}
	}
}

func (r *Relay) pending(ctx context.Context) ([]outboxRow, error) {
	q, err := r.db.QueryContext(ctx, `SELECT event_id, stream, kind, subject_type, subject_id, envelope_json
		FROM event_outbox WHERE appended_at IS NULL ORDER BY rowid LIMIT ?`, r.batch)
	if err != nil {
		return nil, fmt.Errorf("event relay: read outbox: %w", err)
	}
	defer func() { _ = q.Close() }()
	var out []outboxRow
	for q.Next() {
		var (
			row                          outboxRow
			kind, subjectType, subjectID sql.NullString
			env                          []byte
		)
		if err := q.Scan(&row.eventID, &row.stream, &kind, &subjectType, &subjectID, &env); err != nil {
			return nil, fmt.Errorf("event relay: scan outbox: %w", err)
		}
		row.kind, row.subjectType, row.subjectID, row.envelope = kind.String, subjectType.String, subjectID.String, env
		out = append(out, row)
	}
	return out, q.Err()
}

func (r *Relay) deliver(ctx context.Context, row outboxRow) error {
	if _, err := r.log.AppendMeta(ctx, row.stream, Meta{
		EventID: row.eventID, SubjectType: row.subjectType, SubjectID: row.subjectID,
	}, streamhub.Event{Name: row.kind, Data: row.envelope}); err != nil {
		return fmt.Errorf("event relay: append %s: %w", row.eventID, err)
	}
	if r.afterAppend != nil {
		if err := r.afterAppend(row.eventID); err != nil {
			return err
		}
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE event_outbox SET appended_at = ? WHERE event_id = ? AND appended_at IS NULL`,
		r.log.now().UTC().Format(time.RFC3339Nano), row.eventID); err != nil {
		return fmt.Errorf("event relay: mark appended %s: %w", row.eventID, err)
	}
	return nil
}

// TrimAll applies retention to every stream.
func (l *Log) TrimAll(ctx context.Context, ret streamhub.Retention) error {
	streams, err := l.Streams(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, s := range streams {
		if err := l.Trim(ctx, s, ret); err != nil {
			errs = append(errs, fmt.Errorf("trim %s: %w", s, err))
		}
	}
	return errors.Join(errs...)
}

// RunRetention trims every stream to the R-audit window now and then every
// interval (default hourly) until ctx ends.
func (l *Log) RunRetention(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := l.TrimAll(ctx, streamhub.Retention{MaxAge: RetentionAudit}); err != nil && ctx.Err() == nil &&
			errors.Is(err, streamhub.ErrClosed) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
