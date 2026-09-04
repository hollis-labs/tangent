package interaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/tangent/internal/telemetry"
)

var (
	ErrUnauthorized = errors.New("interaction service: requester is not authorized")
	ErrWaitTimeout  = errors.New("interaction service: await duration elapsed")
)

const (
	defaultAwaitPollInterval = 100 * time.Millisecond
	defaultMaximumAwait      = 50 * time.Second
)

type Service struct {
	store        *Store
	catalog      DefinitionCatalog
	privileged   PrivilegedActorPolicy
	delivery     DeliveryWorkerPolicy
	surfaces     SurfaceAccessPolicy
	pollInterval time.Duration
	maximumAwait time.Duration
	// observer records refused draft revisions. It is the only telemetry this
	// service emits: everything else about an interaction's lifecycle is
	// observed by the adapter that owns the invocation, which is the one that
	// knows the room, the envelope, and the caller's mode.
	observer *telemetry.Recorder
}

type ServiceOption func(*Service)

// PrivilegedActorPolicy binds host-policy and administrator capabilities to
// trusted, adapter-established actors. The service never infers either power
// from a caller-controlled string.
type PrivilegedActorPolicy interface {
	AuthorizeHostPolicy(ActorBinding) bool
	AuthorizeAdministrator(ActorBinding) bool
}

type denyPrivilegedActors struct{}

func (denyPrivilegedActors) AuthorizeHostPolicy(ActorBinding) bool    { return false }
func (denyPrivilegedActors) AuthorizeAdministrator(ActorBinding) bool { return false }

// DeliveryWorkerPolicy authorizes trusted destination adapters. Direct MCP
// callers never receive this authority.
type DeliveryWorkerPolicy interface {
	AuthorizeDeliveryWorker(ActorBinding) bool
}

type denyDeliveryWorkers struct{}

func (denyDeliveryWorkers) AuthorizeDeliveryWorker(ActorBinding) bool { return false }

// AuthorizesDeliveryWorker asks the installed policy whether an actor may
// deliver terminal outcomes, without performing or claiming a delivery.
//
// It exists for readiness reporting. A build whose policy denies the
// in-process caller-pull adapter still accepts participant resolutions and
// still answers /healthz — it simply never hands an outcome back, which is the
// failure mode that looks healthiest from outside and therefore the one worth
// being able to ask about directly.
func (s *Service) AuthorizesDeliveryWorker(actor ActorBinding) bool {
	return s.delivery.AuthorizeDeliveryWorker(actor)
}

// SurfaceAccessPolicy lets a host reserve named surfaces without teaching the
// generic interaction service any workflow-specific IDs. Capabilities are
// application-internal and must never be accepted from untrusted wire input.
type SurfaceAccessPolicy interface {
	Authorize(surfaceID string, capability string) bool
}

type allowSurfaceAccess struct{}

func (allowSurfaceAccess) Authorize(string, string) bool { return true }

func WithPrivilegedActorPolicy(policy PrivilegedActorPolicy) ServiceOption {
	return func(service *Service) {
		if policy != nil {
			service.privileged = policy
		}
	}
}

func WithDeliveryWorkerPolicy(policy DeliveryWorkerPolicy) ServiceOption {
	return func(service *Service) {
		if policy != nil {
			service.delivery = policy
		}
	}
}

func WithSurfaceAccessPolicy(policy SurfaceAccessPolicy) ServiceOption {
	return func(service *Service) {
		if policy != nil {
			service.surfaces = policy
		}
	}
}

// WithTelemetry installs the correlation recorder. A nil recorder is a no-op.
func WithTelemetry(recorder *telemetry.Recorder) ServiceOption {
	return func(service *Service) {
		if recorder != nil {
			service.observer = recorder
		}
	}
}

func WithAwaitPollInterval(interval time.Duration) ServiceOption {
	return func(service *Service) { service.pollInterval = interval }
}

func WithMaximumAwait(duration time.Duration) ServiceOption {
	return func(service *Service) { service.maximumAwait = duration }
}

func NewService(store *Store, catalog DefinitionCatalog, options ...ServiceOption) (*Service, error) {
	if store == nil || store.db == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidRecord)
	}
	if catalog == nil {
		return nil, fmt.Errorf("%w: definition catalog is required", ErrInvalidRecord)
	}
	service := &Service{
		store: store, catalog: catalog,
		privileged:   denyPrivilegedActors{},
		delivery:     denyDeliveryWorkers{},
		surfaces:     allowSurfaceAccess{},
		pollInterval: defaultAwaitPollInterval, maximumAwait: defaultMaximumAwait,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	if service.pollInterval <= 0 || service.maximumAwait <= 0 {
		return nil, fmt.Errorf("%w: await intervals must be positive", ErrInvalidRecord)
	}
	return service, nil
}

type ActorBinding struct {
	Scope        string `json:"scope"`
	PrincipalRef string `json:"principal_ref,omitempty"`
	Authority    string `json:"authority"`
	Assurance    string `json:"assurance"`
}

type SurfaceHandle struct {
	SurfaceID string       `json:"surface_id"`
	State     SurfaceState `json:"state"`
	Revision  int64        `json:"revision"`
	Created   bool         `json:"created"`
}

type InteractionHandle struct {
	SurfaceID       string           `json:"surface_id"`
	InteractionID   string           `json:"interaction_id"`
	State           InteractionState `json:"state"`
	Revision        int64            `json:"revision"`
	SurfaceSequence int64            `json:"surface_sequence"`
	Created         bool             `json:"created"`
}

