package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type scanner interface {
	Scan(dest ...any) error
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const interactionSelect = `
SELECT i.id, i.surface_id, i.caller_scope, i.caller_principal_ref,
       i.caller_authority, i.caller_assurance, i.idempotency_key,
	       i.surface_sequence, i.request_snapshot, i.external_refs, i.policy,
	       i.lifecycle_state, i.presented_projection_revision,
	       i.participant_scope, i.participant_ref,
	       i.participant_authority, i.participant_assurance,
	       i.connection_id, i.presented_at,
       i.terminal_cause, i.terminal_reason, i.terminal_policy_ref,
       i.terminal_error_code, i.replacement_interaction_id,
       i.revision, i.created_at, i.updated_at, i.terminal_at,
       i.legacy_room_id, i.legacy_envelope_id,
       d.interaction_id, d.publisher, d.kind, d.version, d.revision,
       d.digest, d.source, d.schema_identity, d.schema_digest,
       d.host_version, d.assurance, d.bound_at
FROM interactions i
JOIN definition_bindings d ON d.interaction_id = i.id
`

func scanSurface(row scanner) (SurfaceRecord, error) {
	var record SurfaceRecord
	var metadata, policy string
	var closedAt any
	var closeReason, legacyRoomID sql.NullString
	var createdAt, updatedAt any
	err := row.Scan(
		&record.ID, &record.OwnerScope, &record.State, &metadata, &policy,
		&record.NextInteractionSequence, &record.Revision, &createdAt, &updatedAt,
		&closedAt, &closeReason, &legacyRoomID,
	)
	if err != nil {
		return SurfaceRecord{}, scanError("surface", err)
	}
	record.Metadata = json.RawMessage(metadata)
	record.Policy = json.RawMessage(policy)
	record.CloseReason = closeReason.String
	record.LegacyRoomID = legacyRoomID.String
	record.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return SurfaceRecord{}, fmt.Errorf("scan surface created_at: %w", err)
	}
	record.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return SurfaceRecord{}, fmt.Errorf("scan surface updated_at: %w", err)
	}
	record.ClosedAt, err = parseNullableTime(closedAt)
	if err != nil {
		return SurfaceRecord{}, fmt.Errorf("scan surface closed_at: %w", err)
	}
	return record, nil
}

func getSurface(ctx context.Context, q queryer, id string) (SurfaceRecord, error) {
	return scanSurface(q.QueryRowContext(ctx, `
SELECT id, owner_scope, lifecycle_state, metadata, policy,
       next_interaction_sequence, revision, created_at, updated_at,
       closed_at, close_reason, legacy_room_id
FROM surfaces WHERE id = ?`, id))
}

func getInteraction(ctx context.Context, db *sql.DB, id string) (InteractionRecord, error) {
	record, err := scanInteractionWithDefinition(db.QueryRowContext(ctx, interactionSelect+` WHERE i.id = ?`, id))
	if err != nil {
		return InteractionRecord{}, err
	}
	return record, nil
}

func getInteractionTx(ctx context.Context, tx *sql.Tx, id string) (InteractionRecord, bool, error) {
	record, err := scanInteractionWithDefinition(tx.QueryRowContext(ctx, interactionSelect+` WHERE i.id = ?`, id))
	if errors.Is(err, ErrNotFound) {
		return InteractionRecord{}, false, nil
	}
	return record, err == nil, err
}

func getInteractionByIdempotencyTx(
	ctx context.Context,
	tx *sql.Tx,
	callerScope string,
	idempotencyKey string,
) (InteractionRecord, bool, error) {
	record, err := scanInteractionWithDefinition(tx.QueryRowContext(ctx, interactionSelect+`
WHERE i.caller_scope = ? AND i.idempotency_key = ?`, callerScope, idempotencyKey))
	if errors.Is(err, ErrNotFound) {
		return InteractionRecord{}, false, nil
	}
	return record, err == nil, err
}

