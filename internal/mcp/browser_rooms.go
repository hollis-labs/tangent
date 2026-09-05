package mcp

import (
	"context"
	"errors"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/roomflow"
)

// The browser room API's application half.
//
// internal/server owns the transport — origin guard, participant session,
// `Cache-Control: no-store` — and calls these three methods. They exist so the
// SPA stops POSTing JSON-RPC at `/mcp`, which is a route with no origin guard
// that reaches every session_* tool.
//
// The browser is a *caller application* here, not a participant (ADR 0004 §1
// names "the Tangent SPA's own browser API" in that row). The participant
// session proved which browser is asking; these methods decide what that
// caller may do, under exactly the same matrix the MCP tools use.

// browserCaller is the identity the SPA's own room API acts as.
//
// Its scope is the anonymous partition of the local authority — the same
// partition every unlabeled loopback MCP caller resolves to — because the
// browser declares no application id either, and pretending otherwise would
// manufacture a partition boundary that nothing verifies. The principal ref
// differs so the SPA's actions are attributable in the audit trail without
// being separately authorized.
//
// The consequence is deliberate and worth stating: the tab-strip close button
// can close rooms opened by unlabeled MCP callers, because they share a
// partition. It cannot close a `gateway:*`-owned room, and it cannot close a
// room opened by a caller that declared an application id. That is exactly the
// advisory-within-authority, enforced-across-authorities strength ADR 0004 §3
// commits to.
var browserCaller = func() interaction.ActorBinding {
	caller := roomflow.DefaultCaller
	caller.PrincipalRef = "tangent-spa"
	return caller
}()

// ListBrowserRooms implements server.RoomService.
func (s *Server) ListBrowserRooms(ctx context.Context, activeOnly bool) ([]room.RoomSummary, error) {
	rooms, err := s.manager.List(ctx, activeOnly)
	if err != nil {
		return nil, err
	}
	return s.visibleRooms(ctx, rooms, browserCaller), nil
}

// InspectBrowserRoom implements server.RoomService.
//
// The payload is the `tangent.session_get` result unchanged, so the SPA's
// decode path is identical either side of the migration and the browser API is
// a transport change rather than a contract change.
func (s *Server) InspectBrowserRoom(ctx context.Context, roomID string) (any, error) {
	if err := s.authorizeRoom(ctx, roomID, browserCaller, authz.View, authz.AuthorityWide); err != nil {
		return nil, err
	}
	state, found, err := s.roomProjection(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, interaction.ErrNotFound
	}
	return state, nil
}

// CloseBrowserRoom implements server.RoomService.
//
// This is the operation ADR 0004 §9 narrows, and the reason the SPA migration
// had to land in the same change: under partition enforcement the tab-strip
// close button could not keep reaching `session_close` through an
// unauthenticated `/mcp` POST.
func (s *Server) CloseBrowserRoom(ctx context.Context, roomID, status string) error {
	if status == "" {
		status = "closed"
	}
	if err := s.authorizeRoom(ctx, roomID, browserCaller, authz.Close, authz.PartitionScoped); err != nil {
		return err
	}
	if err := s.closeRoomDurably(ctx, roomID, status, browserCaller); err != nil {
		return err
	}
	if err := s.manager.Close(roomID, status); err != nil {
		if errors.Is(err, room.ErrRoomNotFound) {
			return interaction.ErrNotFound
		}
		return err
	}
	return nil
}
