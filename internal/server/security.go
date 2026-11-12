package server

import (
	"net/http"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/telemetry"
)

// This file holds the participant gate the browser routes sit behind.
//
// Who may call, from which host, and with which credential are not decided
// here: the host guard, the identity module and the auth guard have run before
// any handler in this package. What is left is the check that the caller is a
// signed-in user (an API key is an agent's credential, never the person) and
// that the user's grants cover the capability the route was registered with.

// requireParticipant refuses a request that carries no signed-in user, or
// whose user was not granted capability.
//
// The refusal names nothing. An authorization failure that reports the
// required capability, the owning scope, or the participant is a probe.
func requireParticipant(
	gate *participant.Gate,
	recorder *telemetry.Recorder,
	capability authz.Capability,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := gate.Resolve(r)
		if err != nil {
			writeParticipantRefusal(w)
			reportObjectAccessDenial(r.Context(), recorder, capability)
			return
		}
		// Through the matrix, not just the session's own grant set. A
		// participant never holds `close` however its scopes are spelled, and
		// routing the check here rather than reading Holds directly is what
		// keeps that true in one place.
		if authErr := gate.Authorize(session, participant.LocalSurfaces(), capability); authErr != nil {
			writeParticipantRefusal(w)
			reportObjectAccessDenial(r.Context(), recorder, capability)
			return
		}
		next.ServeHTTP(w, r.WithContext(participant.WithSession(r.Context(), session)))
	})
}

// participantRefusalMessage is fixed, and deliberately says nothing about why.
const participantRefusalMessage = "this request is not from a signed-in user who may take that action"

func writeParticipantRefusal(w http.ResponseWriter) {
	writeHITLJSON(w, http.StatusForbidden, map[string]any{
		"code":    "forbidden",
		"message": participantRefusalMessage,
	})
}

// sameOriginGuard is the cross-site protection for the /ws upgrade.
//
// A non-browser client sends none of these headers and is unaffected, while a
// browser that sends them must be explicitly same-origin. Host checking is the
// host guard's, which knows the configured allowed hosts.
//
// `Sec-Fetch-Site: none` is permitted because it means a user-initiated
// navigation. It is not a cross-site vector: a foreign page cannot cause a
// request that reports `none`.
func sameOriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			writeForbiddenOrigin(w)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !requestOriginMatches(r, origin) {
			writeForbiddenOrigin(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeForbiddenOrigin(w http.ResponseWriter) {
	writeHITLJSON(w, http.StatusForbidden, map[string]any{
		"code":    "forbidden_origin",
		"message": "this API accepts same-origin browser requests only",
	})
}
