package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/access"
	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/participant"
)

func newRouter() *router {
	return &router{cfg: Config{Participants: participant.NewGate(nil)}, seen: map[string]bool{}}
}

func noop(http.ResponseWriter, *http.Request) {}

// TestARouteWithoutAScopeCannotBeRegistered: the table is the only way a route
// exists, and it refuses one with no scope, so no route can reach the host
// unmapped.
func TestARouteWithoutAScopeCannotBeRegistered(t *testing.T) {
	rt := newRouter()
	rt.handle("GET /api/x", "", false, http.HandlerFunc(noop))
	if rt.err == nil || !strings.Contains(rt.err.Error(), "no scope") {
		t.Fatalf("a route with an empty scope was accepted: %v", rt.err)
	}

	rt = newRouter()
	rt.participant("GET /api/x", authz.Capability("invented"), noop)
	if rt.err == nil {
		t.Fatal("a capability with no scope mapping was accepted")
	}
}

func TestACallerRouteCannotNeedResolve(t *testing.T) {
	rt := newRouter()
	rt.caller("POST /api/x", access.Resolve, noop)
	if rt.err == nil {
		t.Fatal("a route an API key can use was registered with the decision scope")
	}
}

func TestARouteIsRegisteredOnce(t *testing.T) {
	rt := newRouter()
	rt.participant("GET /api/x", authz.View, noop)
	rt.participant("GET /api/x", authz.View, noop)
	if rt.err == nil {
		t.Fatal("the same pattern was registered twice")
	}
}