func scanInteractionWithDefinition(row scanner) (InteractionRecord, error) {
	var record InteractionRecord
	var request, externalRefs, policy string
	var presentedRevision sql.NullInt64
	var callerPrincipalRef, participantScope, participantRef sql.NullString
	var participantAuthority, participantAssurance, connectionID sql.NullString
	var presentedAt, createdAt, updatedAt, terminalAt any
	var terminalCause, terminalReason, terminalPolicyRef, terminalErrorCode, replacementID sql.NullString
	var legacyRoomID, legacyEnvelopeID sql.NullString
	var definitionDigest, schemaIdentity, schemaDigest, hostVersion sql.NullString
	var definitionBoundAt any
	err := row.Scan(
		&record.ID, &record.SurfaceID, &record.CallerScope, &callerPrincipalRef,
		&record.CallerAuthority, &record.CallerAssurance, &record.IdempotencyKey,
		&record.SurfaceSequence, &request, &externalRefs, &policy,
		&record.State, &presentedRevision, &participantScope, &participantRef,
		&participantAuthority, &participantAssurance, &connectionID, &presentedAt,
		&terminalCause, &terminalReason, &terminalPolicyRef, &terminalErrorCode, &replacementID,
		&record.Revision, &createdAt, &updatedAt, &terminalAt,
		&legacyRoomID, &legacyEnvelopeID,
		&record.Definition.InteractionID, &record.Definition.Publisher,
		&record.Definition.Kind, &record.Definition.Version,
		&record.Definition.Revision, &definitionDigest, &record.Definition.Source,
		&schemaIdentity, &schemaDigest, &hostVersion,
		&record.Definition.Assurance, &definitionBoundAt,
	)
	if err != nil {
		return InteractionRecord{}, scanError("interaction", err)
	}
	record.RequestSnapshot = json.RawMessage(request)
	record.CallerPrincipalRef = callerPrincipalRef.String
	record.ExternalRefs = json.RawMessage(externalRefs)
	record.Policy = json.RawMessage(policy)
	if presentedRevision.Valid {
		value := presentedRevision.Int64
		record.PresentedProjectionRevision = &value
	}
	record.ParticipantRef = participantRef.String
	record.ParticipantScope = participantScope.String
	record.ParticipantAuthority = participantAuthority.String
	record.ParticipantAssurance = participantAssurance.String
	record.ConnectionID = connectionID.String
	record.TerminalCause = TerminalCause(terminalCause.String)
	record.TerminalReason = terminalReason.String
	record.TerminalPolicyRef = terminalPolicyRef.String
	record.TerminalErrorCode = terminalErrorCode.String
	record.ReplacementInteractionID = replacementID.String
	record.LegacyRoomID = legacyRoomID.String
	record.LegacyEnvelopeID = legacyEnvelopeID.String
	record.Definition.Digest = definitionDigest.String
	record.Definition.SchemaIdentity = schemaIdentity.String
	record.Definition.SchemaDigest = schemaDigest.String
	record.Definition.HostVersion = hostVersion.String

	var parseErr error
	record.PresentedAt, parseErr = parseNullableTime(presentedAt)
	if parseErr != nil {
		return InteractionRecord{}, fmt.Errorf("scan interaction presented_at: %w", parseErr)
	}
	record.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return InteractionRecord{}, fmt.Errorf("scan interaction created_at: %w", parseErr)
	}
	record.UpdatedAt, parseErr = parseTime(updatedAt)
	if parseErr != nil {
		return InteractionRecord{}, fmt.Errorf("scan interaction updated_at: %w", parseErr)
	}
	record.TerminalAt, parseErr = parseNullableTime(terminalAt)
	if parseErr != nil {
		return InteractionRecord{}, fmt.Errorf("scan interaction terminal_at: %w", parseErr)
	}
	record.Definition.BoundAt, parseErr = parseTime(definitionBoundAt)
	if parseErr != nil {
		return InteractionRecord{}, fmt.Errorf("scan definition bound_at: %w", parseErr)
	}
	return record, nil
}

