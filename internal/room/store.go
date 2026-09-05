package room

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"
)

const (
	envelopeStatusPending   = "pending"
	envelopeStatusCancelled = "cancelled"
	envelopeStatusError     = "error"
	envelopeStatusTimeout   = "timeout"

	errorCodeRoomDisconnected = "ROOM_DISCONNECTED"
	errorCodeServerRestart    = "SERVER_RESTART"
)

type hydratedRoomRow struct {
	ID        string
	CreatedAt time.Time
	Meta      map[string]string
	PhaseState
}

type EnvelopeHistory struct {
	EnvelopeID string                    `json:"envelope_id"`
	Type       string                    `json:"type"`
	Envelope   *envelopes.Envelope       `json:"envelope,omitempty"`
	Response   *envelopes.Response       `json:"response,omitempty"`
	Interview  *InterviewQuestionHistory `json:"interview_question,omitempty"`
	Status     string                    `json:"status"`
	ErrorCode  string                    `json:"error_code,omitempty"`
	ErrorMsg   string                    `json:"error_message,omitempty"`
	CreatedAt  string                    `json:"created_at"`
	ResolvedAt string                    `json:"resolved_at,omitempty"`
}

type RoomSummary struct {
	ID                  string `json:"id"`
	Title               string `json:"title,omitempty"`
	CurrentEnvelopeType string `json:"current_envelope_type,omitempty"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
	// ConnectionCount and ResolverLease describe the room's Connection
	// lifecycle. They are reported next to, never instead of, the interaction
	// summary: a tab strip has to be able to show "two tabs open here" without
	// implying anything about the work in the room.
	ConnectionCount int        `json:"connection_count"`
	ResolverLease   *LeaseView `json:"resolver_lease,omitempty"`
}

func (m *Manager) persistRoomCreate(room *Room) error {
	if m.db == nil {
		return nil
	}
	metaJSON, err := marshalJSONText(room.Meta, "{}")
	if err != nil {
		return fmt.Errorf("marshal room meta: %w", err)
	}
	phaseState := room.PhaseState()
	phasesVisitedJSON, err := marshalJSONText(phaseState.PhasesVisited, "[]")
	if err != nil {
		return fmt.Errorf("marshal room phases_visited: %w", err)
	}
	phaseOutputsJSON, err := marshalJSONText(phaseState.PhaseOutputs, "{}")
	if err != nil {
		return fmt.Errorf("marshal room phase_outputs: %w", err)
	}

	now := nowUTC()
	_, err = m.db.ExecContext(
		context.Background(),
		`INSERT INTO rooms (id, meta, current_phase, phases_visited, phase_outputs, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		room.ID,
		metaJSON,
		phaseState.CurrentPhase,
		phasesVisitedJSON,
		phaseOutputsJSON,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("insert room row: %w", err)
	}
	return nil
}

// reconcilePendingEnvelopesAfterRestart applies the compatibility policy only
// to legacy rows that have no canonical durable interaction owner. Canonical
// staged, presented, draft-bearing, or terminal records recover through the
// interaction service and must not be rewritten into transport timeouts.
func (m *Manager) reconcilePendingEnvelopesAfterRestart(ctx context.Context) error {
	now := nowUTC()
	errorPayload, err := marshalJSONText(map[string]any{
		"code":    errorCodeServerRestart,
		"message": "pending envelope timed out after server restart",
	}, "")
	if err != nil {
		return fmt.Errorf("marshal server restart payload: %w", err)
	}

	_, err = m.db.ExecContext(ctx, `
UPDATE envelopes AS e
SET response_kind = ?,
    response_payload = ?,
    status = ?,
    error_code = ?,
    error_message = ?,
    resolved_at = ?
WHERE status = ?
  AND NOT EXISTS (
    SELECT 1
    FROM interactions i
    WHERE i.legacy_room_id = e.room_id
      AND i.legacy_envelope_id = e.envelope_id
  )`,
		string(envelopes.ResponseKindError),
		errorPayload,
		envelopeStatusTimeout,
		errorCodeServerRestart,
		"pending envelope timed out after server restart",
		now,
		envelopeStatusPending,
	)
	if err != nil {
		return fmt.Errorf("reconcile legacy pending envelopes after restart: %w", err)
	}
	return nil
}

