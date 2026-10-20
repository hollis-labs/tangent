package participant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/tangent/internal/authz"
)

// Store is the durable participant-session table.
//
// Sessions live in SQLite and survive a restart: losing every session on
// restart is a worse ergonomic than the retention obligation of a table, and
// it would contradict the durable direction ADR 0001 set.
type Store struct {
	db  *sql.DB
	now func() time.Time
	// elevation is how long an elevation lasts; nil means ElevationAlways.
	elevation atomic.Pointer[ElevationPolicy]
}

// NewStore constructs a Store over the shared Tangent handle.
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("participant: database handle is required")
	}
	return &Store{db: db, now: func() time.Time { return time.Now().UTC() }}, nil
}

// SetElevationPolicy installs the elevation lifetime. Call it before the store
// serves requests; it is safe to call later (a change applies to the next
// read).
func (s *Store) SetElevationPolicy(p ElevationPolicy) { s.elevation.Store(&p) }

// ElevationPolicy is the lifetime currently applied to elevations.
func (s *Store) ElevationPolicy() ElevationPolicy {
	if p := s.elevation.Load(); p != nil {
		return *p
	}
	return ElevationAlways()
}

// Mint creates a session from a template and returns it with the one and only
// copy of its cookie value. Nothing retains the value: the row holds a
// SHA-256, and the caller's next act is to write the Set-Cookie header.
func (s *Store) Mint(ctx context.Context, template Session) (Session, string, error) {
	value, hash, err := newCookieValue()
	if err != nil {
		return Session{}, "", err
	}
	now := s.now()
	session := Session{
		ID:         uuid.NewString(),
		Scope:      template.Scope,
		Ref:        template.Ref,
		Authority:  template.Authority,
		Assurance:  template.Assurance,
		Grants:     append([]authz.Capability(nil), template.Grants...),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(MaximumLifetime),
	}
	if err := s.insert(ctx, s.db, session, hash); err != nil {
		return Session{}, "", err
	}
	return session, value, nil
}

func (s *Store) insert(ctx context.Context, exec sqlExecutor, session Session, hash string) error {
	grants, err := json.Marshal(authz.FormatCapabilities(session.Grants))
	if err != nil {
		return fmt.Errorf("participant: encode capabilities: %w", err)
	}
	_, err = exec.ExecContext(ctx, `
INSERT INTO participant_sessions (
  id, cookie_sha256, participant_scope, participant_ref,
  participant_authority, participant_assurance, capabilities,
  created_at, last_seen_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, hash, session.Scope, session.Ref,
		session.Authority, session.Assurance, string(grants),
		session.CreatedAt, session.LastSeenAt, session.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("participant: insert session: %w", err)
	}
	return nil
}

// Lookup resolves a cookie value to its live session.
//
// A session that is unknown, revoked, or past its absolute expiry resolves to
// ErrSessionInvalid — one refusal for three causes, so a probe cannot tell
// them apart.
func (s *Store) Lookup(ctx context.Context, value string) (Session, error) {
	if value == "" {
		return Session{}, ErrNoSession
	}
	hash := HashCookie(value)
	session, storedHash, err := s.load(ctx, `cookie_sha256 = ?`, hash)
	if err != nil {
		return Session{}, err
	}
	if !constantTimeEqual(storedHash, hash) {
		return Session{}, ErrSessionInvalid
	}
	return session, nil
}

// LookupByID re-reads a live session by its row id. It is the cheap primary
// key read a long-lived transport uses to notice that the session behind it
// was revoked, expired, or lost its elevation (CW-20261016-0174).
func (s *Store) LookupByID(ctx context.Context, sessionID string) (Session, error) {
	if sessionID == "" {
		return Session{}, ErrSessionInvalid
	}
	session, _, err := s.load(ctx, `id = ?`, sessionID)
	return session, err
}

// load reads one session row by a fixed predicate, refusing a revoked or
// expired one, and applies the elevation window.
func (s *Store) load(ctx context.Context, predicate string, arg string) (Session, string, error) {
	var (
		session    Session
		storedHash string
		grantsJSON string
		revokedAt  sql.NullTime
	)
	//nolint:gosec // G202: predicate is one of two fixed literals in this file.
	err := s.db.QueryRowContext(ctx, `
SELECT id, cookie_sha256, participant_scope, participant_ref,
       participant_authority, participant_assurance, capabilities,
       created_at, last_seen_at, expires_at, revoked_at
FROM participant_sessions
WHERE `+predicate, arg).Scan(
		&session.ID, &storedHash, &session.Scope, &session.Ref,
		&session.Authority, &session.Assurance, &grantsJSON,
		&session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt, &revokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, "", ErrSessionInvalid
	}
	if err != nil {
		return Session{}, "", fmt.Errorf("participant: load session: %w", err)
	}
	now := s.now()
	if revokedAt.Valid || !now.Before(session.ExpiresAt) {
		return Session{}, "", ErrSessionInvalid
	}
	var granted []string
	if err := json.Unmarshal([]byte(grantsJSON), &granted); err != nil {
		return Session{}, "", fmt.Errorf("participant: decode capabilities: %w", err)
	}
	session.Grants = authz.ParseCapabilities(granted)
	return applyElevationWindow(session, now, s.ElevationPolicy()), storedHash, nil
}

// Touch records that a session was used. It moves last_seen_at only: there is
// no idle expiry, so the timestamp is an audit fact rather than a deadline.
func (s *Store) Touch(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE participant_sessions SET last_seen_at = ? WHERE id = ? AND revoked_at IS NULL`,
		s.now(), sessionID)
	if err != nil {
		return fmt.Errorf("participant: touch session: %w", err)
	}
	return nil
}

