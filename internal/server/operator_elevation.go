package server

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"sync"
	"time"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/telemetry"
)

// Operator elevation over HTTP (CW-20261016-0174).
//
// POST /api/session/elevate takes the operator token in a JSON body, verifies
// it in constant time against the digest the gate holds, and on success
// rotates the browser's session to `operator-token` assurance. GET
// /api/session reports whether this browser is elevated, so the UI can ask
// for the token before an operator tries to act rather than after.
//
// Neither route ever logs, echoes, or stores the token. The request log
// records the path only, the body is never read into anything that outlives
// the handler, and every refusal is a fixed message.

const (
	// ElevatePath is the elevation endpoint.
	ElevatePath = "/api/session/elevate"
	// SessionStatusPath reports the browser's elevation state.
	SessionStatusPath = "/api/session"

	elevateMaxBodyBytes = 4 * 1024

	elevationWindow            = time.Minute
	elevationFailuresPerWindow = 5
	// The global ceiling is what stops an attacker who mints a fresh session
	// per guess. It is deliberately not much larger than the per-session
	// one: a 32-byte token is not guessable at any rate, so the limit exists
	// to bound noise and make an attack loud, not to protect entropy.
	elevationGlobalFailuresPerWindow = 10

	codeElevationRefused     = "elevation_refused"
	codeElevationRateLimited = "elevation_rate_limited"

	elevationRefusedMessage     = "operator elevation refused"
	elevationRateLimitedMessage = "too many failed elevation attempts; wait a minute and try again"
)

type elevateCommand struct {
	Token string `json:"token"`
}

type sessionStatus struct {
	Assurance               string `json:"assurance"`
	Elevated                bool   `json:"elevated"`
	OperatorTokenConfigured bool   `json:"operator_token_configured"`
}

// elevationLimiter counts failed elevation attempts in a sliding window, per
// session and across the process. It is in-process on purpose: Tangent is a
// single process, and a restart clearing the counters costs an attacker a
// service restart they cannot cause.
type elevationLimiter struct {
	mu         sync.Mutex
	now        func() time.Time
	global     []time.Time
	perSession map[string][]time.Time
}

func newElevationLimiter() *elevationLimiter {
	return &elevationLimiter{now: time.Now, perSession: map[string][]time.Time{}}
}

func pruneWindow(events []time.Time, cutoff time.Time) []time.Time {
	kept := events[:0]
	for _, at := range events {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	return kept
}

// limited reports whether another attempt from this session must be refused
// unexamined.
func (l *elevationLimiter) limited(sessionID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := l.now().Add(-elevationWindow)
	l.global = pruneWindow(l.global, cutoff)
	for id, events := range l.perSession {
		if events = pruneWindow(events, cutoff); len(events) == 0 {
			delete(l.perSession, id)
		} else {
			l.perSession[id] = events
		}
	}
	return len(l.global) >= elevationGlobalFailuresPerWindow ||
		len(l.perSession[sessionID]) >= elevationFailuresPerWindow
}

func (l *elevationLimiter) recordFailure(sessionID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	at := l.now()
	l.global = append(l.global, at)
	l.perSession[sessionID] = append(l.perSession[sessionID], at)
}

// registerOperatorElevation mounts the elevation and status routes behind the
// same same-origin guard as every browser API route. Both require a live
// session — minted is enough, since elevating one is the point.
func registerOperatorElevation(mux *http.ServeMux, cfg Config) {
	gate := cfg.Participants
	if gate == nil {
		return
	}
	limiter := newElevationLimiter()
	recorder := cfg.Telemetry

	mux.Handle("GET "+SessionStatusPath, hitlSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := gate.Resolve(r)
		if err != nil {
			writeParticipantRefusal(w)
			reportObjectAccessDenial(r.Context(), recorder, authz.View, "browser-api")
			return
		}
		writeHITLJSON(w, http.StatusOK, sessionStatus{
			Assurance:               session.Assurance,
			Elevated:                session.Elevated(),
			OperatorTokenConfigured: gate.OperatorTokenConfigured(),
		})
	})))

	mux.Handle("POST "+ElevatePath, hitlSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := gate.Resolve(r)
		if err != nil {
			writeParticipantRefusal(w)
			reportObjectAccessDenial(r.Context(), recorder, authz.Resolve, "browser-api")
			return
		}
		if limiter.limited(session.ID) {
			writeHITLJSON(w, http.StatusTooManyRequests, map[string]any{
				"code": codeElevationRateLimited, "message": elevationRateLimitedMessage,
			})
			reportObjectAccessDenialCode(r.Context(), recorder, authz.Resolve, "browser-api", codeElevationRateLimited)
			return
		}
		candidate, decodeErr := decodeElevateCommand(w, r)
		if decodeErr != nil {
			// A malformed body is a failed attempt like any other: counting
			// it keeps the limiter from being a free oracle on body shape.
			limiter.recordFailure(session.ID)
			refuseElevation(w, r, recorder)
			return
		}
		elevated, err := gate.Elevate(r.Context(), w, session, candidate)
		if err != nil {
			if !errors.Is(err, participant.ErrElevationRefused) {
				writeHITLJSON(w, http.StatusInternalServerError, map[string]any{
					"code": "internal_error", "message": "operator elevation could not be recorded",
				})
				return
			}
			limiter.recordFailure(session.ID)
			refuseElevation(w, r, recorder)
			return
		}
		writeHITLJSON(w, http.StatusOK, sessionStatus{
			Assurance:               elevated.Assurance,
			Elevated:                elevated.Elevated(),
			OperatorTokenConfigured: true,
		})
	})))
}

func refuseElevation(w http.ResponseWriter, r *http.Request, recorder *telemetry.Recorder) {
	writeHITLJSON(w, http.StatusForbidden, map[string]any{
		"code": codeElevationRefused, "message": elevationRefusedMessage,
	})
	reportObjectAccessDenialCode(r.Context(), recorder, authz.Resolve, "browser-api", codeElevationRefused)
}

// decodeElevateCommand reads exactly one small JSON object carrying the token.
// Its errors never include the body.
func decodeElevateCommand(w http.ResponseWriter, r *http.Request) (string, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return "", errHITLContentType
	}
	r.Body = http.MaxBytesReader(w, r.Body, elevateMaxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var command elevateCommand
	if err := decoder.Decode(&command); err != nil {
		return "", errors.New("elevate: malformed command")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", errors.New("elevate: malformed command")
	}
	return command.Token, nil
}