type OpenSurfaceInput struct {
	ID             string          `json:"surface_id,omitempty"`
	Caller         ActorBinding    `json:"caller"`
	IdempotencyKey string          `json:"idempotency_key"`
	OwnerScope     string          `json:"owner_scope"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Policy         json.RawMessage `json:"policy,omitempty"`
	Capability     string          `json:"-"`
}

func (s *Service) ListInteractionKinds(ctx context.Context) ([]InteractionKind, error) {
	return s.catalog.ListInteractionKinds(ctx)
}

func (s *Service) ResolveInteractionDefinition(
	ctx context.Context,
	ref DefinitionRef,
) (DefinitionBinding, error) {
	return s.catalog.ResolveInteractionDefinition(ctx, ref)
}

func (s *Service) OpenSurface(ctx context.Context, input OpenSurfaceInput) (SurfaceHandle, error) {
	if err := validateActor(input.Caller); err != nil {
		return SurfaceHandle{}, err
	}
	if input.OwnerScope == "" || input.IdempotencyKey == "" {
		return SurfaceHandle{}, fmt.Errorf("%w: owner scope and idempotency key are required", ErrInvalidRecord)
	}
	if !s.surfaces.Authorize(input.ID, input.Capability) {
		return SurfaceHandle{}, ErrUnauthorized
	}
	// The owner scope reaching here is host-derived. ADR 0004 §8 puts that
	// derivation in the adapter rather than in this service, because an
	// in-process host legitimately opens a surface owned by a scope other than
	// its own — `surface_hitl_default` is operator-owned and caller-opened —
	// while no wire request may. internal/mcp never forwards a caller-supplied
	// `owner_scope` into this field; it keeps it as an attribution label.
	request, err := json.Marshal(struct {
		OwnerScope string          `json:"owner_scope"`
		Metadata   json.RawMessage `json:"metadata"`
		Policy     json.RawMessage `json:"policy"`
	}{OwnerScope: input.OwnerScope, Metadata: defaultJSON(input.Metadata), Policy: defaultJSON(input.Policy)})
	if err != nil {
		return SurfaceHandle{}, fmt.Errorf("marshal surface open request: %w", err)
	}
	result, err := s.store.OpenSurface(ctx, OpenSurfaceParams{
		ID: input.ID, CallerScope: input.Caller.Scope, IdempotencyKey: input.IdempotencyKey,
		OwnerScope: input.OwnerScope, Metadata: input.Metadata, Policy: input.Policy,
		RequestSnapshot: request, ActorRef: input.Caller.PrincipalRef, Authority: input.Caller.Authority,
	})
	if err != nil {
		return SurfaceHandle{}, err
	}
	return surfaceHandle(result.Surface, result.Created), nil
}

type SubmitInteractionInput struct {
	ID             string          `json:"interaction_id,omitempty"`
	SurfaceID      string          `json:"surface_id"`
	Caller         ActorBinding    `json:"caller"`
	IdempotencyKey string          `json:"idempotency_key"`
	Definition     DefinitionRef   `json:"definition"`
	Request        json.RawMessage `json:"request"`
	ExternalRefs   json.RawMessage `json:"external_refs,omitempty"`
	Policy         json.RawMessage `json:"policy,omitempty"`
	Capability     string          `json:"-"`

	// LegacyRoomID / LegacyEnvelopeID correlate the canonical interaction with
	// the v0.12 room projection that presents it. They never participate in
	// idempotency identity or authorization.
	LegacyRoomID     string `json:"-"`
	LegacyEnvelopeID string `json:"-"`
}

func (s *Service) SubmitInteraction(
	ctx context.Context,
	input SubmitInteractionInput,
) (InteractionHandle, error) {
	if err := validateActor(input.Caller); err != nil {
		return InteractionHandle{}, err
	}
	if input.SurfaceID == "" || input.IdempotencyKey == "" {
		return InteractionHandle{}, fmt.Errorf("%w: surface and idempotency key are required", ErrInvalidRecord)
	}
	if !s.surfaces.Authorize(input.SurfaceID, input.Capability) {
		return InteractionHandle{}, ErrUnauthorized
	}
	// Creating work on an *existing* surface is a distinct power from reading
	// it, and until now it was the unguarded one: SubmitInteraction checked
	// only the surface access policy, so any caller could submit onto another
	// caller's surface.
	//
	// The check applies to wire-driven submits. An in-process adapter presents
	// a host-internal capability that no request can spell — the same seam
	// SurfaceAccessPolicy already relies on — and carries the host's own
	// authority over surfaces it opened itself; the operator inbox is
	// reachable exactly that way, which is what ADR 0004 §7 means by a caller
	// holding `submit` on the operator inbox surface.
	if input.Capability == "" {
		if err := s.authorizeSurfaceSubmit(ctx, input.SurfaceID, input.Caller.Scope); err != nil {
			return InteractionHandle{}, err
		}
	}
	request, err := canonicalJSON(input.Request, "")
	if err != nil {
		return InteractionHandle{}, fmt.Errorf("%w: request snapshot: %w", ErrInvalidRecord, err)
	}
	existing, found, err := s.store.GetInteractionByIdempotency(ctx, input.Caller.Scope, input.IdempotencyKey)
	if err != nil {
		return InteractionHandle{}, err
	}
	if found {
		if !bytes.Equal(existing.RequestSnapshot, request) {
			return InteractionHandle{}, ErrIdempotencyConflict
		}
		return interactionHandle(existing, false), nil
	}
	input.Request = request
	binding, err := s.catalog.ResolveInteractionDefinition(ctx, input.Definition)
	if err != nil {
		return InteractionHandle{}, err
	}
	if validationErr := s.catalog.ValidateInteractionRequest(ctx, binding, input.Request); validationErr != nil {
		return InteractionHandle{}, validationErr
	}
	// Retain the exact material this binding was cut from before the record
	// that pins it exists. ADR 0001 §3 requires an interaction to pin "the
	// exact definition Tangent used", which is only true for as long as those
	// bytes can still be found: a later release that bumps this kind's version
	// would otherwise leave the record pinned to material nothing holds.
	//
	// Deliberately outside the record transaction and deliberately first. The
	// row is content-addressed on the binding digest, so a crash between the
	// two writes leaves an orphan material row — harmless, and reused verbatim
	// by the retry. The reverse order would leave a record whose pin cannot be
	// resolved, which is the failure this exists to prevent.
	if retainErr := s.catalog.RetainDefinitionMaterial(ctx, binding); retainErr != nil {
		return InteractionHandle{}, retainErr
	}
	result, err := s.store.CreateInteraction(ctx, CreateInteractionParams{
		ID: input.ID, SurfaceID: input.SurfaceID, CallerScope: input.Caller.Scope,
		CallerPrincipalRef: input.Caller.PrincipalRef, CallerAuthority: input.Caller.Authority,
		CallerAssurance: input.Caller.Assurance, IdempotencyKey: input.IdempotencyKey,
		Definition: binding, RequestSnapshot: input.Request, ExternalRefs: input.ExternalRefs,
		Policy: input.Policy, ActorRef: input.Caller.PrincipalRef, Authority: input.Caller.Authority,
		LegacyRoomID: input.LegacyRoomID, LegacyEnvelopeID: input.LegacyEnvelopeID,
	})
	if err != nil {
		return InteractionHandle{}, err
	}
	return interactionHandle(result.Interaction, result.Created), nil
}

type GetSurfaceInput struct {
	SurfaceID      string `json:"surface_id"`
	RequesterScope string `json:"requester_scope"`
	Capability     string `json:"-"`
}

func (s *Service) GetSurface(ctx context.Context, input GetSurfaceInput) (SurfaceSnapshot, error) {
	if !s.surfaces.Authorize(input.SurfaceID, input.Capability) {
		return SurfaceSnapshot{}, ErrUnauthorized
	}
	snapshot, err := s.store.HydrateSurface(ctx, input.SurfaceID)
	if err != nil {
		return SurfaceSnapshot{}, err
	}
	if !scopeMatches(input.RequesterScope, snapshot.Surface.OwnerScope) {
		openedByRequester, lookupErr := s.store.SurfaceWasOpenedByScope(ctx, input.SurfaceID, input.RequesterScope)
		if lookupErr != nil {
			return SurfaceSnapshot{}, lookupErr
		}
		if !openedByRequester {
			return SurfaceSnapshot{}, denial(input.RequesterScope, snapshot.Surface.OwnerScope)
		}
	}
	return snapshot, nil
}

// GetSurfaceInteractions applies the same authorization as GetSurface while
// returning the smaller atomic snapshot used by queue-style projections.
func (s *Service) GetSurfaceInteractions(
	ctx context.Context,
	input GetSurfaceInput,
) (SurfaceInteractionSnapshot, error) {
	if !s.surfaces.Authorize(input.SurfaceID, input.Capability) {
		return SurfaceInteractionSnapshot{}, ErrUnauthorized
	}
	snapshot, err := s.store.HydrateSurfaceInteractions(ctx, input.SurfaceID)
	if err != nil {
		return SurfaceInteractionSnapshot{}, err
	}
	if !scopeMatches(input.RequesterScope, snapshot.Surface.OwnerScope) {
		openedByRequester, lookupErr := s.store.SurfaceWasOpenedByScope(ctx, input.SurfaceID, input.RequesterScope)
		if lookupErr != nil {
			return SurfaceInteractionSnapshot{}, lookupErr
		}
		if !openedByRequester {
			return SurfaceInteractionSnapshot{}, denial(input.RequesterScope, snapshot.Surface.OwnerScope)
		}
	}
	return snapshot, nil
}

type GetInteractionInput struct {
	InteractionID        string          `json:"interaction_id"`
	RequesterScope       string          `json:"requester_scope"`
	TransportCorrelation json.RawMessage `json:"transport_correlation,omitempty"`
	Capability           string          `json:"-"`
}

type TerminalOutcome struct {
	Interaction          InteractionRecord               `json:"interaction"`
	Resolution           *ResolutionRecord               `json:"resolution,omitempty"`
	ResolutionDeliveries []ResolutionDeliveryRecord      `json:"resolution_deliveries,omitempty"`
	Notifications        []TerminalNotificationRecord    `json:"terminal_notifications,omitempty"`
	Retrieval            *TerminalOutcomeRetrievalRecord `json:"retrieval,omitempty"`
}

func (s *Service) GetInteraction(ctx context.Context, input GetInteractionInput) (TerminalOutcome, error) {
	return s.getInteraction(ctx, input, true)
}

// InspectInteraction returns the same durable aggregate as GetInteraction but
// does not record a terminal retrieval. Application services use it to decide
// whether to attempt a lifecycle command; only an actual caller Get/Await is a
// retrieval audit fact.
func (s *Service) InspectInteraction(ctx context.Context, input GetInteractionInput) (TerminalOutcome, error) {
	return s.getInteraction(ctx, input, false)
}

// FindInteractionByIdempotency exposes the generic store's scoped lookup to
// higher-level application services. The supplied actor establishes the exact
// caller scope; no transport identifier participates in this boundary.
func (s *Service) FindInteractionByIdempotency(
	ctx context.Context,
	caller ActorBinding,
	idempotencyKey string,
	capability string,
) (InteractionRecord, bool, error) {
	if err := validateActor(caller); err != nil {
		return InteractionRecord{}, false, err
	}
	record, found, err := s.store.GetInteractionByIdempotency(ctx, caller.Scope, idempotencyKey)
	if err != nil || !found {
		return record, found, err
	}
	if !s.surfaces.Authorize(record.SurfaceID, capability) {
		return InteractionRecord{}, false, ErrUnauthorized
	}
	return record, true, nil
}

// QueuePosition projects a one-based position for the supplied immutable
// interaction snapshot. Terminal snapshots intentionally return nil. This is
// not a second ordering authority: SurfaceSequence remains canonical.
func (s *Service) QueuePosition(
	ctx context.Context,
	record InteractionRecord,
) (*int64, error) {
	if isTerminalState(record.State) {
		return nil, nil
	}
	count, err := s.store.CountNonterminalInteractionsBefore(ctx, record.SurfaceID, record.SurfaceSequence)
	if err != nil {
		return nil, err
	}
	position := count + 1
	return &position, nil
}

func (s *Service) getInteraction(
	ctx context.Context,
	input GetInteractionInput,
	recordRetrieval bool,
) (TerminalOutcome, error) {
	interaction, err := s.store.GetInteraction(ctx, input.InteractionID)
	if err != nil {
		return TerminalOutcome{}, err
	}
	if !s.surfaces.Authorize(interaction.SurfaceID, input.Capability) {
		return TerminalOutcome{}, ErrUnauthorized
	}
	if err := s.authorizeInteraction(ctx, interaction, input.RequesterScope); err != nil {
		return TerminalOutcome{}, err
	}
	outcome := TerminalOutcome{Interaction: interaction}
	if !isTerminalState(interaction.State) {
		return outcome, nil
	}
	if interaction.State == InteractionStateResolved {
		resolution, err := s.store.GetResolution(ctx, interaction.ID)
		if err != nil {
			return TerminalOutcome{}, err
		}
		outcome.Resolution = &resolution
		deliveries, err := s.store.ListResolutionDeliveries(ctx, resolution.ID)
		if err != nil {
			return TerminalOutcome{}, err
		}
		outcome.ResolutionDeliveries = deliveries
	} else {
		notifications, err := s.store.ListTerminalNotifications(ctx, interaction.ID)
		if err != nil {
			return TerminalOutcome{}, err
		}
		outcome.Notifications = notifications
	}
	if recordRetrieval {
		retrieval, err := s.store.RecordTerminalOutcomeRetrieval(ctx, RecordTerminalOutcomeRetrievalParams{
			InteractionID: interaction.ID, RequesterScope: input.RequesterScope,
			TransportCorrelation: input.TransportCorrelation,
		})
		if err != nil {
			return TerminalOutcome{}, err
		}
		outcome.Retrieval = &retrieval
	}
	return outcome, nil
}

type PresentInteractionInput struct {
	InteractionID               string          `json:"interaction_id"`
	ExpectedRevision            int64           `json:"expected_revision"`
	PresentedProjectionRevision int64           `json:"presented_projection_revision"`
	Participant                 ActorBinding    `json:"participant"`
	ConnectionID                string          `json:"connection_id,omitempty"`
	Metadata                    json.RawMessage `json:"metadata,omitempty"`
	Capability                  string          `json:"-"`
}

func (s *Service) AcknowledgePresentation(
	ctx context.Context,
	input PresentInteractionInput,
) (InteractionHandle, error) {
	if err := validateActor(input.Participant); err != nil {
		return InteractionHandle{}, err
	}
	current, err := s.store.GetInteraction(ctx, input.InteractionID)
	if err != nil {
		return InteractionHandle{}, err
	}
	if !s.surfaces.Authorize(current.SurfaceID, input.Capability) {
		return InteractionHandle{}, ErrUnauthorized
	}
	record, err := s.store.AdvanceInteraction(ctx, AdvanceInteractionParams{
		InteractionID: input.InteractionID, ExpectedRevision: input.ExpectedRevision,
		To: InteractionStatePresented, ActorRef: input.Participant.PrincipalRef,
		Authority:                   input.Participant.Authority,
		PresentedProjectionRevision: input.PresentedProjectionRevision,
		ParticipantScope:            input.Participant.Scope, ParticipantRef: input.Participant.PrincipalRef,
		ParticipantAuthority: input.Participant.Authority, ParticipantAssurance: input.Participant.Assurance,
		ConnectionID: input.ConnectionID,
		Metadata:     input.Metadata,
	})
	if err != nil {
		return InteractionHandle{}, err
	}
	return interactionHandle(record, false), nil
}

type SaveDraftInput struct {
	InteractionID       string          `json:"interaction_id"`
	DraftRevision       int64           `json:"draft_revision"`
	InteractionRevision int64           `json:"interaction_revision"`
	Participant         ActorBinding    `json:"participant"`
	DefinitionVersion   string          `json:"definition_version"`
	Payload             json.RawMessage `json:"payload"`
	Sensitivity         string          `json:"sensitivity,omitempty"`
	ExpiresAt           *time.Time      `json:"expires_at,omitempty"`
	Capability          string          `json:"-"`
}

func (s *Service) SaveDraft(ctx context.Context, input SaveDraftInput) (DraftRevision, error) {
	if err := validateActor(input.Participant); err != nil {
		return DraftRevision{}, err
	}
	current, err := s.store.GetInteraction(ctx, input.InteractionID)
	if err != nil {
		return DraftRevision{}, err
	}
	if !actorMatchesParticipant(current, input.Participant) {
		return DraftRevision{}, ErrUnauthorized
	}
	if !s.surfaces.Authorize(current.SurfaceID, input.Capability) {
		return DraftRevision{}, ErrUnauthorized
	}
	draft, err := s.store.SaveDraftRevision(ctx, DraftRevision{
		InteractionID: input.InteractionID, Revision: input.DraftRevision,
		InteractionRevision:  input.InteractionRevision,
		ParticipantScope:     input.Participant.Scope,
		ParticipantRef:       input.Participant.PrincipalRef,
		ParticipantAuthority: input.Participant.Authority,
		ParticipantAssurance: input.Participant.Assurance,
		DefinitionVersion:    input.DefinitionVersion, Payload: input.Payload,
		Sensitivity: input.Sensitivity, ExpiresAt: input.ExpiresAt,
	})
	if err != nil {
		// A draft conflict is a refusal, not a failure: the participant's
		// browser held a revision the record has moved past, and the correct
		// response is to resynchronize and try again. The payload is not
		// touched here and cannot be — the observation carries revisions and a
		// code, which is the whole of what is safe to say about a draft.
		s.observer.Emit(ctx, telemetry.Event{
			Name:    telemetry.EventDraftRefused,
			Outcome: telemetry.OutcomeRefused,
			Code:    telemetry.CodeForError(err, draftErrorCodes),
			Correlation: telemetry.Correlation{
				Trace: telemetry.TraceForRecord(
					current.CallerScope, current.IdempotencyKey, current.ID),
				Span:              telemetry.NewSpanID(),
				SurfaceID:         current.SurfaceID,
				InteractionID:     current.ID,
				DefinitionKind:    current.Definition.Kind,
				DefinitionVersion: current.Definition.Version,
				CallerScope:       current.CallerScope,
				ParticipantScope:  input.Participant.Scope,
			},
			Attrs: []telemetry.Attr{
				telemetry.Int(telemetry.AttrRevision, current.Revision),
				telemetry.Int(telemetry.AttrExpectedRevision, input.InteractionRevision),
				telemetry.String(telemetry.AttrState, string(current.State)),
			},
		})
	}
	return draft, err
}

// draftErrorCodes maps the store's draft refusals onto typed codes. It is a
// table rather than a switch so a new sentinel that is not classified reaches
// telemetry as `unclassified` instead of as its message.
var draftErrorCodes = []telemetry.ErrorCode{
	{Sentinel: ErrRevisionConflict, Code: "revision_conflict"},
	{Sentinel: ErrTerminal, Code: "terminal"},
	{Sentinel: ErrNotRespondable, Code: "not_respondable"},
	{Sentinel: ErrUnauthorized, Code: "unauthorized"},
	{Sentinel: ErrNotFound, Code: "not_found"},
	{Sentinel: ErrInvalidRecord, Code: "invalid_record"},
}

type ResolveInteractionInput struct {
	InteractionID               string       `json:"interaction_id"`
	ExpectedInteractionRevision int64        `json:"expected_interaction_revision"`
	PresentedProjectionRevision int64        `json:"presented_projection_revision"`
	Participant                 ActorBinding `json:"participant"`
	ResponseKind                string       `json:"response_kind"`
	// ResponsePayload is what the durable resolution record stores and what
	// tangent.interaction_get and tangent.interaction_await hand back. Its
	// shape is the caller's: the room path stores the whole envelopes.Response
	// so a resolution replays as the frame the participant sent, while the
	// HITL path stores the bare typed response body.
	ResponsePayload json.RawMessage `json:"response_payload"`
	// ResponseBody is the contract-bearing value the definition's
	// `response_schema` validates — the response *payload*, matching what
	// upstream TypeSpec.PayloadSchema validates and what ADR 0003 §2.2 means
	// by "the terminal response payload".
	//
	// It is a separate field because ResponsePayload's shape is not the same
	// across callers, and one field cannot be both the durable record and the
	// validated contract without those two callers agreeing. They do not, and
	// the disagreement was invisible while tangent.hitl-item was the only kind
	// carrying a response schema: the room path validated a whole
	// envelopes.Response object against a schema describing a payload, which
	// no shipped schema was ever applied to. CW-20260825-0074's form-collect
	// backfill is what surfaced it.
	//
	// Empty falls back to ResponsePayload, which preserves the behavior of
	// every caller that does not distinguish the two.
	ResponseBody        json.RawMessage            `json:"response_body,omitempty"`
	SourceDraftRevision *int64                     `json:"source_draft_revision,omitempty"`
	SubmittedAt         time.Time                  `json:"submitted_at,omitempty"`
	Deliveries          []ResolutionDeliveryParams `json:"deliveries,omitempty"`
	Capability          string                     `json:"-"`
}

func (s *Service) ResolveInteraction(
	ctx context.Context,
	input ResolveInteractionInput,
) (ResolveInteractionResult, error) {
	if err := validateActor(input.Participant); err != nil {
		return ResolveInteractionResult{}, err
	}
	current, err := s.store.GetInteraction(ctx, input.InteractionID)
	if err != nil {
		return ResolveInteractionResult{}, err
	}
	if !actorMatchesParticipant(current, input.Participant) {
		return ResolveInteractionResult{}, ErrUnauthorized
	}
	if !s.surfaces.Authorize(current.SurfaceID, input.Capability) {
		return ResolveInteractionResult{}, ErrUnauthorized
	}
	body := input.ResponseBody
	if len(body) == 0 {
		body = input.ResponsePayload
	}
	if err := s.catalog.ValidateInteractionResponse(ctx, current.Definition, input.ResponseKind, body); err != nil {
		return ResolveInteractionResult{}, err
	}
	deliveries := input.Deliveries
	if len(deliveries) == 0 {
		deliveries = []ResolutionDeliveryParams{callerPullResolutionDelivery(current)}
	}
	return s.store.ResolveInteraction(ctx, ResolveInteractionParams{
		InteractionID:               input.InteractionID,
		ExpectedInteractionRevision: input.ExpectedInteractionRevision,
		PresentedProjectionRevision: input.PresentedProjectionRevision,
		ParticipantScope:            input.Participant.Scope,
		ParticipantRef:              input.Participant.PrincipalRef,
		ParticipantAuthority:        input.Participant.Authority,
		ParticipantAssurance:        input.Participant.Assurance,
		ResponseKind:                input.ResponseKind, ResponsePayload: input.ResponsePayload,
		SourceDraftRevision: input.SourceDraftRevision, SubmittedAt: input.SubmittedAt,
		Deliveries: deliveries,
	})
}

type CancelInteractionInput struct {
	InteractionID    string        `json:"interaction_id"`
	ExpectedRevision int64         `json:"expected_revision"`
	Requester        ActorBinding  `json:"requester"`
	Cause            TerminalCause `json:"cause"`
	Reason           string        `json:"reason,omitempty"`
	Capability       string        `json:"-"`
}

func (s *Service) CancelInteraction(
	ctx context.Context,
	input CancelInteractionInput,
) (TerminalizeInteractionResult, error) {
	if err := validateActor(input.Requester); err != nil {
		return TerminalizeInteractionResult{}, err
	}
	current, err := s.store.GetInteraction(ctx, input.InteractionID)
	if err != nil {
		return TerminalizeInteractionResult{}, err
	}
	if !s.surfaces.Authorize(current.SurfaceID, input.Capability) {
		return TerminalizeInteractionResult{}, ErrUnauthorized
	}
	authorized := false
	switch input.Cause {
	case TerminalCauseCallerWithdrawn, TerminalCauseCallerCanceled:
		authorized = input.Requester.Scope == current.CallerScope
	case TerminalCauseParticipantCanceled:
		authorized = actorMatchesParticipant(current, input.Requester)
	case TerminalCauseAdministratorCanceled:
		authorized = s.privileged.AuthorizeAdministrator(input.Requester)
	case TerminalCauseSurfacePolicy:
		// Surface-policy cancellation belongs exclusively to CloseSurface's
		// aggregate transaction, never to this single-interaction operation.
		authorized = false
	}
	if !authorized {
		return TerminalizeInteractionResult{}, ErrUnauthorized
	}
	return s.store.TerminalizeInteraction(ctx, TerminalizeInteractionParams{
		InteractionID: input.InteractionID, ExpectedRevision: input.ExpectedRevision,
		To: InteractionStateCanceled, Cause: input.Cause, Reason: input.Reason,
		ActorRef: input.Requester.PrincipalRef, Authority: input.Requester.Authority,
		Notifications: []TerminalNotificationParams{callerPullTerminalNotification(current, "cancel")},
	})
}

type SupersedeInteractionInput struct {
	InteractionID            string       `json:"interaction_id"`
	ExpectedRevision         int64        `json:"expected_revision"`
	ReplacementInteractionID string       `json:"replacement_interaction_id"`
	Requester                ActorBinding `json:"requester"`
	Reason                   string       `json:"reason,omitempty"`
	Capability               string       `json:"-"`
}

func (s *Service) SupersedeInteraction(
	ctx context.Context,
	input SupersedeInteractionInput,
) (TerminalizeInteractionResult, error) {
	if err := validateActor(input.Requester); err != nil {
		return TerminalizeInteractionResult{}, err
	}
	if input.InteractionID == input.ReplacementInteractionID {
		return TerminalizeInteractionResult{}, fmt.Errorf("%w: interaction cannot supersede itself", ErrInvalidRecord)
	}
	current, err := s.store.GetInteraction(ctx, input.InteractionID)
	if err != nil {
		return TerminalizeInteractionResult{}, err
	}
	if !s.surfaces.Authorize(current.SurfaceID, input.Capability) {
		return TerminalizeInteractionResult{}, ErrUnauthorized
	}
	replacement, err := s.store.GetInteraction(ctx, input.ReplacementInteractionID)
	if err != nil {
		return TerminalizeInteractionResult{}, err
	}
	if input.Requester.Scope != current.CallerScope || replacement.CallerScope != current.CallerScope ||
		replacement.SurfaceID != current.SurfaceID {
		return TerminalizeInteractionResult{}, ErrUnauthorized
	}
	if isTerminalState(replacement.State) {
		return TerminalizeInteractionResult{}, ErrNotRespondable
	}
	return s.store.TerminalizeInteraction(ctx, TerminalizeInteractionParams{
		InteractionID: input.InteractionID, ExpectedRevision: input.ExpectedRevision,
		To: InteractionStateSuperseded, ReplacementInteractionID: replacement.ID,
		Reason: input.Reason, ActorRef: input.Requester.PrincipalRef,
		Authority:     input.Requester.Authority,
		Notifications: []TerminalNotificationParams{callerPullTerminalNotification(current, "supersede")},
	})
}

type ExpireInteractionInput struct {
	InteractionID    string       `json:"interaction_id"`
	ExpectedRevision int64        `json:"expected_revision"`
	PolicyRef        string       `json:"policy_ref"`
	Reason           string       `json:"reason,omitempty"`
	Actor            ActorBinding `json:"actor"`
	Capability       string       `json:"-"`
}

// ExpireInteraction is a host-policy operation and is intentionally not
// exposed by the direct caller MCP adapter.
func (s *Service) ExpireInteraction(
	ctx context.Context,
	input ExpireInteractionInput,
) (TerminalizeInteractionResult, error) {
	if err := validateActor(input.Actor); err != nil {
		return TerminalizeInteractionResult{}, err
	}
	if !s.privileged.AuthorizeHostPolicy(input.Actor) && !s.privileged.AuthorizeAdministrator(input.Actor) {
		return TerminalizeInteractionResult{}, ErrUnauthorized
	}
	current, err := s.store.GetInteraction(ctx, input.InteractionID)
	if err != nil {
		return TerminalizeInteractionResult{}, err
	}
	if !s.surfaces.Authorize(current.SurfaceID, input.Capability) {
		return TerminalizeInteractionResult{}, ErrUnauthorized
	}
	return s.store.TerminalizeInteraction(ctx, TerminalizeInteractionParams{
		InteractionID: input.InteractionID, ExpectedRevision: input.ExpectedRevision,
		To: InteractionStateExpired, PolicyRef: input.PolicyRef, Reason: input.Reason,
		ActorRef: input.Actor.PrincipalRef, Authority: input.Actor.Authority,
		Notifications: []TerminalNotificationParams{callerPullTerminalNotification(current, "expire")},
	})
}

type CloseSurfaceInput struct {
	SurfaceID        string          `json:"surface_id"`
	ExpectedRevision int64           `json:"expected_revision"`
	Requester        ActorBinding    `json:"requester"`
	Reason           string          `json:"reason,omitempty"`
	PolicyRef        string          `json:"policy_ref"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	Capability       string          `json:"-"`
}

