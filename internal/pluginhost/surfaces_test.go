package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"

	"github.com/hollis-labs/tangent/internal/authz"
)

// These tests hold the two registration surfaces CW-20260910-0029 and
// CW-20260910-0030 added. Each refusal below is a sentence in mcp.go or
// http.go: a boundary stated in a comment and nowhere else is one the next
// change can cross without noticing.

// stubHandler services both SDK dispatch interfaces. Nothing here calls it —
// these tests are about what the host accepts, not about what a plugin does.
type stubHandler struct {
	call  func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error)
	serve func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error)
}

func (h *stubHandler) MCPCallTool(
	ctx context.Context, req subprocess.MCPCallRequest,
) (subprocess.MCPCallResult, error) {
	if h.call == nil {
		return subprocess.MCPCallResult{Content: json.RawMessage(`{}`)}, nil
	}
	return h.call(ctx, req)
}

func (h *stubHandler) HTTPHandle(
	ctx context.Context, req subprocess.HTTPRequest,
) (subprocess.HTTPResponse, error) {
	if h.serve == nil {
		return subprocess.HTTPResponse{Status: http.StatusOK}, nil
	}
	return h.serve(ctx, req)
}

func objectSchema() json.RawMessage { return json.RawMessage(`{"type":"object","properties":{}}`) }

func validTool() MCPTool {
	return MCPTool{
		Name:        "tangent.stub_tool",
		Description: "a stub",
		InputSchema: objectSchema(),
		Handler:     &stubHandler{},
	}
}

func validRoute() HTTPRoute {
	return HTTPRoute{
		Method:     http.MethodPost,
		Path:       "/api/plugins/stub/sync",
		Capability: authz.Draft,
		Handler:    &stubHandler{},
	}
}

// TestValidToolAndRouteAreRecorded is the baseline the refusals are measured
// against: the surfaces work, and what a plugin registered is what the host
// reports back for installation.
func TestValidToolAndRouteAreRecorded(t *testing.T) {
	host, _ := newHost(t)
	if err := host.RegisterMCPTool(validTool()); err != nil {
		t.Fatalf("RegisterMCPTool: %v", err)
	}
	if err := host.RegisterHTTPRoute(validRoute()); err != nil {
		t.Fatalf("RegisterHTTPRoute: %v", err)
	}
	tools := host.MCPTools()
	if len(tools) != 1 || tools[0].Name != "tangent.stub_tool" {
		t.Fatalf("MCPTools() = %+v, want the one registered tool", tools)
	}
	routes := host.HTTPRoutes()
	if len(routes) != 1 || routes[0].Pattern() != "POST /api/plugins/stub/sync" {
		t.Fatalf("HTTPRoutes() = %+v, want the one registered route", routes)
	}
}

// TestToolRegistrationRefusals covers every way mcp.go says no. They are one
// table because they are one posture: refuse by name, never accommodate.
func TestToolRegistrationRefusals(t *testing.T) {
	tests := []struct {
		name string
		tool func(MCPTool) MCPTool
		want error
	}{
		{
			name: "no name",
			tool: func(tool MCPTool) MCPTool { tool.Name = ""; return tool },
			want: ErrInvalidTool,
		},
		{
			// The namespace is not decoration: internal/smoke's documentation
			// gate finds tools by exactly this spelling, so a name outside it
			// would be a shipped tool no document could be checked against.
			name: "outside the tangent namespace",
			tool: func(tool MCPTool) MCPTool { tool.Name = "torque.board"; return tool },
			want: ErrInvalidTool,
		},
		{
			name: "upper case is not a documentable name",
			tool: func(tool MCPTool) MCPTool { tool.Name = "tangent.Board"; return tool },
			want: ErrInvalidTool,
		},
		{
			name: "no handler",
			tool: func(tool MCPTool) MCPTool { tool.Handler = nil; return tool },
			want: ErrInvalidTool,
		},
		{
			name: "no schema",
			tool: func(tool MCPTool) MCPTool { tool.InputSchema = nil; return tool },
			want: ErrInvalidTool,
		},
		{
			name: "schema is not JSON",
			tool: func(tool MCPTool) MCPTool { tool.InputSchema = json.RawMessage(`{`); return tool },
			want: ErrInvalidTool,
		},
		{
			name: "schema is not an object",
			tool: func(tool MCPTool) MCPTool {
				tool.InputSchema = json.RawMessage(`{"type":"array"}`)
				return tool
			},
			want: ErrInvalidTool,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host, _ := newHost(t)
			err := host.RegisterMCPTool(test.tool(validTool()))
			if !errors.Is(err, test.want) {
				t.Fatalf("RegisterMCPTool = %v, want %v", err, test.want)
			}
			if got := host.MCPTools(); len(got) != 0 {
				t.Fatalf("a refused tool was recorded anyway: %+v", got)
			}
		})
	}
}

