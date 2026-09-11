package plugins

import (
	"context"
	"log/slog"
	"slices"
	"testing"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
)

// TestShippedPluginsContributeExactlyTheRegisteredKinds is the join between the
// two halves of the ADR 0007 §4 path.
//
// extensions' registration table says which kinds are meant to arrive through
// the plugin door. This package says which plugins ship. Nothing else compares
// them, so without this a kind could be marked contributedByPlugin and have no
// plugin that contributes it — registered nowhere, absent from the running
// registry, and still counted by every drift gate that reads the table.
//
// Both sides are empty today: since CW-20260911-0036 this build ships no
// plugin-contributed kind, because the one it had was never one. The first
// assertion is therefore vacuous and the second is not — every kind in
// RegisteredTypes has to be in the registry after both doors have run, which is
// now the whole registry through RegisterAll. The comparison stays because it
// is written against the table: the day a genuinely foreign kind is marked,
// this is what fails if no plugin contributes it.
func TestShippedPluginsContributeExactlyTheRegisteredKinds(t *testing.T) {
	t.Parallel()
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if regErr := extensions.RegisterAll(svc); regErr != nil {
		t.Fatalf("RegisterAll: %v", regErr)
	}

	host, err := LoadShipped(context.Background(), slog.New(slog.DiscardHandler), svc)
	if err != nil {
		t.Fatalf("LoadShipped: %v", err)
	}

	want := extensions.PluginContributedTypes()
	slices.Sort(want)
	got := host.ContributedKinds()
	if !slices.Equal(got, want) {
		t.Fatalf("shipped plugins contributed %v, the registration table declares %v", got, want)
	}

	// And the whole shipped registry is now present, both doors together.
	for _, kind := range extensions.RegisteredTypes() {
		if _, found := svc.Lookup(kind); !found {
			t.Errorf("%s is in RegisteredTypes but reached the registry through neither door", kind)
		}
	}
}

// TestEveryShippedPluginHasAnIDAndVersion. The id is what the host keys on and
// what appears in boot logs; an empty one is refused at Load, and finding that
// out here beats finding it out at boot.
func TestEveryShippedPluginHasAnIDAndVersion(t *testing.T) {
	t.Parallel()
	shipped := Shipped()
	if len(shipped) == 0 {
		t.Fatal("no plugins ship: the ADR 0007 §4 path has no consumer in this build")
	}
	seen := map[string]bool{}
	for _, p := range shipped {
		if p.ID() == "" {
			t.Error("a shipped plugin has no id")
			continue
		}
		if seen[p.ID()] {
			t.Errorf("%s ships twice", p.ID())
		}
		seen[p.ID()] = true
		if p.Version() == "" {
			t.Errorf("%s has no version", p.ID())
		}
	}
}
