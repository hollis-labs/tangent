package participant

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/room"
)

// Gate is the request-facing half of the participant session: it reads the
// cookie, resolves the session, and mints one when a document navigation
// arrives without.
//
// Transports hold a Gate rather than a Store so that "what does this request
// prove" has one answer, and so nothing outside this package ever handles a
// cookie value.
type Gate struct {
	store *Store
}

// NewGate constructs a Gate over a session store.
func NewGate(store *Store) (*Gate, error) {
	if store == nil {
		return nil, errors.New("participant: session store is required")
	}
	return &Gate{store: store}, nil
}

// Resolve returns the live session a request carries.
//
// It is the only way a transport learns who is acting. A missing cookie is
// ErrNoSession and a cookie naming nothing live is ErrSessionInvalid; callers
// that must refuse treat both the same, and only the minting path
// distinguishes them.
func (g *Gate) Resolve(r *http.Request) (Session, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil || cookie.Value == "" {
		return Session{}, ErrNoSession
	}
	session, err := g.store.Lookup(r.Context(), cookie.Value)
	if err != nil {
		return Session{}, err
	}
	// Last-seen is an audit fact, not a deadline — there is no idle expiry — so
	// it is written at a coarse interval rather than on every request. The tab
	// strip polls every five seconds and the inbox stream reconnects every
	// fifty; a write per poll would put a steady stream of contention on the
	// single SQLite connection the interaction store also uses, to record a
	// timestamp nothing reads at that resolution.
	//
	// Best effort either way: a session is still valid whether or not the
	// timestamp lands, and failing a request over an audit write would trade a
	// fact for an outage.
	if time.Since(session.LastSeenAt) >= lastSeenResolution {
		_ = g.store.Touch(r.Context(), session.ID)
	}
	return session, nil
}

// lastSeenResolution is how coarsely a session's last-seen timestamp is
// recorded.
const lastSeenResolution = time.Minute

// Mint creates a session for this browser and writes its cookie.
//
// The only evidence is loopback admission, which is exactly what the session
// records: the default template's assurance is `loopback-unverified`. Minting
// is what keeps standalone use ergonomic — a user opens a URL and answers,
// with nothing new on any tool argument — and it is why an invitation token
// was rejected: on loopback an invitation proves nothing admission did not,
// while adding a secret to leak.
func (g *Gate) Mint(w http.ResponseWriter, r *http.Request) (Session, error) {
	session, value, err := g.store.Mint(r.Context(), DefaultTemplate)
	if err != nil {
		return Session{}, err
	}
	http.SetCookie(w, NewCookie(value, session.ExpiresAt))
	return session, nil
}

// Clear expires a cookie that named nothing live, so a browser holding a
// revoked session stops presenting it on every subsequent request.
func (g *Gate) Clear(w http.ResponseWriter) {
	http.SetCookie(w, clearedCookie())
}

// Authorize answers one participant access question against a named object.
//
// It is a thin binding of the session onto authz.Authorize so that no
// transport assembles an authz.Request by hand — the single decision stays
// single, and the matrix is consulted even where the session's own grant set
// would have answered. That matters: `Grants(KindParticipant, Close)` is false,
// so a session row that somehow carried `close` still cannot close anything.
func (g *Gate) Authorize(session Session, ownerScope string, capability authz.Capability) error {
	return authz.Authorize(authz.Request{
		Kind:       authz.KindParticipant,
		Scope:      session.Scope,
		Granted:    session.Grants,
		OwnerScope: ownerScope,
		Capability: capability,
	})
}

// LocalSurfaces names the object a loopback participant session is bound to.
//
// The browser routes this gate protects are not per-object: the inbox is the
// operator's whole queue and the room list is every room this machine can see.
// The object for those is the local participant surface set, which is what the
// session was minted against.
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

// ResolveBinding returns the room-facing participant binding for a request, or
// an error when the request carries no authorized session.
//
// It is the shape internal/ws installs as its ParticipantResolver: the ws
// package holds a function, not this package, so the transport keeps its
// existing seam and this package keeps sole custody of the cookie.
//
// The binding carries the capability set and deliberately not the session id.
// The session is the only capability material in the system; ADR 0004 §6.1
// keeps it in the cookie header and a hash column, never in a connection
// record, a room's metadata, or a log line.
func (g *Gate) ResolveBinding(r *http.Request) (room.ParticipantBinding, error) {
	session, err := g.Resolve(r)
	if err != nil {
		return room.ParticipantBinding{}, err
	}
	return room.ParticipantBinding{
		Scope:        session.Scope,
		PrincipalRef: session.Ref,
		Authority:    session.Authority,
		Assurance:    session.Assurance,
		Capabilities: authz.FormatCapabilities(session.Grants),
	}, nil
}
