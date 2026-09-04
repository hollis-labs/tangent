package interaction

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrNoDeliveryAvailable = errors.New("interaction store: no delivery is currently eligible")

const (
	DeliveryKindResolution           DeliveryKind = "resolution"
	DeliveryKindTerminalNotification DeliveryKind = "terminal_notification"

	recoveryErrorCode = "process_restart_outcome_unknown"
)

// DeliveryKind identifies which durable terminal-outcome table owns an
// obligation. Both kinds share one claim/complete lifecycle.
type DeliveryKind string

// RecoveryReport describes durable state found at startup. Open interaction
// lifecycles are observed, not rewritten; only abandoned delivery claims are
// reconciled.
type RecoveryReport struct {
	Staged                        int `json:"staged"`
	Presented                     int `json:"presented"`
	DraftBearing                  int `json:"draft_bearing"`
	ResolvedUndelivered           int `json:"resolved_undelivered"`
	DeliveredUnacknowledged       int `json:"delivered_unacknowledged"`
	PreservedTerminalInteractions int `json:"preserved_terminal_interactions"`
	TerminalFailedDeliveries      int `json:"terminal_failed_deliveries"`
	InterruptedDeliveries         int `json:"interrupted_deliveries"`
	ImmediatelyRetryable          int `json:"immediately_retryable"`
	ManualReconciliationRequired  int `json:"manual_reconciliation_required"`
}

// DeliveryClaim is the complete immutable input an adapter needs. The adapter
// must pass IdempotencyKey to its destination; a claim is never evidence that
// the destination observed the effect.
type DeliveryClaim struct {
	Kind               DeliveryKind    `json:"kind"`
	ID                 string          `json:"delivery_id"`
	InteractionID      string          `json:"interaction_id"`
	ResolutionID       string          `json:"resolution_id,omitempty"`
	DestinationBinding json.RawMessage `json:"destination_binding"`
	IdempotencyKey     string          `json:"idempotency_key"`
	Policy             json.RawMessage `json:"policy"`
	State              DeliveryState   `json:"state"`
	Revision           int64           `json:"revision"`
	AttemptID          string          `json:"attempt_id"`
	AttemptNumber      int64           `json:"attempt_number"`
	LeaseOwner         string          `json:"lease_owner"`
	LeaseExpiresAt     time.Time       `json:"lease_expires_at"`
}

type ClaimDeliveryParams struct {
	LeaseOwner    string
	LeaseDuration time.Duration
}

type CompleteDeliveryParams struct {
	Kind             DeliveryKind
	ID               string
	ExpectedRevision int64
	LeaseOwner       string
	To               DeliveryState
	Receipt          json.RawMessage
	ErrorCode        string
	ErrorMessage     string
	NextEligibleAt   *time.Time
}

type ClaimDeliveryInput struct {
	Worker        ActorBinding  `json:"worker"`
	LeaseDuration time.Duration `json:"lease_duration"`
}

type CompleteDeliveryInput struct {
	Worker           ActorBinding    `json:"worker"`
	Kind             DeliveryKind    `json:"kind"`
	ID               string          `json:"delivery_id"`
	ExpectedRevision int64           `json:"expected_revision"`
	To               DeliveryState   `json:"to"`
	Receipt          json.RawMessage `json:"receipt,omitempty"`
	ErrorCode        string          `json:"error_code,omitempty"`
	ErrorMessage     string          `json:"error_message,omitempty"`
	NextEligibleAt   *time.Time      `json:"next_eligible_at,omitempty"`
}

func (s *Service) RecoverAfterRestart(ctx context.Context) (RecoveryReport, error) {
	return s.store.RecoverAfterRestart(ctx)
}

func (s *Service) ClaimNextDelivery(ctx context.Context, input ClaimDeliveryInput) (DeliveryClaim, error) {
	if err := validateActor(input.Worker); err != nil {
		return DeliveryClaim{}, err
	}
	if !s.delivery.AuthorizeDeliveryWorker(input.Worker) {
		return DeliveryClaim{}, ErrUnauthorized
	}
	return s.store.ClaimNextDelivery(ctx, ClaimDeliveryParams{
		LeaseOwner: deliveryWorkerLeaseOwner(input.Worker), LeaseDuration: input.LeaseDuration,
	})
}

