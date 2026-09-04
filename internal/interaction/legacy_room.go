package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// legacyRoomSurfaceIdempotencyPrefix scopes the surface-open request that
// adopts a v0.12 room. The room id is server-issued and already unique, so one
// scoped key per room is both stable across retries and stable across process
// restarts.
const legacyRoomSurfaceIdempotencyPrefix = "legacy-room:"

// LegacyRoomSurfaceIdempotencyKey is the caller-scoped key under which a room
// is adopted as a durable surface. Exported so the compatibility adapter and
// its tests name the same durable identity.
func LegacyRoomSurfaceIdempotencyKey(roomID string) string {
	return legacyRoomSurfaceIdempotencyPrefix + roomID
}

// TerminalOutcomeAcknowledgement is the caller's explicit, idempotent
// statement that it has taken responsibility for an immutable terminal
// outcome. It is deliberately distinct from retrieval (which only proves the
// caller read the outcome) and from delivery state (which only proves Tangent
// handed the outcome to a destination).
type TerminalOutcomeAcknowledgement struct {
	ID                   string          `json:"acknowledgement_id"`
	InteractionID        string          `json:"interaction_id"`
	ResolutionID         string          `json:"resolution_id,omitempty"`
	RequesterScope       string          `json:"requester_scope"`
	TransportCorrelation json.RawMessage `json:"transport_correlation"`
	AcknowledgedAt       time.Time       `json:"acknowledged_at"`
	Created              bool            `json:"created"`
}

// EnsureLegacyRoomSurfaceParams adopts one v0.12 room as a durable surface.
type EnsureLegacyRoomSurfaceParams struct {
	RoomID      string
	CallerScope string
	OwnerScope  string
	Metadata    json.RawMessage
	ActorRef    string
	Authority   string
}

