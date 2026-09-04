package interactionpkg_test

import (
	"context"
	"errors"
	"testing"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/interactionpkg"
)

// stubPackage is the minimum a package can be. That it fits in twenty lines is
// the point: the contract a publisher implements is narrow enough to read in
// one sitting, which is what makes "core knows nothing about the kind" a
// checkable claim rather than an aspiration.
type stubPackage struct {
	descriptor interactionpkg.Descriptor
	projection any
	schema     any
}

func (s stubPackage) Describe() interactionpkg.Descriptor { return s.descriptor }

func (s stubPackage) PresentRequest(
	context.Context, interactionpkg.StateStore, string, *envelopes.Envelope,
) (*envelopes.Envelope, error) {
	return nil, nil
}

func (s stubPackage) NormalizeResponse(
	_ context.Context, _ interactionpkg.StateStore, _ string,
	_ *envelopes.Envelope, resp *envelopes.Response,
) (*envelopes.Response, error) {
	return resp, nil
}

func (s stubPackage) ProjectState(map[string]any) any { return s.projection }
func (s stubPackage) ProjectionSchema() any           { return s.schema }

func newStub(kind, phase, projectionKey string) stubPackage {
	return stubPackage{
		descriptor: interactionpkg.Descriptor{
			Kind: kind, StatePhaseID: phase, ProjectionKey: projectionKey,
		},
		projection: map[string]any{"kind": kind},
		schema:     map[string]any{"type": "object"},
	}
}

func TestRegistryResolvesAndRefusesDuplicates(t *testing.T) {
	t.Parallel()
	registry := interactionpkg.NewRegistry()
	pkg := newStub("example.one", "one", "one")

	if err := registry.Register(pkg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := registry.Lookup("example.one"); !ok {
		t.Fatal("Lookup missed a registered package")
	}
	if err := registry.Register(pkg); !errors.Is(err, interactionpkg.ErrDuplicatePackage) {
		t.Fatalf("second Register err = %v, want ErrDuplicatePackage", err)
	}
	if err := registry.Register(nil); err == nil {
		t.Fatal("Register(nil) err = nil, want an error")
	}
	if err := registry.Register(newStub("", "", "")); err == nil {
		t.Fatal("Register of a package with no kind err = nil, want an error")
	}
}

// TestDisableAndRemoveBothFailClosed is the removal/disable contract every
// caller depends on: one code path handles both, and neither leaves a caller
// silently falling through to something that still knows the kind.
func TestDisableAndRemoveBothFailClosed(t *testing.T) {
	t.Parallel()
	registry := interactionpkg.NewRegistry()
	if err := registry.Register(newStub("example.one", "one", "one")); err != nil {
		t.Fatalf("Register: %v", err)
	}

	registry.SetEnabled("example.one", false)
	if _, ok := registry.Lookup("example.one"); ok {
		t.Fatal("Lookup served a disabled package")
	}
	// Disabled is still registered: the registry has to be able to list and
	// explain a kind it will not serve (ADR 0003 §1, §8 C7).
	if kinds := registry.Kinds(); len(kinds) != 1 || kinds[0] != "example.one" {
		t.Fatalf("Kinds() = %v, want the disabled kind still listed", kinds)
	}
	if registry.Enabled("example.one") {
		t.Fatal("Enabled reported true for a disabled kind")
	}

	registry.SetEnabled("example.one", true)
	if _, ok := registry.Lookup("example.one"); !ok {
		t.Fatal("re-enabling did not restore the package")
	}

	if removed := registry.Remove("example.one"); !removed {
		t.Fatal("Remove reported nothing removed")
	}
	if _, ok := registry.Lookup("example.one"); ok {
		t.Fatal("Lookup served a removed package")
	}
	if kinds := registry.Kinds(); len(kinds) != 0 {
		t.Fatalf("Kinds() = %v after removal, want empty", kinds)
	}
	if removed := registry.Remove("example.one"); removed {
		t.Fatal("Remove of an absent kind reported a removal")
	}
	// A nil registry is the no-packages build, and must behave as "nothing is
	// installed" rather than panicking.
	var absent *interactionpkg.Registry
	if _, ok := absent.Lookup("example.one"); ok {
		t.Fatal("a nil registry served a package")
	}
}

func TestProjectionsSkipEmptyAndDisabledPackages(t *testing.T) {
	t.Parallel()
	registry := interactionpkg.NewRegistry()
	for _, pkg := range []stubPackage{
		newStub("example.one", "one", "one"),
		newStub("example.two", "two", "two"),
		newStub("example.stateless", "", ""),
	} {
		if err := registry.Register(pkg); err != nil {
			t.Fatalf("Register %s: %v", pkg.descriptor.Kind, err)
		}
	}
	registry.SetEnabled("example.two", false)

	read := func(string) (map[string]any, bool) { return map[string]any{}, true }
	projections := registry.Projections(read)
	if _, ok := projections["one"]; !ok {
		t.Error("an enabled package contributed no projection")
	}
	if _, ok := projections["two"]; ok {
		t.Error("a disabled package contributed a projection")
	}
	if _, ok := projections[""]; ok {
		t.Error("a stateless package contributed a projection")
	}

	// Schemas are the contract, not the runtime state, so a disabled package
	// still contributes one: toggling a kind off must not reshape a tool.
	schemas := registry.ProjectionSchemas()
	if _, ok := schemas["one"]; !ok {
		t.Error("an enabled package contributed no projection schema")
	}
	if _, ok := schemas["two"]; !ok {
		t.Error("a disabled package dropped its projection schema; a runtime " +
			"toggle must not change an advertised contract")
	}
}
