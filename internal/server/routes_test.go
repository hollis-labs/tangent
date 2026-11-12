package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tangent/internal/access"
	"github.com/hollis-labs/tangent/internal/channelpane"
	"github.com/hollis-labs/tangent/internal/effect"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/server"
	"github.com/hollis-labs/tangent/internal/testdb"
)

// The route table is the only place a route's authority is written, so these
// tests read it back rather than a list kept here, which would drift the way
// the one that let PR #37 ship a route no participant could pass did.

func buildTable(t *testing.T) *server.Table {
	t.Helper()
	ctx := context.Background()

	envelopeService, err := envelope.New(ctx)
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	database := testdb.Open(t, filepath.Join(t.TempDir(), "routes.db"))
	effectStore, err := effect.NewSQLStore(database)
	if err != nil {
		t.Fatalf("effect.NewSQLStore: %v", err)
	}
	broker, err := effect.NewBroker(effectStore, effect.Standalone())
	if err != nil {
		t.Fatalf("effect.NewBroker: %v", err)
	}
	table, err := server.New(server.Config{
		Envelope:      envelopeService,
		Participants:  participant.NewGate(nil),
		HITL:          noopHITLService{},
		Inbox:         noopInboxService{},
		Rooms:         noopRoomService{},
		Channels:      noopChannelService{},
		Effects:       broker,
		EffectContext: noopEffectContextResolver{},
		// Calling any method on these panics: a request that reaches one of them
		// has got past the gate, which is the thing these tests deny.
		Turns:     struct{ server.TurnsService }{},
		Docs:      struct{ server.DocsService }{},
		Artifacts: struct{ server.ArtifactsService }{},
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return table
}

// TestEveryRouteHasAScope: a route with no scope is a route the host would
// leave open or refuse unpredictably. The table refuses to be built with one
// (TestARouteWithoutAScopeCannotBeRegistered); this reads every route back,
// maps it the way the host maps it onto auth.ScopeTable, and asks the table
// the question the guard asks.
func TestEveryRouteHasAScope(t *testing.T) {
	table := buildTable(t)
	routes := table.Routes()
	if len(routes) < 40 {
		t.Fatalf("the table has %d routes; this check would pass over too little to mean anything", len(routes))
	}
	scopes := libauth.NewScopeTable()
	for _, route := range routes {
		if !strings.HasPrefix(route.Scope, "tangent.") {
			t.Errorf("route %q has scope %q; want a tangent.* scope", route.Pattern, route.Scope)
		}
		scopes.Route(route.Method(), route.ScopePattern(), route.Scope)
	}
	for _, route := range routes {
		path := strings.NewReplacer("{", "x", "}", "", "...", "").Replace(route.Path())
		got, known := scopes.ForRoute(route.Method(), path)
		if !known || got != route.Scope {
			t.Errorf("route %q: the scope table answers %q (known %t), want %q", route.Pattern, got, known, route.Scope)
		}
	}
	// And a path nobody registered is unmapped, so the guard refuses it.
	if _, known := scopes.ForRoute(http.MethodGet, "/api/never-registered"); known {
		t.Error("an unregistered path is mapped")
	}
}

// TestEveryResolveRouteIsUserOnly: tangent.resolve is a person's decision, so a
// route that needs it must demand a user session, and no other route may carry
// the flag without needing it.
func TestEveryResolveRouteIsUserOnly(t *testing.T) {
	var resolve int
	for _, route := range buildTable(t).Routes() {
		if (route.Scope == access.Resolve) != route.UserOnly {
			t.Errorf("route %q: scope %q, user-only %t", route.Pattern, route.Scope, route.UserOnly)
		}
		if route.Scope == access.Resolve {
			resolve++
		}
	}
	if resolve < 10 {
		t.Fatalf("only %d resolve routes found; the table changed shape or the check lost its target", resolve)
	}
}

// agent credentials, every kind the system issues, each carrying the most the
// scope table could grant it.
func agentCredentials() map[string]libauth.Principal {
	future := time.Now().Add(time.Hour)
	return map[string]libauth.Principal{
		"api key holding tangent.resolve": {
			Verified: true, ID: "key:k_1", Kind: "service", Display: "claude-code",
			Scopes: []string{access.Resolve, access.View, access.Draft}, TokenID: "k_1", ExpiresAt: future,
		},
		"api key holding every scope": {
			Verified: true, ID: "key:k_2", Kind: "service", Display: "team-agent",
			Scopes: []string{"*"}, TokenID: "k_2", ExpiresAt: future,
		},
		"api key holding tangent.*": {
			Verified: true, ID: "key:k_3", Kind: "service", Display: "messaging",
			Scopes: []string{"tangent.*"}, TokenID: "k_3", ExpiresAt: future,
		},
		"plugin key": {
			Verified: true, ID: "key:k_4", Kind: "service", Display: "tangent-plugin-tesseract",
			Scopes: []string{access.Resolve, access.Draft}, TokenID: "k_4", ExpiresAt: future,
		},
		"gateway key": {
			Verified: true, ID: "key:k_5", Kind: "service", Display: "tether-gateway",
			Scopes: []string{access.Resolve, access.Submit}, TokenID: "k_5", ExpiresAt: future,
		},
		"token exchanged from the owner's session": {
			Verified: true, ID: "user:usr_owner", Kind: "user", Scopes: []string{"*"}, ExpiresAt: future,
		},
		"agent principal": {
			Verified: true, ID: "agent:a_1", Kind: "agent", Scopes: []string{"*"}, ExpiresAt: future,
		},
		"unverified principal": {
			Verified: false, ID: "user:usr_owner", Kind: "user", SessionID: "ses_x", Scopes: []string{"*"},
		},
		"user session without the scope": {
			Verified: true, ID: "user:usr_member", Kind: "user", SessionID: "ses_m",
			Scopes: []string{access.View, access.Draft}, ExpiresAt: future,
		},
	}
}

func request(h http.Handler, method, path string, p *libauth.Principal) int {
	r := httptest.NewRequest(method, path, strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	if p != nil {
		r = r.WithContext(libauth.WithPrincipal(r.Context(), *p))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

// TestNoAgentCredentialMayResolve: every credential an agent can hold is
// refused on every resolve route, before any handler or service is reached
// (the stubs panic if one is). The same request from the owner's user session
// gets past the gate.
func TestNoAgentCredentialMayResolve(t *testing.T) {
	table := buildTable(t)
	handler := table.Handler()
	var routes []server.Route
	for _, route := range table.Routes() {
		if route.Scope == access.Resolve {
			routes = append(routes, route)
		}
	}
	for name, principal := range agentCredentials() {
		for _, route := range routes {
			path := strings.NewReplacer("{itemID}", "i1", "{pluginID}", "p1", "{roomID}", "r1", "{channelID}", "c1").Replace(route.Path())
			if code := request(handler, route.Method(), path, &principal); code != http.StatusForbidden {
				t.Errorf("%s on %s: status %d, want 403", name, route.Pattern, code)
			}
		}
	}
	for _, route := range routes {
		path := strings.NewReplacer("{itemID}", "i1", "{pluginID}", "p1").Replace(route.Path())
		if code := request(handler, route.Method(), path, nil); code != http.StatusForbidden {
			t.Errorf("unauthenticated loopback on %s: status %d, want 403", route.Pattern, code)
		}
	}
}

// TestTheOwnerSessionGetsPastTheResolveGate is the control for the test above:
// without it, a gate that refused everyone would pass.
func TestTheOwnerSessionGetsPastTheResolveGate(t *testing.T) {
	handler := buildTable(t).Handler()
	owner := libauth.Principal{
		Verified: true, ID: "user:usr_owner", Kind: "user", SessionID: "ses_owner",
		Scopes: []string{"*"}, ExpiresAt: time.Now().Add(time.Hour),
	}
	if code := request(handler, http.MethodPost, "/api/hitl/items/i1/resolve", &owner); code == http.StatusForbidden || code == http.StatusUnauthorized {
		t.Fatalf("the owner's session was refused on resolve with %d", code)
	}
	member := libauth.Principal{
		Verified: true, ID: "user:usr_member", Kind: "user", SessionID: "ses_member",
		Scopes: []string{access.View, access.Draft}, ExpiresAt: time.Now().Add(time.Hour),
	}
	if code := request(handler, http.MethodPost, "/api/hitl/items/i1/resolve", &member); code != http.StatusForbidden {
		t.Fatalf("a user session without tangent.resolve got %d on resolve, want 403", code)
	}
	if code := request(handler, http.MethodGet, "/api/hitl", &member); code == http.StatusForbidden {
		t.Fatalf("a user session with tangent.view was refused on the HITL inbox")
	}
}

// TestAnAgentKeyIsNotAParticipant: the browser routes serve the signed-in user.
// An API key with tangent.view reads through MCP, not through them.
func TestAnAgentKeyIsNotAParticipant(t *testing.T) {
	handler := buildTable(t).Handler()
	key := agentCredentials()["api key holding every scope"]
	for _, path := range []string{"/api/hitl", "/api/inbox", "/api/rooms", "/api/channels", "/api/docs", "/api/artifacts", "/api/turns"} {
		if code := request(handler, http.MethodGet, path, &key); code != http.StatusForbidden {
			t.Errorf("an API key got %d on GET %s, want 403", code, path)
		}
	}
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
