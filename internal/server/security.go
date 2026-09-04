package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/participant"
)

// This file holds the process-wide security posture and the participant-session
// gate, implementing ADR 0004 §4, §5, and §6.
//
// The shape to keep in mind while reading it: a room URL is a *locator*. It may
// appear in tool responses, agent transcripts, the address bar, and browser
// history without transferring any authority. What carries authority is a
// server-held session named by an HttpOnly cookie, which a link does not carry
// and a log line cannot leak.

// securityHeaders applies the response headers that belong on every response
// this process emits.
//
// `Referrer-Policy: no-referrer` because `design-iteration` renders untrusted
// agent-authored HTML and evidence payloads may carry links: locators are not
// authority, but leaking one to a third party is needless.
// `X-Content-Type-Options: nosniff` because the same untrusted material must
// never be re-typed by a browser's content sniffer.
//
// `Content-Security-Policy` and `Permissions-Policy` are the browser-side half
// of the renderer trust classes, and they are why three effect capabilities
// stopped being declarations (see csp.go, which explains each directive and
// each thing it deliberately does not do). `X-Frame-Options` duplicates the
// policy's `frame-ancestors` for browsers that read only the older header.
//
// `Cross-Origin-Opener-Policy` severs the `window.opener` relationship a page
// that navigated to Tangent would otherwise keep, and
// `Cross-Origin-Resource-Policy` stops another site from loading Tangent's
// responses as subresources. Both are free on a single-origin loopback
// application and both close a class of cross-origin read that no capability
// check would ever see.
//
// It wraps the mux rather than each route: a header that is applied per route
// is a header that the next route forgets.
func securityHeaders(devFrontendURL string, next http.Handler) http.Handler {
	permissions := permissionsPolicy()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Content-Security-Policy", contentSecurityPolicy(r, devFrontendURL))
		header.Set("Permissions-Policy", permissions)
		header.Set("X-Frame-Options", "DENY")
		header.Set("Cross-Origin-Opener-Policy", "same-origin")
		header.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// mintParticipantSessions issues a participant session to a browser that
// navigates to a Tangent document without one.
//
// This is what keeps standalone use ergonomic: the user opens a URL and
// answers, and nothing new appears on any tool argument. The session — not the
// URL — is thereafter the authority, so a second browser on the same machine
// gets its own session and its own audit trail rather than inheriting the
// first one's.
//
// Only same-origin loopback *document* navigations mint. A cross-site request,
// a non-loopback Host, or a subresource fetch does not: minting on anything
// that a foreign page can cause would hand a session to the page that caused
// it.
func mintParticipantSessions(gate *participant.Gate, next http.Handler) http.Handler {
	if gate == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isDocumentNavigation(r) || !isSameOriginLoopback(r) {
			next.ServeHTTP(w, r)
			return
		}
		switch _, err := gate.Resolve(r); {
		case err == nil:
			// Already carries a live session.
		case errors.Is(err, participant.ErrSessionInvalid):
			// The cookie named a revoked, expired, or unknown session. Clear it
			// and issue a fresh one, so a user whose sessions were revoked can
			// simply reload rather than having to clear site data by hand.
			gate.Clear(w)
			_, _ = gate.Mint(w, r)
		default:
			_, _ = gate.Mint(w, r)
		}
		next.ServeHTTP(w, r)
	})
}

// isDocumentNavigation reports whether this request is a browser loading a
// Tangent page, as opposed to a subresource, an API call, or a WebSocket
// upgrade.
//
// `Sec-Fetch-Dest: document` is the authoritative signal where a browser sends
// it. The Accept fallback covers the browsers and the dev proxy that do not,
// and a bare GET with no Accept header is not a document.
func isDocumentNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" {
		return dest == "document"
	}
	return acceptsHTML(r.Header.Get("Accept"))
}

func acceptsHTML(accept string) bool {
	return strings.Contains(accept, "text/html")
}

// isSameOriginLoopback is the admission fact a minted session records. It is
// the same test the operator API has always applied, named here so the minting
// path and the guard cannot drift apart.
func isSameOriginLoopback(r *http.Request) bool {
	if !requestHostIsLoopback(r.Host) {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && !requestOriginMatches(r, origin) {
		return false
	}
	return true
}

// requireParticipant refuses a request that carries no live participant
// session, or whose session was not granted capability.
//
// It is the ADR 0004 §4.4 rule: every state-changing browser request needs a
// valid session with the relevant capability on the named object. The static
// SPA bundle does not, which is why this wraps API routes and not the root
// handler.
//
// The refusal names nothing. An authorization failure that reports the
// required capability, the owning scope, or the participant is a probe.
func requireParticipant(
	gate *participant.Gate,
	capability authz.Capability,
	next http.Handler,
) http.Handler {
	if gate == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := gate.Resolve(r)
		if err != nil {
			writeParticipantRefusal(w)
			return
		}
		// Through the matrix, not just the session's own grant set. A
		// participant never holds `close` however a session row is spelled,
		// and routing the check here rather than reading Holds directly is
		// what keeps that true in one place.
		if gate.Authorize(session, participant.LocalSurfaces(), capability) != nil {
			writeParticipantRefusal(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(participant.WithSession(r.Context(), session)))
	})
}

// participantRefusalMessage is fixed, and deliberately says nothing about why.
const participantRefusalMessage = "this browser has no authorized Tangent session for that action"

func writeParticipantRefusal(w http.ResponseWriter) {
	writeHITLJSON(w, http.StatusForbidden, map[string]any{
		"code":    "forbidden",
		"message": participantRefusalMessage,
	})
}

// sameOriginGuard is the cross-site protection ADR 0004 §4.5 extends to `/mcp`,
// `/sse`, and `/ws`.
//
// It is the same test the operator API has always applied, and it works for
// the same reason: a non-browser client sends none of these headers and is
// unaffected, while a browser that sends them must be explicitly same-origin.
// Before this, `/mcp` carried no origin, same-site, or CSRF middleware at all,
// so any page the operator had open could reach every session_* tool.
//
// `Sec-Fetch-Site: none` is permitted because it means a user-initiated
// navigation — a typed URL or a bookmark — which is how someone diagnoses
// `/sse` by hand. It is not a cross-site vector: a foreign page cannot cause a
// request that reports `none`, and a cross-site form POST reports both
// `cross-site` and an `Origin`, so it is refused twice over.
//
// It refuses in plain text rather than the HITL contract envelope: these
// routes are not the HITL API and must not appear to be.
func sameOriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requestHostIsLoopback(r.Host) {
			http.Error(w, "tangent is available on loopback hosts only", http.StatusForbidden)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "same-origin browser requests only", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !requestOriginMatches(r, origin) {
			http.Error(w, "same-origin browser requests only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
