package mcp

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/authz"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/roomflow"
)

// This file is acceptance criterion 2 at the tool layer: cross-caller list,
// get, and close are denied by default.
//
// It is an internal test because the caller identity is deliberately *not*
// reachable from a tool argument — that is the whole of ADR 0004 §3.2 — so the
// only honest way to vary it is to seed rooms owned by different scopes and
// call the handlers as the caller the adapter actually derives.

// TestSessionCloseIsPartitionEnforcingWhileReadsStayAuthorityWide covers the
// one behavior ADR 0004 deliberately narrows, together with the two it
// deliberately does not.
//
// The asymmetry is the decision: closing dispositions another caller's pending
// human work, so it is enforced even though `standalone-local` partitions are
// advisory for reads. Narrowing the reads as well would break "show me all my
// rooms" and buy nothing a partition can actually guarantee.
func TestSessionCloseIsPartitionEnforcingWhileReadsStayAuthorityWide(t *testing.T) {
	t.Parallel()
	server, manager := newAuthorizationFixture(t)
	ctx := context.Background()

	ownRoom := seedRoom(t, server, manager, roomflow.DefaultCaller)
	otherPartition := seedRoom(t, server, manager, roomflow.CallerFor("another-agent"))
	foreignAuthority := seedRoom(t, server, manager, gatewayCaller())

	// List is authority-wide: a caller sees its own partition and its
	// neighbors', and never another authority's.
	listedAny, err := server.handleSessionList(ctx, sessionListInput{ActiveOnly: true})
	if err != nil {
		t.Fatalf("handleSessionList: %v", err)
	}
	listed := listedAny.(sessionListResult)
	visible := map[string]bool{}
	for _, summary := range listed.Rooms {
		visible[summary.ID] = true
	}
	if !visible[ownRoom] || !visible[otherPartition] {
		t.Fatalf("session_list dropped a same-authority room: %v", visible)
	}
	if visible[foreignAuthority] {
		t.Fatal("session_list leaked a room belonging to another authority")
	}

	// Get is authority-wide too, and cross-authority reads are not-found
	// rather than forbidden, so a foreign authority cannot probe for
	// existence.
	_, neighborErr := server.handleSessionGet(ctx, sessionGetInput{RoomID: otherPartition})
	if neighborErr != nil {
		t.Fatalf("session_get refused a same-authority room: %v", neighborErr)
	}
	_, err = server.handleSessionGet(ctx, sessionGetInput{RoomID: foreignAuthority})
	assertToolErrorCode(t, err, errorCodeRoomNotFound)

	// Close is partition-enforcing. Own room: allowed.
	_, err = server.handleSessionClose(ctx, sessionCloseInput{RoomID: ownRoom})
	if err != nil {
		t.Fatalf("a caller could not close its own room: %v", err)
	}

	// Another partition inside the same authority: forbidden, and the message
	// names neither the required capability nor the owning scope.
	_, err = server.handleSessionClose(ctx, sessionCloseInput{RoomID: otherPartition})
	assertToolErrorCode(t, err, errorCodeRoomForbidden)
	if text := err.Error(); strings.Contains(text, "another-agent") || strings.Contains(text, "close") {
		t.Fatalf("the refusal described the authorization decision: %s", text)
	}
	if _, open := manager.Get(otherPartition); !open {
		t.Fatal("a refused close still tore the room down")
	}

	// Another authority: not-found, so existence does not leak.
	_, err = server.handleSessionClose(ctx, sessionCloseInput{RoomID: foreignAuthority})
	assertToolErrorCode(t, err, errorCodeRoomNotFound)
	if _, open := manager.Get(foreignAuthority); !open {
		t.Fatal("a cross-authority close still tore the room down")
	}
}

// TestWorkflowToolsCannotPushIntoAnotherCallersRoom covers the seventeen
// workflow tools and tangent.session_advance, which all funnel through
// advanceRoomEnvelope. Before this, any caller could reuse any room through
// env.Meta["roomID"] and receive that room's human response.
func TestWorkflowToolsCannotPushIntoAnotherCallersRoom(t *testing.T) {
	t.Parallel()
	server, _ := newAuthorizationFixture(t)
	ctx := context.Background()

	otherPartition := seedRoom(t, server, nil, roomflow.CallerFor("another-agent"))
	foreignAuthority := seedRoom(t, server, nil, gatewayCaller())

	request := triageEnvelope("env-cross-partition")
	_, err := server.advanceRoomEnvelope(ctx, otherPartition, request, nil, completionInput{})
	assertToolErrorCode(t, err, errorCodeRoomForbidden)

	_, err = server.advanceRoomEnvelope(
		ctx, foreignAuthority, triageEnvelope("env-cross-authority"), nil, completionInput{})
	assertToolErrorCode(t, err, errorCodeRoomNotFound)
}

// TestSessionCreateRecordsOwnershipImmediately: a room with no owner is a room
// every authorization check has to guess about, so ownership lands with the
// room rather than with its first advance.
func TestSessionCreateRecordsOwnershipImmediately(t *testing.T) {
	t.Parallel()
	server, _ := newAuthorizationFixture(t)
	ctx := context.Background()

	createdAny, err := server.handleSessionCreate(ctx, sessionCreateInput{Title: "Fresh"})
	if err != nil {
		t.Fatalf("handleSessionCreate: %v", err)
	}
	created := createdAny.(sessionCreateResult)
	owner, found, err := server.roomflow.OwnerScope(ctx, created.RoomID)
	if err != nil {
		t.Fatalf("OwnerScope: %v", err)
	}
	if !found {
		t.Fatal("a freshly created room has no recorded owner")
	}
	if owner != roomflow.DefaultCaller.Scope {
		t.Fatalf("owner scope = %q, want the host-derived caller scope %q",
			owner, roomflow.DefaultCaller.Scope)
	}
}