func (m *Manager) loadActiveRooms(ctx context.Context) ([]hydratedRoomRow, error) {
	rows, err := m.db.QueryContext(ctx, `
SELECT id, meta, created_at
     , current_phase, phases_visited, phase_outputs
FROM rooms
WHERE closed_at IS NULL
ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("query active rooms: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []hydratedRoomRow
	for rows.Next() {
		var (
			row          hydratedRoomRow
			metaRaw      string
			currentPhase string
			visitedRaw   string
			outputsRaw   string
			tsRaw        any
		)
		if scanErr := rows.Scan(&row.ID, &metaRaw, &tsRaw, &currentPhase, &visitedRaw, &outputsRaw); scanErr != nil {
			return nil, fmt.Errorf("scan active room: %w", scanErr)
		}
		if unmarshalErr := json.Unmarshal([]byte(metaRaw), &row.Meta); unmarshalErr != nil {
			return nil, fmt.Errorf("unmarshal room %q meta: %w", row.ID, unmarshalErr)
		}
		row.PhaseState, err = decodePhaseState(currentPhase, visitedRaw, outputsRaw)
		if err != nil {
			return nil, fmt.Errorf("decode room %q phase state: %w", row.ID, err)
		}
		row.CreatedAt, err = parseSQLiteTimestampValue(tsRaw)
		if err != nil {
			return nil, fmt.Errorf("parse room %q created_at: %w", row.ID, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active rooms: %w", err)
	}
	return out, nil
}

// History returns the persisted envelope history for the given room,
// ordered oldest-first.
func (m *Manager) History(ctx context.Context, roomID string) ([]EnvelopeHistory, error) {
	if m.db == nil {
		return nil, nil
	}
	rows, err := m.db.QueryContext(ctx, `
SELECT envelope_id, type, request_payload, response_kind, response_payload, status, error_code, error_message, created_at, resolved_at
FROM envelopes
WHERE room_id = ?
ORDER BY created_at ASC, envelope_id ASC`,
		roomID,
	)
	if err != nil {
		return nil, fmt.Errorf("query room %q history: %w", roomID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	history := make([]EnvelopeHistory, 0)
	for rows.Next() {
		var (
			item           EnvelopeHistory
			requestPayload string
			responseKind   sql.NullString
			responseRaw    sql.NullString
			errorCode      sql.NullString
			errorMessage   sql.NullString
			createdRaw     any
			resolvedRaw    any
		)
		if err := rows.Scan(
			&item.EnvelopeID,
			&item.Type,
			&requestPayload,
			&responseKind,
			&responseRaw,
			&item.Status,
			&errorCode,
			&errorMessage,
			&createdRaw,
			&resolvedRaw,
		); err != nil {
			return nil, fmt.Errorf("scan room %q history: %w", roomID, err)
		}
		item.ErrorCode = nullableString(errorCode)
		item.ErrorMsg = nullableString(errorMessage)

		createdAt, err := parseSQLiteTimestampValue(createdRaw)
		if err != nil {
			return nil, fmt.Errorf("parse room %q history created_at: %w", roomID, err)
		}
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)

		if item.Envelope, err = envelopeHistoryRequest(item.EnvelopeID, item.Type, requestPayload); err != nil {
			return nil, fmt.Errorf("decode room %q envelope %q request: %w", roomID, item.EnvelopeID, err)
		}
		if item.Response, err = envelopeHistoryResponse(item.EnvelopeID, item.Status, responseKind, responseRaw, item.ErrorCode, item.ErrorMsg, resolvedRaw); err != nil {
			return nil, fmt.Errorf("decode room %q envelope %q response: %w", roomID, item.EnvelopeID, err)
		}
		item.Interview = buildInterviewQuestionHistory(item.Envelope, item.Response)
		if item.Response != nil && item.Response.CompletedAt != "" {
			item.ResolvedAt = item.Response.CompletedAt
		}
		history = append(history, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate room %q history: %w", roomID, err)
	}
	return history, nil
}

// List returns room summaries, optionally limited to active rooms. DB-backed
// production callers get durable created/updated timestamps from SQLite; tests
// without a DB fall back to the live in-memory registry snapshot.
func (m *Manager) List(ctx context.Context, activeOnly bool) ([]RoomSummary, error) {
	if m.db == nil {
		return m.listInMemory(activeOnly), nil
	}
	query := `
SELECT id, meta, created_at, updated_at
FROM rooms`
	if activeOnly {
		query += "\nWHERE closed_at IS NULL"
	}
	query += "\nORDER BY updated_at DESC, created_at DESC"
	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query room list: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	summaries := make([]RoomSummary, 0)
	for rows.Next() {
		var (
			summary RoomSummary
			metaRaw string
			created any
			updated any
		)
		if err := rows.Scan(&summary.ID, &metaRaw, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan room list: %w", err)
		}
		meta := map[string]string{}
		if err := json.Unmarshal([]byte(metaRaw), &meta); err != nil {
			return nil, fmt.Errorf("unmarshal room %q meta: %w", summary.ID, err)
		}
		summary.Title = meta["title"]
		createdAt, err := parseSQLiteTimestampValue(created)
		if err != nil {
			return nil, fmt.Errorf("parse room %q created_at: %w", summary.ID, err)
		}
		updatedAt, err := parseSQLiteTimestampValue(updated)
		if err != nil {
			return nil, fmt.Errorf("parse room %q updated_at: %w", summary.ID, err)
		}
		summary.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
		summary.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
		if rm, ok := m.Get(summary.ID); ok {
			if env := rm.CurrentEnvelope(); env != nil {
				summary.CurrentEnvelopeType = env.Type
			}
			applyConnectionSummary(&summary, rm)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate room list: %w", err)
	}
	return summaries, nil
}

func (m *Manager) listInMemory(activeOnly bool) []RoomSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	summaries := make([]RoomSummary, 0, len(m.rooms))
	for _, rm := range m.rooms {
		if activeOnly && rm.IsClosed() {
			continue
		}
		summary := RoomSummary{
			ID:        rm.ID,
			Title:     rm.MetaCopy()["title"],
			CreatedAt: rm.CreatedAt.UTC().Format(time.RFC3339Nano),
			UpdatedAt: rm.CreatedAt.UTC().Format(time.RFC3339Nano),
		}
		if env := rm.CurrentEnvelope(); env != nil {
			summary.CurrentEnvelopeType = env.Type
		}
		applyConnectionSummary(&summary, rm)
		summaries = append(summaries, summary)
	}
	return summaries
}

// applyConnectionSummary copies the room's connection lifecycle onto a summary.
func applyConnectionSummary(summary *RoomSummary, rm *Room) {
	state := rm.ConnectionState()
	summary.ConnectionCount = len(state.Connections)
	summary.ResolverLease = state.Lease
}

func (r *Room) persistPendingEnvelope(env *envelopes.Envelope) error {
	if r.db == nil {
		return nil
	}
	requestPayload, err := marshalJSONText(env.Data, "{}")
	if err != nil {
		return fmt.Errorf("marshal request payload: %w", err)
	}
	now := nowUTC()

	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin pending envelope tx: %w", err)
	}
	defer rollbackTx(tx)

	if _, err := tx.ExecContext(
		context.Background(),
		`INSERT INTO envelopes (room_id, envelope_id, type, request_payload, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		r.ID,
		env.ID,
		env.Type,
		requestPayload,
		envelopeStatusPending,
		now,
	); err != nil {
		return fmt.Errorf("insert pending envelope row: %w", err)
	}
	if _, err := tx.ExecContext(
		context.Background(),
		`UPDATE rooms SET updated_at = ? WHERE id = ?`,
		now,
		r.ID,
	); err != nil {
		return fmt.Errorf("touch room updated_at: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pending envelope tx: %w", err)
	}
	return nil
}

// persistPendingEnvelopeIfAbsent projects a durable presentation into the
// v0.12 envelopes table without treating an existing row as a conflict. The
// canonical interaction is the authority for "this request exists"; the legacy
// row is a projection that a previous process may already have written.
func (r *Room) persistPendingEnvelopeIfAbsent(env *envelopes.Envelope) error {
	if r.db == nil {
		return nil
	}
	requestPayload, err := marshalJSONText(env.Data, "{}")
	if err != nil {
		return fmt.Errorf("marshal request payload: %w", err)
	}
	now := nowUTC()

	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin pending envelope tx: %w", err)
	}
	defer rollbackTx(tx)

	if _, err := tx.ExecContext(
		context.Background(),
		`INSERT INTO envelopes (room_id, envelope_id, type, request_payload, status, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (room_id, envelope_id) DO NOTHING`,
		r.ID,
		env.ID,
		env.Type,
		requestPayload,
		envelopeStatusPending,
		now,
	); err != nil {
		return fmt.Errorf("project pending envelope row: %w", err)
	}
	if _, err := tx.ExecContext(
		context.Background(),
		`UPDATE rooms SET updated_at = ? WHERE id = ?`,
		now,
		r.ID,
	); err != nil {
		return fmt.Errorf("touch room updated_at: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pending envelope tx: %w", err)
	}
	return nil
}

