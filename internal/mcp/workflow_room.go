package mcp

import (
	"context"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// resolveWorkflowRoom names the room one named workflow invocation runs in.
//
// Three sources, in strict precedence order:
//
//  1. The room a durable interaction for this identity is already bound to.
//  2. The room the caller named in envelope meta.
//  3. A freshly minted room.
//
// The durable lookup is first, and that ordering is the contract rather than
// an optimization. A caller repeating an invocation without naming a room is
// repeating a request Tangent already owns, and that request is already bound
// to a room whose id appears in the handle the caller was given. Minting a
// second room for it produced a second envelope projection of one interaction,
// an unbounded row for every retry in a retry loop, and a receipt whose
// room_id and url contradicted the interaction's own legacy_room_id — so the
// documented recovery route sent a human to the wrong room.
//
// The lookup is skipped when the durable substrate is absent: an embedder
// without an interaction service has no identity to recognize, and the v0.12
// blocking path this falls back to has no handle to keep stable.
func (s *Server) resolveWorkflowRoom(
	ctx context.Context,
	workflow string,
	env *envelopes.Envelope,
) (string, *mcpsdk.CallToolResult, error) {
	if s.roomflow != nil {
		bound, known, err := s.roomflow.Recognize(ctx, callerIdentity(nil), env)
		if err != nil {
			return "", triageErrorResult(err), nil
		}
		if known && bound != "" {
			s.logWorkflowRoomReused(workflow, bound, env.ID)
			return bound, nil, nil
		}
	}
	if roomID, named := metaRoomID(env.Meta); named && roomID != "" {
		s.logWorkflowRoomReused(workflow, roomID, env.ID)
		return roomID, nil, nil
	}
	createRes, created, err := s.handleSessionCreate(ctx, nil, sessionCreateInput{
		Meta: map[string]any{
			"envelopeID":   env.ID,
			"envelopeType": env.Type,
		},
	})
	if err != nil {
		return "", nil, err
	}
	if createRes.IsError {
		return "", createRes, nil
	}
	s.logWorkflowRoomCreated(workflow, created.RoomID, env.ID)
	return created.RoomID, nil, nil
}