// TestLegacyScopeSpellingsResolveToTheSameCaller proves the read-time alias
// with no data rewrite: a room whose surface was written at the pre-grammar
// `direct-loopback:codex` is the same caller as one resolved today to
// `standalone-local:codex`.
func TestLegacyScopeSpellingsResolveToTheSameCaller(t *testing.T) {
	t.Parallel()
	server, manager := newAuthorizationFixture(t)
	ctx := context.Background()

	legacy := seedRoom(t, server, manager, interaction.ActorBinding{
		Scope: "direct-loopback:codex", PrincipalRef: "codex",
		Authority: "direct-loopback", Assurance: "asserted",
	})
	if err := server.authorizeRoom(ctx, legacy, roomflow.CallerFor("codex"),
		authz.Close, authz.PartitionScoped); err != nil {
		t.Fatalf("the canonical spelling could not close its own legacy room: %v", err)
	}
	// The stored row is untouched: the alias is a read, not a migration.
	var stored string
	if err := server.database.QueryRow(
		`SELECT owner_scope FROM surfaces WHERE id = ?`, legacy).Scan(&stored); err != nil {
		t.Fatalf("read stored owner scope: %v", err)
	}
	if stored != "direct-loopback:codex" {
		t.Fatalf("stored owner scope = %q, want the untouched legacy spelling", stored)
	}
}

// authorizationFixture is a durable MCP server plus the database behind it.
type authorizationFixture struct {
	*Server
	database *sql.DB
}

func newAuthorizationFixture(t *testing.T) (*authorizationFixture, *room.Manager) {
	t.Helper()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "authorization.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrateErr := tangentdb.RunMigrations(database); migrateErr != nil {
		t.Fatalf("run migrations: %v", migrateErr)
	}
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if registerErr := extensions.RegisterTriage(envelopeService); registerErr != nil {
		t.Fatalf("extensions.RegisterTriage: %v", registerErr)
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, HostVersion),
		interaction.WithAwaitPollInterval(time.Millisecond),
		interaction.WithSurfaceAccessPolicy(hitl.SurfaceAccessPolicy{}),
		interaction.WithDeliveryWorkerPolicy(roomflow.DeliveryWorkerPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	manager := room.NewManager(database)
	server, err := New(
		envelopeService, envelope.NewDispatcher(envelopeService), manager, "",
		WithInteractionService(interactions),
	)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	return &authorizationFixture{Server: server, database: database}, manager
}

// seedRoom creates a room owned by a named caller. Ownership is established
// through the same path a real caller takes, so the fixture cannot diverge
// from production ownership.
func seedRoom(
	t *testing.T,
	fixture *authorizationFixture,
	_ *room.Manager,
	owner interaction.ActorBinding,
) string {
	t.Helper()
	rm, err := fixture.manager.CreateWithError(nil)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if err := fixture.roomflow.EnsureRoomOwnership(context.Background(), rm.ID, owner); err != nil {
		t.Fatalf("record ownership for %s: %v", owner.Scope, err)
	}
	return rm.ID
}

// gatewayCaller is a caller a trusted in-process adapter established. Nothing
// in the shipped binary produces one, which is exactly why the cross-authority
// boundary needs a test that constructs it directly.
func gatewayCaller() interaction.ActorBinding {
	return interaction.ActorBinding{
		Scope: "gateway:tether-1", PrincipalRef: "upstream-principal",
		Authority: "gateway", Assurance: "adapter-verified",
	}
}

// triageEnvelope is the smallest valid caller envelope for a room push.
func triageEnvelope(id string) *envelopes.Envelope {
	return &envelopes.Envelope{
		V: envelopes.ProtocolVersion, ID: id, Type: "tangent.triage",
		Data: map[string]any{
			"items": []any{map[string]any{"id": "item-1", "title": "Decide"}},
			"actions": []any{
				map[string]any{"id": "accept", "label": "Accept"},
				map[string]any{"id": "reject", "label": "Reject"},
			},
		},
	}
}

// toolErrorCode extracts the envelope-style error code toolErrorResult builds
// (internal/mcp/tool_errors.go: {v, kind: "error", error: {code, message}}),
// by reading the *rawToolError's structured body directly rather than
// round-tripping it through JSON.
func toolErrorCode(t *testing.T, err error) string {
	t.Helper()
	var rte *rawToolError
	if !errors.As(err, &rte) {
		t.Fatalf("error is not a tool error: %v", err)
	}
	body, ok := rte.body.(map[string]any)
	if !ok {
		t.Fatalf("tool error body is not a map: %#v", rte.body)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("tool error body has no error object: %#v", body)
	}
	code, _ := errObj["code"].(string)
	return code
}

func assertToolErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a %s tool error, got a success", want)
	}
	if got := toolErrorCode(t, err); got != want {
		t.Fatalf("tool error code = %q, want %q (err: %v)", got, want, err)
	}
}
