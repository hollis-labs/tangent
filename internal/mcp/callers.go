package mcp

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	libauth "github.com/hollis-labs/libs/security/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/childkeys"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/roomflow"
)

// This file is the single place a verified principal becomes a caller.
//
// The Substrate host has already verified the credential and checked that it
// holds the scope the tool is registered with; what is left is the question
// every durable record asks of its caller: which application is this, and under
// which authority. The answer is derived from the credential, never from the
// request:
//
//   - A key whose name is a configured gateway producer (`tether-gateway`, a
//     machine's own gateway) is `gateway:<name>`, assurance `adapter-verified`.
//     A producer also listed as a trusted forwarder may attest the session it
//     forwards for, in the headers it has always used; the attribution is
//     evidence and never changes authority.
//   - Any other key is `standalone-local:<name>`, assurance `key-verified`.
//     The key's name is the application id, so the partition a historical item
//     was written under stays visible to the same client under the same name.
//   - A request with no principal (an in-process call, a test) is the
//     anonymous partition, as it always was.
//
// What this ends: a caller choosing its own partition. The `caller.scope`,
// `requester_scope` and `owner_scope` arguments are still accepted so the
// shipped schemas do not break, but with a verified credential their value is
// ignored; the credential's name decides.

const (
	forwardedUserHeader    = "X-Forwarded-User-Id"
	forwardedSessionHeader = "X-Tether-Session-Id"
)

// AssuranceKeyVerified is the assurance of a local caller identified by a
// Substrate API key.
const AssuranceKeyVerified = "key-verified"

var (
	canonicalSessionID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	callerName         = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

// Callers is the host's configuration of who counts as a gateway.
type Callers struct {
	// Gateway names the keys that are gateway producers.
	Gateway []string
	// Forwarders names the gateway producers trusted to attest the session
	// they forward for. Each must also be in Gateway.
	Forwarders []string
}

type callerSet struct {
	gateway    map[string]bool
	forwarders map[string]bool
}

func newCallerSet(c Callers) (*callerSet, error) {
	set := &callerSet{gateway: map[string]bool{}, forwarders: map[string]bool{}}
	for _, name := range c.Gateway {
		if !callerName.MatchString(name) || set.gateway[name] || strings.HasPrefix(name, childkeys.NamePrefix) {
			return nil, fmt.Errorf("mcp: gateway producer %q is not a distinct caller name", name)
		}
		set.gateway[name] = true
	}
	for _, name := range c.Forwarders {
		if !set.gateway[name] || set.forwarders[name] {
			return nil, fmt.Errorf("mcp: trusted forwarder %q must be a distinct gateway producer", name)
		}
		set.forwarders[name] = true
	}
	return set, nil
}

// WithCallers sets which keys are gateway producers and which may forward
// session attribution. The default is none: every key is a local caller.
func WithCallers(c Callers) Option {
	return func(server *Server) error {
		set, err := newCallerSet(c)
		if err != nil {
			return err
		}
		server.callers = set
		return nil
	}
}

type (
	verifiedCallerKey struct{}
	localCallerKey    struct{}
)

// verifiedCaller is the gateway binding the middleware stamped, if any. The
// name is the old one on purpose: every use of it asks "was this caller
// verified as an adapter", which is still the question.
func verifiedCaller(ctx context.Context) (interaction.ActorBinding, bool) {
	binding, ok := ctx.Value(verifiedCallerKey{}).(interaction.ActorBinding)
	return binding, ok
}

// localCaller is the standalone-local binding derived from a key's name.
func localCaller(ctx context.Context) (interaction.ActorBinding, bool) {
	binding, ok := ctx.Value(localCallerKey{}).(interaction.ActorBinding)
	return binding, ok
}

// keyName turns a credential's display name into a caller name. A name that
// cannot be one is reported not ok, and the caller is anonymous rather than
// being given a spelling the operator did not choose.
func keyName(display string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(display))
	return name, callerName.MatchString(name)
}

// callerMiddleware stamps the caller derived from the request's principal. It
// runs first, so every later middleware and every handler sees the same one.
func (s *Server) callerMiddleware(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		principal, ok := libauth.FromContext(ctx)
		if !ok || !principal.Verified || principal.Kind == "user" {
			return next(ctx, method, req)
		}
		name, valid := keyName(principal.Display)
		if !valid {
			return next(ctx, method, req)
		}
		if strings.HasPrefix(name, childkeys.NamePrefix) {
			// A plugin child's key (minted by the host, internal/childkeys): the child stays in the
			// unlabeled partition the in-process plugin host has, so a child moving from a loopback
			// caller to a key moves no data.
			return next(ctx, method, req)
		}
		if s.callers != nil && s.callers.gateway[name] {
			var headers http.Header
			if extra := req.GetExtra(); extra != nil {
				headers = extra.Header
			}
			attribution, valid := s.callers.forwarded(headers, name)
			if !valid {
				return nil, fmt.Errorf("invalid forwarded caller context")
			}
			ref := name
			if attribution.SessionID != "" {
				ref += "/session:" + attribution.SessionID
			}
			ctx = context.WithValue(ctx, verifiedCallerKey{}, interaction.ActorBinding{
				Scope: authz.AuthorityGateway + ":" + ref, Authority: authz.AuthorityGateway,
				PrincipalRef: ref, Assurance: "adapter-verified",
			})
			if attribution.SessionID != "" {
				ctx = interaction.WithForwarderAttribution(ctx, attribution)
			}
			return next(ctx, method, req)
		}
		local := roomflow.CallerFor(name)
		local.PrincipalRef = name
		local.Assurance = AssuranceKeyVerified
		return next(context.WithValue(ctx, localCallerKey{}, local), method, req)
	}
}

// forwarded reads a trusted producer's attestation of the session it forwards
// for. Only a producer configured as a forwarder is believed; an attestation
// that is malformed is refused whole.
func (c *callerSet) forwarded(headers http.Header, producer string) (interaction.ForwarderAttribution, bool) {
	var out interaction.ForwarderAttribution
	if c == nil || !c.forwarders[producer] {
		return out, true
	}
	user, userPresent := headers[http.CanonicalHeaderKey(forwardedUserHeader)]
	session, sessionPresent := headers[http.CanonicalHeaderKey(forwardedSessionHeader)]
	if !userPresent && !sessionPresent {
		return out, true
	}
	if len(user) != 1 || len(session) != 1 || !canonicalSessionID.MatchString(session[0]) || user[0] != "session:"+session[0] {
		return out, false
	}
	out.Forwarder, out.SessionID = producer, session[0]
	for name, dest := range map[string]*string{
		"X-Tether-Agent-Urn":        &out.AgentURN,
		"X-Tether-Workstream-Id":    &out.WorkstreamID,
		"X-Tether-Launch-Id":        &out.LaunchID,
		"X-Tether-Project-Id":       &out.ProjectID,
		"X-Tether-Logical-Agent-Id": &out.LogicalAgentID,
	} {
		values, present := headers[http.CanonicalHeaderKey(name)]
		if !present {
			continue
		}
		if len(values) != 1 || !boundedForwardedLabel(values[0]) {
			return interaction.ForwarderAttribution{}, false
		}
		*dest = values[0]
	}
	return out, true
}

func boundedForwardedLabel(value string) bool {
	if len(value) == 0 || len(value) > 512 {
		return false
	}
	for i := range len(value) {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}