func scanDraft(row scanner) (DraftRevision, error) {
	var record DraftRevision
	var payload string
	var sensitivity sql.NullString
	var expiresAt, createdAt any
	err := row.Scan(
		&record.InteractionID, &record.Revision, &record.InteractionRevision,
		&record.ParticipantScope, &record.ParticipantRef,
		&record.ParticipantAuthority, &record.ParticipantAssurance,
		&record.DefinitionVersion, &payload,
		&sensitivity, &expiresAt, &createdAt,
	)
	if err != nil {
		return DraftRevision{}, scanError("draft revision", err)
	}
	record.Payload = json.RawMessage(payload)
	record.Sensitivity = sensitivity.String
	var parseErr error
	record.ExpiresAt, parseErr = parseNullableTime(expiresAt)
	if parseErr != nil {
		return DraftRevision{}, fmt.Errorf("scan draft expires_at: %w", parseErr)
	}
	record.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return DraftRevision{}, fmt.Errorf("scan draft created_at: %w", parseErr)
	}
	return record, nil
}

func scanResolution(row scanner) (ResolutionRecord, error) {
	var record ResolutionRecord
	var payload string
	var sourceDraftRevision sql.NullInt64
	var submittedAt, validatedAt, recordedAt any
	err := row.Scan(
		&record.ID, &record.InteractionID, &record.ExpectedInteractionRevision,
		&record.PresentedProjectionRevision, &record.ParticipantScope, &record.ParticipantRef,
		&record.ParticipantAuthority, &record.ParticipantAssurance,
		&record.ResponseKind, &payload, &sourceDraftRevision,
		&record.IntegrityDigest, &submittedAt, &validatedAt, &recordedAt,
	)
	if err != nil {
		return ResolutionRecord{}, scanError("resolution", err)
	}
	record.ResponsePayload = json.RawMessage(payload)
	if sourceDraftRevision.Valid {
		value := sourceDraftRevision.Int64
		record.SourceDraftRevision = &value
	}
	var parseErr error
	record.SubmittedAt, parseErr = parseTime(submittedAt)
	if parseErr != nil {
		return ResolutionRecord{}, fmt.Errorf("scan resolution submitted_at: %w", parseErr)
	}
	record.ValidatedAt, parseErr = parseTime(validatedAt)
	if parseErr != nil {
		return ResolutionRecord{}, fmt.Errorf("scan resolution validated_at: %w", parseErr)
	}
	record.RecordedAt, parseErr = parseTime(recordedAt)
	if parseErr != nil {
		return ResolutionRecord{}, fmt.Errorf("scan resolution recorded_at: %w", parseErr)
	}
	return record, nil
}

