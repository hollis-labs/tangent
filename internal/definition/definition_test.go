package definition

import (
	"errors"
	"strings"
	"testing"
)

// validManifest is the smallest manifest that satisfies §2. Each test edits one
// field, so a failure names the field that broke rather than "the fixture".
const validManifest = `manifest_version: "1.0.0"
publisher: tangent
kind: tangent.fixture
version: "1.0"
revision: 1
title: "Fixture"
description: "A synthetic definition."
package_id: tangent.fixture
package_version: "1.0.0"
ownership_class: host-package
request_schema: request.schema.json
response_kind: data
compatibility_response_schema: absent
renderer:
  id: tangent.renderer.fixture
  class: react-component
  entry: "components/envelopes/Fixture#Fixture"
  isolation: main-origin
  fallback:
    preserves_meaning: false
    degradation: none
compatible_host_versions: ">=0.12.0 <1.0.0"
compatible_protocol_versions: ">=1 <2"
compatibility_class: additive
required_capabilities: []
draft_custody: disabled
sensitivity_default: normal
inline_payload_limit_bytes: 262144
client_persistence_prohibited: false
trust:
  assurance: content-addressed-registry
telemetry:
  emits: []
  redact_fields: []
  opt_in: true
`

func edit(t *testing.T, old, replacement string) []byte {
	t.Helper()
	if !strings.Contains(validManifest, old) {
		t.Fatalf("fixture no longer contains %q", old)
	}
	return []byte(strings.ReplaceAll(validManifest, old, replacement))
}

func TestParseAcceptsAWellFormedManifest(t *testing.T) {
	t.Parallel()
	manifest, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if manifest.Kind != "tangent.fixture" || manifest.Revision != 1 {
		t.Fatalf("parsed identity is wrong: %+v", manifest)
	}
	// Absence is not the same as false. Telemetry opt-in defaults to true, so
	// a manifest that says nothing does not silently opt its users in.
	if !manifest.Telemetry.TelemetryOptIn() {
		t.Error("telemetry.opt_in defaulted to false")
	}
	if manifest.RetentionClass != nil {
		t.Error("an unauthored retention_class must stay nil so the host default governs")
	}
}

// TestParseRejectsUnknownFields catches the failure mode a permissive parser
// hides: a manifest with `trust_class` at the top level instead of under
// `renderer` parses cleanly, declares nothing, and gets the zero value — which
// would silently be a weaker trust decision than the author wrote.
func TestParseRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	tampered := []byte(validManifest + "\nrenderer_isolation: main-origin\n")
	if _, err := Parse(tampered); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("unknown field error = %v, want ErrInvalidManifest", err)
	}
}

func TestParseRejectsIncoherentDeclarations(t *testing.T) {
	t.Parallel()
	for name, tampered := range map[string][]byte{
		"revision below one":          edit(t, "revision: 1", "revision: 0"),
		"unknown ownership class":     edit(t, "ownership_class: host-package", "ownership_class: mystery"),
		"unknown response kind":       edit(t, "response_kind: data", "response_kind: telepathy"),
		"unknown draft custody":       edit(t, "draft_custody: disabled", "draft_custody: wherever"),
		"unknown sensitivity":         edit(t, "sensitivity_default: normal", "sensitivity_default: spicy"),
		"malformed host range":        edit(t, `">=0.12.0 <1.0.0"`, `"~0.12"`),
		"zero payload limit":          edit(t, "inline_payload_limit_bytes: 262144", "inline_payload_limit_bytes: 0"),
		"unnamespaced capability":     edit(t, "required_capabilities: []", "required_capabilities:\n  - id: clipboard"),
		"reserved unverified trust":   edit(t, "assurance: content-addressed-registry", "assurance: unverified"),
		"host package, non-publisher": edit(t, "publisher: tangent", "publisher: someone-else"),
	} {
		if _, err := Parse(tampered); !errors.Is(err, ErrInvalidManifest) {
			t.Errorf("%s: error = %v, want ErrInvalidManifest", name, err)
		}
	}
}

