// Package roomflow routes Tangent's named room-backed workflows through the
// canonical durable interaction substrate while preserving their v0.12 public
// response shape.
//
// The problem it exists to solve: every named workflow used to block a single
// MCP request on a process-local channel until a human answered. A caller whose
// transport expired first lost the result outright — the operator's answer was
// recorded in SQLite, and the caller had no handle with which to go find it.
// Worse, the expiring context terminalized the request, so the outcome the
// human had already produced could be overwritten by a timeout.
//
// The shape here separates the three facts that used to be conflated:
//
//  1. The request exists. A durable interaction is created before the caller
//     can possibly lose it, keyed by caller scope + workflow kind + envelope id,
//     so an identical retry always finds the same one.
//  2. The human answered. The browser's submission becomes an immutable
//     resolution before any projection, channel, or waiting caller observes it.
//  3. The caller is still listening. This is the only fact a transport timeout
//     can change. When the compatibility window elapses the caller receives a
//     successful pending receipt carrying its durable handle — never an error,
//     and never a cancellation.
package roomflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/room"
)

// CompatibilityWindow is how long a wait-mode call blocks before returning a
// durable pending receipt.
//
// It sits deliberately under the 60s HTTP write timeout in internal/server so
// the receipt is written while the response is still writable. It also sits
// under the interaction service's 50s maximum bounded await, so one wait is
// always one await.
const CompatibilityWindow = 45 * time.Second

// Completion modes. Wait is the default so third-party callers written against
// v0.12 keep their exact behavior for fast interactions.
const (
	ModeWait  = "wait"
	ModeAsync = "async"
)

// StatusPending is the receipt discriminator callers branch on.
const StatusPending = "pending"

// surfaceCapability names roomflow's in-process authority over room-backed
// surfaces. It is never accepted from wire input.
const surfaceCapability = "tangent:room-workflow-adapter:v1"

// workflowIdempotencyPrefix namespaces the scoped workflow identity so it can
// never collide with the migration-imported v0.12 keys.
const workflowIdempotencyPrefix = "workflow:"

// ErrRoomNotFound is returned when the named room has no live presentation
// container. It is never returned for a request Tangent already owns: an
// interaction outlives the room that showed it.
var ErrRoomNotFound = errors.New("roomflow: room not found")

// DefaultCaller is the caller identity Tangent records for a direct loopback
// MCP call that declares no application id.
//
// `standalone-local` is no longer a legacy fallback string: ADR 0004 §3 makes
// it the real, host-assigned authority for every unauthenticated loopback
// caller, and `anonymous` is a real partition rather than a missing one. The
// authority is assigned from admission facts and can never be spelled by a
// caller; the assurance stays explicitly unverified, because nothing about a
// loopback call proves who made it.
//
// Records written before this grammar — migration 0003 backfilled every
// imported v0.12 room at bare `standalone-local` — read through the fixed
// alias in internal/authz. Nothing rewrites them.
var DefaultCaller = interaction.ActorBinding{
	Scope:        authz.AuthorityStandaloneLocal + ":" + authz.PartitionAnonymous,
	PrincipalRef: "loopback-mcp-caller",
	Authority:    authz.AuthorityStandaloneLocal,
	Assurance:    "loopback-unverified",
}

// Participant is the local operator acting through a room's browser tab.
//
// It is the template a minted participant session instantiates, not the
// identity itself: the session is the identity (ADR 0004 §4.3). The scope is
// unchanged so that a resolution recorded before participant sessions existed
// and one recorded after name the same principal.
var Participant = interaction.ActorBinding{
	Scope:        authz.ParticipantScope,
	PrincipalRef: "local-operator",
	Authority:    "tangent-loopback",
	Assurance:    "loopback-unverified",
}

// DeliveryWorker is the in-process destination adapter that hands terminal
// outcomes back over the caller's own MCP request. It is a host actor, not a
// caller: no wire input can assume this identity.
var DeliveryWorker = interaction.ActorBinding{
	Scope:        "tangent:room-workflow-delivery",
	PrincipalRef: "in-process-caller-pull",
	Authority:    "tangent-host",
	Assurance:    "in-process",
}

