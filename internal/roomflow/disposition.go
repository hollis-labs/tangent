package roomflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/room"
)

// roomDisposition is the canonical lifecycle authority behind one room
// presentation. It is what makes Room.Push's channels a delivery optimization
// rather than the source of truth: every terminal fact the participant
// produces lands in the durable substrate here first, and only then becomes
// visible to the legacy projection and to whoever happens to be waiting.
type roomDisposition struct {
	service       *Service
	interactionID string
	caller        interaction.ActorBinding
}

// Presented records which projection revision the participant is currently
// looking at, and on which connection. It is idempotent across reconnects,
// refreshes, and additional observers: the canonical presentation revision is
// established once, and later attachments re-render the same interaction
// rather than advancing its lifecycle again.
//
// The connection id is recorded as the operational fact ADR 0001 names — which
// attachment the projection reached — and never as an authorization input. It
// is deliberately last-writer-wins: with several tabs observing one surface,
// the record simply names the most recent delivery.
func (d *roomDisposition) Presented(
	ctx context.Context,
	_ string,
	_ *envelopes.Envelope,
	revision int64,
	connectionID string,
) error {
	_, err := d.ensurePresented(ctx, revision, connectionID)
	return err
}

// DurableRevision reads the canonical revisions behind this presentation so an
// attaching connection can be synchronized from durable state.
//
// The surface revision is read alongside the interaction because a client
// synchronizes to a surface, not to one envelope: it is the number a client
// compares against to decide it has fallen behind and should ask to resync.
func (d *roomDisposition) DurableRevision(ctx context.Context) (room.DurableRevision, error) {
	outcome, err := d.service.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
		InteractionID: d.interactionID, RequesterScope: d.caller.Scope,
		Capability: surfaceCapability,
	})
	if err != nil {
		return room.DurableRevision{}, err
	}
	record := outcome.Interaction
	revision := room.DurableRevision{
		SurfaceID:           record.SurfaceID,
		InteractionID:       record.ID,
		InteractionRevision: record.Revision,
		State:               string(record.State),
	}
	if record.PresentedProjectionRevision != nil {
		revision.PresentedProjectionRevision = *record.PresentedProjectionRevision
	}
	// A surface read that fails leaves the interaction revisions intact rather
	// than failing the whole sync: knowing less is better than showing nothing,
	// and nothing about correctness depends on this number.
	if snapshot, surfaceErr := d.service.interactions.GetSurfaceInteractions(
		ctx, interaction.GetSurfaceInput{
			SurfaceID: record.SurfaceID, RequesterScope: d.caller.Scope,
			Capability: surfaceCapability,
		},
	); surfaceErr == nil {
		revision.SurfaceRevision = snapshot.Surface.Revision
	}
	return revision, nil
}

// Resolve records the participant's immutable terminal response.
//
// The response is stored verbatim so a caller retrieving it later — through
// interaction_get, interaction_await, or an identical retry of the original
// invocation — receives byte-identical bytes rather than a re-derivation.
func (d *roomDisposition) Resolve(
	ctx context.Context,
	roomID string,
	env *envelopes.Envelope,
	resp *envelopes.Response,
) error {
	record, err := d.ensurePresented(ctx, 0, "")
	if err != nil {
		return err
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("roomflow: marshal participant response for %q: %w", env.ID, err)
	}
	responseKind, err := d.service.normalizer.PinnedResponseKind(env.Type)
	if err != nil {
		return err
	}
	// The canonical presentation revision is established once, when the
	// interaction first became visible, and stays fixed for its life. The
	// room's own revision counter is a transport-level attachment counter that
	// restarts at one after a reconnect or a process restart; using it here
	// would make a resolution fail the interaction's revision check for the
	// entirely ordinary reason that the operator refreshed their tab.
	presented := int64(1)
	if record.PresentedProjectionRevision != nil {
		presented = *record.PresentedProjectionRevision
	}
	_, err = d.service.interactions.ResolveInteraction(ctx, interaction.ResolveInteractionInput{
		InteractionID:               record.ID,
		ExpectedInteractionRevision: record.Revision,
		PresentedProjectionRevision: presented,
		Participant:                 Participant,
		ResponseKind:                responseKind,
		ResponsePayload:             payload,
		SubmittedAt:                 time.Now().UTC(),
		Capability:                  surfaceCapability,
	})
	switch {
	case err == nil:
		d.service.logger.Info("roomflow: recorded durable resolution",
			"room", roomID, "envelope", env.ID, "interaction", record.ID)
		return nil
	case errors.Is(err, interaction.ErrTerminal):
		return fmt.Errorf("%w: %s", room.ErrDispositionTerminal, record.ID)
	default:
		return err
	}
}

