// Package authz holds Tangent's object-access authorization model: the scope
// grammar, the seven capabilities, and the single decision function every
// transport routes a refusal through.
//
// It is deliberately a leaf package. It imports nothing from the rest of
// Tangent so that internal/interaction, internal/mcp, internal/server, and
// internal/ws can all reach the same decision without any of them owning it.
//
// The model is ADR 0004. Nothing here re-decides any of it.
package authz

import "strings"

// Authorities. The authority half of a caller scope is assigned by the
// receiving adapter from admission facts and is never caller-supplied.
const (
	// AuthorityStandaloneLocal is every direct loopback caller with no
	// verified adapter binding.
	AuthorityStandaloneLocal = "standalone-local"

	// AuthorityGateway prefixes a caller scope established by a trusted
	// in-process adapter that authenticated its upstream.
	AuthorityGateway = "gateway"

	// AuthorityOperator is the participant namespace. It is not a caller
	// authority: a participant reaches an object through its session, never by
	// matching a caller scope.
	AuthorityOperator = "operator"

	// ParticipantScope is the scope every loopback participant session is
	// minted with. It is the spelling internal/hitl has recorded since the
	// inbox shipped; ADR 0004 §4.3 keeps the scope and makes the *session* the
	// identity, so two browsers on one machine get two sessions and two audit
	// trails while both act as this operator.
	ParticipantScope = AuthorityOperator + ":local"
)

// PartitionAnonymous is the partition a caller that declares no application id
// receives. It is a real partition, not a missing one.
const PartitionAnonymous = "anonymous"

// legacyAuthorityAliases maps persisted authority spellings onto their
// canonical form. Reads go through the alias; nothing rewrites stored data.
//
// ADR 0004 §3.5: `direct-loopback:<app>` reads as `standalone-local:<app>`,
// and the two pre-grammar spellings the MCP adapters wrote (`direct-mcp`,
// `tangent-loopback` as a scope prefix) are the same unverified local caller
// under a different name.
var legacyAuthorityAliases = map[string]string{
	"direct-loopback": AuthorityStandaloneLocal,
	"direct-mcp":      AuthorityStandaloneLocal,
}

// Scope is a parsed caller or participant scope: `<authority>:<partition>`.
type Scope struct {
	Authority string
	Partition string
}

// String renders the canonical spelling.
func (s Scope) String() string {
	if s.Authority == "" {
		return ""
	}
	return s.Authority + ":" + s.Partition
}

// IsStandaloneLocal reports whether this scope is the unverified local caller
// authority, whose partitions are advisory rather than a security boundary.
func (s Scope) IsStandaloneLocal() bool { return s.Authority == AuthorityStandaloneLocal }

// ParseScope resolves one persisted or wire-supplied scope string to its
// canonical parsed form, applying the fixed read-time aliases.
//
// The rules, in order:
//
//   - Empty reads as `standalone-local:anonymous`. A caller that supplied
//     nothing is a real local caller with no declared application id.
//   - A bare token with no separator is an authority only when it is a known
//     one; `standalone-local` therefore reads as `standalone-local:anonymous`.
//     Anything else bare is a partition of the local authority, which is what
//     an application id supplied without a prefix means.
//   - `gateway:<binding>` and `operator:<name>` keep their authority.
//   - A legacy authority is aliased; every other prefix is a caller-invented
//     string and must not be allowed to name an authority, so the whole value
//     becomes a partition of `standalone-local`.
func ParseScope(raw string) Scope {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Scope{Authority: AuthorityStandaloneLocal, Partition: PartitionAnonymous}
	}
	authority, partition, found := strings.Cut(trimmed, ":")
	if !found {
		if trimmed == AuthorityStandaloneLocal {
			return Scope{Authority: AuthorityStandaloneLocal, Partition: PartitionAnonymous}
		}
		return Scope{Authority: AuthorityStandaloneLocal, Partition: normalizePartition(trimmed)}
	}
	if alias, ok := legacyAuthorityAliases[authority]; ok {
		authority = alias
	}
	switch authority {
	case AuthorityStandaloneLocal, AuthorityGateway, AuthorityOperator:
		return Scope{Authority: authority, Partition: normalizePartition(partition)}
	default:
		// A prefix this host does not assign is not an authority. Keeping the
		// whole string as a partition means a caller can neither invent an
		// authority nor collide with one, which is exactly the guarantee §3
		// rests the cross-authority boundary on.
		return Scope{Authority: AuthorityStandaloneLocal, Partition: normalizePartition(trimmed)}
	}
}