func (s *Service) CompleteDelivery(ctx context.Context, input CompleteDeliveryInput) (DeliveryClaim, error) {
	if err := validateActor(input.Worker); err != nil {
		return DeliveryClaim{}, err
	}
	if !s.delivery.AuthorizeDeliveryWorker(input.Worker) {
		return DeliveryClaim{}, ErrUnauthorized
	}
	return s.store.CompleteDelivery(ctx, CompleteDeliveryParams{
		Kind: input.Kind, ID: input.ID, ExpectedRevision: input.ExpectedRevision,
		LeaseOwner: deliveryWorkerLeaseOwner(input.Worker), To: input.To,
		Receipt: input.Receipt, ErrorCode: input.ErrorCode, ErrorMessage: input.ErrorMessage,
		NextEligibleAt: input.NextEligibleAt,
	})
}

// RecoverAfterRestart preserves every interaction and terminal outcome, then
// seals delivery claims abandoned by the old process as outcome-unknown
// attempts. Only caller-pull or explicitly idempotent policies become
// immediately claimable. Unknown external policies stay paused for manual
// reconciliation so startup cannot blindly repeat an effect.
func (s *Store) RecoverAfterRestart(ctx context.Context) (RecoveryReport, error) {
	if s == nil || s.db == nil {
		return RecoveryReport{}, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryReport{}, fmt.Errorf("begin restart recovery: %w", err)
	}
	defer rollback(tx)

	report, err := loadRecoveryReport(ctx, tx)
	if err != nil {
		return RecoveryReport{}, err
	}
	now := s.now()
	for _, kind := range []DeliveryKind{DeliveryKindResolution, DeliveryKindTerminalNotification} {
		interrupted, listErr := listInterruptedDeliveries(ctx, tx, kind)
		if listErr != nil {
			return RecoveryReport{}, listErr
		}
		for _, delivery := range interrupted {
			nextEligibleAt := any(nil)
			if restartPolicyIsIdempotentlyRetryable(delivery.DestinationBinding, delivery.Policy) {
				nextEligibleAt = now
				report.ImmediatelyRetryable++
			} else {
				report.ManualReconciliationRequired++
			}
			attempt, openedForCompatibility, attemptErr := s.ensureOpenDeliveryAttempt(
				ctx, tx, delivery, delivery.StartedAt,
			)
			if attemptErr != nil {
				return RecoveryReport{}, attemptErr
			}
			if openedForCompatibility {
				if err := s.insertDeliveryEvent(
					ctx, tx, delivery.Kind, delivery.ID, "delivery.started",
					delivery.Revision-1, delivery.Revision, &attempt.AttemptNumber,
					mustJSON(map[string]any{"attempt_id": attempt.ID, "source": "restart-compatibility"}),
					delivery.StartedAt,
				); err != nil {
					return RecoveryReport{}, err
				}
			}
			if err := sealDeliveryAttempt(
				ctx, tx, attempt.ID, DeliveryStateRetryableFailure, nil,
				recoveryErrorCode, "delivery outcome is unknown because the owning process restarted", now,
			); err != nil {
				return RecoveryReport{}, err
			}
			if err := updateRecoveredDelivery(ctx, tx, delivery, now, nextEligibleAt); err != nil {
				return RecoveryReport{}, err
			}
			if err := s.insertDeliveryEvent(
				ctx, tx, delivery.Kind, delivery.ID, "delivery.retryable_failed",
				delivery.Revision, delivery.Revision+1, &attempt.AttemptNumber,
				mustJSON(map[string]any{"error_code": recoveryErrorCode, "outcome_unknown": true}), now,
			); err != nil {
				return RecoveryReport{}, err
			}
			report.InterruptedDeliveries++
		}
	}
	if err := tx.Commit(); err != nil {
		return RecoveryReport{}, fmt.Errorf("commit restart recovery: %w", err)
	}
	return report, nil
}

