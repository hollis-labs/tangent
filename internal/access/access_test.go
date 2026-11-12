package access

import (
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"
)

func TestOnlyASignedInUserWithTheScopeMayResolve(t *testing.T) {
	user := libauth.Principal{Verified: true, Kind: "user", ID: "user:u1", SessionID: "ses_1", Scopes: []string{Resolve}}
	cases := map[string]struct {
		p    libauth.Principal
		want bool
	}{
		"user session with the scope":      {user, true},
		"user session with the wildcard":   {withScopes(user, "*"), true},
		"user session without the scope":   {withScopes(user, View), false},
		"unverified":                       {unverified(user), false},
		"user token with no session":       {noSession(user), false},
		"api key holding the scope":        {libauth.Principal{Verified: true, Kind: "service", ID: "key:k1", Scopes: []string{Resolve}}, false},
		"api key holding the wildcard":     {libauth.Principal{Verified: true, Kind: "service", ID: "key:k1", Scopes: []string{"*"}}, false},
		"api key holding the app wildcard": {libauth.Principal{Verified: true, Kind: "service", ID: "key:k1", Scopes: []string{"tangent.*"}}, false},
		"service with a user-shaped id":    {libauth.Principal{Verified: true, Kind: "service", ID: "user:u1", SessionID: "ses_1", Scopes: []string{Resolve}}, false},
		"the plugin host":                  {PluginHostPrincipal(), false},
		"nobody":                           {libauth.Principal{}, false},
	}
	for name, c := range cases {
		if got := MayResolve(c.p); got != c.want {
			t.Errorf("%s: MayResolve = %t, want %t", name, got, c.want)
		}
	}
}

func TestThePluginHostNeverHoldsTheDecisionOrAdministration(t *testing.T) {
	p := PluginHostPrincipal()
	for _, scope := range []string{Resolve, Admin} {
		if p.HasScope(scope) {
			t.Errorf("the plugin host holds %s", scope)
		}
	}
}

func TestPluginChildScopesNeverDecide(t *testing.T) {
	p := libauth.Principal{Verified: true, Kind: "service", Scopes: PluginChildScopes()}
	for _, scope := range []string{Resolve, Admin} {
		if p.HasScope(scope) {
			t.Errorf("a plugin child's key holds %s", scope)
		}
	}
	for _, scope := range PluginChildScopes() {
		if scope == "*" || strings.HasSuffix(scope, ".*") {
			t.Errorf("a plugin child's key holds the wildcard %s", scope)
		}
	}
	if !p.HasScope(MCP) || !p.HasScope(Plugin) || !p.HasScope(View) {
		t.Errorf("a plugin child's key cannot reach /mcp or use what a plugin may: %v", PluginChildScopes())
	}
}

func withScopes(p libauth.Principal, scopes ...string) libauth.Principal {
	p.Scopes = scopes
	return p
}

func unverified(p libauth.Principal) libauth.Principal {
	p.Verified = false
	return p
}

func noSession(p libauth.Principal) libauth.Principal {
	p.SessionID = ""
	return p
}
