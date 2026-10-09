package extensions

import (
	"bytes"
	"context"
	"errors"
	"testing"

	envelopes "github.com/hollis-labs/libs/ui-go/envelopes"

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
//
// The contributed loop walks an empty set today (CW-20260911-0036 moved
// `tangent.app-board` to RegisterAll, where it belonged all along). It stays
// because it is written against the table rather than against an inventory: the
// first kind this host genuinely does not own lands in the drift gates by being
// marked, with nothing here to remember to change. What holds the door itself
// while no kind sets the mark is contributedDoorFixture below.
func registerEveryShippedKind(t *testing.T, svc *envelope.Service) {
	t.Helper()
	if err := RegisterAll(svc); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	for _, kind := range PluginContributedTypes() {
		if err := RegisterContributedKind(svc, kind); err != nil {
			t.Fatalf("RegisterContributedKind(%s): %v", kind, err)
		}
	}
}

// contributedDoorFixture is the shipped registration table with one row moved
// to the plugin door: `tangent.dashboard`, which really ships, really has a
// manifest, and in production really arrives through RegisterAll.
//
// It exists because CW-20260911-0036 left the ADR 0007 §4 door with no shipped
// user, and a door whose success path stops being exercised is a door that
// rots. AGENTS.md is explicit that both doors stay inside the drift tests or
// ADR 0003 §6's ownership guarantee narrows to half the registry; the answer to
// "there is nothing to walk" is a fixture, not a smaller assertion.
//
// The fixture is a value, not a swap of the package-level table: these tests
// run in parallel, and a door proven by mutating global state would be proving
// it under conditions production never has. Every branch below resolves a real
// manifest out of the real embedded package tree, so what is fixtured is which
// row carries the mark — nothing about how the door behaves once it does.
func contributedDoorFixture(t *testing.T) []registration {
	t.Helper()
	fixture := make([]registration, len(registrations))
	copy(fixture, registrations)
	for i := range fixture {
		if fixture[i].name == DashboardEnvelopeType {
			fixture[i].contributedByPlugin = true
			return fixture
		}
	}
	t.Fatalf("%s is no longer in the registration table; the door fixture needs a "+
		"shipped kind to stand in for a contributed one", DashboardEnvelopeType)
	return nil
}

// TestTheContributedKindDoorStillOpens is the ADR 0007 §4 success path, held
// with a fixture because no shipped kind goes through it.
//
// It asserts the two halves that have to move together: registerAll leaves a
// marked row alone, and registerContributedKind installs exactly that row —
// from its real manifest, with the definition the registry ends up holding
// being the one the package tree authored. A door that registered *something*
// would pass a weaker version of this test.
func TestTheContributedKindDoorStillOpens(t *testing.T) {
	t.Parallel()
	fixture := contributedDoorFixture(t)

	if got := pluginContributedTypes(fixture); len(got) != 1 || got[0] != DashboardEnvelopeType {
		t.Fatalf("pluginContributedTypes(fixture) = %v, want [%s]", got, DashboardEnvelopeType)
	}

	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if regErr := registerAll(fixture, svc); regErr != nil {
		t.Fatalf("registerAll: %v", regErr)
	}
	if _, found := svc.Lookup(DashboardEnvelopeType); found {
		t.Fatal("registerAll installed a marked row; production would then register it twice")
	}

	if doorErr := registerContributedKind(fixture, svc, DashboardEnvelopeType); doorErr != nil {
		t.Fatalf("registerContributedKind: %v", doorErr)
	}
	spec, found := svc.Lookup(DashboardEnvelopeType)
	if !found {
		t.Fatal("the door reported success and the kind is not in the registry")
	}
	if spec.Name != DashboardEnvelopeType {
		t.Errorf("registered name = %q, want %q", spec.Name, DashboardEnvelopeType)
	}
	// The manifest is what the door resolved, not bytes a caller handed it.
	material, held := svc.LookupDefinitionMaterial(DashboardEnvelopeType)
	if !held {
		t.Fatal("the door registered a kind with no definition material; the manifest did not resolve")
	}
	authored, err := PackageManifest(DashboardEnvelopeType)
	if err != nil {
		t.Fatalf("PackageManifest: %v", err)
	}
	if !bytes.Equal(material.Manifest, authored) {
		t.Error("the definition the door installed is not the one the package tree ships")
	}
}

// TestTheContributedKindDoorRefusesAnUnmarkedRowInTheSameTable is the fixture's
// other half, and the reason the fixture is a whole table rather than one row:
// the refusal has to hold for a kind sitting *beside* a contributable one.
//
// Without it, "unmarked rows are refused" could pass for the accidental reason
// that no row in the table is marked at all — which, on the production table,
// is now true.
func TestTheContributedKindDoorRefusesAnUnmarkedRowInTheSameTable(t *testing.T) {
	t.Parallel()
	fixture := contributedDoorFixture(t)
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	err = registerContributedKind(fixture, svc, AppBoardEnvelopeType)
	if !errors.Is(err, ErrNotContributable) {
		t.Fatalf("err = %v, want ErrNotContributable", err)
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
