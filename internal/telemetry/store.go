package telemetry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SQLStore is the durable half of criterion 4: audit records that are
// queryable without reading a process log.
//
// It is deliberately a plain table with a plain query surface rather than a
// structured-logging convention. A log line is a string that a retention
// policy, a log rotation, or a restart can take away, and answering "what
// happened to this invocation last Tuesday" from stderr is not answering it.
// A row survives all three and can be selected by trace, by interaction, by
// event, and by time.
type SQLStore struct {
	db *sql.DB
}

// NewSQLStore builds the durable sink over the shared database handle.
func NewSQLStore(db *sql.DB) (*SQLStore, error) {
	if db == nil {
		return nil, errors.New("telemetry: database handle is required")
	}
	return &SQLStore{db: db}, nil
}

// Append writes one observation. It is the only write path.
func (s *SQLStore) Append(ctx context.Context, record Record) error {
	if s == nil || s.db == nil {
		return errors.New("telemetry: no database handle")
	}
	if record.EventID == "" || record.TraceID == "" || record.Name == "" {
		return errors.New("telemetry: incomplete record")
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO telemetry_events (
  event_id, trace_id, span_id, parent_span_id,
  event_name, outcome, code, occurred_at, duration_ms,
  surface_id, interaction_id, room_id, envelope_id, connection_id,
  definition_kind, definition_version, caller_scope, participant_scope,
  attributes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.EventID, record.TraceID, record.SpanID, nullableString(record.ParentSpanID),
		record.Name, record.Outcome, record.Code, record.OccurredAt, nullableInt(record.DurationMS),
		record.SurfaceID, record.InteractionID, record.RoomID, record.EnvelopeID, record.ConnectionID,
		record.DefinitionKind, record.DefinitionVersion, record.CallerScope, record.ParticipantScope,
		encodeAttributes(record.Attributes),
	)
	if err != nil {
		// Wrapped without the driver error for the reason internal/health
		// gives: a SQLite error string carries the database file path, and
		// this error is returned to a recorder that may log it.
		return errors.New("telemetry: append observation failed")
	}
	return nil
}

// Query selects observations.
//
// Every filter is an exact match on an indexed identifier. There is no
// substring search and no free-text predicate, which is the same decision the
// schema makes: a query surface that could match on content would imply
// content is there.
type Query struct {
	TraceID       string
	InteractionID string
	RoomID        string
	EventName     string
	Outcome       string
	Since         time.Time
	// Limit bounds the result. Zero means DefaultQueryLimit; anything above
	// MaxQueryLimit is clamped, because a report is read in a terminal.
	Limit int
}

const (
	// DefaultQueryLimit is what an unbounded query returns.
	DefaultQueryLimit = 100
	// MaxQueryLimit is the ceiling a caller cannot exceed.
	MaxQueryLimit = 500
)

// Query returns matching observations, newest first.
func (s *SQLStore) Query(ctx context.Context, query Query) ([]Record, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("telemetry: no database handle")
	}
	predicates := make([]string, 0, 6)
	arguments := make([]any, 0, 6)
	appendMatch := func(column, value string) {
		if value = strings.TrimSpace(value); value != "" {
			predicates = append(predicates, column+" = ?")
			arguments = append(arguments, value)
		}
	}
	appendMatch("trace_id", query.TraceID)
	appendMatch("interaction_id", query.InteractionID)
	appendMatch("room_id", query.RoomID)
	appendMatch("event_name", query.EventName)
	appendMatch("outcome", query.Outcome)
	if !query.Since.IsZero() {
		predicates = append(predicates, "occurred_at >= ?")
		arguments = append(arguments, query.Since)
	}
	where := ""
	if len(predicates) > 0 {
		where = " WHERE " + strings.Join(predicates, " AND ")
	}
	arguments = append(arguments, boundLimit(query.Limit))

	// The column list is a constant and every value is a placeholder; only the
	// predicate names are interpolated, and they come from this function.
	//nolint:gosec
	rows, err := s.db.QueryContext(ctx, `
SELECT event_id, trace_id, span_id, parent_span_id,
       event_name, outcome, code, occurred_at, duration_ms,
       surface_id, interaction_id, room_id, envelope_id, connection_id,
       definition_kind, definition_version, caller_scope, participant_scope,
       attributes
FROM telemetry_events`+where+`
ORDER BY occurred_at DESC, event_id DESC
LIMIT ?`, arguments...)
	if err != nil {
		return nil, errors.New("telemetry: query observations failed")
	}
	defer func() { _ = rows.Close() }()

	records := make([]Record, 0, 16)
	for rows.Next() {
		record, scanErr := scanRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		records = append(records, record)
	}
	if rows.Err() != nil {
		return nil, errors.New("telemetry: read observations failed")
	}
	return records, nil
}

// Trace returns one invocation's whole story, oldest first.
//
// It is the query criterion 1 is really about: one trace id, and every
// observation any boundary made about that invocation, in order.
func (s *SQLStore) Trace(ctx context.Context, trace TraceID, limit int) ([]Record, error) {
	records, err := s.Query(ctx, Query{TraceID: trace.String(), Limit: limit})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].OccurredAt.Before(records[j].OccurredAt)
	})
	return records, nil
}