// TestTwoPluginsCannotClaimOneToolName is the collision half of the rule. The
// MCP SDK's registry is keyed by name and a second registration silently
// replaces the first, so the refusal has to happen before it gets there.
func TestTwoPluginsCannotClaimOneToolName(t *testing.T) {
	host, _ := newHost(t)
	if err := host.RegisterMCPTool(validTool()); err != nil {
		t.Fatalf("first RegisterMCPTool: %v", err)
	}
	second := validTool()
	second.Description = "a different plugin, the same name"
	err := host.RegisterMCPTool(second)
	if !errors.Is(err, ErrToolNameClaimed) {
		t.Fatalf("second RegisterMCPTool = %v, want ErrToolNameClaimed", err)
	}
	if tools := host.MCPTools(); len(tools) != 1 || tools[0].Description != "a stub" {
		t.Fatalf("MCPTools() = %+v, want the first registration intact", tools)
	}
}

// TestRouteRegistrationRefusals covers every way http.go says no.
func TestRouteRegistrationRefusals(t *testing.T) {
	tests := []struct {
		name  string
		route func(HTTPRoute) HTTPRoute
		want  error
	}{
		{
			name:  "no method",
			route: func(route HTTPRoute) HTTPRoute { route.Method = ""; return route },
			want:  ErrInvalidRoute,
		},
		{
			// PUT/PATCH/DELETE describe a resource surface over host-held
			// records, and RegisterCRUDHandler already refuses that for a
			// reason this must not route around.
			name:  "a resource method is not a plugin route",
			route: func(route HTTPRoute) HTTPRoute { route.Method = http.MethodDelete; return route },
			want:  ErrInvalidRoute,
		},
		{
			name:  "outside the reserved prefix",
			route: func(route HTTPRoute) HTTPRoute { route.Path = "/api/rooms/steal"; return route },
			want:  ErrInvalidRoute,
		},
		{
			name:  "no plugin-owned segment",
			route: func(route HTTPRoute) HTTPRoute { route.Path = "/api/plugins/sync"; return route },
			want:  ErrInvalidRoute,
		},
		{
			// A ServeMux wildcard could match traffic meant for another
			// plugin's route.
			name:  "a wildcard is not a path",
			route: func(route HTTPRoute) HTTPRoute { route.Path = "/api/plugins/{any}/sync"; return route },
			want:  ErrInvalidRoute,
		},
		{
			name:  "no capability",
			route: func(route HTTPRoute) HTTPRoute { route.Capability = ""; return route },
			want:  ErrInvalidRoute,
		},
		{
			name:  "no handler",
			route: func(route HTTPRoute) HTTPRoute { route.Handler = nil; return route },
			want:  ErrInvalidRoute,
		},
		{
			// CW-20260907-0084, one layer up: a route gated on a capability the
			// participant row never holds is a route no browser session can
			// ever pass, and it looks completely correct until someone presses
			// the button.
			name:  "a capability no participant holds",
			route: func(route HTTPRoute) HTTPRoute { route.Capability = authz.Submit; return route },
			want:  ErrUnholdableCapability,
		},
		{
			name:  "close is a caller power, not a participant's",
			route: func(route HTTPRoute) HTTPRoute { route.Capability = authz.Close; return route },
			want:  ErrUnholdableCapability,
		},
		{
			name:  "administer is nothing's",
			route: func(route HTTPRoute) HTTPRoute { route.Capability = authz.Administer; return route },
			want:  ErrUnholdableCapability,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host, _ := newHost(t)
			err := host.RegisterHTTPRoute(test.route(validRoute()))
			if !errors.Is(err, test.want) {
				t.Fatalf("RegisterHTTPRoute = %v, want %v", err, test.want)
			}
			if got := host.HTTPRoutes(); len(got) != 0 {
				t.Fatalf("a refused route was recorded anyway: %+v", got)
			}
		})
	}
}

