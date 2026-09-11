package extensions

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/envelope"
)

// The drift gates in this file compare the four places a shipped definition
// exists — the authored manifest, the Go registration table, the embedded
// package tree, and the running registry — against each other.
//
// ADR 0003 §4 is explicit that drift must be *detected* rather than inferred
// from a successful build. A build succeeds whether or not the manifest a
// reader is looking at is the manifest the server registered, and the failure
// mode of getting that wrong is not a crash: it is a definition whose declared
// trust class, capabilities, or custody differ from the ones actually in force.

// TestPackageTreeMatchesRegistrations pins the invariant that makes
// registrations readable: every kind directory in the shipped tree is
// registered, and every registration ships a directory.
//
// Without it, a manifest can be authored, reviewed, and merged while nothing
// registers it — so the trust class and capability declarations a reviewer
// approved are not the ones the host applies — and a registration can survive
// the deletion of its package, failing at boot rather than at review.
func TestPackageTreeMatchesRegistrations(t *testing.T) {
	t.Parallel()
	shipped, err := ShippedPackagePaths()
	if err != nil {
		t.Fatalf("ShippedPackagePaths: %v", err)
	}
	registered := PackagedKinds()
	slices.Sort(registered)

	for _, path := range shipped {
		if !slices.Contains(registered, path) {
			t.Errorf("package %s ships a manifest that no registration installs", path)
		}
	}
	for _, path := range registered {
		if !slices.Contains(shipped, path) {
			t.Errorf("registration %s has no manifest in the shipped package tree", path)
		}
	}
	if len(shipped) != len(RegisteredTypes()) {
		t.Errorf("shipped packages = %d, registered kinds = %d", len(shipped), len(RegisteredTypes()))
	}
}

// TestEveryShippedManifestMaterializesAsAvailable is the gate that turns the
// manifest from documentation into behavior: a kind that is registered but not
// `available` cannot be submitted against, so a manifest whose compatibility
// range, capability request, or renderer entry is wrong takes the kind out of
// production. Catching that here, rather than at the first submission, is the
// difference between a failing test and a broken workflow.
func TestEveryShippedManifestMaterializesAsAvailable(t *testing.T) {
	t.Parallel()
	svc := registeredService(t)
	materialized := svc.MaterializedDefinitions()
	if len(materialized) != len(RegisteredTypes()) {
		t.Fatalf("materialized definitions = %d, registered kinds = %d",
			len(materialized), len(RegisteredTypes()))
	}
	for _, item := range materialized {
		if item.State != definition.StateAvailable {
			t.Errorf("%s@%s materialized as %s: %s",
				item.Manifest.Kind, item.Manifest.Version, item.State, item.StateReason)
		}
		if item.Derived.ManifestDigest == "" || item.Derived.ContractDigest == "" {
			t.Errorf("%s has no derived identity: %+v", item.Manifest.Kind, item.Derived)
		}
		if item.SourceLocator == "" {
			t.Errorf("%s records no trust.source_locator", item.Manifest.Kind)
		}
	}
}

// TestShippedManifestsHonorCompatibilityDefaults asserts the ADR 0003 §8 C4
// defaults are what the shipped manifests actually say, one clause at a time.
//
// The `retention_class` clause is the one that matters most and is the least
// obvious. C4 deliberately leaves it unauthored so ADR 0002 §4's host default —
// `interaction`, redacted 30 days after terminal — governs new interactions of
// these kinds. Authoring `surface` here would re-loosen, through the min() in
// ADR 0002 §3, exactly the default ADR 0002 tightened, and it would do so
// silently: nothing else in the system would report the change.
// responseSchemaBackfilled is the set of shipped kinds that carry a real
// response schema. It starts as tangent.hitl-item, the ADR 0003 §6 reference
// package, plus tangent.form-collect, backfilled by CW-20260825-0074 alongside
// its interaction package.
var responseSchemaBackfilled = []string{
	HITLItemEnvelopeType,
	FormCollectEnvelopeType,
}