func (s *Service) CloseSurface(ctx context.Context, input CloseSurfaceInput) (CloseSurfaceResult, error) {
	if err := validateActor(input.Requester); err != nil {
		return CloseSurfaceResult{}, err
	}
	if !s.surfaces.Authorize(input.SurfaceID, input.Capability) {
		return CloseSurfaceResult{}, ErrUnauthorized
	}
	surface, err := s.store.GetSurface(ctx, input.SurfaceID)
	if err != nil {
		return CloseSurfaceResult{}, err
	}
	// Close is partition-scoped even inside `standalone-local`, where reads are
	// authority-wide. Closing dispositions other partitions' outstanding human
	// work, so the advisory boundary is enforced for the destructive operation
	// and only for it (ADR 0004 §9).
	if !scopeMatches(input.Requester.Scope, surface.OwnerScope) {
		return CloseSurfaceResult{}, denial(input.Requester.Scope, surface.OwnerScope)
	}
	return s.store.CloseSurface(ctx, CloseSurfaceParams{
		SurfaceID: input.SurfaceID, ExpectedRevision: input.ExpectedRevision,
		Reason: input.Reason, PolicyRef: input.PolicyRef,
		ActorRef: input.Requester.PrincipalRef, Authority: input.Requester.Authority,
		EventMetadata:      input.Metadata,
		NotificationPolicy: json.RawMessage(`{"delivery":"durable-caller-pull"}`),
	})
}

