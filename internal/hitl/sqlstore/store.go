// Package sqlstore implements the go-hitl Store (libs/mesh/core/hitl) over
// Tangent's SQLite database. Tangent stays the queue and UI; this is only the
// persistence boundary the lib's Service needs, and it is held to the lib's own
// hitltest storecontract.
package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

const timeLayout = time.RFC3339Nano

// Store is a hitl.Store backed by the hitl_store_records table.
type Store struct {
	db *sql.DB
}

var _ hitl.Store = (*Store)(nil)

// New returns a Store over a migrated Tangent database.
func New(db *sql.DB) *Store { return &Store{db: db} }

const columns = `item_id, caller_scope, idempotency_key, digest, kind, request, state,
revision, created_at, updated_at, expires_at_ns, outcome`

type scanner interface{ Scan(dest ...any) error }

func scan(row scanner) (hitl.Record, error) {
	var (
		rec              hitl.Record
		request, state   string
		created, updated string
		expires          sql.NullInt64
		outcome          sql.NullString
	)
	if err := row.Scan(&rec.ItemID, &rec.CallerScope, &rec.IdempotencyKey, &rec.Digest, &rec.Kind,
		&request, &state, &rec.Revision, &created, &updated, &expires, &outcome); err != nil {
		return hitl.Record{}, err
	}
	rec.Request, rec.State = []byte(request), hitl.State(state)
	var err error
	if rec.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return hitl.Record{}, fmt.Errorf("sqlstore: created_at: %w", err)
	}
	if rec.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return hitl.Record{}, fmt.Errorf("sqlstore: updated_at: %w", err)
	}
	if expires.Valid {
		t := time.Unix(0, expires.Int64).UTC()
		rec.ExpiresAt = &t
	}
	if outcome.Valid {
		if rec.Outcome, err = hitl.UnmarshalOutcome([]byte(outcome.String)); err != nil {
			return hitl.Record{}, fmt.Errorf("sqlstore: outcome: %w", err)
		}
	}
	return rec, nil
}

func fields(rec hitl.Record) (expires, outcome any, err error) {
	if rec.ExpiresAt != nil {
		expires = rec.ExpiresAt.UnixNano()
	}
	if rec.Outcome != nil {
		b, mErr := rec.Outcome.MarshalJSON()
		if mErr != nil {
			return nil, nil, fmt.Errorf("%w: outcome: %w", hitl.ErrInvalidRecord, mErr)
		}
		outcome = string(b)
	}
	return expires, outcome, nil
}

// Create implements hitl.Store.
func (s *Store) Create(ctx context.Context, rec hitl.Record) (hitl.Record, bool, error) {
	if err := rec.Validate(); err != nil {
		return hitl.Record{}, false, err
	}
	expires, outcome, err := fields(rec)
	if err != nil {
		return hitl.Record{}, false, err
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO hitl_store_records (`+columns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (caller_scope, idempotency_key) DO NOTHING`,
		rec.ItemID, rec.CallerScope, rec.IdempotencyKey, rec.Digest, rec.Kind, string(rec.Request),
		string(rec.State), rec.Revision, rec.CreatedAt.UTC().Format(timeLayout),
		rec.UpdatedAt.UTC().Format(timeLayout), expires, outcome)
	if err != nil {
		// A duplicate item id (the other unique key) is a caller bug, not an
		// idempotent replay.
		return hitl.Record{}, false, fmt.Errorf("%w: %w", hitl.ErrInvalidRecord, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return hitl.Record{}, false, err
	}
	if n == 1 {
		got, getErr := s.Get(ctx, rec.ItemID)
		return got, true, getErr
	}
	existing, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+`
FROM hitl_store_records WHERE caller_scope = ? AND idempotency_key = ?`, rec.CallerScope, rec.IdempotencyKey))
	return existing, false, err
}

// Get implements hitl.Store.
func (s *Store) Get(ctx context.Context, itemID string) (hitl.Record, error) {
	rec, err := scan(s.db.QueryRowContext(ctx,
		`SELECT `+columns+` FROM hitl_store_records WHERE item_id = ?`, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return hitl.Record{}, hitl.ErrNotFound
	}
	return rec, err
}

// Swap implements hitl.Store. The update is conditional on the stored revision
// and on the row being nonterminal, so of any number of concurrent swaps
// exactly one writes.
func (s *Store) Swap(ctx context.Context, itemID string, expected int64, next hitl.Record) error {
	prev, err := s.Get(ctx, itemID)
	if err != nil {
		return err
	}
	if err = hitl.CheckSwap(prev, expected, next); err != nil {
		return err
	}
	_, outcome, err := fields(next)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE hitl_store_records
SET state = ?, revision = ?, updated_at = ?, outcome = ?
WHERE item_id = ? AND revision = ? AND outcome IS NULL`,
		string(next.State), next.Revision, next.UpdatedAt.UTC().Format(timeLayout), outcome, itemID, expected)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	// Lost the race: classify against what is stored now.
	cur, err := s.Get(ctx, itemID)
	if err != nil {
		return err
	}
	if cur.State.IsTerminal() {
		return hitl.ErrTerminalRecord
	}
	return hitl.ErrRevisionMismatch
}

// DueForExpiry implements hitl.Store.
func (s *Store) DueForExpiry(ctx context.Context, now time.Time, limit int) ([]hitl.Record, error) {
	q := `SELECT ` + columns + ` FROM hitl_store_records
WHERE outcome IS NULL AND expires_at_ns IS NOT NULL AND expires_at_ns <= ?
ORDER BY expires_at_ns, item_id`
	args := []any{now.UnixNano()}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var due []hitl.Record
	for rows.Next() {
		rec, err := scan(rows)
		if err != nil {
			return nil, err
		}
		due = append(due, rec)
	}
	return due, rows.Err()
}
