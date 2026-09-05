package authz

import (
	"errors"
	"sort"
	"strings"
)

// Capability is one object-access power from ADR 0004 §2. Capabilities are
// always evaluated against a named object — a surface, or an interaction on a
// surface. There is no global grant except Administer.
//
// These are deliberately a different namespace from the host-mediated *effect*
// capabilities a renderer declares (ADR 0003 §2.5). The two never substitute
// for one another and must not be merged.
type Capability string

const (
	// View reads a surface or interaction projection, its definition binding,
	// its state, and its terminal outcome.
	View Capability = "view"
	// Submit creates an interaction on a surface, mutates caller-owned surface
	// workflow state, and opens a surface under a given owner scope.
	Submit Capability = "submit"
	// Draft writes an immutable draft revision and acknowledges a presented
	// projection revision. Never terminal.
	Draft Capability = "draft"
	// Resolve submits a terminal response at pinned revisions.
	Resolve Capability = "resolve"
	// Cancel terminates a nonterminal interaction with an authorized cause, or
	// supersedes one with a replacement.
	Cancel Capability = "cancel"
	// Close terminates a surface and dispositions every outstanding
	// interaction on it under the named surface policy.
	Close Capability = "close"
	// Administer is every capability on every object, plus expiry, plus
	// participant-session revocation. Nothing holds it in the shipped binary.
	Administer Capability = "administer"
)

// PrincipalKind is one row of the ADR 0004 §7 matrix.
type PrincipalKind string

const (
	// KindCallerApplication is the software product or adapter that submits
	// work — an MCP client, a gateway adapter, a script, or the Tangent SPA's
	// own browser API.
	KindCallerApplication PrincipalKind = "caller_application"
	// KindCallerAgent is the agent turn or human the caller acts for.
	// Attribution only in v0.x; it holds nothing of its own.
	KindCallerAgent PrincipalKind = "caller_agent"
	// KindParticipant is the person whose input Tangent presents and captures.
	KindParticipant PrincipalKind = "participant"
	// KindConnection is one client attachment. It holds no capability and
	// grants none; it is named so nothing later grants it something.
	KindConnection PrincipalKind = "connection"
	// KindPluginPublisher owns a definition. Publisher identity is provenance,
	// not request authority.
	KindPluginPublisher PrincipalKind = "plugin_publisher"
	// KindAdministrator is the host operator. Deny-by-default and unwired;
	// CW-20260825-0077 supplies the real PrivilegedActorPolicy.
	KindAdministrator PrincipalKind = "administrator"
)

// defaultGrants is the ADR 0004 §7 matrix, verbatim.
//
// Three cells are deliberately empty and must stay empty: a caller never
// resolves, a participant never closes, and nothing administers in the shipped
// binary.
var defaultGrants = map[PrincipalKind]map[Capability]bool{
	KindCallerApplication: {
		View: true, Submit: true, Cancel: true, Close: true,
	},
	KindCallerAgent:     {},
	KindParticipant:     {View: true, Draft: true, Resolve: true, Cancel: true},
	KindConnection:      {},
	KindPluginPublisher: {},
	KindAdministrator: {
		View: true, Submit: true, Draft: true, Cancel: true, Close: true, Administer: true,
	},
}

// DefaultParticipantCapabilities is the set a freshly minted loopback
// participant session receives: exactly the Participant human row. It does not
// include Close, which stays a caller and administrator power, and it does not
// include Administer.
func DefaultParticipantCapabilities() []Capability {
	return []Capability{View, Draft, Resolve, Cancel}
}

// Grants reports whether a principal kind holds a capability at all, before
// any object is considered. An empty matrix cell can never be reached by an
// object rule.
func Grants(kind PrincipalKind, capability Capability) bool {
	return defaultGrants[kind][capability]
}

// Isolation selects how strictly an operation compares the principal's
// partition against the object's, within one authority.
type Isolation int

const (
	// AuthorityWide allows any caller of the same authority. It is what the
	// legacy `session_list` and `session_get` compatibility vocabulary keeps
	// (ADR 0004 §9), and it is honest precisely because `standalone-local`
	// partitions are advisory.
	AuthorityWide Isolation = iota
	// PartitionScoped requires the same partition. Destructive operations use
	// it — closing dispositions another partition's pending human work — even
	// though the same partition is advisory for reads.
	PartitionScoped
)

