package roomflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/room"
)

// RestoreReport summarizes what restart reconstruction rebuilt.
type RestoreReport struct {
	// Restored counts interactions re-presented from canonical records.
	Restored int `json:"restored"`
	// MissingRooms counts open interactions whose room is no longer live.
	// Their durable records are untouched and still retrievable by handle;
	// only the browser view is gone.
	MissingRooms int `json:"missing_rooms"`
	// Undefinable counts interactions whose pinned definition can no longer be
	// resolved, so their request cannot be safely re-rendered.
	Undefinable int `json:"undefinable"`
}

// RestorePresentations rebuilds every live room's UI from canonical records.
//
// This is what makes Room.Push's in-memory map demonstrably non-authoritative:
// after a restart the map is empty, and the rooms a browser reconnects to are
// repopulated purely from interactions and their immutable request snapshots.
// Nothing about an interaction's lifecycle changes here.
func (s *Service) RestorePresentations(ctx context.Context) (RestoreReport, error) {
	records, err := s.interactions.ListOpenLegacyRoomInteractions(ctx)
	if err != nil {
		return RestoreReport{}, err
	}
	var report RestoreReport
	for index := range records {
		record := records[index]
		env, decodeErr := envelopeFromSnapshot(record)
		if decodeErr != nil {
			report.Undefinable++
			s.logger.Warn("roomflow: cannot rebuild room presentation",
				"interaction", record.ID, "room", record.LegacyRoomID, "err", decodeErr)
			continue
		}
		if _, defErr := s.normalizer.PinnedResponseKind(env.Type); defErr != nil {
			report.Undefinable++
			s.logger.Warn("roomflow: pinned definition unavailable for rebuild",
				"interaction", record.ID, "room", record.LegacyRoomID, "type", env.Type, "err", defErr)
			continue
		}
		caller := interaction.ActorBinding{
			Scope: record.CallerScope, PrincipalRef: record.CallerPrincipalRef,
			Authority: record.CallerAuthority, Assurance: record.CallerAssurance,
		}
		if caller.PrincipalRef == "" {
			caller.PrincipalRef = DefaultCaller.PrincipalRef
		}
		if err := s.present(record.LegacyRoomID, env, record.ID, caller); err != nil {
			if errors.Is(err, ErrRoomNotFound) {
				report.MissingRooms++
				continue
			}
			return report, err
		}
		report.Restored++
	}
	return report, nil
}

// CloseRoom terminalizes a room's outstanding work canonically and only then
// tears down its presentation.
//
// Closing a room is an explicit, authorized caller action, which is exactly
// what the contract permits to terminalize an interaction. The ordering is the
// contract: the durable surface disposition is recorded first, so the legacy
// room rows that Manager.Close writes are a projection of a decision already
// made rather than an independent claim about it.
func (s *Service) CloseRoom(ctx context.Context, roomID, status string, caller interaction.ActorBinding) error {
	if caller == (interaction.ActorBinding{}) {
		caller = DefaultCaller
	}
	surface, _, err := s.interactions.EnsureLegacyRoomSurface(ctx, interaction.EnsureLegacyRoomSurfaceInput{
		RoomID: roomID, Caller: caller, OwnerScope: caller.Scope,
		Metadata: roomSurfaceMetadata(roomID), Capability: surfaceCapability,
	})
	if err != nil {
		if errors.Is(err, interaction.ErrNotFound) {
			return nil
		}
		return err
	}
	if surface.State == interaction.SurfaceStateClosed || surface.State == interaction.SurfaceStateExpired {
		return nil
	}
	if _, err := s.interactions.CloseSurface(ctx, interaction.CloseSurfaceInput{
		SurfaceID: roomID, ExpectedRevision: surface.Revision, Requester: caller,
		Reason: status, PolicyRef: "tangent:legacy-room-close:v1",
		Metadata:   roomSurfaceMetadata(roomID),
		Capability: surfaceCapability,
	}); err != nil {
		return err
	}
	return nil
}

// envelopeFromSnapshot rebuilds the exact envelope a participant was shown.
//
// The presentation artifact is retained beside the interaction as an external
// correlation rather than as its request, because several workflows present
// the caller's payload merged with persisted room state (and synthesis notes
// presents a redacted view of it). The immutable request snapshot remains the
// identity; this is the view.
func envelopeFromSnapshot(record interaction.InteractionRecord) (*envelopes.Envelope, error) {
	var refs struct {
		Envelope json.RawMessage `json:"legacy_envelope"`
	}
	if err := json.Unmarshal(record.ExternalRefs, &refs); err != nil {
		return nil, fmt.Errorf("roomflow: decode external refs for %q: %w", record.ID, err)
	}
	if len(refs.Envelope) == 0 {
		return nil, fmt.Errorf(
			"roomflow: interaction %q retains no presented envelope to rebuild", record.ID)
	}
	var env envelopes.Envelope
	if err := json.Unmarshal(refs.Envelope, &env); err != nil {
		return nil, fmt.Errorf("roomflow: decode presented envelope for %q: %w", record.ID, err)
	}
	if env.ID == "" {
		env.ID = record.LegacyEnvelopeID
	}
	if env.ID != record.LegacyEnvelopeID {
		return nil, fmt.Errorf(
			"roomflow: interaction %q snapshot names envelope %q but is bound to %q",
			record.ID, env.ID, record.LegacyEnvelopeID)
	}
	if env.Type == "" {
		env.Type = record.Definition.Kind
	}
	if env.Type != record.Definition.Kind {
		return nil, fmt.Errorf(
			"roomflow: interaction %q retained a %q envelope but is bound to definition %q",
			record.ID, env.Type, record.Definition.Kind)
	}
	return &env, nil
}

var _ room.Disposition = (*roomDisposition)(nil)