// TestEveryShippedRendererIsExplicitlyClassified is CW-20260825-0073's
// acceptance criterion 5.
//
// Every shipped manifest already *spelled* a trust class before this task; what
// it did not have was a consequence. The class now decides an isolation, a
// capability ceiling, and — through effect.MediationFor — whether an effect is
// enforced or merely declared. So the classification is written out here kind
// by kind rather than derived from the manifests, which would assert nothing:
// ADR 0003 §8 C2 makes raising a trust class a major version bump, and this
// list is what turns that rule into a failing build.
func TestEveryShippedRendererIsExplicitlyClassified(t *testing.T) {
	t.Parallel()
	// Most kinds draw with React components reviewed and shipped in Tangent's
	// own tree. `tangent.whiteboard` is portfolio-trusted because it embeds
	// tldraw — a third-party editor whose code Tangent hosts but does not
	// author — and `tangent.design-iteration` is the one renderer that executes
	// agent-authored markup, so it is the only sandboxed-code class in the
	// distribution.
	//
	// `tangent.app-board` is core-trusted like the rest. It was listed here as
	// the plugin-contributed exception until CW-20260911-0036 established it had
	// never been one; the rule it was cited for is what matters and still holds.
	// ADR 0007 §4 keeps core-trusted unreachable for a publisher that is not
	// `tangent` or `hollis-labs/go-envelopes`, so a kind a plugin genuinely
	// contributes cannot reach a higher class than a host-package one — which is
	// the failure this line exists to make visible.
	classified := map[string]definition.TrustClass{
		"tangent.app-board":          definition.TrustCoreTrusted,
		"tangent.approval-queue":     definition.TrustCoreTrusted,
		"tangent.block-draft":        definition.TrustCoreTrusted,
		"tangent.dashboard":          definition.TrustCoreTrusted,
		"tangent.design-iteration":   definition.TrustSandboxedCode,
		"tangent.diff-review":        definition.TrustCoreTrusted,
		"tangent.feedback":           definition.TrustCoreTrusted,
		"tangent.file-picker":        definition.TrustCoreTrusted,
		"tangent.form-collect":       definition.TrustCoreTrusted,
		"tangent.hitl-item":          definition.TrustCoreTrusted,
		"tangent.interview-question": definition.TrustCoreTrusted,
		"tangent.output-render":      definition.TrustCoreTrusted,
		"tangent.progress-panel":     definition.TrustCoreTrusted,
		"tangent.prose-revision":     definition.TrustCoreTrusted,
		"tangent.spreadsheet-review": definition.TrustCoreTrusted,
		"tangent.synthesis-notes":    definition.TrustCoreTrusted,
		"tangent.triage":             definition.TrustCoreTrusted,
		"tangent.whiteboard":         definition.TrustPortfolioTrusted,
		"tangent.wizard":             definition.TrustCoreTrusted,
	}

	svc := registeredService(t)
	materialized := svc.MaterializedDefinitions()
	if len(materialized) != len(classified) {
		t.Fatalf("materialized %d definitions, classified %d: a new kind needs a line here",
			len(materialized), len(classified))
	}
	for _, item := range materialized {
		kind := item.Manifest.Kind
		want, listed := classified[kind]
		if !listed {
			t.Errorf("%s has no reviewed trust class in this test", kind)
			continue
		}
		// The *granted* class, not the requested one. They are equal on an
		// available definition by construction — a request the evidence does
		// not support is quarantined — and asserting the granted one is what
		// makes that construction load-bearing rather than incidental.
		if item.TrustClass != want {
			t.Errorf("%s granted trust class = %q, want %q", kind, item.TrustClass, want)
		}
		if item.Manifest.Renderer.TrustClass != want {
			t.Errorf("%s requests trust class %q, want %q",
				kind, item.Manifest.Renderer.TrustClass, want)
		}
		if got := definition.IsolationFor(want); item.Isolation != got {
			t.Errorf("%s isolation = %q, want %q", kind, item.Isolation, got)
		}
		// Nothing in v0.x declares an effect capability, so nothing is denied
		// by either gate. A kind that starts declaring one has to come through
		// this test, which is where a reviewer will see the ceiling.
		if len(item.TrustDeniedCapabilities) != 0 {
			t.Errorf("%s has capabilities its trust class refuses: %v",
				kind, item.TrustDeniedCapabilities)
		}
	}

	// The one renderer that runs untrusted code runs it nowhere near Tangent's
	// own authority. This is acceptance criterion 1 stated against the shipped
	// set rather than against the model.
	if definition.IsolationFor(definition.TrustSandboxedCode).AmbientHostAuthority() {
		t.Error("tangent.design-iteration would execute agent-authored markup with " +
			"Tangent main-origin authority")
	}
}