// ClaimNextDelivery obtains one durable lease. It performs no destination I/O;
// callers receive the original idempotency identity before any external effect.
func (s *Store) ClaimNextDelivery(ctx context.Context, params ClaimDeliveryParams) (DeliveryClaim, error) {
	if s == nil || s.db == nil {
		return DeliveryClaim{}, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	if params.LeaseOwner == "" || params.LeaseDuration <= 0 {
		return DeliveryClaim{}, fmt.Errorf("%w: lease owner and positive duration are required", ErrInvalidRecord)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeliveryClaim{}, fmt.Errorf("begin delivery claim: %w", err)
	}
	defer rollback(tx)
	now := s.now()
	claim, found, err := nextEligibleDelivery(ctx, tx, now)
	if err != nil {
		return DeliveryClaim{}, err
	}
	if !found {
		return DeliveryClaim{}, ErrNoDeliveryAvailable
	}
	claim.LeaseOwner = params.LeaseOwner
	claim.LeaseExpiresAt = now.Add(params.LeaseDuration)
	claim.State = DeliveryStateDelivering
	claim.Revision++
	if updateErr := updateDeliveryClaim(ctx, tx, claim.DeliveryClaim, now); updateErr != nil {
		return DeliveryClaim{}, updateErr
	}
	attempt, err := s.openDeliveryAttempt(ctx, tx, claim, now)
	if err != nil {
		return DeliveryClaim{}, err
	}
	claim.AttemptID = attempt.ID
	claim.AttemptNumber = attempt.AttemptNumber
	if err := s.insertDeliveryEvent(
		ctx, tx, claim.Kind, claim.ID, "delivery.started",
		claim.Revision-1, claim.Revision, &attempt.AttemptNumber,
		mustJSON(map[string]any{"attempt_id": attempt.ID, "lease_expires_at": claim.LeaseExpiresAt}), now,
	); err != nil {
		return DeliveryClaim{}, err
	}
	if err := tx.Commit(); err != nil {
		return DeliveryClaim{}, fmt.Errorf("commit delivery claim: %w", err)
	}
	return claim.DeliveryClaim, nil
}

// CompleteDelivery seals the strongest outcome the adapter observed. The
// immutable attempt row and delivery state transition commit together.
func (s *Store) CompleteDelivery(ctx context.Context, params CompleteDeliveryParams) (DeliveryClaim, error) {
	if s == nil || s.db == nil {
		return DeliveryClaim{}, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	if params.ID == "" || params.LeaseOwner == "" || params.ExpectedRevision < 1 {
		return DeliveryClaim{}, fmt.Errorf("%w: delivery, revision, and lease owner are required", ErrInvalidRecord)
	}
	if err := validateDeliveryCompletion(params); err != nil {
		return DeliveryClaim{}, err
	}
	receipt, err := canonicalJSON(params.Receipt, "{}")
	if err != nil {
		return DeliveryClaim{}, fmt.Errorf("%w: delivery receipt: %w", ErrInvalidRecord, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeliveryClaim{}, fmt.Errorf("begin delivery completion: %w", err)
	}
	defer rollback(tx)
	current, found, err := getDelivery(ctx, tx, params.Kind, params.ID)
	if err != nil {
		return DeliveryClaim{}, err
	}
	if !found {
		return DeliveryClaim{}, ErrNotFound
	}
	if current.Revision != params.ExpectedRevision {
		return DeliveryClaim{}, ErrRevisionConflict
	}
	now := s.now()
	if current.LeaseOwner != params.LeaseOwner || current.LeaseExpiresAt.IsZero() ||
		!current.LeaseExpiresAt.After(now) {
		return DeliveryClaim{}, ErrUnauthorized
	}
	if current.State != DeliveryStateDelivering {
		return DeliveryClaim{}, ErrNotRespondable
	}
	attempt, found, err := getOpenDeliveryAttempt(ctx, tx, current.Kind, current.ID)
	if err != nil {
		return DeliveryClaim{}, err
	}
	if !found {
		return DeliveryClaim{}, fmt.Errorf("%w: delivering claim has no open attempt", ErrInvalidRecord)
	}
	if err := sealDeliveryAttempt(
		ctx, tx, attempt.ID, params.To, receipt, params.ErrorCode, params.ErrorMessage, now,
	); err != nil {
		return DeliveryClaim{}, err
	}
	if err := updateCompletedDelivery(ctx, tx, params, receipt, now); err != nil {
		return DeliveryClaim{}, err
	}
	if err := s.insertDeliveryEvent(
		ctx, tx, current.Kind, current.ID, deliveryCompletionEvent(params.To),
		current.Revision, current.Revision+1, &attempt.AttemptNumber,
		deliveryCompletionMetadata(params), now,
	); err != nil {
		return DeliveryClaim{}, err
	}
	if err := tx.Commit(); err != nil {
		return DeliveryClaim{}, fmt.Errorf("commit delivery completion: %w", err)
	}
	current.Revision++
	current.AttemptID = attempt.ID
	current.AttemptNumber = attempt.AttemptNumber
	current.State = params.To
	current.LeaseOwner = ""
	current.LeaseExpiresAt = time.Time{}
	return current.DeliveryClaim, nil
}

type interruptedDelivery struct {
	DeliveryClaim
	StartedAt time.Time
}

func loadRecoveryReport(ctx context.Context, tx *sql.Tx) (RecoveryReport, error) {
	var report RecoveryReport
	err := tx.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(*) FROM interactions WHERE lifecycle_state = 'staged'),
  (SELECT COUNT(*) FROM interactions WHERE lifecycle_state = 'presented'),
  (SELECT COUNT(DISTINCT i.id) FROM interactions i
     JOIN draft_revisions d ON d.interaction_id = i.id
     WHERE i.lifecycle_state = 'in_progress'),
  (SELECT COUNT(DISTINCT i.id) FROM interactions i
     JOIN resolutions r ON r.interaction_id = i.id
     JOIN resolution_deliveries d ON d.resolution_id = r.id
     WHERE i.lifecycle_state = 'resolved'
       AND d.lifecycle_state IN ('queued', 'delivering', 'retryable_failure')),
  (SELECT COUNT(DISTINCT i.id) FROM interactions i
     JOIN resolutions r ON r.interaction_id = i.id
     JOIN resolution_deliveries d ON d.resolution_id = r.id
     WHERE i.lifecycle_state = 'resolved' AND d.lifecycle_state = 'delivered'),
  (SELECT COUNT(*) FROM interactions
	 WHERE lifecycle_state IN ('canceled', 'expired', 'failed', 'superseded')),
  ((SELECT COUNT(*) FROM resolution_deliveries WHERE lifecycle_state = 'terminal_failure')
    + (SELECT COUNT(*) FROM terminal_notifications WHERE lifecycle_state = 'terminal_failure'))`).Scan(
		&report.Staged,
		&report.Presented,
		&report.DraftBearing,
		&report.ResolvedUndelivered,
		&report.DeliveredUnacknowledged,
		&report.PreservedTerminalInteractions,
		&report.TerminalFailedDeliveries,
	)
	if err != nil {
		return RecoveryReport{}, fmt.Errorf("classify restart recovery state: %w", err)
	}
	return report, nil
}

func listInterruptedDeliveries(
	ctx context.Context,
	tx *sql.Tx,
	kind DeliveryKind,
) ([]interruptedDelivery, error) {
	query, err := deliverySelect(kind, "lifecycle_state = 'delivering'")
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list interrupted %s deliveries: %w", kind, err)
	}
	defer closeRows(rows)
	var out []interruptedDelivery
	for rows.Next() {
		delivery, scanErr := scanDeliveryClaim(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate interrupted %s deliveries: %w", kind, err)
	}
	return out, nil
}

func nextEligibleDelivery(
	ctx context.Context,
	tx *sql.Tx,
	now time.Time,
) (interruptedDelivery, bool, error) {
	row := tx.QueryRowContext(ctx, `
	SELECT kind, id, interaction_id, resolution_id, destination_binding,
	       idempotency_key, policy, lifecycle_state, revision, lease_owner, lease_expires_at,
       updated_at
FROM (
  SELECT 'resolution' AS kind, d.id, r.interaction_id, d.resolution_id,
	         d.destination_binding, d.idempotency_key, d.policy, d.lifecycle_state, d.revision,
         d.lease_owner, d.lease_expires_at, d.updated_at, d.created_at
  FROM resolution_deliveries d
  JOIN resolutions r ON r.id = d.resolution_id
  WHERE d.lifecycle_state = 'queued'
     OR (d.lifecycle_state = 'retryable_failure'
         AND d.next_eligible_at IS NOT NULL AND d.next_eligible_at <= ?)
  UNION ALL
  SELECT 'terminal_notification' AS kind, n.id, n.interaction_id, NULL,
	         n.destination_binding, n.idempotency_key, n.policy, n.lifecycle_state, n.revision,
         n.lease_owner, n.lease_expires_at, n.updated_at, n.created_at
  FROM terminal_notifications n
  WHERE n.lifecycle_state = 'queued'
     OR (n.lifecycle_state = 'retryable_failure'
         AND n.next_eligible_at IS NOT NULL AND n.next_eligible_at <= ?)
)
ORDER BY created_at, id
LIMIT 1`, now, now)
	delivery, err := scanDeliveryClaim(row)
	if errors.Is(err, sql.ErrNoRows) {
		return interruptedDelivery{}, false, nil
	}
	if err != nil {
		return interruptedDelivery{}, false, err
	}
	return delivery, true, nil
}

func getDelivery(
	ctx context.Context,
	tx *sql.Tx,
	kind DeliveryKind,
	id string,
) (interruptedDelivery, bool, error) {
	query, err := deliverySelect(kind, "id = ?")
	if err != nil {
		return interruptedDelivery{}, false, err
	}
	delivery, err := scanDeliveryClaim(tx.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return interruptedDelivery{}, false, nil
	}
	if err != nil {
		return interruptedDelivery{}, false, err
	}
	return delivery, true, nil
}

func deliverySelect(kind DeliveryKind, predicate string) (string, error) {
	switch kind {
	case DeliveryKindResolution:
		return `
SELECT 'resolution', d.id, r.interaction_id, d.resolution_id,
	       d.destination_binding, d.idempotency_key, d.policy, d.lifecycle_state, d.revision,
       d.lease_owner, d.lease_expires_at, d.updated_at
FROM resolution_deliveries d
JOIN resolutions r ON r.id = d.resolution_id
WHERE d.` + predicate, nil
	case DeliveryKindTerminalNotification:
		return `
SELECT 'terminal_notification', n.id, n.interaction_id, NULL,
	       n.destination_binding, n.idempotency_key, n.policy, n.lifecycle_state, n.revision,
       n.lease_owner, n.lease_expires_at, n.updated_at
FROM terminal_notifications n
WHERE n.` + predicate, nil
	default:
		return "", fmt.Errorf("%w: unknown delivery kind %q", ErrInvalidRecord, kind)
	}
}

func scanDeliveryClaim(row scanner) (interruptedDelivery, error) {
	var delivery interruptedDelivery
	var destination, policy string
	var resolutionID, leaseOwner sql.NullString
	var leaseExpiresAt, startedAt any
	if err := row.Scan(
		&delivery.Kind,
		&delivery.ID,
		&delivery.InteractionID,
		&resolutionID,
		&destination,
		&delivery.IdempotencyKey,
		&policy,
		&delivery.State,
		&delivery.Revision,
		&leaseOwner,
		&leaseExpiresAt,
		&startedAt,
	); err != nil {
		return interruptedDelivery{}, err
	}
	delivery.ResolutionID = resolutionID.String
	delivery.DestinationBinding = json.RawMessage(destination)
	delivery.Policy = json.RawMessage(policy)
	delivery.LeaseOwner = leaseOwner.String
	parsedLease, err := parseNullableTime(leaseExpiresAt)
	if err != nil {
		return interruptedDelivery{}, fmt.Errorf("scan delivery lease expiry: %w", err)
	}
	if parsedLease != nil {
		delivery.LeaseExpiresAt = *parsedLease
	}
	delivery.StartedAt, err = parseTime(startedAt)
	if err != nil {
		return interruptedDelivery{}, fmt.Errorf("scan delivery claim start: %w", err)
	}
	return delivery, nil
}

func updateRecoveredDelivery(
	ctx context.Context,
	tx *sql.Tx,
	delivery interruptedDelivery,
	now time.Time,
	nextEligibleAt any,
) error {
	query, err := recoveredDeliveryUpdate(delivery.Kind)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, query,
		nextEligibleAt, now, delivery.ID, delivery.Revision)
	if err != nil {
		return fmt.Errorf("recover %s delivery %s: %w", delivery.Kind, delivery.ID, err)
	}
	if !changedOne(result) {
		return ErrRevisionConflict
	}
	return nil
}

func recoveredDeliveryUpdate(kind DeliveryKind) (string, error) {
	const suffix = `
SET lifecycle_state = 'retryable_failure', revision = revision + 1,
    lease_owner = NULL, lease_expires_at = NULL, next_eligible_at = ?,
    updated_at = ?
WHERE id = ? AND revision = ? AND lifecycle_state = 'delivering'`
	switch kind {
	case DeliveryKindResolution:
		return `UPDATE resolution_deliveries` + suffix, nil
	case DeliveryKindTerminalNotification:
		return `UPDATE terminal_notifications` + suffix, nil
	default:
		return "", fmt.Errorf("%w: unknown delivery kind %q", ErrInvalidRecord, kind)
	}
}

func updateDeliveryClaim(ctx context.Context, tx *sql.Tx, claim DeliveryClaim, now time.Time) error {
	query, err := deliveryClaimUpdate(claim.Kind)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, query,
		claim.LeaseOwner,
		claim.LeaseExpiresAt,
		now,
		claim.ID,
		claim.Revision-1,
		now,
	)
	if err != nil {
		return fmt.Errorf("claim %s delivery %s: %w", claim.Kind, claim.ID, err)
	}
	if !changedOne(result) {
		return ErrRevisionConflict
	}
	return nil
}

func deliveryClaimUpdate(kind DeliveryKind) (string, error) {
	const suffix = `
SET lifecycle_state = 'delivering', revision = revision + 1,
    lease_owner = ?, lease_expires_at = ?, next_eligible_at = NULL,
    updated_at = ?
WHERE id = ? AND revision = ?
  AND (lifecycle_state = 'queued'
       OR (lifecycle_state = 'retryable_failure'
           AND next_eligible_at IS NOT NULL AND next_eligible_at <= ?))`
	switch kind {
	case DeliveryKindResolution:
		return `UPDATE resolution_deliveries` + suffix, nil
	case DeliveryKindTerminalNotification:
		return `UPDATE terminal_notifications` + suffix, nil
	default:
		return "", fmt.Errorf("%w: unknown delivery kind %q", ErrInvalidRecord, kind)
	}
}

func updateCompletedDelivery(
	ctx context.Context,
	tx *sql.Tx,
	params CompleteDeliveryParams,
	receipt json.RawMessage,
	now time.Time,
) error {
	query, err := completedDeliveryUpdate(params.Kind)
	if err != nil {
		return err
	}
	var storedReceipt any
	var terminalReason any
	var nextEligibleAt any
	var deliveredAt any
	switch params.To {
	case DeliveryStateDelivered:
		storedReceipt = string(receipt)
		deliveredAt = now
	case DeliveryStateRetryableFailure:
		nextEligibleAt = params.NextEligibleAt
	case DeliveryStateTerminalFailure:
		terminalReason = params.ErrorMessage
	}
	result, err := tx.ExecContext(ctx, query,
		params.To,
		storedReceipt,
		terminalReason,
		nextEligibleAt,
		deliveredAt,
		now,
		params.ID,
		params.ExpectedRevision,
		params.LeaseOwner,
	)
	if err != nil {
		return fmt.Errorf("complete %s delivery %s: %w", params.Kind, params.ID, err)
	}
	if !changedOne(result) {
		return ErrRevisionConflict
	}
	return nil
}

func completedDeliveryUpdate(kind DeliveryKind) (string, error) {
	const suffix = `
SET lifecycle_state = ?, revision = revision + 1,
    lease_owner = NULL, lease_expires_at = NULL,
    receipt = COALESCE(?, receipt), terminal_reason = ?,
    next_eligible_at = ?, delivered_at = COALESCE(?, delivered_at),
    updated_at = ?
WHERE id = ? AND revision = ? AND lifecycle_state = 'delivering'
  AND lease_owner = ?`
	switch kind {
	case DeliveryKindResolution:
		return `UPDATE resolution_deliveries` + suffix, nil
	case DeliveryKindTerminalNotification:
		return `UPDATE terminal_notifications` + suffix, nil
	default:
		return "", fmt.Errorf("%w: unknown delivery kind %q", ErrInvalidRecord, kind)
	}
}

func (s *Store) openDeliveryAttempt(
	ctx context.Context,
	tx *sql.Tx,
	delivery interruptedDelivery,
	startedAt time.Time,
) (DeliveryAttempt, error) {
	attemptNumber, err := nextDeliveryAttemptNumber(ctx, tx, delivery.Kind, delivery.ID)
	if err != nil {
		return DeliveryAttempt{}, err
	}
	var resolutionDeliveryID any
	var notificationID any
	var resolutionID string
	var terminalNotificationID string
	switch delivery.Kind {
	case DeliveryKindResolution:
		resolutionDeliveryID = delivery.ID
		resolutionID = delivery.ID
	case DeliveryKindTerminalNotification:
		notificationID = delivery.ID
		terminalNotificationID = delivery.ID
	default:
		return DeliveryAttempt{}, fmt.Errorf("%w: unknown delivery kind %q", ErrInvalidRecord, delivery.Kind)
	}
	attemptID := s.id()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO delivery_attempts (
  id, resolution_delivery_id, terminal_notification_id,
	  attempt_number, status, started_at
) VALUES (?, ?, ?, ?, 'delivering', ?)`,
		attemptID, resolutionDeliveryID, notificationID, attemptNumber, startedAt,
	); err != nil {
		return DeliveryAttempt{}, fmt.Errorf("open %s delivery attempt: %w", delivery.Kind, err)
	}
	return DeliveryAttempt{
		ID: attemptID, ResolutionDeliveryID: resolutionID,
		TerminalNotificationID: terminalNotificationID, AttemptNumber: attemptNumber,
		Status: DeliveryStateDelivering, StartedAt: startedAt,
	}, nil
}

func (s *Store) ensureOpenDeliveryAttempt(
	ctx context.Context,
	tx *sql.Tx,
	delivery interruptedDelivery,
	startedAt time.Time,
) (DeliveryAttempt, bool, error) {
	attempt, found, err := getOpenDeliveryAttempt(ctx, tx, delivery.Kind, delivery.ID)
	if err != nil {
		return DeliveryAttempt{}, false, err
	}
	if found {
		return attempt, false, nil
	}
	attempt, err = s.openDeliveryAttempt(ctx, tx, delivery, startedAt)
	return attempt, true, err
}

func getOpenDeliveryAttempt(
	ctx context.Context,
	tx *sql.Tx,
	kind DeliveryKind,
	id string,
) (DeliveryAttempt, bool, error) {
	var query string
	switch kind {
	case DeliveryKindResolution:
		query = `
SELECT id, resolution_delivery_id, terminal_notification_id,
       attempt_number, status, receipt, error_code, error_message,
       started_at, completed_at
FROM delivery_attempts
WHERE resolution_delivery_id = ? AND status = 'delivering' AND completed_at IS NULL`
	case DeliveryKindTerminalNotification:
		query = `
SELECT id, resolution_delivery_id, terminal_notification_id,
       attempt_number, status, receipt, error_code, error_message,
       started_at, completed_at
FROM delivery_attempts
WHERE terminal_notification_id = ? AND status = 'delivering' AND completed_at IS NULL`
	default:
		return DeliveryAttempt{}, false, fmt.Errorf("%w: unknown delivery kind %q", ErrInvalidRecord, kind)
	}
	attempt, err := scanDeliveryAttempt(tx.QueryRowContext(ctx, query, id))
	if errors.Is(err, ErrNotFound) {
		return DeliveryAttempt{}, false, nil
	}
	if err != nil {
		return DeliveryAttempt{}, false, err
	}
	return attempt, true, nil
}

func sealDeliveryAttempt(
	ctx context.Context,
	tx *sql.Tx,
	attemptID string,
	status DeliveryState,
	receipt json.RawMessage,
	errorCode string,
	errorMessage string,
	completedAt time.Time,
) error {
	var storedReceipt any
	if status == DeliveryStateDelivered {
		storedReceipt = string(receipt)
	}
	result, err := tx.ExecContext(ctx, `
UPDATE delivery_attempts
SET status = ?, receipt = ?, error_code = ?, error_message = ?, completed_at = ?
WHERE id = ? AND status = 'delivering' AND completed_at IS NULL`,
		status, storedReceipt, nullString(errorCode), nullString(errorMessage), completedAt, attemptID,
	)
	if err != nil {
		return fmt.Errorf("seal delivery attempt %s: %w", attemptID, err)
	}
	if !changedOne(result) {
		return ErrRevisionConflict
	}
	return nil
}

func (s *Store) insertDeliveryEvent(
	ctx context.Context,
	tx *sql.Tx,
	kind DeliveryKind,
	deliveryID string,
	eventType string,
	fromRevision int64,
	toRevision int64,
	attemptNumber *int64,
	metadata string,
	recordedAt time.Time,
) error {
	var resolutionDeliveryID any
	var notificationID any
	switch kind {
	case DeliveryKindResolution:
		resolutionDeliveryID = deliveryID
	case DeliveryKindTerminalNotification:
		notificationID = deliveryID
	default:
		return fmt.Errorf("%w: unknown delivery kind %q", ErrInvalidRecord, kind)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO delivery_events (
  event_id, resolution_delivery_id, terminal_notification_id,
  event_type, from_revision, to_revision, attempt_number, metadata, recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.id(), resolutionDeliveryID, notificationID, eventType,
		fromRevision, toRevision, attemptNumber, metadata, recordedAt,
	); err != nil {
		return fmt.Errorf("insert %s event for %s: %w", eventType, deliveryID, err)
	}
	return nil
}

func nextDeliveryAttemptNumber(
	ctx context.Context,
	tx *sql.Tx,
	kind DeliveryKind,
	id string,
) (int64, error) {
	var query string
	switch kind {
	case DeliveryKindResolution:
		query = `
SELECT COALESCE(MAX(attempt_number), 0) + 1
FROM delivery_attempts
WHERE resolution_delivery_id = ?`
	case DeliveryKindTerminalNotification:
		query = `
SELECT COALESCE(MAX(attempt_number), 0) + 1
FROM delivery_attempts
WHERE terminal_notification_id = ?`
	default:
		return 0, fmt.Errorf("%w: unknown delivery kind %q", ErrInvalidRecord, kind)
	}
	var attemptNumber int64
	if err := tx.QueryRowContext(ctx, query, id).Scan(&attemptNumber); err != nil {
		return 0, fmt.Errorf("load next %s delivery attempt: %w", kind, err)
	}
	return attemptNumber, nil
}

func validateDeliveryCompletion(params CompleteDeliveryParams) error {
	invalid := func(message string) error {
		return fmt.Errorf("%w: %s", ErrInvalidRecord, message)
	}
	switch params.To {
	case DeliveryStateDelivered:
		if len(params.Receipt) == 0 || params.ErrorCode != "" || params.ErrorMessage != "" ||
			params.NextEligibleAt != nil {
			return invalid("delivered completion requires only a positive receipt")
		}
	case DeliveryStateRetryableFailure:
		if params.ErrorCode == "" || params.ErrorMessage == "" || len(params.Receipt) != 0 {
			return invalid("retryable failure requires an error and no receipt")
		}
	case DeliveryStateTerminalFailure:
		if params.ErrorCode == "" || params.ErrorMessage == "" || len(params.Receipt) != 0 ||
			params.NextEligibleAt != nil {
			return invalid("terminal failure requires an error and no receipt")
		}
	default:
		return invalid("completion state must be delivered, retryable_failure, or terminal_failure")
	}
	return nil
}

func restartPolicyIsIdempotentlyRetryable(destinationRaw, policyRaw json.RawMessage) bool {
	var destination struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(destinationRaw, &destination); err != nil {
		return false
	}
	var policy struct {
		Delivery        string `json:"delivery"`
		RestartRecovery string `json:"restart_recovery"`
	}
	if err := json.Unmarshal(policyRaw, &policy); err != nil {
		return false
	}
	switch policy.RestartRecovery {
	case "manual", "non_idempotent", "do_not_retry", "never":
		return false
	case "idempotent_retry":
		return true
	case "":
		return destination.Kind == "caller_pull" && policy.Delivery == "durable-caller-pull"
	default:
		return false
	}
}

func deliveryCompletionEvent(state DeliveryState) string {
	switch state {
	case DeliveryStateDelivered:
		return "delivery.delivered"
	case DeliveryStateRetryableFailure:
		return "delivery.retryable_failed"
	case DeliveryStateTerminalFailure:
		return "delivery.terminal_failed"
	default:
		return ""
	}
}

func deliveryCompletionMetadata(params CompleteDeliveryParams) string {
	metadata := map[string]any{}
	if params.ErrorCode != "" {
		metadata["error_code"] = params.ErrorCode
	}
	if params.NextEligibleAt != nil {
		metadata["next_eligible_at"] = *params.NextEligibleAt
	}
	return mustJSON(metadata)
}

// deliveryWorkerLeaseOwner binds a lease to the complete adapter-established
// actor rather than trusting a caller-controlled authority label.
func deliveryWorkerLeaseOwner(actor ActorBinding) string {
	sum := sha256.Sum256([]byte(actor.Scope + "\x00" + actor.PrincipalRef + "\x00" + actor.Authority + "\x00" + actor.Assurance))
	return "actor-sha256:" + hex.EncodeToString(sum[:])
}
