// Package channel owns the durable channel, thread/subject, participant
// reference, membership, runtime-binding, and view-focus storage that
// implements docs/adr/0006-collaboration-surface-and-relay-boundary.md §3
// (CW-20260906-0064).
//
// It is intentionally a leaf storage package, shaped the way
// internal/interaction and internal/participant are: a concrete Store over
// the shared *sql.DB, no transport, no MCP surface. The personal-MVP plan's
// decision 2 builds the relay in Tangent core first and lifts it into
// go-app-agent later; keeping this package self-contained with a narrow,
// concrete API is what makes that lift mechanical rather than a rewrite.
//
// A room stays a compatibility projection of a surface (internal/room,
// ADR 0001 §2) and is never referenced here. A channel associates with the
// existing surface/interaction substrate through Subject; it does not
// replace, rename, or gain authority over it.
package channel

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

// Errors returned by Store.
var (
	ErrNotFound      = errors.New("channel store: record not found")
	ErrInvalidRecord = errors.New("channel store: invalid record")
)

// Store persists channels, subjects, participants, and their bindings.
type Store struct {
	db  *sql.DB
	now func() time.Time
	id  func() string
}

// NewStore constructs a Store over the shared Tangent handle.
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	return &Store{db: db, now: func() time.Time { return time.Now().UTC() }, id: uuid.NewString}, nil
}

// ── channels ────────────────────────────────────────────────────────────

// CreateChannelParams are the fields a caller supplies to open a channel.
type CreateChannelParams struct {
	Title      string
	OwnerScope string
	ProjectRef string
	Metadata   json.RawMessage
}

