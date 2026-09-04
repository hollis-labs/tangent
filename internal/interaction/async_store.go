package interaction

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// OpenSurfaceParams is the durable, idempotent form of surface creation used
// by asynchronous callers. RequestSnapshot excludes the idempotency key and
// contains every immutable caller-controlled open parameter.
type OpenSurfaceParams struct {
	ID              string
	CallerScope     string
	IdempotencyKey  string
	OwnerScope      string
	Metadata        json.RawMessage
	Policy          json.RawMessage
	RequestSnapshot json.RawMessage
	ActorRef        string
	Authority       string
}

type OpenSurfaceResult struct {
	Surface SurfaceRecord
	Created bool
}

// OpenSurface atomically creates and activates a surface, returning the same
// durable handle for a retry of the same scoped canonical request.
func (s *Store) OpenSurface(ctx context.Context, params OpenSurfaceParams) (OpenSurfaceResult, error) {
	if s == nil || s.db == nil {
		return OpenSurfaceResult{}, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	if params.CallerScope == "" || params.IdempotencyKey == "" || params.OwnerScope == "" {
		return OpenSurfaceResult{}, fmt.Errorf(
			"%w: caller scope, idempotency key, and owner scope are required",
			ErrInvalidRecord,
		)
	}
	request, err := canonicalJSON(params.RequestSnapshot, "")
	if err != nil {
		return OpenSurfaceResult{}, fmt.Errorf("%w: open request snapshot: %w", ErrInvalidRecord, err)
	}
	metadata, err := canonicalJSON(params.Metadata, "{}")
	if err != nil {
		return OpenSurfaceResult{}, fmt.Errorf("%w: metadata: %w", ErrInvalidRecord, err)
	}
	policy, err := canonicalJSON(params.Policy, "{}")
	if err != nil {
		return OpenSurfaceResult{}, fmt.Errorf("%w: policy: %w", ErrInvalidRecord, err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OpenSurfaceResult{}, fmt.Errorf("begin open surface: %w", err)
	}
	defer rollback(tx)

	var existingID, existingRequest string
	err = tx.QueryRowContext(ctx, `
SELECT surface_id, request_snapshot
FROM surface_open_requests
WHERE caller_scope = ? AND idempotency_key = ?`, params.CallerScope, params.IdempotencyKey).Scan(
		&existingID,
		&existingRequest,
	)
	if err == nil {
		if !bytes.Equal(json.RawMessage(existingRequest), request) {
			return OpenSurfaceResult{}, ErrIdempotencyConflict
		}
		surface, getErr := getSurface(ctx, tx, existingID)
		if getErr != nil {
			return OpenSurfaceResult{}, getErr
		}
		return OpenSurfaceResult{Surface: surface, Created: false}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return OpenSurfaceResult{}, fmt.Errorf("lookup surface open request: %w", err)
	}

	id := params.ID
	if id == "" {
		id = s.id()
	}
	now := s.now()
	if _, insertErr := tx.ExecContext(ctx, `
INSERT INTO surfaces (
  id, owner_scope, lifecycle_state, metadata, policy,
  next_interaction_sequence, revision, created_at, updated_at
) VALUES (?, ?, 'active', ?, ?, 1, 2, ?, ?)`,
		id,
		params.OwnerScope,
		string(metadata),
		string(policy),
		now,
		now,
	); insertErr != nil {
		return OpenSurfaceResult{}, fmt.Errorf("insert open surface: %w", insertErr)
	}
	if _, insertErr := tx.ExecContext(ctx, `
INSERT INTO surface_open_requests (
  caller_scope, idempotency_key, surface_id, request_snapshot, created_at
) VALUES (?, ?, ?, ?, ?)`,
		params.CallerScope,
		params.IdempotencyKey,
		id,
		string(request),
		now,
	); insertErr != nil {
		return OpenSurfaceResult{}, fmt.Errorf("insert surface open request: %w", insertErr)
	}
	for revision, eventType := range []string{"surface.created", "surface.activated"} {
		var fromRevision any
		if revision > 0 {
			fromRevision = revision
		}
		if _, eventErr := tx.ExecContext(ctx, `
INSERT INTO surface_events (
  event_id, surface_id, event_type, actor_ref, authority,
  from_revision, to_revision, metadata, recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, '{}', ?)`,
			s.id(),
			id,
			eventType,
			nullString(params.ActorRef),
			nullString(params.Authority),
			fromRevision,
			revision+1,
			now,
		); eventErr != nil {
			return OpenSurfaceResult{}, fmt.Errorf("insert %s event: %w", eventType, eventErr)
		}
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return OpenSurfaceResult{}, fmt.Errorf("commit open surface: %w", commitErr)
	}
	surface, err := s.GetSurface(ctx, id)
	return OpenSurfaceResult{Surface: surface, Created: true}, err
}

func (s *Store) SurfaceWasOpenedByScope(ctx context.Context, surfaceID, callerScope string) (bool, error) {
	if surfaceID == "" || callerScope == "" {
		return false, fmt.Errorf("%w: surface and caller scope are required", ErrInvalidRecord)
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM surface_open_requests
  WHERE surface_id = ? AND caller_scope = ?
)`, surfaceID, callerScope).Scan(&exists); err != nil {
		return false, fmt.Errorf("lookup surface opening scope: %w", err)
	}
	return exists, nil
}

func (s *Store) GetInteractionByIdempotency(
	ctx context.Context,
	callerScope string,
	idempotencyKey string,
) (InteractionRecord, bool, error) {
	if callerScope == "" || idempotencyKey == "" {
		return InteractionRecord{}, false, fmt.Errorf("%w: caller scope and idempotency key are required", ErrInvalidRecord)
	}
	record, err := scanInteractionWithDefinition(s.db.QueryRowContext(ctx, interactionSelect+`
WHERE i.caller_scope = ? AND i.idempotency_key = ?`, callerScope, idempotencyKey))
	if errors.Is(err, ErrNotFound) {
		return InteractionRecord{}, false, nil
	}
	return record, err == nil, err
}

// CountNonterminalInteractionsBefore returns the current FIFO position basis
// for a durable interaction: the number of nonterminal records on the same
// surface with a lower immutable surface sequence. Callers add one when
// projecting a one-based queue position. The sequence remains the ordering
// authority; this count is deliberately only a current projection.
func (s *Store) CountNonterminalInteractionsBefore(
	ctx context.Context,
	surfaceID string,
	surfaceSequence int64,
) (int64, error) {
	if surfaceID == "" || surfaceSequence < 1 {
		return 0, fmt.Errorf("%w: surface and sequence are required", ErrInvalidRecord)
	}
	var count int64
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM interactions
WHERE surface_id = ?
  AND surface_sequence < ?
  AND lifecycle_state NOT IN ('resolved', 'canceled', 'expired', 'failed', 'superseded')`,
		surfaceID, surfaceSequence,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count earlier nonterminal interactions: %w", err)
	}
	return count, nil
}

type CloseSurfaceParams struct {
	SurfaceID          string
	ExpectedRevision   int64
	Reason             string
	PolicyRef          string
	ActorRef           string
	Authority          string
	EventMetadata      json.RawMessage
	NotificationPolicy json.RawMessage
}

type CloseSurfaceResult struct {
	Surface              SurfaceRecord
	DisposedInteractions int
}

type outstandingInteraction struct {
	id          string
	callerScope string
	revision    int64
}

// CloseSurface closes a surface and cancels every outstanding interaction as
// a surface-policy disposition in the same transaction. Terminal interactions
// remain immutable and are not rewritten.
func (s *Store) CloseSurface(ctx context.Context, params CloseSurfaceParams) (CloseSurfaceResult, error) {
	if s == nil || s.db == nil {
		return CloseSurfaceResult{}, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	if params.SurfaceID == "" || params.ExpectedRevision < 1 || params.PolicyRef == "" {
		return CloseSurfaceResult{}, fmt.Errorf(
			"%w: surface, expected revision, and policy reference are required",
			ErrInvalidRecord,
		)
	}
	metadata, err := canonicalJSON(params.EventMetadata, "{}")
	if err != nil {
		return CloseSurfaceResult{}, fmt.Errorf("%w: event metadata: %w", ErrInvalidRecord, err)
	}
	var metadataObject map[string]any
	if decodeErr := json.Unmarshal(metadata, &metadataObject); decodeErr != nil || metadataObject == nil {
		return CloseSurfaceResult{}, fmt.Errorf("%w: event metadata must be an object", ErrInvalidRecord)
	}
	metadataObject["policy_ref"] = params.PolicyRef
	metadata = json.RawMessage(mustJSON(metadataObject))
	notificationPolicy, err := canonicalJSON(params.NotificationPolicy, "{}")
	if err != nil {
		return CloseSurfaceResult{}, fmt.Errorf("%w: notification policy: %w", ErrInvalidRecord, err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CloseSurfaceResult{}, fmt.Errorf("begin close surface: %w", err)
	}
	defer rollback(tx)
	current, err := getSurface(ctx, tx, params.SurfaceID)
	if err != nil {
		return CloseSurfaceResult{}, err
	}
	if current.State == SurfaceStateClosed || current.State == SurfaceStateExpired {
		return CloseSurfaceResult{}, ErrTerminal
	}
	if current.Revision != params.ExpectedRevision {
		return CloseSurfaceResult{}, ErrRevisionConflict
	}

	rows, err := tx.QueryContext(ctx, `
SELECT id, caller_scope, revision
FROM interactions
WHERE surface_id = ?
  AND lifecycle_state NOT IN ('resolved', 'canceled', 'expired', 'failed', 'superseded')
ORDER BY surface_sequence`, params.SurfaceID)
	if err != nil {
		return CloseSurfaceResult{}, fmt.Errorf("list outstanding interactions for close: %w", err)
	}
	var outstanding []outstandingInteraction
	for rows.Next() {
		var interaction outstandingInteraction
		if scanErr := rows.Scan(&interaction.id, &interaction.callerScope, &interaction.revision); scanErr != nil {
			_ = rows.Close()
			return CloseSurfaceResult{}, fmt.Errorf("scan outstanding interaction for close: %w", scanErr)
		}
		outstanding = append(outstanding, interaction)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		return CloseSurfaceResult{}, fmt.Errorf("iterate outstanding interactions for close: %w", rowsErr)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return CloseSurfaceResult{}, fmt.Errorf("close outstanding interaction rows: %w", closeErr)
	}

	now := s.now()
	for _, interaction := range outstanding {
		result, updateErr := tx.ExecContext(ctx, `
UPDATE interactions
SET lifecycle_state = 'canceled', terminal_cause = 'surface_policy',
    terminal_reason = ?, revision = revision + 1, updated_at = ?, terminal_at = ?
WHERE id = ? AND revision = ?`,
			params.Reason,
			now,
			now,
			interaction.id,
			interaction.revision,
		)
		if updateErr != nil {
			return CloseSurfaceResult{}, fmt.Errorf("dispose interaction during close: %w", updateErr)
		}
		if !changedOne(result) {
			return CloseSurfaceResult{}, ErrRevisionConflict
		}
		if _, eventErr := tx.ExecContext(ctx, `
INSERT INTO interaction_events (
  event_id, surface_id, interaction_id, event_type, actor_ref, authority,
  from_revision, to_revision, metadata, recorded_at
) VALUES (?, ?, ?, 'interaction.canceled', ?, ?, ?, ?, ?, ?)`,
			s.id(),
			params.SurfaceID,
			interaction.id,
			nullString(params.ActorRef),
			nullString(params.Authority),
			interaction.revision,
			interaction.revision+1,
			string(metadata),
			now,
		); eventErr != nil {
			return CloseSurfaceResult{}, fmt.Errorf("insert close disposition event: %w", eventErr)
		}
		destination := mustJSON(map[string]any{"kind": "caller_pull", "caller_scope": interaction.callerScope})
		if _, notificationErr := tx.ExecContext(ctx, `
INSERT INTO terminal_notifications (
  id, interaction_id, terminal_state, terminal_cause,
  destination_binding, idempotency_key, policy,
  lifecycle_state, revision, created_at, updated_at
) VALUES (?, ?, 'canceled', 'surface_policy', ?, ?, ?, 'queued', 1, ?, ?)`,
			s.id(),
			interaction.id,
			destination,
			fmt.Sprintf("surface-close:%s:%d", params.SurfaceID, params.ExpectedRevision),
			string(notificationPolicy),
			now,
			now,
		); notificationErr != nil {
			return CloseSurfaceResult{}, fmt.Errorf("insert close terminal notification: %w", notificationErr)
		}
	}

	result, err := tx.ExecContext(ctx, `
UPDATE surfaces
SET lifecycle_state = 'closed', revision = revision + 1,
    updated_at = ?, closed_at = ?, close_reason = ?
WHERE id = ? AND revision = ?`,
		now,
		now,
		nullString(params.Reason),
		params.SurfaceID,
		params.ExpectedRevision,
	)
	if err != nil {
		return CloseSurfaceResult{}, fmt.Errorf("close surface: %w", err)
	}
	if !changedOne(result) {
		return CloseSurfaceResult{}, ErrRevisionConflict
	}
	if _, eventErr := tx.ExecContext(ctx, `
INSERT INTO surface_events (
  event_id, surface_id, event_type, actor_ref, authority,
  from_revision, to_revision, metadata, recorded_at
) VALUES (?, ?, 'surface.closed', ?, ?, ?, ?, ?, ?)`,
		s.id(),
		params.SurfaceID,
		nullString(params.ActorRef),
		nullString(params.Authority),
		params.ExpectedRevision,
		params.ExpectedRevision+1,
		string(metadata),
		now,
	); eventErr != nil {
		return CloseSurfaceResult{}, fmt.Errorf("insert surface close event: %w", eventErr)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return CloseSurfaceResult{}, fmt.Errorf("commit close surface: %w", commitErr)
	}
	surface, err := s.GetSurface(ctx, params.SurfaceID)
	return CloseSurfaceResult{Surface: surface, DisposedInteractions: len(outstanding)}, err
}