// DeliveryWorkerPolicy authorizes exactly the in-process caller-pull adapter.
type DeliveryWorkerPolicy struct{}

// AuthorizeDeliveryWorker implements interaction.DeliveryWorkerPolicy.
func (DeliveryWorkerPolicy) AuthorizeDeliveryWorker(actor interaction.ActorBinding) bool {
	return actor == DeliveryWorker
}

// WorkflowIdempotencyKey derives the durable identity of one named workflow
// invocation. Combined with the caller scope the store already keys on, the
// identity is exactly caller scope + workflow kind + envelope id: the room is
// deliberately not part of it, so the same logical request routed to a
// different room is still the same request.
func WorkflowIdempotencyKey(workflowKind, envelopeID string) string {
	return workflowIdempotencyPrefix + workflowKind + ":" + envelopeID
}

// Normalizer supplies the workflow-specific parts of the contract that live in
// the MCP layer: the pinned response kind for an envelope type, and the
// validation/normalization a participant response goes through before it can
// become an immutable resolution.
type Normalizer interface {
	// PinnedResponseKind returns the response kind the envelope registry pins
	// for a type. It is what the interaction catalog validates a resolution
	// against, and it is stable for the life of a definition binding.
	PinnedResponseKind(envelopeType string) (string, error)

	// NormalizeResponse validates and normalizes one participant response.
	// Returning an error rejects the submission without terminalizing.
	NormalizeResponse(
		roomID string,
		env *envelopes.Envelope,
		resp *envelopes.Response,
	) (*envelopes.Response, error)
}

// Service is the shared compatibility adapter every named room workflow and
// tangent.session_advance routes through.
type Service struct {
	interactions *interaction.Service
	rooms        *room.Manager
	normalizer   Normalizer
	roomURLBase  string
	window       time.Duration
	logger       *slog.Logger
}

// Option customizes a Service.
type Option func(*Service)

// WithCompatibilityWindow overrides the wait-mode window. Tests compress it;
// production uses CompatibilityWindow.
func WithCompatibilityWindow(window time.Duration) Option {
	return func(s *Service) {
		if window > 0 {
			s.window = window
		}
	}
}

