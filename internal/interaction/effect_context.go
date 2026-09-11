package interaction

import (
	"context"
	"fmt"

	"github.com/hollis-labs/tangent/internal/effect"
)

// ResolveEffectContext returns the pinned definition binding whose granted
// capabilities govern one interaction's host-mediated effects, and the scope
// that owns it.
//
// It is the seam between ADR 0003's definition binding and CW-20260825-0077's
// effect broker, and it lives here because this package already owns the pin.
// `*Service` therefore satisfies `server.EffectContextResolver` directly, with
// no adapter at the composition root that could drift from it.
//
// Three properties are deliberate:
//
//   - **The binding comes from the record, never from the request.** ADR 0003
//     §3 makes `binding_digest` the pin; an effect is evaluated against the
//     exact definition the interaction was submitted under, not against
//     whatever the registry serves today.
//
//   - **`required` and `granted` are both returned.** They are two facts — the
//     publisher asked, and this host answered — and the broker reports them as
//     two different refusals.
//
//   - **No authorization happens here.** This resolves a binding; the broker
//     decides. Splitting the lookup from the decision is what keeps the single
//     gate single.
func (s *Service) ResolveEffectContext(
	ctx context.Context,
	interactionID string,
) (effect.Binding, string, error) {
	if interactionID == "" {
		return effect.Binding{}, "", fmt.Errorf("%w: interaction id is required", ErrInvalidRecord)
	}
	record, err := s.store.GetInteraction(ctx, interactionID)
	if err != nil {
		return effect.Binding{}, "", err
	}
	// The trust class comes from the pin too, and the isolation is derived
	// from it rather than stored beside it. That is deliberate: storing both
	// would let a record exist whose class and isolation disagree, and the
	// mapping is a host decision (definition.IsolationFor) that must be the
	// same one materialization applied. A pinned class this build no longer
	// implements resolves to `external-surface` — the position with no
	// Tangent-granted authority — so an unrecognized pin never buys the main
	// origin by omission.
	isolation := record.Definition.RendererIsolation
	return effect.Binding{
		Kind:          record.Definition.Kind,
		Version:       record.Definition.Version,
		BindingDigest: record.Definition.Digest,
		Required:      effect.CapabilitiesFromManifestJSON(record.Definition.RequiredCapabilities),
		Granted:       effect.CapabilitiesFromManifestJSON(record.Definition.GrantedCapabilities),
		Isolation:     effect.Isolation(isolation),
	}, record.CallerScope, nil
}

// EffectPrivilegedActorPolicy binds [PrivilegedActorPolicy] to a host
// authority — a Cerberus Workspace where one is composed (ADR 0004 §10).
//
// ADR 0004 §Q10 left the choice of whether to supply a real policy to
// CW-20260825-0077. The choice made there, and implemented here, is: **wire
// the seam, keep the default deny.**
//
// What was wrong before was not the denial. Granting `administer` needs a
// credential-custody decision ADR 0004 §12 puts out of scope, and inventing
// one here would be exactly the "administrator credential" ADR 0004 §7 forbids
// — nothing in a standalone Tangent should hold it. What was wrong was that
// `NewService` fell back to `denyPrivilegedActors{}` because no call site ever
// constructed a policy, so the composition point had no wiring and no test,
// and the first embedder to need one would have had to discover the interface
// from the source.
//
// Now the shipped binary constructs this, over [effect.Standalone], and it
// denies. An embedder replaces the authority and nothing else changes.
type EffectPrivilegedActorPolicy struct {
	actors effect.PrivilegedActors
}

// NewEffectPrivilegedActorPolicy wraps a host authority. A nil authority is
// the standalone one, which denies both questions.
func NewEffectPrivilegedActorPolicy(authority effect.Authority) EffectPrivilegedActorPolicy {
	return EffectPrivilegedActorPolicy{actors: effect.NewPrivilegedActors(authority)}
}

// AuthorizeHostPolicy asks the authority about host-policy operations —
// today, ExpireInteraction.
func (p EffectPrivilegedActorPolicy) AuthorizeHostPolicy(actor ActorBinding) bool {
	return p.actors.AuthorizeHostPolicy(effectActor(actor))
}

// AuthorizeAdministrator asks the authority about ADR 0004 `administer`.
func (p EffectPrivilegedActorPolicy) AuthorizeAdministrator(actor ActorBinding) bool {
	return p.actors.AuthorizeAdministrator(effectActor(actor))
}

// effectActor is a field copy rather than a translation, so the two shapes
// cannot drift into meaning different things.
func effectActor(actor ActorBinding) effect.ActorRef {
	return effect.ActorRef{
		Scope:        actor.Scope,
		PrincipalRef: actor.PrincipalRef,
		Authority:    actor.Authority,
		Assurance:    actor.Assurance,
	}
}
