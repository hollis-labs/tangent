package interaction

import (
	"context"
	"encoding/json"
	"fmt"
)

// EnsureLegacyRoomSurfaceInput adopts a v0.12 room as a durable surface.
type EnsureLegacyRoomSurfaceInput struct {
	RoomID     string          `json:"room_id"`
	Caller     ActorBinding    `json:"caller"`
	OwnerScope string          `json:"owner_scope,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	Capability string          `json:"-"`
}

// EnsureLegacyRoomSurface returns the durable surface that owns a legacy room.
// It is an in-process compatibility operation and is deliberately not exposed
// on the direct caller MCP surface: rooms are server-issued, so a wire caller
// can never nominate the surface identity this establishes.
func (s *Service) EnsureLegacyRoomSurface(
	ctx context.Context,
	input EnsureLegacyRoomSurfaceInput,
) (SurfaceRecord, bool, error) {
	if err := validateActor(input.Caller); err != nil {
		return SurfaceRecord{}, false, err
	}
	if !s.surfaces.Authorize(input.RoomID, input.Capability) {
		return SurfaceRecord{}, false, ErrUnauthorized
	}
	return s.store.EnsureLegacyRoomSurface(ctx, EnsureLegacyRoomSurfaceParams{
		RoomID: input.RoomID, CallerScope: input.Caller.Scope, OwnerScope: input.OwnerScope,
		Metadata: input.Metadata, ActorRef: input.Caller.PrincipalRef,
		Authority: input.Caller.Authority,
	})
}

// ListOpenLegacyRoomInteractions returns every nonterminal interaction bound to
// a legacy room so restart reconstruction can rebuild room presentation from
// canonical records rather than from process-local channels.
func (s *Service) ListOpenLegacyRoomInteractions(ctx context.Context) ([]InteractionRecord, error) {
	return s.store.ListOpenLegacyRoomInteractions(ctx)
}

// FindInteractionByLegacyEnvelope resolves the canonical interaction that owns
// one legacy room/envelope pair. It records no retrieval: this is a routing
// lookup, not a caller observing a terminal outcome.
func (s *Service) FindInteractionByLegacyEnvelope(
	ctx context.Context,
	roomID string,
	envelopeID string,
	capability string,
) (InteractionRecord, bool, error) {
	record, found, err := s.store.FindInteractionByLegacyEnvelope(ctx, roomID, envelopeID)
	if err != nil || !found {
		return record, found, err
	}
	if !s.surfaces.Authorize(record.SurfaceID, capability) {
		return InteractionRecord{}, false, ErrUnauthorized
	}
	return record, true, nil
}

// AcknowledgeTerminalOutcomeInput names one caller acknowledgement.
type AcknowledgeTerminalOutcomeInput struct {
	InteractionID        string          `json:"interaction_id"`
	RequesterScope       string          `json:"requester_scope"`
	TransportCorrelation json.RawMessage `json:"transport_correlation,omitempty"`
	Capability           string          `json:"-"`
}

// AcknowledgeTerminalOutcome records the caller's explicit acknowledgement of
// an immutable terminal outcome. It is idempotent, and it is separate from
// both retrieval and delivery: neither reading an outcome nor Tangent
// successfully writing it to a socket makes this statement on the caller's
// behalf.
func (s *Service) AcknowledgeTerminalOutcome(
	ctx context.Context,
	input AcknowledgeTerminalOutcomeInput,
) (TerminalOutcomeAcknowledgement, error) {
	if input.InteractionID == "" || input.RequesterScope == "" {
		return TerminalOutcomeAcknowledgement{}, fmt.Errorf(
			"%w: interaction and requester scope are required", ErrInvalidRecord)
	}
	record, err := s.store.GetInteraction(ctx, input.InteractionID)
	if err != nil {
		return TerminalOutcomeAcknowledgement{}, err
	}
	if !s.surfaces.Authorize(record.SurfaceID, input.Capability) {
		return TerminalOutcomeAcknowledgement{}, ErrUnauthorized
	}
	if err := s.authorizeInteraction(ctx, record, input.RequesterScope); err != nil {
		return TerminalOutcomeAcknowledgement{}, err
	}
	return s.store.AcknowledgeTerminalOutcome(ctx, AcknowledgeTerminalOutcomeParams{
		InteractionID:  input.InteractionID,
		RequesterScope: input.RequesterScope,
		// The correlation is transport evidence about this acknowledgement, not
		// an authorization input; it is stored verbatim and never interpreted.
		TransportCorrelation: input.TransportCorrelation,
	})
}

// RecordTerminalOutcomeDeliveryInput names one caller-pull hand-off performed
// by a trusted in-process destination adapter.
type RecordTerminalOutcomeDeliveryInput struct {
	Worker        ActorBinding    `json:"worker"`
	InteractionID string          `json:"interaction_id"`
	Receipt       json.RawMessage `json:"receipt,omitempty"`
}

// RecordTerminalOutcomeDelivery durably records that Tangent handed an
// immutable terminal outcome back to its caller. Only an authorized delivery
// worker may assert a delivery happened; direct MCP callers never hold that
// authority.
func (s *Service) RecordTerminalOutcomeDelivery(
	ctx context.Context,
	input RecordTerminalOutcomeDeliveryInput,
) (DeliveryClaim, bool, error) {
	if err := validateActor(input.Worker); err != nil {
		return DeliveryClaim{}, false, err
	}
	if !s.delivery.AuthorizeDeliveryWorker(input.Worker) {
		return DeliveryClaim{}, false, ErrUnauthorized
	}
	return s.store.RecordTerminalOutcomeDelivery(ctx, RecordTerminalOutcomeDeliveryParams{
		InteractionID: input.InteractionID,
		LeaseOwner:    deliveryWorkerLeaseOwner(input.Worker),
		Receipt:       input.Receipt,
	})
}

// SurfaceOwnerScope returns just the owner scope of a surface, with no
// authorization check and no payload.
//
// Reading the owner is how an authorization decision is made, so it cannot
// itself require authorization without circularity. It deliberately returns
// one string and nothing else: no metadata, no policy, no interactions, and no
// evidence that the caller has not been cleared to see.
func (s *Service) SurfaceOwnerScope(ctx context.Context, surfaceID string) (string, error) {
	surface, err := s.store.GetSurface(ctx, surfaceID)
	if err != nil {
		return "", err
	}
	return surface.OwnerScope, nil
}