// Rotate revokes a session and issues its successor in one transaction.
//
// This is the ADR 0004 §10.2 composition point: when an identity authority
// binds a verified principal to an existing session, the old id must not
// survive the assurance change. The successor carries the new binding and the
// capability set that authority granted; the predecessor records where it went
// so the audit trail crosses the rotation.
func (s *Store) Rotate(ctx context.Context, sessionID string, next Session) (Session, string, error) {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, "", fmt.Errorf("participant: begin rotation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	value, hash, err := newCookieValue()
	if err != nil {
		return Session{}, "", err
	}
	now := s.now()
	// The successor inherits the predecessor's absolute expiry: an assurance
	// change is not a reason to extend a session's life, and an elevation in
	// particular must not refresh the 30-day clock (CW-20261016-0174).
	var predecessorExpiry time.Time
	switch scanErr := transaction.QueryRowContext(ctx,
		`SELECT expires_at FROM participant_sessions WHERE id = ? AND revoked_at IS NULL`, sessionID,
	).Scan(&predecessorExpiry); {
	case errors.Is(scanErr, sql.ErrNoRows):
		return Session{}, "", ErrSessionInvalid
	case scanErr != nil:
		return Session{}, "", fmt.Errorf("participant: load predecessor: %w", scanErr)
	}
	if !now.Before(predecessorExpiry) {
		return Session{}, "", ErrSessionInvalid
	}
	successor := Session{
		ID:         uuid.NewString(),
		Scope:      next.Scope,
		Ref:        next.Ref,
		Authority:  next.Authority,
		Assurance:  next.Assurance,
		Grants:     append([]authz.Capability(nil), next.Grants...),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  predecessorExpiry,
	}
	if insertErr := s.insert(ctx, transaction, successor, hash); insertErr != nil {
		return Session{}, "", insertErr
	}
	result, err := transaction.ExecContext(ctx, `
UPDATE participant_sessions
SET revoked_at = ?, revoked_reason = 'assurance-change', rotated_to = ?
WHERE id = ? AND revoked_at IS NULL`, now, successor.ID, sessionID)
	if err != nil {
		return Session{}, "", fmt.Errorf("participant: revoke predecessor: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Session{}, "", fmt.Errorf("participant: revoke predecessor: %w", err)
	}
	if affected == 0 {
		return Session{}, "", ErrSessionInvalid
	}
	if err := transaction.Commit(); err != nil {
		return Session{}, "", fmt.Errorf("participant: commit rotation: %w", err)
	}
	return successor, value, nil
}

// Revoke ends one session by id.
func (s *Store) Revoke(ctx context.Context, sessionID, reason string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE participant_sessions SET revoked_at = ?, revoked_reason = ?
WHERE id = ? AND revoked_at IS NULL`, s.now(), reason, sessionID)
	if err != nil {
		return false, fmt.Errorf("participant: revoke session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("participant: revoke session: %w", err)
	}
	return affected > 0, nil
}

// RevokeAll ends every live session. It backs the CLI revocation flag, which
// is deliberately the only way to revoke: a UI control on a single-user
// loopback tool would be a foot-gun with no threat behind it.
func (s *Store) RevokeAll(ctx context.Context, reason string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE participant_sessions SET revoked_at = ?, revoked_reason = ?
WHERE revoked_at IS NULL`, s.now(), reason)
	if err != nil {
		return 0, fmt.Errorf("participant: revoke sessions: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("participant: revoke sessions: %w", err)
	}
	return affected, nil
}

// MinRevokePrefixLength is the shortest session-id prefix RevokeByIDPrefix
// accepts. Eight hex digits is the first group of a UUID: short enough to
// type, long enough that it does not sweep up sessions by accident.
const MinRevokePrefixLength = 8

// ErrInvalidPrefix means a revocation prefix was too short or not hex.
var ErrInvalidPrefix = errors.New("participant: session id prefix must be at least 8 hex digits")

// ValidRevokePrefix reports whether prefix is at least MinRevokePrefixLength
// characters of lowercase hex (UUID hyphens allowed), so it can never carry a
// LIKE wildcard or sweep up sessions by accident.
func ValidRevokePrefix(prefix string) bool {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if len(prefix) < MinRevokePrefixLength {
		return false
	}
	for _, r := range prefix {
		isDigit := r >= '0' && r <= '9'
		isHexLetter := r >= 'a' && r <= 'f'
		if !isDigit && !isHexLetter && r != '-' {
			return false
		}
	}
	return true
}

// RevokeByIDPrefix ends every live session whose id starts with prefix, and
// returns how many it ended. The prefix is validated as lowercase hex (with
// the UUID hyphens allowed) so it can never carry a LIKE wildcard.
func (s *Store) RevokeByIDPrefix(ctx context.Context, prefix, reason string) (int64, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if !ValidRevokePrefix(prefix) {
		return 0, ErrInvalidPrefix
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE participant_sessions SET revoked_at = ?, revoked_reason = ?
WHERE revoked_at IS NULL AND substr(id, 1, ?) = ?`, s.now(), reason, len(prefix), prefix)
	if err != nil {
		return 0, fmt.Errorf("participant: revoke sessions: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("participant: revoke sessions: %w", err)
	}
	return affected, nil
}

// SessionSummary is the non-secret description of one session row, for the
// operator CLI. It carries no cookie hash.
type SessionSummary struct {
	ID         string
	Assurance  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	Revoked    bool
}

// List returns every session row, newest first, without capability material.
func (s *Store) List(ctx context.Context) ([]SessionSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, participant_assurance, created_at, last_seen_at, expires_at, revoked_at
FROM participant_sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("participant: list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SessionSummary
	for rows.Next() {
		var (
			summary   SessionSummary
			revokedAt sql.NullTime
		)
		if err := rows.Scan(&summary.ID, &summary.Assurance, &summary.CreatedAt,
			&summary.LastSeenAt, &summary.ExpiresAt, &revokedAt); err != nil {
			return nil, fmt.Errorf("participant: list sessions: %w", err)
		}
		summary.Revoked = revokedAt.Valid
		out = append(out, summary)
	}
	return out, rows.Err()
}

// sqlExecutor is the narrow slice of *sql.DB and *sql.Tx insert needs, so a
// mint and a rotation share one statement.
type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}
