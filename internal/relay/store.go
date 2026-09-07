// Package relay owns the durable exchange journal and its transactional
// outbox that implement docs/adr/0006-collaboration-surface-and-relay-boundary.md
// §4 (CW-20260906-0065): "Tangent's relay journal is authoritative for what
// Tangent accepted and delivered, and for nothing else."
//
// It is a leaf storage package over internal/channel the same way
// internal/channel is a leaf storage package over the shared *sql.DB — a
// concrete Store, no transport, no MCP surface, shaped so the personal-MVP
// plan's decision 2 can lift it into go-app-agent later without a rewrite.
// It depends on internal/channel to validate a channel, its participants,
// and their current runtime binding; internal/channel has no reciprocal
// dependency.
package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/tangent/internal/channel"
)

// Errors returned by Store.
var (
	ErrNotFound = errors.New("relay store: record not found")
	// ErrInvalidRecord is returned for a caller error: a missing required
	// field or an unrecognized enum value.
	ErrInvalidRecord = errors.New("relay store: invalid record")
	// ErrIdempotencyConflict means the same sender reused an idempotency key
	// for a materially different exchange — the one case idempotency must
	// refuse rather than silently resolve to the earlier row.
	ErrIdempotencyConflict = errors.New("relay store: idempotency key reused for a different exchange")
	// ErrNotMember means AcceptExchange's sender or recipient is not a live
	// member of the named channel.
	ErrNotMember = errors.New("relay store: sender or recipient is not a member of the channel")
)

// Store persists the exchange journal, its outbox, delivery receipts, and
// read facts.
type Store struct {
	db       *sql.DB
	channels *channel.Store
	now      func() time.Time
	id       func() string
}

// NewStore constructs a Store over the shared Tangent handle and the
// channel store it validates against.
func NewStore(db *sql.DB, channels *channel.Store) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	if channels == nil {
		return nil, fmt.Errorf("%w: nil channel store", ErrInvalidRecord)
	}
	return &Store{db: db, channels: channels, now: func() time.Time { return time.Now().UTC() }, id: uuid.NewString}, nil
}

// ── exchanges (the journal) ─────────────────────────────────────────────

// AcceptExchangeParams are the fields a caller supplies to accept one
// message into the journal.
type AcceptExchangeParams struct {
	IdempotencyKey         string
	ChannelID              string
	SubjectID              string // optional: which thread this correlates
	SenderParticipantID    string
	RecipientParticipantID string
	ReplyToExchangeID      string // optional
	Body                   string
}