// TestSandboxedFrameCannotRequestCoreTrust is ADR 0003 §7 T6 as a parse-time
// refusal. A renderer that executes untrusted markup asking for the trust class
// reserved for code shipped and reviewed with the release is not a subtle
// mistake — it is the exact escalation the trust test exists to stop, and
// catching it here means it cannot be smuggled in through a renderer
// convention.
func TestSandboxedFrameCannotRequestCoreTrust(t *testing.T) {
	t.Parallel()
	tampered := edit(t, "class: react-component", "class: sandboxed-frame")
	if _, err := Parse(tampered); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("sandboxed-frame requesting core-trusted was accepted: %v", err)
	}
}

// TestResponseSchemaIsMandatoryUnlessCompatibilityMarked is ADR 0003 §9 S2. A
// new definition must carry a response schema; a shipped kind that genuinely
// has none has to *say* so, because an omission and a deliberate compatibility
// statement read identically in a file but not in a review.
func TestResponseSchemaIsMandatoryUnlessCompatibilityMarked(t *testing.T) {
	t.Parallel()
	missing := edit(t, "compatibility_response_schema: absent\n", "")
	if _, err := Parse(missing); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("a manifest with neither a response schema nor the absent marker was accepted: %v", err)
	}

	contradictory := edit(t, "compatibility_response_schema: absent",
		"response_schema: response.schema.json\ncompatibility_response_schema: absent")
	if _, err := Parse(contradictory); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("absent alongside an authored response_schema was accepted: %v", err)
	}
}

// TestFallbackMustNameARendererToPreserveMeaning closes the hole that would
// make ADR 0003 §8 C5 unenforceable: a manifest claiming its fallback preserves
// meaning while naming no fallback would read as "a safe fallback exists" to
// every reviewer and resolve to nothing at runtime.
func TestFallbackMustNameARendererToPreserveMeaning(t *testing.T) {
	t.Parallel()
	tampered := edit(t, "preserves_meaning: false", "preserves_meaning: true")
	if _, err := Parse(tampered); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("preserves_meaning without a fallback renderer was accepted: %v", err)
	}
}

// TestRetentionClassShorthandExpandsToThePair covers ADR 0002 §1: custody is a
// pair, and `external-reference` is not a fifth position on the retention scale
// — it is `interaction` retention with the content held elsewhere. Reading it
// as a scale position would order it against `durable-record`, which is
// meaningless.
func TestRetentionClassShorthandExpandsToThePair(t *testing.T) {
	t.Parallel()
	for token, want := range map[string]RetentionClass{
		"ephemeral":          {Retention: RetentionEphemeral, ContentMode: ContentModeInline},
		"interaction":        {Retention: RetentionInteraction, ContentMode: ContentModeInline},
		"surface":            {Retention: RetentionSurface, ContentMode: ContentModeInline},
		"durable-record":     {Retention: RetentionDurableRecord, ContentMode: ContentModeInline},
		"external-reference": {Retention: RetentionInteraction, ContentMode: ContentModeExternalRef},
	} {
		got, err := ExpandRetentionShorthand(token)
		if err != nil {
			t.Errorf("%s: %v", token, err)
			continue
		}
		if got != want {
			t.Errorf("%s expanded to %+v, want %+v", token, got, want)
		}
	}

	manifest, err := Parse(edit(t, "draft_custody: disabled",
		"draft_custody: disabled\nretention_class: external-reference"))
	if err != nil {
		t.Fatalf("Parse with shorthand: %v", err)
	}
	if manifest.RetentionClass == nil ||
		manifest.RetentionClass.Retention != RetentionInteraction ||
		manifest.RetentionClass.ContentMode != ContentModeExternalRef {
		t.Fatalf("shorthand did not expand in a manifest: %+v", manifest.RetentionClass)
	}
}

