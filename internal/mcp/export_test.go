package mcp

import (
	"context"
	"net/http"
	"strings"

	mcphttp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/transport/http"
	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tangent/internal/access"
)

// Tests build servers with no host to authorize them. They are allowed every
// call unless a test says otherwise with WithAuthorizer.
func init() {
	defaultAuthorizer = func(context.Context, string, string) error { return nil }
}

// TestKeys maps a bearer token to the name of the key it stands for, as the
// host's identity module would have verified it.
type TestKeys map[string]string

// HTTPHandler serves the SDK server over the Streamable HTTP transport the
// host uses, with no authentication in front: the caller is anonymous.
func (s *Server) HTTPHandler() http.Handler { return s.HTTPHandlerWith(nil) }

// HTTPHandlerWith is HTTPHandler behind a stand-in for the host's guard: a
// request whose bearer token is in keys is stamped with that key's principal,
// and one that carries any other token, or none, is refused. A nil keys is no
// guard at all.
func (s *Server) HTTPHandlerWith(keys TestKeys) http.Handler {
	inner := mcphttp.NewHandler(s.mcp, mcphttp.HandlerOptions{})
	if keys == nil {
		return inner
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		name, ok := keys[token]
		if !ok || token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		principal := libauth.Principal{Verified: true, ID: "key:" + name, Kind: "service", Display: name, Scopes: []string{"*"}}
		r.Header.Del("Authorization")
		inner.ServeHTTP(w, r.WithContext(libauth.WithPrincipal(r.Context(), principal)))
	})
}

// ScopeToolForTest gives a tool a test registered straight on the SDK server
// the view scope, as registering it through the package would have.
func (s *Server) ScopeToolForTest(name string) { s.scopes.set(name, access.View) }

// HostToolScopes is the scope table's host half.
func HostToolScopes() map[string]string { return hostToolScopes }
