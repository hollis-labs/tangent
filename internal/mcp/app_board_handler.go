package mcp

import (
	"context"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"
)

type appBoardInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

// handleAppBoard dispatches an app-board envelope into a room.
//
// It is deliberately the shortest room-workflow handler in this package, and
// the absences are the design (ADR 0007 §6):
//
//   - **No room phase state.** Compare dashboard_handler.go, which projects a
//     snapshot into the room and merges the caller's envelope over it. The
//     board's content is whatever the caller last sent; a new revision arrives
//     through tangent.interaction_supersede, and the record of it is the
//     interaction's own revision history rather than a second projection this
//     handler would have to keep consistent with it.
//   - **No submit normalization.** There is no normalizeAppBoardSubmitResponse
//     and roomflow_adapter's switch has no arm for this kind, so a response
//     falls through to the default and is returned as the participant sent it.
//     The caller applies the consequence with its own tools. A normalizer here
//     would be the beginning of Tangent knowing what an action means.
//   - **No write to the owning application.** Nothing in this file can reach
//     one: there is no client, no credential and no address. That is §6's test
//     stated as an absence rather than a check.
//
// What the participant does to the board in the meantime — filters, selection,
// whether the detail pane is open — is not this handler's business either. It
// arrives as a draft frame on the WebSocket, goes through Room.HandleDraftFrom
// to roomDisposition.Draft, and the caller reads it back through
// tangent.surface_get. Nothing pushes it.
//
// Callers who want the board to stay open pass completion mode "async": the
// receipt returns immediately, nothing is cancelled, and the interaction stays
// pending for as long as the work takes.
func (s *Server) handleAppBoard(
	ctx context.Context,
	args appBoardInput,
) (any, error) {
	if args.Envelope.Type != appBoardEnvelopeType {
		return nil, toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.app-board rejects envelope type %q; want %q",
				args.Envelope.Type, appBoardEnvelopeType,
			),
		)
	}

	roomID, err := s.resolveWorkflowRoom(ctx, "app-board", &args.Envelope)
	if err != nil {
		return nil, err
	}

	// The presented envelope is the request. There is nothing persisted to
	// merge over it, so passing nil would be equivalent; it is explicit so the
	// difference from dashboard_handler.go is legible rather than inferred.
	return s.advanceRoomEnvelope(
		ctx, roomID, &args.Envelope, cloneEnvelopeForDispatch(&args.Envelope), args.Completion)
}
