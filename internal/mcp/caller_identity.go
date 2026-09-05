package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/roomflow"
	"github.com/hollis-labs/tangent/internal/telemetry"
)

// This file is the single seam where a tool call becomes a caller identity.
//
// Before ADR 0004, every generic tool took its authorization value straight
// off the wire: `caller.scope`, `requester_scope`, and `owner_scope` were
// whatever the caller typed, and every check in the durable substrate was a
// string comparison against that. The arguments are still accepted so the
// shipped schemas do not break — but only their *partition* half survives.
// The authority is assigned here, from admission facts, and no request can
// spell it.
//
// The result of that split, stated plainly so nothing downstream mistakes it:
// within `standalone-local` a partition prevents accident, not intent. Any
// local caller can assert any application id. Isolation is enforced only
// across authorities, where the prefix is host-assigned.

// callerIdentity derives the durable caller identity for one tool call.
//
// The seventeen workflow tools and the seven `session_*` tools carry no caller
// argument at all, so they resolve to the anonymous partition of the local
// authority. That is the honest answer: nothing on those code paths declares
// an application, and inventing one would manufacture an isolation the product
// cannot back.
//
// When a trusted in-process adapter composes a verified caller binding (ADR
// 0004 §10.1), this is the one place that binding is read.
func callerIdentity(_ *mcpsdk.CallToolRequest) interaction.ActorBinding {
	return roomflow.DefaultCaller
}

// declaredCaller derives the caller identity for a tool that does accept a
// caller assertion, from whatever application id or legacy scope string it
// supplied.
func declaredCaller(declared, principalRef string) interaction.ActorBinding {
	binding := roomflow.CallerFor(declared)
	if principalRef != "" {
		// The principal ref is attribution, never authority: ADR 0004 §1 makes
		// the caller agent a row of the matrix that holds nothing of its own.
		binding.PrincipalRef = principalRef
	}
	return binding
}

// requesterScope derives the scope a read is authorized against from a
// wire-supplied `requester_scope`. The argument survives; its authority does
// not.
func requesterScope(declared string) string {
	return authz.CallerScope(declared)
}

// authorizeRoom applies the ADR 0004 §7 matrix to one room-backed operation.
//
// Every room tool calls exactly this, so the matrix is consulted in one place
// per transport rather than re-implemented per tool. When the durable
// substrate is not installed there is no ownership to check against, and the
// legacy compatibility behavior is preserved unchanged.
func (s *Server) authorizeRoom(
	ctx context.Context,
	roomID string,
	caller interaction.ActorBinding,
	capability authz.Capability,
	isolation authz.Isolation,
) error {
	if s.roomflow == nil {
		return nil
	}
	err := s.roomflow.AuthorizeRoom(ctx, roomID, caller, capability, isolation)
	if err != nil {
		// Recorded here rather than at the dozen call sites that render the
		// refusal, so a new room-backed tool acquires the observation by
		// routing through the check it already has to route through. The
		// ADR 0004 §5 split is preserved in the code: `not_found` is a
		// cross-authority refusal and `forbidden` is one inside an authority,
		// and collapsing them would lose the distinction the contract exists
		// to make.
		code := "forbidden"
		if isNotFound(err) {
			code = "not_found"
		}
		s.telemetry.Emit(ctx, telemetry.Event{
			Name:    telemetry.EventCapabilityDenied,
			Outcome: telemetry.OutcomeRefused,
			Code:    code,
			Correlation: telemetry.Correlation{
				Trace:       telemetry.TraceForRoom(roomID),
				Span:        telemetry.NewSpanID(),
				RoomID:      roomID,
				CallerScope: caller.Scope,
			},
			Attrs: []telemetry.Attr{
				telemetry.String(telemetry.AttrNamespace, "object-access"),
				telemetry.String(telemetry.AttrCapability, string(capability)),
				telemetry.String(telemetry.AttrTransport, "mcp"),
			},
		})
	}
	return err
}

// roomAuthorizationError renders a refused room operation.
//
// Cross-authority denial is reported as `room not found`, matching what an
// unknown id already returns, so a foreign authority cannot probe for
// existence. In-authority denial says only that the caller is not authorized:
// it never names the required capability, the owning scope, the session, or
// the participant.
func roomAuthorizationError(roomID string, err error) *mcpsdk.CallToolResult {
	if isNotFound(err) {
		return toolErrorResult(errorCodeRoomNotFound, "room \""+roomID+"\" not found")
	}
	return toolErrorResult(errorCodeRoomForbidden, "not authorized for room \""+roomID+"\"")
}

func isNotFound(err error) bool {
	return errors.Is(err, interaction.ErrNotFound)
}

// surfaceMetadataWithAttribution keeps a wire-supplied owner scope as a label
// beside a surface without letting it decide anything.
//
// ADR 0004 §3.2: where a caller supplied a scope string different from the one
// the adapter derived, it is stored as attribution and ignored for
// authorization. Discarding it silently would lose a caller's own record of
// what it asked for; honoring it would be the bug this decision removes.
func surfaceMetadataWithAttribution(metadata map[string]any, declaredOwner, declaredCallerScope string) json.RawMessage {
	declaredOwner = strings.TrimSpace(declaredOwner)
	declaredCallerScope = strings.TrimSpace(declaredCallerScope)
	if declaredOwner == "" && declaredCallerScope == "" {
		return rawJSON(metadata)
	}
	labels := map[string]any{}
	for key, value := range metadata {
		labels[key] = value
	}
	attribution := map[string]any{}
	if declaredOwner != "" {
		attribution["owner_scope"] = declaredOwner
	}
	if declaredCallerScope != "" {
		attribution["caller_scope"] = declaredCallerScope
	}
	labels["declared_by_caller"] = attribution
	return rawJSON(labels)
}
