package extensions

import (
	"context"
	"errors"
	"testing"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/envelope"
)

// TestRegisterAllRegistersEveryTangentKind pins the invariant the codegen
// pipeline depends on: RegisterAll installs exactly the kinds it advertises,
// and nothing else reaches the registry under the Tangent plugin id. If a
// kind is added to a Register* function but not to the registrations table,
// the server and cmd/tangent-dump-types would see different registries —
// which is the drift ADR 0003 §9 S1 exists to prevent.
func TestRegisterAllRegistersEveryTangentKind(t *testing.T) {
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	coreCount := svc.Len()

	registerEveryShippedKind(t, svc)

	names := RegisteredTypes()
	if len(names) == 0 {
		t.Fatal("RegisteredTypes returned no kinds")
	}
	if got, want := svc.Len(), coreCount+len(names); got != want {
		t.Fatalf("registry size after RegisterAll = %d, want %d", got, want)
	}

	for _, name := range names {
		spec, ok := svc.Lookup(name)
		if !ok {
			t.Errorf("kind %q advertised by RegisteredTypes but not registered", name)
			continue
		}
		if spec.PluginID != PluginID {
			t.Errorf("kind %q registered under plugin %q, want %q", name, spec.PluginID, PluginID)
		}
		if spec.Source != envelopes.TypeSourcePlugin {
			t.Errorf("kind %q has source %s, want plugin", name, spec.Source)
		}
	}
}

// TestRegisterAllRetainsSchemaMaterial guards the other half of the codegen
// contract: go-envelopes discards schema source after compiling, so
// cmd/tangent-dump-types can only emit TypeScript for a Tangent kind if the
// service retained its registration bytes. A kind registered by some path
// that skips Service.RegisterTypeFromManifest would silently drop out of the
// generated types instead of failing the build.
func TestRegisterAllRetainsSchemaMaterial(t *testing.T) {
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	registerEveryShippedKind(t, svc)

	for _, name := range RegisteredTypes() {
		material, ok := svc.LookupDefinitionMaterial(name)
		if !ok {
			t.Errorf("kind %q retained no registration material", name)
			continue
		}
		if len(material.Manifest) == 0 {
			t.Errorf("kind %q retained an empty manifest", name)
		}
		if len(material.RequestSchema) == 0 {
			t.Errorf("kind %q retained an empty request schema", name)
		}
	}
}

// TestRegisterAllRejectsNilService keeps the boot-time call site honest: a
// nil service is a programming error, not a no-op registration.
func TestRegisterAllRejectsNilService(t *testing.T) {
	if err := RegisterAll(nil); err == nil {
		t.Fatal("RegisterAll(nil) = nil, want error")
	}
}

// registerEveryShippedKind installs the whole shipped registry through both
// doors: RegisterAll for host-package kinds, and RegisterContributedKind for
// the ones a plugin contributes under ADR 0007 §4.
//
// Production reaches the second door through internal/pluginhost, which is
// where the manifest requirement is enforced. This package cannot import it —
// pluginhost imports this one — so the tests here call the door directly and
// internal/pluginhost's own tests hold the check that sits in front of it.
//
// The alternative, testing only what RegisterAll installs, would quietly
// exclude every plugin-contributed kind from the drift gates. ADR 0007 §4 names
// that specific narrowing as the thing not to let happen.
func registerEveryShippedKind(t *testing.T, svc *envelope.Service) {
	t.Helper()
	if err := RegisterAll(svc); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	contributed := PluginContributedTypes()
	if len(contributed) == 0 {
		t.Fatal("no plugin-contributed kinds: the ADR 0007 §4 door has no shipped consumer, " +
			"so nothing here would notice if it broke")
	}
	for _, kind := range contributed {
		if err := RegisterContributedKind(svc, kind); err != nil {
			t.Fatalf("RegisterContributedKind(%s): %v", kind, err)
		}
	}
}

// TestContributedKindDoorRefusesWhatItShould holds the two refusals that make
// RegisterContributedKind a boundary rather than a second registration helper.
func TestContributedKindDoorRefusesWhatItShould(t *testing.T) {
	t.Parallel()

	t.Run("a kind this host ships no manifest for", func(t *testing.T) {
		svc, err := envelope.New(context.Background())
		if err != nil {
			t.Fatalf("envelope.New: %v", err)
		}
		err = RegisterContributedKind(svc, "tangent.not-a-shipped-kind")
		if !errors.Is(err, ErrNotContributable) {
			t.Fatalf("err = %v, want ErrNotContributable", err)
		}
	})

	t.Run("a host-package kind RegisterAll owns", func(t *testing.T) {
		svc, err := envelope.New(context.Background())
		if err != nil {
			t.Fatalf("envelope.New: %v", err)
		}
		// tangent.dashboard ships a manifest and is emphatically registrable —
		// just not through this door. A kind reachable by both paths could be
		// registered twice, and no test could say which path production used.
		err = RegisterContributedKind(svc, DashboardEnvelopeType)
		if !errors.Is(err, ErrNotContributable) {
			t.Fatalf("err = %v, want ErrNotContributable", err)
		}
	})
}

// TestRegisterAllSkipsPluginContributedKinds is the other half: RegisterAll
// must leave the plugin door's kinds alone, or booting the host would try to
// register them a second time and fail.
func TestRegisterAllSkipsPluginContributedKinds(t *testing.T) {
	t.Parallel()
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if regErr := RegisterAll(svc); regErr != nil {
		t.Fatalf("RegisterAll: %v", regErr)
	}
	for _, kind := range PluginContributedTypes() {
		if _, found := svc.Lookup(kind); found {
			t.Errorf("RegisterAll installed %s, which arrives through the plugin host", kind)
		}
	}
}