// TestDigestsAreStableAndContentAddressed is the property every pin depends on.
// Reformatting a manifest must not move its digest — otherwise a whitespace
// change would read as a contract change and the drift gate would cry wolf —
// while changing what it declares must.
func TestDigestsAreStableAndContentAddressed(t *testing.T) {
	t.Parallel()
	manifest, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	material := Material{
		ManifestSource: []byte(validManifest),
		RequestSchema:  []byte(`{"type":"object"}`),
	}
	base, err := Derive(manifest, material)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if base.ManifestDigest == "" || base.ContractDigest == "" || base.RequestSchemaDigest == "" {
		t.Fatalf("digests are incomplete: %+v", base)
	}
	// Absent is not empty: a definition with no response schema must have no
	// response schema digest, or "absent" and "empty document" become
	// indistinguishable.
	if base.ResponseSchemaDigest != "" {
		t.Errorf("absent response schema produced digest %q", base.ResponseSchemaDigest)
	}

	// A comment-only edit changes the file but not the declarations.
	reformatted := "# an added comment\n" + validManifest
	reformattedManifest, err := Parse([]byte(reformatted))
	if err != nil {
		t.Fatalf("Parse reformatted: %v", err)
	}
	reformattedDerived, err := Derive(reformattedManifest, Material{
		ManifestSource: []byte(reformatted), RequestSchema: material.RequestSchema,
	})
	if err != nil {
		t.Fatalf("Derive reformatted: %v", err)
	}
	if reformattedDerived.ManifestDigest != base.ManifestDigest {
		t.Error("a comment moved manifest_digest; the digest must cover declarations, not formatting")
	}

	// A declaration change must move it.
	changed, err := Parse(edit(t, "sensitivity_default: normal", "sensitivity_default: sensitive"))
	if err != nil {
		t.Fatalf("Parse changed: %v", err)
	}
	changedDerived, err := Derive(changed, material)
	if err != nil {
		t.Fatalf("Derive changed: %v", err)
	}
	if changedDerived.ManifestDigest == base.ManifestDigest {
		t.Error("changing sensitivity_default did not move manifest_digest")
	}
	// It must NOT move the contract digest: sensitivity is not part of the
	// request/response contract, and ADR 0003 §3 lets a revision advance only
	// while the contract digest is unchanged.
	if changedDerived.ContractDigest != base.ContractDigest {
		t.Error("a non-contract field moved contract_digest")
	}

	// A schema change must move both.
	schemaChanged, err := Derive(manifest, Material{
		ManifestSource: material.ManifestSource,
		RequestSchema:  []byte(`{"type":"object","required":["a"]}`),
	})
	if err != nil {
		t.Fatalf("Derive schema changed: %v", err)
	}
	if schemaChanged.ContractDigest == base.ContractDigest {
		t.Error("a request schema change did not move contract_digest")
	}
}

// TestSourceDigestIgnoresEntryOrder keeps the drift stamp from reporting drift
// that does not exist. A generator that walks a map and one that walks a sorted
// tree must produce the same stamp; a gate that fires on iteration order is a
// gate people learn to ignore.
func TestSourceDigestIgnoresEntryOrder(t *testing.T) {
	t.Parallel()
	forward := []SourceEntry{
		{Kind: "a.one", Version: "1.0", Revision: 1, ManifestDigest: "sha256:aa"},
		{Kind: "b.two", Version: "2.0", Revision: 3, ManifestDigest: "sha256:bb"},
	}
	reversed := []SourceEntry{forward[1], forward[0]}
	first, err := SourceDigest(forward)
	if err != nil {
		t.Fatalf("SourceDigest: %v", err)
	}
	second, err := SourceDigest(reversed)
	if err != nil {
		t.Fatalf("SourceDigest: %v", err)
	}
	if first != second {
		t.Fatal("source digest depends on entry order")
	}

	bumped := []SourceEntry{forward[0], {
		Kind: "b.two", Version: "2.0", Revision: 4, ManifestDigest: "sha256:bb",
	}}
	third, err := SourceDigest(bumped)
	if err != nil {
		t.Fatalf("SourceDigest: %v", err)
	}
	if third == first {
		t.Fatal("a revision bump did not move the source digest")
	}
}

