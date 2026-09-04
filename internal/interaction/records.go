// Package interaction owns Tangent's canonical durable interaction records.
// Transport adapters and workflow packages consume these records through an
// application service; they do not own their lifecycle or persistence.
package interaction

import (
	"encoding/json"
	"time"
)

// SurfaceState is independent from browser connection and interaction state.
type SurfaceState string

const (
	SurfaceStateCreated   SurfaceState = "created"
	SurfaceStateActive    SurfaceState = "active"
	SurfaceStateSuspended SurfaceState = "suspended"
	SurfaceStateClosed    SurfaceState = "closed"
	SurfaceStateExpired   SurfaceState = "expired"
)

// InteractionState is the canonical lifecycle of one immutable caller request.
type InteractionState string

const (
	InteractionStateSubmitted  InteractionState = "submitted"
	InteractionStateValidated  InteractionState = "validated"
	InteractionStateStaged     InteractionState = "staged"
	InteractionStatePresented  InteractionState = "presented"
	InteractionStateInProgress InteractionState = "in_progress"
	InteractionStateResolved   InteractionState = "resolved"
	InteractionStateCanceled   InteractionState = "canceled"
	InteractionStateExpired    InteractionState = "expired"
	InteractionStateFailed     InteractionState = "failed"
	InteractionStateSuperseded InteractionState = "superseded"
)

// TerminalCause preserves who or what authorized a terminal disposition.
type TerminalCause string

const (
	TerminalCauseCallerWithdrawn       TerminalCause = "caller_withdrawn"
	TerminalCauseCallerCanceled        TerminalCause = "caller_canceled"
	TerminalCauseParticipantCanceled   TerminalCause = "participant_canceled"
	TerminalCauseAdministratorCanceled TerminalCause = "administrator_canceled"
	TerminalCauseSurfacePolicy         TerminalCause = "surface_policy"
)

// DeliveryState is independent from its interaction's terminal state.
type DeliveryState string

const (
	DeliveryStateQueued           DeliveryState = "queued"
	DeliveryStateDelivering       DeliveryState = "delivering"
	DeliveryStateDelivered        DeliveryState = "delivered"
	DeliveryStateAcknowledged     DeliveryState = "acknowledged"
	DeliveryStateRetryableFailure DeliveryState = "retryable_failure"
	DeliveryStateTerminalFailure  DeliveryState = "terminal_failure"
)

// SurfaceRecord is Tangent's durable presentation container.
type SurfaceRecord struct {
	ID                      string          `json:"surface_id"`
	OwnerScope              string          `json:"owner_scope"`
	State                   SurfaceState    `json:"state"`
	Metadata                json.RawMessage `json:"metadata"`
	Policy                  json.RawMessage `json:"policy"`
	NextInteractionSequence int64           `json:"next_interaction_sequence"`
	Revision                int64           `json:"revision"`
	CreatedAt               time.Time       `json:"created_at"`
	UpdatedAt               time.Time       `json:"updated_at"`
	ClosedAt                *time.Time      `json:"closed_at,omitempty"`
	CloseReason             string          `json:"close_reason,omitempty"`
	LegacyRoomID            string          `json:"legacy_room_id,omitempty"`
}

// DefinitionBinding pins an interaction to the exact definition Tangent used.
type DefinitionBinding struct {
	InteractionID  string    `json:"interaction_id"`
	Publisher      string    `json:"publisher"`
	Kind           string    `json:"kind"`
	Version        string    `json:"version"`
	Revision       string    `json:"revision"`
	Digest         string    `json:"digest,omitempty"`
	Source         string    `json:"source"`
	SchemaIdentity string    `json:"schema_identity,omitempty"`
	SchemaDigest   string    `json:"schema_digest,omitempty"`
	HostVersion    string    `json:"host_version,omitempty"`
	Assurance      string    `json:"assurance"`
	BoundAt        time.Time `json:"bound_at"`
}

