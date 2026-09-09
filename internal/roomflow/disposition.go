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
	"github.com/hollis-labs/tangent/internal/telemetry"
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
	roomID string,
	env *envelopes.Envelope,
	revision int64,
	connectionID string,
) error {
	envelopeID := ""
	if env != nil {
		envelopeID = env.ID
	}
	_, err := d.ensurePresented(ctx, roomID, envelopeID, revision, connectionID)
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
		DefinitionKind:      record.Definition.Kind,
		// The trace is recomputed from the record here rather than being
		// carried from the caller's goroutine, which is the whole reason the
		// identity is derived: this call happens on a WebSocket's goroutine,
		// possibly in a different process generation from the one that
		// admitted the request, and it still produces the same trace.
		TraceID: telemetry.TraceForRecord(
			record.CallerScope, record.IdempotencyKey, record.ID).String(),
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
	record, err := d.ensurePresented(ctx, roomID, env.ID, 0, "")
	if err != nil {
		return err
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("roomflow: marshal participant response for %q: %w", env.ID, err)
	}
	// The durable record keeps the whole response frame, unchanged; the
	// definition's response_schema validates the payload inside it. They are
	// different values and are passed as different fields, because a schema
	// authored to describe "what may come back" describes the payload, not the
	// envelope carrying it.
	body, err := json.Marshal(resp.Payload)
	if err != nil {
		return fmt.Errorf("roomflow: marshal participant response payload for %q: %w", env.ID, err)
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
		ResponseBody:                body,
		SubmittedAt:                 time.Now().UTC(),
		Capability:                  surfaceCapability,
	})
	correlation := correlationFor(record, roomID, env.ID)
	switch {
	case err == nil:
		d.service.logger.Info("roomflow: recorded durable resolution",
			"room", roomID, "envelope", env.ID, "interaction", record.ID)
		// The duration is presentation → resolution: how long the human took.
		// It is measured from the record's own presented_at rather than from
		// anything this process remembers, so a resolution that follows a
		// restart still reports the real interval.
		d.service.emit(ctx, telemetry.Event{
			Name:        telemetry.EventInteractionResolved,
			Outcome:     telemetry.OutcomeOK,
			Correlation: correlation,
			Duration:    sincePresented(record, d.service.telemetry.Now()),
			Attrs: []telemetry.Attr{
				telemetry.Int(telemetry.AttrRevision, record.Revision),
				telemetry.Int(telemetry.AttrPresentedRevision, presented),
				telemetry.String(telemetry.AttrTransport, "websocket"),
			},
		})
		return nil
	case errors.Is(err, interaction.ErrTerminal):
		d.reportDispositionRefusal(ctx, correlation, "terminal")
		return fmt.Errorf("%w: %s", room.ErrDispositionTerminal, record.ID)
	default:
		d.reportDispositionRefusal(ctx, correlation,
			telemetry.CodeForError(err, interactionErrorCodes))
		return err
	}
}

// reportDispositionRefusal records a terminal participant action the canonical
// authority declined. It is a refusal and not a failure: the interaction is
// intact and the participant can act again.
func (d *roomDisposition) reportDispositionRefusal(
	ctx context.Context,
	correlation telemetry.Correlation,
	code string,
) {
	d.service.emit(ctx, telemetry.Event{
		Name:        telemetry.EventPresentationRefused,
		Outcome:     telemetry.OutcomeRefused,
		Code:        code,
		Correlation: correlation.WithSpan(),
		Attrs:       []telemetry.Attr{telemetry.String(telemetry.AttrTransport, "websocket")},
	})
}

// sincePresented is presentation → now, from the durable record's own
// timestamp. A record with no presented_at reports no duration rather than a
// misleading one measured from creation.
func sincePresented(record interaction.InteractionRecord, now time.Time) time.Duration {
	if record.PresentedAt == nil {
		return 0
	}
	return since(*record.PresentedAt, now)
}

