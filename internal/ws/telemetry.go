package ws

import (
	"context"
	"encoding/hex"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/telemetry"
)

// The WebSocket boundary is where the correlation identity is *reconstructed*
// rather than carried.
//
// Nothing about a browser frame carries a trace. The tab supplies a client id
// and an envelope id, and that is all it is trusted to supply — a
// client-asserted trace header would be a correlation identifier the host
// cannot vouch for, filed against work it may not own. So the handler asks the
// room for the durable record behind the presentation and recomputes the same
// trace the caller's invocation was admitted under. A refusal here therefore
// lands in the same trace as the MCP call it refuses, across a process
// boundary that carried nothing.
//
// Two things genuinely do not join that trace, and are filed under the room's
// own trace instead:
//
//   - Connection lifecycle. Attaching and detaching happen whether or not
//     there is work on the surface.
//   - A frame naming an envelope the surface is not presenting. There is no
//     record to reconstruct from, which is exactly why the frame was ignored.

// correlate builds the correlation for one frame against one envelope.
//
// The connection id is always present because the transport knows it; the
// interaction identity is present only when the room could resolve it, which
// is honest — a frame naming an already-settled envelope has no interaction
// behind it any more.
func (h *Handler) correlate(
	ctx context.Context,
	rm *room.Room,
	c *room.Connection,
	envelopeID string,
) telemetry.Correlation {
	correlation := telemetry.Correlation{
		Trace:            telemetry.TraceForRoom(rm.ID),
		Span:             telemetry.NewSpanID(),
		RoomID:           rm.ID,
		EnvelopeID:       envelopeID,
		ParticipantScope: c.Participant().Scope,
	}
	if c != nil {
		correlation.ConnectionID = c.ID()
	}
	if envelopeID == "" {
		return correlation
	}
	durable, ok := rm.DurableRevisionFor(ctx, envelopeID)
	if !ok {
		return correlation
	}
	correlation.SurfaceID = durable.SurfaceID
	correlation.InteractionID = durable.InteractionID
	correlation.DefinitionKind = durable.DefinitionKind
	if trace, err := hex.DecodeString(durable.TraceID); err == nil && len(trace) == len(telemetry.TraceID{}) {
		copy(correlation.Trace[:], trace)
	}
	return correlation
}

// emit records one observation, tolerating a handler with no recorder.
func (h *Handler) emit(ctx context.Context, event telemetry.Event) {
	h.telemetry.Emit(ctx, event)
}

// reportAttachment records one client attaching to a surface.
//
// `replaced` is what makes a reconnect countable: the same client id coming
// back while its previous socket was still attached is a refresh, and it is
// the single fact that distinguishes "the operator reloaded the tab" from "a
// second person opened the room". It is read off the connection rather than
// inferred from a count, because inferring it would be a second, weaker copy
// of a decision internal/room already made under its own lock.
func (h *Handler) reportAttachment(ctx context.Context, rm *room.Room, c *room.Connection) {
	h.emit(ctx, telemetry.Event{
		Name:    telemetry.EventConnectionAttached,
		Outcome: telemetry.OutcomeOK,
		Correlation: telemetry.Correlation{
			Trace:            telemetry.TraceForRoom(rm.ID),
			Span:             telemetry.NewSpanID(),
			RoomID:           rm.ID,
			ConnectionID:     c.ID(),
			ParticipantScope: c.Participant().Scope,
		},
		Attrs: []telemetry.Attr{
			telemetry.Bool(telemetry.AttrReplaced, c.Replaced()),
			telemetry.Bool(telemetry.AttrLeaseInherited, c.InheritedLease()),
			telemetry.String(telemetry.AttrRole, string(rm.RoleOf(c))),
			telemetry.String(telemetry.AttrClientKind, c.ClientKind()),
			telemetry.Int(telemetry.AttrConnections, int64(rm.ConnectionCount())),
		},
	})
}

// reportDetachment records one client leaving.
func (h *Handler) reportDetachment(ctx context.Context, rm *room.Room, c *room.Connection, attached bool) {
	if !attached {
		// A detach of a connection that was already gone is not an event; it
		// is the second half of a replacement the attach already recorded.
		return
	}
	h.emit(ctx, telemetry.Event{
		Name:    telemetry.EventConnectionDetached,
		Outcome: telemetry.OutcomeOK,
		Correlation: telemetry.Correlation{
			Trace:            telemetry.TraceForRoom(rm.ID),
			Span:             telemetry.NewSpanID(),
			RoomID:           rm.ID,
			ConnectionID:     c.ID(),
			ParticipantScope: c.Participant().Scope,
		},
		Attrs: []telemetry.Attr{
			telemetry.Int(telemetry.AttrConnections, int64(rm.ConnectionCount())),
		},
	})
}

// reportPresentationRefusal records a participant action the surface declined.
//
// The code is the same stable string the client is sent in its error frame, so
// the row an operator reads and the message the human saw name the same thing.
// A stale client is exactly `stale_presentation` (a failed compare-and-set
// against the frame this connection was actually shown) or `resolver_lease_held`
// (another live connection owns the surface).
func (h *Handler) reportPresentationRefusal(
	ctx context.Context,
	rm *room.Room,
	c *room.Connection,
	msg inboundMessage,
	code string,
) {
	correlation := h.correlate(ctx, rm, c, msg.EnvelopeID)
	h.emit(ctx, telemetry.Event{
		Name:        telemetry.EventPresentationRefused,
		Outcome:     telemetry.OutcomeRefused,
		Code:        code,
		Correlation: correlation,
		Attrs: []telemetry.Attr{
			telemetry.String(telemetry.AttrRefusal, code),
			telemetry.String(telemetry.AttrRole, string(rm.RoleOf(c))),
			telemetry.String(telemetry.AttrTransport, "websocket"),
			telemetry.Int(telemetry.AttrPresentedRevision, msg.Revision),
		},
	})
}

// reportCapabilityDenial records a frame the attached session may not send.
//
// It is filed in the object-access namespace, which ADR 0004 §2 keeps strictly
// apart from the host-mediated effect namespace: a participant that may not
// resolve and a renderer that may not write a file are two different refusals
// and collapsing them into one counter would make neither actionable. The
// capability is recorded because it is a host-published name; nothing about
// what the session *does* hold ever appears, for the same reason the client's
// error message says nothing (ADR 0004 §6.5).
func (h *Handler) reportCapabilityDenial(
	ctx context.Context,
	rm *room.Room,
	c *room.Connection,
	msg inboundMessage,
	capability authz.Capability,
) {
	h.emit(ctx, telemetry.Event{
		Name:        telemetry.EventCapabilityDenied,
		Outcome:     telemetry.OutcomeRefused,
		Code:        errorCodeNotAuthorized,
		Correlation: h.correlate(ctx, rm, c, msg.EnvelopeID),
		Attrs: []telemetry.Attr{
			telemetry.String(telemetry.AttrNamespace, "object-access"),
			telemetry.String(telemetry.AttrCapability, string(capability)),
			telemetry.String(telemetry.AttrTransport, "websocket"),
		},
	})
}
