package extensions

import (
	"context"
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

	if regErr := RegisterAll(svc); regErr != nil {
		t.Fatalf("RegisterAll: %v", regErr)
	}

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
	if regErr := RegisterAll(svc); regErr != nil {
		t.Fatalf("RegisterAll: %v", regErr)
	}

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