// CreateChannel opens a new channel.
func (s *Store) CreateChannel(ctx context.Context, params CreateChannelParams) (Channel, error) {
	if s == nil || s.db == nil {
		return Channel{}, fmt.Errorf("%w: nil store", ErrInvalidRecord)
	}
	if params.OwnerScope == "" {
		return Channel{}, fmt.Errorf("%w: owner scope is required", ErrInvalidRecord)
	}
	metadata, err := canonicalJSON(params.Metadata, "{}")
	if err != nil {
		return Channel{}, fmt.Errorf("%w: metadata: %w", ErrInvalidRecord, err)
	}
	now := s.now()
	ch := Channel{
		ID:         s.id(),
		Title:      params.Title,
		OwnerScope: params.OwnerScope,
		ProjectRef: params.ProjectRef,
		Metadata:   metadata,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO channels (id, title, owner_scope, project_ref, metadata, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ch.ID, ch.Title, ch.OwnerScope, ch.ProjectRef, string(ch.Metadata), ch.CreatedAt, ch.UpdatedAt,
	)
	if err != nil {
		return Channel{}, fmt.Errorf("channel store: insert channel: %w", err)
	}
	return ch, nil
}

// GetChannel loads one channel by id.
func (s *Store) GetChannel(ctx context.Context, id string) (Channel, error) {
	var (
		ch         Channel
		metadata   string
		archivedAt sql.NullTime
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, title, owner_scope, project_ref, metadata, created_at, updated_at, archived_at
FROM channels WHERE id = ?`, id).Scan(
		&ch.ID, &ch.Title, &ch.OwnerScope, &ch.ProjectRef, &metadata, &ch.CreatedAt, &ch.UpdatedAt, &archivedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	if err != nil {
		return Channel{}, fmt.Errorf("channel store: load channel: %w", err)
	}
	ch.Metadata = json.RawMessage(metadata)
	if archivedAt.Valid {
		t := archivedAt.Time
		ch.ArchivedAt = &t
	}
	return ch, nil
}

// ArchiveChannel marks a channel archived. Archiving is presentation only:
// it retains every subject, membership, and binding row (ARCHITECTURE.md §6,
// "Clear view / archive").
func (s *Store) ArchiveChannel(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE channels SET archived_at = ?, updated_at = ? WHERE id = ? AND archived_at IS NULL`,
		s.now(), s.now(), id)
	if err != nil {
		return fmt.Errorf("channel store: archive channel: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("channel store: archive channel: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ── subjects ────────────────────────────────────────────────────────────

// CreateSubjectParams are the fields a caller supplies to open a subject.
type CreateSubjectParams struct {
	ChannelID        string
	Type             SubjectType
	InteractionID    string
	SurfaceID        string
	ExternalRef      string
	Title            string
	WorkerProvenance json.RawMessage
}

// AddSubject opens a new thread/subject in a channel. A channel may hold
// several pending subjects at once; this never enforces a single-pending
// invariant the way a room's pending envelope does.
func (s *Store) AddSubject(ctx context.Context, params CreateSubjectParams) (Subject, error) {
	if s == nil || s.db == nil {
		return Subject{}, fmt.Errorf("%w: nil store", ErrInvalidRecord)
	}
	if params.ChannelID == "" {
		return Subject{}, fmt.Errorf("%w: channel id is required", ErrInvalidRecord)
	}
	switch params.Type {
	case SubjectInteraction:
		if params.InteractionID == "" {
			return Subject{}, fmt.Errorf("%w: interaction subject requires an interaction id", ErrInvalidRecord)
		}
		params.SurfaceID = ""
	case SubjectSurface:
		if params.SurfaceID == "" {
			return Subject{}, fmt.Errorf("%w: surface subject requires a surface id", ErrInvalidRecord)
		}
		params.InteractionID = ""
	case SubjectArtifact, SubjectFreeform:
		params.InteractionID = ""
		params.SurfaceID = ""
	default:
		return Subject{}, fmt.Errorf("%w: unknown subject type %q", ErrInvalidRecord, params.Type)
	}
	provenance, err := canonicalJSON(params.WorkerProvenance, "[]")
	if err != nil {
		return Subject{}, fmt.Errorf("%w: worker provenance: %w", ErrInvalidRecord, err)
	}
	now := s.now()
	subject := Subject{
		ID:               s.id(),
		ChannelID:        params.ChannelID,
		Type:             params.Type,
		InteractionID:    params.InteractionID,
		SurfaceID:        params.SurfaceID,
		ExternalRef:      params.ExternalRef,
		Title:            params.Title,
		Status:           SubjectPending,
		WorkerProvenance: provenance,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO channel_subjects (
  id, channel_id, subject_type, interaction_id, surface_id, external_ref,
  title, status, worker_provenance, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		subject.ID, subject.ChannelID, string(subject.Type),
		nullableText(subject.InteractionID), nullableText(subject.SurfaceID),
		subject.ExternalRef, subject.Title, string(subject.Status),
		string(subject.WorkerProvenance), subject.CreatedAt, subject.UpdatedAt,
	)
	if err != nil {
		return Subject{}, fmt.Errorf("channel store: insert subject: %w", err)
	}
	return subject, nil
}

// ListSubjects returns every subject in a channel, oldest first.
func (s *Store) ListSubjects(ctx context.Context, channelID string) ([]Subject, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, channel_id, subject_type, interaction_id, surface_id, external_ref,
       title, status, worker_provenance, created_at, updated_at
FROM channel_subjects WHERE channel_id = ? ORDER BY created_at, id`, channelID)
	if err != nil {
		return nil, fmt.Errorf("channel store: list subjects: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var subjects []Subject
	for rows.Next() {
		subject, scanErr := scanSubject(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("channel store: scan subject: %w", scanErr)
		}
		subjects = append(subjects, subject)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channel store: iterate subjects: %w", err)
	}
	return subjects, nil
}

// ResolveSubject marks a subject resolved. It never touches the interaction
// or surface it may correlate: resolving a subject is a channel-local
// projection, not a terminal outcome (ADR 0006 §3).
func (s *Store) ResolveSubject(ctx context.Context, id string) (Subject, error) {
	now := s.now()
	result, err := s.db.ExecContext(ctx,
		`UPDATE channel_subjects SET status = 'resolved', updated_at = ? WHERE id = ? AND status = 'pending'`,
		now, id)
	if err != nil {
		return Subject{}, fmt.Errorf("channel store: resolve subject: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Subject{}, fmt.Errorf("channel store: resolve subject: %w", err)
	}
	if affected == 0 {
		return Subject{}, ErrNotFound
	}
	return s.GetSubject(ctx, id)
}

// GetSubject loads one subject by id.
func (s *Store) GetSubject(ctx context.Context, id string) (Subject, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, channel_id, subject_type, interaction_id, surface_id, external_ref,
       title, status, worker_provenance, created_at, updated_at
FROM channel_subjects WHERE id = ?`, id)
	subject, err := scanSubject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Subject{}, ErrNotFound
	}
	if err != nil {
		return Subject{}, fmt.Errorf("channel store: load subject: %w", err)
	}
	return subject, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSubject(row rowScanner) (Subject, error) {
	var (
		subject       Subject
		subjectType   string
		interactionID sql.NullString
		surfaceID     sql.NullString
		status        string
		provenance    string
	)
	if err := row.Scan(
		&subject.ID, &subject.ChannelID, &subjectType, &interactionID, &surfaceID,
		&subject.ExternalRef, &subject.Title, &status, &provenance,
		&subject.CreatedAt, &subject.UpdatedAt,
	); err != nil {
		return Subject{}, err
	}
	subject.Type = SubjectType(subjectType)
	subject.Status = SubjectStatus(status)
	subject.InteractionID = interactionID.String
	subject.SurfaceID = surfaceID.String
	subject.WorkerProvenance = json.RawMessage(provenance)
	return subject, nil
}

// ── participants ────────────────────────────────────────────────────────

// UpsertParticipantParams are the fields a caller supplies to register or
// resolve a participant reference.
type UpsertParticipantParams struct {
	Kind              ParticipantKind
	ExternalAuthority string
	ExternalRef       string
	Label             string
	Metadata          json.RawMessage
}

// UpsertParticipant resolves a participant reference. When ExternalRef is
// non-empty and a participant already carries the same (ExternalAuthority,
// ExternalRef) pair, that existing row is returned unchanged — this is what
// lets a peer that re-registers on every launch still resolve to one stable
// participant across channels, rather than minting a new identity per run.
//
// The non-empty-ExternalRef path is one INSERT ... ON CONFLICT DO NOTHING
// statement against idx_participants_external_identity, not a lookup
// followed by a separate insert: a director review of CW-20260906-0064
// (PR #32) reproduced two concurrent registrations of the same identity —
// exactly the 0071/0072 scenario of two peers attaching at once — each
// observing "not found" and both attempting to insert, so the loser errored
// on the unique constraint instead of resolving to the winner. An UPSERT is
// one atomic statement from SQLite's point of view, so exactly one caller's
// insert lands and every other caller's affected-row count is zero, at
// which point it looks up the row it lost the race to.
func (s *Store) UpsertParticipant(ctx context.Context, params UpsertParticipantParams) (Participant, error) {
	if s == nil || s.db == nil {
		return Participant{}, fmt.Errorf("%w: nil store", ErrInvalidRecord)
	}
	switch params.Kind {
	case ParticipantOperator, ParticipantAgent:
	default:
		return Participant{}, fmt.Errorf("%w: unknown participant kind %q", ErrInvalidRecord, params.Kind)
	}
	metadata, err := canonicalJSON(params.Metadata, "{}")
	if err != nil {
		return Participant{}, fmt.Errorf("%w: metadata: %w", ErrInvalidRecord, err)
	}

	now := s.now()
	participant := Participant{
		ID:                s.id(),
		Kind:              params.Kind,
		ExternalAuthority: params.ExternalAuthority,
		ExternalRef:       params.ExternalRef,
		Label:             params.Label,
		Metadata:          metadata,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if params.ExternalRef == "" {
		// No stable external identity to dedupe against — every call mints a
		// fresh participant, same as an operator template always has.
		_, insertErr := s.db.ExecContext(ctx, `
INSERT INTO participants (id, kind, external_authority, external_ref, label, metadata, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			participant.ID, string(participant.Kind), participant.ExternalAuthority, participant.ExternalRef,
			participant.Label, string(participant.Metadata), participant.CreatedAt, participant.UpdatedAt,
		)
		if insertErr != nil {
			return Participant{}, fmt.Errorf("channel store: insert participant: %w", insertErr)
		}
		return participant, nil
	}

	result, err := s.db.ExecContext(ctx, `
INSERT INTO participants (id, kind, external_authority, external_ref, label, metadata, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (external_authority, external_ref) WHERE external_ref != '' DO NOTHING`,
		participant.ID, string(participant.Kind), participant.ExternalAuthority, participant.ExternalRef,
		participant.Label, string(participant.Metadata), participant.CreatedAt, participant.UpdatedAt,
	)
	if err != nil {
		return Participant{}, fmt.Errorf("channel store: insert participant: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Participant{}, fmt.Errorf("channel store: insert participant: %w", err)
	}
	if affected == 1 {
		return participant, nil
	}
	// Another caller's registration of the same identity won the race.
	existing, lookupErr := s.lookupParticipantByExternalIdentity(ctx, params.ExternalAuthority, params.ExternalRef)
	if lookupErr != nil {
		return Participant{}, fmt.Errorf("channel store: resolve conflicting participant: %w", lookupErr)
	}
	return existing, nil
}

func (s *Store) lookupParticipantByExternalIdentity(ctx context.Context, authority, ref string) (Participant, error) {
	var (
		participant Participant
		kind        string
		metadata    string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, kind, external_authority, external_ref, label, metadata, created_at, updated_at
FROM participants WHERE external_authority = ? AND external_ref = ?`, authority, ref).Scan(
		&participant.ID, &kind, &participant.ExternalAuthority, &participant.ExternalRef,
		&participant.Label, &metadata, &participant.CreatedAt, &participant.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Participant{}, ErrNotFound
	}
	if err != nil {
		return Participant{}, fmt.Errorf("channel store: lookup participant: %w", err)
	}
	participant.Kind = ParticipantKind(kind)
	participant.Metadata = json.RawMessage(metadata)
	return participant, nil
}

// GetParticipant loads one participant by id.
func (s *Store) GetParticipant(ctx context.Context, id string) (Participant, error) {
	var (
		participant Participant
		kind        string
		metadata    string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, kind, external_authority, external_ref, label, metadata, created_at, updated_at
FROM participants WHERE id = ?`, id).Scan(
		&participant.ID, &kind, &participant.ExternalAuthority, &participant.ExternalRef,
		&participant.Label, &metadata, &participant.CreatedAt, &participant.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Participant{}, ErrNotFound
	}
	if err != nil {
		return Participant{}, fmt.Errorf("channel store: load participant: %w", err)
	}
	participant.Kind = ParticipantKind(kind)
	participant.Metadata = json.RawMessage(metadata)
	return participant, nil
}

// ── membership ──────────────────────────────────────────────────────────

// AddParticipant binds a participant to a channel. It is idempotent: adding
// a participant already bound (and not removed) returns the existing
// membership rather than erroring, because a peer that reconnects should not
// have to check membership before it can be sure it is bound.
func (s *Store) AddParticipant(ctx context.Context, channelID, participantID string) (Membership, error) {
	now := s.now()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO channel_participants (channel_id, participant_id, joined_at)
VALUES (?, ?, ?)
ON CONFLICT (channel_id, participant_id) DO UPDATE SET left_at = NULL`,
		channelID, participantID, now)
	if err != nil {
		return Membership{}, fmt.Errorf("channel store: add participant: %w", err)
	}
	return s.getMembership(ctx, channelID, participantID)
}

// RemoveParticipant ends a participant's membership without deleting the
// row, so binding history (channel_participant_bindings) keeps its parent.
//
// Undecided, flagged by a director review of CW-20260906-0064 (PR #32) and
// left open on purpose rather than fixed in that PR: this does not supersede
// the participant's current runtime binding. CurrentBinding keeps resolving
// a destination for a participant who has left, and a leave-then-rejoin
// resumes on whatever binding was current before — a caller has to make an
// explicit Rebind call to know it is not routing to a dead session, rather
// than being told so by CurrentBinding returning ErrNotFound. That is the
// same failure ADR 0006 §3's explicit-rebind rule exists to prevent for a
// live member ("no automatic choice of the newest session sharing a name"),
// just reached through membership instead of through a stale generation.
// The two readings — leaving a channel implicitly supersedes the binding
// (recommended: membership and routing stay consistent, and 0066/0072 never
// have to remember to check both), versus a live binding for a
// non-member is fine because 0066's routing path is expected to consult
// ListChannelParticipants/getMembership before ever calling CurrentBinding —
// have not been chosen between. Whichever CW-20260906-0066 or 0072 decides,
// record it here.
func (s *Store) RemoveParticipant(ctx context.Context, channelID, participantID string) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE channel_participants SET left_at = ?
WHERE channel_id = ? AND participant_id = ? AND left_at IS NULL`,
		s.now(), channelID, participantID)
	if err != nil {
		return fmt.Errorf("channel store: remove participant: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("channel store: remove participant: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) getMembership(ctx context.Context, channelID, participantID string) (Membership, error) {
	var (
		membership Membership
		leftAt     sql.NullTime
	)
	err := s.db.QueryRowContext(ctx, `
SELECT channel_id, participant_id, joined_at, left_at
FROM channel_participants WHERE channel_id = ? AND participant_id = ?`, channelID, participantID).Scan(
		&membership.ChannelID, &membership.ParticipantID, &membership.JoinedAt, &leftAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("channel store: load membership: %w", err)
	}
	if leftAt.Valid {
		t := leftAt.Time
		membership.LeftAt = &t
	}
	return membership, nil
}

// IsMember reports whether a participant currently holds live membership
// (left_at IS NULL) in a channel. It exists for callers one layer up — the
// relay journal validates sender/recipient membership before accepting an
// exchange — that need the yes/no answer without loading the full
// Membership record.
func (s *Store) IsMember(ctx context.Context, channelID, participantID string) (bool, error) {
	_, err := s.getMembership(ctx, channelID, participantID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ListChannelParticipants returns every participant currently bound to a
// channel (left_at IS NULL), in join order.
func (s *Store) ListChannelParticipants(ctx context.Context, channelID string) ([]Participant, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT p.id, p.kind, p.external_authority, p.external_ref, p.label, p.metadata, p.created_at, p.updated_at
FROM participants p
JOIN channel_participants cp ON cp.participant_id = p.id
WHERE cp.channel_id = ? AND cp.left_at IS NULL
ORDER BY cp.joined_at, p.id`, channelID)
	if err != nil {
		return nil, fmt.Errorf("channel store: list channel participants: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var participants []Participant
	for rows.Next() {
		var (
			participant Participant
			kind        string
			metadata    string
		)
		if err := rows.Scan(&participant.ID, &kind, &participant.ExternalAuthority, &participant.ExternalRef,
			&participant.Label, &metadata, &participant.CreatedAt, &participant.UpdatedAt); err != nil {
			return nil, fmt.Errorf("channel store: scan participant: %w", err)
		}
		participant.Kind = ParticipantKind(kind)
		participant.Metadata = json.RawMessage(metadata)
		participants = append(participants, participant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channel store: iterate channel participants: %w", err)
	}
	return participants, nil
}

// ListParticipantChannels returns every channel a participant currently
// holds live membership in (left_at IS NULL) — the proof that one
// participant reference can span several channels.
func (s *Store) ListParticipantChannels(ctx context.Context, participantID string) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT c.id, c.title, c.owner_scope, c.project_ref, c.metadata, c.created_at, c.updated_at, c.archived_at
FROM channels c
JOIN channel_participants cp ON cp.channel_id = c.id
WHERE cp.participant_id = ? AND cp.left_at IS NULL
ORDER BY cp.joined_at, c.id`, participantID)
	if err != nil {
		return nil, fmt.Errorf("channel store: list participant channels: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var channels []Channel
	for rows.Next() {
		var (
			ch         Channel
			metadata   string
			archivedAt sql.NullTime
		)
		if err := rows.Scan(&ch.ID, &ch.Title, &ch.OwnerScope, &ch.ProjectRef, &metadata,
			&ch.CreatedAt, &ch.UpdatedAt, &archivedAt); err != nil {
			return nil, fmt.Errorf("channel store: scan channel: %w", err)
		}
		ch.Metadata = json.RawMessage(metadata)
		if archivedAt.Valid {
			t := archivedAt.Time
			ch.ArchivedAt = &t
		}
		channels = append(channels, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channel store: iterate participant channels: %w", err)
	}
	return channels, nil
}

// ── runtime bindings ────────────────────────────────────────────────────

// RebindParams are the fields a caller supplies to bind or rebind a
// participant's runtime destination.
type RebindParams struct {
	RuntimeAuthority    string
	RuntimeEndpointRef  string
	AdapterCapabilities json.RawMessage
}

// Rebind explicitly binds a channel participant to a runtime destination,
// advancing the generation. The previous current binding, if any, is marked
// superseded in the same transaction — there is never more than one current
// binding for a (channel, participant) pair, and rebinding never happens
// automatically.
func (s *Store) Rebind(ctx context.Context, channelID, participantID string, params RebindParams) (RuntimeBinding, error) {
	if params.RuntimeAuthority == "" {
		return RuntimeBinding{}, fmt.Errorf("%w: runtime authority is required", ErrInvalidRecord)
	}
	capabilities, err := canonicalJSON(params.AdapterCapabilities, "[]")
	if err != nil {
		return RuntimeBinding{}, fmt.Errorf("%w: adapter capabilities: %w", ErrInvalidRecord, err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RuntimeBinding{}, fmt.Errorf("channel store: begin rebind: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var previousGeneration int64
	err = tx.QueryRowContext(ctx, `
SELECT generation FROM channel_participant_bindings
WHERE channel_id = ? AND participant_id = ? AND superseded_at IS NULL`, channelID, participantID).
		Scan(&previousGeneration)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		previousGeneration = 0
	case err != nil:
		return RuntimeBinding{}, fmt.Errorf("channel store: read current binding: %w", err)
	default:
		now := s.now()
		if _, updateErr := tx.ExecContext(ctx, `
UPDATE channel_participant_bindings SET superseded_at = ?
WHERE channel_id = ? AND participant_id = ? AND superseded_at IS NULL`,
			now, channelID, participantID); updateErr != nil {
			return RuntimeBinding{}, fmt.Errorf("channel store: supersede binding: %w", updateErr)
		}
	}

	now := s.now()
	binding := RuntimeBinding{
		ID:                  s.id(),
		ChannelID:           channelID,
		ParticipantID:       participantID,
		Generation:          previousGeneration + 1,
		RuntimeAuthority:    params.RuntimeAuthority,
		RuntimeEndpointRef:  params.RuntimeEndpointRef,
		AdapterCapabilities: capabilities,
		BoundAt:             now,
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO channel_participant_bindings (
  id, channel_id, participant_id, generation, runtime_authority,
  runtime_endpoint_ref, adapter_capabilities, bound_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		binding.ID, binding.ChannelID, binding.ParticipantID, binding.Generation,
		binding.RuntimeAuthority, binding.RuntimeEndpointRef, string(binding.AdapterCapabilities), binding.BoundAt,
	)
	if err != nil {
		return RuntimeBinding{}, fmt.Errorf("channel store: insert binding: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RuntimeBinding{}, fmt.Errorf("channel store: commit rebind: %w", err)
	}
	return binding, nil
}

// CurrentBinding returns the live runtime binding for a channel participant.
func (s *Store) CurrentBinding(ctx context.Context, channelID, participantID string) (RuntimeBinding, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, channel_id, participant_id, generation, runtime_authority,
       runtime_endpoint_ref, adapter_capabilities, bound_at, superseded_at
FROM channel_participant_bindings
WHERE channel_id = ? AND participant_id = ? AND superseded_at IS NULL`, channelID, participantID)
	binding, err := scanBinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeBinding{}, ErrNotFound
	}
	if err != nil {
		return RuntimeBinding{}, fmt.Errorf("channel store: load current binding: %w", err)
	}
	return binding, nil
}

// BindingHistory returns every generation bound for a channel participant,
// oldest first.
func (s *Store) BindingHistory(ctx context.Context, channelID, participantID string) ([]RuntimeBinding, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, channel_id, participant_id, generation, runtime_authority,
       runtime_endpoint_ref, adapter_capabilities, bound_at, superseded_at
FROM channel_participant_bindings
WHERE channel_id = ? AND participant_id = ?
ORDER BY generation`, channelID, participantID)
	if err != nil {
		return nil, fmt.Errorf("channel store: list binding history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var bindings []RuntimeBinding
	for rows.Next() {
		binding, scanErr := scanBinding(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("channel store: scan binding: %w", scanErr)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channel store: iterate binding history: %w", err)
	}
	return bindings, nil
}

func scanBinding(row rowScanner) (RuntimeBinding, error) {
	var (
		binding      RuntimeBinding
		capabilities string
		supersededAt sql.NullTime
	)
	if err := row.Scan(
		&binding.ID, &binding.ChannelID, &binding.ParticipantID, &binding.Generation,
		&binding.RuntimeAuthority, &binding.RuntimeEndpointRef, &capabilities,
		&binding.BoundAt, &supersededAt,
	); err != nil {
		return RuntimeBinding{}, err
	}
	binding.AdapterCapabilities = json.RawMessage(capabilities)
	if supersededAt.Valid {
		t := supersededAt.Time
		binding.SupersededAt = &t
	}
	return binding, nil
}

// ── view focus ──────────────────────────────────────────────────────────

// SetViewFocusParams are the fields a caller supplies to update one view's
// focus inside a channel. Empty strings clear the corresponding pointer.
type SetViewFocusParams struct {
	ChannelID            string
	ViewRef              string
	FocusedSubjectID     string
	FocusedParticipantID string
}

// SetViewFocus upserts one view's focus. Views are independent by
// construction: this only ever touches the row for (ChannelID, ViewRef), so
// setting one view's focus never moves another view's focus in the same
// channel.
func (s *Store) SetViewFocus(ctx context.Context, params SetViewFocusParams) (ViewFocus, error) {
	if params.ChannelID == "" || params.ViewRef == "" {
		return ViewFocus{}, fmt.Errorf("%w: channel id and view ref are required", ErrInvalidRecord)
	}
	now := s.now()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO channel_view_focus (channel_id, view_ref, focused_subject_id, focused_participant_id, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (channel_id, view_ref) DO UPDATE SET
  focused_subject_id = excluded.focused_subject_id,
  focused_participant_id = excluded.focused_participant_id,
  updated_at = excluded.updated_at`,
		params.ChannelID, params.ViewRef,
		nullableText(params.FocusedSubjectID), nullableText(params.FocusedParticipantID), now,
	)
	if err != nil {
		return ViewFocus{}, fmt.Errorf("channel store: set view focus: %w", err)
	}
	return s.GetViewFocus(ctx, params.ChannelID, params.ViewRef)
}

// GetViewFocus loads one view's current focus.
func (s *Store) GetViewFocus(ctx context.Context, channelID, viewRef string) (ViewFocus, error) {
	var (
		focus       ViewFocus
		subjectID   sql.NullString
		recipientID sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
SELECT channel_id, view_ref, focused_subject_id, focused_participant_id, updated_at
FROM channel_view_focus WHERE channel_id = ? AND view_ref = ?`, channelID, viewRef).Scan(
		&focus.ChannelID, &focus.ViewRef, &subjectID, &recipientID, &focus.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ViewFocus{}, ErrNotFound
	}
	if err != nil {
		return ViewFocus{}, fmt.Errorf("channel store: load view focus: %w", err)
	}
	focus.FocusedSubjectID = subjectID.String
	focus.FocusedParticipantID = recipientID.String
	return focus, nil
}

// ── helpers ─────────────────────────────────────────────────────────────

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// canonicalJSON validates a caller-supplied JSON document, falling back to a
// default when none was supplied. It mirrors internal/interaction's helper
// of the same name and purpose: reject anything that is not exactly one JSON
// value, so a metadata or provenance column never silently accepts garbage
// past the json_valid CHECK the migration already enforces.
func canonicalJSON(raw json.RawMessage, fallback string) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(fallback)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return raw, nil
}