// Cancel records an explicit participant cancellation. Participant
// cancellation is one of only two authorized ways an interaction becomes
// terminal; a lost socket or an expired caller never reaches this path.
func (d *roomDisposition) Cancel(ctx context.Context, roomID string, env *envelopes.Envelope) error {
	record, err := d.ensurePresented(ctx, 0, "")
	if err != nil {
		return err
	}
	_, err = d.service.interactions.CancelInteraction(ctx, interaction.CancelInteractionInput{
		InteractionID: record.ID, ExpectedRevision: record.Revision,
		Requester: Participant, Cause: interaction.TerminalCauseParticipantCanceled,
		Reason:     "participant cancelled the room workflow",
		Capability: surfaceCapability,
	})
	switch {
	case err == nil:
		d.service.logger.Info("roomflow: recorded participant cancellation",
			"room", roomID, "envelope", env.ID, "interaction", record.ID)
		return nil
	case errors.Is(err, interaction.ErrTerminal):
		return fmt.Errorf("%w: %s", room.ErrDispositionTerminal, record.ID)
	default:
		return err
	}
}

// ensurePresented guarantees the interaction carries its participant binding
// and presentation revision before a terminal disposition is attempted.
//
// Reporting a presentation can fail (a transient database error, a race with
// another attachment) without anything noticing, because presentation is not a
// lifecycle fact. A terminal disposition is, so it re-establishes the binding
// itself rather than trusting that the earlier report succeeded.
func (d *roomDisposition) ensurePresented(
	ctx context.Context,
	revision int64,
	connectionID string,
) (interaction.InteractionRecord, error) {
	// Three passes is enough for the only contended sequence: read staged,
	// establish the binding, read it back.
	for range 3 {
		outcome, err := d.service.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
			InteractionID: d.interactionID, RequesterScope: d.caller.Scope,
			Capability: surfaceCapability,
		})
		if err != nil {
			return interaction.InteractionRecord{}, err
		}
		record := outcome.Interaction
		switch {
		case isTerminal(record.State):
			return interaction.InteractionRecord{}, fmt.Errorf(
				"%w: %s", room.ErrDispositionTerminal, record.ID)
		case record.State == interaction.InteractionStatePresented,
			record.State == interaction.InteractionStateInProgress:
			return record, nil
		}
		projection := revision
		if projection < 1 {
			projection = 1
		}
		if _, err := d.service.interactions.AcknowledgePresentation(ctx, interaction.PresentInteractionInput{
			InteractionID: record.ID, ExpectedRevision: record.Revision,
			PresentedProjectionRevision: projection, Participant: Participant,
			ConnectionID: connectionID, Capability: surfaceCapability,
		}); err != nil {
			if errors.Is(err, interaction.ErrRevisionConflict) ||
				errors.Is(err, interaction.ErrNotRespondable) {
				// Another attachment advanced it first; re-read and use theirs.
				continue
			}
			if errors.Is(err, interaction.ErrTerminal) {
				return interaction.InteractionRecord{}, fmt.Errorf(
					"%w: %s", room.ErrDispositionTerminal, record.ID)
			}
			return interaction.InteractionRecord{}, err
		}
	}
	return interaction.InteractionRecord{}, fmt.Errorf(
		"roomflow: could not establish presentation for interaction %q", d.interactionID)
}
