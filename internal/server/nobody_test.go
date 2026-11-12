package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/access"
	"github.com/hollis-labs/tangent/internal/server"
)

// The owner routes below take no request body. An envelope can aim the owner's POST at any same-origin route
// and the browser attaches the envelope's JSON, so each refuses a non-empty body. Sign-out is the same rule,
// applied where the identity module is mounted (cmd/tangent, TestRefuseLogoutBody).
var bodylessRoutes = []string{
	"POST /api/inbox/items/{interactionID}/archive",
	"POST /api/plugin-management/{pluginID}/enable",
	"POST /api/plugin-management/{pluginID}/disable",
	"POST /api/plugin-management/{pluginID}/reload",
	"POST /api/channels/{channelID}/read",
}

// Every other non-GET route that needs tangent.resolve reads a body that its handler decodes strictly (an
// unknown field is refused), so a foreign body fails there. A new resolve route must be put in one list or the
// other on purpose.
var strictBodyResolveRoutes = []string{
	"POST /api/hitl/items/{itemID}/present",
	"POST /api/hitl/items/{itemID}/resolve",
	"POST /api/turns/items/{itemID}/reply",
	"POST /api/turns/items/{itemID}/dismiss",
	"POST /api/docs/items/{itemID}/acknowledge",
	"POST /api/docs/items/{itemID}/archive",
	"POST /api/artifacts/items/{itemID}/messages",
	"POST /api/artifacts/items/{itemID}/signoff",
	"POST /api/artifacts/items/{itemID}/archive",
	"POST /api/plugin-management/{pluginID}/config/apply",
	"POST /api/plugin-management/{pluginID}/config/reset",
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestBodylessOwnerRoutesAreMarkedAndGated(t *testing.T) {
	var flagged int
	for _, route := range buildTable(t).Routes() {
		if route.NoBody != contains(bodylessRoutes, route.Pattern) {
			t.Errorf("route %q: NoBody is %t, the list here says %t", route.Pattern, route.NoBody, contains(bodylessRoutes, route.Pattern))
		}
		if !route.NoBody {
			continue
		}
		flagged++
		// Without a session the gate answers first, so a refused caller learns nothing from the body rule.
		request := httptest.NewRequest(route.Method(), "/x", strings.NewReader(`{"room_id":"r","envelope_id":"e"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		route.Handler.ServeHTTP(response, request)
		if response.Code == http.StatusBadRequest {
			t.Errorf("route %q answers 400 to a caller with no session; the body rule must run after the gate", route.Pattern)
		}
	}
	if flagged < 1 {
		t.Fatal("no route is marked NoBody; the check lost its target")
	}
}

func TestEveryResolvePostRouteIsClassified(t *testing.T) {
	for _, route := range buildTable(t).Routes() {
		if route.Scope != access.Resolve || route.Method() != http.MethodPost {
			continue
		}
		if !contains(bodylessRoutes, route.Pattern) && !contains(strictBodyResolveRoutes, route.Pattern) {
			t.Errorf("resolve route %q is in neither list: say whether it takes a body (strict decode) or none (NoBody + RefuseBody)", route.Pattern)
		}
	}
}

func TestRefuseBody(t *testing.T) {
	handler := server.RefuseBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	declared := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":1}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, declared)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "takes no request body") {
		t.Fatalf("a declared body = %d %s", response.Code, response.Body.String())
	}
	chunked := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":1}`))
	chunked.ContentLength = -1
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, chunked)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("a chunked body = %d", response.Code)
	}
	TestRefuseBodyDoesNotConsumeAnEmptyRequest(t)
}

func TestRefuseBodyDoesNotConsumeAnEmptyRequest(t *testing.T) {
	reached := false
	handler := server.RefuseBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	request := httptest.NewRequest(http.MethodPost, "/x", nil)
	request.ContentLength = -1
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !reached || response.Code != http.StatusOK {
		t.Fatalf("an empty request was refused: reached=%t code=%d", reached, response.Code)
	}
}
