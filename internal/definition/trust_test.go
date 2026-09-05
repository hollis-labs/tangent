package definition

import (
	"strings"
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
			ID:         "tangent.renderer.example",
			Class:      RendererReactComponent,
			Entry:      "components/envelopes/Example#Example",
			TrustClass: TrustCoreTrusted,
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

func TestEveryTrustClassHasAProfileAndEveryProfileHasAClass(t *testing.T) {
	t.Parallel()
	classes := TrustClasses()
	if len(classes) != len(trustProfiles) {
		t.Fatalf("TrustClasses lists %d classes, the table has %d", len(classes), len(trustProfiles))
	}
	for _, class := range classes {
		profile, ok := TrustProfileFor(class)
		if !ok {
			t.Fatalf("%s has no profile", class)
		}
		if !class.valid() {
			t.Errorf("%s is in the profile table but not in the enum", class)
		}
		if !profile.Isolation.valid() {
			t.Errorf("%s has isolation %q, which is not a value", class, profile.Isolation)
		}
		if len(profile.RendererClasses) == 0 {
			t.Errorf("%s admits no renderer class, so nothing could ever request it", class)
		}
	}
	if _, ok := TrustProfileFor(TrustClass("core-trusted-ish")); ok {
		t.Error("an unknown trust class resolved to a profile")
	}
}

// The ordering is the model. If it stops being strict, "raising trust_class"
// stops being a statement about what a renderer may reach.
func TestTheCapabilityCeilingsAreStrictlyOrdered(t *testing.T) {
	t.Parallel()
	set := func(class TrustClass) map[string]bool {
		profile, _ := TrustProfileFor(class)
		out := map[string]bool{}
		for _, id := range profile.Capabilities {
			out[id] = true
		}
		return out
	}
	core, portfolio, sandboxed := set(TrustCoreTrusted), set(TrustPortfolioTrusted), set(TrustSandboxedCode)

	for id := range portfolio {
		if !core[id] {
			t.Errorf("portfolio-trusted permits %q and core-trusted does not", id)
		}
	}
	for id := range sandboxed {
		if !portfolio[id] {
			t.Errorf("sandboxed-code permits %q and portfolio-trusted does not", id)
		}
	}
	if len(core) <= len(portfolio) || len(portfolio) <= len(sandboxed) {
		t.Errorf("ceilings are not strictly decreasing: core=%d portfolio=%d sandboxed=%d",
			len(core), len(portfolio), len(sandboxed))
	}
	for _, class := range []TrustClass{TrustDeclarative, TrustExternalSurface} {
		if profile, _ := TrustProfileFor(class); len(profile.Capabilities) != 0 {
			t.Errorf("%s permits %v; no publisher code runs in that class, so there is "+
				"no renderer to grant a capability to", class, profile.Capabilities)
		}
	}
	// The two that matter most, named rather than left to the ordering.
	if sandboxed[capabilityFileWrite] {
		t.Error("sandboxed-code may write to the workspace through a host proxy")
	}
	if sandboxed[capabilityProcessExec] || portfolio[capabilityProcessExec] {
		t.Error("process.exec is nameable outside core-trusted")
	}
}

// Ambient authority is a property of where code runs, never of what a manifest
// calls itself. This is acceptance criterion 1 as an assertion.
func TestOnlyMainOriginCarriesAmbientHostAuthority(t *testing.T) {
	t.Parallel()
	for _, class := range TrustClasses() {
		profile, _ := TrustProfileFor(class)
		want := profile.Isolation == IsolationMainOrigin
		if got := profile.Isolation.AmbientHostAuthority(); got != want {
			t.Errorf("%s: AmbientHostAuthority = %v, want %v", class, got, want)
		}
		if profile.ExecutesPublisherCode && !want && profile.Isolation == IsolationHostPrimitive {
			t.Errorf("%s executes publisher code in the host-primitive isolation, which is "+
				"defined as the isolation where none does", class)
		}
	}
	if IsolationFor(TrustSandboxedCode).AmbientHostAuthority() {
		t.Error("sandboxed code has ambient host authority")
	}
	// An unrecognized class must not fall through to the main origin.
	if got := IsolationFor(TrustClass("something-new")); got != IsolationExternalSurface {
		t.Errorf("unknown trust class isolation = %q, want %q", got, IsolationExternalSurface)
	}
}

func TestRendererShapeAndTrustClassMustAgree(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		class      RendererClass
		trustClass TrustClass
		wantErr    bool
	}{
		{"react component may be core trusted", RendererReactComponent, TrustCoreTrusted, false},
		{"react component may be portfolio trusted", RendererReactComponent, TrustPortfolioTrusted, false},
		{"sandboxed frame may be sandboxed code", RendererSandboxedFrame, TrustSandboxedCode, false},
		{"declarative may be declarative", RendererDeclarative, TrustDeclarative, false},
		{"external surface may be external surface", RendererExternalSurface, TrustExternalSurface, false},

		// The original narrow rule (§7 T6).
		{"sandboxed frame may not be core trusted", RendererSandboxedFrame, TrustCoreTrusted, true},
		// The cases the narrow rule missed. A React component that calls
		// itself sandboxed is not sandboxed — it is mislabeled, and the label
		// would have bought it a weaker-looking classification for code that
		// still runs in the main origin.
		{"react component may not claim sandboxed code", RendererReactComponent, TrustSandboxedCode, true},
		{"react component may not claim external surface", RendererReactComponent, TrustExternalSurface, true},
		{"sandboxed frame may not be declarative", RendererSandboxedFrame, TrustDeclarative, true},
		{"declarative may not be sandboxed code", RendererDeclarative, TrustSandboxedCode, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := trustManifest(func(m *Manifest) {
				m.Renderer.Class = testCase.class
				m.Renderer.TrustClass = testCase.trustClass
			})
			err := manifest.validate()
			if testCase.wantErr && err == nil {
				t.Fatalf("%s/%s was accepted", testCase.class, testCase.trustClass)
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("%s/%s was refused: %v", testCase.class, testCase.trustClass, err)
			}
		})
	}
}

