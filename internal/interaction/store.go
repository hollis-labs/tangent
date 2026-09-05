package interaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound            = errors.New("interaction store: record not found")
	ErrIdempotencyConflict = errors.New("interaction store: idempotency key reused with different request")
	ErrRevisionConflict    = errors.New("interaction store: revision conflict")
	ErrNotRespondable      = errors.New("interaction store: interaction is not respondable")
	ErrTerminal            = errors.New("interaction store: terminal record is immutable")
	ErrInvalidRecord       = errors.New("interaction store: invalid record")
)

// Store persists canonical records without imposing transport behavior.
type Store struct {
	db  *sql.DB
	now func() time.Time
	id  func() string
}

func NewStore(db *sql.DB) *Store {
	return &Store{
		db:  db,
		now: func() time.Time { return time.Now().UTC() },
		id:  uuid.NewString,
	}
}

type CreateSurfaceParams struct {
	ID         string
	OwnerScope string
	Metadata   json.RawMessage
	Policy     json.RawMessage
	ActorRef   string
	Authority  string
}

func (s *Store) CreateSurface(ctx context.Context, params CreateSurfaceParams) (SurfaceRecord, error) {
	if s == nil || s.db == nil {
		return SurfaceRecord{}, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	if params.OwnerScope == "" {
		return SurfaceRecord{}, fmt.Errorf("%w: owner scope is required", ErrInvalidRecord)
	}
	metadata, metadataErr := canonicalJSON(params.Metadata, "{}")
	if metadataErr != nil {
		return SurfaceRecord{}, fmt.Errorf("%w: metadata: %w", ErrInvalidRecord, metadataErr)
	}
	policy, policyErr := canonicalJSON(params.Policy, "{}")
	if policyErr != nil {
		return SurfaceRecord{}, fmt.Errorf("%w: policy: %w", ErrInvalidRecord, policyErr)
	}
	id := params.ID
	if id == "" {
		id = s.id()
	}
	now := s.now()
	tx, beginErr := s.db.BeginTx(ctx, nil)
	if beginErr != nil {
		return SurfaceRecord{}, fmt.Errorf("begin create surface: %w", beginErr)
	}
	defer rollback(tx)

	if _, err := tx.ExecContext(ctx, `
INSERT INTO surfaces (
  id, owner_scope, lifecycle_state, metadata, policy,
  next_interaction_sequence, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 1, 1, ?, ?)`,
		id, params.OwnerScope, SurfaceStateCreated, string(metadata), string(policy), now, now,
	); err != nil {
		return SurfaceRecord{}, fmt.Errorf("insert surface: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO surface_events (
  event_id, surface_id, event_type, actor_ref, authority,
  from_revision, to_revision, metadata, recorded_at
) VALUES (?, ?, 'surface.created', ?, ?, NULL, 1, '{}', ?)`,
		s.id(), id, nullString(params.ActorRef), nullString(params.Authority), now,
	); err != nil {
		return SurfaceRecord{}, fmt.Errorf("insert surface event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SurfaceRecord{}, fmt.Errorf("commit create surface: %w", err)
	}
	return s.GetSurface(ctx, id)
}

func (s *Store) GetSurface(ctx context.Context, id string) (SurfaceRecord, error) {
	return getSurface(ctx, s.db, id)
}

type AdvanceSurfaceParams struct {
	SurfaceID        string
	ExpectedRevision int64
	To               SurfaceState
	ActorRef         string
	Authority        string
	Metadata         json.RawMessage
}

// AdvanceSurface records the nonterminal initialization/suspension lifecycle.
// Terminal close/expiry requires dispositioning every outstanding interaction
// in the same application-service transaction and is intentionally separate.
func (s *Store) AdvanceSurface(ctx context.Context, params AdvanceSurfaceParams) (SurfaceRecord, error) {
	if params.SurfaceID == "" || params.ExpectedRevision < 1 {
		return SurfaceRecord{}, fmt.Errorf("%w: surface and expected revision are required", ErrInvalidRecord)
	}
	metadata, metadataErr := canonicalJSON(params.Metadata, "{}")
	if metadataErr != nil {
		return SurfaceRecord{}, fmt.Errorf("%w: event metadata: %w", ErrInvalidRecord, metadataErr)
	}
	tx, beginErr := s.db.BeginTx(ctx, nil)
	if beginErr != nil {
		return SurfaceRecord{}, fmt.Errorf("begin advance surface: %w", beginErr)
	}
	defer rollback(tx)
	current, loadErr := scanSurface(tx.QueryRowContext(ctx, `
SELECT id, owner_scope, lifecycle_state, metadata, policy,
       next_interaction_sequence, revision, created_at, updated_at,
       closed_at, close_reason, legacy_room_id
FROM surfaces WHERE id = ?`, params.SurfaceID))
	if loadErr != nil {
		return SurfaceRecord{}, loadErr
	}
	if current.Revision != params.ExpectedRevision {
		return SurfaceRecord{}, ErrRevisionConflict
	}
	if !canAdvanceSurface(current.State, params.To) {
		if current.State == SurfaceStateClosed || current.State == SurfaceStateExpired {
			return SurfaceRecord{}, ErrTerminal
		}
		return SurfaceRecord{}, ErrNotRespondable
	}
	now := s.now()
	result, updateErr := tx.ExecContext(ctx, `
UPDATE surfaces
SET lifecycle_state = ?, revision = revision + 1, updated_at = ?
WHERE id = ? AND revision = ?`, params.To, now, params.SurfaceID, params.ExpectedRevision)
	if updateErr != nil {
		return SurfaceRecord{}, fmt.Errorf("advance surface: %w", updateErr)
	}
	if !changedOne(result) {
		return SurfaceRecord{}, ErrRevisionConflict
	}
	eventType := "surface.activated"
	if params.To == SurfaceStateSuspended {
		eventType = "surface.suspended"
	} else if current.State == SurfaceStateSuspended {
		eventType = "surface.resumed"
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO surface_events (
  event_id, surface_id, event_type, actor_ref, authority,
  from_revision, to_revision, metadata, recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.id(), current.ID, eventType, nullString(params.ActorRef), nullString(params.Authority),
		params.ExpectedRevision, params.ExpectedRevision+1, string(metadata), now,
	); err != nil {
		return SurfaceRecord{}, fmt.Errorf("insert surface transition event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SurfaceRecord{}, fmt.Errorf("commit advance surface: %w", err)
	}
	return s.GetSurface(ctx, params.SurfaceID)
}

type CreateInteractionParams struct {
	ID                 string
	SurfaceID          string
	CallerScope        string
	CallerPrincipalRef string
	CallerAuthority    string
	CallerAssurance    string
	IdempotencyKey     string
	Definition         DefinitionBinding
	RequestSnapshot    json.RawMessage
	ExternalRefs       json.RawMessage
	Policy             json.RawMessage
	ActorRef           string
	Authority          string

	// LegacyRoomID / LegacyEnvelopeID bind a canonical interaction to the
	// v0.12 room projection it is presented through. They are correlation
	// only: the interaction record remains the sole lifecycle authority and
	// the legacy rows are derived from it.
	LegacyRoomID     string
	LegacyEnvelopeID string
}

type CreateInteractionResult struct {
	Interaction InteractionRecord
	Created     bool
}

// CreateInteraction atomically stores a request, pins its definition, records
// submitted/validated/staged events, and allocates its stable surface order.
func (s *Store) CreateInteraction(ctx context.Context, params CreateInteractionParams) (CreateInteractionResult, error) {
	if s == nil || s.db == nil {
		return CreateInteractionResult{}, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	if params.SurfaceID == "" || params.CallerScope == "" || params.IdempotencyKey == "" {
		return CreateInteractionResult{}, fmt.Errorf("%w: surface, caller scope, and idempotency key are required", ErrInvalidRecord)
	}
	request, requestErr := canonicalJSON(params.RequestSnapshot, "")
	if requestErr != nil {
		return CreateInteractionResult{}, fmt.Errorf("%w: request snapshot: %w", ErrInvalidRecord, requestErr)
	}
	params.RequestSnapshot = request

	tx, beginErr := s.db.BeginTx(ctx, nil)
	if beginErr != nil {
		return CreateInteractionResult{}, fmt.Errorf("begin create interaction: %w", beginErr)
	}
	defer rollback(tx)

	existing, found, lookupErr := getInteractionByIdempotencyTx(ctx, tx, params.CallerScope, params.IdempotencyKey)
	if lookupErr != nil {
		return CreateInteractionResult{}, lookupErr
	}
	if found {
		if !equivalentInteraction(existing, params) {
			return CreateInteractionResult{}, ErrIdempotencyConflict
		}
		return CreateInteractionResult{Interaction: existing, Created: false}, nil
	}
	if params.CallerAuthority == "" || params.CallerAssurance == "" {
		return CreateInteractionResult{}, fmt.Errorf("%w: caller authority and assurance are required", ErrInvalidRecord)
	}
	if params.Definition.Publisher == "" || params.Definition.Kind == "" || params.Definition.Version == "" ||
		params.Definition.Revision < 1 || params.Definition.Source == "" || params.Definition.Assurance == "" {
		return CreateInteractionResult{}, fmt.Errorf("%w: complete immutable definition binding is required", ErrInvalidRecord)
	}
	externalRefs, externalRefsErr := canonicalJSON(params.ExternalRefs, "{}")
	if externalRefsErr != nil {
		return CreateInteractionResult{}, fmt.Errorf("%w: external refs: %w", ErrInvalidRecord, externalRefsErr)
	}
	policy, policyErr := canonicalJSON(params.Policy, "{}")
	if policyErr != nil {
		return CreateInteractionResult{}, fmt.Errorf("%w: policy: %w", ErrInvalidRecord, policyErr)
	}
	params.ExternalRefs = externalRefs
	params.Policy = policy

	var sequence, surfaceRevision int64
	var surfaceState SurfaceState
	if err := tx.QueryRowContext(ctx, `
SELECT next_interaction_sequence, revision, lifecycle_state
FROM surfaces WHERE id = ?`, params.SurfaceID).Scan(&sequence, &surfaceRevision, &surfaceState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CreateInteractionResult{}, ErrNotFound
		}
		return CreateInteractionResult{}, fmt.Errorf("load surface sequence: %w", err)
	}
	if surfaceState != SurfaceStateActive {
		if surfaceState == SurfaceStateClosed || surfaceState == SurfaceStateExpired {
			return CreateInteractionResult{}, ErrTerminal
		}
		return CreateInteractionResult{}, ErrNotRespondable
	}
	now := s.now()
	result, updateErr := tx.ExecContext(ctx, `
UPDATE surfaces
SET next_interaction_sequence = next_interaction_sequence + 1,
    revision = revision + 1,
    updated_at = ?
WHERE id = ? AND revision = ?`, now, params.SurfaceID, surfaceRevision)
	if updateErr != nil {
		return CreateInteractionResult{}, fmt.Errorf("allocate surface sequence: %w", updateErr)
	}
	if !changedOne(result) {
		return CreateInteractionResult{}, ErrRevisionConflict
	}

	id := params.ID
	if id == "" {
		id = s.id()
	}
	const stagedRevision int64 = 3
	if _, err := tx.ExecContext(ctx, `
INSERT INTO interactions (
  id, surface_id, caller_scope, caller_principal_ref, caller_authority,
  caller_assurance, idempotency_key, surface_sequence,
  request_snapshot, external_refs, policy, lifecycle_state, revision,
  created_at, updated_at, legacy_room_id, legacy_envelope_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, params.SurfaceID, params.CallerScope, nullString(params.CallerPrincipalRef),
		params.CallerAuthority, params.CallerAssurance, params.IdempotencyKey, sequence,
		string(request), string(externalRefs), string(policy), InteractionStateStaged,
		stagedRevision, now, now,
		nullString(params.LegacyRoomID), nullString(params.LegacyEnvelopeID),
	); err != nil {
		return CreateInteractionResult{}, fmt.Errorf("insert interaction: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO definition_bindings (
  interaction_id, publisher, kind, version, revision, digest, source,
  schema_identity, schema_digest, host_version, assurance, bound_at,
  manifest_digest, contract_digest, response_schema_digest,
  package_id, package_version, ownership_class, compatibility_class,
  renderer_id, renderer_class, renderer_trust_class,
  required_capabilities, granted_capabilities, materialization_state
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, params.Definition.Publisher, params.Definition.Kind,
		params.Definition.Version, strconv.FormatInt(params.Definition.Revision, 10),
		nullString(params.Definition.Digest), params.Definition.Source,
		nullString(params.Definition.SchemaIdentity), nullString(params.Definition.SchemaDigest),
		nullString(params.Definition.HostVersion), params.Definition.Assurance, now,
		nullString(params.Definition.ManifestDigest), nullString(params.Definition.ContractDigest),
		nullString(params.Definition.ResponseSchemaDigest),
		nullString(params.Definition.PackageID), nullString(params.Definition.PackageVersion),
		nullString(params.Definition.OwnershipClass), nullString(params.Definition.CompatibilityClass),
		nullString(params.Definition.RendererID), nullString(params.Definition.RendererClass),
		nullString(params.Definition.RendererTrustClass),
		nullString(string(params.Definition.RequiredCapabilities)),
		nullString(string(params.Definition.GrantedCapabilities)),
		nullString(params.Definition.MaterializationState),
	); err != nil {
		return CreateInteractionResult{}, fmt.Errorf("insert definition binding: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO surface_events (
  event_id, surface_id, event_type, actor_ref, authority,
  from_revision, to_revision, metadata, recorded_at
) VALUES (?, ?, 'surface.interaction_appended', ?, ?, ?, ?, ?, ?)`,
		s.id(), params.SurfaceID, nullString(params.ActorRef), nullString(params.Authority),
		surfaceRevision, surfaceRevision+1,
		mustJSON(map[string]any{"interaction_id": id, "surface_sequence": sequence}), now,
	); err != nil {
		return CreateInteractionResult{}, fmt.Errorf("insert surface append event: %w", err)
	}
	for revision, eventType := range []string{"interaction.submitted", "interaction.validated", "interaction.staged"} {
		var fromRevision any
		if revision > 0 {
			fromRevision = revision
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO interaction_events (
  event_id, surface_id, interaction_id, event_type, actor_ref, authority,
  from_revision, to_revision, metadata, recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '{}', ?)`,
			s.id(), params.SurfaceID, id, eventType,
			nullString(params.ActorRef), nullString(params.Authority),
			fromRevision, revision+1, now,
		); err != nil {
			return CreateInteractionResult{}, fmt.Errorf("insert %s event: %w", eventType, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return CreateInteractionResult{}, fmt.Errorf("commit create interaction: %w", err)
	}
	record, getErr := s.GetInteraction(ctx, id)
	return CreateInteractionResult{Interaction: record, Created: true}, getErr
}

func (s *Store) GetInteraction(ctx context.Context, id string) (InteractionRecord, error) {
	return getInteraction(ctx, s.db, id)
}

type AdvanceInteractionParams struct {
	InteractionID               string
	ExpectedRevision            int64
	To                          InteractionState
	ActorRef                    string
	Authority                   string
	PresentedProjectionRevision int64
	ParticipantScope            string
	ParticipantRef              string
	ParticipantAuthority        string
	ParticipantAssurance        string
	ConnectionID                string
	Metadata                    json.RawMessage
}

// AdvanceInteraction is the repository-level compare-and-set primitive for
// nonterminal progress. Terminal outcomes use dedicated atomic methods.
func (s *Store) AdvanceInteraction(ctx context.Context, params AdvanceInteractionParams) (InteractionRecord, error) {
	if params.InteractionID == "" || params.ExpectedRevision < 1 || isTerminalState(params.To) {
		return InteractionRecord{}, fmt.Errorf("%w: invalid nonterminal transition", ErrInvalidRecord)
	}
	metadata, metadataErr := canonicalJSON(params.Metadata, "{}")
	if metadataErr != nil {
		return InteractionRecord{}, fmt.Errorf("%w: event metadata: %w", ErrInvalidRecord, metadataErr)
	}
	if params.To == InteractionStatePresented &&
		(params.PresentedProjectionRevision < 1 || params.ParticipantScope == "" || params.ParticipantRef == "" ||
			params.ParticipantAuthority == "" || params.ParticipantAssurance == "") {
		return InteractionRecord{}, fmt.Errorf("%w: presentation revision and complete participant binding are required", ErrInvalidRecord)
	}
	tx, beginErr := s.db.BeginTx(ctx, nil)
	if beginErr != nil {
		return InteractionRecord{}, fmt.Errorf("begin advance interaction: %w", beginErr)
	}
	defer rollback(tx)

	current, found, loadErr := getInteractionTx(ctx, tx, params.InteractionID)
	if loadErr != nil {
		return InteractionRecord{}, loadErr
	}
	if !found {
		return InteractionRecord{}, ErrNotFound
	}
	if isTerminalState(current.State) {
		return InteractionRecord{}, ErrTerminal
	}
	if current.Revision != params.ExpectedRevision {
		return InteractionRecord{}, ErrRevisionConflict
	}
	if !canAdvanceInteraction(current.State, params.To) {
		return InteractionRecord{}, ErrNotRespondable
	}
	now := s.now()
	presentationRevision := any(nil)
	participantScope := any(nil)
	participantRef := any(nil)
	participantAuthority := any(nil)
	participantAssurance := any(nil)
	connectionID := any(nil)
	presentedAt := any(nil)
	if params.To == InteractionStatePresented {
		presentationRevision = params.PresentedProjectionRevision
		participantScope = params.ParticipantScope
		participantRef = params.ParticipantRef
		participantAuthority = params.ParticipantAuthority
		participantAssurance = params.ParticipantAssurance
		connectionID = nullString(params.ConnectionID)
		presentedAt = now
	}
	result, updateErr := tx.ExecContext(ctx, `
UPDATE interactions
SET lifecycle_state = ?, revision = revision + 1, updated_at = ?,
	    presented_projection_revision = COALESCE(?, presented_projection_revision),
	    participant_scope = COALESCE(?, participant_scope),
	    participant_ref = COALESCE(?, participant_ref),
	    participant_authority = COALESCE(?, participant_authority),
	    participant_assurance = COALESCE(?, participant_assurance),
	    connection_id = COALESCE(?, connection_id),
    presented_at = COALESCE(?, presented_at)
WHERE id = ? AND revision = ?`,
		params.To, now, presentationRevision, participantScope, participantRef,
		participantAuthority, participantAssurance, connectionID, presentedAt,
		params.InteractionID, params.ExpectedRevision,
	)
	if updateErr != nil {
		return InteractionRecord{}, fmt.Errorf("advance interaction: %w", updateErr)
	}
	if !changedOne(result) {
		return InteractionRecord{}, ErrRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO interaction_events (
  event_id, surface_id, interaction_id, event_type, actor_ref, authority,
  from_revision, to_revision, metadata, recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.id(), current.SurfaceID, current.ID, "interaction."+string(params.To),
		nullString(params.ActorRef), nullString(params.Authority),
		params.ExpectedRevision, params.ExpectedRevision+1, string(metadata), now,
	); err != nil {
		return InteractionRecord{}, fmt.Errorf("insert interaction transition event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return InteractionRecord{}, fmt.Errorf("commit advance interaction: %w", err)
	}
	return s.GetInteraction(ctx, params.InteractionID)
}

func (s *Store) SaveDraftRevision(ctx context.Context, draft DraftRevision) (DraftRevision, error) {
	if draft.InteractionID == "" || draft.Revision < 1 || draft.InteractionRevision < 1 ||
		draft.ParticipantScope == "" || draft.ParticipantRef == "" || draft.ParticipantAuthority == "" ||
		draft.ParticipantAssurance == "" || draft.DefinitionVersion == "" {
		return DraftRevision{}, fmt.Errorf("%w: incomplete draft revision", ErrInvalidRecord)
	}
	payload, payloadErr := canonicalJSON(draft.Payload, "")
	if payloadErr != nil {
		return DraftRevision{}, fmt.Errorf("%w: draft payload: %w", ErrInvalidRecord, payloadErr)
	}
	tx, beginErr := s.db.BeginTx(ctx, nil)
	if beginErr != nil {
		return DraftRevision{}, fmt.Errorf("begin save draft revision: %w", beginErr)
	}
	defer rollback(tx)
	current, found, loadErr := getInteractionTx(ctx, tx, draft.InteractionID)
	if loadErr != nil {
		return DraftRevision{}, loadErr
	}
	if !found {
		return DraftRevision{}, ErrNotFound
	}
	if isTerminalState(current.State) {
		return DraftRevision{}, ErrTerminal
	}
	if current.State != InteractionStatePresented && current.State != InteractionStateInProgress {
		return DraftRevision{}, ErrNotRespondable
	}
	if current.Revision != draft.InteractionRevision {
		return DraftRevision{}, ErrRevisionConflict
	}
	if current.ParticipantScope != draft.ParticipantScope || current.ParticipantRef != draft.ParticipantRef ||
		current.ParticipantAuthority != draft.ParticipantAuthority ||
		current.ParticipantAssurance != draft.ParticipantAssurance ||
		current.Definition.Version != draft.DefinitionVersion {
		return DraftRevision{}, fmt.Errorf("%w: draft binding does not match presented interaction", ErrInvalidRecord)
	}
	var nextDraftRevision int64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(revision), 0) + 1 FROM draft_revisions WHERE interaction_id = ?`,
		draft.InteractionID,
	).Scan(&nextDraftRevision); err != nil {
		return DraftRevision{}, fmt.Errorf("load next draft revision: %w", err)
	}
	if draft.Revision != nextDraftRevision {
		return DraftRevision{}, ErrRevisionConflict
	}
	if draft.CreatedAt.IsZero() {
		draft.CreatedAt = s.now()
	}
	_, insertErr := tx.ExecContext(ctx, `
INSERT INTO draft_revisions (
  interaction_id, revision, interaction_revision, participant_scope,
  participant_ref, participant_authority, participant_assurance,
  definition_version, payload, sensitivity, expires_at, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		draft.InteractionID, draft.Revision, draft.InteractionRevision,
		draft.ParticipantScope, draft.ParticipantRef, draft.ParticipantAuthority,
		draft.ParticipantAssurance, draft.DefinitionVersion, string(payload),
		nullString(draft.Sensitivity), draft.ExpiresAt, draft.CreatedAt,
	)
	if insertErr != nil {
		return DraftRevision{}, fmt.Errorf("insert draft revision: %w", insertErr)
	}
	result, updateErr := tx.ExecContext(ctx, `
UPDATE interactions
SET lifecycle_state = 'in_progress', revision = revision + 1, updated_at = ?
WHERE id = ? AND revision = ?`,
		draft.CreatedAt, draft.InteractionID, draft.InteractionRevision,
	)
	if updateErr != nil {
		return DraftRevision{}, fmt.Errorf("advance interaction for draft: %w", updateErr)
	}
	if !changedOne(result) {
		return DraftRevision{}, ErrRevisionConflict
	}
	eventType := "interaction.draft_saved"
	if current.State == InteractionStatePresented {
		eventType = "interaction.in_progress"
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO interaction_events (
	  event_id, surface_id, interaction_id, event_type, actor_ref, authority,
	  from_revision, to_revision, metadata, recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.id(), current.SurfaceID, current.ID, eventType, draft.ParticipantRef, draft.ParticipantAuthority,
		draft.InteractionRevision, draft.InteractionRevision+1,
		mustJSON(map[string]any{"draft_revision": draft.Revision}), draft.CreatedAt,
	); err != nil {
		return DraftRevision{}, fmt.Errorf("insert draft event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return DraftRevision{}, fmt.Errorf("commit draft revision: %w", err)
	}
	draft.Payload = payload
	return draft, nil
}

type ResolutionDeliveryParams struct {
	ID                 string
	DestinationBinding json.RawMessage
	IdempotencyKey     string
	Policy             json.RawMessage
}

type ResolveInteractionParams struct {
	ID                          string
	InteractionID               string
	ExpectedInteractionRevision int64
	PresentedProjectionRevision int64
	ParticipantScope            string
	ParticipantRef              string
	ParticipantAuthority        string
	ParticipantAssurance        string
	ResponseKind                string
	ResponsePayload             json.RawMessage
	SourceDraftRevision         *int64
	SubmittedAt                 time.Time
	Deliveries                  []ResolutionDeliveryParams
}

type ResolveInteractionResult struct {
	Interaction InteractionRecord
	Resolution  ResolutionRecord
	Deliveries  []ResolutionDeliveryRecord
}

// ResolveInteraction records the immutable resolution, terminal interaction
// transition, event, and initial delivery obligations in one transaction.
func (s *Store) ResolveInteraction(ctx context.Context, params ResolveInteractionParams) (ResolveInteractionResult, error) {
	if params.InteractionID == "" || params.ExpectedInteractionRevision < 1 ||
		params.PresentedProjectionRevision < 1 || params.ParticipantScope == "" || params.ParticipantRef == "" ||
		params.ParticipantAuthority == "" || params.ParticipantAssurance == "" || params.ResponseKind == "" ||
		len(params.Deliveries) == 0 {
		return ResolveInteractionResult{}, fmt.Errorf("%w: incomplete resolution", ErrInvalidRecord)
	}
	payload, payloadErr := canonicalJSON(params.ResponsePayload, "")
	if payloadErr != nil {
		return ResolveInteractionResult{}, fmt.Errorf("%w: response payload: %w", ErrInvalidRecord, payloadErr)
	}
	normalizedDeliveries := make([]ResolutionDeliveryParams, len(params.Deliveries))
	for i := range params.Deliveries {
		delivery := params.Deliveries[i]
		if delivery.IdempotencyKey == "" {
			return ResolveInteractionResult{}, fmt.Errorf("%w: delivery idempotency key is required", ErrInvalidRecord)
		}
		destination, destinationErr := canonicalJSON(delivery.DestinationBinding, "")
		if destinationErr != nil {
			return ResolveInteractionResult{}, fmt.Errorf("%w: delivery destination: %w", ErrInvalidRecord, destinationErr)
		}
		delivery.DestinationBinding = destination
		policy, policyErr := canonicalJSON(delivery.Policy, "{}")
		if policyErr != nil {
			return ResolveInteractionResult{}, fmt.Errorf("%w: delivery policy: %w", ErrInvalidRecord, policyErr)
		}
		delivery.Policy = policy
		normalizedDeliveries[i] = delivery
	}

	tx, beginErr := s.db.BeginTx(ctx, nil)
	if beginErr != nil {
		return ResolveInteractionResult{}, fmt.Errorf("begin resolve interaction: %w", beginErr)
	}
	defer rollback(tx)
	current, found, loadErr := getInteractionTx(ctx, tx, params.InteractionID)
	if loadErr != nil {
		return ResolveInteractionResult{}, loadErr
	}
	if !found {
		return ResolveInteractionResult{}, ErrNotFound
	}
	if isTerminalState(current.State) {
		return ResolveInteractionResult{}, ErrTerminal
	}
	if current.Revision != params.ExpectedInteractionRevision {
		return ResolveInteractionResult{}, ErrRevisionConflict
	}
	if current.State != InteractionStatePresented && current.State != InteractionStateInProgress {
		return ResolveInteractionResult{}, ErrNotRespondable
	}
	if current.ParticipantScope != params.ParticipantScope || current.ParticipantRef != params.ParticipantRef ||
		current.ParticipantAuthority != params.ParticipantAuthority ||
		current.ParticipantAssurance != params.ParticipantAssurance {
		return ResolveInteractionResult{}, ErrNotRespondable
	}
	if current.PresentedProjectionRevision == nil ||
		*current.PresentedProjectionRevision != params.PresentedProjectionRevision {
		return ResolveInteractionResult{}, ErrRevisionConflict
	}

	now := s.now()
	if params.SubmittedAt.IsZero() {
		params.SubmittedAt = now
	}
	resolutionID := params.ID
	if resolutionID == "" {
		resolutionID = s.id()
	}
	digest := resolutionDigest(params, payload)
	result, updateErr := tx.ExecContext(ctx, `
UPDATE interactions
SET lifecycle_state = 'resolved', revision = revision + 1,
    updated_at = ?, terminal_at = ?
WHERE id = ? AND revision = ?`,
		now, now, params.InteractionID, params.ExpectedInteractionRevision,
	)
	if updateErr != nil {
		return ResolveInteractionResult{}, fmt.Errorf("terminalize interaction: %w", updateErr)
	}
	if !changedOne(result) {
		return ResolveInteractionResult{}, ErrRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO resolutions (
	  id, interaction_id, expected_interaction_revision,
	  presented_projection_revision, participant_scope, participant_ref, participant_authority,
	  participant_assurance, response_kind, response_payload,
	  source_draft_revision, integrity_digest,
	  submitted_at, validated_at, recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		resolutionID, params.InteractionID, params.ExpectedInteractionRevision,
		params.PresentedProjectionRevision, params.ParticipantScope, params.ParticipantRef,
		params.ParticipantAuthority, params.ParticipantAssurance,
		params.ResponseKind, string(payload), params.SourceDraftRevision, digest,
		params.SubmittedAt, now, now,
	); err != nil {
		return ResolveInteractionResult{}, fmt.Errorf("insert resolution: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO interaction_events (
  event_id, surface_id, interaction_id, event_type, actor_ref, authority,
  from_revision, to_revision, metadata, recorded_at
) VALUES (?, ?, ?, 'interaction.resolved', ?, ?, ?, ?, ?, ?)`,
		s.id(), current.SurfaceID, current.ID,
		params.ParticipantRef, params.ParticipantAuthority,
		params.ExpectedInteractionRevision, params.ExpectedInteractionRevision+1,
		mustJSON(map[string]any{"resolution_id": resolutionID}), now,
	); err != nil {
		return ResolveInteractionResult{}, fmt.Errorf("insert resolution event: %w", err)
	}
	for i := range normalizedDeliveries {
		deliveryID := normalizedDeliveries[i].ID
		if deliveryID == "" {
			deliveryID = s.id()
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO resolution_deliveries (
  id, resolution_id, destination_binding, idempotency_key, policy,
  lifecycle_state, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 'queued', 1, ?, ?)`,
			deliveryID, resolutionID, string(normalizedDeliveries[i].DestinationBinding),
			normalizedDeliveries[i].IdempotencyKey, string(normalizedDeliveries[i].Policy), now, now,
		); err != nil {
			return ResolveInteractionResult{}, fmt.Errorf("insert resolution delivery: %w", err)
		}
		if err := s.insertDeliveryEvent(
			ctx, tx, DeliveryKindResolution, deliveryID, "delivery.queued",
			0, 1, nil, mustJSON(map[string]any{"resolution_id": resolutionID}), now,
		); err != nil {
			return ResolveInteractionResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ResolveInteractionResult{}, fmt.Errorf("commit resolve interaction: %w", err)
	}

	interactionRecord, err := s.GetInteraction(ctx, params.InteractionID)
	if err != nil {
		return ResolveInteractionResult{}, err
	}
	resolution, err := s.GetResolution(ctx, params.InteractionID)
	if err != nil {
		return ResolveInteractionResult{}, err
	}
	deliveries, err := s.ListResolutionDeliveries(ctx, resolution.ID)
	if err != nil {
		return ResolveInteractionResult{}, err
	}
	return ResolveInteractionResult{
		Interaction: interactionRecord,
		Resolution:  resolution,
		Deliveries:  deliveries,
	}, nil
}

type TerminalNotificationParams struct {
	ID                 string
	DestinationBinding json.RawMessage
	IdempotencyKey     string
	Policy             json.RawMessage
}

type TerminalizeInteractionParams struct {
	InteractionID            string
	ExpectedRevision         int64
	To                       InteractionState
	Cause                    TerminalCause
	Reason                   string
	PolicyRef                string
	ErrorCode                string
	ReplacementInteractionID string
	ActorRef                 string
	Authority                string
	Metadata                 json.RawMessage
	Notifications            []TerminalNotificationParams
}

type TerminalizeInteractionResult struct {
	Interaction   InteractionRecord
	Notifications []TerminalNotificationRecord
}

// TerminalizeInteraction atomically seals a non-resolution terminal outcome
// and queues its initial notification obligations. Transport loss is not a
// valid caller of this repository primitive by itself.
func (s *Store) TerminalizeInteraction(
	ctx context.Context,
	params TerminalizeInteractionParams,
) (TerminalizeInteractionResult, error) {
	if params.InteractionID == "" || params.ExpectedRevision < 1 ||
		len(params.Notifications) == 0 || !isNonResolutionTerminalState(params.To) {
		return TerminalizeInteractionResult{}, fmt.Errorf("%w: incomplete terminal disposition", ErrInvalidRecord)
	}
	if validationErr := validateTerminalDisposition(params); validationErr != nil {
		return TerminalizeInteractionResult{}, validationErr
	}
	metadata, metadataErr := canonicalJSON(params.Metadata, "{}")
	if metadataErr != nil {
		return TerminalizeInteractionResult{}, fmt.Errorf("%w: event metadata: %w", ErrInvalidRecord, metadataErr)
	}
	normalizedNotifications := make([]TerminalNotificationParams, len(params.Notifications))
	for index := range params.Notifications {
		notification := params.Notifications[index]
		if notification.IdempotencyKey == "" {
			return TerminalizeInteractionResult{}, fmt.Errorf("%w: notification idempotency key is required", ErrInvalidRecord)
		}
		destination, destinationErr := canonicalJSON(notification.DestinationBinding, "")
		if destinationErr != nil {
			return TerminalizeInteractionResult{}, fmt.Errorf("%w: notification destination: %w", ErrInvalidRecord, destinationErr)
		}
		notification.DestinationBinding = destination
		policy, policyErr := canonicalJSON(notification.Policy, "{}")
		if policyErr != nil {
			return TerminalizeInteractionResult{}, fmt.Errorf("%w: notification policy: %w", ErrInvalidRecord, policyErr)
		}
		notification.Policy = policy
		normalizedNotifications[index] = notification
	}

	tx, beginErr := s.db.BeginTx(ctx, nil)
	if beginErr != nil {
		return TerminalizeInteractionResult{}, fmt.Errorf("begin terminalize interaction: %w", beginErr)
	}
	defer rollback(tx)
	current, found, loadErr := getInteractionTx(ctx, tx, params.InteractionID)
	if loadErr != nil {
		return TerminalizeInteractionResult{}, loadErr
	}
	if !found {
		return TerminalizeInteractionResult{}, ErrNotFound
	}
	if isTerminalState(current.State) {
		return TerminalizeInteractionResult{}, ErrTerminal
	}
	if current.Revision != params.ExpectedRevision {
		return TerminalizeInteractionResult{}, ErrRevisionConflict
	}
	if !canTerminalizeFrom(current.State, params.To) {
		return TerminalizeInteractionResult{}, ErrNotRespondable
	}
	if params.To == InteractionStateSuperseded {
		replacement, replacementFound, replacementErr := getInteractionTx(
			ctx,
			tx,
			params.ReplacementInteractionID,
		)
		if replacementErr != nil {
			return TerminalizeInteractionResult{}, replacementErr
		}
		if !replacementFound {
			return TerminalizeInteractionResult{}, ErrNotFound
		}
		if replacement.ID == current.ID || replacement.SurfaceID != current.SurfaceID ||
			replacement.CallerScope != current.CallerScope {
			return TerminalizeInteractionResult{}, fmt.Errorf(
				"%w: replacement must be a distinct interaction on the same surface and caller scope",
				ErrInvalidRecord,
			)
		}
		if isTerminalState(replacement.State) {
			return TerminalizeInteractionResult{}, ErrNotRespondable
		}
	}
	now := s.now()
	result, updateErr := tx.ExecContext(ctx, `
UPDATE interactions
SET lifecycle_state = ?, terminal_cause = ?, terminal_reason = ?,
    terminal_policy_ref = ?, terminal_error_code = ?,
    replacement_interaction_id = ?, revision = revision + 1,
    updated_at = ?, terminal_at = ?
WHERE id = ? AND revision = ?`,
		params.To, nullString(string(params.Cause)), nullString(params.Reason),
		nullString(params.PolicyRef), nullString(params.ErrorCode), nullString(params.ReplacementInteractionID),
		now, now, params.InteractionID, params.ExpectedRevision,
	)
	if updateErr != nil {
		return TerminalizeInteractionResult{}, fmt.Errorf("terminalize interaction: %w", updateErr)
	}
	if !changedOne(result) {
		return TerminalizeInteractionResult{}, ErrRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO interaction_events (
  event_id, surface_id, interaction_id, event_type, actor_ref, authority,
  from_revision, to_revision, metadata, recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.id(), current.SurfaceID, current.ID, "interaction."+string(params.To),
		nullString(params.ActorRef), nullString(params.Authority),
		params.ExpectedRevision, params.ExpectedRevision+1, string(metadata), now,
	); err != nil {
		return TerminalizeInteractionResult{}, fmt.Errorf("insert terminal event: %w", err)
	}
	for index := range normalizedNotifications {
		notification := normalizedNotifications[index]
		id := notification.ID
		if id == "" {
			id = s.id()
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO terminal_notifications (
  id, interaction_id, terminal_state, terminal_cause,
  destination_binding, idempotency_key, policy,
  lifecycle_state, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'queued', 1, ?, ?)`,
			id, params.InteractionID, params.To, nullString(string(params.Cause)),
			string(notification.DestinationBinding), notification.IdempotencyKey,
			string(notification.Policy), now, now,
		); err != nil {
			return TerminalizeInteractionResult{}, fmt.Errorf("insert terminal notification: %w", err)
		}
		if err := s.insertDeliveryEvent(
			ctx, tx, DeliveryKindTerminalNotification, id, "delivery.queued",
			0, 1, nil, mustJSON(map[string]any{"interaction_id": params.InteractionID}), now,
		); err != nil {
			return TerminalizeInteractionResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return TerminalizeInteractionResult{}, fmt.Errorf("commit terminal disposition: %w", err)
	}
	interactionRecord, err := s.GetInteraction(ctx, params.InteractionID)
	if err != nil {
		return TerminalizeInteractionResult{}, err
	}
	notifications, err := s.ListTerminalNotifications(ctx, params.InteractionID)
	if err != nil {
		return TerminalizeInteractionResult{}, err
	}
	return TerminalizeInteractionResult{Interaction: interactionRecord, Notifications: notifications}, nil
}

func (s *Store) GetResolution(ctx context.Context, interactionID string) (ResolutionRecord, error) {
	return scanResolution(s.db.QueryRowContext(ctx, `
SELECT id, interaction_id, expected_interaction_revision,
	       presented_projection_revision, participant_scope, participant_ref, participant_authority,
       participant_assurance, response_kind, response_payload,
       source_draft_revision, integrity_digest,
       submitted_at, validated_at, recorded_at
FROM resolutions WHERE interaction_id = ?`, interactionID))
}

func (s *Store) ListResolutionDeliveries(ctx context.Context, resolutionID string) ([]ResolutionDeliveryRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, resolution_id, destination_binding, idempotency_key, policy,
       lifecycle_state, revision, lease_owner, lease_expires_at,
       receipt, terminal_reason, next_eligible_at,
       created_at, updated_at, delivered_at, acknowledged_at
FROM resolution_deliveries
WHERE resolution_id = ?
ORDER BY created_at, id`, resolutionID)
	if err != nil {
		return nil, fmt.Errorf("list resolution deliveries: %w", err)
	}
	defer closeRows(rows)
	var out []ResolutionDeliveryRecord
	for rows.Next() {
		record, err := scanResolutionDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resolution deliveries: %w", err)
	}
	return out, nil
}

func (s *Store) ListTerminalNotifications(ctx context.Context, interactionID string) ([]TerminalNotificationRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, interaction_id, terminal_state, terminal_cause,
       destination_binding, idempotency_key, policy, lifecycle_state,
       revision, lease_owner, lease_expires_at,
       receipt, terminal_reason, next_eligible_at,
       created_at, updated_at, delivered_at, acknowledged_at
FROM terminal_notifications
WHERE interaction_id = ?
ORDER BY created_at, id`, interactionID)
	if err != nil {
		return nil, fmt.Errorf("list terminal notifications: %w", err)
	}
	defer closeRows(rows)
	var out []TerminalNotificationRecord
	for rows.Next() {
		record, scanErr := scanTerminalNotification(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate terminal notifications: %w", err)
	}
	return out, nil
}

type RecordTerminalOutcomeRetrievalParams struct {
	ID                   string
	InteractionID        string
	RequesterScope       string
	TransportCorrelation json.RawMessage
}

// RecordTerminalOutcomeRetrieval appends an audit fact for a terminal handle
// read without changing or duplicating the underlying terminal outcome.
func (s *Store) RecordTerminalOutcomeRetrieval(
	ctx context.Context,
	params RecordTerminalOutcomeRetrievalParams,
) (TerminalOutcomeRetrievalRecord, error) {
	if params.InteractionID == "" || params.RequesterScope == "" {
		return TerminalOutcomeRetrievalRecord{}, fmt.Errorf("%w: interaction and requester scope are required", ErrInvalidRecord)
	}
	correlation, correlationErr := canonicalJSON(params.TransportCorrelation, "{}")
	if correlationErr != nil {
		return TerminalOutcomeRetrievalRecord{}, fmt.Errorf("%w: transport correlation: %w", ErrInvalidRecord, correlationErr)
	}
	tx, beginErr := s.db.BeginTx(ctx, nil)
	if beginErr != nil {
		return TerminalOutcomeRetrievalRecord{}, fmt.Errorf("begin record terminal retrieval: %w", beginErr)
	}
	defer rollback(tx)
	interactionRecord, found, loadErr := getInteractionTx(ctx, tx, params.InteractionID)
	if loadErr != nil {
		return TerminalOutcomeRetrievalRecord{}, loadErr
	}
	if !found {
		return TerminalOutcomeRetrievalRecord{}, ErrNotFound
	}
	if !isTerminalState(interactionRecord.State) {
		return TerminalOutcomeRetrievalRecord{}, ErrNotRespondable
	}
	var resolutionIDValue string
	var resolutionID any
	if interactionRecord.State == InteractionStateResolved {
		var id string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM resolutions WHERE interaction_id = ?`, params.InteractionID).Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return TerminalOutcomeRetrievalRecord{}, fmt.Errorf("%w: resolved interaction has no resolution", ErrInvalidRecord)
			}
			return TerminalOutcomeRetrievalRecord{}, fmt.Errorf("load retrieval resolution: %w", err)
		}
		resolutionIDValue = id
		resolutionID = id
	}
	id := params.ID
	if id == "" {
		id = s.id()
	}
	retrievedAt := s.now()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO terminal_outcome_retrievals (
  id, interaction_id, resolution_id, requester_scope,
  transport_correlation, retrieved_at
) VALUES (?, ?, ?, ?, ?, ?)`,
		id, params.InteractionID, resolutionID, params.RequesterScope,
		string(correlation), retrievedAt,
	); err != nil {
		return TerminalOutcomeRetrievalRecord{}, fmt.Errorf("insert terminal retrieval: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TerminalOutcomeRetrievalRecord{}, fmt.Errorf("commit terminal retrieval: %w", err)
	}
	return TerminalOutcomeRetrievalRecord{
		ID: id, InteractionID: params.InteractionID,
		ResolutionID: resolutionIDValue, RequesterScope: params.RequesterScope,
		TransportCorrelation: correlation, RetrievedAt: retrievedAt,
	}, nil
}

// HydrateSurface returns canonical records exactly as persisted, including
// open interactions. It never derives timeout state from process lifetime.
func (s *Store) HydrateSurface(ctx context.Context, surfaceID string) (SurfaceSnapshot, error) {
	tx, beginErr := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if beginErr != nil {
		return SurfaceSnapshot{}, fmt.Errorf("begin surface hydration: %w", beginErr)
	}
	defer rollback(tx)
	surface, err := getSurface(ctx, tx, surfaceID)
	if err != nil {
		return SurfaceSnapshot{}, err
	}
	interactions, err := s.listSurfaceInteractions(ctx, tx, surfaceID)
	if err != nil {
		return SurfaceSnapshot{}, err
	}
	drafts, err := s.listSurfaceDrafts(ctx, tx, surfaceID)
	if err != nil {
		return SurfaceSnapshot{}, err
	}
	resolutions, err := s.listSurfaceResolutions(ctx, tx, surfaceID)
	if err != nil {
		return SurfaceSnapshot{}, err
	}
	deliveries, err := s.listSurfaceDeliveries(ctx, tx, surfaceID)
	if err != nil {
		return SurfaceSnapshot{}, err
	}
	notifications, err := s.listSurfaceTerminalNotifications(ctx, tx, surfaceID)
	if err != nil {
		return SurfaceSnapshot{}, err
	}
	retrievals, err := s.listSurfaceOutcomeRetrievals(ctx, tx, surfaceID)
	if err != nil {
		return SurfaceSnapshot{}, err
	}
	attempts, err := s.listSurfaceDeliveryAttempts(ctx, tx, surfaceID)
	if err != nil {
		return SurfaceSnapshot{}, err
	}
	events, err := s.listSurfaceDeliveryEvents(ctx, tx, surfaceID)
	if err != nil {
		return SurfaceSnapshot{}, err
	}
	snapshot := SurfaceSnapshot{
		Surface: surface, Interactions: interactions, Drafts: drafts,
		Resolutions: resolutions, ResolutionDeliveries: deliveries,
		TerminalNotifications: notifications, OutcomeRetrievals: retrievals,
		DeliveryAttempts: attempts, DeliveryEvents: events,
	}
	if err := tx.Commit(); err != nil {
		return SurfaceSnapshot{}, fmt.Errorf("commit surface hydration: %w", err)
	}
	return snapshot, nil
}

// HydrateSurfaceInteractions returns one read-transaction snapshot containing
// only interaction records and their terminal resolutions. Operator queues use
// this instead of loading unrelated delivery and audit collections on every
// durable resynchronization.
func (s *Store) HydrateSurfaceInteractions(ctx context.Context, surfaceID string) (SurfaceInteractionSnapshot, error) {
	tx, beginErr := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if beginErr != nil {
		return SurfaceInteractionSnapshot{}, fmt.Errorf("begin surface interaction hydration: %w", beginErr)
	}
	defer rollback(tx)
	surface, err := getSurface(ctx, tx, surfaceID)
	if err != nil {
		return SurfaceInteractionSnapshot{}, err
	}
	interactions, err := s.listSurfaceInteractions(ctx, tx, surfaceID)
	if err != nil {
		return SurfaceInteractionSnapshot{}, err
	}
	resolutions, err := s.listSurfaceResolutions(ctx, tx, surfaceID)
	if err != nil {
		return SurfaceInteractionSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return SurfaceInteractionSnapshot{}, fmt.Errorf("commit surface interaction hydration: %w", err)
	}
	return SurfaceInteractionSnapshot{
		Surface: surface, Interactions: interactions, Resolutions: resolutions,
	}, nil
}

func (s *Store) ListLegacyRoomHistory(ctx context.Context, roomID string) ([]LegacyRoomHistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT room_id, envelope_id, type, request_payload, response_kind,
       response_payload, status, error_code, error_message,
       created_at, resolved_at, surface_id, interaction_id,
       interaction_state, caller_acknowledged_at
FROM legacy_room_history_v12
WHERE room_id = ?
ORDER BY created_at, envelope_id`, roomID)
	if err != nil {
		return nil, fmt.Errorf("list legacy room history: %w", err)
	}
	defer closeRows(rows)
	var out []LegacyRoomHistoryEntry
	for rows.Next() {
		var record LegacyRoomHistoryEntry
		var request string
		var responseKind, responsePayload, errorCode, errorMessage sql.NullString
		// A legacy room that predates the durable substrate and was never
		// touched since has no surface or interaction. That is a legitimate
		// projection, not a scan failure.
		var surfaceID, interactionID, interactionState sql.NullString
		var resolvedAt, acknowledgedAt sql.NullTime
		if err := rows.Scan(
			&record.RoomID, &record.EnvelopeID, &record.Type, &request,
			&responseKind, &responsePayload, &record.Status, &errorCode,
			&errorMessage, &record.CreatedAt, &resolvedAt,
			&surfaceID, &interactionID, &interactionState, &acknowledgedAt,
		); err != nil {
			return nil, fmt.Errorf("scan legacy room history: %w", err)
		}
		record.SurfaceID = surfaceID.String
		record.InteractionID = interactionID.String
		record.InteractionState = InteractionState(interactionState.String)
		if acknowledgedAt.Valid {
			acknowledged := acknowledgedAt.Time
			record.CallerAcknowledgedAt = &acknowledged
		}
		record.RequestPayload = json.RawMessage(request)
		record.ResponseKind = responseKind.String
		if responsePayload.Valid {
			record.ResponsePayload = json.RawMessage(responsePayload.String)
		}
		record.ErrorCode = errorCode.String
		record.ErrorMessage = errorMessage.String
		if resolvedAt.Valid {
			resolved := resolvedAt.Time
			record.ResolvedAt = &resolved
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy room history: %w", err)
	}
	return out, nil
}

func (s *Store) listSurfaceInteractions(ctx context.Context, q queryer, surfaceID string) ([]InteractionRecord, error) {
	rows, err := q.QueryContext(ctx, interactionSelect+`
WHERE i.surface_id = ? ORDER BY i.surface_sequence`, surfaceID)
	if err != nil {
		return nil, fmt.Errorf("list surface interactions: %w", err)
	}
	defer closeRows(rows)
	var out []InteractionRecord
	for rows.Next() {
		record, err := scanInteractionWithDefinition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate surface interactions: %w", err)
	}
	return out, nil
}

func (s *Store) listSurfaceDrafts(ctx context.Context, q queryer, surfaceID string) ([]DraftRevision, error) {
	rows, err := q.QueryContext(ctx, `
SELECT d.interaction_id, d.revision, d.interaction_revision,
	       d.participant_scope, d.participant_ref, d.participant_authority,
	       d.participant_assurance, d.definition_version, d.payload,
       d.sensitivity, d.expires_at, d.created_at
FROM draft_revisions d
JOIN interactions i ON i.id = d.interaction_id
WHERE i.surface_id = ?
ORDER BY d.interaction_id, d.revision`, surfaceID)
	if err != nil {
		return nil, fmt.Errorf("list surface drafts: %w", err)
	}
	defer closeRows(rows)
	var out []DraftRevision
	for rows.Next() {
		record, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate surface drafts: %w", err)
	}
	return out, nil
}

func (s *Store) listSurfaceResolutions(ctx context.Context, q queryer, surfaceID string) ([]ResolutionRecord, error) {
	rows, err := q.QueryContext(ctx, `
SELECT r.id, r.interaction_id, r.expected_interaction_revision,
	       r.presented_projection_revision, r.participant_scope, r.participant_ref, r.participant_authority,
       r.participant_assurance, r.response_kind, r.response_payload,
       r.source_draft_revision, r.integrity_digest,
       r.submitted_at, r.validated_at, r.recorded_at
FROM resolutions r
JOIN interactions i ON i.id = r.interaction_id
WHERE i.surface_id = ?
ORDER BY r.recorded_at, r.id`, surfaceID)
	if err != nil {
		return nil, fmt.Errorf("list surface resolutions: %w", err)
	}
	defer closeRows(rows)
	var out []ResolutionRecord
	for rows.Next() {
		record, err := scanResolution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate surface resolutions: %w", err)
	}
	return out, nil
}

func (s *Store) listSurfaceDeliveries(ctx context.Context, q queryer, surfaceID string) ([]ResolutionDeliveryRecord, error) {
	rows, err := q.QueryContext(ctx, `
SELECT d.id, d.resolution_id, d.destination_binding, d.idempotency_key,
       d.policy, d.lifecycle_state, d.revision, d.lease_owner, d.lease_expires_at, d.receipt,
       d.terminal_reason, d.next_eligible_at, d.created_at, d.updated_at,
       d.delivered_at, d.acknowledged_at
FROM resolution_deliveries d
JOIN resolutions r ON r.id = d.resolution_id
JOIN interactions i ON i.id = r.interaction_id
WHERE i.surface_id = ?
ORDER BY d.created_at, d.id`, surfaceID)
	if err != nil {
		return nil, fmt.Errorf("list surface deliveries: %w", err)
	}
	defer closeRows(rows)
	var out []ResolutionDeliveryRecord
	for rows.Next() {
		record, err := scanResolutionDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate surface deliveries: %w", err)
	}
	return out, nil
}

func (s *Store) listSurfaceTerminalNotifications(
	ctx context.Context,
	q queryer,
	surfaceID string,
) ([]TerminalNotificationRecord, error) {
	rows, err := q.QueryContext(ctx, `
SELECT n.id, n.interaction_id, n.terminal_state, n.terminal_cause,
       n.destination_binding, n.idempotency_key, n.policy, n.lifecycle_state,
       n.revision, n.lease_owner, n.lease_expires_at,
       n.receipt, n.terminal_reason, n.next_eligible_at,
       n.created_at, n.updated_at, n.delivered_at, n.acknowledged_at
FROM terminal_notifications n
JOIN interactions i ON i.id = n.interaction_id
WHERE i.surface_id = ?
ORDER BY n.created_at, n.id`, surfaceID)
	if err != nil {
		return nil, fmt.Errorf("list surface terminal notifications: %w", err)
	}
	defer closeRows(rows)
	var out []TerminalNotificationRecord
	for rows.Next() {
		record, scanErr := scanTerminalNotification(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate surface terminal notifications: %w", err)
	}
	return out, nil
}

func (s *Store) listSurfaceOutcomeRetrievals(
	ctx context.Context,
	q queryer,
	surfaceID string,
) ([]TerminalOutcomeRetrievalRecord, error) {
	rows, err := q.QueryContext(ctx, `
SELECT r.id, r.interaction_id, r.resolution_id, r.requester_scope,
       r.transport_correlation, r.retrieved_at
FROM terminal_outcome_retrievals r
JOIN interactions i ON i.id = r.interaction_id
WHERE i.surface_id = ?
ORDER BY r.retrieved_at, r.id`, surfaceID)
	if err != nil {
		return nil, fmt.Errorf("list surface outcome retrievals: %w", err)
	}
	defer closeRows(rows)
	var out []TerminalOutcomeRetrievalRecord
	for rows.Next() {
		record, scanErr := scanTerminalOutcomeRetrieval(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate surface outcome retrievals: %w", err)
	}
	return out, nil
}

func (s *Store) listSurfaceDeliveryAttempts(
	ctx context.Context,
	q queryer,
	surfaceID string,
) ([]DeliveryAttempt, error) {
	rows, err := q.QueryContext(ctx, `
SELECT a.id, a.resolution_delivery_id, a.terminal_notification_id,
       a.attempt_number, a.status, a.receipt, a.error_code, a.error_message,
       a.started_at, a.completed_at
FROM delivery_attempts a
LEFT JOIN resolution_deliveries d ON d.id = a.resolution_delivery_id
LEFT JOIN resolutions r ON r.id = d.resolution_id
LEFT JOIN terminal_notifications n ON n.id = a.terminal_notification_id
JOIN interactions i ON i.id = COALESCE(r.interaction_id, n.interaction_id)
WHERE i.surface_id = ?
ORDER BY a.started_at, a.id`, surfaceID)
	if err != nil {
		return nil, fmt.Errorf("list surface delivery attempts: %w", err)
	}
	defer closeRows(rows)
	var out []DeliveryAttempt
	for rows.Next() {
		record, scanErr := scanDeliveryAttempt(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate surface delivery attempts: %w", err)
	}
	return out, nil
}

func (s *Store) listSurfaceDeliveryEvents(
	ctx context.Context,
	q queryer,
	surfaceID string,
) ([]DeliveryEvent, error) {
	rows, err := q.QueryContext(ctx, `
SELECT e.event_id, e.resolution_delivery_id, e.terminal_notification_id,
       e.event_type, e.from_revision, e.to_revision,
       e.attempt_number, e.metadata, e.recorded_at
FROM delivery_events e
LEFT JOIN resolution_deliveries d ON d.id = e.resolution_delivery_id
LEFT JOIN resolutions r ON r.id = d.resolution_id
LEFT JOIN terminal_notifications n ON n.id = e.terminal_notification_id
JOIN interactions i ON i.id = COALESCE(r.interaction_id, n.interaction_id)
WHERE i.surface_id = ?
ORDER BY e.recorded_at, e.event_id`, surfaceID)
	if err != nil {
		return nil, fmt.Errorf("list surface delivery events: %w", err)
	}
	defer closeRows(rows)
	var out []DeliveryEvent
	for rows.Next() {
		record, scanErr := scanDeliveryEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate surface delivery events: %w", err)
	}
	return out, nil
}

func equivalentInteraction(existing InteractionRecord, params CreateInteractionParams) bool {
	// Idempotency is defined by the scoped canonical caller request. Surface
	// selection, extracted correlations/policy, and definition materialization
	// are downstream facts and cannot turn the same retry into a conflict.
	return bytes.Equal(existing.RequestSnapshot, params.RequestSnapshot)
}

func isTerminalState(state InteractionState) bool {
	switch state {
	case InteractionStateResolved, InteractionStateCanceled, InteractionStateExpired,
		InteractionStateFailed, InteractionStateSuperseded:
		return true
	default:
		return false
	}
}

func isNonResolutionTerminalState(state InteractionState) bool {
	switch state {
	case InteractionStateCanceled, InteractionStateExpired, InteractionStateFailed, InteractionStateSuperseded:
		return true
	default:
		return false
	}
}

func canAdvanceSurface(from, to SurfaceState) bool {
	return (from == SurfaceStateCreated && to == SurfaceStateActive) ||
		(from == SurfaceStateActive && to == SurfaceStateSuspended) ||
		(from == SurfaceStateSuspended && to == SurfaceStateActive)
}

func canAdvanceInteraction(from, to InteractionState) bool {
	return (from == InteractionStateValidated && to == InteractionStateStaged) ||
		(from == InteractionStateStaged && to == InteractionStatePresented) ||
		(from == InteractionStatePresented && to == InteractionStateInProgress)
}

func canTerminalizeFrom(from, to InteractionState) bool {
	switch to {
	case InteractionStateCanceled, InteractionStateExpired:
		return !isTerminalState(from)
	case InteractionStateFailed:
		return from == InteractionStateSubmitted || from == InteractionStateValidated || from == InteractionStateStaged
	case InteractionStateSuperseded:
		return from == InteractionStateStaged || from == InteractionStatePresented || from == InteractionStateInProgress
	default:
		return false
	}
}

func validateTerminalDisposition(params TerminalizeInteractionParams) error {
	invalid := func(message string) error {
		return fmt.Errorf("%w: %s", ErrInvalidRecord, message)
	}
	switch params.To {
	case InteractionStateCanceled:
		if !isCancellationCause(params.Cause) {
			return invalid("canceled disposition requires a recognized cancellation cause")
		}
		if params.PolicyRef != "" || params.ErrorCode != "" || params.ReplacementInteractionID != "" {
			return invalid("canceled disposition contains fields owned by another terminal state")
		}
	case InteractionStateExpired:
		if params.Cause != "" || params.PolicyRef == "" || params.ErrorCode != "" || params.ReplacementInteractionID != "" {
			return invalid("expired disposition requires only a policy reference")
		}
	case InteractionStateFailed:
		if params.Cause != "" || params.PolicyRef != "" || params.ErrorCode == "" ||
			params.Reason == "" || params.ReplacementInteractionID != "" {
			return invalid("failed disposition requires only an error code and message")
		}
	case InteractionStateSuperseded:
		if params.Cause != "" || params.PolicyRef != "" || params.ErrorCode != "" || params.ReplacementInteractionID == "" {
			return invalid("superseded disposition requires only a replacement interaction")
		}
	default:
		return invalid("unsupported non-resolution terminal state")
	}
	return nil
}

func isCancellationCause(cause TerminalCause) bool {
	switch cause {
	case TerminalCauseCallerWithdrawn, TerminalCauseCallerCanceled,
		TerminalCauseParticipantCanceled, TerminalCauseAdministratorCanceled,
		TerminalCauseSurfacePolicy:
		return true
	default:
		return false
	}
}

func canonicalJSON(raw json.RawMessage, fallback string) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		if fallback == "" {
			return nil, errors.New("value is required")
		}
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
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

func resolutionDigest(params ResolveInteractionParams, payload json.RawMessage) string {
	canonical := mustJSON(map[string]any{
		"interaction_id":                params.InteractionID,
		"expected_interaction_revision": params.ExpectedInteractionRevision,
		"presented_projection_revision": params.PresentedProjectionRevision,
		"participant_scope":             params.ParticipantScope,
		"participant_ref":               params.ParticipantRef,
		"participant_authority":         params.ParticipantAuthority,
		"participant_assurance":         params.ParticipantAssurance,
		"response_kind":                 params.ResponseKind,
		"response_payload":              payload,
		"source_draft_revision":         params.SourceDraftRevision,
	})
	sum := sha256.Sum256([]byte(canonical))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func changedOne(result sql.Result) bool {
	count, err := result.RowsAffected()
	return err == nil && count == 1
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}

func closeRows(rows *sql.Rows) {
	_ = rows.Close()
}