func scanResolutionDelivery(row scanner) (ResolutionDeliveryRecord, error) {
	var record ResolutionDeliveryRecord
	var destination, policy string
	var leaseOwner, receipt, terminalReason sql.NullString
	var leaseExpiresAt, nextEligibleAt, createdAt, updatedAt, deliveredAt, acknowledgedAt any
	err := row.Scan(
		&record.ID, &record.ResolutionID, &destination, &record.IdempotencyKey,
		&policy, &record.State, &record.Revision, &leaseOwner, &leaseExpiresAt,
		&receipt, &terminalReason,
		&nextEligibleAt, &createdAt, &updatedAt, &deliveredAt, &acknowledgedAt,
	)
	if err != nil {
		return ResolutionDeliveryRecord{}, scanError("resolution delivery", err)
	}
	record.DestinationBinding = json.RawMessage(destination)
	record.Policy = json.RawMessage(policy)
	record.LeaseOwner = leaseOwner.String
	if receipt.Valid {
		record.Receipt = json.RawMessage(receipt.String)
	}
	record.TerminalReason = terminalReason.String
	var parseErr error
	record.LeaseExpiresAt, parseErr = parseNullableTime(leaseExpiresAt)
	if parseErr != nil {
		return ResolutionDeliveryRecord{}, fmt.Errorf("scan delivery lease_expires_at: %w", parseErr)
	}
	record.NextEligibleAt, parseErr = parseNullableTime(nextEligibleAt)
	if parseErr != nil {
		return ResolutionDeliveryRecord{}, fmt.Errorf("scan delivery next_eligible_at: %w", parseErr)
	}
	record.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return ResolutionDeliveryRecord{}, fmt.Errorf("scan delivery created_at: %w", parseErr)
	}
	record.UpdatedAt, parseErr = parseTime(updatedAt)
	if parseErr != nil {
		return ResolutionDeliveryRecord{}, fmt.Errorf("scan delivery updated_at: %w", parseErr)
	}
	record.DeliveredAt, parseErr = parseNullableTime(deliveredAt)
	if parseErr != nil {
		return ResolutionDeliveryRecord{}, fmt.Errorf("scan delivery delivered_at: %w", parseErr)
	}
	record.AcknowledgedAt, parseErr = parseNullableTime(acknowledgedAt)
	if parseErr != nil {
		return ResolutionDeliveryRecord{}, fmt.Errorf("scan delivery acknowledged_at: %w", parseErr)
	}
	return record, nil
}

func scanTerminalNotification(row scanner) (TerminalNotificationRecord, error) {
	var record TerminalNotificationRecord
	var terminalCause, leaseOwner, receipt, terminalReason sql.NullString
	var destination, policy string
	var leaseExpiresAt, nextEligibleAt, createdAt, updatedAt, deliveredAt, acknowledgedAt any
	err := row.Scan(
		&record.ID, &record.InteractionID, &record.TerminalState, &terminalCause,
		&destination, &record.IdempotencyKey, &policy, &record.State,
		&record.Revision, &leaseOwner, &leaseExpiresAt, &receipt, &terminalReason, &nextEligibleAt,
		&createdAt, &updatedAt, &deliveredAt, &acknowledgedAt,
	)
	if err != nil {
		return TerminalNotificationRecord{}, scanError("terminal notification", err)
	}
	record.TerminalCause = TerminalCause(terminalCause.String)
	record.DestinationBinding = json.RawMessage(destination)
	record.Policy = json.RawMessage(policy)
	record.LeaseOwner = leaseOwner.String
	if receipt.Valid {
		record.Receipt = json.RawMessage(receipt.String)
	}
	record.TerminalReason = terminalReason.String
	var parseErr error
	record.LeaseExpiresAt, parseErr = parseNullableTime(leaseExpiresAt)
	if parseErr != nil {
		return TerminalNotificationRecord{}, fmt.Errorf("scan terminal notification lease_expires_at: %w", parseErr)
	}
	record.NextEligibleAt, parseErr = parseNullableTime(nextEligibleAt)
	if parseErr != nil {
		return TerminalNotificationRecord{}, fmt.Errorf("scan terminal notification next_eligible_at: %w", parseErr)
	}
	record.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return TerminalNotificationRecord{}, fmt.Errorf("scan terminal notification created_at: %w", parseErr)
	}
	record.UpdatedAt, parseErr = parseTime(updatedAt)
	if parseErr != nil {
		return TerminalNotificationRecord{}, fmt.Errorf("scan terminal notification updated_at: %w", parseErr)
	}
	record.DeliveredAt, parseErr = parseNullableTime(deliveredAt)
	if parseErr != nil {
		return TerminalNotificationRecord{}, fmt.Errorf("scan terminal notification delivered_at: %w", parseErr)
	}
	record.AcknowledgedAt, parseErr = parseNullableTime(acknowledgedAt)
	if parseErr != nil {
		return TerminalNotificationRecord{}, fmt.Errorf("scan terminal notification acknowledged_at: %w", parseErr)
	}
	return record, nil
}

