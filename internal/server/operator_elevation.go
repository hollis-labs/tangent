package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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
	// DeElevatePath ends operator elevation for the browser's session.
	DeElevatePath = "/api/session/de-elevate"

	elevateMaxBodyBytes = 4 * 1024

	elevationWindow            = time.Minute
	elevationFailuresPerWindow = 5
	// elevationFloodThreshold is an alert, not a refusal. A process-wide
	// refusal would let any local agent minting free sessions keep the real
	// operator locked out indefinitely, and it buys nothing: a 32+ byte
	// random token is not guessable at any rate. Crossing it emits one
	// telemetry event and one log line per window, and refuses nobody.
	elevationFloodThreshold = 10

	codeElevationRefused     = "elevation_refused"
	codeElevationRateLimited = "elevation_rate_limited"
	codeElevationFlood       = "elevation_flood"

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
	// ElevationTTL is the configured lifetime of an elevation (TANGENT_ELEVATION_TTL):
	// "always", "never" or "<N>d". "always" lasts until revoked or until the
	// browser session itself ends.
	ElevationTTL string `json:"elevation_ttl"`
	// ElevationExpiresAt is when this session stops reading as elevated: the
	// elevation window's end or the session's own absolute expiry, whichever
	// comes first. Present only while elevated.
	ElevationExpiresAt *time.Time `json:"elevation_expires_at,omitempty"`
}

// newSessionStatus describes a session under the gate's elevation policy.
func newSessionStatus(gate *participant.Gate, session participant.Session, tokenConfigured bool) sessionStatus {
	policy := gate.ElevationPolicy()
	status := sessionStatus{
		Assurance:               session.Assurance,
		Elevated:                session.Elevated(),
		OperatorTokenConfigured: tokenConfigured,
		ElevationTTL:            policy.String(),
	}
	if session.Elevated() {
		end := session.ElevationEnd(policy).UTC()
		status.ElevationExpiresAt = &end
	}
	return status
}

// elevationLimiter counts elevation attempts in a sliding window. The
// per-session limit refuses; the process-wide count only raises a flood alert.
// It is in-process on purpose: Tangent is a single process.
//
// An attempt is admitted and recorded as a failure in one critical section,
// before the token is examined, and a success removes its own record. That
// makes the check and the record atomic: N concurrent requests from one
// session cannot all pass the check before any of them is counted.
type elevationLimiter struct {
	mu          sync.Mutex
	now         func() time.Time
	global      []time.Time
	perSession  map[string][]time.Time
	lastFloodAt time.Time
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

// elevationAttempt is one admitted attempt, provisionally counted as a
// failure until succeed removes it.
type elevationAttempt struct {
	sessionID string
	at        time.Time
}

// admit decides whether an attempt from this session may be examined and, if
// so, records it as a failure in the same critical section. flood is true
// once per window when the process-wide failure count crosses the alert
// threshold.
func (l *elevationLimiter) admit(sessionID string) (attempt elevationAttempt, ok bool, flood bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-elevationWindow)
	l.global = pruneWindow(l.global, cutoff)
	for id, events := range l.perSession {
		if events = pruneWindow(events, cutoff); len(events) == 0 {
			delete(l.perSession, id)
		} else {
			l.perSession[id] = events
		}
	}
	if len(l.perSession[sessionID]) >= elevationFailuresPerWindow {
		return elevationAttempt{}, false, false
	}
	l.global = append(l.global, now)
	l.perSession[sessionID] = append(l.perSession[sessionID], now)
	if len(l.global) >= elevationFloodThreshold && !now.Before(l.lastFloodAt.Add(elevationWindow)) {
		l.lastFloodAt = now
		flood = true
	}
	return elevationAttempt{sessionID: sessionID, at: now}, true, flood
}