// Cancel records an explicit participant cancellation. Participant
// cancellation is one of only two authorized ways an interaction becomes
// terminal; a lost socket or an expired caller never reaches this path.
func (d *roomDisposition) Cancel(ctx context.Context, roomID string, env *envelopes.Envelope) error {
	record, err := d.ensurePresented(ctx, roomID, env.ID, 0, "")
	if err != nil {
		return err
	}
	_, err = d.service.interactions.CancelInteraction(ctx, interaction.CancelInteractionInput{
		InteractionID: record.ID, ExpectedRevision: record.Revision,
		Requester: Participant, Cause: interaction.TerminalCauseParticipantCanceled,
		Reason:     "participant cancelled the room workflow",
		Capability: surfaceCapability,
	})
	correlation := correlationFor(record, roomID, env.ID)
	switch {
	case err == nil:
		d.service.logger.Info("roomflow: recorded participant cancellation",
			"room", roomID, "envelope", env.ID, "interaction", record.ID)
		d.service.emit(ctx, telemetry.Event{
			Name:        telemetry.EventInteractionCanceled,
			Outcome:     telemetry.OutcomeOK,
			Correlation: correlation,
			Duration:    sincePresented(record, d.service.telemetry.Now()),
			Attrs: []telemetry.Attr{
				telemetry.String(telemetry.AttrTerminalCause,
					string(interaction.TerminalCauseParticipantCanceled)),
				telemetry.String(telemetry.AttrTransport, "websocket"),
			},
		})
		return nil
	case errors.Is(err, interaction.ErrTerminal):
		d.reportDispositionRefusal(ctx, correlation, "terminal")
		return fmt.Errorf("%w: %s", room.ErrDispositionTerminal, record.ID)
	default:
		d.reportDispositionRefusal(ctx, correlation,
			telemetry.CodeForError(err, interactionErrorCodes))
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
	roomID string,
	envelopeID string,
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
		acknowledged, err := d.service.interactions.AcknowledgePresentation(
			ctx, interaction.PresentInteractionInput{
				InteractionID: record.ID, ExpectedRevision: record.Revision,
				PresentedProjectionRevision: projection, Participant: Participant,
				ConnectionID: connectionID, Capability: surfaceCapability,
			})
		if err == nil {
			// This is the one place presentation actually happens, so it is
			// the only place that may measure it. The duration is admission →
			// presentation: how long the caller's request waited before a
			// human could see it, taken from the record's own created_at so a
			// restart between the two does not lose the interval.
			correlation := correlationFor(record, roomID, envelopeID)
			correlation.ConnectionID = connectionID
			d.service.emit(ctx, telemetry.Event{
				Name:        telemetry.EventInteractionPresented,
				Outcome:     telemetry.OutcomeOK,
				Correlation: correlation,
				Duration:    since(record.CreatedAt, d.service.telemetry.Now()),
				Attrs: []telemetry.Attr{
					telemetry.Int(telemetry.AttrPresentedRevision, projection),
					telemetry.Int(telemetry.AttrRevision, acknowledged.Revision),
					telemetry.String(telemetry.AttrTransport, "websocket"),
				},
			})
		}
		if err != nil {
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

// Draft records the participant's non-terminal state inside the interaction.
//
// This is the surface's view state — which filters are applied, which record is
// selected, whether a detail pane is open — and it is deliberately a different
// object from both a resolution and a channel's ViewFocus (ADR 0007 §3). It is
// interaction-scoped because it is meaningless without the interaction that
// defines what its contents refer to.
//
// Nothing here settles. The interaction stays open, the presentation stays
// live, and no caller waiting on a resolution is woken. A caller reads this by
// pulling `tangent.surface_get`, which already projects the draft revisions;
// Tangent never pushes a participant's in-progress state at a caller, and a
// caller must not present a draft as a decision.
func (d *roomDisposition) Draft(
	ctx context.Context,
	roomID string,
	env *envelopes.Envelope,
	draftRevision int64,
	payload json.RawMessage,
) error {
	envelopeID := ""
	if env != nil {
		envelopeID = env.ID
	}
	record, err := d.ensurePresented(ctx, roomID, envelopeID, 0, "")
	if err != nil {
		return err
	}
	_, err = d.service.interactions.SaveDraft(ctx, interaction.SaveDraftInput{
		InteractionID:       record.ID,
		DraftRevision:       draftRevision,
		InteractionRevision: record.Revision,
		Participant:         Participant,
		DefinitionVersion:   record.Definition.Version,
		Payload:             payload,
		Capability:          surfaceCapability,
	})
	correlation := correlationFor(record, roomID, envelopeID)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, interaction.ErrRevisionConflict):
		// The participant's browser built on a draft the record has moved past.
		// The client resynchronizes and retries; the payload is untouched and
		// cannot be reported, so the refusal carries revisions and a code and
		// nothing else. `Service.SaveDraft` has already emitted the telemetry.
		return fmt.Errorf("%w: %s", room.ErrDispositionDraftConflict, record.ID)
	case errors.Is(err, interaction.ErrTerminal):
		d.reportDispositionRefusal(ctx, correlation, "terminal")
		return fmt.Errorf("%w: %s", room.ErrDispositionTerminal, record.ID)
	default:
		d.reportDispositionRefusal(ctx, correlation,
			telemetry.CodeForError(err, interactionErrorCodes))
		return err
	}
}