// AcceptExchange validates the channel and its participants, resolves the
// recipient's current runtime binding as a frozen destination snapshot, and
// atomically inserts the exchange plus its outbox row in one transaction —
// the transactional-outbox guarantee that a caller can never accept a
// message without delivery work queued for it.
//
// Replaying the same (sender, idempotency key) pair for the same channel,
// recipient, and body resolves to the original exchange rather than minting
// a second one; replaying it for a different channel, recipient, or body is
// ErrIdempotencyConflict.
func (s *Store) AcceptExchange(ctx context.Context, params AcceptExchangeParams) (Exchange, error) {
	if s == nil || s.db == nil {
		return Exchange{}, fmt.Errorf("%w: nil store", ErrInvalidRecord)
	}
	if params.IdempotencyKey == "" || params.ChannelID == "" || params.SenderParticipantID == "" ||
		params.RecipientParticipantID == "" || params.Body == "" {
		return Exchange{}, fmt.Errorf("%w: idempotency key, channel, sender, recipient, and body are all required", ErrInvalidRecord)
	}

	if _, err := s.channels.GetChannel(ctx, params.ChannelID); err != nil {
		if errors.Is(err, channel.ErrNotFound) {
			return Exchange{}, fmt.Errorf("%w: channel %q", ErrNotFound, params.ChannelID)
		}
		return Exchange{}, err
	}
	for _, participantID := range []string{params.SenderParticipantID, params.RecipientParticipantID} {
		member, err := s.channels.IsMember(ctx, params.ChannelID, participantID)
		if err != nil {
			return Exchange{}, err
		}
		if !member {
			return Exchange{}, fmt.Errorf("%w: participant %q in channel %q", ErrNotMember, participantID, params.ChannelID)
		}
	}

	recipient, err := s.channels.GetParticipant(ctx, params.RecipientParticipantID)
	if err != nil {
		return Exchange{}, err
	}
	var recipientBindingID string
	if recipient.Kind == channel.ParticipantAgent {
		binding, bindingErr := s.channels.CurrentBinding(ctx, params.ChannelID, params.RecipientParticipantID)
		switch {
		case bindingErr == nil:
			recipientBindingID = binding.ID
		case errors.Is(bindingErr, channel.ErrNotFound):
			// No live binding yet: the exchange is still accepted, queued
			// awaiting-peer. ARCHITECTURE.md §5 names this explicitly as a
			// UI state, not a failure.
		default:
			return Exchange{}, bindingErr
		}
	}

	existing, err := s.getByIdempotencyKey(ctx, params.SenderParticipantID, params.IdempotencyKey)
	switch {
	case err == nil:
		if existing.ChannelID != params.ChannelID || existing.RecipientParticipantID != params.RecipientParticipantID ||
			existing.Body != params.Body {
			return Exchange{}, fmt.Errorf("%w: %q", ErrIdempotencyConflict, params.IdempotencyKey)
		}
		return existing, nil
	case errors.Is(err, ErrNotFound):
		// No prior exchange under this key — proceed to accept a new one.
	default:
		return Exchange{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Exchange{}, fmt.Errorf("relay store: begin accept: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var nextSequence int64
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(sequence), 0) + 1 FROM exchanges WHERE recipient_participant_id = ?`,
		params.RecipientParticipantID).Scan(&nextSequence)
	if err != nil {
		return Exchange{}, fmt.Errorf("relay store: assign sequence: %w", err)
	}

	now := s.now()
	exchange := Exchange{
		ID:                     s.id(),
		IdempotencyKey:         params.IdempotencyKey,
		ChannelID:              params.ChannelID,
		SubjectID:              params.SubjectID,
		SenderParticipantID:    params.SenderParticipantID,
		RecipientParticipantID: params.RecipientParticipantID,
		RecipientBindingID:     recipientBindingID,
		ReplyToExchangeID:      params.ReplyToExchangeID,
		Body:                   params.Body,
		Sequence:               nextSequence,
		CreatedAt:              now,
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO exchanges (
  id, idempotency_key, channel_id, subject_id, sender_participant_id,
  recipient_participant_id, recipient_binding_id, reply_to_exchange_id,
  body, sequence, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		exchange.ID, exchange.IdempotencyKey, exchange.ChannelID, nullableText(exchange.SubjectID),
		exchange.SenderParticipantID, exchange.RecipientParticipantID, nullableText(exchange.RecipientBindingID),
		nullableText(exchange.ReplyToExchangeID), exchange.Body, exchange.Sequence, exchange.CreatedAt,
	)
	if err != nil {
		return Exchange{}, fmt.Errorf("relay store: insert exchange: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO exchange_outbox (exchange_id, created_at, updated_at) VALUES (?, ?, ?)`,
		exchange.ID, now, now,
	); err != nil {
		return Exchange{}, fmt.Errorf("relay store: insert outbox row: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Exchange{}, fmt.Errorf("relay store: commit accept: %w", err)
	}
	return exchange, nil
}

// GetExchange loads one exchange by id.
func (s *Store) GetExchange(ctx context.Context, id string) (Exchange, error) {
	row := s.db.QueryRowContext(ctx, exchangeSelectColumns+` FROM exchanges WHERE id = ?`, id)
	exchange, err := scanExchange(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Exchange{}, ErrNotFound
	}
	if err != nil {
		return Exchange{}, fmt.Errorf("relay store: load exchange: %w", err)
	}
	return exchange, nil
}

func (s *Store) getByIdempotencyKey(ctx context.Context, senderParticipantID, idempotencyKey string) (Exchange, error) {
	row := s.db.QueryRowContext(ctx,
		exchangeSelectColumns+` FROM exchanges WHERE sender_participant_id = ? AND idempotency_key = ?`,
		senderParticipantID, idempotencyKey)
	exchange, err := scanExchange(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Exchange{}, ErrNotFound
	}
	if err != nil {
		return Exchange{}, fmt.Errorf("relay store: load exchange by idempotency key: %w", err)
	}
	return exchange, nil
}

// ListForDestination is the storage half of the spike's `receive(cursor,
// wait_ms)` contract: every exchange addressed to recipientParticipantID
// with sequence strictly greater than sinceSequence, oldest first. The
// caller holds the cursor (the last sequence it saw) and passes it back
// next time; nothing here persists a per-destination read position.
func (s *Store) ListForDestination(ctx context.Context, recipientParticipantID string, sinceSequence int64, limit int) ([]Exchange, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		exchangeSelectColumns+` FROM exchanges WHERE recipient_participant_id = ? AND sequence > ? ORDER BY sequence LIMIT ?`,
		recipientParticipantID, sinceSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("relay store: list for destination: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var exchanges []Exchange
	for rows.Next() {
		exchange, scanErr := scanExchange(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("relay store: scan exchange: %w", scanErr)
		}
		exchanges = append(exchanges, exchange)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("relay store: iterate destination exchanges: %w", err)
	}
	return exchanges, nil
}

const exchangeSelectColumns = `SELECT id, idempotency_key, channel_id, subject_id, sender_participant_id,
       recipient_participant_id, recipient_binding_id, reply_to_exchange_id, body, sequence, created_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanExchange(row rowScanner) (Exchange, error) {
	var (
		exchange  Exchange
		subjectID sql.NullString
		bindingID sql.NullString
		replyToID sql.NullString
	)
	if err := row.Scan(
		&exchange.ID, &exchange.IdempotencyKey, &exchange.ChannelID, &subjectID,
		&exchange.SenderParticipantID, &exchange.RecipientParticipantID, &bindingID,
		&replyToID, &exchange.Body, &exchange.Sequence, &exchange.CreatedAt,
	); err != nil {
		return Exchange{}, err
	}
	exchange.SubjectID = subjectID.String
	exchange.RecipientBindingID = bindingID.String
	exchange.ReplyToExchangeID = replyToID.String
	return exchange, nil
}

// RecipientBindingCurrent reports whether an exchange's frozen destination
// snapshot still matches the recipient's live runtime binding. false means
// the recipient rebound (ADR 0006 §3) since this exchange was accepted — the
// stale-binding signal a delivery worker uses to hold rather than route to a
// destination the exchange was never actually addressed to.
func (s *Store) RecipientBindingCurrent(ctx context.Context, exchangeID string) (bool, error) {
	exchange, err := s.GetExchange(ctx, exchangeID)
	if err != nil {
		return false, err
	}
	current, err := s.channels.CurrentBinding(ctx, exchange.ChannelID, exchange.RecipientParticipantID)
	if errors.Is(err, channel.ErrNotFound) {
		return exchange.RecipientBindingID == "", nil
	}
	if err != nil {
		return false, err
	}
	return exchange.RecipientBindingID == current.ID, nil
}

// ── exchange_outbox (the transactional outbox) ─────────────────────────

// ClaimNextForDelivery leases up to limit claimable outbox rows — those
// pending, or leased with an expired lease — to workerID for leaseDuration.
// The expired-lease branch is the crash-boundary recovery: a worker that
// leased a row and never called RecordDeliveryOutcome (crashed, was killed,
// lost its connection) leaves a lease that any worker, including a restarted
// instance of itself, can reclaim once it expires. The claiming UPDATE
// re-checks the same claimability condition the SELECT used, so a lease that
// was reclaimed by another caller between the two statements is not
// silently re-claimed a second time.
func (s *Store) ClaimNextForDelivery(ctx context.Context, workerID string, leaseDuration time.Duration, limit int) ([]OutboxItem, error) {
	if workerID == "" {
		return nil, fmt.Errorf("%w: worker id is required", ErrInvalidRecord)
	}
	if leaseDuration <= 0 {
		return nil, fmt.Errorf("%w: lease duration must be positive", ErrInvalidRecord)
	}
	if limit <= 0 {
		limit = 10
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("relay store: begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.now()
	rows, err := tx.QueryContext(ctx, `
SELECT exchange_id FROM exchange_outbox
WHERE status = 'pending' OR (status = 'leased' AND lease_expires_at < ?)
ORDER BY created_at LIMIT ?`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("relay store: select claimable: %w", err)
	}
	var ids []string
	for rows.Next() {
		var exchangeID string
		if scanErr := rows.Scan(&exchangeID); scanErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("relay store: scan claimable: %w", scanErr)
		}
		ids = append(ids, exchangeID)
	}
	iterErr := rows.Err()
	_ = rows.Close()
	if iterErr != nil {
		return nil, fmt.Errorf("relay store: iterate claimable: %w", iterErr)
	}

	leaseExpiresAt := now.Add(leaseDuration)
	claimed := make([]OutboxItem, 0, len(ids))
	for _, exchangeID := range ids {
		result, execErr := tx.ExecContext(ctx, `
UPDATE exchange_outbox
SET status = 'leased', leased_by = ?, leased_at = ?, lease_expires_at = ?, attempts = attempts + 1, updated_at = ?
WHERE exchange_id = ? AND (status = 'pending' OR (status = 'leased' AND lease_expires_at < ?))`,
			workerID, now, leaseExpiresAt, now, exchangeID, now)
		if execErr != nil {
			return nil, fmt.Errorf("relay store: claim %s: %w", exchangeID, execErr)
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return nil, fmt.Errorf("relay store: claim %s: %w", exchangeID, affectedErr)
		}
		if affected == 0 {
			continue
		}
		item, getErr := s.getOutbox(ctx, tx, exchangeID)
		if getErr != nil {
			return nil, getErr
		}
		claimed = append(claimed, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("relay store: commit claim: %w", err)
	}
	return claimed, nil
}

// RecordDeliveryOutcomeParams are the fields a caller supplies to
// reconcile one delivery attempt.
type RecordDeliveryOutcomeParams struct {
	Outcome            DeliveryOutcome
	RuntimeAuthority   string
	RuntimeEndpointRef string
	ErrorCode          string
	ErrorMessage       string
}

// RecordDeliveryOutcome writes the append-only receipt for one delivery
// attempt and, in the same transaction, moves the outbox row to the status
// the outcome implies: delivered and failed are terminal (failed only until
// an explicit Requeue); uncertain returns to pending automatically, because
// an ambiguous outcome must default to retryable rather than to either
// certainty. Neither transition ever touches the exchange's sender,
// recipient, or body — those live on the immutable journal row, not here.
func (s *Store) RecordDeliveryOutcome(ctx context.Context, exchangeID string, params RecordDeliveryOutcomeParams) (DeliveryReceipt, error) {
	switch params.Outcome {
	case DeliveryDelivered, DeliveryFailed, DeliveryUncertain:
	default:
		return DeliveryReceipt{}, fmt.Errorf("%w: unknown delivery outcome %q", ErrInvalidRecord, params.Outcome)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeliveryReceipt{}, fmt.Errorf("relay store: begin record outcome: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	item, err := s.getOutbox(ctx, tx, exchangeID)
	if err != nil {
		return DeliveryReceipt{}, err
	}

	now := s.now()
	receipt := DeliveryReceipt{
		ID:                 s.id(),
		ExchangeID:         exchangeID,
		AttemptNumber:      item.Attempts,
		Outcome:            params.Outcome,
		RuntimeAuthority:   params.RuntimeAuthority,
		RuntimeEndpointRef: params.RuntimeEndpointRef,
		ErrorCode:          params.ErrorCode,
		ErrorMessage:       params.ErrorMessage,
		AttemptedAt:        now,
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO exchange_delivery_receipts (
  id, exchange_id, attempt_number, outcome, runtime_authority,
  runtime_endpoint_ref, error_code, error_message, attempted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		receipt.ID, receipt.ExchangeID, receipt.AttemptNumber, string(receipt.Outcome),
		receipt.RuntimeAuthority, receipt.RuntimeEndpointRef, receipt.ErrorCode, receipt.ErrorMessage, receipt.AttemptedAt,
	); err != nil {
		return DeliveryReceipt{}, fmt.Errorf("relay store: insert delivery receipt: %w", err)
	}

	var nextStatus OutboxStatus
	switch params.Outcome {
	case DeliveryDelivered:
		nextStatus = OutboxDelivered
	case DeliveryUncertain:
		nextStatus = OutboxPending
	case DeliveryFailed:
		nextStatus = OutboxFailed
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE exchange_outbox
SET status = ?, leased_by = '', leased_at = NULL, lease_expires_at = NULL, last_error = ?, updated_at = ?
WHERE exchange_id = ?`,
		string(nextStatus), params.ErrorMessage, now, exchangeID,
	); err != nil {
		return DeliveryReceipt{}, fmt.Errorf("relay store: update outbox after outcome: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return DeliveryReceipt{}, fmt.Errorf("relay store: commit record outcome: %w", err)
	}
	return receipt, nil
}

// Requeue explicitly moves a failed outbox row back to pending so
// ClaimNextForDelivery can pick it up again. It never touches the exchange
// row, so a retry can never retarget: the same sender, recipient, frozen
// binding, and body are retried, never a caller-supplied replacement.
func (s *Store) Requeue(ctx context.Context, exchangeID string) (OutboxItem, error) {
	now := s.now()
	result, err := s.db.ExecContext(ctx, `
UPDATE exchange_outbox
SET status = 'pending', leased_by = '', leased_at = NULL, lease_expires_at = NULL, updated_at = ?
WHERE exchange_id = ? AND status = 'failed'`, now, exchangeID)
	if err != nil {
		return OutboxItem{}, fmt.Errorf("relay store: requeue: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return OutboxItem{}, fmt.Errorf("relay store: requeue: %w", err)
	}
	if affected == 0 {
		return OutboxItem{}, ErrNotFound
	}
	return s.GetOutboxItem(ctx, exchangeID)
}

// GetOutboxItem loads one outbox row by its exchange id.
func (s *Store) GetOutboxItem(ctx context.Context, exchangeID string) (OutboxItem, error) {
	return s.getOutbox(ctx, s.db, exchangeID)
}

const outboxSelectColumns = `SELECT exchange_id, status, attempts, leased_by, leased_at, lease_expires_at,
       next_attempt_at, last_error, created_at, updated_at`

// queryRower is the narrow slice of *sql.DB and *sql.Tx a single-row read
// needs, so ClaimNextForDelivery and RecordDeliveryOutcome can read an
// outbox row from inside their own transaction while GetOutboxItem reads it
// from the plain handle.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) getOutbox(ctx context.Context, q queryRower, exchangeID string) (OutboxItem, error) {
	row := q.QueryRowContext(ctx, outboxSelectColumns+` FROM exchange_outbox WHERE exchange_id = ?`, exchangeID)
	item, err := scanOutboxItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxItem{}, ErrNotFound
	}
	if err != nil {
		return OutboxItem{}, fmt.Errorf("relay store: load outbox item: %w", err)
	}
	return item, nil
}

func scanOutboxItem(row rowScanner) (OutboxItem, error) {
	var (
		item                                    OutboxItem
		status                                  string
		leasedAt, leaseExpiresAt, nextAttemptAt sql.NullTime
	)
	if err := row.Scan(
		&item.ExchangeID, &status, &item.Attempts, &item.LeasedBy,
		&leasedAt, &leaseExpiresAt, &nextAttemptAt, &item.LastError,
		&item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return OutboxItem{}, err
	}
	item.Status = OutboxStatus(status)
	if leasedAt.Valid {
		t := leasedAt.Time
		item.LeasedAt = &t
	}
	if leaseExpiresAt.Valid {
		t := leaseExpiresAt.Time
		item.LeaseExpiresAt = &t
	}
	if nextAttemptAt.Valid {
		t := nextAttemptAt.Time
		item.NextAttemptAt = &t
	}
	return item, nil
}

// ── exchange_reads (the read fact) ─────────────────────────────────────

// RecordRead records that a participant consumed an exchange. It is
// idempotent: acking the same (exchange, participant) pair twice is a
// no-op, never an error, so a retried ack call can never produce a false
// "acked twice" claim.
func (s *Store) RecordRead(ctx context.Context, exchangeID, participantID string) (ReadReceipt, error) {
	now := s.now()
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO exchange_reads (exchange_id, participant_id, acked_at)
VALUES (?, ?, ?)
ON CONFLICT (exchange_id, participant_id) DO NOTHING`, exchangeID, participantID, now); err != nil {
		return ReadReceipt{}, fmt.Errorf("relay store: record read: %w", err)
	}
	return s.GetRead(ctx, exchangeID, participantID)
}

// GetRead loads the read fact for one (exchange, participant) pair.
// ErrNotFound means retrieval has not yet been followed by an ack —
// retrieval is not acknowledgement.
func (s *Store) GetRead(ctx context.Context, exchangeID, participantID string) (ReadReceipt, error) {
	var receipt ReadReceipt
	err := s.db.QueryRowContext(ctx,
		`SELECT exchange_id, participant_id, acked_at FROM exchange_reads WHERE exchange_id = ? AND participant_id = ?`,
		exchangeID, participantID,
	).Scan(&receipt.ExchangeID, &receipt.ParticipantID, &receipt.AckedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ReadReceipt{}, ErrNotFound
	}
	if err != nil {
		return ReadReceipt{}, fmt.Errorf("relay store: load read receipt: %w", err)
	}
	return receipt, nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
