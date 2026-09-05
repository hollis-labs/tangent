package effect

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/authz"
)

// TestTheTwoCapabilityNamespacesAreDisjoint is the reconciliation, asserted.
//
// ADR 0003 §2.5 and ADR 0004 §2 describe two namespaces that "never substitute
// for one another". The Go type system already refuses the substitution — an
// authz.Capability cannot be passed where an effect.Capability is expected —
// but a type check does not stop someone from spelling an effect capability
// `resolve`, and a shared spelling is how two namespaces become one by
// accident five refactors later.
//
// So the identifier sets are held apart by name as well as by type.
func TestTheTwoCapabilityNamespacesAreDisjoint(t *testing.T) {
	t.Parallel()
	objectAccess := map[string]bool{}
	for _, capability := range []authz.Capability{
		authz.View, authz.Submit, authz.Draft, authz.Resolve,
		authz.Cancel, authz.Close, authz.Administer,
	} {
		objectAccess[string(capability)] = true
	}
	for _, capability := range Capabilities() {
		if objectAccess[string(capability)] {
			t.Errorf("effect capability %q collides with an object-access capability", capability)
		}
		// Every effect capability is namespaced, which the manifest validator
		// already requires of a publisher (internal/definition/manifest.go).
		// The host's own catalog is held to the same rule, or the validator is
		// enforcing a convention the host does not follow.
		if !strings.Contains(string(capability), ".") {
			t.Errorf("effect capability %q is not namespaced", capability)
		}
	}
}

// TestEveryEffectNamesAnObjectPrecondition holds the bridge honest. An effect
// with no object-access precondition would be a capability that grants an
// external act to a principal that may not even view the object.
func TestEveryEffectNamesAnObjectPrecondition(t *testing.T) {
	t.Parallel()
	for _, capability := range Capabilities() {
		precondition := ObjectPrecondition(capability)
		if precondition == "" {
			t.Errorf("%s names no object-access precondition", capability)
		}
		if !authz.Grants(authz.KindAdministrator, precondition) {
			t.Errorf("%s requires %q, which no principal in the matrix holds", capability, precondition)
		}
	}
}

// TestProcessExecIsRefusedTwiceOver documents the belt-and-braces on the one
// capability that must never fire. Neither rule is a fallback for the other:
// removing the precondition would still leave no executor, and shipping an
// executor would still leave `administer` unheld.
func TestProcessExecIsRefusedTwiceOver(t *testing.T) {
	t.Parallel()
	if got := ObjectPrecondition(ProcessExec); got != authz.Administer {
		t.Errorf("process.exec precondition = %q, want %q", got, authz.Administer)
	}
	if got := MediationOf(ProcessExec); got != MediationUnimplemented {
		t.Errorf("process.exec mediation = %q, want %q", got, MediationUnimplemented)
	}
	// Nothing holds `administer` in the shipped binary (ADR 0004 §7), so the
	// precondition alone already refuses it for every principal that exists.
	for _, kind := range []authz.PrincipalKind{
		authz.KindCallerApplication, authz.KindCallerAgent, authz.KindParticipant,
		authz.KindConnection, authz.KindPluginPublisher,
	} {
		if authz.Grants(kind, authz.Administer) {
			t.Errorf("%s holds administer; process.exec's first refusal is gone", kind)
		}
	}
}

// TestAnUnknownCapabilityIsNeverAGrant mirrors authz.ParseCapabilities. A
// capability id this build does not recognize must not survive a round trip
// through storage and reappear as something a handle covers.
func TestAnUnknownCapabilityIsNeverAGrant(t *testing.T) {
	t.Parallel()
	parsed := ParseCapabilities([]string{
		"file.read_scoped", "file.destroy_everything", "resolve", "", "file.read_scoped",
	})
	if len(parsed) != 1 || parsed[0] != FileReadScoped {
		t.Fatalf("ParseCapabilities = %v, want exactly [file.read_scoped]", parsed)
	}
	if Known("resolve") {
		t.Error("`resolve` is known to the effect namespace; the two namespaces have merged")
	}
	if got := ObjectPrecondition("file.destroy_everything"); got != authz.Administer {
		t.Errorf("unknown capability precondition = %q, want administer (unreachable)", got)
	}
}

// TestStandaloneGrantsNothing is the shipped configuration, asserted. ADR 0004
// §10 requires composition to be optional; the default must therefore be a
// complete, working answer that grants no effect at all.
func TestStandaloneGrantsNothing(t *testing.T) {
	t.Parallel()
	authority := Standalone()
	roots, err := authority.WorkspaceRoots(context.Background(), authz.ParticipantScope)
	if err != nil || len(roots) != 0 {
		t.Fatalf("standalone roots = %v, %v; want none", roots, err)
	}
	if ids := GrantableCapabilityIDs(context.Background(), authority); len(ids) != 0 {
		t.Fatalf("standalone grants %v; want nothing", ids)
	}
	policy := NewPrivilegedActors(authority)
	actor := ActorRef{Scope: authz.ParticipantScope, Authority: "operator", Assurance: "loopback-unverified"}
	if policy.AuthorizeHostPolicy(actor) || policy.AuthorizeAdministrator(actor) {
		t.Error("standalone PrivilegedActors authorized something; it must deny both")
	}
	if nilPolicy := NewPrivilegedActors(nil); nilPolicy.AuthorizeAdministrator(actor) {
		t.Error("a nil authority must behave as standalone, not as permissive")
	}
}

// TestAnUnimplementedCapabilityIsNeverGrantable stops a composed authority
// from making the definition registry lie. Granting `process.exec` would let a
// definition materialize as `available` on the strength of a grant nothing can
// honor.
func TestAnUnimplementedCapabilityIsNeverGrantable(t *testing.T) {
	t.Parallel()
	ids := GrantableCapabilityIDs(context.Background(), generousAuthority{
		grants: []Capability{ProcessExec, FileReadScoped, "invented.capability"},
	})
	if ids[string(ProcessExec)] {
		t.Error("process.exec is grantable; a grant with no executor reached host policy")
	}
	if ids["invented.capability"] {
		t.Error("an unknown capability became grantable")
	}
	if !ids[string(FileReadScoped)] {
		t.Error("file.read_scoped was dropped; a composed authority cannot grant anything")
	}
}

type generousAuthority struct {
	grants []Capability
	roots  []Root
}

func (a generousAuthority) WorkspaceRoots(context.Context, string) ([]Root, error) {
	return a.roots, nil
}
func (a generousAuthority) GrantableCapabilities(context.Context) []Capability { return a.grants }
func (generousAuthority) AuthorizesHostPolicy(ActorRef) bool                   { return true }
func (generousAuthority) AuthorizesAdministrator(ActorRef) bool                { return true }