func (r *Room) persistResolvedEnvelope(envelopeID string, resp *envelopes.Response) error {
	if r.db == nil {
		return nil
	}
	kind, payload, errorCode, errorMessage, err := responseRecord(resp)
	if err != nil {
		return err
	}
	return r.persistEnvelopeFinalState(envelopeID, kind, payload, string(resp.Status), errorCode, errorMessage)
}

func (r *Room) persistCancelledEnvelope(envelopeID string, cancelErr error) error {
	if r.db == nil {
		return nil
	}
	return r.persistEnvelopeFinalState(
		envelopeID,
		string(envelopes.ResponseKindAck),
		nil,
		envelopeStatusCancelled,
		envelopes.ErrorCodeUserCancelled,
		cancelErr.Error(),
	)
}

func (r *Room) persistTerminalEnvelopeError(envelopeID string, err error) error {
	if r.db == nil {
		return nil
	}

	switch {
	case errors.Is(err, envelopes.ErrSchemaValidation):
		payload := mustMarshalJSONText(map[string]any{
			"code":    envelopes.ErrorCodeValidationFailed,
			"message": err.Error(),
		})
		return r.persistEnvelopeFinalState(
			envelopeID,
			string(envelopes.ResponseKindError),
			&payload,
			envelopeStatusError,
			envelopes.ErrorCodeValidationFailed,
			err.Error(),
		)
	case errors.Is(err, context.DeadlineExceeded):
		payload := mustMarshalJSONText(map[string]any{
			"code":    envelopes.ErrorCodeTimeout,
			"message": err.Error(),
		})
		return r.persistEnvelopeFinalState(
			envelopeID,
			string(envelopes.ResponseKindError),
			&payload,
			envelopeStatusTimeout,
			envelopes.ErrorCodeTimeout,
			err.Error(),
		)
	case errors.Is(err, context.Canceled):
		payload := mustMarshalJSONText(map[string]any{
			"code":    envelopes.ErrorCodeUserCancelled,
			"message": err.Error(),
		})
		return r.persistEnvelopeFinalState(
			envelopeID,
			string(envelopes.ResponseKindError),
			&payload,
			envelopeStatusError,
			envelopes.ErrorCodeUserCancelled,
			err.Error(),
		)
	case errors.Is(err, ErrRoomDisconnected), errors.Is(err, ErrRoomClosed):
		payload := mustMarshalJSONText(map[string]any{
			"code":    errorCodeRoomDisconnected,
			"message": err.Error(),
		})
		return r.persistEnvelopeFinalState(
			envelopeID,
			string(envelopes.ResponseKindError),
			&payload,
			envelopeStatusError,
			errorCodeRoomDisconnected,
			err.Error(),
		)
	default:
		payload := mustMarshalJSONText(map[string]any{
			"code":    envelopes.ErrorCodeHostError,
			"message": err.Error(),
		})
		return r.persistEnvelopeFinalState(
			envelopeID,
			string(envelopes.ResponseKindError),
			&payload,
			envelopeStatusError,
			envelopes.ErrorCodeHostError,
			err.Error(),
		)
	}
}

