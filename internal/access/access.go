// Package access is where Tangent's authority is spelled: the scope each
// capability needs, and who may hold the one that decides.
//
// Authority comes from the Substrate identity module and nowhere else. A
// request arrives with a verified principal (a signed-in user session, or an
// API key that is one agent class's credential); the auth guard has already
// checked that it holds the scope the route or tool is registered with. This
// package adds the one rule a scope cannot express: a decision (tangent.resolve)
// is made by a person at a user session, never by a credential an agent holds.
package access

import (
	"context"
	"errors"
	"slices"
	"strings"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tangent/internal/authz"
)

// The scopes. Each is "tangent.<verb>"; tangent.admin is the module's own
// admin scope and stays the identity module's.
const (
	View     = "tangent.view"
	Draft    = "tangent.draft"
	Submit   = "tangent.submit"
	Cancel   = "tangent.cancel"
	Close    = "tangent.close"
	Admin    = "tangent.admin"
	Resolve  = "tangent.resolve"
	Withdraw = "tangent.withdraw"
	// Deliver is what a turn consumer holds to acknowledge that it received a
	// turn. It is not a decision and carries no authority over a person's
	// answer; it exists so that acknowledging delivery never needs Resolve.
	Deliver = "tangent.deliver"
	// Plugin is what a caller holds to call the tools plugins contribute. A
	// plugin tool is scoped when it registers and unscoped when it is
	// withdrawn, so a plugin cannot be called through a scope it did not get.
	Plugin = "tangent.plugin"
)

// ForCapability is the scope a capability needs.
func ForCapability(capability authz.Capability) string {
	switch capability {
	case authz.View:
		return View
	case authz.Draft:
		return Draft
	case authz.Submit:
		return Submit
	case authz.Cancel:
		return Cancel
	case authz.Close:
		return Close
	case authz.Administer:
		return Admin
	case authz.Resolve:
		return Resolve
	}
	return ""
}

// IsUserSession reports whether p is a signed-in user's session: the kind the
// identity module gives a browser cookie session. An API key is a service
// principal; a short-lived token exchanged from a session has no session id.
// Neither counts, whatever scopes they carry.
func IsUserSession(p libauth.Principal) bool {
	return p.Verified && p.Kind == "user" && p.SessionID != "" && strings.HasPrefix(p.ID, "user:")
}

// MayResolve is the one test for deciding. It needs a user session and the
// scope; a wildcard grant ("*" or "tangent.*") satisfies the scope for any
// principal, which is why the session half is checked here and not left to the
// scope table.
func MayResolve(p libauth.Principal) bool {
	return IsUserSession(p) && p.HasScope(Resolve)
}

// PluginHostPrincipal is who the plugin host's in-process tool caller is: a
// verified service with no name (so it keeps the unlabeled caller partition)
// holding what a plugin may use, including acknowledging a delivery (the host
// is the delivery worker in process). It does not hold the decision scope or
// administration.
func PluginHostPrincipal() libauth.Principal {
	return libauth.Principal{
		Verified: true,
		Kind:     "service",
		ID:       "tangent:plugin-host",
		Scopes:   []string{View, Draft, Submit, Cancel, Close, Withdraw, Deliver, Plugin},
	}
}

// MCP is the scope that reaches /mcp at all (the mcp module's own, "<app>.mcp").
const MCP = "tangent.mcp"

// PluginChildScopes is what the key the host mints for a plugin child holds: what a plugin may
// use (the plugin host's scopes) and the right to reach /mcp. Never the decision or
// administration; TestPluginChildScopesNeverDecide holds that.
func PluginChildScopes() []string {
	return append(slices.Clone(PluginHostPrincipal().Scopes), MCP)
}

// AuthorizeTool is the MCP authorizer: the caller in ctx must be verified and
// hold the scope the tool needs. The host's guard has already verified the
// credential on the way in; this is the per-tool half.
func AuthorizeTool(ctx context.Context, _ string, scope string) error {
	p, ok := libauth.FromContext(ctx)
	if !ok || !p.Verified || scope == "" || !p.HasScope(scope) {
		return errors.New("access: the caller does not hold the tool's scope")
	}
	return nil
}
