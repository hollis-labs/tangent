package participant

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"github.com/hollis-labs/tangent/internal/authz"
)

// Operator elevation (CW-20261016-0174).
//
// Loopback admission is not authentication: any local process, and anything a
// reverse proxy forwards with a rewritten Host, can make a same-origin document
// navigation and mint a session. Before this change that minted session held
// `resolve`, so the agent that enqueued an approval could mint a session and
// approve its own request.
//
// The fix splits the session in two tiers:
//
//   - A minted session is `loopback-unverified`. It can load the UI and view
//     the queues; it does not hold `resolve`.
//   - An elevated session is `operator-token`. It is reached only by proving
//     knowledge of TANGENT_OPERATOR_TOKEN, a secret the owner provisions in the
//     service environment, and the elevation rotates the session id.
//
// Gate.Authorize additionally refuses `resolve` to any session whose assurance
// is not `operator-token`, whatever its stored grant set says. That matters for
// sessions minted before this change: their rows still list `resolve`, and the
// assurance check is what stops them using it.

const (
	// EnvOperatorToken names the environment variable the operator token is
	// read from. It is the only place the name is spelled.
	EnvOperatorToken = "TANGENT_OPERATOR_TOKEN"

	// MinOperatorTokenBytes is the shortest token accepted. A shorter value is
	// treated exactly like an unset one: elevation fails closed.
	MinOperatorTokenBytes = 32

	// AssuranceLoopbackUnverified is what a minted session records: the only
	// evidence behind it is loopback admission.
	AssuranceLoopbackUnverified = "loopback-unverified"

	// AssuranceOperatorToken is what an elevated session records, and what
	// a resolution made through it carries as participant_assurance.
	AssuranceOperatorToken = "operator-token"
)

// ErrAssuranceInsufficient is the refusal for a session that is authorized for
// the surface but has not been elevated to operator assurance. It is distinct
// from authz.ErrForbidden so the browser can be told what would fix it.
var ErrAssuranceInsufficient = errors.New("participant: operator elevation required")

// ErrElevationRefused is the single refusal for a failed elevation: wrong
// token, no token configured, or a session that is not live. One error for
// every cause, so a probe cannot tell them apart.
var ErrElevationRefused = errors.New("participant: elevation refused")

// OperatorTemplate is the participant an elevated session instantiates: the
// same local operator, with operator assurance and the full participant row.
var OperatorTemplate = Session{
	Scope:     authz.ParticipantScope,
	Ref:       "local-operator",
	Authority: "tangent-loopback",
	Assurance: AssuranceOperatorToken,
	Grants:    authz.DefaultParticipantCapabilities(),
}

// Elevated reports whether a session carries operator assurance.
func (s Session) Elevated() bool { return s.Assurance == AssuranceOperatorToken }

// requiresOperatorAssurance names the capabilities only an elevated session may
// exercise. `resolve` is the terminal decision — approving, denying, replying,
// acknowledging — and it is the authority the self-approval defect handed out.
func requiresOperatorAssurance(capability authz.Capability) bool {
	return capability == authz.Resolve
}

// operatorToken holds the digest of the configured token, never the token.
type operatorToken struct {
	mu         sync.RWMutex
	digest     [sha256.Size]byte
	configured bool
}

// ConfigureOperatorToken installs the operator token this gate verifies
// elevation against, and reports whether it was accepted.
//
// Only a SHA-256 digest is retained. An empty value, or one shorter than
// MinOperatorTokenBytes, leaves elevation disabled — nobody can resolve — and
// logs one warning naming the variable. The value itself is never logged.
func (g *Gate) ConfigureOperatorToken(raw string, logger *slog.Logger) bool {
	g.operator.mu.Lock()
	defer g.operator.mu.Unlock()
	if len(raw) < MinOperatorTokenBytes {
		g.operator.digest = [sha256.Size]byte{}
		g.operator.configured = false
		if logger != nil {
			logger.Warn("operator token not configured: browser approvals cannot be resolved until it is set",
				"env", EnvOperatorToken, "min_bytes", MinOperatorTokenBytes)
		}
		return false
	}
	g.operator.digest = sha256.Sum256([]byte(raw))
	g.operator.configured = true
	return true
}

// OperatorTokenConfigured reports whether elevation is possible at all.
func (g *Gate) OperatorTokenConfigured() bool {
	g.operator.mu.RLock()
	defer g.operator.mu.RUnlock()
	return g.operator.configured
}

// verifyOperatorToken compares a candidate against the configured token in
// constant time, on digests so the comparison length never depends on input.
func (g *Gate) verifyOperatorToken(candidate string) bool {
	g.operator.mu.RLock()
	defer g.operator.mu.RUnlock()
	sum := sha256.Sum256([]byte(candidate))
	match := subtle.ConstantTimeCompare(sum[:], g.operator.digest[:]) == 1
	return g.operator.configured && match
}

// Elevate verifies an operator token for the session this request carries and,
// on success, rotates it: a new session id and cookie with operator assurance,
// and the predecessor revoked in the same transaction.
//
// Every failure is ErrElevationRefused. The candidate is never retained,
// logged, or included in an error.
func (g *Gate) Elevate(ctx context.Context, w http.ResponseWriter, current Session, candidate string) (Session, error) {
	if current.ID == "" || !g.verifyOperatorToken(candidate) {
		return Session{}, ErrElevationRefused
	}
	successor, value, err := g.store.Rotate(ctx, current.ID, OperatorTemplate)
	if err != nil {
		if errors.Is(err, ErrSessionInvalid) {
			return Session{}, ErrElevationRefused
		}
		return Session{}, err
	}
	http.SetCookie(w, NewCookie(value, successor.ExpiresAt))
	return successor, nil
}

// effectiveCapabilities is the grant set a session may actually exercise: its
// stored grants, minus anything that requires operator assurance it lacks.
func effectiveCapabilities(session Session) []authz.Capability {
	out := make([]authz.Capability, 0, len(session.Grants))
	for _, capability := range session.Grants {
		if requiresOperatorAssurance(capability) && !session.Elevated() {
			continue
		}
		out = append(out, capability)
	}
	return out
}
