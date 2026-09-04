// Package participant owns the authenticated browser participant session:
// minting on loopback admission, durable storage of a hash rather than the
// cookie, rotation on assurance change, and revocation.
//
// It implements ADR 0004 §4. The property the whole package exists for is
// this: sharing, logging, or pasting a room URL no longer shares access.
// Authority moves off a string that travels and onto a cookie that does not.
//
// Loopback admission is not authentication, and this package must never be
// described as if it were. A malicious local process running as the same user
// can still mint a session by making a same-origin request. What the session
// buys is that a room UUID stops being an answer credential.
package participant

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hollis-labs/tangent/internal/authz"
)

const (
	// CookieName names the participant session cookie.
	//
	// It carries no `__Host-` prefix: that prefix requires the Secure
	// attribute, which loopback HTTP cannot set. This is inherent to the
	// deployment shape rather than an oversight (ADR 0004 §4.2).
	CookieName = "tangent_participant"

	// MaximumLifetime is the absolute session lifetime. There is deliberately
	// no idle expiry — a local single-user tool must not log its user out
	// mid-decision.
	MaximumLifetime = 30 * 24 * time.Hour

	// cookieEntropyBytes sizes the cookie value. 32 bytes is the same order as
	// a session id from any mainstream framework and well past guessing.
	cookieEntropyBytes = 32
)

// Errors returned by the store and the resolver.
var (
	// ErrNoSession means the request carried no participant session cookie at
	// all. It is separate from ErrSessionInvalid because a missing cookie on a
	// document navigation is the ordinary first-visit case that mints one,
	// while a cookie naming nothing is not.
	ErrNoSession = errors.New("participant: request carries no session")

	// ErrSessionInvalid means the cookie named no live session — unknown,
	// revoked, or past its absolute expiry.
	ErrSessionInvalid = errors.New("participant: session is not valid")
)

// Session is one authenticated browser participant.
//
// It never carries the cookie value. Code that needs to set a cookie receives
// the value once, from Mint, and nothing retains it.
type Session struct {
	ID         string
	Scope      string
	Ref        string
	Authority  string
	Assurance  string
	Grants     []authz.Capability
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// Holds reports whether this session was granted a capability.
func (s Session) Holds(capability authz.Capability) bool {
	for _, granted := range s.Grants {
		if granted == capability {
			return true
		}
	}
	return false
}

// DefaultTemplate is the participant a freshly minted loopback session
// instantiates.
//
// It is the same local operator `internal/hitl` has always recorded, with the
// same authority and the same explicitly unverified assurance. ADR 0004 §4.3
// makes it a *template* rather than the identity itself: the session is the
// identity, and two browsers on the same machine get two sessions and two
// audit trails while both act as this operator.
var DefaultTemplate = Session{
	Scope:     authz.ParticipantScope,
	Ref:       "local-operator",
	Authority: "tangent-loopback",
	Assurance: "loopback-unverified",
	Grants:    authz.DefaultParticipantCapabilities(),
}

// newCookieValue returns a fresh opaque cookie value and its storage hash.
func newCookieValue() (value string, hash string, err error) {
	raw := make([]byte, cookieEntropyBytes)
	if _, readErr := rand.Read(raw); readErr != nil {
		return "", "", fmt.Errorf("participant: read entropy: %w", readErr)
	}
	value = base64.RawURLEncoding.EncodeToString(raw)
	return value, HashCookie(value), nil
}

// HashCookie returns the storage form of a cookie value. It is exported so a
// test can assert that no row anywhere holds the value itself.
func HashCookie(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// constantTimeEqual compares two hashes without leaking a timing signal. The
// values compared are already digests rather than secrets, but the comparison
// is on the authentication path and cheap to get right.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// NewCookie builds the Set-Cookie for a minted or rotated session.
//
// HttpOnly so script cannot read it; SameSite=Lax rather than Strict so a link
// opened from a terminal or another application still carries the session on
// top-level navigation; Path=/ with no Domain; and no Secure, which loopback
// HTTP cannot use.
func NewCookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
	}
}

// clearedCookie expires a cookie that named nothing live, so a browser holding
// a revoked session stops presenting it on every request.
func clearedCookie() *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}
