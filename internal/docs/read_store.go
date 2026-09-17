package docs

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ReadStore tracks which doc items the operator has explicitly marked read.
//
// This lives outside the generic interaction substrate on purpose: read/unread
// is orthogonal to interaction state (an unread item can be resolved, a read
// item can still be pending), and the shared interactions table has no reason
// to carry a concept only this package needs. Presence of a row means read;
// absence means unread. See migration 0018_docs_inbox_read_receipts.
const readReceiptsSelect = `SELECT interaction_id, read_at FROM docs_read_receipts WHERE interaction_id IN (`

type ReadStore struct {
	db  *sql.DB
	now func() time.Time
}

func NewReadStore(db *sql.DB) *ReadStore {
	return &ReadStore{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// MarkRead records interactionID as read at the current time. Idempotent —
// marking an already-read item read again leaves the original read_at intact.
func (s *ReadStore) MarkRead(ctx context.Context, interactionID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO docs_read_receipts (interaction_id, read_at) VALUES (?, ?)
		 ON CONFLICT(interaction_id) DO NOTHING`,
		interactionID, s.now(),
	)
	if err != nil {
		return fmt.Errorf("docs read store: mark read: %w", err)
	}
	return nil
}

// ReadAtBulk returns the read_at time for every interactionID that has one.
// An id absent from the returned map is unread.
func (s *ReadStore) ReadAtBulk(ctx context.Context, interactionIDs []string) (map[string]time.Time, error) {
	result := make(map[string]time.Time, len(interactionIDs))
	if len(interactionIDs) == 0 {
		return result, nil
	}
	args := make([]any, len(interactionIDs))
	for i, id := range interactionIDs {
		args[i] = id
	}
	// placeholders is a run of `?,` sized to len(interactionIDs) and nothing
	// else; every real value is bound through args, never interpolated. Same
	// IN-clause shape as interaction.scopePlaceholders.
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(interactionIDs)), ",")
	query := readReceiptsSelect + placeholders + `)` // #nosec G202 -- see comment above
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("docs read store: query read receipts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var readAt time.Time
		if scanErr := rows.Scan(&id, &readAt); scanErr != nil {
			return nil, fmt.Errorf("docs read store: scan read receipt: %w", scanErr)
		}
		result[id] = readAt
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("docs read store: iterate read receipts: %w", err)
	}
	return result, nil
}
