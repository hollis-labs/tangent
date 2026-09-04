package effect

import (
	"context"
	"sort"
	"strings"
)

// Root is one directory this host will mediate file effects against.
//
// A root is *registered by an authority*, never declared by a caller and never
// named by a renderer. That is the whole difference between this model and a
// path string: `browse_roots[].path` in a `tangent.file-picker` envelope is
// caller-authored display text, and after this change it stays display text
// forever — a matching `root_id` here is what makes a root mediatable, and a
// caller cannot mint one by describing it.
type Root struct {
	// ID is the key a handle refers to. It is matched against the root ids a
	// caller declared only to *label* a handle, never to authorize one.
	ID string
	// Label is what a participant sees.
	Label string
	// Path is the host-side absolute path, already symlink-resolved by the
	// authority. It is never sent to a renderer, never accepted from one, and
	// never logged.
	Path string
	// Writable admits `file.write_scoped`. Read-only is the default because a
	// root that can be written is a root that can be destroyed.
	Writable bool
	// MaxReadBytes caps a single read from this root. Zero uses
	// [DefaultMaxReadBytes].
	MaxReadBytes int64
}

// DefaultMaxReadBytes bounds one mediated read.
//
// It matches `hitl.maximumArtifactPreviewBytes` deliberately: both are "how
// much host-fetched content may cross into a browser in one response", and two
// different answers to one question is how a limit becomes advisory.
const DefaultMaxReadBytes = 64 * 1024

// ActorRef identifies an actor to an authority without handing it capability
// material. It is the same four fields `interaction.ActorBinding` carries, so
// the adapter between them is a field copy and cannot drift into a
// translation.
type ActorRef struct {
	Scope        string
	PrincipalRef string
	Authority    string
	Assurance    string
}

// Authority is the optional in-process host authority — a Cerberus Workspace
// where one is composed (ADR 0004 §10).
//
// It is deliberately an interface with no in-tree implementation. Tangent
// never calls out to a composing product to authorize a request (ADR 0004
// §10); an authority is *installed* at construction by an embedder that
// already holds the trust relationship, exactly as `PrivilegedActorPolicy` and
// `DeliveryWorkerPolicy` already are.
//
// Composition is never mandatory. With no authority installed the host
// registers no root, grants no effect capability, and holds no administrator —
// which is standalone Tangent's normal, fully-functional configuration and not
// a degraded one. Every shipped workflow works with no authority present,
// because no shipped workflow performs a host effect.
type Authority interface {
	// WorkspaceRoots returns the roots this participant may have effects
	// mediated against. Returning none is a complete answer.
	WorkspaceRoots(ctx context.Context, participantScope string) ([]Root, error)

	// GrantableCapabilities returns the effect capability ids this
	// installation is willing to grant at all. It is what populates
	// definition.HostPolicy.GrantableCapabilities, which has had no producer
	// until now.
	GrantableCapabilities(ctx context.Context) []Capability

	// AuthorizesHostPolicy reports whether an actor may exercise host-policy
	// operations — today, ExpireInteraction.
	AuthorizesHostPolicy(actor ActorRef) bool

	// AuthorizesAdministrator reports whether an actor holds ADR 0004
	// `administer`.
	AuthorizesAdministrator(actor ActorRef) bool
}

// standalone is the shipped authority: no roots, no grants, no administrator.
//
// It exists so the seam is *wired* rather than absent. ADR 0004 §Q10 assigned
// this task the choice of whether to supply a real `PrivilegedActorPolicy`,
// and the answer is: wire the composition point, keep the default deny.
// Granting `administer` needs a credential-custody decision ADR 0004 §12 puts
// out of scope, and inventing one here would be the exact "administrator
// credential" §7 forbids. What was wrong before was not the denial — it was
// that nothing constructed a policy at all, so the seam had no call site and
// no test. Now it has both, and it denies.
type standalone struct{}

