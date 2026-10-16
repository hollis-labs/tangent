package log

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	streamhub "github.com/hollis-labs/libs/ui/ui-go/streamhub"
)

// DefaultRelayTick is how often a Relay polls when nothing wakes it.
const DefaultRelayTick = 2 * time.Second

// DefaultMaxRowFailures is how many consecutive failed deliveries of one
// outbox row the relay tolerates before it quarantines the row and moves on.
const DefaultMaxRowFailures = 5

// Bounded failure classes: only these reach the logs and event_outbox_skipped,
// never a raw error string (it can echo payload values).
const (
	classAppendRejected = "append_rejected"
	classMarkFailed     = "mark_failed"
	classReadFailed     = "read_failed"
	// ClassRelayRejected is the event_outbox_skipped.error_class of a
	// quarantined poison row.
	ClassRelayRejected = "relay_rejected"
	// QuarantinedPrefix starts the appended_at value of a quarantined row, so
	// it leaves the pending set but is never mistaken for a delivered one.
	QuarantinedPrefix = "quarantined:"
)

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
	batch  int
	logger *slog.Logger
	// maxFailures consecutive failures on one row quarantine it.
	maxFailures int
	failures    map[string]int
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
	return &Relay{
		db: db, log: l, tick: tick, batch: 256, wakeCh: make(chan struct{}, 1),
		logger: slog.Default(), maxFailures: DefaultMaxRowFailures, failures: make(map[string]int),
	}
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
			if errors.Is(err, streamhub.ErrClosed) {
				return err
			}
			// Row-level failures are logged with their event id where they
			// happen (deliver); a poison row is quarantined after
			// maxFailures consecutive attempts, so this never stalls forever.
			r.logger.Warn("event relay: drain failed; will retry", "class", errClass(err))
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
				if ctx.Err() != nil || errors.Is(err, streamhub.ErrClosed) {
					return delivered, err
				}
				r.failures[row.eventID]++
				n := r.failures[row.eventID]
				r.logger.Warn("event relay: delivery failed", "event_id", row.eventID,
					"class", errClass(err), "consecutive_failures", n)
				if n < r.maxFailures {
					// Stop here so order is never skipped; retried next pass.
					return delivered, err
				}
				if qerr := r.quarantine(ctx, row); qerr != nil {
					return delivered, qerr
				}
				delete(r.failures, row.eventID)
				continue
			}
			delete(r.failures, row.eventID)
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

// errClass maps a delivery error to a bounded class for logs and rows.
func errClass(err error) string {
	var d *deliveryError
	if errors.As(err, &d) {
		return d.class
	}
	return classReadFailed
}

type deliveryError struct {
	class string
	err   error
}

func (e *deliveryError) Error() string { return e.err.Error() }
func (e *deliveryError) Unwrap() error { return e.err }

// quarantine takes a poison row out of the pending set: one transaction records
// it in event_outbox_skipped (class relay_rejected) and stamps appended_at with
// QuarantinedPrefix, so it is repairable but never retried or purged as
// delivered.
func (r *Relay) quarantine(ctx context.Context, row outboxRow) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("event relay: begin quarantine: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := r.log.now().UTC().Format(time.RFC3339Nano)
	item := row.subjectID
	if item == "" {
		item = row.eventID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO event_outbox_skipped (item_id, kind, error_class, created_at)
		VALUES (?, ?, ?, ?)`, item, row.kind, ClassRelayRejected, now); err != nil {
		return fmt.Errorf("event relay: record quarantined %s: %w", row.eventID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE event_outbox SET appended_at = ? WHERE event_id = ? AND appended_at IS NULL`,
		QuarantinedPrefix+now, row.eventID); err != nil {
		return fmt.Errorf("event relay: stamp quarantined %s: %w", row.eventID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("event relay: commit quarantine: %w", err)
	}
	r.logger.Error("event relay: row quarantined", "event_id", row.eventID, "class", ClassRelayRejected)
	return nil
}

func (r *Relay) deliver(ctx context.Context, row outboxRow) error {
	if _, err := r.log.AppendMeta(ctx, row.stream, Meta{
		EventID: row.eventID, SubjectType: row.subjectType, SubjectID: row.subjectID,
	}, streamhub.Event{Name: row.kind, Data: row.envelope}); err != nil {
		return &deliveryError{class: classAppendRejected, err: fmt.Errorf("event relay: append %s: %w", row.eventID, err)}
	}
	if r.afterAppend != nil {
		if err := r.afterAppend(row.eventID); err != nil {
			return err
		}
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE event_outbox SET appended_at = ? WHERE event_id = ? AND appended_at IS NULL`,
		r.log.now().UTC().Format(time.RFC3339Nano), row.eventID); err != nil {
		return &deliveryError{class: classMarkFailed, err: fmt.Errorf("event relay: mark appended %s: %w", row.eventID, err)}
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
