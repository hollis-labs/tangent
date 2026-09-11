package definition

import (
	"testing"
	"time"
)

// The trust-class model, tested as a model rather than a lookup table.
//
// The three properties worth holding are the ones a future edit is most likely
// to break by accident: the ceilings are strictly ordered, the shapes and the
// classes agree in both directions, and the evidence gate refuses material that
// did not come through the release.

func trustManifest(mutate func(*Manifest)) *Manifest {
	manifest := &Manifest{
		ManifestVersion:   "1.0.0",
		Publisher:         TangentPublisher,
		Kind:              "tangent.example",
		Version:           "1.0",
		Revision:          1,
		PackageID:         "tangent.example",
		PackageVersion:    "1.0.0",
		OwnershipClass:    OwnershipHostPackage,
		RequestSchemaRef:  "request.schema.json",
		ResponseKind:      "data",
		ResponseSchemaRef: "response.schema.json",
		Renderer: Renderer{
			ID:        "tangent.renderer.example",
			Class:     RendererReactComponent,
			Entry:     "components/envelopes/Example#Example",
			Isolation: IsolationMainOrigin,
		},
		CompatibleHostVersions:     ">=0.0.0 <2.0.0",
		CompatibleProtocolVersions: ">=1 <2",
		CompatibilityClass:         CompatibilityAdditive,
		DraftCustody:               DraftCustodyDisabled,
		SensitivityDefault:         SensitivityNormal,
		InlinePayloadLimitBytes:    1024,
		Trust:                      Trust{Assurance: AssuranceInTreeBuild},
	}
	if mutate != nil {
		mutate(manifest)
	}
	return manifest
}

func trustMaterial() Material {
	return Material{
		ManifestSource: []byte("kind: tangent.example\n"),
		RequestSchema:  []byte(`{"type":"object"}`),
		ResponseSchema: []byte(`{"type":"object"}`),
		SourceLocator:  "packages/tangent.example",
	}
}

func trustPolicy(grantable ...string) HostPolicy {
	grants := map[string]bool{}
	for _, id := range grantable {
		grants[id] = true
	}
	return HostPolicy{
		HostVersion:           "v0.12.0",
		ProtocolVersion:       "1",
		GrantableCapabilities: grants,
		Now:                   func() time.Time { return time.Unix(0, 0).UTC() },
	}
}

func TestEveryIsolationHasAProfileAndEveryProfileHasAnIsolation(t *testing.T) {
	t.Parallel()
	isolations := Isolations()
	if len(isolations) != len(isolationProfiles) {
		t.Fatalf("Isolations lists %d, the table has %d", len(isolations), len(isolationProfiles))
	}
	for _, isolation := range isolations {
		profile, ok := IsolationProfileFor(isolation)
		if !ok {
			t.Fatalf("%s has no profile", isolation)
		}
		if !isolation.valid() {
			t.Errorf("%s is in the profile table but not in the enum", isolation)
		}
		if profile.Isolation != isolation {
			t.Errorf("%s is keyed to a profile describing %s", isolation, profile.Isolation)
		}
		if len(profile.RendererClasses) == 0 {
			t.Errorf("%s admits no renderer shape, so nothing could ever declare it", isolation)
		}
	}
	if _, ok := IsolationProfileFor(Isolation("main-origin-ish")); ok {
		t.Error("an unknown isolation resolved to a profile")
	}
}

// Ambient authority is a property of where code runs, never of what a manifest
// calls itself. This is acceptance criterion 1 as an assertion.
func TestOnlyMainOriginCarriesAmbientHostAuthority(t *testing.T) {
	t.Parallel()
	for _, isolation := range Isolations() {
		profile, _ := IsolationProfileFor(isolation)
		want := profile.Isolation == IsolationMainOrigin
		if got := profile.Isolation.AmbientHostAuthority(); got != want {
			t.Errorf("%s: AmbientHostAuthority = %v, want %v", isolation, got, want)
		}
		if profile.ExecutesPublisherCode && profile.Isolation == IsolationHostPrimitive {
			t.Errorf("%s executes publisher code in the host-primitive isolation, which is "+
				"defined as the isolation where none does", isolation)
		}
	}
	if IsolationSandboxedFrame.AmbientHostAuthority() {
		t.Error("sandboxed code has ambient host authority")
	}
	// An unrecognized value must not resolve to anything at all — it has no
	// profile, and a manifest declaring it is refused rather than defaulted.
	if _, ok := IsolationProfileFor(Isolation("something-new")); ok {
		t.Error("an unknown isolation resolved to a profile instead of being refused")
	}
}

