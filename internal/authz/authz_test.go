package authz

import (
	"errors"
	"testing"
)

// TestParseScopeAliasesLegacySpellingsWithoutLettingCallersNameAnAuthority
// covers the whole of the ADR 0004 §3 grammar in one table, because the two
// halves are only correct together: reading a legacy spelling as the same
// caller is what makes "no data rewrite" possible, and refusing to let a
// caller-supplied prefix become an authority is what makes the cross-authority
// boundary real.
func TestParseScopeAliasesLegacySpellingsWithoutLettingCallersNameAnAuthority(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		raw       string
		canonical string
		realm     string
	}{
		{"empty is a real anonymous partition", "", "standalone-local:anonymous", "standalone-local"},
		{"bare legacy authority", "standalone-local", "standalone-local:anonymous", "standalone-local"},
		{"legacy direct-loopback alias", "direct-loopback:codex", "standalone-local:codex", "standalone-local"},
		{"legacy direct-mcp alias", "direct-mcp:codex", "standalone-local:codex", "standalone-local"},
		{"canonical local partition", "standalone-local:codex", "standalone-local:codex", "standalone-local"},
		{"bare application id is a partition", "codex", "standalone-local:codex", "standalone-local"},
		{"operator participant namespace shares the local realm", "operator:local", "operator:local", "standalone-local"},
		{"gateway keeps its own realm", "gateway:tether-1", "gateway:tether-1", "gateway:tether-1"},
		{
			"an invented prefix becomes a partition, never an authority",
			"gateway-ish:trusted", "standalone-local:gateway-ish:trusted", "standalone-local",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := Normalize(test.raw); got != test.canonical {
				t.Fatalf("Normalize(%q) = %q, want %q", test.raw, got, test.canonical)
			}
			if got := Realm(test.raw); got != test.realm {
				t.Fatalf("Realm(%q) = %q, want %q", test.raw, got, test.realm)
			}
		})
	}
}

// TestCallerScopeAssignsTheAuthorityRegardlessOfWhatTheCallerSupplied is the
// property the whole grammar rests on. A caller may choose its partition; it
// may never choose its authority, however it spells the argument.
func TestCallerScopeAssignsTheAuthorityRegardlessOfWhatTheCallerSupplied(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"":                       "standalone-local:anonymous",
		"codex":                  "standalone-local:codex",
		"standalone-local:codex": "standalone-local:codex",
		"direct-loopback:codex":  "standalone-local:codex",
		// The attack this exists to stop: a caller spelling a foreign
		// authority into its own scope argument to reach that authority's
		// objects.
		"gateway:tether-1": "standalone-local:gateway:tether-1",
		"operator:local":   "standalone-local:operator:local",
	}
	for declared, want := range tests {
		if got := CallerScope(declared); got != want {
			t.Errorf("CallerScope(%q) = %q, want %q", declared, got, want)
		}
		if Realm(CallerScope(declared)) != AuthorityStandaloneLocal {
			t.Errorf("CallerScope(%q) escaped the local realm", declared)
		}
	}
}

// TestMatrixCellsThatMustStayEmpty pins the three deliberate holes in ADR 0004
// §7. Each of them is load-bearing, and each would be easy to fill in by
// accident while adding a capability.
func TestMatrixCellsThatMustStayEmpty(t *testing.T) {
	t.Parallel()
	// A caller can never resolve: resolution is the participant's immutable
	// act, and it is the one fact Tangent is authoritative for.
	if Grants(KindCallerApplication, Resolve) {
		t.Error("a caller application may not resolve")
	}
	// A participant can never close a surface: closing dispositions other
	// callers' outstanding work.
	if Grants(KindParticipant, Close) {
		t.Error("a participant may not close a surface")
	}
	// Nothing administers in the shipped binary.
	for _, kind := range []PrincipalKind{
		KindCallerApplication, KindCallerAgent, KindParticipant,
		KindConnection, KindPluginPublisher,
	} {
		if Grants(kind, Administer) {
			t.Errorf("%s may not administer", kind)
		}
	}
	// A connection and a publisher hold nothing at all. They are in the matrix
	// so that a later change cannot grant them something by omission.
	for _, kind := range []PrincipalKind{KindConnection, KindPluginPublisher, KindCallerAgent} {
		for _, capability := range []Capability{View, Submit, Draft, Resolve, Cancel, Close, Administer} {
			if Grants(kind, capability) {
				t.Errorf("%s must hold nothing, holds %s", kind, capability)
			}
		}
	}
}