// succeed removes an admitted attempt's provisional failure record.
func (l *elevationLimiter) succeed(attempt elevationAttempt) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.global = removeOnce(l.global, attempt.at)
	if events := removeOnce(l.perSession[attempt.sessionID], attempt.at); len(events) == 0 {
		delete(l.perSession, attempt.sessionID)
	} else {
		l.perSession[attempt.sessionID] = events
	}
}

func removeOnce(events []time.Time, at time.Time) []time.Time {
	for i := range events {
		if events[i].Equal(at) {
			return append(events[:i], events[i+1:]...)
		}
	}
	return events
}

// registerOperatorElevation mounts the elevation and status routes behind the
// same same-origin guard as every browser API route. Both require a live
// session — minted is enough, since elevating one is the point.
func registerOperatorElevation(mux *http.ServeMux, cfg Config, logger *slog.Logger) {
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
			reportObjectAccessDenial(r.Context(), recorder, authz.View)
			return
		}
		writeHITLJSON(w, http.StatusOK, newSessionStatus(gate, session, gate.OperatorTokenConfigured()))
	})))

	// De-elevation needs a live session and nothing else: ending authority is
	// always safe to grant to whoever holds the cookie, and the same-origin
	// guard keeps a foreign page from doing it to a browser that visits it.
	mux.Handle("POST "+DeElevatePath, hitlSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := gate.Resolve(r)
		if err != nil {
			writeParticipantRefusal(w)
			reportObjectAccessDenial(r.Context(), recorder, authz.View)
			return
		}
		minted, err := gate.DeElevate(r.Context(), w, session)
		if err != nil {
			writeHITLJSON(w, http.StatusInternalServerError, map[string]any{
				"code": "internal_error", "message": "operator elevation could not be ended",
			})
			return
		}
		writeHITLJSON(w, http.StatusOK, newSessionStatus(gate, minted, gate.OperatorTokenConfigured()))
	})))

	mux.Handle("POST "+ElevatePath, hitlSameOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := gate.Resolve(r)
		if err != nil {
			writeParticipantRefusal(w)
			reportObjectAccessDenial(r.Context(), recorder, authz.Resolve)
			return
		}
		attempt, admitted, flood := limiter.admit(session.ID)
		if flood {
			// One line and one event per window, naming nothing but the
			// threshold: never a token, a session, or a candidate.
			logger.Warn("operator elevation: failure rate above alert threshold",
				"threshold_per_minute", elevationFloodThreshold)
			reportObjectAccessDenialCode(r.Context(), recorder, authz.Resolve, codeElevationFlood)
		}
		if !admitted {
			writeHITLJSON(w, http.StatusTooManyRequests, map[string]any{
				"code": codeElevationRateLimited, "message": elevationRateLimitedMessage,
			})
			reportObjectAccessDenialCode(r.Context(), recorder, authz.Resolve, codeElevationRateLimited)
			return
		}
		candidate, decodeErr := decodeElevateCommand(w, r)
		if decodeErr != nil {
			// A malformed body is a failed attempt like any other (it stays
			// counted), which keeps the limiter from being a free oracle on
			// body shape.
			refuseElevation(w, r, recorder)
			return
		}
		elevated, err := gate.Elevate(r.Context(), w, session, candidate)
		if err != nil {
			if !errors.Is(err, participant.ErrElevationRefused) {
				limiter.succeed(attempt)
				writeHITLJSON(w, http.StatusInternalServerError, map[string]any{
					"code": "internal_error", "message": "operator elevation could not be recorded",
				})
				return
			}
			refuseElevation(w, r, recorder)
			return
		}
		limiter.succeed(attempt)
		writeHITLJSON(w, http.StatusOK, newSessionStatus(gate, elevated, true))
	})))
}

func refuseElevation(w http.ResponseWriter, r *http.Request, recorder *telemetry.Recorder) {
	writeHITLJSON(w, http.StatusForbidden, map[string]any{
		"code": codeElevationRefused, "message": elevationRefusedMessage,
	})
	reportObjectAccessDenialCode(r.Context(), recorder, authz.Resolve, codeElevationRefused)
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
