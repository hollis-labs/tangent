package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/server"
)

// ownerPrincipal is a signed-in user holding every scope, as the host's guard
// would have stamped one for the owner's browser session.
func ownerPrincipal() libauth.Principal {
	return libauth.Principal{
		Verified: true, ID: "user:usr_test", Kind: "user", SessionID: "ses_test",
		Scopes: []string{"*"}, ExpiresAt: time.Now().Add(time.Hour),
	}
}

// serveAs builds the route table (with a gate when the config has none) and
// serves it over HTTP behind a stamp that makes every request p.
func serveAs(t *testing.T, cfg server.Config, p libauth.Principal) string {
	t.Helper()
	if cfg.Participants == nil {
		cfg.Participants = participant.NewGate(nil)
	}
	table, err := server.New(cfg)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		table.Handler().ServeHTTP(w, r.WithContext(libauth.WithPrincipal(r.Context(), p)))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