type AwaitResolutionInput struct {
	InteractionID        string          `json:"interaction_id"`
	RequesterScope       string          `json:"requester_scope"`
	MaximumWait          time.Duration   `json:"-"`
	MaximumWaitMillis    int64           `json:"maximum_wait_ms,omitempty"`
	TransportCorrelation json.RawMessage `json:"transport_correlation,omitempty"`
	Capability           string          `json:"-"`
}

func (s *Service) AwaitResolution(ctx context.Context, input AwaitResolutionInput) (TerminalOutcome, error) {
	duration := input.MaximumWait
	if duration == 0 && input.MaximumWaitMillis > 0 {
		if input.MaximumWaitMillis > int64(s.maximumAwait/time.Millisecond) {
			return TerminalOutcome{}, fmt.Errorf(
				"%w: maximum wait must not exceed %s",
				ErrInvalidRecord,
				s.maximumAwait,
			)
		}
		duration = time.Duration(input.MaximumWaitMillis) * time.Millisecond
	}
	if duration <= 0 || duration > s.maximumAwait {
		return TerminalOutcome{}, fmt.Errorf(
			"%w: maximum wait must be between 1ns and %s",
			ErrInvalidRecord,
			s.maximumAwait,
		)
	}
	waitCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		outcome, err := s.getInteraction(waitCtx, GetInteractionInput{
			InteractionID: input.InteractionID, RequesterScope: input.RequesterScope,
			TransportCorrelation: input.TransportCorrelation, Capability: input.Capability,
		}, false)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return TerminalOutcome{}, ErrWaitTimeout
			}
			return TerminalOutcome{}, err
		}
		if isTerminalState(outcome.Interaction.State) {
			// The bounded wait has completed successfully. Record retrieval under
			// the caller context so a deadline firing between the observing read
			// and the audit insert cannot hide an already-observed terminal fact.
			return s.getInteraction(ctx, GetInteractionInput{
				InteractionID: input.InteractionID, RequesterScope: input.RequesterScope,
				TransportCorrelation: input.TransportCorrelation, Capability: input.Capability,
			}, true)
		}
		select {
		case <-ctx.Done():
			return TerminalOutcome{}, ctx.Err()
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return TerminalOutcome{}, ctx.Err()
			}
			return TerminalOutcome{}, ErrWaitTimeout
		case <-ticker.C:
		}
	}
}

