// Package packages is the shipped interaction-package set: the one list that
// says which publisher-owned packages this Tangent build hosts.
//
// It is the runtime sibling of internal/envelope/extensions' `registrations`
// table, which says which definitions this build registers. The two are
// deliberately separate lists, because they answer different questions and a
// kind can legitimately appear in one and not the other: `tangent.hitl-item`
// registers a definition and ships no room-workflow package, and a kind may
// register a definition whose behavior is entirely generic and needs no
// package at all — which is the state the other sixteen room-backed kinds are
// still in at the end of CW-20260825-0074.
//
// Removing a package is deleting its directory and its line below. Nothing in
// internal/room, internal/mcp, or internal/envelope names any of them, which is
// the claim TestRemovingThePackageLeavesCoreIntact exists to hold.
package packages

import (
	"fmt"

	"github.com/hollis-labs/tangent/internal/interactionpkg"
	"github.com/hollis-labs/tangent/internal/packages/formcollect"
)

// RegisterAll installs every shipped interaction package on reg. It is the
// only entry point production code should use; the per-package constructors
// stay exported for tests that deliberately boot a partial set.
func RegisterAll(reg *interactionpkg.Registry) error {
	if reg == nil {
		return fmt.Errorf("packages: registry is nil")
	}
	for _, pkg := range []interactionpkg.Package{
		formcollect.New(),
	} {
		if err := reg.Register(pkg); err != nil {
			return fmt.Errorf("packages: %w", err)
		}
	}
	return nil
}

// NewRegistry returns a registry with every shipped package installed.
func NewRegistry() (*interactionpkg.Registry, error) {
	reg := interactionpkg.NewRegistry()
	if err := RegisterAll(reg); err != nil {
		return nil, err
	}
	return reg, nil
}