// Errors. The two shapes are the ADR 0004 §5 error contract and adapters must
// not collapse them.
var (
	// ErrForbidden is a refusal within the requester's own authority. It maps
	// to 403: existence leakage to the local user is not a threat this product
	// defends against, and a 403 is far easier to debug.
	ErrForbidden = errors.New("authz: principal is not authorized for this object")

	// ErrNotFound is a refusal across authorities. It maps to 404 so a foreign
	// authority cannot probe for existence.
	ErrNotFound = errors.New("authz: object not found")
)

// Request is one authorization question.
//
// It names a principal, an object, and the capability being exercised. It
// carries no headers, no payload, and no session material: the session id is
// capability material and never reaches a decision input (ADR 0004 §6.1).
type Request struct {
	// Kind selects the matrix row.
	Kind PrincipalKind
	// Scope is the principal's resolved scope. For a caller it is
	// host-assigned; for a participant it is the scope its session was minted
	// with.
	Scope string
	// Granted is the participant session's capability set. It is consulted
	// only for KindParticipant; a caller's powers come from the matrix row and
	// object ownership, never from a stored grant.
	Granted []Capability
	// OwnerScope is the object's owner or caller scope.
	OwnerScope string
	// Capability is the power being exercised.
	Capability Capability
	// Isolation selects authority-wide or partition-scoped comparison.
	Isolation Isolation
}

// Authorize is the single decision. Every transport — MCP tools, the browser
// room API, the HITL browser API, and the WebSocket frame dispatch — routes
// its refusal through here so the matrix is enforced in one place.
//
// The order matters:
//
//  1. The matrix row is consulted first. An empty cell is refused before any
//     object is examined, so a principal can never reach a capability it does
//     not hold by owning the right object.
//  2. Cross-authority is refused as not-found. The authority prefix is
//     host-assigned, so this boundary is real.
//  3. Within an authority the partition rule for this operation applies.
func Authorize(request Request) error {
	if !Grants(request.Kind, request.Capability) {
		return ErrForbidden
	}
	if request.Kind == KindAdministrator {
		// Administer is the one global grant. Nothing holds it in the shipped
		// binary — PrivilegedActorPolicy stays deny-by-default until
		// CW-20260825-0077 — so this branch exists to keep the matrix honest,
		// not to be reachable.
		return nil
	}
	if request.Kind == KindParticipant {
		if !holds(request.Granted, request.Capability) {
			return ErrForbidden
		}
		// A participant reaches an object through the session it is bound to,
		// never by matching a caller scope. A loopback session is bound to the
		// local realm; a `gateway:*`-owned object needs a participant binding
		// an identity authority established (ADR 0004 §10.2), which the
		// shipped binary never composes, and must not be distinguishable from
		// an object that does not exist.
		if !SameAuthority(request.Scope, request.OwnerScope) {
			return ErrNotFound
		}
		return nil
	}
	if !SameAuthority(request.Scope, request.OwnerScope) {
		return ErrNotFound
	}
	if request.Isolation == PartitionScoped && !SamePartition(request.Scope, request.OwnerScope) {
		return ErrForbidden
	}
	return nil
}

func holds(granted []Capability, capability Capability) bool {
	for _, held := range granted {
		if held == capability {
			return true
		}
	}
	return false
}

// ParseCapabilities decodes a stored capability set, dropping anything this
// build does not know. An unknown capability is never a grant.
func ParseCapabilities(values []string) []Capability {
	known := map[Capability]bool{
		View: true, Submit: true, Draft: true, Resolve: true,
		Cancel: true, Close: true, Administer: true,
	}
	seen := map[Capability]bool{}
	capabilities := make([]Capability, 0, len(values))
	for _, value := range values {
		capability := Capability(strings.TrimSpace(value))
		if !known[capability] || seen[capability] {
			continue
		}
		seen[capability] = true
		capabilities = append(capabilities, capability)
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i] < capabilities[j] })
	return capabilities
}

// FormatCapabilities encodes a capability set for storage.
func FormatCapabilities(capabilities []Capability) []string {
	values := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		values = append(values, string(capability))
	}
	sort.Strings(values)
	return values
}