func TestTrustEvidenceGatesTheReleaseReviewedClasses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		mutate        func(*Manifest)
		wantState     State
		wantReasonSub string
	}{
		{
			name:      "an in-tree core-trusted renderer materializes",
			mutate:    nil,
			wantState: StateAvailable,
		},
		{
			name: "a separately distributed bundle may not be core-trusted",
			mutate: func(m *Manifest) {
				m.Renderer.AssetDigest = "sha256:" + strings.Repeat("a", 64)
			},
			wantState:     StateQuarantined,
			wantReasonSub: "asset_digest",
		},
		{
			name: "another publisher may not be core-trusted",
			mutate: func(m *Manifest) {
				m.Publisher = "acme"
				m.OwnershipClass = OwnershipApplicationPackage
				m.PackageID = "acme.thing"
			},
			wantState:     StateQuarantined,
			wantReasonSub: "reviewed with the Tangent release",
		},
		{
			name: "another publisher may be portfolio-trusted on grantable evidence",
			mutate: func(m *Manifest) {
				m.Publisher = "acme"
				m.OwnershipClass = OwnershipApplicationPackage
				m.PackageID = "acme.thing"
				m.Renderer.TrustClass = TrustPortfolioTrusted
			},
			wantState: StateAvailable,
		},
		{
			name: "a separately distributed portfolio bundle is admitted",
			mutate: func(m *Manifest) {
				m.Renderer.TrustClass = TrustPortfolioTrusted
				m.Renderer.AssetDigest = "sha256:" + strings.Repeat("b", 64)
			},
			wantState: StateAvailable,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := trustManifest(testCase.mutate)
			materialized, err := Materialize(manifest, trustMaterial(), trustPolicy())
			if err != nil {
				t.Fatalf("Materialize: %v", err)
			}
			if materialized.State != testCase.wantState {
				t.Fatalf("state = %s (%s), want %s",
					materialized.State, materialized.StateReason, testCase.wantState)
			}
			if testCase.wantReasonSub != "" &&
				!strings.Contains(materialized.QuarantineReason, testCase.wantReasonSub) {
				t.Fatalf("quarantine reason = %q, want it to mention %q",
					materialized.QuarantineReason, testCase.wantReasonSub)
			}
			if testCase.wantState == StateAvailable {
				if materialized.TrustClass != manifest.Renderer.TrustClass {
					t.Errorf("granted trust class = %q, want the requested %q",
						materialized.TrustClass, manifest.Renderer.TrustClass)
				}
				if materialized.Isolation != IsolationFor(manifest.Renderer.TrustClass) {
					t.Errorf("isolation = %q, want %q",
						materialized.Isolation, IsolationFor(manifest.Renderer.TrustClass))
				}
			}
		})
	}
}

