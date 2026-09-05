package roomflow

import (
	"context"
	"errors"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/interaction"
)

// This file is where room-backed tools ask the ADR 0004 §7 matrix a question.
//
// Rooms are the compatibility vocabulary of ADR 0001 §10, and one room is one
// surface: `EnsureLegacyRoomSurface` uses the room id as the surface id, so a
// room's owner scope is its surface's owner scope. That is the whole mapping,
// and it is why the seventeen workflow tools and the seven `session_*` tools
// can be scoped without inventing a second ownership model for rooms.

// CallerFor assigns the host-derived caller identity for a direct loopback
// call that declared applicationID.
//
// The authority half of the scope is assigned here from admission facts and
// cannot be influenced by the argument; only the partition is caller-supplied.
// A caller that declares nothing is `standalone-local:anonymous` — a real
// partition, not a missing one.
//
// The partitions this produces are advisory. Any local caller can assert any
// application id, so a partition prevents one agent's retry from canceling
// another agent's item; it does not prevent one agent from claiming to be
// another. See authz.AdvisoryPartitionNotice.
func CallerFor(applicationID string) interaction.ActorBinding {
	return interaction.ActorBinding{
		Scope:        authz.CallerScope(applicationID),
		PrincipalRef: DefaultCaller.PrincipalRef,
		Authority:    authz.AuthorityStandaloneLocal,
		Assurance:    "loopback-unverified",
	}
}

// EnsureRoomOwnership records the caller that owns a room, at creation time
// rather than at first advance.
//
// Without it a room exists for a window with no owner, and an unowned room is
// one that every authorization check has to guess about. Creating the surface
// eagerly means `session_list`, `session_get`, and `session_close` have a real
// answer for a room the moment it exists.
func (s *Service) EnsureRoomOwnership(
	ctx context.Context,
	roomID string,
	caller interaction.ActorBinding,
) error {
	if caller == (interaction.ActorBinding{}) {
		caller = DefaultCaller
	}
	_, _, err := s.interactions.EnsureLegacyRoomSurface(ctx, interaction.EnsureLegacyRoomSurfaceInput{
		RoomID: roomID, Caller: caller, OwnerScope: caller.Scope,
		Metadata: roomSurfaceMetadata(roomID), Capability: surfaceCapability,
	})
	return err
}

// OwnerScope returns the caller scope that owns a room, and whether the room
// has an owner at all.
//
// A room with no durable surface yet has no owner. That is not an error: it is
// a room created by an embedder without the durable substrate, or one racing
// its own first advance, and the caller-facing decision for it is made by
// [AuthorizeRoom] rather than here.
func (s *Service) OwnerScope(ctx context.Context, roomID string) (string, bool, error) {
	scope, err := s.interactions.SurfaceOwnerScope(ctx, roomID)
	if errors.Is(err, interaction.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return scope, true, nil
}

// AuthorizeRoom is the single check every room-backed tool routes through.
//
// It resolves the object (the room's owner scope), then asks authz.Authorize
// for the matrix cell. The refusal it returns is already in the shape ADR 0004
// §5 requires — ErrUnauthorized within an authority, ErrNotFound across one —
// so no transport re-derives that distinction.
//
// A room with no owner is permitted. It cannot be another authority's, because
// only a durable surface records an authority at all, and refusing it would
// break embedders that construct the MCP server without the durable substrate.
func (s *Service) AuthorizeRoom(
	ctx context.Context,
	roomID string,
	caller interaction.ActorBinding,
	capability authz.Capability,
	isolation authz.Isolation,
) error {
	owner, found, err := s.OwnerScope(ctx, roomID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return TranslateDecision(authz.Authorize(authz.Request{
		Kind:       authz.KindCallerApplication,
		Scope:      caller.Scope,
		OwnerScope: owner,
		Capability: capability,
		Isolation:  isolation,
	}))
}

// VisibleToCaller reports whether a room may appear in a caller's listing.
//
// Listing is authority-wide, unchanged, so today's "show me all my rooms"
// behavior is preserved for the legacy `session_*` vocabulary. A foreign
// authority's rooms are omitted, never counted, and never errored on — an
// error would itself be an existence signal.
func (s *Service) VisibleToCaller(
	ctx context.Context,
	roomID string,
	caller interaction.ActorBinding,
) bool {
	return s.AuthorizeRoom(ctx, roomID, caller, authz.View, authz.AuthorityWide) == nil
}

// TranslateDecision maps an authz refusal onto the durable substrate's error
// vocabulary, so one decision produces one error shape everywhere.
//
// The two shapes must not be collapsed: ErrUnauthorized surfaces as 403 and
// names nothing, ErrNotFound surfaces as 404 so a foreign authority cannot
// probe for existence.
func TranslateDecision(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authz.ErrNotFound):
		return interaction.ErrNotFound
	default:
		return interaction.ErrUnauthorized
	}
}
