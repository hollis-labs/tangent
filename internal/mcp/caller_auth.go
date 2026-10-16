package mcp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/interaction"
)

// EnvTrustedForwarders opts configured producers into session attribution.
const EnvTrustedForwarders = "TANGENT_MCP_TRUSTED_FORWARDERS"

// EnvCallerTokens contains comma-separated producer=SHA256(token) entries.
// The server stores only digests. Operators provision the bearer values separately.
const EnvCallerTokens = "TANGENT_MCP_TOKENS" // #nosec G101 -- environment variable name, not a credential.

type callerCredential struct {
	producer string
	digest   [sha256.Size]byte
}

// CallerAuth is an immutable credential set. A nil or empty set permits only
// unmarked direct loopback callers; remote and proxied requests fail closed.
type CallerAuth struct {
	credentials       []callerCredential
	trustedForwarders map[string]bool
}

var producerName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// ParseCallerAuth validates configuration without exposing any supplied values
// in errors. Duplicate names or digests would make identity ambiguous. Trusted
// forwarders must name configured producers and default to none.
func ParseCallerAuth(config, forwarders string) (*CallerAuth, error) {
	auth := &CallerAuth{trustedForwarders: map[string]bool{}}
	if strings.TrimSpace(config) == "" {
		return auth, auth.parseTrustedForwarders(forwarders)
	}
	names := map[string]bool{}
	digests := map[[sha256.Size]byte]bool{}
	for index, entry := range strings.Split(config, ",") {
		name, encoded, found := strings.Cut(strings.TrimSpace(entry), "=")
		if !found || !producerName.MatchString(name) {
			return nil, fmt.Errorf("%s: entry %d has invalid producer syntax", EnvCallerTokens, index+1)
		}
		raw, err := hex.DecodeString(encoded)
		if err != nil || len(raw) != sha256.Size {
			return nil, fmt.Errorf("%s: entry %d requires a SHA-256 hex digest", EnvCallerTokens, index+1)
		}
		digest := [sha256.Size]byte(raw)
		if names[name] || digests[digest] {
			return nil, fmt.Errorf("%s: entry %d duplicates a producer or digest", EnvCallerTokens, index+1)
		}
		names[name], digests[digest] = true, true
		auth.credentials = append(auth.credentials, callerCredential{producer: name, digest: digest})
	}
	return auth, auth.parseTrustedForwarders(forwarders)
}

// WithCallerAuth installs the host's verified producer credentials before any
// transport is constructed. In-process SDK callers retain local compatibility.
func WithCallerAuth(auth *CallerAuth) Option {
	return func(server *Server) error {
		server.callerAuth = auth
		return nil
	}
}

type verifiedCallerKey struct{}

func verifiedCaller(ctx context.Context) (interaction.ActorBinding, bool) {
	binding, ok := ctx.Value(verifiedCallerKey{}).(interaction.ActorBinding)
	return binding, ok
}

func (a *CallerAuth) authenticate(r *http.Request) (string, bool) {
	values, supplied := r.Header["Authorization"]
	if !supplied {
		return "", directLoopback(r)
	}
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	digest := sha256.Sum256([]byte(parts[1]))
	producer := ""
	if a != nil {
		// Compare every fixed-length digest, without an early match return.
		for _, credential := range a.credentials {
			if subtle.ConstantTimeCompare(digest[:], credential.digest[:]) == 1 {
				producer = credential.producer
			}
		}
	}
	return producer, producer != ""
}

func directLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return false
	}
	// Host and Origin are not peer identity: Caddy rewrites both. Its
	// reviewed configuration overwrites X-Tangent-Via, and any forwarding
	// evidence also requires authentication. Even an empty marker counts.
	for name := range r.Header {
		name = strings.ToLower(name)
		if name == "x-tangent-via" || name == "forwarded" || name == "x-real-ip" || strings.HasPrefix(name, "x-forwarded-") {
			return false
		}
	}
	return true
}

func (a *CallerAuth) wrap(next func(http.ResponseWriter, *http.Request, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		producer, ok := a.authenticate(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tangent-mcp"`)
			http.Error(w, "MCP authentication required", http.StatusUnauthorized)
			return
		}
		scope := ""
		if producer != "" {
			attribution, valid := a.forwardedCaller(r.Header, producer)
			if !valid {
				http.Error(w, "Invalid forwarded caller context", http.StatusBadRequest)
				return
			}
			principal := producer
			if attribution.SessionID != "" {
				principal += "/session:" + attribution.SessionID
			}
			scope = authz.AuthorityGateway + ":" + principal
			binding := interaction.ActorBinding{
				Scope: scope, Authority: authz.AuthorityGateway,
				PrincipalRef: principal, Assurance: "adapter-verified",
			}
			ctx := context.WithValue(r.Context(), verifiedCallerKey{}, binding)
			if attribution.SessionID != "" {
				ctx = interaction.WithForwarderAttribution(ctx, attribution)
			}
			r = r.Clone(ctx)
		} else {
			r = r.Clone(r.Context())
		}
		// Downstream consumers receive only host-derived context. Neither
		// bearer values nor raw identity assertions reach the SDK/plugins.
		r.Header.Del("Authorization")
		stripForwardedIdentity(r.Header)
		next(w, r, scope)
	})
}