func (r *Room) persistRoomClose(reason string) error {
	if r.db == nil {
		return nil
	}
	now := nowUTC()
	errorPayload, err := marshalJSONText(map[string]any{
		"code":    errorCodeRoomDisconnected,
		"message": reason,
	}, "")
	if err != nil {
		return fmt.Errorf("marshal room close payload: %w", err)
	}

	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin room close tx: %w", err)
	}
	defer rollbackTx(tx)

	if _, err := tx.ExecContext(
		context.Background(),
		`UPDATE rooms SET closed_at = ?, closed_reason = ?, updated_at = ? WHERE id = ?`,
		now,
		reason,
		now,
		r.ID,
	); err != nil {
		return fmt.Errorf("close room row: %w", err)
	}
	if _, err := tx.ExecContext(
		context.Background(),
		`UPDATE envelopes
SET response_kind = ?,
    response_payload = ?,
    status = ?,
    error_code = ?,
    error_message = ?,
    resolved_at = ?
WHERE room_id = ? AND status = ?`,
		string(envelopes.ResponseKindError),
		errorPayload,
		envelopeStatusError,
		errorCodeRoomDisconnected,
		reason,
		now,
		r.ID,
		envelopeStatusPending,
	); err != nil {
		return fmt.Errorf("close pending envelopes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit room close tx: %w", err)
	}
	return nil
}