// TestEveryCapabilityAParticipantHoldsIsAcceptedOnARoute is the other side of
// the capability check: the refusal must be exactly the participant row, not a
// narrower list someone wrote down here and forgot to update when ADR 0004
// changes.
func TestEveryCapabilityAParticipantHoldsIsAcceptedOnARoute(t *testing.T) {
	for _, capability := range authz.DefaultParticipantCapabilities() {
		host, _ := newHost(t)
		route := validRoute()
		route.Capability = capability
		if err := host.RegisterHTTPRoute(route); err != nil {
			t.Errorf("RegisterHTTPRoute(%s) = %v, want accepted", capability, err)
		}
	}
}

// TestTwoPluginsCannotClaimOneRoute mirrors the tool collision. http.ServeMux
// panics on a duplicate pattern, so this refusal is also what keeps a second
// plugin from taking the process down at boot.
func TestTwoPluginsCannotClaimOneRoute(t *testing.T) {
	host, _ := newHost(t)
	if err := host.RegisterHTTPRoute(validRoute()); err != nil {
		t.Fatalf("first RegisterHTTPRoute: %v", err)
	}
	if err := host.RegisterHTTPRoute(validRoute()); !errors.Is(err, ErrRouteClaimed) {
		t.Fatalf("second RegisterHTTPRoute = %v, want ErrRouteClaimed", err)
	}
	if routes := host.HTTPRoutes(); len(routes) != 1 {
		t.Fatalf("HTTPRoutes() = %+v, want one", routes)
	}
}

// TestTheSamePathMayCarryBothMethods: a route is a method and a path, so a
// plugin can serve a GET read and a POST action at one address. This is the
// one collision that is not one.
func TestTheSamePathMayCarryBothMethods(t *testing.T) {
	host, _ := newHost(t)
	get := validRoute()
	get.Method = http.MethodGet
	get.Capability = authz.View
	if err := host.RegisterHTTPRoute(get); err != nil {
		t.Fatalf("RegisterHTTPRoute(GET): %v", err)
	}
	if err := host.RegisterHTTPRoute(validRoute()); err != nil {
		t.Fatalf("RegisterHTTPRoute(POST): %v", err)
	}
	if routes := host.HTTPRoutes(); len(routes) != 2 {
		t.Fatalf("HTTPRoutes() = %+v, want both methods", routes)
	}
}

// TestContributedSurfacesAreSorted pins the ordering both accessors promise.
// Two builds with the same plugins should produce the same surface whatever
// order the plugins loaded in, or a diff of tools/list means nothing.
func TestContributedSurfacesAreSorted(t *testing.T) {
	host, _ := newHost(t)
	for _, name := range []string{"tangent.zebra", "tangent.alpha", "tangent.middle"} {
		tool := validTool()
		tool.Name = name
		if err := host.RegisterMCPTool(tool); err != nil {
			t.Fatalf("RegisterMCPTool(%s): %v", name, err)
		}
	}
	for _, path := range []string{"/api/plugins/z/sync", "/api/plugins/a/sync"} {
		route := validRoute()
		route.Path = path
		if err := host.RegisterHTTPRoute(route); err != nil {
			t.Fatalf("RegisterHTTPRoute(%s): %v", path, err)
		}
	}
	tools := host.MCPTools()
	if len(tools) != 3 || tools[0].Name != "tangent.alpha" || tools[2].Name != "tangent.zebra" {
		t.Fatalf("MCPTools() is not sorted: %+v", tools)
	}
	routes := host.HTTPRoutes()
	if len(routes) != 2 || routes[0].Path != "/api/plugins/a/sync" {
		t.Fatalf("HTTPRoutes() is not sorted: %+v", routes)
	}
}