// TestRendererShapeAndIsolationMustAgree is the check ADR 0009 made
// load-bearing.
//
// It used to be one refusal among several — the provenance apparatus refused a
// manifest on publisher, on assurance, and on asset digest as well. Those are
// gone, so this is now the ONLY thing standing between a declared isolation and
// the one in force, and it is what lets `renderer.isolation` be read as a fact
// rather than as a request. If it ever stops refusing in both directions, the
// field silently becomes a wish.
func TestRendererShapeAndIsolationMustAgree(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		class     RendererClass
		isolation Isolation
		wantErr   bool
	}{
		{"react component runs in the main origin", RendererReactComponent, IsolationMainOrigin, false},
		{"declarative may be drawn by host primitives", RendererDeclarative, IsolationHostPrimitive, false},
		{"declarative may also be main-origin", RendererDeclarative, IsolationMainOrigin, false},
		{"sandboxed frame is sandboxed", RendererSandboxedFrame, IsolationSandboxedFrame, false},
		{"external surface is external", RendererExternalSurface, IsolationExternalSurface, false},

		// The original narrow rule (§7 T6): untrusted markup may not claim the
		// host's own authority.
		{"sandboxed frame may not claim main origin", RendererSandboxedFrame, IsolationMainOrigin, true},
		// And the direction the narrow rule missed. A React component that
		// calls itself sandboxed is not sandboxed — it is mislabeled, and the
		// label would buy it a weaker-looking classification for code that
		// still runs in the main origin.
		{"react component may not claim sandboxed frame", RendererReactComponent, IsolationSandboxedFrame, true},
		{"react component may not claim external surface", RendererReactComponent, IsolationExternalSurface, true},
		{"react component may not claim host primitive", RendererReactComponent, IsolationHostPrimitive, true},
		{"sandboxed frame may not claim host primitive", RendererSandboxedFrame, IsolationHostPrimitive, true},
		{"declarative may not claim sandboxed frame", RendererDeclarative, IsolationSandboxedFrame, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := trustManifest(func(m *Manifest) {
				m.Renderer.Class = testCase.class
				m.Renderer.Isolation = testCase.isolation
			})
			err := manifest.validate()
			if testCase.wantErr && err == nil {
				t.Fatalf("%s/%s was accepted", testCase.class, testCase.isolation)
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("%s/%s was refused: %v", testCase.class, testCase.isolation, err)
			}
		})
	}
}

// TestMaterializationNeverSubstitutesAnIsolation is the property ADR 0009 leans
// on to call the field a statement of fact.
//
// A manifest either materializes with exactly the isolation it declared, or it
// does not materialize. There is no third outcome in which the host quietly
// runs a renderer somewhere other than where its author said — which is what a
// downgrade would be, and what makes "the name does not lie" checkable rather
// than asserted.
func TestMaterializationNeverSubstitutesAnIsolation(t *testing.T) {
	t.Parallel()
	for _, isolation := range Isolations() {
		profile, _ := IsolationProfileFor(isolation)
		manifest := trustManifest(func(m *Manifest) {
			m.Renderer.Isolation = isolation
			m.Renderer.Class = profile.RendererClasses[0]
		})
		materialized, err := Materialize(manifest, trustMaterial(), trustPolicy())
		if err != nil {
			t.Fatalf("%s: Materialize: %v", isolation, err)
		}
		if materialized.State == StateQuarantined {
			continue // refused outright, which is the other legal outcome
		}
		if materialized.Isolation == "" {
			t.Fatalf("%s: never reached the isolation step (state %s: %s)",
				isolation, materialized.State, materialized.StateReason)
		}
		if materialized.Isolation != isolation {
			t.Errorf("%s declared, %q in force: materialization substituted an isolation",
				isolation, materialized.Isolation)
		}
	}
}