// normalizePartition trims a declared application id to its stored form. An
// empty partition is the anonymous one, never a missing value.
func normalizePartition(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return PartitionAnonymous
	}
	return trimmed
}

// Normalize returns the canonical spelling of one scope string.
func Normalize(raw string) string { return ParseScope(raw).String() }

// CallerScope assigns the host-derived caller scope for a direct loopback
// caller that declared applicationID.
//
// The authority is assigned here and cannot be influenced by the argument: a
// caller that spells "gateway:trusted" as its application id lands in
// `standalone-local:gateway:trusted`, a partition, not an authority.
func CallerScope(applicationID string) string {
	return Scope{
		Authority: AuthorityStandaloneLocal,
		Partition: normalizePartition(declaredPartition(applicationID)),
	}.String()
}

// declaredPartition extracts the partition a caller declared, from either a
// bare application id or a full scope string a v0.12 caller still sends.
//
// Wire arguments named `caller.scope`, `requester_scope`, and `owner_scope`
// stay accepted so shipped schemas do not break (ADR 0004 §3.2), but only
// their partition half survives: the authority is the host's to assign.
func declaredPartition(declared string) string {
	trimmed := strings.TrimSpace(declared)
	if trimmed == "" {
		return PartitionAnonymous
	}
	authority, partition, found := strings.Cut(trimmed, ":")
	if !found {
		return trimmed
	}
	if alias, ok := legacyAuthorityAliases[authority]; ok {
		authority = alias
	}
	if authority == AuthorityStandaloneLocal {
		return partition
	}
	return trimmed
}

// Realm collapses a scope onto the boundary that is actually enforced.
//
// ADR 0004 §3 names two caller authorities, `standalone-local` and
// `gateway:<binding_id>`, and the cross-authority rule is about those. The
// `operator:*` participant namespace is not a third authority — it is the
// local participant of the same single-user host — so it shares the local
// realm. Collapsing it here is what keeps a cross-application `hitl_get` a
// 403 (the inbox is shared, its items are not) rather than a 404, while a
// `gateway:*`-owned object stays indistinguishable from one that does not
// exist.
func Realm(scope string) string {
	parsed := ParseScope(scope)
	if parsed.Authority == AuthorityGateway {
		return AuthorityGateway + ":" + parsed.Partition
	}
	return AuthorityStandaloneLocal
}

// SameAuthority reports whether two scopes fall inside the same enforced
// boundary. It is the only comparison that is genuinely a security boundary,
// because the authority prefix is host-assigned and no caller can spell it.
func SameAuthority(a, b string) bool { return Realm(a) == Realm(b) }

// SamePartition reports whether two scopes are identical after normalization.
//
// Within `standalone-local` this is an accident-avoidance check, not a
// security boundary: any local caller can assert any partition. See
// [AdvisoryPartitionNotice].
func SamePartition(a, b string) bool { return Normalize(a) == Normalize(b) }

// AdvisoryPartitionNotice is the sentence every tool description, doc, and
// error path that mentions a `standalone-local` partition has to keep true.
//
// It is exported so the statement lives in exactly one place and drifts in
// none: ADR 0004 §3.4 makes documenting the honest strength part of the
// decision rather than an implementation nicety.
const AdvisoryPartitionNotice = "standalone-local partitions are advisory, not a security boundary: " +
	"any local caller can assert any partition, and isolation is enforced only across authorities."
