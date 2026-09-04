package roomflow

import (
	"context"
	"errors"
	"time"

	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/telemetry"
)

// Correlation is minted here because this is where a caller invocation
// acquires its durable identity.
//
// Everything downstream — the room presentation, the WebSocket frame that
// answers it, the delivery that hands the outcome back, a health report naming
// the kind — recomputes the same trace id from the interaction record rather
// than being handed one. See internal/telemetry/correlation.go for why the
// identity is derived rather than propagated.

// correlationFor builds the dimensions one observation of this interaction is
// filed under. Every field is a Tangent identifier or a host-assigned label;
// the presented envelope, the request snapshot, and the room URL are all
// deliberately absent.
func correlationFor(
	record interaction.InteractionRecord,
	roomID, envelopeID string,
) telemetry.Correlation {
	return telemetry.Correlation{
		Trace:             telemetry.TraceForRecord(record.CallerScope, record.IdempotencyKey, record.ID),
		Span:              telemetry.NewSpanID(),
		SurfaceID:         record.SurfaceID,
		InteractionID:     record.ID,
		RoomID:            roomID,
		EnvelopeID:        envelopeID,
		DefinitionKind:    record.Definition.Kind,
		DefinitionVersion: record.Definition.Version,
		CallerScope:       record.CallerScope,
		ParticipantScope:  record.ParticipantScope,
	}
}

// pendingCorrelation is the identity of an invocation that has no record yet —
// a conflict, or a definition this build cannot serve. The trace is the same
// one the record would have had, because it is derived from the same two
// values, so a refused attempt and a later successful retry share a trace.
func pendingCorrelation(
	caller interaction.ActorBinding,
	idempotencyKey, kind, roomID, envelopeID string,
) telemetry.Correlation {
	return telemetry.Correlation{
		Trace:          telemetry.TraceFor(caller.Scope, idempotencyKey),
		Span:           telemetry.NewSpanID(),
		RoomID:         roomID,
		EnvelopeID:     envelopeID,
		DefinitionKind: kind,
		CallerScope:    caller.Scope,
	}
}

// interactionErrorCodes maps this package's reachable failures onto typed
// codes. It lives beside the call sites rather than in internal/telemetry so
// the sentinels it names are the ones this package can actually see, and so
// nothing is ever classified by reading an error message.
var interactionErrorCodes = []telemetry.ErrorCode{
	{Sentinel: interaction.ErrIdempotencyConflict, Code: "idempotency_conflict"},
	{Sentinel: interaction.ErrDefinitionUnavailable, Code: "definition_unavailable"},
	{Sentinel: interaction.ErrDefinitionNotFound, Code: "definition_not_found"},
	{Sentinel: interaction.ErrRevisionConflict, Code: "revision_conflict"},
	{Sentinel: interaction.ErrTerminal, Code: "terminal"},
	{Sentinel: interaction.ErrNotRespondable, Code: "not_respondable"},
	{Sentinel: interaction.ErrUnauthorized, Code: "unauthorized"},
	{Sentinel: interaction.ErrNotFound, Code: "not_found"},
	{Sentinel: interaction.ErrWaitTimeout, Code: "wait_timeout"},
	{Sentinel: interaction.ErrInvalidRecord, Code: "invalid_record"},
}

// emit records one observation. It is a method so a nil recorder is handled in
// one place rather than at every call site.
func (s *Service) emit(ctx context.Context, event telemetry.Event) {
	s.telemetry.Emit(ctx, event)
}

// reportPending records a caller whose transport gave up before the human
// answered.
//
// It is an `ok` outcome and not a refusal, for the same reason the caller
// receives a receipt rather than an error: nothing failed, and counting it as
// a failure would make the most ordinary thing this product does look like an
// incident. The duration is how long the caller actually waited, which is the
// number an operator tuning the compatibility window needs.
func (s *Service) reportPending(
	ctx context.Context,
	correlation telemetry.Correlation,
	mode string,
	admitted time.Time,
) {
	s.emit(ctx, telemetry.Event{
		Name:        telemetry.EventInteractionPending,
		Outcome:     telemetry.OutcomeOK,
		Correlation: correlation.WithSpan(),
		Duration:    since(admitted, s.telemetry.Now()),
		Attrs:       []telemetry.Attr{telemetry.String(telemetry.AttrMode, mode)},
	})
}

// reportUnservableDefinition records a renderer this build cannot serve.
//
// It is filed under the definition's own trace rather than the invocation's,
// because the fact is about the kind and not about this one caller: the next
// caller to ask for the same kind will hit the same wall, and a health report
// naming that kind points at this same trace.
func (s *Service) reportUnservableDefinition(ctx context.Context, err error) {
	state := &interaction.DefinitionStateError{}
	if !errors.As(err, &state) {
		return
	}
	s.emit(ctx, telemetry.Event{
		Name:    telemetry.EventRendererUnavailable,
		Outcome: telemetry.OutcomeRefused,
		Code:    telemetry.Code(state.ErrorCode),
		Correlation: telemetry.Correlation{
			Trace:             telemetry.TraceForKind(state.Kind),
			Span:              telemetry.NewSpanID(),
			DefinitionKind:    state.Kind,
			DefinitionVersion: state.Version,
		},
		Attrs: []telemetry.Attr{
			telemetry.String(telemetry.AttrMaterializationState, state.State),
			telemetry.String(telemetry.AttrTransport, "mcp"),
		},
	})
}

// since measures an elapsed interval that is never negative. Two clocks are in
// play — the store stamps a record and the process measures now — and a
// negative duration would corrupt a histogram rather than reporting a clock
// skew nobody can act on.
func since(start time.Time, now time.Time) time.Duration {
	if start.IsZero() || !now.After(start) {
		return 0
	}
	return now.Sub(start)
}