// WithLogger sets the structured logger.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// New constructs the adapter. All three dependencies are required: without the
// durable service there is nothing to be authoritative, without the room
// manager there is nowhere to present, and without the normalizer a
// participant response cannot be validated before it becomes immutable.
func New(
	interactions *interaction.Service,
	rooms *room.Manager,
	normalizer Normalizer,
	roomURLBase string,
	options ...Option,
) (*Service, error) {
	if interactions == nil {
		return nil, fmt.Errorf("roomflow: interaction service is required")
	}
	if rooms == nil {
		return nil, fmt.Errorf("roomflow: room manager is required")
	}
	if normalizer == nil {
		return nil, fmt.Errorf("roomflow: response normalizer is required")
	}
	service := &Service{
		interactions: interactions, rooms: rooms, normalizer: normalizer,
		roomURLBase: roomURLBase, window: CompatibilityWindow, logger: slog.Default(),
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

// Handle is the durable identity a caller needs to come back for a result.
type Handle struct {
	SurfaceID     string `json:"surface_id"`
	InteractionID string `json:"interaction_id"`
	RoomID        string `json:"room_id"`
	EnvelopeID    string `json:"envelope_id"`
	URL           string `json:"url"`
}

// Resume names the exact ways a caller can recover the outcome.
type Resume struct {
	GetTool       string `json:"get_tool"`
	AwaitTool     string `json:"await_tool"`
	RetryOriginal bool   `json:"retry_original"`
}

// PendingReceipt is the successful result a caller receives when the human has
// not answered yet. It is deliberately not an error: nothing has failed, and
// nothing about the interaction has changed.
type PendingReceipt struct {
	Status string `json:"status"`
	Handle Handle `json:"handle"`
	Resume Resume `json:"resume"`
}

// Status discriminates what Run observed.
type Status string

const (
	// StatusResolved means the participant produced an immutable response.
	StatusResolved Status = "resolved"
	// StatusCancelled means an authorized actor cancelled the interaction.
	StatusCancelled Status = "cancelled"
	// StatusPendingOutcome means the human has not answered within the window.
	StatusPendingOutcome Status = "pending"
	// StatusConflict means the identity was reused with a different payload.
	StatusConflict Status = "conflict"
	// StatusFailed means the interaction reached a non-cancellation terminal
	// state (expired, failed, or superseded).
	StatusFailed Status = "failed"
)

// Request is one named workflow invocation.
type Request struct {
	RoomID string
	// Envelope is the caller's own envelope. It establishes durable identity
	// and is the immutable request snapshot a retry is compared against.
	Envelope *envelopes.Envelope
	// Presented is what the participant sees. For workflows that merge
	// persisted room state into the view, or that redact part of the request,
	// it differs from Envelope. Empty means they are the same.
	Presented *envelopes.Envelope
	Mode      string
	Caller    interaction.ActorBinding
}

// presented returns the envelope to show the participant.
func (r Request) presented() *envelopes.Envelope {
	if r.Presented != nil {
		return r.Presented
	}
	return r.Envelope
}

// Outcome is what a caller observed. Exactly one of Response, Receipt, or the
// terminal-failure fields is meaningful, selected by Status.
type Outcome struct {
	Status   Status
	Handle   Handle
	Response *envelopes.Response
	Receipt  *PendingReceipt

	// ExistingInteractionID names the interaction that already owns a
	// conflicting identity.
	ExistingInteractionID string

	// TerminalErrorCode / TerminalMessage describe a non-cancellation terminal
	// disposition.
	TerminalErrorCode string
	TerminalMessage   string
}

// Run executes one named workflow invocation end to end.
//
// The ordering matters and is the whole point of the type. Recovery is checked
// before anything is created, so a retry can never open a second interaction.
// The durable record is created before presentation, so the handle exists
// before any wait can lose it. Presentation is idempotent, so a retry
// re-attaches rather than re-asking. And the bounded wait is the last step,
// because it is the only step whose failure is not a lifecycle event.
func (s *Service) Run(ctx context.Context, request Request) (Outcome, error) {
	if request.Envelope == nil {
		return Outcome{}, fmt.Errorf("roomflow: envelope is required")
	}
	if request.RoomID == "" {
		return Outcome{}, fmt.Errorf("roomflow: room id is required")
	}
	caller := request.Caller
	if caller == (interaction.ActorBinding{}) {
		caller = DefaultCaller
	}

	record, err := s.ensureInteraction(ctx, request, caller)
	if err != nil {
		if conflict := (*ConflictError)(nil); errors.As(err, &conflict) {
			return Outcome{
				Status:                StatusConflict,
				ExistingInteractionID: conflict.ExistingInteractionID,
			}, nil
		}
		return Outcome{}, err
	}
	handle := s.handleFor(record, request.RoomID, request.Envelope.ID)

	if isTerminal(record.State) {
		// The identity already has an immutable outcome: an identical retry,
		// or a caller coming back after its transport gave up. Return exactly
		// what was recorded, never a fresh execution.
		s.retirePresentation(request.RoomID, request.Envelope.ID)
		recovered, recoverErr := s.interactions.GetInteraction(ctx, interaction.GetInteractionInput{
			InteractionID: record.ID, RequesterScope: caller.Scope,
			Capability:           surfaceCapability,
			TransportCorrelation: transportCorrelation(request.RoomID, request.Envelope.ID, "named-workflow-recovery"),
		})
		if recoverErr != nil {
			return Outcome{}, recoverErr
		}
		return s.projectTerminal(ctx, recovered, handle)
	}

	if presentErr := s.present(request.RoomID, request.presented(), record.ID, caller); presentErr != nil {
		return Outcome{}, presentErr
	}

	if request.Mode == ModeAsync {
		return Outcome{Status: StatusPendingOutcome, Handle: handle, Receipt: s.receipt(handle)}, nil
	}

	outcome, err := s.interactions.AwaitResolution(ctx, interaction.AwaitResolutionInput{
		InteractionID: record.ID, RequesterScope: caller.Scope,
		MaximumWait: s.window, Capability: surfaceCapability,
		TransportCorrelation: transportCorrelation(request.RoomID, request.Envelope.ID, "named-workflow-wait"),
	})
	switch {
	case err == nil:
		// AwaitResolution already recorded this caller's retrieval; projecting
		// from the outcome it returned keeps retrieval one fact per
		// observation instead of two.
		return s.projectTerminal(ctx, outcome, handle)
	case errors.Is(err, interaction.ErrWaitTimeout),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, context.Canceled):
		// The waiter expired. That is a fact about this transport only: the
		// interaction stays exactly as it was, still presented, still
		// answerable, and now addressable by the handle in the receipt.
		return Outcome{Status: StatusPendingOutcome, Handle: handle, Receipt: s.receipt(handle)}, nil
	default:
		return Outcome{}, err
	}
}

// Recognize reports whether Tangent already owns a durable interaction for
// this scoped workflow identity. Admission control uses it so a retry reaches
// its own outcome instead of colliding with the room's current work.
func (s *Service) Recognize(
	ctx context.Context,
	caller interaction.ActorBinding,
	env *envelopes.Envelope,
) (bool, error) {
	if env == nil {
		return false, fmt.Errorf("roomflow: envelope is required")
	}
	if caller == (interaction.ActorBinding{}) {
		caller = DefaultCaller
	}
	_, found, err := s.interactions.FindInteractionByIdempotency(
		ctx, caller, WorkflowIdempotencyKey(env.Type, env.ID), surfaceCapability)
	return found, err
}

// ConflictError reports a reused identity carrying a different payload.
type ConflictError struct {
	IdempotencyKey        string
	ExistingInteractionID string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf(
		"%v: %q already names interaction %q with a different request",
		interaction.ErrIdempotencyConflict, e.IdempotencyKey, e.ExistingInteractionID,
	)
}

func (e *ConflictError) Unwrap() error { return interaction.ErrIdempotencyConflict }

// ensureInteraction returns the durable interaction that owns this invocation,
// creating it only when the scoped identity has never been seen.
func (s *Service) ensureInteraction(
	ctx context.Context,
	request Request,
	caller interaction.ActorBinding,
) (interaction.InteractionRecord, error) {
	key := WorkflowIdempotencyKey(request.Envelope.Type, request.Envelope.ID)
	existing, found, err := s.interactions.FindInteractionByIdempotency(ctx, caller, key, surfaceCapability)
	if err != nil {
		return interaction.InteractionRecord{}, err
	}
	if found {
		snapshot, snapshotErr := canonicalRequest(request.Envelope)
		if snapshotErr != nil {
			return interaction.InteractionRecord{}, snapshotErr
		}
		if !json.Valid(existing.RequestSnapshot) || string(existing.RequestSnapshot) != string(snapshot) {
			return interaction.InteractionRecord{}, &ConflictError{
				IdempotencyKey: key, ExistingInteractionID: existing.ID,
			}
		}
		return existing, nil
	}

	if _, _, surfaceErr := s.interactions.EnsureLegacyRoomSurface(ctx, interaction.EnsureLegacyRoomSurfaceInput{
		RoomID: request.RoomID, Caller: caller, OwnerScope: caller.Scope,
		Metadata:   roomSurfaceMetadata(request.RoomID),
		Capability: surfaceCapability,
	}); surfaceErr != nil {
		return interaction.InteractionRecord{}, surfaceErr
	}

	snapshot, err := canonicalRequest(request.Envelope)
	if err != nil {
		return interaction.InteractionRecord{}, err
	}
	presented, err := json.Marshal(request.presented())
	if err != nil {
		return interaction.InteractionRecord{}, fmt.Errorf(
			"roomflow: marshal presented envelope %q: %w", request.Envelope.ID, err)
	}
	handle, err := s.interactions.SubmitInteraction(ctx, interaction.SubmitInteractionInput{
		SurfaceID: request.RoomID, Caller: caller, IdempotencyKey: key,
		Definition: interaction.DefinitionRef{Kind: request.Envelope.Type},
		Request:    snapshot,
		// The presented envelope is retained as an external correlation, not as
		// the request: the canonical request is the schema-validated payload,
		// while the envelope is the v0.12 presentation artifact restart
		// reconstruction rebuilds the room from.
		ExternalRefs: json.RawMessage(mustJSON(map[string]any{
			"legacy_room_id":     request.RoomID,
			"legacy_envelope_id": request.Envelope.ID,
			"legacy_envelope":    json.RawMessage(presented),
		})),
		Policy: json.RawMessage(mustJSON(map[string]any{
			"completion":           completionMode(request.Mode),
			"compatibility_window": s.window.String(),
		})),
		LegacyRoomID: request.RoomID, LegacyEnvelopeID: request.Envelope.ID,
		Capability: surfaceCapability,
	})
	if err != nil {
		if errors.Is(err, interaction.ErrIdempotencyConflict) {
			conflicting, found, lookupErr := s.interactions.FindInteractionByIdempotency(
				ctx, caller, key, surfaceCapability)
			existingID := ""
			if lookupErr == nil && found {
				existingID = conflicting.ID
			}
			return interaction.InteractionRecord{}, &ConflictError{
				IdempotencyKey: key, ExistingInteractionID: existingID,
			}
		}
		return interaction.InteractionRecord{}, err
	}
	outcome, err := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID: handle.InteractionID, RequesterScope: caller.Scope,
		Capability: surfaceCapability,
	})
	if err != nil {
		return interaction.InteractionRecord{}, err
	}
	return outcome.Interaction, nil
}