func TestShippedManifestsHonorCompatibilityDefaults(t *testing.T) {
	t.Parallel()
	// The kinds that ship a browser-local draft-storage module today. ADR 0003
	// §8 C4 requires the manifest to describe what the code does, so these are
	// authored `browser-local` and every other kind is `disabled`.
	//
	// ADR 0003 §2.4 says nine such modules exist; the tree has eight —
	// progress-panel has no *-draft-storage.ts. The manifest follows the code.
	browserLocal := []string{
		"tangent.approval-queue", "tangent.dashboard", "tangent.diff-review",
		"tangent.file-picker", "tangent.form-collect", "tangent.spreadsheet-review",
		"tangent.whiteboard", "tangent.wizard",
	}

	// The kinds whose drafts are Tangent's own durable records rather than a
	// browser's localStorage. ADR 0007 §5 is the decision; Service.SaveDraft is
	// the mechanism, and `tangent.surface_get` is how the caller reads them.
	//
	// This list being short is the point. `tangent-custodied` is a new class of
	// retained participant content under ADR 0002 custody, so a kind joining it
	// is a deliberate act and not a manifest edit nobody noticed. The value has
	// been in the format since `2e2c48a`; what is new is a kind that means it.
	tangentCustodied := []string{
		"tangent.app-board",
	}

	svc := registeredService(t)
	for _, item := range svc.MaterializedDefinitions() {
		manifest := item.Manifest
		kind := manifest.Kind

		if manifest.RetentionClass != nil {
			t.Errorf("%s authors retention_class %+v; C4 requires it unauthored so the "+
				"ADR 0002 §4 host default governs", kind, *manifest.RetentionClass)
		}
		if len(manifest.RequiredCapabilities) != 0 {
			t.Errorf("%s requires capabilities %+v; nothing is capability-mediated in v0.x "+
				"and CW-20260825-0077 is what authors real grants", kind, manifest.RequiredCapabilities)
		}
		if item.Assurance != definition.AssuranceContentAddressedRegistry {
			t.Errorf("%s assurance = %q, want the shipped value %q",
				kind, item.Assurance, definition.AssuranceContentAddressedRegistry)
		}

		wantCustody := definition.DraftCustodyDisabled
		switch {
		case slices.Contains(browserLocal, kind):
			wantCustody = definition.DraftCustodyBrowserLocal
		case slices.Contains(tangentCustodied, kind):
			wantCustody = definition.DraftCustodyTangentCustodied
		}
		if manifest.DraftCustody != wantCustody {
			t.Errorf("%s draft_custody = %q, want %q", kind, manifest.DraftCustody, wantCustody)
		}

		wantClass := definition.RendererReactComponent
		if kind == DesignIterationEnvelopeType {
			wantClass = definition.RendererSandboxedFrame
		}
		if manifest.Renderer.Class != wantClass {
			t.Errorf("%s renderer.class = %q, want %q", kind, manifest.Renderer.Class, wantClass)
		}

		// The kinds backfilled so far. ADR 0003 §9 S2 backfills the seventeen
		// shipped kinds one at a time; tangent.form-collect is the one
		// CW-20260825-0074 packaged and therefore the one whose response
		// schema could be authored from a single owner's real behavior.
		// Adding a kind here without authoring its schema from its handler is
		// the mistake this list exists to make visible.
		wantResponseSchema := definition.ResponseSchemaAbsent
		if slices.Contains(responseSchemaBackfilled, kind) {
			wantResponseSchema = definition.ResponseSchemaPresent
		}
		if manifest.CompatibilityResponseSchema != wantResponseSchema {
			t.Errorf("%s compatibility_response_schema = %q, want %q",
				kind, manifest.CompatibilityResponseSchema, wantResponseSchema)
		}
	}
}