func (r *Room) persistPhaseState(currentPhase string, phasesVisited []string, phaseOutputs map[string]PhaseOutput) error {
	if r.db == nil {
		return nil
	}

	phasesVisitedJSON, err := marshalJSONText(phasesVisited, "[]")
	if err != nil {
		return fmt.Errorf("marshal phases_visited: %w", err)
	}
	phaseOutputsJSON, err := marshalJSONText(phaseOutputs, "{}")
	if err != nil {
		return fmt.Errorf("marshal phase_outputs: %w", err)
	}

	now := nowUTC()
	_, err = r.db.ExecContext(
		context.Background(),
		`UPDATE rooms
SET current_phase = ?,
    phases_visited = ?,
    phase_outputs = ?,
    updated_at = ?
WHERE id = ?`,
		currentPhase,
		phasesVisitedJSON,
		phaseOutputsJSON,
		now,
		r.ID,
	)
	if err != nil {
		return fmt.Errorf("update room phase state: %w", err)
	}
	return nil
}

func (r *Room) persistEnvelopeFinalState(
	envelopeID string,
	responseKind string,
	responsePayload *string,
	status string,
	errorCode string,
	errorMessage string,
) error {
	if r.db == nil {
		return nil
	}
	now := nowUTC()

	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin envelope final-state tx: %w", err)
	}
	defer rollbackTx(tx)

	result, err := tx.ExecContext(
		context.Background(),
		`UPDATE envelopes
SET response_kind = ?,
    response_payload = ?,
    status = ?,
    error_code = ?,
    error_message = ?,
    resolved_at = ?
WHERE room_id = ? AND envelope_id = ? AND status = 'pending'`,
		nullIfEmpty(responseKind),
		nullStringPtr(responsePayload),
		status,
		nullIfEmpty(errorCode),
		nullIfEmpty(errorMessage),
		now,
		r.ID,
		envelopeID,
	)
	if err != nil {
		return fmt.Errorf("update envelope final state: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read envelope final-state rows affected: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("update envelope final state: expected one pending row, updated %d", updated)
	}
	if _, err := tx.ExecContext(
		context.Background(),
		`UPDATE rooms SET updated_at = ? WHERE id = ?`,
		now,
		r.ID,
	); err != nil {
		return fmt.Errorf("touch room updated_at: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit envelope final-state tx: %w", err)
	}
	return nil
}

func responseRecord(resp *envelopes.Response) (kind string, payload *string, errorCode string, errorMessage string, err error) {
	if resp == nil {
		return "", nil, "", "", fmt.Errorf("nil response")
	}
	kind = string(resp.Kind)
	switch resp.Kind {
	case envelopes.ResponseKindData:
		payload, err = marshalOptionalJSONText(resp.Payload)
	case envelopes.ResponseKindAck, envelopes.ResponseKindAsyncAck:
		payload, err = marshalOptionalJSONText(resp.Handle)
	case envelopes.ResponseKindUI:
		payload, err = marshalOptionalJSONText(map[string]any{"action": resp.Action})
	case envelopes.ResponseKindError:
		payload, err = marshalOptionalJSONText(resp.Error)
		if resp.Error != nil {
			errorCode = resp.Error.Code
			errorMessage = resp.Error.Message
		}
	default:
		payload, err = marshalOptionalJSONText(resp.Payload)
	}
	if err != nil {
		return "", nil, "", "", fmt.Errorf("marshal response payload: %w", err)
	}
	return kind, payload, errorCode, errorMessage, nil
}