// present attaches the envelope to its room. Presentation is a delivery
// optimization: it makes the request visible, and nothing more. The durable
// record already exists by the time this runs, so a room that is missing or
// closed cannot lose the request.
func (s *Service) present(
	roomID string,
	env *envelopes.Envelope,
	interactionID string,
	caller interaction.ActorBinding,
) error {
	rm, ok := s.rooms.Get(roomID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	disposition := &roomDisposition{service: s, interactionID: interactionID, caller: caller}
	transform := func(resp *envelopes.Response) (*envelopes.Response, error) {
		return s.normalizer.NormalizeResponse(roomID, env, resp)
	}
	return rm.Present(env, transform, disposition)
}

// retirePresentation drops a stale room presentation for an interaction that
// is already terminal. It writes nothing: the outcome is immutable and the
// room is only a view of it.
func (s *Service) retirePresentation(roomID, envelopeID string) {
	if rm, ok := s.rooms.Get(roomID); ok {
		rm.Release(envelopeID)
	}
}

func (s *Service) handleFor(
	record interaction.InteractionRecord,
	roomID string,
	envelopeID string,
) Handle {
	return Handle{
		SurfaceID: record.SurfaceID, InteractionID: record.ID,
		RoomID: roomID, EnvelopeID: envelopeID, URL: s.RoomURL(roomID),
	}
}

// RoomURL renders the browser link for a room.
func (s *Service) RoomURL(roomID string) string {
	if s.roomURLBase == "" {
		return "/r/" + roomID
	}
	return s.roomURLBase + "/r/" + roomID
}

func (s *Service) receipt(handle Handle) *PendingReceipt {
	return &PendingReceipt{
		Status: StatusPending,
		Handle: handle,
		Resume: Resume{
			GetTool:       "tangent.interaction_get",
			AwaitTool:     "tangent.interaction_await",
			RetryOriginal: true,
		},
	}
}

// projectTerminal turns an already-observed immutable terminal outcome into
// the exact public response the workflow contract promises. A resolution
// replays the stored response verbatim, so an identical retry is
// byte-identical to the first answer; a cancellation is synthesized from the
// record's own terminal instant rather than from wall-clock time, for the same
// reason.
//
// The caller's retrieval is recorded by whoever read the outcome, not here, so
// one observation is one retrieval fact.
func (s *Service) projectTerminal(
	ctx context.Context,
	outcome interaction.TerminalOutcome,
	handle Handle,
) (Outcome, error) {
	record := outcome.Interaction
	result := Outcome{Handle: handle}
	switch outcome.Interaction.State {
	case interaction.InteractionStateResolved:
		if outcome.Resolution == nil {
			return Outcome{}, fmt.Errorf("roomflow: resolved interaction %q has no resolution", record.ID)
		}
		var response envelopes.Response
		if err := json.Unmarshal(outcome.Resolution.ResponsePayload, &response); err != nil {
			return Outcome{}, fmt.Errorf("roomflow: decode stored response for %q: %w", record.ID, err)
		}
		result.Status = StatusResolved
		result.Response = &response
	case interaction.InteractionStateCanceled:
		result.Status = StatusCancelled
		result.Response = cancelledResponse(handle.EnvelopeID, outcome.Interaction.TerminalAt)
	default:
		result.Status = StatusFailed
		result.TerminalErrorCode = outcome.Interaction.TerminalErrorCode
		result.TerminalMessage = terminalMessage(outcome.Interaction)
	}
	s.recordDelivery(ctx, record.ID, handle)
	return result, nil
}

// recordDelivery durably records that Tangent handed this immutable outcome
// back over the caller's own request. It is intentionally best-effort and
// never changes what the caller receives: a delivery journal that could fail
// the delivery it is journaling would be worse than one that logs.
func (s *Service) recordDelivery(ctx context.Context, interactionID string, handle Handle) {
	if _, _, err := s.interactions.RecordTerminalOutcomeDelivery(ctx, interaction.RecordTerminalOutcomeDeliveryInput{
		Worker: DeliveryWorker, InteractionID: interactionID,
		Receipt: json.RawMessage(mustJSON(map[string]any{
			"transport":   "mcp-tool-result",
			"room_id":     handle.RoomID,
			"envelope_id": handle.EnvelopeID,
		})),
	}); err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Warn("roomflow: record terminal outcome delivery",
			"interaction", interactionID, "room", handle.RoomID, "err", err)
	}
}