// TestShippedManifestsMatchTheirADROwnershipAssignment holds the §6 table to
// the manifests. The ownership label is what gives the §7 decision tests force:
// a kind that quietly relabels itself `host-package` has moved Tangent's
// responsibility for its semantics without anyone deciding to.
func TestShippedManifestsMatchTheirADROwnershipAssignment(t *testing.T) {
	t.Parallel()
	want := map[string]struct {
		packageID string
		ownership definition.OwnershipClass
	}{
		// The six ADR 0003 §6 generic-catalog *destinations*. No upstream move
		// is scheduled, so they ship bundled under a holding package.
		TriageEnvelopeType:            {"tangent.generic-candidate", definition.OwnershipGenericCatalog},
		FeedbackEnvelopeType:          {"tangent.generic-candidate", definition.OwnershipGenericCatalog},
		FormCollectEnvelopeType:       {"tangent.generic-candidate", definition.OwnershipGenericCatalog},
		InterviewQuestionEnvelopeType: {"tangent.generic-candidate", definition.OwnershipGenericCatalog},
		OutputRenderEnvelopeType:      {"tangent.generic-candidate", definition.OwnershipGenericCatalog},
		ProgressPanelEnvelopeType:     {"tangent.generic-candidate", definition.OwnershipGenericCatalog},

		ApprovalQueueEnvelopeType:     {"tangent.review", definition.OwnershipHostPackage},
		DiffReviewEnvelopeType:        {"tangent.review", definition.OwnershipHostPackage},
		SpreadsheetReviewEnvelopeType: {"tangent.review", definition.OwnershipHostPackage},
		FilePickerEnvelopeType:        {"tangent.workspace", definition.OwnershipHostPackage},
		DesignIterationEnvelopeType:   {"tangent.canvas", definition.OwnershipHostPackage},
		WhiteboardEnvelopeType:        {"tangent.canvas", definition.OwnershipHostPackage},
		DashboardEnvelopeType:         {"tangent.canvas", definition.OwnershipHostPackage},
		WizardEnvelopeType:            {"tangent.compound", definition.OwnershipHostPackage},
		HITLItemEnvelopeType:          {HITLPackageID, definition.OwnershipHostPackage},

		// Its own package, and a host package. It went through the plugin door
		// until CW-20260911-0036, and this row is the reason that was always
		// wrong: the kind's semantics — what a column, a card, a filter and a
		// detail pane mean — are Tangent's, and the manifest that says so is
		// Tangent's too. Two applications supply content to it and neither owns
		// any of it.
		AppBoardEnvelopeType: {AppBoardPackageID, definition.OwnershipHostPackage},

		// Publisher-owned editorial semantics, bundled only until a writing
		// application exists to own them.
		BlockDraftEnvelopeType:     {"tangent.writing", definition.OwnershipApplicationPackage},
		ProseRevisionEnvelopeType:  {"tangent.writing", definition.OwnershipApplicationPackage},
		SynthesisNotesEnvelopeType: {"tangent.writing", definition.OwnershipApplicationPackage},
	}

	svc := registeredService(t)
	seen := 0
	for _, item := range svc.MaterializedDefinitions() {
		expected, ok := want[item.Manifest.Kind]
		if !ok {
			t.Errorf("%s has no ADR 0003 §6 ownership assignment in this table", item.Manifest.Kind)
			continue
		}
		seen++
		if item.Manifest.PackageID != expected.packageID {
			t.Errorf("%s package_id = %q, want %q",
				item.Manifest.Kind, item.Manifest.PackageID, expected.packageID)
		}
		if item.Manifest.OwnershipClass != expected.ownership {
			t.Errorf("%s ownership_class = %q, want %q",
				item.Manifest.Kind, item.Manifest.OwnershipClass, expected.ownership)
		}
	}
	if seen != len(want) {
		t.Errorf("checked %d kinds against a table of %d", seen, len(want))
	}
}

// TestWireShapesAreUnchangedByTheManifestPath is the ADR 0003 §8 C3 gate. The
// compatibility adapters must produce the exact wire name, version,
// description, response kind, `ui.component` slug, and — critically — the same
// compiled schema resource identity the previous inline registration produced.
//
// `schema_identity` is the one that would fail quietly. It is persisted into
// every definition binding, so a change to the URI RegisterDefinition compiles
// with would move the identity on every future binding while leaving existing
// records pointing at a URI nothing produces any more.
func TestWireShapesAreUnchangedByTheManifestPath(t *testing.T) {
	t.Parallel()
	svc := registeredService(t)
	for _, kind := range RegisteredTypes() {
		spec, ok := svc.Lookup(kind)
		if !ok {
			t.Errorf("%s is not registered", kind)
			continue
		}
		if spec.Name != kind {
			t.Errorf("registered name %q does not match %q", spec.Name, kind)
		}
		if spec.PluginID != PluginID {
			t.Errorf("%s plugin id = %q, want %q", kind, spec.PluginID, PluginID)
		}
		if spec.DataSchema == nil {
			t.Errorf("%s registered without a request schema", kind)
			continue
		}
		want := "plugin://" + PluginID + "/envelopes/" + kind + ".schema.json#"
		if spec.DataSchema.Location != want {
			t.Errorf("%s schema identity = %q, want %q — this value is persisted in every "+
				"definition binding and must not move", kind, spec.DataSchema.Location, want)
		}

		material, found := svc.LookupDefinitionMaterial(kind)
		if !found || material.Definition == nil {
			t.Errorf("%s retained no manifest material", kind)
			continue
		}
		manifest := material.Definition.Manifest
		if spec.Version != manifest.Version {
			t.Errorf("%s registered version %q, manifest says %q", kind, spec.Version, manifest.Version)
		}
		if spec.Description != manifest.Description {
			t.Errorf("%s description drifted from its manifest", kind)
		}
		if string(spec.ResponseKind) != manifest.ResponseKind {
			t.Errorf("%s response kind = %q, manifest says %q", kind, spec.ResponseKind, manifest.ResponseKind)
		}
		// The legacy ui.component slug still keys the generated
		// EnvelopeKindMap and every committed TypeScript consumer.
		// renderer.entry is the authoritative binding, but dropping the slug
		// would be a wire change dressed as a cleanup.
		if manifest.Renderer.Component != "" {
			component, _ := spec.UIMetadata["component"].(string)
			if component != manifest.Renderer.Component {
				t.Errorf("%s ui.component = %q, manifest says %q",
					kind, component, manifest.Renderer.Component)
			}
		}
	}
}

