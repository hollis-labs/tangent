package interaction

import (
	"context"
	"testing"

	"github.com/hollis-labs/tangent/internal/effect"
)

// TestTheShippedPrivilegedActorPolicyDenies is ADR 0004 §Q10's decision,
// asserted.
//
// The decision this task made was "wire the seam, keep the default deny", and
// the half worth testing is the seam: before this, nothing constructed a
// policy at all, so `denyPrivilegedActors{}` was reached by fallback and the
// composition point had no call site. A test that only checked the denial
// would have passed against the old code too.
func TestTheShippedPrivilegedActorPolicyDenies(t *testing.T) {
	t.Parallel()
	policy := NewEffectPrivilegedActorPolicy(effect.Standalone())

	// It satisfies the interface the service takes, which is the wiring claim.
	var _ PrivilegedActorPolicy = policy

	actor := ActorBinding{
		Scope:        "standalone-local:tests",
		PrincipalRef: "local-operator",
		Authority:    "standalone-local",
		Assurance:    "loopback-unverified",
	}
	if policy.AuthorizeHostPolicy(actor) {
		t.Error("the shipped policy authorized a host-policy operation")
	}
	if policy.AuthorizeAdministrator(actor) {
		t.Error("the shipped policy authorized an administrator")
	}
	// A nil authority is standalone, not permissive: a wiring mistake must
	// fail closed.
	if nilPolicy := NewEffectPrivilegedActorPolicy(nil); nilPolicy.AuthorizeAdministrator(actor) {
		t.Error("a nil authority authorized an administrator")
	}
}

// TestAComposedAuthorityReachesThePolicy proves the seam carries something. A
// composition point only ever exercised with the deny answer has not been
// tested.
func TestAComposedAuthorityReachesThePolicy(t *testing.T) {
	t.Parallel()
	policy := NewEffectPrivilegedActorPolicy(grantingAuthority{})
	actor := ActorBinding{Scope: "gateway:workspace", Authority: "gateway", Assurance: "adapter-verified"}
	if !policy.AuthorizeHostPolicy(actor) || !policy.AuthorizeAdministrator(actor) {
		t.Fatal("a composed authority's answer did not reach the policy")
	}
	// And the actor arrives intact, so the adapter is a field copy rather than
	// a translation that could drift.
	seen := &recordingAuthority{}
	NewEffectPrivilegedActorPolicy(seen).AuthorizeAdministrator(actor)
	if seen.actor.Scope != actor.Scope || seen.actor.PrincipalRef != actor.PrincipalRef ||
		seen.actor.Authority != actor.Authority || seen.actor.Assurance != actor.Assurance {
		t.Fatalf("the authority saw %+v, want the binding's four fields", seen.actor)
	}
}

type grantingAuthority struct{}

func (grantingAuthority) WorkspaceRoots(context.Context, string) ([]effect.Root, error) {
	return nil, nil
}
func (grantingAuthority) GrantableCapabilities(context.Context) []effect.Capability { return nil }
func (grantingAuthority) AuthorizesHostPolicy(effect.ActorRef) bool                 { return true }
func (grantingAuthority) AuthorizesAdministrator(effect.ActorRef) bool              { return true }

type recordingAuthority struct{ actor effect.ActorRef }

func (r *recordingAuthority) WorkspaceRoots(context.Context, string) ([]effect.Root, error) {
	return nil, nil
}
func (r *recordingAuthority) GrantableCapabilities(context.Context) []effect.Capability { return nil }
func (r *recordingAuthority) AuthorizesHostPolicy(actor effect.ActorRef) bool {
	r.actor = actor
	return false
}
func (r *recordingAuthority) AuthorizesAdministrator(actor effect.ActorRef) bool {
	r.actor = actor
	return false
}