// Standalone returns the authority a Tangent with nothing composed runs under.
// It is never nil, so no call site needs a nil check and no nil check can be
// forgotten.
func Standalone() Authority { return standalone{} }

func (standalone) WorkspaceRoots(context.Context, string) ([]Root, error) { return nil, nil }
func (standalone) GrantableCapabilities(context.Context) []Capability     { return nil }
func (standalone) AuthorizesHostPolicy(ActorRef) bool                     { return false }
func (standalone) AuthorizesAdministrator(ActorRef) bool                  { return false }

// PrivilegedActors adapts an [Authority] onto the two questions
// `interaction.PrivilegedActorPolicy` asks.
//
// It is defined here rather than in internal/interaction so the dependency
// runs one way: internal/effect knows what an authority is, and
// internal/interaction keeps knowing only that *someone* answers. The tiny
// binding to `interaction.ActorBinding` lives at the composition root, where
// the concrete types are already in scope.
type PrivilegedActors struct {
	authority Authority
}

// NewPrivilegedActors wraps an authority. A nil authority is [Standalone],
// which denies both questions.
func NewPrivilegedActors(authority Authority) PrivilegedActors {
	if authority == nil {
		authority = Standalone()
	}
	return PrivilegedActors{authority: authority}
}

// AuthorizeHostPolicy answers the host-policy half.
func (p PrivilegedActors) AuthorizeHostPolicy(actor ActorRef) bool {
	return p.authority.AuthorizesHostPolicy(actor)
}

// AuthorizeAdministrator answers the administrator half.
func (p PrivilegedActors) AuthorizeAdministrator(actor ActorRef) bool {
	return p.authority.AuthorizesAdministrator(actor)
}

// GrantableCapabilityIDs renders an authority's grant set in the
// `map[string]bool` shape `definition.HostPolicy` takes.
//
// Two filters apply, and both are deliberate:
//
//   - An id this build does not know is dropped. An authority that names a
//     capability Tangent has never heard of has not thereby invented one.
//   - A [MediationUnimplemented] capability is dropped even when an authority
//     grants it. There is no executor behind `process.exec`; letting a
//     definition materialize as `available` on the strength of a grant that
//     nothing can honor would make the registry lie about what it can serve.
func GrantableCapabilityIDs(ctx context.Context, authority Authority) map[string]bool {
	if authority == nil {
		return nil
	}
	granted := authority.GrantableCapabilities(ctx)
	if len(granted) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, capability := range granted {
		if !Known(capability) || MediationOf(capability) == MediationUnimplemented {
			continue
		}
		out[string(capability)] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RootSet indexes an authority's roots for lookup, and is the only thing that
// ever turns a root id into a path.
type RootSet struct {
	roots map[string]Root
}

// NewRootSet indexes roots, dropping any with an empty id or a non-absolute
// path. A root the host cannot state precisely is not a root.
func NewRootSet(roots []Root) RootSet {
	indexed := map[string]Root{}
	for _, root := range roots {
		id := strings.TrimSpace(root.ID)
		path := strings.TrimSpace(root.Path)
		if id == "" || path == "" || !isAbsolutePath(path) {
			continue
		}
		root.ID, root.Path = id, path
		if root.MaxReadBytes <= 0 {
			root.MaxReadBytes = DefaultMaxReadBytes
		}
		indexed[id] = root
	}
	return RootSet{roots: indexed}
}

// Lookup returns one root by id.
func (s RootSet) Lookup(id string) (Root, bool) {
	root, ok := s.roots[strings.TrimSpace(id)]
	return root, ok
}

// IDs lists the registered root ids, sorted. It is what a renderer may learn
// about the root set — the ids, never the paths.
func (s RootSet) IDs() []string {
	out := make([]string, 0, len(s.roots))
	for id := range s.roots {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Empty reports whether no root is registered, which is the shipped state.
func (s RootSet) Empty() bool { return len(s.roots) == 0 }