// TestDefaultParticipantCapabilitiesAreExactlyTheParticipantRow guards the
// grant a freshly minted loopback session receives.
func TestDefaultParticipantCapabilitiesAreExactlyTheParticipantRow(t *testing.T) {
	t.Parallel()
	granted := map[Capability]bool{}
	for _, capability := range DefaultParticipantCapabilities() {
		granted[capability] = true
	}
	for _, want := range []Capability{View, Draft, Resolve, Cancel} {
		if !granted[want] {
			t.Errorf("a minted session should hold %s", want)
		}
	}
	for _, withheld := range []Capability{Submit, Close, Administer} {
		if granted[withheld] {
			t.Errorf("a minted session must not hold %s", withheld)
		}
	}
}

// TestAuthorizeUsesTheTwoRefusalShapes is the error contract adapters must not
// collapse: 403 within an authority so a local user can debug it, 404 across
// authorities so a foreign authority cannot probe for existence.
func TestAuthorizeUsesTheTwoRefusalShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		request Request
		want    error
	}{
		{
			name: "same partition is allowed",
			request: Request{
				Kind: KindCallerApplication, Scope: "standalone-local:a",
				OwnerScope: "standalone-local:a", Capability: Close, Isolation: PartitionScoped,
			},
		},
		{
			name: "legacy spelling is the same partition",
			request: Request{
				Kind: KindCallerApplication, Scope: "standalone-local:codex",
				OwnerScope: "direct-loopback:codex", Capability: Close, Isolation: PartitionScoped,
			},
		},
		{
			name: "reads stay authority-wide inside standalone-local",
			request: Request{
				Kind: KindCallerApplication, Scope: "standalone-local:a",
				OwnerScope: "standalone-local:b", Capability: View, Isolation: AuthorityWide,
			},
		},
		{
			name: "close is refused across partitions, in-authority",
			request: Request{
				Kind: KindCallerApplication, Scope: "standalone-local:a",
				OwnerScope: "standalone-local:b", Capability: Close, Isolation: PartitionScoped,
			},
			want: ErrForbidden,
		},
		{
			name: "cross-authority reads are not found, not forbidden",
			request: Request{
				Kind: KindCallerApplication, Scope: "standalone-local:a",
				OwnerScope: "gateway:tether-1", Capability: View, Isolation: AuthorityWide,
			},
			want: ErrNotFound,
		},
		{
			name: "a gateway caller cannot reach a local object either",
			request: Request{
				Kind: KindCallerApplication, Scope: "gateway:tether-1",
				OwnerScope: "standalone-local:a", Capability: View, Isolation: AuthorityWide,
			},
			want: ErrNotFound,
		},
		{
			name: "an empty matrix cell is refused before the object is examined",
			request: Request{
				Kind: KindCallerApplication, Scope: "standalone-local:a",
				OwnerScope: "standalone-local:a", Capability: Resolve,
			},
			want: ErrForbidden,
		},
		{
			name: "a participant without the grant is refused",
			request: Request{
				Kind: KindParticipant, Scope: ParticipantScope,
				Granted: []Capability{View}, OwnerScope: "standalone-local:a", Capability: Resolve,
			},
			want: ErrForbidden,
		},
		{
			name: "a participant with the grant reaches a local surface",
			request: Request{
				Kind: KindParticipant, Scope: ParticipantScope,
				Granted:    DefaultParticipantCapabilities(),
				OwnerScope: "standalone-local:a", Capability: Resolve,
			},
		},
		{
			name: "a loopback participant cannot see a gateway surface at all",
			request: Request{
				Kind: KindParticipant, Scope: ParticipantScope,
				Granted:    DefaultParticipantCapabilities(),
				OwnerScope: "gateway:tether-1", Capability: View,
			},
			want: ErrNotFound,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := Authorize(test.request)
			if test.want == nil {
				if err != nil {
					t.Fatalf("Authorize = %v, want allowed", err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("Authorize = %v, want %v", err, test.want)
			}
		})
	}
}

// TestParseCapabilitiesDropsUnknownGrants: an unknown capability is never a
// grant, so a session row written by a newer build cannot widen an older one.
func TestParseCapabilitiesDropsUnknownGrants(t *testing.T) {
	t.Parallel()
	parsed := ParseCapabilities([]string{"view", "teleport", "resolve", "view"})
	if len(parsed) != 2 || parsed[0] != Resolve || parsed[1] != View {
		t.Fatalf("ParseCapabilities = %v, want sorted [resolve view]", parsed)
	}
}
