package plugin

import (
	"encoding/json"

	sdkmanifest "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
)

// ToolNamespace is the prefix every tool this build serves carries. It is not
// decoration: the documentation gate matches tool mentions by this spelling,
// so a name outside it is a tool no document can be checked against.
const ToolNamespace = "tangent."

// MCPTool is one agent-callable tool a plugin contributes.
//
// InputSchema is raw JSON rather than a parsed schema type on purpose: it is
// what a plugin puts on the wire and in its plugin.yaml (ToolDecl), so the
// declaration and the registration cannot drift apart.
type MCPTool struct {
	// Name is the wire name agents call. It must be `tangent.<name>`.
	Name string
	// Description is what an agent reads when choosing a tool.
	Description string
	// InputSchema is the tool's JSON Schema. It must parse and it must be of
	// type "object" — MCP requires that of every tool, and the host checks it
	// here so a malformed schema fails the boot rather than the first call.
	InputSchema json.RawMessage
	// Effect is the reviewed host effect, never an authorization grant.
	Effect      string
	Annotations *sdkmanifest.ToolAnnotations
	// Handler services invocations. It is the SDK's own dispatch interface,
	// unchanged, so a plugin written against it needs no Tangent-specific
	// handler type.
	Handler subprocess.MCPHandler
}

// Capability is an ADR 0004 §2 object-access capability. internal/authz
// aliases this type and owns the full set and the rules; a plugin names one on
// an HTTPRoute or a RouteDecl.
type Capability string

// The capabilities a plugin-served browser route may be gated on. A route must
// name one a participant session can hold — the host refuses the rest at
// registration, because a route gated on anything else is unreachable from a
// browser.
const (
	// CapabilityView reads a surface or interaction projection.
	CapabilityView Capability = "view"
	// CapabilityDraft writes a draft revision: view state, never a decision.
	CapabilityDraft Capability = "draft"
)

// RoutePrefix is the reserved mount point for every plugin-served route.
const RoutePrefix = "/api/plugins/"

// HTTPRoute is one plugin-served browser route.
type HTTPRoute struct {
	// Method is GET or POST.
	Method string
	// Path is the literal mount path. It must be under RoutePrefix and must
	// carry a plugin-owned segment: /api/plugins/<plugin>/<path>.
	Path string
	// Capability is the ADR 0004 §2 capability the route exercises, checked
	// against the participant's session before the handler runs. It must be
	// one a participant session can hold; the host refuses the rest at
	// registration.
	Capability Capability
	// Handler services requests. It is the SDK's own dispatch interface.
	Handler subprocess.HTTPHandler
}

// Pattern is the http.ServeMux pattern this route mounts at.
func (r HTTPRoute) Pattern() string { return r.Method + " " + r.Path }
