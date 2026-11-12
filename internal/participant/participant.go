// Package participant resolves who a browser request acts as.
//
// A participant is a signed-in user. The identity module owns the session, its
// cookie, its lifetime and its revocation; this package only projects the
// principal the auth guard verified into the Session the HITL, turns, docs,
// artifact, channel and effect handlers record as the actor, and into the
// binding a WebSocket carries.
//
// What it does not do is mint, store or elevate anything. There is no
// participant cookie, no loopback admission and no operator token: a decision
// needs a user session holding tangent.resolve (package access), and that is
// all it needs.
package participant

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tangent/internal/access"
	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/room"
)

const (
	// Authority is recorded on every interaction a user acts on.
	Authority = "tangent-identity"
	// AssuranceSession is the assurance of a signed-in user session. It ranks
	// above the loopback-unverified and operator-token values the old
	// admission recorded (interaction.AssuranceRank), so the owner's session
	// may take over, and resolve, an item presented under either; the takeover
	// is journaled with both refs.
	AssuranceSession = "identity-session"
)

// ErrNoSession means the request carries no signed-in user. An API key is not
// a participant: it is an agent's credential and never acts as the person.
var ErrNoSession = errors.New("participant: request carries no user session")

// Session is the signed-in user behind one request.
type Session struct {
	// ID is the identity session id. It is not capability material (the cookie
	// value is) and never leaves the process.
	ID        string
	Scope     string
	Ref       string
	Authority string
	Assurance string
	Grants    []authz.Capability
	ExpiresAt time.Time
}

// Holds reports whether the session was granted a capability.
func (s Session) Holds(capability authz.Capability) bool {
	for _, granted := range s.Grants {
		if granted == capability {
			return true
		}
	}
	return false
}

// Verifier re-checks a bearer token. identity.Service.Verify is one.
type Verifier func(ctx context.Context, token string) (libauth.Principal, error)

// Gate resolves participants from verified principals.
type Gate struct {
	verify Verifier
}

// NewGate returns a Gate. verify is optional: with it a WebSocket re-checks its
// session before a frame that decides, so a session revoked while the socket
// is open stops deciding at once.
func NewGate(verify Verifier) *Gate { return &Gate{verify: verify} }

// FromPrincipal projects a principal into a Session, or reports ErrNoSession
// when it is not a user session.
func (g *Gate) FromPrincipal(p libauth.Principal) (Session, error) {
	if !access.IsUserSession(p) {
		return Session{}, ErrNoSession
	}
	return Session{
		ID:        p.SessionID,
		Scope:     authz.ParticipantScope,
		Ref:       p.ID,
		Authority: Authority,
		Assurance: AssuranceSession,
		Grants:    grants(p),
		ExpiresAt: p.ExpiresAt,
	}, nil
}

// grants is the participant row of the ADR 0004 matrix intersected with the
// scopes the user holds. Resolve additionally needs the user session, which
// FromPrincipal has already required.
func grants(p libauth.Principal) []authz.Capability {
	var out []authz.Capability
	for _, capability := range authz.DefaultParticipantCapabilities() {
		if p.HasScope(access.ForCapability(capability)) {
			out = append(out, capability)
		}
	}
	return out
}

// Resolve returns the session behind a request the auth guard admitted.
func (g *Gate) Resolve(r *http.Request) (Session, error) {
	p, ok := libauth.FromContext(r.Context())
	if !ok {
		return Session{}, ErrNoSession
	}
	return g.FromPrincipal(p)
}

// Authorize binds a session onto authz.Authorize, so no transport assembles a
// request by hand and the matrix is consulted even where the grants would have
// answered: a participant never closes however its scopes are spelled.
func (g *Gate) Authorize(session Session, ownerScope string, capability authz.Capability) error {
	return authz.Authorize(authz.Request{
		Kind:       authz.KindParticipant,
		Scope:      session.Scope,
		Granted:    session.Grants,
		OwnerScope: ownerScope,
		Capability: capability,
	})
}

// LocalSurfaces names the object the browser routes are bound to: the whole
// local participant surface set, which is what the user sees.
func LocalSurfaces() string { return authz.ParticipantScope }

type contextKey struct{}

// WithSession stores a resolved session on a request context.
func WithSession(ctx context.Context, session Session) context.Context {
	return context.WithValue(ctx, contextKey{}, session)
}

// FromContext returns the session a middleware resolved for this request.
func FromContext(ctx context.Context) (Session, bool) {
	session, ok := ctx.Value(contextKey{}).(Session)
	return session, ok
}

// ResolveBinding is the room-facing binding for a request, the shape the
// WebSocket installs as its ParticipantResolver. The session id stays out of
// it; the bearer token stays in the Revalidate closure only, in memory.
func (g *Gate) ResolveBinding(r *http.Request) (room.ParticipantBinding, error) {
	session, err := g.Resolve(r)
	if err != nil {
		return room.ParticipantBinding{}, err
	}
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return g.binding(session, token), nil
}

func (g *Gate) binding(session Session, token string) room.ParticipantBinding {
	return room.ParticipantBinding{
		Scope:        session.Scope,
		PrincipalRef: session.Ref,
		Authority:    session.Authority,
		Assurance:    session.Assurance,
		Capabilities: authz.FormatCapabilities(session.Grants),
		Revalidate: func(ctx context.Context) (room.ParticipantBinding, error) {
			if g.verify == nil || token == "" {
				return room.ParticipantBinding{}, ErrNoSession
			}
			p, err := g.verify(ctx, token)
			if err != nil {
				return room.ParticipantBinding{}, ErrNoSession
			}
			current, err := g.FromPrincipal(p)
			if err != nil {
				return room.ParticipantBinding{}, err
			}
			return g.binding(current, token), nil
		},
	}
}