// authorizeSurfaceSubmit answers whether a caller may create an interaction on
// a surface it does not necessarily own. The rule is the read rule: the
// surface's owner, or a caller that opened it — the existing
// SurfaceWasOpenedByScope relation, reused rather than replaced.
func (s *Service) authorizeSurfaceSubmit(ctx context.Context, surfaceID, callerScope string) error {
	surface, err := s.store.GetSurface(ctx, surfaceID)
	if err != nil {
		return err
	}
	if scopeMatches(callerScope, surface.OwnerScope) {
		return nil
	}
	openedByCaller, lookupErr := s.store.SurfaceWasOpenedByScope(ctx, surfaceID, callerScope)
	if lookupErr != nil {
		return lookupErr
	}
	if openedByCaller {
		return nil
	}
	return denial(callerScope, surface.OwnerScope)
}

func (s *Service) authorizeInteraction(
	ctx context.Context,
	interaction InteractionRecord,
	requesterScope string,
) error {
	if requesterScope == "" {
		return ErrUnauthorized
	}
	if scopeMatches(requesterScope, interaction.CallerScope) {
		return nil
	}
	surface, err := s.store.GetSurface(ctx, interaction.SurfaceID)
	if err != nil {
		return err
	}
	if !scopeMatches(requesterScope, surface.OwnerScope) {
		return denial(requesterScope, surface.OwnerScope)
	}
	return nil
}