// TestOnlyBackfilledKindsCarryAResponseSchema keeps the §9 S2 sequencing
// visible: a kind that starts carrying a response schema begins validating
// responses against it, which is a behavior change that must be a deliberate
// CW-20260825-0074 backfill and not a side effect of editing a manifest.
func TestOnlyBackfilledKindsCarryAResponseSchema(t *testing.T) {
	t.Parallel()
	svc := registeredService(t)
	for _, kind := range RegisteredTypes() {
		material, ok := svc.LookupDefinitionMaterial(kind)
		if !ok {
			t.Fatalf("%s retained no material", kind)
		}
		hasResponse := len(material.ResponseSchema) > 0
		if slices.Contains(responseSchemaBackfilled, kind) {
			if !hasResponse {
				t.Errorf("%s is declared backfilled and must carry a response schema", kind)
			}
			continue
		}
		if hasResponse {
			t.Errorf("%s carries a response schema but declares compatibility_response_schema: absent; "+
				"backfilling one is a CW-20260825-0074 decision", kind)
		}
	}
}

// TestHITLContractProjectionsMatchBundle is the drift gate for the two schema
// documents the HITL package ships alongside its request bundle. They are
// projections of that bundle, committed as files because a manifest references
// files — so an edit to the bundle that is not reflected in them produces a
// definition whose response and error contracts silently disagree with its
// request contract.
func TestHITLContractProjectionsMatchBundle(t *testing.T) {
	t.Parallel()
	for _, projection := range []struct {
		name    string
		file    string
		project func() ([]byte, error)
	}{
		{"response", responseSchemaFileName, HITLResponseContractSchema},
		{"error", errorSchemaFileName, HITLErrorContractSchema},
	} {
		committed := mustPackageFile(HITLPackageID, HITLItemEnvelopeType, projection.file)
		fresh, err := projection.project()
		if err != nil {
			t.Fatalf("project %s schema: %v", projection.name, err)
		}
		if !bytes.Equal(committed, fresh) {
			t.Errorf("packages/%s/%s/%s no longer matches the v1 $defs bundle it projects; "+
				"regenerate it from HITL%sContractSchema",
				HITLPackageID, kindSlug(HITLItemEnvelopeType), projection.file,
				map[string]string{"response": "Response", "error": "Error"}[projection.name])
		}
	}
}