// AggregateRow is one event/outcome/code combination and how often it occurred.
type AggregateRow struct {
	Name    string `json:"event_name"`
	Outcome string `json:"outcome"`
	Code    string `json:"code,omitempty"`
	Count   int64  `json:"count"`
	// MeanDurationMS is present only for events that carry a duration.
	MeanDurationMS *float64 `json:"mean_duration_ms,omitempty"`
}

// Aggregate summarizes the durable trail.
//
// It exists because the in-process registry is lost on restart and this is
// not. A snapshot answers "what has this process seen"; an aggregate answers
// "what has this installation seen", and during an incident that follows a
// restart the second is the one worth having.
func (s *SQLStore) Aggregate(ctx context.Context, since time.Time) ([]AggregateRow, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("telemetry: no database handle")
	}
	predicate := ""
	arguments := []any{}
	if !since.IsZero() {
		predicate = " WHERE occurred_at >= ?"
		arguments = append(arguments, since)
	}
	//nolint:gosec
	rows, err := s.db.QueryContext(ctx, `
SELECT event_name, outcome, code, COUNT(*), AVG(duration_ms)
FROM telemetry_events`+predicate+`
GROUP BY event_name, outcome, code
ORDER BY event_name, outcome, code`, arguments...)
	if err != nil {
		return nil, errors.New("telemetry: aggregate observations failed")
	}
	defer func() { _ = rows.Close() }()

	summary := make([]AggregateRow, 0, 16)
	for rows.Next() {
		var row AggregateRow
		var mean sql.NullFloat64
		if scanErr := rows.Scan(&row.Name, &row.Outcome, &row.Code, &row.Count, &mean); scanErr != nil {
			return nil, errors.New("telemetry: read aggregate failed")
		}
		if mean.Valid {
			value := mean.Float64
			row.MeanDurationMS = &value
		}
		summary = append(summary, row)
	}
	if rows.Err() != nil {
		return nil, errors.New("telemetry: read aggregate failed")
	}
	return summary, nil
}

// Prune bounds retention.
//
// Two ceilings, because either alone fails: an age ceiling lets a busy day
// produce an unbounded table, and a row ceiling lets a quiet installation keep
// observations from a year ago that nobody will read. Both are generous — the
// rows are small and carry no content — and pruning is what makes DELETE
// deliberately permitted on this one audit table (see migration 0011).
func (s *SQLStore) Prune(ctx context.Context, olderThan time.Time, keepRows int64) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("telemetry: no database handle")
	}
	var pruned int64
	if !olderThan.IsZero() {
		result, err := s.db.ExecContext(ctx,
			`DELETE FROM telemetry_events WHERE occurred_at < ?`, olderThan)
		if err != nil {
			return pruned, errors.New("telemetry: prune by age failed")
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr == nil {
			pruned += affected
		}
	}
	if keepRows > 0 {
		result, err := s.db.ExecContext(ctx, `
DELETE FROM telemetry_events
WHERE event_id IN (
  SELECT event_id FROM telemetry_events
  ORDER BY occurred_at DESC, event_id DESC
  LIMIT -1 OFFSET ?
)`, keepRows)
		if err != nil {
			return pruned, errors.New("telemetry: prune by count failed")
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr == nil {
			pruned += affected
		}
	}
	return pruned, nil
}

// DefaultRetention is how long an observation is kept, and how many are kept
// at most. Thirty days covers "what happened last time this went wrong" for a
// tool a person uses daily; two hundred thousand rows is a few tens of
// megabytes of identifiers and enumerated states.
const (
	DefaultRetention    = 30 * 24 * time.Hour
	DefaultRetainedRows = 200_000
)

func scanRecord(rows *sql.Rows) (Record, error) {
	var record Record
	var parent, code sql.NullString
	var duration sql.NullInt64
	var attributes string
	err := rows.Scan(
		&record.EventID, &record.TraceID, &record.SpanID, &parent,
		&record.Name, &record.Outcome, &code, &record.OccurredAt, &duration,
		&record.SurfaceID, &record.InteractionID, &record.RoomID, &record.EnvelopeID,
		&record.ConnectionID, &record.DefinitionKind, &record.DefinitionVersion,
		&record.CallerScope, &record.ParticipantScope, &attributes,
	)
	if err != nil {
		return Record{}, fmt.Errorf("telemetry: scan observation failed")
	}
	record.ParentSpanID = parent.String
	record.Code = code.String
	if duration.Valid {
		value := duration.Int64
		record.DurationMS = &value
	}
	if attributes != "" && attributes != "{}" {
		decoded := map[string]any{}
		if json.Unmarshal([]byte(attributes), &decoded) == nil {
			record.Attributes = decoded
		}
	}
	return record, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func boundLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultQueryLimit
	case limit > MaxQueryLimit:
		return MaxQueryLimit
	}
	return limit
}
