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
}

func (m *Manager) persistRoomCreate(room *Room) error {
	if m.db == nil {
		return nil
	}
	metaJSON, err := marshalJSONText(room.Meta, "{}")
	if err != nil {
		return fmt.Errorf("marshal room meta: %w", err)
	}

	now := nowUTC()
	_, err = m.db.ExecContext(
		context.Background(),
		`INSERT INTO rooms (id, meta, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		room.ID,
		metaJSON,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("insert room row: %w", err)
	}
	return nil
}

func (m *Manager) timeoutStalePendingEnvelopes(ctx context.Context) error {
	now := nowUTC()
	errorPayload, err := marshalJSONText(map[string]any{
		"code":    errorCodeServerRestart,
		"message": "pending envelope timed out after server restart",
	}, "")
	if err != nil {
		return fmt.Errorf("marshal server restart payload: %w", err)
	}

	_, err = m.db.ExecContext(ctx, `
UPDATE envelopes
SET response_kind = ?,
    response_payload = ?,
    status = ?,
    error_code = ?,
    error_message = ?,
    resolved_at = ?
WHERE status = ?`,
		string(envelopes.ResponseKindError),
		errorPayload,
		envelopeStatusTimeout,
		errorCodeServerRestart,
		"pending envelope timed out after server restart",
		now,
		envelopeStatusPending,
	)
	if err != nil {
		return fmt.Errorf("timeout stale pending envelopes: %w", err)
	}
	return nil
}

func (m *Manager) loadActiveRooms(ctx context.Context) ([]hydratedRoomRow, error) {
	rows, err := m.db.QueryContext(ctx, `
SELECT id, meta, created_at
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
			row     hydratedRoomRow
			metaRaw string
			tsRaw   any
		)
		if scanErr := rows.Scan(&row.ID, &metaRaw, &tsRaw); scanErr != nil {
			return nil, fmt.Errorf("scan active room: %w", scanErr)
		}
		if unmarshalErr := json.Unmarshal([]byte(metaRaw), &row.Meta); unmarshalErr != nil {
			return nil, fmt.Errorf("unmarshal room %q meta: %w", row.ID, unmarshalErr)
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

	if _, err := tx.ExecContext(
		context.Background(),
		`UPDATE envelopes
SET response_kind = ?,
    response_payload = ?,
    status = ?,
    error_code = ?,
    error_message = ?,
    resolved_at = ?
WHERE room_id = ? AND envelope_id = ?`,
		nullIfEmpty(responseKind),
		nullStringPtr(responsePayload),
		status,
		nullIfEmpty(errorCode),
		nullIfEmpty(errorMessage),
		now,
		r.ID,
		envelopeID,
	); err != nil {
		return fmt.Errorf("update envelope final state: %w", err)
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