func validateActor(actor ActorBinding) error {
	if actor.Scope == "" || actor.PrincipalRef == "" || actor.Authority == "" || actor.Assurance == "" {
		return fmt.Errorf("%w: complete actor binding is required", ErrInvalidRecord)
	}
	return nil
}

func actorMatchesParticipant(interaction InteractionRecord, actor ActorBinding) bool {
	return interaction.ParticipantScope != "" && interaction.ParticipantScope == actor.Scope &&
		interaction.ParticipantRef == actor.PrincipalRef &&
		interaction.ParticipantAuthority == actor.Authority &&
		interaction.ParticipantAssurance == actor.Assurance
}

func surfaceHandle(surface SurfaceRecord, created bool) SurfaceHandle {
	return SurfaceHandle{SurfaceID: surface.ID, State: surface.State, Revision: surface.Revision, Created: created}
}

func interactionHandle(interaction InteractionRecord, created bool) InteractionHandle {
	return InteractionHandle{
		SurfaceID: interaction.SurfaceID, InteractionID: interaction.ID,
		State: interaction.State, Revision: interaction.Revision,
		SurfaceSequence: interaction.SurfaceSequence, Created: created,
	}
}

func defaultJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	return value
}

func callerPullResolutionDelivery(interaction InteractionRecord) ResolutionDeliveryParams {
	return ResolutionDeliveryParams{
		DestinationBinding: json.RawMessage(mustJSON(map[string]any{
			"kind": "caller_pull", "caller_scope": interaction.CallerScope,
		})),
		IdempotencyKey: fmt.Sprintf("resolution:%s:%d", interaction.ID, interaction.Revision),
		Policy:         json.RawMessage(`{"delivery":"durable-caller-pull"}`),
	}
}

func callerPullTerminalNotification(
	interaction InteractionRecord,
	operation string,
) TerminalNotificationParams {
	return TerminalNotificationParams{
		DestinationBinding: json.RawMessage(mustJSON(map[string]any{
			"kind": "caller_pull", "caller_scope": interaction.CallerScope,
		})),
		IdempotencyKey: fmt.Sprintf("%s:%s:%d", operation, interaction.ID, interaction.Revision),
		Policy:         json.RawMessage(`{"delivery":"durable-caller-pull"}`),
	}
}

// RetainedDefinitionCount reports how many definitions have durable retained
// material — the coverage of the replay guarantee in ADR 0001 §3. Surfaced
// through registry diagnostics so an operator can see that the pins in the
// record store still have something to resolve against.
func (s *Service) RetainedDefinitionCount(ctx context.Context) (int64, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("%w: store is required", ErrInvalidRecord)
	}
	return s.store.CountRetainedDefinitions(ctx)
}