func TestVersionRangesCoverTheAuthoredGrammar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rangeIn string
		version string
		want    bool
	}{
		{"*", "9.9.9", true},
		{">=0.12.0 <1.0.0", "v0.12.0", true},
		{">=0.12.0 <1.0.0", "v0.11.9", false},
		{">=0.12.0 <1.0.0", "1.0.0", false},
		{">=1 <2", "1", true},
		{">=1 <2", "2", false},
		{"=1.0.0", "1.0.0", true},
		{"1.0.0", "1.0.1", false},
		{"<=1.0.0", "1.0.0", true},
	}
	for _, testCase := range cases {
		allowed, err := ParseRange(testCase.rangeIn)
		if err != nil {
			t.Errorf("ParseRange(%q): %v", testCase.rangeIn, err)
			continue
		}
		version, err := ParseVersion(testCase.version)
		if err != nil {
			t.Errorf("ParseVersion(%q): %v", testCase.version, err)
			continue
		}
		if got := allowed.Contains(version); got != testCase.want {
			t.Errorf("%q contains %q = %v, want %v", testCase.rangeIn, testCase.version, got, testCase.want)
		}
	}

	// An empty range is an error rather than "any version": an unauthored
	// field and a deliberately open one must not be indistinguishable.
	if _, err := ParseRange(""); !errors.Is(err, ErrUnsupportedRange) {
		t.Errorf("empty range error = %v, want ErrUnsupportedRange", err)
	}
	// Caret and tilde mean different things in different ecosystems. A
	// compatibility range a reader can misread is worse than one they cannot
	// write.
	for _, unsupported := range []string{"^1.0.0", "~1.0", "1.x", "1.0.0-rc.1"} {
		if _, err := ParseRange(unsupported); err == nil {
			t.Errorf("ParseRange(%q) was accepted", unsupported)
		}
	}
}

