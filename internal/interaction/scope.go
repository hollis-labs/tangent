package interaction

import (
	"strings"

	"github.com/hollis-labs/tangent/internal/authz"
)

// This file is the durable substrate's half of ADR 0004 §3: every scope
// comparison in the service goes through it, and every scope-keyed query goes
// through equivalentScopes.
//
// Two rules, and they are separate on purpose:
//
//   - Comparison is normalized. A record written as `direct-loopback:codex`
//     and a caller resolved to `standalone-local:codex` are the same caller,
//     read through the fixed alias, with no data rewrite.
//   - Cross-authority denial is `not found`, in-authority denial is
//     `unauthorized`. A foreign authority must not be able to probe for
//     existence; a local caller gets the error it can actually debug.

// scopeMatches reports whether two scope strings name the same caller after
// alias normalization.
func scopeMatches(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return authz.SamePartition(a, b)
}

// crossAuthority reports whether a requester and an object belong to different
// authorities. This is the boundary that is genuinely enforced, because the
// authority prefix is host-assigned and no caller can spell it.
func crossAuthority(requesterScope, ownerScope string) bool {
	if requesterScope == "" || ownerScope == "" {
		return false
	}
	return !authz.SameAuthority(requesterScope, ownerScope)
}

// denial returns the refusal shape ADR 0004 §5 requires for one comparison:
// ErrNotFound across authorities so existence does not leak, ErrUnauthorized
// within one so a local user can debug it.
//
// The message is fixed in both cases. An authorization failure must never name
// the required capability, the owning scope, the session, or the participant.
func denial(requesterScope, ownerScope string) error {
	if crossAuthority(requesterScope, ownerScope) {
		return ErrNotFound
	}
	return ErrUnauthorized
}

// equivalentScopes returns every persisted spelling that resolves to the same
// caller, canonical form first.
//
// Scope-keyed lookups — idempotency in particular — must find rows written
// before this grammar existed. Migration 0003 backfilled every legacy room and
// envelope at bare `standalone-local`, and the HITL adapter wrote
// `direct-loopback:<app>` for two releases. Widening the read is the whole of
// the compatibility story: nothing rewrites those rows, and new writes use the
// canonical form only.
func equivalentScopes(scope string) []string {
	parsed := authz.ParseScope(scope)
	canonical := parsed.String()
	spellings := []string{canonical}
	appendUnique := func(value string) {
		if value == "" || value == canonical {
			return
		}
		for _, existing := range spellings {
			if existing == value {
				return
			}
		}
		spellings = append(spellings, value)
	}
	if parsed.IsStandaloneLocal() {
		appendUnique("direct-loopback:" + parsed.Partition)
		appendUnique(parsed.Partition)
		if parsed.Partition == authz.PartitionAnonymous {
			appendUnique(authz.AuthorityStandaloneLocal)
			appendUnique("direct-loopback:")
		}
	}
	// A caller that supplied a raw string this host reassigned still needs its
	// own prior rows: the raw value was what was persisted before the grammar.
	appendUnique(strings.TrimSpace(scope))
	return spellings
}

// scopePlaceholders renders the `?, ?, …` list for an IN clause over
// equivalentScopes, plus the arguments to bind.
func scopePlaceholders(scope string) (string, []any) {
	spellings := equivalentScopes(scope)
	arguments := make([]any, 0, len(spellings))
	for _, spelling := range spellings {
		arguments = append(arguments, spelling)
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(spellings)), ","), arguments
}