// The ceiling runs before host policy, so widening policy widens nothing the
// class already closed. This is the escalation path the ordering exists to
// prevent, asserted directly.
func TestTheTrustCeilingOutranksHostPolicy(t *testing.T) {
	t.Parallel()
	sandboxed := func(m *Manifest) {
		m.Renderer.Class = RendererSandboxedFrame
		m.Renderer.TrustClass = TrustSandboxedCode
		m.Renderer.Entry = "frame/example"
		m.RequiredCapabilities = []Capability{{ID: capabilityFileWrite}}
	}

	// An operator who has granted the capability outright.
	materialized, err := Materialize(
		trustManifest(sandboxed), trustMaterial(), trustPolicy(capabilityFileWrite))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if materialized.State != StateQuarantined {
		t.Fatalf("state = %s, want quarantined even with the capability granted", materialized.State)
	}
	if !strings.Contains(materialized.QuarantineReason, "sandboxed-code") ||
		!strings.Contains(materialized.QuarantineReason, capabilityFileWrite) {
		t.Fatalf("quarantine reason = %q, want it to name the class and the capability",
			materialized.QuarantineReason)
	}
	if len(materialized.TrustDeniedCapabilities) != 1 {
		t.Fatalf("trust-denied capabilities = %v, want exactly the one the class refuses",
			materialized.TrustDeniedCapabilities)
	}
	if len(materialized.GrantedCapabilities) != 0 {
		t.Errorf("granted %v despite the class refusing it", materialized.GrantedCapabilities)
	}
}

// An *optional* capability outside the ceiling degrades rather than
// quarantines, and is reported separately from a host-policy denial so an
// operator is not told to widen a policy that would not help.
func TestAnOptionalCapabilityOutsideTheCeilingDegrades(t *testing.T) {
	t.Parallel()
	manifest := trustManifest(func(m *Manifest) {
		m.Renderer.Class = RendererSandboxedFrame
		m.Renderer.TrustClass = TrustSandboxedCode
		m.Renderer.Entry = "frame/example"
		m.RequiredCapabilities = []Capability{
			{ID: capabilityFileWrite, Optional: true},
			{ID: capabilityFileRead},
		}
	})
	materialized, err := Materialize(
		manifest, trustMaterial(), trustPolicy(capabilityFileRead, capabilityFileWrite))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if materialized.State != StateAvailable {
		t.Fatalf("state = %s (%s), want available", materialized.State, materialized.StateReason)
	}
	if len(materialized.GrantedCapabilities) != 1 ||
		materialized.GrantedCapabilities[0].ID != capabilityFileRead {
		t.Fatalf("granted = %v, want only the permitted capability", materialized.GrantedCapabilities)
	}
	if len(materialized.TrustDeniedCapabilities) != 1 {
		t.Fatalf("trust-denied = %v, want the optional one the class refuses",
			materialized.TrustDeniedCapabilities)
	}
	if len(materialized.DeniedCapabilities) != 0 {
		t.Errorf("host-policy denials = %v; the class refused it, not the operator",
			materialized.DeniedCapabilities)
	}
}