func scanTerminalOutcomeRetrieval(row scanner) (TerminalOutcomeRetrievalRecord, error) {
	var record TerminalOutcomeRetrievalRecord
	var resolutionID sql.NullString
	var correlation string
	var retrievedAt any
	if err := row.Scan(
		&record.ID, &record.InteractionID, &resolutionID, &record.RequesterScope,
		&correlation, &retrievedAt,
	); err != nil {
		return TerminalOutcomeRetrievalRecord{}, scanError("terminal outcome retrieval", err)
	}
	record.ResolutionID = resolutionID.String
	record.TransportCorrelation = json.RawMessage(correlation)
	var err error
	record.RetrievedAt, err = parseTime(retrievedAt)
	if err != nil {
		return TerminalOutcomeRetrievalRecord{}, fmt.Errorf("scan terminal retrieval retrieved_at: %w", err)
	}
	return record, nil
}

func scanDeliveryAttempt(row scanner) (DeliveryAttempt, error) {
	var record DeliveryAttempt
	var resolutionDeliveryID, terminalNotificationID sql.NullString
	var receipt, errorCode, errorMessage sql.NullString
	var startedAt, completedAt any
	if err := row.Scan(
		&record.ID, &resolutionDeliveryID, &terminalNotificationID,
		&record.AttemptNumber, &record.Status, &receipt, &errorCode, &errorMessage,
		&startedAt, &completedAt,
	); err != nil {
		return DeliveryAttempt{}, scanError("delivery attempt", err)
	}
	record.ResolutionDeliveryID = resolutionDeliveryID.String
	record.TerminalNotificationID = terminalNotificationID.String
	if receipt.Valid {
		record.Receipt = json.RawMessage(receipt.String)
	}
	record.ErrorCode = errorCode.String
	record.ErrorMessage = errorMessage.String
	var err error
	record.StartedAt, err = parseTime(startedAt)
	if err != nil {
		return DeliveryAttempt{}, fmt.Errorf("scan delivery attempt started_at: %w", err)
	}
	record.CompletedAt, err = parseNullableTime(completedAt)
	if err != nil {
		return DeliveryAttempt{}, fmt.Errorf("scan delivery attempt completed_at: %w", err)
	}
	return record, nil
}

func scanDeliveryEvent(row scanner) (DeliveryEvent, error) {
	var record DeliveryEvent
	var resolutionDeliveryID, terminalNotificationID sql.NullString
	var attemptNumber sql.NullInt64
	var metadata string
	var recordedAt any
	if err := row.Scan(
		&record.ID, &resolutionDeliveryID, &terminalNotificationID,
		&record.Type, &record.FromRevision, &record.ToRevision,
		&attemptNumber, &metadata, &recordedAt,
	); err != nil {
		return DeliveryEvent{}, scanError("delivery event", err)
	}
	record.ResolutionDeliveryID = resolutionDeliveryID.String
	record.TerminalNotificationID = terminalNotificationID.String
	if attemptNumber.Valid {
		record.AttemptNumber = &attemptNumber.Int64
	}
	record.Metadata = json.RawMessage(metadata)
	var err error
	record.RecordedAt, err = parseTime(recordedAt)
	if err != nil {
		return DeliveryEvent{}, fmt.Errorf("scan delivery event recorded_at: %w", err)
	}
	return record, nil
}

func scanError(kind string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("scan %s: %w", kind, err)
}

func parseNullableTime(value any) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := parseTime(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC(), nil
	case string:
		return parseTimeString(typed)
	case []byte:
		return parseTimeString(string(typed))
	default:
		return time.Time{}, fmt.Errorf("unsupported time value %T", value)
	}
}

func parseTimeString(value string) (time.Time, error) {
	formats := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05",
	}
	for _, format := range formats {
		parsed, err := time.Parse(format, value)
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time %q", value)
}
