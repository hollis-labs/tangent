// Package interactionpkg is the host side of the interaction package
// boundary: the narrow contract a publisher-owned package implements, and the
// registry Tangent resolves it through.
//
// docs/adr/0003-definition-and-package-ownership.md §5 splits ownership by
// authority rather than by repository. A definition that ships bundled with
// the Tangent release is still publisher-owned content Tangent hosts, and
// §5's "must not own" column is explicit that Tangent core owns neither
// publisher schemas and renderer semantics nor caller-owned business state.
// Before CW-20260825-0074 that split existed only in the manifest: the
// manifest declared who owned a kind while the kind's business state, its
// request projection, and its response interpretation lived in
// internal/room and internal/mcp, keyed by a wire-name switch.
//
// This package is the runtime half of the same split. A package implements
// Package; core resolves it by kind and never learns what the kind means.
// Everything crossing the boundary is either an envelope, a response, or an
// opaque map[string]any the host stores and returns without interpreting.
//
// Dependencies are deliberately minimal — stdlib plus the go-envelopes wire
// model. In particular this package does not import internal/room: the phase
// substrate reaches a package through StateStore, so a package cannot reach
// past its own state into the room lifecycle, and internal/room cannot grow a
// dependency on any package.
package interactionpkg

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	envelopes "github.com/hollis-labs/go-envelopes"
)

// ErrPackageUnavailable is returned when a kind resolves to no enabled
// package. It is the package-boundary sibling of definition.ErrorCode's
// `unavailable` state: a caller must be able to tell "this host does not ship
// that package" from "that package rejected your payload" (ADR 0003 §8 C7).
var ErrPackageUnavailable = errors.New("interactionpkg: package unavailable")

// ErrDuplicatePackage is returned when two packages claim one kind.
// Registration is not idempotent, for the same reason go-envelopes rejects a
// duplicate type name: a boot-time call site that registers twice is a wiring
// bug, not a state to reconcile.
var ErrDuplicatePackage = errors.New("interactionpkg: kind already has a package")

// ErrInvalidState is what a package wraps its own validation refusals in.
//
// It exists so core can map any package's refusal onto the upstream
// `validation-failed` code without naming a single publisher error sentinel.
// Before this boundary, internal/mcp's phase-state error mapper was a
// 40-arm errors.Is chain over every workflow's typed errors, which meant
// adding a kind meant editing core's error taxonomy.
var ErrInvalidState = errors.New("interactionpkg: invalid package state")

// StateStore is the workflow-neutral phase substrate a package persists
// through.
//
// It is deliberately map[string]any in both directions. Core stores and
// returns opaque publisher-owned JSON and never learns what a key means,
// which is what keeps ADR 0003 §5's "must not own caller-owned business
// state" enforceable by inspection rather than by review discipline. The
// concrete implementation is one adapter over room.Manager's existing
// PhaseOutput blob; no migration and no new table is involved.
type StateStore interface {
	// LoadState returns the package's persisted blob for one room. The bool
	// distinguishes "no state yet" from "empty state", which a package needs
	// in order to tell a first turn from a cleared one.
	LoadState(ctx context.Context, roomID, phaseID string) (map[string]any, bool, error)
	// SaveState replaces the package's blob for one room.
	SaveState(ctx context.Context, roomID, phaseID string, data map[string]any) error
}

// Descriptor is the static identity of one package's runtime binding.
type Descriptor struct {
	// Kind is the wire name this package serves. It matches the `kind` its
	// manifest declares; the registry checks nothing else about it, because
	// the manifest is the authority on identity and this is only the lookup
	// key.
	Kind string
	// StatePhaseID is the phase-output key the package's state lives under.
	// Empty means the package keeps no room state, in which case core never
	// calls LoadState or SaveState on its behalf.
	StatePhaseID string
	// ProjectionKey is the JSON field name the package's state appears under
	// in the tangent.session_get result. It is authored by the package rather
	// than derived from Kind because the shipped kinds' field names predate
	// this boundary and ADR 0003 §8 C6 freezes them.
	ProjectionKey string
}

// Package is the runtime half of one interaction package.
//
// Every method is optional in effect — a package that only needs a renderer
// and a schema returns nil, nil from the projections and normalizers and the
// generic path behaves exactly as it did before. What a package must not do
// is require core to know anything about it beyond this interface.
type Package interface {
	// Describe returns the package's static binding.
	Describe() Descriptor

	// PresentRequest merges an inbound request into publisher-owned state and
	// returns the envelope the participant should see. Returning nil presents
	// the request envelope unchanged, which is the correct answer for a
	// stateless kind.
	//
	// This is where a kind's request projection lives — the logic that used
	// to be a per-kind block inside the MCP tool handler.
	PresentRequest(
		ctx context.Context,
		store StateStore,
		roomID string,
		env *envelopes.Envelope,
	) (*envelopes.Envelope, error)

	// NormalizeResponse validates and normalizes one participant response
	// before it can become an immutable resolution. Returning an error
	// rejects the submission without terminalizing the interaction, matching
	// roomflow.Normalizer's contract exactly.
	//
	// Returning the response unchanged is valid and is what a kind with no
	// response interpretation does.
	NormalizeResponse(
		ctx context.Context,
		store StateStore,
		roomID string,
		env *envelopes.Envelope,
		resp *envelopes.Response,
	) (*envelopes.Response, error)

	// ProjectState renders the package's persisted blob for tangent.session_get.
	// A nil result omits the field, which is the shipped `omitempty` behavior.
	ProjectState(state map[string]any) any

	// ProjectionSchema is the JSON Schema for what ProjectState returns. Core
	// composes it into session_get's advertised output schema so a package can
	// own its projection type without moving that frozen tool's contract
	// (ADR 0003 §8 C3, C6). Returning nil contributes no property.
	ProjectionSchema() any
}

