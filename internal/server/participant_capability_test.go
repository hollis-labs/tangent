package server_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/channelpane"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/effect"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/server"
)

// TestEveryParticipantGuardedRouteUsesACapabilityParticipantsHold is
// CW-20260907-0084: PR #37 shipped POST /api/channels/{id}/messages gated on
// authz.Submit, a capability ADR 0004 §7's KindParticipant row never holds,
// so no browser participant session could ever have sent a message through
// it. Every test for that route used a fake ChannelService with no
// Participants gate configured, so nothing could have caught it — the route
// looked correct and the tests passed.
//
// This reads the routes back from server.Server.ParticipantRoutes(), which
// registerParticipantRoute populates at the exact point every
// participant-guarded route is wired up in internal/server/server.go — not
// from a list maintained here, which would be the same failure one level up.
// It needs no participant session and makes no HTTP request: a route's
// capability is fixed at registration, before any request ever arrives.
func TestEveryParticipantGuardedRouteUsesACapabilityParticipantsHold(t *testing.T) {
	srv := buildServerForRouteCapabilityCheck(t)

	routes := srv.ParticipantRoutes()
	if len(routes) == 0 {
		t.Fatal("ParticipantRoutes() returned no routes; this check would silently pass without " +
			"testing anything — confirm HITL, Rooms, Channels, and Effects are all configured above")
	}
	for _, route := range routes {
		if !authz.Grants(authz.KindParticipant, route.Capability) {
			t.Errorf("route %q is guarded on capability %q, which authz.KindParticipant's row "+
				"(ADR 0004 §7: view, draft, resolve, cancel) never holds — no browser participant "+
				"session could ever pass this guard", route.Pattern, route.Capability)
		}
	}
}

// buildServerForRouteCapabilityCheck constructs a real *server.Server with
// every participant-guarded section (HITL, Rooms, Channels, Effects)
// configured, so New's own route registration runs in full. The services
// behind them are minimal fakes — no method on any of them is ever called,
// since this test never issues an HTTP request — except Effects, a concrete
// *effect.Broker the config field requires, built the same way
// internal/server/effects_test.go builds one for a real route.
func buildServerForRouteCapabilityCheck(t *testing.T) *server.Server {
	t.Helper()
	ctx := context.Background()

	envelopeService, err := envelope.New(ctx)
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}

	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "route-capabilities.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrateErr := tangentdb.RunMigrations(database); migrateErr != nil {
		t.Fatalf("run migrations: %v", migrateErr)
	}
	effectStore, err := effect.NewSQLStore(database)
	if err != nil {
		t.Fatalf("effect.NewSQLStore: %v", err)
	}
	broker, err := effect.NewBroker(effectStore, effect.Standalone())
	if err != nil {
		t.Fatalf("effect.NewBroker: %v", err)
	}

	srv, err := server.New(server.Config{
		Envelope:      envelopeService,
		HITL:          noopHITLService{},
		Inbox:         noopInboxService{},
		Rooms:         noopRoomService{},
		Channels:      noopChannelService{},
		Effects:       broker,
		EffectContext: noopEffectContextResolver{},
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return srv
}

type noopHITLService struct{}

func (noopHITLService) Inbox(context.Context) (hitl.OperatorInbox, error) {
	return hitl.OperatorInbox{}, nil
}

func (noopHITLService) InspectOperatorItem(context.Context, string) (hitl.OperatorItemView, error) {
	return hitl.OperatorItemView{}, nil
}

func (noopHITLService) Present(context.Context, hitl.PresentInput) (hitl.ItemHandle, error) {
	return hitl.ItemHandle{}, nil
}

func (noopHITLService) Resolve(context.Context, hitl.ResolveInput) (hitl.TerminalOutcome, error) {
	return hitl.TerminalOutcome{}, nil
}

type noopRoomService struct{}

func (noopRoomService) ListBrowserRooms(context.Context, bool) ([]room.RoomSummary, error) {
	return nil, nil
}

func (noopRoomService) InspectBrowserRoom(context.Context, string) (any, error) {
	return nil, nil
}

func (noopRoomService) CloseBrowserRoom(context.Context, string, string) error {
	return nil
}

type noopChannelService struct{}

func (noopChannelService) ListChannels(context.Context) ([]channelpane.ChannelSummary, error) {
	return nil, nil
}

func (noopChannelService) GetChannel(context.Context, string) (channelpane.ChannelDetail, error) {
	return channelpane.ChannelDetail{}, nil
}

func (noopChannelService) SendMessage(
	context.Context, string, channelpane.SendInput,
) (channelpane.MessageView, error) {
	return channelpane.MessageView{}, nil
}

func (noopChannelService) MarkRead(context.Context, string) (int, error) {
	return 0, nil
}

func (noopChannelService) Revision(context.Context) (string, error) {
	return "", nil
}

type noopEffectContextResolver struct{}

func (noopEffectContextResolver) ResolveEffectContext(context.Context, string) (effect.Binding, string, error) {
	return effect.Binding{}, "", nil
}

type noopInboxService struct{}

func (noopInboxService) BrowserInbox(context.Context) ([]interaction.InboxEntry, error) {
	return []interaction.InboxEntry{}, nil
}