// EnsureLegacyRoomSurface returns the durable surface that owns a legacy room,
// creating it when the room predates or postdates the durable substrate.
//
// It is deliberately separate from OpenSurface. Migration 0003 backfilled a
// surface (keyed by room id) for every room that existed at upgrade time, but
// wrote no surface_open_request for it; a plain OpenSurface would collide on
// the primary key instead of adopting the imported record. Rooms created after
// the upgrade have no surface at all. Both cases converge here on the same
// stable identity: surface id == room id.
func (s *Store) EnsureLegacyRoomSurface(
	ctx context.Context,
	params EnsureLegacyRoomSurfaceParams,
) (SurfaceRecord, bool, error) {
	if s == nil || s.db == nil {
		return SurfaceRecord{}, false, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	if params.RoomID == "" || params.CallerScope == "" {
		return SurfaceRecord{}, false, fmt.Errorf("%w: room and caller scope are required", ErrInvalidRecord)
	}
	ownerScope := params.OwnerScope
	if ownerScope == "" {
		ownerScope = params.CallerScope
	}
	metadata, err := canonicalJSON(params.Metadata, "{}")
	if err != nil {
		return SurfaceRecord{}, false, fmt.Errorf("%w: surface metadata: %w", ErrInvalidRecord, err)
	}
	request, err := json.Marshal(struct {
		OwnerScope   string          `json:"owner_scope"`
		LegacyRoomID string          `json:"legacy_room_id"`
		Metadata     json.RawMessage `json:"metadata"`
	}{OwnerScope: ownerScope, LegacyRoomID: params.RoomID, Metadata: metadata})
	if err != nil {
		return SurfaceRecord{}, false, fmt.Errorf("marshal legacy room surface request: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SurfaceRecord{}, false, fmt.Errorf("begin ensure legacy room surface: %w", err)
	}
	defer rollback(tx)

	existing, found, err := getSurfaceTx(ctx, tx, params.RoomID)
	if err != nil {
		return SurfaceRecord{}, false, err
	}
	if found {
		if adoptErr := ensureSurfaceOpenRequest(
			ctx, tx, params.CallerScope, params.RoomID, request, existing.CreatedAt,
		); adoptErr != nil {
			return SurfaceRecord{}, false, adoptErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return SurfaceRecord{}, false, fmt.Errorf("commit adopt legacy room surface: %w", commitErr)
		}
		return existing, false, nil
	}

	now := s.now()
	if _, insertErr := tx.ExecContext(ctx, `
INSERT INTO surfaces (
  id, owner_scope, lifecycle_state, metadata, policy,
  next_interaction_sequence, revision, created_at, updated_at, legacy_room_id
) VALUES (?, ?, 'active', ?, '{}', 1, 2, ?, ?, ?)`,
		params.RoomID, ownerScope, string(metadata), now, now, params.RoomID,
	); insertErr != nil {
		return SurfaceRecord{}, false, fmt.Errorf("insert legacy room surface: %w", insertErr)
	}
	if openErr := ensureSurfaceOpenRequest(
		ctx, tx, params.CallerScope, params.RoomID, request, now,
	); openErr != nil {
		return SurfaceRecord{}, false, openErr
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
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.id(), params.RoomID, eventType, nullString(params.ActorRef), nullString(params.Authority),
			fromRevision, revision+1, mustJSON(map[string]any{"legacy_room_id": params.RoomID}), now,
		); eventErr != nil {
			return SurfaceRecord{}, false, fmt.Errorf("insert %s event: %w", eventType, eventErr)
		}
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return SurfaceRecord{}, false, fmt.Errorf("commit create legacy room surface: %w", commitErr)
	}
	record, err := s.GetSurface(ctx, params.RoomID)
	return record, true, err
}

// ensureSurfaceOpenRequest records the scoped open request when it is missing.
// Migration-imported surfaces have none; retries and restarts must converge on
// the same durable handle rather than opening a second surface.
func ensureSurfaceOpenRequest(
	ctx context.Context,
	tx *sql.Tx,
	callerScope string,
	surfaceID string,
	request json.RawMessage,
	createdAt time.Time,
) error {
	var existingSurfaceID, existingRequest string
	err := tx.QueryRowContext(ctx, `
SELECT surface_id, request_snapshot
FROM surface_open_requests
WHERE caller_scope = ? AND idempotency_key = ?`,
		callerScope, LegacyRoomSurfaceIdempotencyKey(surfaceID),
	).Scan(&existingSurfaceID, &existingRequest)
	switch {
	case err == nil:
		if existingSurfaceID != surfaceID {
			return ErrIdempotencyConflict
		}
		return nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return fmt.Errorf("lookup legacy room surface open request: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO surface_open_requests (
  caller_scope, idempotency_key, surface_id, request_snapshot, created_at
) VALUES (?, ?, ?, ?, ?)`,
		callerScope, LegacyRoomSurfaceIdempotencyKey(surfaceID), surfaceID, string(request), createdAt,
	); err != nil {
		return fmt.Errorf("insert legacy room surface open request: %w", err)
	}
	return nil
}

func getSurfaceTx(ctx context.Context, tx *sql.Tx, id string) (SurfaceRecord, bool, error) {
	record, err := getSurface(ctx, tx, id)
	if errors.Is(err, ErrNotFound) {
		return SurfaceRecord{}, false, nil
	}
	return record, err == nil, err
}

// ListOpenLegacyRoomInteractions returns every nonterminal interaction bound
// to a legacy room, oldest first. Restart reconstruction reads this instead of
// process-local channel state so a room's presentation is rebuilt from
// canonical records.
func (s *Store) ListOpenLegacyRoomInteractions(ctx context.Context) ([]InteractionRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	rows, err := s.db.QueryContext(ctx, interactionSelect+`
WHERE i.legacy_room_id IS NOT NULL
  AND i.legacy_envelope_id IS NOT NULL
  AND i.lifecycle_state NOT IN ('resolved', 'canceled', 'expired', 'failed', 'superseded')
ORDER BY i.legacy_room_id, i.surface_sequence`)
	if err != nil {
		return nil, fmt.Errorf("list open legacy room interactions: %w", err)
	}
	defer closeRows(rows)
	var out []InteractionRecord
	for rows.Next() {
		record, scanErr := scanInteractionWithDefinition(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate open legacy room interactions: %w", err)
	}
	return out, nil
}

// FindInteractionByLegacyEnvelope resolves the canonical interaction that owns
// one legacy room/envelope pair.
func (s *Store) FindInteractionByLegacyEnvelope(
	ctx context.Context,
	roomID string,
	envelopeID string,
) (InteractionRecord, bool, error) {
	if roomID == "" || envelopeID == "" {
		return InteractionRecord{}, false, fmt.Errorf("%w: room and envelope are required", ErrInvalidRecord)
	}
	record, err := scanInteractionWithDefinition(s.db.QueryRowContext(ctx, interactionSelect+`
WHERE i.legacy_room_id = ? AND i.legacy_envelope_id = ?`, roomID, envelopeID))
	if errors.Is(err, ErrNotFound) {
		return InteractionRecord{}, false, nil
	}
	return record, err == nil, err
}

// AcknowledgeTerminalOutcomeParams records one caller acknowledgement.
type AcknowledgeTerminalOutcomeParams struct {
	InteractionID        string
	RequesterScope       string
	TransportCorrelation json.RawMessage
}

// AcknowledgeTerminalOutcome records the caller's explicit acknowledgement of
// an immutable terminal outcome exactly once. A repeat returns the original
// fact with Created=false and writes nothing, so retrying is always safe.
//
// When the outcome's caller-pull obligation has already been delivered, the
// same transaction advances it to acknowledged and appends the matching
// delivery event. A queued or failed obligation is left alone: the caller's
// acknowledgement is a fact about the caller, not a claim that Tangent
// completed a delivery it never made.
func (s *Store) AcknowledgeTerminalOutcome(
	ctx context.Context,
	params AcknowledgeTerminalOutcomeParams,
) (TerminalOutcomeAcknowledgement, error) {
	if s == nil || s.db == nil {
		return TerminalOutcomeAcknowledgement{}, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	if params.InteractionID == "" || params.RequesterScope == "" {
		return TerminalOutcomeAcknowledgement{}, fmt.Errorf(
			"%w: interaction and requester scope are required", ErrInvalidRecord)
	}
	correlation, err := canonicalJSON(params.TransportCorrelation, "{}")
	if err != nil {
		return TerminalOutcomeAcknowledgement{}, fmt.Errorf("%w: transport correlation: %w", ErrInvalidRecord, err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TerminalOutcomeAcknowledgement{}, fmt.Errorf("begin acknowledge terminal outcome: %w", err)
	}
	defer rollback(tx)

	interactionRecord, found, err := getInteractionTx(ctx, tx, params.InteractionID)
	if err != nil {
		return TerminalOutcomeAcknowledgement{}, err
	}
	if !found {
		return TerminalOutcomeAcknowledgement{}, ErrNotFound
	}
	if !isTerminalState(interactionRecord.State) {
		return TerminalOutcomeAcknowledgement{}, ErrNotRespondable
	}

	existing, found, err := getTerminalOutcomeAcknowledgement(ctx, tx, params.InteractionID)
	if err != nil {
		return TerminalOutcomeAcknowledgement{}, err
	}
	if found {
		if err := tx.Commit(); err != nil {
			return TerminalOutcomeAcknowledgement{}, fmt.Errorf("commit repeat acknowledgement read: %w", err)
		}
		return existing, nil
	}

	var resolutionID any
	if interactionRecord.State == InteractionStateResolved {
		var id string
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM resolutions WHERE interaction_id = ?`, params.InteractionID,
		).Scan(&id); err != nil {
			return TerminalOutcomeAcknowledgement{}, fmt.Errorf("load resolution for acknowledgement: %w", err)
		}
		resolutionID = id
	}

	now := s.now()
	acknowledgementID := s.id()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO terminal_outcome_acknowledgements (
  interaction_id, acknowledgement_id, resolution_id, requester_scope,
  transport_correlation, acknowledged_at
) VALUES (?, ?, ?, ?, ?, ?)`,
		params.InteractionID, acknowledgementID, resolutionID,
		params.RequesterScope, string(correlation), now,
	); err != nil {
		return TerminalOutcomeAcknowledgement{}, fmt.Errorf("insert terminal outcome acknowledgement: %w", err)
	}
	if err := s.acknowledgeDeliveredObligations(ctx, tx, interactionRecord, now); err != nil {
		return TerminalOutcomeAcknowledgement{}, err
	}
	if err := tx.Commit(); err != nil {
		return TerminalOutcomeAcknowledgement{}, fmt.Errorf("commit terminal outcome acknowledgement: %w", err)
	}
	record := TerminalOutcomeAcknowledgement{
		ID: acknowledgementID, InteractionID: params.InteractionID,
		RequesterScope: params.RequesterScope, TransportCorrelation: correlation,
		AcknowledgedAt: now, Created: true,
	}
	if id, ok := resolutionID.(string); ok {
		record.ResolutionID = id
	}
	return record, nil
}

func getTerminalOutcomeAcknowledgement(
	ctx context.Context,
	tx *sql.Tx,
	interactionID string,
) (TerminalOutcomeAcknowledgement, bool, error) {
	var record TerminalOutcomeAcknowledgement
	var resolutionID sql.NullString
	var correlation string
	var acknowledgedAt any
	err := tx.QueryRowContext(ctx, `
SELECT acknowledgement_id, interaction_id, resolution_id, requester_scope,
       transport_correlation, acknowledged_at
FROM terminal_outcome_acknowledgements
WHERE interaction_id = ?`, interactionID).Scan(
		&record.ID, &record.InteractionID, &resolutionID, &record.RequesterScope,
		&correlation, &acknowledgedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return TerminalOutcomeAcknowledgement{}, false, nil
	}
	if err != nil {
		return TerminalOutcomeAcknowledgement{}, false, fmt.Errorf("load terminal outcome acknowledgement: %w", err)
	}
	record.ResolutionID = resolutionID.String
	record.TransportCorrelation = json.RawMessage(correlation)
	parsed, parseErr := parseTime(acknowledgedAt)
	if parseErr != nil {
		return TerminalOutcomeAcknowledgement{}, false, fmt.Errorf("scan acknowledged_at: %w", parseErr)
	}
	record.AcknowledgedAt = parsed
	return record, true, nil
}

func (s *Store) acknowledgeDeliveredObligations(
	ctx context.Context,
	tx *sql.Tx,
	record InteractionRecord,
	now time.Time,
) error {
	kind := DeliveryKindTerminalNotification
	query := `
SELECT id, revision FROM terminal_notifications
WHERE interaction_id = ? AND lifecycle_state = 'delivered'`
	if record.State == InteractionStateResolved {
		kind = DeliveryKindResolution
		query = `
SELECT d.id, d.revision FROM resolution_deliveries d
JOIN resolutions r ON r.id = d.resolution_id
WHERE r.interaction_id = ? AND d.lifecycle_state = 'delivered'`
	}
	rows, err := tx.QueryContext(ctx, query, record.ID)
	if err != nil {
		return fmt.Errorf("list delivered %s obligations: %w", kind, err)
	}
	type obligation struct {
		id       string
		revision int64
	}
	var obligations []obligation
	for rows.Next() {
		var found obligation
		if err := rows.Scan(&found.id, &found.revision); err != nil {
			closeRows(rows)
			return fmt.Errorf("scan delivered %s obligation: %w", kind, err)
		}
		obligations = append(obligations, found)
	}
	if err := rows.Err(); err != nil {
		closeRows(rows)
		return fmt.Errorf("iterate delivered %s obligations: %w", kind, err)
	}
	closeRows(rows)

	update := `
UPDATE terminal_notifications
SET lifecycle_state = 'acknowledged', revision = revision + 1,
    acknowledged_at = ?, updated_at = ?
WHERE id = ? AND revision = ? AND lifecycle_state = 'delivered'`
	if kind == DeliveryKindResolution {
		update = `
UPDATE resolution_deliveries
SET lifecycle_state = 'acknowledged', revision = revision + 1,
    acknowledged_at = ?, updated_at = ?
WHERE id = ? AND revision = ? AND lifecycle_state = 'delivered'`
	}
	for _, item := range obligations {
		result, err := tx.ExecContext(ctx, update, now, now, item.id, item.revision)
		if err != nil {
			return fmt.Errorf("acknowledge %s delivery %s: %w", kind, item.id, err)
		}
		if !changedOne(result) {
			return ErrRevisionConflict
		}
		if err := s.insertDeliveryEvent(
			ctx, tx, kind, item.id, "delivery.acknowledged",
			item.revision, item.revision+1, nil,
			mustJSON(map[string]any{"interaction_id": record.ID}), now,
		); err != nil {
			return err
		}
	}
	return nil
}

// RecordTerminalOutcomeDeliveryParams names one caller-pull hand-off.
type RecordTerminalOutcomeDeliveryParams struct {
	InteractionID string
	LeaseOwner    string
	Receipt       json.RawMessage
}

// RecordTerminalOutcomeDelivery durably records that Tangent handed an
// immutable terminal outcome to its caller-pull destination. The claim, the
// immutable attempt, and the sealed result commit together, so an outcome can
// never be reported as delivered without the attempt that proves it.
//
// The obligation is left untouched when another worker already holds the
// lease, or when it is already delivered or acknowledged: handing the same
// immutable result back twice is one delivery, not two.
func (s *Store) RecordTerminalOutcomeDelivery(
	ctx context.Context,
	params RecordTerminalOutcomeDeliveryParams,
) (DeliveryClaim, bool, error) {
	if s == nil || s.db == nil {
		return DeliveryClaim{}, false, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	if params.InteractionID == "" || params.LeaseOwner == "" {
		return DeliveryClaim{}, false, fmt.Errorf("%w: interaction and lease owner are required", ErrInvalidRecord)
	}
	receipt, err := canonicalJSON(params.Receipt, "{}")
	if err != nil {
		return DeliveryClaim{}, false, fmt.Errorf("%w: delivery receipt: %w", ErrInvalidRecord, err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeliveryClaim{}, false, fmt.Errorf("begin record terminal outcome delivery: %w", err)
	}
	defer rollback(tx)

	record, found, err := getInteractionTx(ctx, tx, params.InteractionID)
	if err != nil {
		return DeliveryClaim{}, false, err
	}
	if !found {
		return DeliveryClaim{}, false, ErrNotFound
	}
	if !isTerminalState(record.State) {
		return DeliveryClaim{}, false, ErrNotRespondable
	}
	kind := DeliveryKindTerminalNotification
	predicate := `interaction_id = ?
  AND json_extract(destination_binding, '$.kind') = 'caller_pull'`
	if record.State == InteractionStateResolved {
		kind = DeliveryKindResolution
		predicate = `resolution_id = (SELECT id FROM resolutions WHERE interaction_id = ?)
  AND json_extract(d.destination_binding, '$.kind') = 'caller_pull'`
	}
	query, err := deliverySelect(kind, predicate)
	if err != nil {
		return DeliveryClaim{}, false, err
	}
	delivery, err := scanDeliveryClaim(tx.QueryRowContext(ctx, query+` LIMIT 1`, params.InteractionID))
	if errors.Is(err, sql.ErrNoRows) {
		return DeliveryClaim{}, false, ErrNotFound
	}
	if err != nil {
		return DeliveryClaim{}, false, err
	}

	now := s.now()
	eligible, err := deliveryIsClaimable(ctx, tx, delivery, now)
	if err != nil {
		return DeliveryClaim{}, false, err
	}
	if !eligible {
		if commitErr := tx.Commit(); commitErr != nil {
			return DeliveryClaim{}, false, fmt.Errorf("commit delivery no-op: %w", commitErr)
		}
		return delivery.DeliveryClaim, false, nil
	}

	claimed := delivery
	claimed.LeaseOwner = params.LeaseOwner
	claimed.LeaseExpiresAt = now.Add(time.Minute)
	claimed.State = DeliveryStateDelivering
	claimed.Revision++
	if claimErr := updateDeliveryClaim(ctx, tx, claimed.DeliveryClaim, now); claimErr != nil {
		return DeliveryClaim{}, false, claimErr
	}
	attempt, attemptErr := s.openDeliveryAttempt(ctx, tx, claimed, now)
	if attemptErr != nil {
		return DeliveryClaim{}, false, attemptErr
	}
	if startedErr := s.insertDeliveryEvent(
		ctx, tx, claimed.Kind, claimed.ID, "delivery.started",
		claimed.Revision-1, claimed.Revision, &attempt.AttemptNumber,
		mustJSON(map[string]any{"attempt_id": attempt.ID, "transport": "caller_pull"}), now,
	); startedErr != nil {
		return DeliveryClaim{}, false, startedErr
	}
	if sealErr := sealDeliveryAttempt(
		ctx, tx, attempt.ID, DeliveryStateDelivered, receipt, "", "", now,
	); sealErr != nil {
		return DeliveryClaim{}, false, sealErr
	}
	completion := CompleteDeliveryParams{
		Kind: claimed.Kind, ID: claimed.ID, ExpectedRevision: claimed.Revision,
		LeaseOwner: params.LeaseOwner, To: DeliveryStateDelivered, Receipt: receipt,
	}
	if completeErr := updateCompletedDelivery(ctx, tx, completion, receipt, now); completeErr != nil {
		return DeliveryClaim{}, false, completeErr
	}
	if deliveredErr := s.insertDeliveryEvent(
		ctx, tx, claimed.Kind, claimed.ID, "delivery.delivered",
		claimed.Revision, claimed.Revision+1, &attempt.AttemptNumber, "{}", now,
	); deliveredErr != nil {
		return DeliveryClaim{}, false, deliveredErr
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return DeliveryClaim{}, false, fmt.Errorf("commit terminal outcome delivery: %w", commitErr)
	}
	claimed.Revision++
	claimed.State = DeliveryStateDelivered
	claimed.AttemptID = attempt.ID
	claimed.AttemptNumber = attempt.AttemptNumber
	claimed.LeaseOwner = ""
	claimed.LeaseExpiresAt = time.Time{}
	return claimed.DeliveryClaim, true, nil
}

// deliveryIsClaimable reports whether one obligation may start a new attempt
// now. Delivering, delivered, acknowledged, and terminally failed obligations
// are never re-attempted here; a retryable failure waits for its backoff.
func deliveryIsClaimable(
	ctx context.Context,
	tx *sql.Tx,
	delivery interruptedDelivery,
	now time.Time,
) (bool, error) {
	switch delivery.State {
	case DeliveryStateQueued:
		return true, nil
	case DeliveryStateRetryableFailure:
	case DeliveryStateDelivering, DeliveryStateDelivered,
		DeliveryStateAcknowledged, DeliveryStateTerminalFailure:
		return false, nil
	default:
		return false, nil
	}
	table := "terminal_notifications"
	if delivery.Kind == DeliveryKindResolution {
		table = "resolution_deliveries"
	}
	var nextEligibleAt any
	if err := tx.QueryRowContext(ctx,
		"SELECT next_eligible_at FROM "+table+" WHERE id = ?", delivery.ID,
	).Scan(&nextEligibleAt); err != nil {
		return false, fmt.Errorf("load %s delivery backoff: %w", delivery.Kind, err)
	}
	parsed, err := parseNullableTime(nextEligibleAt)
	if err != nil {
		return false, fmt.Errorf("scan %s delivery backoff: %w", delivery.Kind, err)
	}
	return parsed != nil && !parsed.After(now), nil
}