// Registry resolves a wire name to the package that serves it.
//
// Disable and Remove exist because ADR 0003 §1 requires `registered` never to
// imply `available`, and because a boundary that cannot be un-crossed has not
// been proven. A disabled kind stays listed and explains itself; a removed
// one is gone. Both fail closed: Lookup reports absence and the caller
// surfaces ErrPackageUnavailable rather than silently falling through to a
// core default that knows the kind.
type Registry struct {
	mu       sync.RWMutex
	packages map[string]Package
	disabled map[string]bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		packages: map[string]Package{},
		disabled: map[string]bool{},
	}
}

// Register installs one package. A nil package, an empty kind, or a second
// package for one kind is an error.
func (r *Registry) Register(pkg Package) error {
	if pkg == nil {
		return fmt.Errorf("interactionpkg: package is nil")
	}
	descriptor := pkg.Describe()
	if descriptor.Kind == "" {
		return fmt.Errorf("interactionpkg: package declares no kind")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.packages[descriptor.Kind]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicatePackage, descriptor.Kind)
	}
	r.packages[descriptor.Kind] = pkg
	return nil
}

// Remove uninstalls a package entirely. It reports whether one was present.
func (r *Registry) Remove(kind string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, existed := r.packages[kind]
	delete(r.packages, kind)
	delete(r.disabled, kind)
	return existed
}

// SetEnabled turns one kind's package on or off without uninstalling it. It
// mirrors definition.HostPolicy.DisabledKinds, which does the same for the
// definition, so an operator can disable both halves of a kind coherently.
func (r *Registry) SetEnabled(kind string, enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if enabled {
		delete(r.disabled, kind)
		return
	}
	r.disabled[kind] = true
}

// Lookup returns the enabled package for a kind. A registered-but-disabled
// kind reports false, so every caller's fail-closed path is the same one.
func (r *Registry) Lookup(kind string) (Package, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.disabled[kind] {
		return nil, false
	}
	pkg, ok := r.packages[kind]
	return pkg, ok
}

// Kinds returns every registered kind, enabled or not, sorted. Disabled kinds
// are included because the registry must be able to list and explain a kind it
// will not serve.
func (r *Registry) Kinds() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	kinds := make([]string, 0, len(r.packages))
	for kind := range r.packages {
		kinds = append(kinds, kind)
	}
	r.mu.RUnlock()
	sort.Strings(kinds)
	return kinds
}

// Enabled reports whether a kind has an installed and enabled package.
func (r *Registry) Enabled(kind string) bool {
	_, ok := r.Lookup(kind)
	return ok
}

// StateReader supplies one room's persisted phase blobs to Projections. It is
// a function rather than an interface so core can pass a closure over an
// already-loaded PhaseState without another round trip.
type StateReader func(phaseID string) (map[string]any, bool)

// Projections renders every enabled package's session_get projection from one
// room's phase state, keyed on each package's ProjectionKey. A package with no
// state phase, or whose ProjectState returns nil, contributes nothing.
func (r *Registry) Projections(read StateReader) map[string]any {
	if r == nil || read == nil {
		return nil
	}
	r.mu.RLock()
	installed := make([]Package, 0, len(r.packages))
	for kind, pkg := range r.packages {
		if r.disabled[kind] {
			continue
		}
		installed = append(installed, pkg)
	}
	r.mu.RUnlock()

	var out map[string]any
	for _, pkg := range installed {
		descriptor := pkg.Describe()
		if descriptor.ProjectionKey == "" || descriptor.StatePhaseID == "" {
			continue
		}
		state, _ := read(descriptor.StatePhaseID)
		projection := pkg.ProjectState(state)
		if projection == nil {
			continue
		}
		if out == nil {
			out = map[string]any{}
		}
		out[descriptor.ProjectionKey] = projection
	}
	return out
}

// ProjectionSchemas returns each registered package's projection schema keyed
// on its ProjectionKey. Disabled packages are included: session_get's
// advertised output schema is a contract, and it must not change shape when an
// operator toggles a kind off at runtime.
func (r *Registry) ProjectionSchemas() map[string]any {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out map[string]any
	for _, pkg := range r.packages {
		descriptor := pkg.Describe()
		if descriptor.ProjectionKey == "" {
			continue
		}
		schema := pkg.ProjectionSchema()
		if schema == nil {
			continue
		}
		if out == nil {
			out = map[string]any{}
		}
		out[descriptor.ProjectionKey] = schema
	}
	return out
}
