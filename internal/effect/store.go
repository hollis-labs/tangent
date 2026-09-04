package effect

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SQLStore is the durable handle and receipt table pair (migration 0009).
//
// It is deliberately a thin implementation of [Store] with no policy in it:
// every decision lives in [Broker], so a reader who wants to know why an
// effect was refused has one file to read rather than two.
type SQLStore struct {
	db  *sql.DB
	now func() time.Time
}

// NewSQLStore constructs a store over the shared Tangent handle.
func NewSQLStore(db *sql.DB) (*SQLStore, error) {
	if db == nil {
		return nil, errors.New("effect: database handle is required")
	}
	return &SQLStore{db: db, now: func() time.Time { return time.Now().UTC() }}, nil
}

// InsertHandle persists a freshly minted handle.
func (s *SQLStore) InsertHandle(ctx context.Context, handle Handle) error {
	capabilities, err := json.Marshal(FormatCapabilities(handle.Capabilities))
	if err != nil {
		return fmt.Errorf("effect: encode handle capabilities: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO effect_handles (
  id, class, root_id, relative_path, interaction_id, participant_scope,
  binding_digest, capabilities, issued_at, expires_at, max_uses, used
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		handle.ID, string(handle.Class), handle.RootID, handle.RelativePath,
		handle.InteractionID, handle.ParticipantScope, handle.BindingDigest,
		string(capabilities), handle.IssuedAt, handle.ExpiresAt, handle.MaxUses,
	)
	if err != nil {
		return fmt.Errorf("effect: insert handle: %w", err)
	}
	return nil
}

// LookupHandle loads one handle by id. An unknown id is [ErrHandleUnknown] —
// the same error every mismatch returns, so a refusal cannot enumerate.
func (s *SQLStore) LookupHandle(ctx context.Context, id string) (Handle, error) {
	var (
		handle          Handle
		class           string
		capabilitiesRaw string
		revokedAt       sql.NullTime
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, class, root_id, relative_path, interaction_id, participant_scope,
       binding_digest, capabilities, issued_at, expires_at, max_uses, used, revoked_at
FROM effect_handles WHERE id = ?`, id).Scan(
		&handle.ID, &class, &handle.RootID, &handle.RelativePath,
		&handle.InteractionID, &handle.ParticipantScope, &handle.BindingDigest,
		&capabilitiesRaw, &handle.IssuedAt, &handle.ExpiresAt,
		&handle.MaxUses, &handle.Used, &revokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Handle{}, ErrHandleUnknown
	}
	if err != nil {
		return Handle{}, fmt.Errorf("effect: load handle: %w", err)
	}
	handle.Class = HandleClass(class)
	if revokedAt.Valid {
		handle.RevokedAt = revokedAt.Time
	}
	var capabilities []string
	if err := json.Unmarshal([]byte(capabilitiesRaw), &capabilities); err != nil {
		return Handle{}, fmt.Errorf("effect: decode handle capabilities: %w", err)
	}
	handle.Capabilities = ParseCapabilities(capabilities)
	return handle, nil
}

// ConsumeHandleUse spends one unit of a handle's granted-use budget.
//
// The `used < max_uses` predicate is in the UPDATE rather than in Go: two
// concurrent requests against a single-use handle must not both find `used`
// at zero and both proceed. Zero rows affected means the budget was already
// spent, which is [ErrHandleExhausted] and not a database error.
func (s *SQLStore) ConsumeHandleUse(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE effect_handles SET used = used + 1
WHERE id = ? AND revoked_at IS NULL AND max_uses > 0 AND used < max_uses`, id)
	if err != nil {
		return fmt.Errorf("effect: consume handle use: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("effect: consume handle use: %w", err)
	}
	if affected == 0 {
		return ErrHandleExhausted
	}
	return nil
}

// RevokeHandle withdraws a handle before its expiry. It reports whether one
// was live.
func (s *SQLStore) RevokeHandle(ctx context.Context, id string) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE effect_handles SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		s.now(), id)
	if err != nil {
		return false, fmt.Errorf("effect: revoke handle: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("effect: revoke handle: %w", err)
	}
	return affected > 0, nil
}

// RevokeHandlesForInteraction withdraws every handle attached to one
// interaction. It is what a terminal transition calls: an interaction that has
// been resolved, canceled, or expired must not leave live grants behind.
func (s *SQLStore) RevokeHandlesForInteraction(ctx context.Context, interactionID string) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE effect_handles SET revoked_at = ? WHERE interaction_id = ? AND revoked_at IS NULL`,
		s.now(), interactionID)
	if err != nil {
		return 0, fmt.Errorf("effect: revoke interaction handles: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("effect: revoke interaction handles: %w", err)
	}
	return affected, nil
}

// RecordReceipt writes one immutable decision row.
func (s *SQLStore) RecordReceipt(ctx context.Context, receipt Receipt, requestDigest string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO effect_receipts (
  id, capability, decision, code, mediation, handle_id, interaction_id,
  participant_scope, binding_digest, idempotency_key, request_digest,
  intent_control_id, intent_revision, issued_at, byte_count, content_sha256, media_type
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		receipt.ID, string(receipt.Capability), string(receipt.Decision), receipt.Code,
		string(receipt.Mediation), receipt.HandleID, receipt.InteractionID,
		receipt.ParticipantScope, receipt.BindingDigest, receipt.IdempotencyKey, requestDigest,
		receipt.IntentControlID, receipt.IntentRevision, receipt.IssuedAt,
		receipt.Bytes, receipt.ContentSHA256, receipt.MediaType,
	)
	if err != nil {
		return fmt.Errorf("effect: record receipt: %w", err)
	}
	return nil
}

// ReceiptForKey returns the receipt already written under one idempotency key
// and capability, with the request fingerprint it was written for.
func (s *SQLStore) ReceiptForKey(
	ctx context.Context,
	key string,
	capability Capability,
) (Receipt, string, bool, error) {
	var (
		receipt       Receipt
		capabilityRaw string
		decision      string
		mediation     string
		requestDigest string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, capability, decision, code, mediation, handle_id, interaction_id,
       participant_scope, binding_digest, idempotency_key, request_digest,
       intent_control_id, intent_revision, issued_at, byte_count, content_sha256, media_type
FROM effect_receipts WHERE idempotency_key = ? AND capability = ?`,
		key, string(capability)).Scan(
		&receipt.ID, &capabilityRaw, &decision, &receipt.Code, &mediation,
		&receipt.HandleID, &receipt.InteractionID, &receipt.ParticipantScope,
		&receipt.BindingDigest, &receipt.IdempotencyKey, &requestDigest,
		&receipt.IntentControlID, &receipt.IntentRevision, &receipt.IssuedAt,
		&receipt.Bytes, &receipt.ContentSHA256, &receipt.MediaType,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Receipt{}, "", false, nil
	}
	if err != nil {
		return Receipt{}, "", false, fmt.Errorf("effect: load receipt: %w", err)
	}
	receipt.Capability = Capability(capabilityRaw)
	receipt.Decision = Decision(decision)
	receipt.Mediation = Mediation(mediation)
	return receipt, requestDigest, true, nil
}

// ExpireHandles deletes handles whose window closed before cutoff. Retention
// removes whole rows (ADR 0002 §6); nothing rewrites one.
func (s *SQLStore) ExpireHandles(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM effect_handles WHERE expires_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("effect: expire handles: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("effect: expire handles: %w", err)
	}
	return affected, nil
}