func cancelledResponse(envelopeID string, terminalAt *time.Time) *envelopes.Response {
	completedAt := ""
	if terminalAt != nil {
		completedAt = terminalAt.UTC().Format(time.RFC3339)
	}
	return &envelopes.Response{
		V:           envelopes.ProtocolVersion,
		EnvelopeID:  envelopeID,
		Kind:        envelopes.ResponseKindAck,
		Status:      envelopes.ResponseStatusCancelled,
		CompletedAt: completedAt,
	}
}

func terminalMessage(record interaction.InteractionRecord) string {
	if record.TerminalReason != "" {
		return record.TerminalReason
	}
	return fmt.Sprintf("interaction %s reached terminal state %s", record.ID, record.State)
}

func completionMode(mode string) string {
	if mode == ModeAsync {
		return ModeAsync
	}
	return ModeWait
}

func isTerminal(state interaction.InteractionState) bool {
	switch state {
	case interaction.InteractionStateResolved, interaction.InteractionStateCanceled,
		interaction.InteractionStateExpired, interaction.InteractionStateFailed,
		interaction.InteractionStateSuperseded:
		return true
	default:
		return false
	}
}

// canonicalRequest is the exact request snapshot an interaction is keyed and
// compared against: the envelope's payload, which is what the pinned
// definition schema describes and what a caller means by "the same request".
// A retry carrying a different payload is therefore a conflict rather than a
// silent substitution.
func canonicalRequest(env *envelopes.Envelope) (json.RawMessage, error) {
	payload := env.Data
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("roomflow: marshal envelope %q request: %w", env.ID, err)
	}
	// Numbers are decoded as json.Number rather than float64 so this produces
	// byte-for-byte what the store's own canonicalization produces. Comparing
	// a retry against the stored snapshot is only meaningful if both sides
	// normalize identically; a numeric round trip that differed by one
	// rendering would turn a legitimate retry into a conflict.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var normalized any
	if decodeErr := decoder.Decode(&normalized); decodeErr != nil {
		return nil, fmt.Errorf("roomflow: normalize envelope %q: %w", env.ID, decodeErr)
	}
	canonical, canonicalErr := json.Marshal(normalized)
	if canonicalErr != nil {
		return nil, fmt.Errorf("roomflow: canonicalize envelope %q: %w", env.ID, canonicalErr)
	}
	return canonical, nil
}

func roomSurfaceMetadata(roomID string) json.RawMessage {
	return json.RawMessage(mustJSON(map[string]any{
		"presentation": "legacy-room", "legacy_room_id": roomID,
	}))
}

func transportCorrelation(roomID, envelopeID, phase string) json.RawMessage {
	return json.RawMessage(mustJSON(map[string]any{
		"room_id": roomID, "envelope_id": envelopeID, "phase": phase,
	}))
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