// InteractionRecord is one immutable caller request and its operational state.
type InteractionRecord struct {
	ID                          string            `json:"interaction_id"`
	SurfaceID                   string            `json:"surface_id"`
	CallerScope                 string            `json:"caller_scope"`
	CallerPrincipalRef          string            `json:"caller_principal_ref,omitempty"`
	CallerAuthority             string            `json:"caller_authority"`
	CallerAssurance             string            `json:"caller_assurance"`
	IdempotencyKey              string            `json:"idempotency_key"`
	SurfaceSequence             int64             `json:"surface_sequence"`
	Definition                  DefinitionBinding `json:"definition_binding"`
	RequestSnapshot             json.RawMessage   `json:"request_snapshot"`
	ExternalRefs                json.RawMessage   `json:"external_refs"`
	Policy                      json.RawMessage   `json:"policy"`
	State                       InteractionState  `json:"state"`
	PresentedProjectionRevision *int64            `json:"presented_projection_revision,omitempty"`
	ParticipantScope            string            `json:"participant_scope,omitempty"`
	ParticipantRef              string            `json:"participant_ref,omitempty"`
	ParticipantAuthority        string            `json:"participant_authority,omitempty"`
	ParticipantAssurance        string            `json:"participant_assurance,omitempty"`
	ConnectionID                string            `json:"connection_id,omitempty"`
	PresentedAt                 *time.Time        `json:"presented_at,omitempty"`
	TerminalCause               TerminalCause     `json:"terminal_cause,omitempty"`
	TerminalReason              string            `json:"terminal_reason,omitempty"`
	TerminalPolicyRef           string            `json:"terminal_policy_ref,omitempty"`
	TerminalErrorCode           string            `json:"terminal_error_code,omitempty"`
	ReplacementInteractionID    string            `json:"replacement_interaction_id,omitempty"`
	Revision                    int64             `json:"revision"`
	CreatedAt                   time.Time         `json:"created_at"`
	UpdatedAt                   time.Time         `json:"updated_at"`
	TerminalAt                  *time.Time        `json:"terminal_at,omitempty"`
	LegacyRoomID                string            `json:"legacy_room_id,omitempty"`
	LegacyEnvelopeID            string            `json:"legacy_envelope_id,omitempty"`
}

// DraftRevision is an immutable Tangent-custodied draft snapshot.
type DraftRevision struct {
	InteractionID        string          `json:"interaction_id"`
	Revision             int64           `json:"revision"`
	InteractionRevision  int64           `json:"interaction_revision"`
	ParticipantScope     string          `json:"participant_scope"`
	ParticipantRef       string          `json:"participant_ref"`
	ParticipantAuthority string          `json:"participant_authority"`
	ParticipantAssurance string          `json:"participant_assurance"`
	DefinitionVersion    string          `json:"definition_version"`
	Payload              json.RawMessage `json:"payload"`
	Sensitivity          string          `json:"sensitivity,omitempty"`
	ExpiresAt            *time.Time      `json:"expires_at,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
}

// ResolutionRecord is an immutable participant terminal response.
type ResolutionRecord struct {
	ID                          string          `json:"resolution_id"`
	InteractionID               string          `json:"interaction_id"`
	ExpectedInteractionRevision int64           `json:"expected_interaction_revision"`
	PresentedProjectionRevision int64           `json:"presented_projection_revision"`
	ParticipantScope            string          `json:"participant_scope"`
	ParticipantRef              string          `json:"participant_ref"`
	ParticipantAuthority        string          `json:"participant_authority"`
	ParticipantAssurance        string          `json:"participant_assurance"`
	ResponseKind                string          `json:"response_kind"`
	ResponsePayload             json.RawMessage `json:"response_payload"`
	SourceDraftRevision         *int64          `json:"source_draft_revision,omitempty"`
	IntegrityDigest             string          `json:"integrity_digest"`
	SubmittedAt                 time.Time       `json:"submitted_at"`
	ValidatedAt                 time.Time       `json:"validated_at"`
	RecordedAt                  time.Time       `json:"recorded_at"`
}

// ResolutionDeliveryRecord tracks delivery of an immutable resolution.
type ResolutionDeliveryRecord struct {
	ID                 string          `json:"delivery_id"`
	ResolutionID       string          `json:"resolution_id"`
	DestinationBinding json.RawMessage `json:"destination_binding"`
	IdempotencyKey     string          `json:"idempotency_key"`
	Policy             json.RawMessage `json:"policy"`
	State              DeliveryState   `json:"state"`
	Revision           int64           `json:"revision"`
	LeaseOwner         string          `json:"lease_owner,omitempty"`
	LeaseExpiresAt     *time.Time      `json:"lease_expires_at,omitempty"`
	Receipt            json.RawMessage `json:"receipt,omitempty"`
	TerminalReason     string          `json:"terminal_reason,omitempty"`
	NextEligibleAt     *time.Time      `json:"next_eligible_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	DeliveredAt        *time.Time      `json:"delivered_at,omitempty"`
	AcknowledgedAt     *time.Time      `json:"acknowledged_at,omitempty"`
}

