package pluginhost

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"

	"github.com/hollis-labs/plugin-sdk/subprocess"

	"github.com/hollis-labs/tangent/internal/authz"
)

// This file is the third registration surface this host honors: an HTTP route
// served by a plugin (ADR 0007 §4, CW-20260910-0030).
//
// # What it is for
//
// A button in a room that does work without spending an agent turn. The board
// a plugin opened needs a sync it can trigger itself; asking a model to
// re-issue a mechanical call because the UI had no way to make it is the waste
// this exists to remove.
//
// # A plugin route is not a side door
//
// It is mounted by internal/server through the same registerParticipantRoute
// every other browser API route goes through, so it inherits the same-origin
// guard, the participant-session requirement and the ADR 0004 §7 capability
// check without opting in to any of them. Three properties are enforced right
// here, at registration, because a route that fails closed at boot is cheaper
// than one that fails open at request time:
//
//   - **The capability must be one a participant can hold.** ADR 0004 §7's
//     KindParticipant row is {view, draft, resolve, cancel} and
//     authz.Authorize consults that row unconditionally. A route gated on
//     `submit` would be unreachable by every real browser session — which is
//     exactly the bug CW-20260907-0084 was written about, shipped, and only
//     caught live. A plugin must not be able to reintroduce it.
//   - **The path must be under the reserved prefix.** Nothing a plugin
//     registers may collide with /api/hitl, /api/rooms, /api/channels,
//     /api/effects, /mcp, /sse, /ws or the SPA.
//   - **The method must be GET or POST.** A plugin route is a read or a
//     button. PUT/PATCH/DELETE describe a CRUD surface over host-held
//     resources, and RegisterCRUDHandler is refused for a reason that has not
//     changed: the owning application's agent is its client.
//
// Streaming is unsupported, per the SDK's own comment on HTTPHandler. The
// response is a status, headers and a body — there is no flush, no SSE, and
// this host does not invent one.

// RoutePrefix is the reserved mount point for every plugin-served route.
const RoutePrefix = "/api/plugins/"

// pluginRoutePath requires a plugin-owned segment under the prefix and a
// literal path beneath it. `{` is refused because http.ServeMux would read it
// as a wildcard, and a plugin that could register a pattern rather than a path
// could match traffic meant for another one.
var pluginRoutePath = regexp.MustCompile(`^/api/plugins/[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._/-]*$`)

// Errors this host returns from the HTTP surface.
var (
	// ErrInvalidRoute reports a structurally unusable route registration.
	ErrInvalidRoute = errors.New("pluginhost: invalid HTTP route registration")
	// ErrRouteClaimed reports a method+path another plugin already claimed.
	ErrRouteClaimed = errors.New("pluginhost: HTTP route already claimed by another plugin")
	// ErrUnholdableCapability reports a route gated on a capability ADR 0004
	// §7's participant row never holds — a route no browser could ever reach.
	ErrUnholdableCapability = errors.New("pluginhost: route capability is one a participant never holds")
)

// HTTPRoute is one plugin-served browser route.
type HTTPRoute struct {
	// Method is GET or POST.
	Method string
	// Path is the literal mount path. It must be under RoutePrefix and must
	// carry a plugin-owned segment: /api/plugins/<plugin>/<path>.
	Path string
	// Capability is the ADR 0004 §2 capability the route exercises, checked
	// against the participant's session before the handler runs. It must be
	// one KindParticipant can hold.
	Capability authz.Capability
	// Handler services requests. It is the SDK's own dispatch interface.
	Handler subprocess.HTTPHandler
}

// Pattern is the http.ServeMux pattern this route mounts at.
func (r HTTPRoute) Pattern() string { return r.Method + " " + r.Path }

// RegisterHTTPRoute records one plugin-served route.
//
// Like RegisterMCPTool it only records: plugins load before the HTTP server is
// constructed, and internal/server mounts what HTTPRoutes() reports.
func (h *Host) RegisterHTTPRoute(route HTTPRoute) error {
	switch route.Method {
	case http.MethodGet, http.MethodPost:
	case "":
		return fmt.Errorf("%w: route %q names no method", ErrInvalidRoute, route.Path)
	default:
		return fmt.Errorf(
			"%w: %s %s — a plugin route is a read or a button, so this host mounts GET and POST only; "+
				"a resource surface is what RegisterCRUDHandler refuses",
			ErrInvalidRoute, route.Method, route.Path)
	}
	if !pluginRoutePath.MatchString(route.Path) {
		return fmt.Errorf(
			"%w: %q is not a plugin route path; it must be %s<plugin>/<path>, all lower case, "+
				"with no `{` wildcard (a wildcard could match another plugin's traffic)",
			ErrInvalidRoute, route.Path, RoutePrefix)
	}
	if route.Capability == "" {
		return fmt.Errorf("%w: %s names no capability", ErrInvalidRoute, route.Pattern())
	}
	if !authz.Grants(authz.KindParticipant, route.Capability) {
		return fmt.Errorf(
			"%w: %s is gated on %q; ADR 0004 §7's participant row holds only %v, so no browser "+
				"session could ever pass this guard",
			ErrUnholdableCapability, route.Pattern(), route.Capability,
			authz.DefaultParticipantCapabilities())
	}
	if route.Handler == nil {
		return fmt.Errorf(
			"%w: %s supplies no subprocess.HTTPHandler, so nothing would service a request to it",
			ErrInvalidRoute, route.Pattern())
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if _, claimed := h.routes[route.Pattern()]; claimed {
		return fmt.Errorf("%w: %s", ErrRouteClaimed, route.Pattern())
	}
	// Bounded and panic-safe at registration, for the reason RegisterMCPTool
	// gives: a participant pressing a button must get an answer, and a handler
	// that never returns must not be what graceful shutdown is waiting on.
	route.Handler = guardedHTTPHandler{
		pattern: route.Pattern(), release: h.release, budget: h.budget, inner: route.Handler,
	}
	h.routes[route.Pattern()] = route
	h.logger.Info("pluginhost: http route contributed",
		"method", route.Method, "path", route.Path, "capability", route.Capability)
	return nil
}

// HTTPRoutes returns every plugin-contributed route, sorted by pattern, for
// the same reason MCPTools sorts: the mounted surface should not depend on
// load order.
func (h *Host) HTTPRoutes() []HTTPRoute {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]HTTPRoute, 0, len(h.routes))
	for _, route := range h.routes {
		out = append(out, route)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pattern() < out[j].Pattern() })
	return out
}
