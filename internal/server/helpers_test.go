package server

import (
	"net/http"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tangent/internal/participant"
)

// Server is what the handler tests drive: the route table behind a request
// that already carries a verified user session holding every scope, as the
// host's guard would have stamped it.
type Server struct {
	mux http.Handler
}

// ownerPrincipal is a signed-in user holding every scope.
func ownerPrincipal() libauth.Principal {
	return libauth.Principal{
		Verified: true, ID: "user:usr_test", Kind: "user", SessionID: "ses_test",
		Scopes: []string{"*"}, ExpiresAt: time.Now().Add(time.Hour),
	}
}

func stamp(p libauth.Principal, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(libauth.WithPrincipal(r.Context(), p)))
	})
}

// newTest builds the table (with a gate when the config has none) and serves it
// as the owner.
func newTest(cfg Config) (*Server, error) {
	if cfg.Participants == nil {
		cfg.Participants = participant.NewGate(nil)
	}
	table, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return &Server{mux: stamp(ownerPrincipal(), table.Handler())}, nil
}