// TestMaterializeWalksTheStateMachine covers each transition and each terminal
// projection, including the ordering: trust is evaluated before compatibility,
// so an untrusted definition is never described as merely out of range.
func TestMaterializeWalksTheStateMachine(t *testing.T) {
	t.Parallel()
	manifest, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	material := Material{
		ManifestSource: []byte(validManifest),
		RequestSchema:  []byte(`{"type":"object"}`),
		SourceLocator:  "embedded://packages/tangent.fixture/fixture/manifest.yaml",
	}
	policy := HostPolicy{HostVersion: "v0.12.0", ProtocolVersion: "1"}

	available, err := Materialize(manifest, material, policy)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if available.State != StateAvailable || !available.State.Servable() {
		t.Fatalf("state = %q (%s), want available", available.State, available.StateReason)
	}
	if available.Assurance != AssuranceContentAddressedRegistry {
		t.Errorf("assurance = %q, want the manifest's verified request", available.Assurance)
	}
	if _, ok := available.SafeFallback(); ok {
		t.Error("a fallback that does not preserve meaning was offered as safe")
	}

	// Missing material: resolved is never reached.
	missing, err := Materialize(manifest, Material{ManifestSource: material.ManifestSource}, policy)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if missing.State != StateUnavailable || missing.ErrorCode != ErrorCodeComponentLoadFailed {
		t.Errorf("missing schema = %q/%q, want unavailable/component-load-failed",
			missing.State, missing.ErrorCode)
	}

	// Host out of range: incompatible, with the version code.
	outOfRange, err := Materialize(manifest, material, HostPolicy{
		HostVersion: "v1.5.0", ProtocolVersion: "1",
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if outOfRange.State != StateIncompatible || outOfRange.ErrorCode != ErrorCodeUnsupportedVersion {
		t.Errorf("out-of-range host = %q/%q, want incompatible/unsupported-version",
			outOfRange.State, outOfRange.ErrorCode)
	}

	// Manifest format major this build does not implement.
	futureFormat, err := Parse(edit(t, `manifest_version: "1.0.0"`, `manifest_version: "2.0.0"`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	future, err := Materialize(futureFormat, material, policy)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if future.State != StateIncompatible {
		t.Errorf("unimplemented manifest major = %q, want incompatible", future.State)
	}
}

// TestOptionalCapabilityDegradesInsteadOfQuarantining is the one case where a
// denied capability is not fatal. ADR 0003 §2.5 makes that the publisher's
// explicit choice: `optional: true` means "denial degrades the renderer", and a
// host that quarantined anyway would make the flag meaningless.
func TestOptionalCapabilityDegradesInsteadOfQuarantining(t *testing.T) {
	t.Parallel()
	material := Material{
		ManifestSource: []byte(validManifest),
		RequestSchema:  []byte(`{"type":"object"}`),
	}
	policy := HostPolicy{HostVersion: "v0.12.0", ProtocolVersion: "1"}

	required, err := Parse(edit(t, "required_capabilities: []",
		"required_capabilities:\n  - id: export.download\n    rationale: \"Export the review.\""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	quarantined, err := Materialize(required, material, policy)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if quarantined.State != StateQuarantined || quarantined.ErrorCode != ErrorCodeCapabilityDenied {
		t.Fatalf("ungranted required capability = %q/%q, want quarantined/capability-denied",
			quarantined.State, quarantined.ErrorCode)
	}
	if quarantined.QuarantineReason == "" {
		t.Error("a quarantine must explain itself")
	}

	optional, err := Parse(edit(t, "required_capabilities: []",
		"required_capabilities:\n  - id: export.download\n    optional: true\n    rationale: \"Nice to have.\""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	degraded, err := Materialize(optional, material, policy)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if degraded.State != StateAvailable {
		t.Fatalf("optional capability denial = %q, want available", degraded.State)
	}
	if len(degraded.GrantedCapabilities) != 0 || len(degraded.DeniedCapabilities) != 1 {
		t.Errorf("granted=%v denied=%v; the denial must still be recorded",
			degraded.GrantedCapabilities, degraded.DeniedCapabilities)
	}

	// Granting it moves the same capability to the other side of the
	// intersection, which is what gets persisted into the binding.
	granted, err := Materialize(required, material, HostPolicy{
		HostVersion: "v0.12.0", ProtocolVersion: "1",
		GrantableCapabilities: map[string]bool{"export.download": true},
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if granted.State != StateAvailable || len(granted.GrantedCapabilities) != 1 {
		t.Fatalf("granted capability = %q with %d grants", granted.State, len(granted.GrantedCapabilities))
	}
}

// TestHostCapsThePublishersInlinePayloadLimit covers §2.6's "publisher, capped
// by Tangent": a definition may ask for less inline payload than the host
// allows, never more.
func TestHostCapsThePublishersInlinePayloadLimit(t *testing.T) {
	t.Parallel()
	manifest, err := Parse([]byte(validManifest))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	materialized, err := Materialize(manifest, Material{
		ManifestSource: []byte(validManifest), RequestSchema: []byte(`{"type":"object"}`),
	}, HostPolicy{
		HostVersion: "v0.12.0", ProtocolVersion: "1", InlinePayloadCeilingBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if materialized.EffectiveInlineLimitBytes != 1024 {
		t.Fatalf("effective inline limit = %d, want the host ceiling 1024",
			materialized.EffectiveInlineLimitBytes)
	}
	if materialized.Manifest.InlinePayloadLimitBytes != 262144 {
		t.Error("capping rewrote the publisher's declaration instead of overriding its effect")
	}
}