func marshalJSONText(v any, fallback string) (string, error) {
	if v == nil {
		return fallback, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalOptionalJSONText(v any) (*string, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := string(raw)
	return &out, nil
}

func mustMarshalJSONText(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func parseSQLiteTimestamp(raw string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts.UTC(), nil
		}
	}
	ts, err := time.ParseInLocation("2006-01-02 15:04:05", raw, time.UTC)
	if err == nil {
		return ts.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unsupported sqlite timestamp %q", raw)
}

func envelopeHistoryRequest(envelopeID, envelopeType, requestPayload string) (*envelopes.Envelope, error) {
	env := &envelopes.Envelope{
		V:    envelopes.ProtocolVersion,
		ID:   envelopeID,
		Type: envelopeType,
	}
	if requestPayload == "" {
		return env, nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(requestPayload), &data); err != nil {
		return nil, err
	}
	env.Data = data
	return env, nil
}

func envelopeHistoryResponse(
	envelopeID string,
	status string,
	responseKind sql.NullString,
	responseRaw sql.NullString,
	errorCode string,
	errorMessage string,
	resolvedRaw any,
) (*envelopes.Response, error) {
	if !responseKind.Valid {
		return nil, nil
	}
	resp := &envelopes.Response{
		V:          envelopes.ProtocolVersion,
		EnvelopeID: envelopeID,
		Kind:       envelopes.ResponseKind(responseKind.String),
		Status:     historyResponseStatus(status),
	}
	if resolvedRaw != nil {
		resolvedAt, err := parseSQLiteTimestampValue(resolvedRaw)
		if err != nil {
			return nil, err
		}
		resp.CompletedAt = resolvedAt.UTC().Format(time.RFC3339Nano)
	}
	if responseRaw.Valid && responseRaw.String != "" {
		switch resp.Kind {
		case envelopes.ResponseKindData:
			var payload any
			if err := json.Unmarshal([]byte(responseRaw.String), &payload); err != nil {
				return nil, err
			}
			resp.Payload = payload
		case envelopes.ResponseKindAck, envelopes.ResponseKindAsyncAck:
			var handle envelopes.Handle
			if err := json.Unmarshal([]byte(responseRaw.String), &handle); err != nil {
				return nil, err
			}
			resp.Handle = &handle
		case envelopes.ResponseKindUI:
			var uiPayload map[string]any
			if err := json.Unmarshal([]byte(responseRaw.String), &uiPayload); err != nil {
				return nil, err
			}
			if action, ok := uiPayload["action"].(string); ok {
				resp.Action = action
			}
		case envelopes.ResponseKindError:
			var responseErr envelopes.ResponseError
			if err := json.Unmarshal([]byte(responseRaw.String), &responseErr); err != nil {
				return nil, err
			}
			resp.Error = &responseErr
		default:
			var payload any
			if err := json.Unmarshal([]byte(responseRaw.String), &payload); err != nil {
				return nil, err
			}
			resp.Payload = payload
		}
	}
	if resp.Kind == envelopes.ResponseKindError && resp.Error == nil && (errorCode != "" || errorMessage != "") {
		resp.Error = &envelopes.ResponseError{
			Code:    errorCode,
			Message: errorMessage,
		}
	}
	return resp, nil
}

func historyResponseStatus(status string) envelopes.ResponseStatus {
	switch status {
	case string(envelopes.ResponseStatusSubmitted):
		return envelopes.ResponseStatusSubmitted
	case string(envelopes.ResponseStatusCancelled):
		return envelopes.ResponseStatusCancelled
	case string(envelopes.ResponseStatusPartial):
		return envelopes.ResponseStatusPartial
	default:
		return envelopes.ResponseStatusError
	}
}

func nullableString(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func parseSQLiteTimestampValue(v any) (time.Time, error) {
	switch value := v.(type) {
	case time.Time:
		return value.UTC(), nil
	case string:
		return parseSQLiteTimestamp(value)
	case []byte:
		return parseSQLiteTimestamp(string(value))
	default:
		return time.Time{}, fmt.Errorf("unsupported sqlite timestamp type %T", v)
	}
}

func rollbackTx(tx *sql.Tx) {
	if tx != nil {
		_ = tx.Rollback()
	}
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullStringPtr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