// TestHITLNamedDefinitionsExistInTheBundle keeps the manifest's declared
// codegen entry points honest. `named_definitions` is what MCP schema
// derivation and Sigil consume (ADR 0003 §2.2); an entry naming a `$defs` key
// that does not exist is a generator failure deferred to whoever runs the
// generator next.
func TestHITLNamedDefinitionsExistInTheBundle(t *testing.T) {
	t.Parallel()
	manifestBytes, err := PackageManifest(HITLItemEnvelopeType)
	if err != nil {
		t.Fatalf("PackageManifest: %v", err)
	}
	manifest, err := definition.Parse(manifestBytes)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(manifest.NamedDefinitions) == 0 {
		t.Fatal("the reference package declares no named definitions")
	}

	var bundle map[string]json.RawMessage
	if err := json.Unmarshal(HITLItemContractSchema(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	var defs map[string]json.RawMessage
	if err := json.Unmarshal(bundle["$defs"], &defs); err != nil {
		t.Fatalf("decode $defs: %v", err)
	}
	for name, purpose := range manifest.NamedDefinitions {
		if _, ok := defs[name]; !ok {
			t.Errorf("named_definitions declares %q, which is not in the $defs bundle", name)
		}
		if purpose == "" {
			t.Errorf("named_definitions entry %q has no stated purpose", name)
		}
	}
}

// TestManifestsRejectAuthoredDerivedFields guards the one-way rule: Tangent
// derives digests and grants, publishers author declarations. A manifest that
// could name its own digest could lie about it, and every downstream check —
// the pin, the staleness gate, the runtime digest comparison — would then be
// checking a value the publisher chose.
func TestManifestsRejectAuthoredDerivedFields(t *testing.T) {
	t.Parallel()
	base := mustPackageFile("tangent.generic-candidate", TriageEnvelopeType, manifestFileName)
	for _, derived := range []string{
		"manifest_digest: \"sha256:0\"",
		"contract_digest: \"sha256:0\"",
		"binding_digest: \"sha256:0\"",
		"granted_capabilities: []",
		"request_schema_digest: \"sha256:0\"",
		"source: core",
	} {
		tampered := append(bytes.Clone(base), '\n')
		tampered = append(tampered, []byte(derived+"\n")...)
		if _, err := definition.Parse(tampered); err == nil {
			t.Errorf("a manifest authoring %q was accepted", derived)
		}
	}
}

// TestReservedUnverifiedAssuranceHasNoProducer holds ADR 0003 §2.7's line: the
// value exists so signed-package and future classes are expressible without a
// format change, and it must never become the permissive fallback the ADR
// rejected by name. A manifest requesting it fails registration outright.
func TestReservedUnverifiedAssuranceHasNoProducer(t *testing.T) {
	t.Parallel()
	base := string(mustPackageFile("tangent.generic-candidate", TriageEnvelopeType, manifestFileName))
	tampered := bytes.ReplaceAll(
		[]byte(base),
		[]byte("assurance: content-addressed-registry"),
		[]byte("assurance: unverified"))
	if _, err := definition.Parse(tampered); err == nil {
		t.Fatal("a manifest requesting trust.assurance: unverified was accepted")
	}

	svc := registeredService(t)
	for _, item := range svc.MaterializedDefinitions() {
		if item.Assurance == definition.AssuranceUnverified {
			t.Errorf("%s materialized as unverified", item.Manifest.Kind)
		}
	}
}

func registeredService(t *testing.T) *envelope.Service {
	t.Helper()
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	// Both doors. See registerEveryShippedKind in register_all_test.go for why
	// this cannot go through internal/pluginhost from inside this package, and
	// why testing only RegisterAll would take every plugin-contributed kind out
	// of the drift gates below.
	registerEveryShippedKind(t, svc)
	return svc
}

// TestGeneratedArtifactsCarryTheLiveSourceDigest is the Go-side half of the
// codegen staleness gate. `make check-envelopes` runs the same comparison
// through the Node generator, but that only runs when someone runs it; this
// runs on every `go test ./...`, and a stale generated file is a UI that types
// a definition the server no longer serves.
//
// It checks the stamp only. The Node gate additionally compares bytes, because
// a stamp cannot notice a hand-edit; duplicating that here would mean
// reimplementing the generator in Go, which is exactly the second source of
// truth this pipeline exists to avoid.
func TestGeneratedArtifactsCarryTheLiveSourceDigest(t *testing.T) {
	t.Parallel()
	svc := registeredService(t)
	live, err := svc.DefinitionSourceDigest()
	if err != nil {
		t.Fatalf("DefinitionSourceDigest: %v", err)
	}
	for _, generated := range []string{
		"../../../ui/src/generated/envelope-types.ts",
		"../../../ui/src/generated/renderer-bindings.ts",
	} {
		// #nosec G304 -- both paths are compile-time constants in this file;
		// nothing here reads a path from input.
		body, readErr := os.ReadFile(generated)
		if readErr != nil {
			t.Errorf("read %s: %v", generated, readErr)
			continue
		}
		stamp := definitionSourceStamp(string(body))
		if stamp == "" {
			t.Errorf("%s carries no @definition-source stamp", generated)
			continue
		}
		if stamp != live {
			t.Errorf("%s was generated from %s, the registry is now %s; run make generate-envelopes",
				generated, stamp, live)
		}
	}
}

// definitionSourceStamp reads the `// @definition-source <digest>` header line.
var definitionSourceStampPattern = regexp.MustCompile(`(?m)^// @definition-source (\S+)$`)

func definitionSourceStamp(body string) string {
	match := definitionSourceStampPattern.FindStringSubmatch(body)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}