// TerminalNotificationRecord delivers a non-resolution terminal disposition.
type TerminalNotificationRecord struct {
	ID                 string           `json:"notification_id"`
	InteractionID      string           `json:"interaction_id"`
	TerminalState      InteractionState `json:"terminal_state"`
	TerminalCause      TerminalCause    `json:"terminal_cause,omitempty"`
	DestinationBinding json.RawMessage  `json:"destination_binding"`
	IdempotencyKey     string           `json:"idempotency_key"`
	Policy             json.RawMessage  `json:"policy"`
	State              DeliveryState    `json:"state"`
	Revision           int64            `json:"revision"`
	LeaseOwner         string           `json:"lease_owner,omitempty"`
	LeaseExpiresAt     *time.Time       `json:"lease_expires_at,omitempty"`
	Receipt            json.RawMessage  `json:"receipt,omitempty"`
	TerminalReason     string           `json:"terminal_reason,omitempty"`
	NextEligibleAt     *time.Time       `json:"next_eligible_at,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	DeliveredAt        *time.Time       `json:"delivered_at,omitempty"`
	AcknowledgedAt     *time.Time       `json:"acknowledged_at,omitempty"`
}

// TerminalOutcomeRetrievalRecord records handle-based terminal retrieval.
type TerminalOutcomeRetrievalRecord struct {
	ID                   string          `json:"retrieval_id"`
	InteractionID        string          `json:"interaction_id"`
	ResolutionID         string          `json:"resolution_id,omitempty"`
	RequesterScope       string          `json:"requester_scope"`
	TransportCorrelation json.RawMessage `json:"transport_correlation"`
	RetrievedAt          time.Time       `json:"retrieved_at"`
}

// DeliveryAttempt is opened durably with a delivery lease and then sealed once
// with the strongest outcome the adapter observed. A sealed attempt is
// immutable.
type DeliveryAttempt struct {
	ID                     string          `json:"attempt_id"`
	ResolutionDeliveryID   string          `json:"resolution_delivery_id,omitempty"`
	TerminalNotificationID string          `json:"terminal_notification_id,omitempty"`
	AttemptNumber          int64           `json:"attempt_number"`
	Status                 DeliveryState   `json:"status"`
	Receipt                json.RawMessage `json:"receipt,omitempty"`
	ErrorCode              string          `json:"error_code,omitempty"`
	ErrorMessage           string          `json:"error_message,omitempty"`
	StartedAt              time.Time       `json:"started_at"`
	CompletedAt            *time.Time      `json:"completed_at,omitempty"`
}

// DeliveryEvent is an append-only audit fact for one delivery revision.
type DeliveryEvent struct {
	ID                     string          `json:"event_id"`
	ResolutionDeliveryID   string          `json:"resolution_delivery_id,omitempty"`
	TerminalNotificationID string          `json:"terminal_notification_id,omitempty"`
	Type                   string          `json:"event_type"`
	FromRevision           int64           `json:"from_revision"`
	ToRevision             int64           `json:"to_revision"`
	AttemptNumber          *int64          `json:"attempt_number,omitempty"`
	Metadata               json.RawMessage `json:"metadata"`
	RecordedAt             time.Time       `json:"recorded_at"`
}

// LegacyRoomHistoryEntry is the explicit read-only v0.12 compatibility view.
type LegacyRoomHistoryEntry struct {
	RoomID          string          `json:"room_id"`
	EnvelopeID      string          `json:"envelope_id"`
	Type            string          `json:"type"`
	RequestPayload  json.RawMessage `json:"request_payload"`
	ResponseKind    string          `json:"response_kind,omitempty"`
	ResponsePayload json.RawMessage `json:"response_payload,omitempty"`
	Status          string          `json:"status"`
	ErrorCode       string          `json:"error_code,omitempty"`
	ErrorMessage    string          `json:"error_message,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	ResolvedAt      *time.Time      `json:"resolved_at,omitempty"`
	SurfaceID       string          `json:"surface_id,omitempty"`
	InteractionID   string          `json:"interaction_id,omitempty"`

	// InteractionState and CallerAcknowledgedAt make the projection honest
	// about its own status. A legacy row can read "pending" long after the
	// canonical interaction resolved — the row is a projection, and these
	// fields say which record actually holds the outcome.
	InteractionState     InteractionState `json:"interaction_state,omitempty"`
	CallerAcknowledgedAt *time.Time       `json:"caller_acknowledged_at,omitempty"`
}

// SurfaceSnapshot is a restart-safe hydration of canonical state for a surface.
type SurfaceSnapshot struct {
	Surface               SurfaceRecord                    `json:"surface"`
	Interactions          []InteractionRecord              `json:"interactions"`
	Drafts                []DraftRevision                  `json:"drafts"`
	Resolutions           []ResolutionRecord               `json:"resolutions"`
	ResolutionDeliveries  []ResolutionDeliveryRecord       `json:"resolution_deliveries"`
	TerminalNotifications []TerminalNotificationRecord     `json:"terminal_notifications"`
	OutcomeRetrievals     []TerminalOutcomeRetrievalRecord `json:"outcome_retrievals"`
	DeliveryAttempts      []DeliveryAttempt                `json:"delivery_attempts"`
	DeliveryEvents        []DeliveryEvent                  `json:"delivery_events"`
}

// SurfaceInteractionSnapshot is the focused projection needed by operator
// queues. It deliberately excludes drafts, delivery attempts, notifications,
// retrievals, and event journals so live inbox refreshes do not hydrate audit
// history that they never render.
type SurfaceInteractionSnapshot struct {
	Surface      SurfaceRecord       `json:"surface"`
	Interactions []InteractionRecord `json:"interactions"`
	Resolutions  []ResolutionRecord  `json:"resolutions"`
}
