// Package definition owns the interaction definition manifest: the single
// immutable document a definition publisher authors for one kind@version, and
// the derived identity Tangent computes from it.
//
// The manifest is specified by docs/adr/0003-definition-and-package-ownership.md
// §2. Field names here are that section's names verbatim, so a reader can move
// between the ADR table and this struct without a translation step.
//
// This package deliberately depends on nothing but the standard library and a
// YAML parser. In particular it does not import go-envelopes: the manifest
// format is publisher-owned, and a tool that only wants to read or digest a
// manifest must not have to load a registry to do it. The registry adapter
// lives in internal/envelope; the pinned projection lives in
// internal/interaction.
//
// Nothing in this package authors a derived field. Every digest is computed
// from authored bytes, and Parse rejects a manifest that tries to supply one.
package definition

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// SupportedManifestMajor is the manifest *format* major version this build
// implements. A manifest declaring a different major is `incompatible` — it is
// not a parse error, because the registry must be able to list and explain a
// manifest it refuses to serve (ADR 0003 §8 C7).
const SupportedManifestMajor = 1

// OwnershipClass is the publisher's declared answer to the ADR 0003 §7
// decision tests. Tangent checks it against the publisher namespace rather
// than trusting it (§2.1).
type OwnershipClass string

const (
	// OwnershipGenericCatalog is the go-envelopes generic catalog. Under ADR
	// 0003 §6 it is a *destination*: a kind may carry the label while still
	// shipping in Tangent's tree, because no upstream move is scheduled.
	OwnershipGenericCatalog OwnershipClass = "generic-catalog"
	// OwnershipHostPackage is a Tangent-distributed package (§7 T3).
	OwnershipHostPackage OwnershipClass = "host-package"
	// OwnershipApplicationPackage is publisher-owned (§7 T4), whether or not
	// it currently ships bundled with the Tangent release.
	OwnershipApplicationPackage OwnershipClass = "application-package"
)

func (c OwnershipClass) valid() bool {
	switch c {
	case OwnershipGenericCatalog, OwnershipHostPackage, OwnershipApplicationPackage:
		return true
	}
	return false
}

// RendererClass is the isolation shape a renderer runs in (§2.3).
type RendererClass string

const (
	RendererReactComponent  RendererClass = "react-component"
	RendererDeclarative     RendererClass = "declarative"
	RendererSandboxedFrame  RendererClass = "sandboxed-frame"
	RendererExternalSurface RendererClass = "external-surface"
)

func (c RendererClass) valid() bool {
	switch c {
	case RendererReactComponent, RendererDeclarative, RendererSandboxedFrame, RendererExternalSurface:
		return true
	}
	return false
}

// TrustClass is the renderer trust level a manifest *requests*. Tangent policy
// decides what is granted and never grants more than the trust evidence in
// §2.7 supports. This is the field CW-20260825-0073 binds to.
type TrustClass string

const (
	TrustCoreTrusted      TrustClass = "core-trusted"
	TrustPortfolioTrusted TrustClass = "portfolio-trusted"
	TrustDeclarative      TrustClass = "declarative"
	TrustSandboxedCode    TrustClass = "sandboxed-code"
	TrustExternalSurface  TrustClass = "external-surface"
)

func (c TrustClass) valid() bool {
	switch c {
	case TrustCoreTrusted, TrustPortfolioTrusted, TrustDeclarative, TrustSandboxedCode, TrustExternalSurface:
		return true
	}
	return false
}

// Degradation describes how much of the interaction a fallback renderer can
// still present (§2.3).
type Degradation string

const (
	DegradationNone       Degradation = "none"
	DegradationReadOnly   Degradation = "read-only"
	DegradationRawPayload Degradation = "raw-payload"
)

func (d Degradation) valid() bool {
	switch d {
	case DegradationNone, DegradationReadOnly, DegradationRawPayload:
		return true
	}
	return false
}

// CompatibilityClass is the publisher's claim about this version relative to
// the previous version of the same kind (§2.4).
type CompatibilityClass string

const (
	CompatibilityAdditive CompatibilityClass = "additive"
	CompatibilityBreaking CompatibilityClass = "breaking"
)

func (c CompatibilityClass) valid() bool {
	return c == CompatibilityAdditive || c == CompatibilityBreaking
}

// ResponseSchemaCompatibility records whether a definition carries a real
// response schema. ADR 0003 §9 S2 makes `response_schema` mandatory for every
// *new* definition; the shipped kinds declare `absent` under C4 and preserve
// today's response-kind-only check until CW-20260825-0074 backfills them.
type ResponseSchemaCompatibility string

const (
	ResponseSchemaPresent ResponseSchemaCompatibility = "present"
	ResponseSchemaAbsent  ResponseSchemaCompatibility = "absent"
)

// DraftCustody is a truthful description of where the publisher expects drafts
// to live (§2.6). It is never a grant: effective browser persistence still
// derives from ADR 0002 §5.
type DraftCustody string

const (
	DraftCustodyDisabled         DraftCustody = "disabled"
	DraftCustodyEphemeral        DraftCustody = "ephemeral"
	DraftCustodyBrowserLocal     DraftCustody = "browser-local"
	DraftCustodyTangentCustodied DraftCustody = "tangent-custodied"
	DraftCustodyExternal         DraftCustody = "external"
)

func (c DraftCustody) valid() bool {
	switch c {
	case DraftCustodyDisabled, DraftCustodyEphemeral, DraftCustodyBrowserLocal,
		DraftCustodyTangentCustodied, DraftCustodyExternal:
		return true
	}
	return false
}

// Sensitivity is the publisher's default sensitivity for this kind (§2.6). A
// caller may request stricter; a package can never weaken host policy.
type Sensitivity string

const (
	SensitivityNormal     Sensitivity = "normal"
	SensitivitySensitive  Sensitivity = "sensitive"
	SensitivityRestricted Sensitivity = "restricted"
)

func (s Sensitivity) valid() bool {
	switch s {
	case SensitivityNormal, SensitivitySensitive, SensitivityRestricted:
		return true
	}
	return false
}

// Assurance is how a manifest was obtained and verified (§2.7). Tangent
// derives it at materialization; a manifest requests it and never decides it.
//
// AssuranceUnverified is a reserved enum value with no v0.x producer: a
// manifest that resolves to neither in-tree-build nor
// content-addressed-registry fails registration rather than materializing as
// unverified. Reserving it keeps CW-20260825-0073 from needing a format
// change. It must never become a permissive fallback.
type Assurance string

const (
	AssuranceInTreeBuild              Assurance = "in-tree-build"
	AssuranceContentAddressedRegistry Assurance = "content-addressed-registry"
	AssuranceSignedPackage            Assurance = "signed-package"
	AssuranceUnverified               Assurance = "unverified"
)

// Grantable reports whether an assurance value is one a v0.x path may
// materialize. signed-package is expressible but has no verifier yet, so it
// is not grantable either.
func (a Assurance) Grantable() bool {
	return a == AssuranceInTreeBuild || a == AssuranceContentAddressedRegistry
}

func (a Assurance) valid() bool {
	switch a {
	case AssuranceInTreeBuild, AssuranceContentAddressedRegistry, AssuranceSignedPackage, AssuranceUnverified:
		return true
	}
	return false
}

// Retention is the retention half of ADR 0002 §1's custody pair. The ordering
// ephemeral < interaction < surface < durable-record is what the precedence
// min() in ADR 0002 §3 operates over.
type Retention string

const (
	RetentionEphemeral     Retention = "ephemeral"
	RetentionInteraction   Retention = "interaction"
	RetentionSurface       Retention = "surface"
	RetentionDurableRecord Retention = "durable-record"
	ContentModeInline                = "inline"
	ContentModeExternalRef           = "external-reference"
)

// RetentionClass is ADR 0002 §1's custody pair. It is a pair and not a flat
// five-value scale; the single-token shorthands expand into it.
//
// A nil *RetentionClass on a manifest means the publisher authored nothing,
// which is the deliberate C4 state for every shipped kind: leaving it
// unauthored is what lets ADR 0002 §4's host default (`interaction`, redacted
// 30 days after terminal) govern new interactions. Authoring `surface` here
// would re-loosen, through the min() in ADR 0002 §3, exactly the default ADR
// 0002 tightened.
type RetentionClass struct {
	Retention   Retention `yaml:"retention" json:"retention"`
	ContentMode string    `yaml:"content_mode" json:"content_mode"`
}

// UnmarshalYAML accepts either the pair form or one of ADR 0002 §1's
// single-token shorthands.
func (r *RetentionClass) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var token string
		if err := node.Decode(&token); err != nil {
			return err
		}
		expanded, err := ExpandRetentionShorthand(token)
		if err != nil {
			return err
		}
		*r = expanded
		return nil
	}
	type plain RetentionClass
	var out plain
	if err := node.Decode(&out); err != nil {
		return err
	}
	*r = RetentionClass(out)
	return nil
}

// ExpandRetentionShorthand expands ADR 0002 §1's single-token custody
// shorthand into the canonical pair. `external-reference` is not a fifth
// position on the retention scale — it is `interaction` retention with the
// content held elsewhere.
func ExpandRetentionShorthand(token string) (RetentionClass, error) {
	switch Retention(token) {
	case RetentionEphemeral, RetentionInteraction, RetentionSurface, RetentionDurableRecord:
		return RetentionClass{Retention: Retention(token), ContentMode: ContentModeInline}, nil
	}
	if token == ContentModeExternalRef {
		return RetentionClass{Retention: RetentionInteraction, ContentMode: ContentModeExternalRef}, nil
	}
	return RetentionClass{}, fmt.Errorf("%w: unknown retention_class %q", ErrInvalidManifest, token)
}

func (r RetentionClass) valid() bool {
	switch r.Retention {
	case RetentionEphemeral, RetentionInteraction, RetentionSurface, RetentionDurableRecord:
	default:
		return false
	}
	return r.ContentMode == ContentModeInline || r.ContentMode == ContentModeExternalRef
}

// Capability is one host-mediated *effect* the renderer cannot perform on its
// own authority (§2.5).
//
// This namespace is not the object-access capability namespace in ADR 0004 §2
// (view, submit, draft, resolve, cancel, close, administer), which governs who
// may perform an operation on a surface or interaction. The two never
// substitute for one another: a granted `export.download` never implies
// `resolve`, and a participant's `resolve` never implies `file.read_scoped`.
// CW-20260825-0077 consumes both and must keep them separately named.
type Capability struct {
	// ID is a namespaced grant name — clipboard.write, export.download,
	// file.read_scoped, network.fetch, process.exec.
	ID string `yaml:"id" json:"id"`
	// Scope is the grant-shaped constraint (allowed roots, allowed origins,
	// byte ceilings). A bare path string is not a capability.
	Scope map[string]any `yaml:"scope,omitempty" json:"scope,omitempty"`
	// Optional marks a capability whose denial degrades the renderer rather
	// than failing resolution.
	Optional bool `yaml:"optional,omitempty" json:"optional,omitempty"`
	// Rationale is shown to the user at grant time.
	Rationale string `yaml:"rationale,omitempty" json:"rationale,omitempty"`
}

// Fallback is the publisher's declared safe fallback renderer (§2.3).
type Fallback struct {
	RendererID string `yaml:"renderer_id,omitempty" json:"renderer_id,omitempty"`
	// PreservesMeaning states whether the fallback still captures the
	// interaction's required decision. Tangent uses the fallback only when
	// this is true (ADR 0003 §8 C5).
	PreservesMeaning bool        `yaml:"preserves_meaning" json:"preserves_meaning"`
	Degradation      Degradation `yaml:"degradation,omitempty" json:"degradation,omitempty"`
}

// Renderer is the manifest's single answer to "what draws this kind", which
// replaces the three unrelated conventions ADR 0003's context section
// describes (a dead `ui.component` slug, a string-literal React registration,
// and one bespoke route).
type Renderer struct {
	ID    string        `yaml:"id" json:"id"`
	Class RendererClass `yaml:"class" json:"class"`
	// Entry is a module specifier / exported symbol for react-component, a
	// Sigil page reference for declarative, a frame entry for
	// sandboxed-frame, or a handoff URL template for external-surface.
	Entry string `yaml:"entry,omitempty" json:"entry,omitempty"`
	// AssetDigest is the digest of a renderer bundle that ships separately
	// from the host binary. Empty for in-tree renderers built with the
	// release.
	AssetDigest string     `yaml:"asset_digest,omitempty" json:"asset_digest,omitempty"`
	TrustClass  TrustClass `yaml:"trust_class" json:"trust_class"`
	Fallback    Fallback   `yaml:"fallback,omitempty" json:"fallback,omitzero"`
	// Presentation carries non-authoritative accessibility and layout hints.
	Presentation map[string]any `yaml:"presentation,omitempty" json:"presentation,omitempty"`
	// Component is the legacy `ui.component` slug. It is retained because the
	// generated EnvelopeKindMap and the committed TypeScript are keyed on it;
	// Entry is the authoritative binding.
	Component string `yaml:"component,omitempty" json:"component,omitempty"`
}

// Trust is §2.7's evidence block. Named trust.* deliberately: `evidence` is
// already taken in the HITL contract for participant-facing decision material
// (internal/hitl/evidence.go) and the two concepts must not be confused.
type Trust struct {
	// Assurance is what the manifest requests. Tangent derives the granted
	// value at materialization and never copies this field through unchecked.
	Assurance Assurance `yaml:"assurance" json:"assurance"`
	// Signature is a detached signature over manifest_digest. Not implemented
	// in v0.x; the field exists so signed-package is expressible without a
	// format change.
	Signature   string `yaml:"signature,omitempty" json:"signature,omitempty"`
	SignerKeyID string `yaml:"signer_key_id,omitempty" json:"signer_key_id,omitempty"`
}

// Telemetry is §2.8. Tangent has no telemetry subsystem yet; these fields are
// declared now so adding one later is not a manifest-format break, and so
// redact_fields is authored beside the schema that needs it.
type Telemetry struct {
	// Emits lists typed event names the renderer may emit. An event not
	// declared here is dropped by the host.
	Emits []string `yaml:"emits,omitempty" json:"emits,omitempty"`
	// RedactFields are JSON pointers into request/response that must never
	// appear in a log, event, or trace. Additive tightening on top of ADR
	// 0002 §8's floor; it can never widen what that floor permits.
	RedactFields []string `yaml:"redact_fields,omitempty" json:"redact_fields,omitempty"`
	// OptIn defaults to true. Because Go's zero value is false and YAML
	// absence is indistinguishable from an authored false, this is a pointer:
	// nil means unauthored and reads as true.
	OptIn *bool `yaml:"opt_in,omitempty" json:"opt_in,omitempty"`
}

// TelemetryOptIn reports the effective value, defaulting to true.
func (t Telemetry) TelemetryOptIn() bool { return t.OptIn == nil || *t.OptIn }

// Manifest is one authored interaction definition manifest.
//
// Field order is the ADR's field order, and it is load-bearing: ManifestDigest
// canonicalizes by marshaling this struct, and encoding/json emits struct
// fields in declaration order. Reordering fields changes every digest.
type Manifest struct {
	// ── §2.1 Identity ───────────────────────────────────────────────
	ManifestVersion string         `yaml:"manifest_version" json:"manifest_version"`
	Publisher       string         `yaml:"publisher" json:"publisher"`
	Kind            string         `yaml:"kind" json:"kind"`
	Version         string         `yaml:"version" json:"version"`
	Revision        int64          `yaml:"revision" json:"revision"`
	Title           string         `yaml:"title,omitempty" json:"title,omitempty"`
	Description     string         `yaml:"description,omitempty" json:"description,omitempty"`
	PackageID       string         `yaml:"package_id" json:"package_id"`
	PackageVersion  string         `yaml:"package_version" json:"package_version"`
	OwnershipClass  OwnershipClass `yaml:"ownership_class" json:"ownership_class"`

	// ── §2.2 Contract ───────────────────────────────────────────────
	// The three schema fields are package-relative file names — the "$ref
	// into the package" form. Inline schema bodies are expressible in the
	// ADR but not implemented in v0.x; a file has a digest-able identity and
	// an inline literal does not, which is the same reason manifests are
	// files rather than Go []byte.
	RequestSchemaRef  string            `yaml:"request_schema" json:"request_schema"`
	ResponseKind      string            `yaml:"response_kind" json:"response_kind"`
	ResponseSchemaRef string            `yaml:"response_schema,omitempty" json:"response_schema,omitempty"`
	ErrorSchemaRef    string            `yaml:"error_schema,omitempty" json:"error_schema,omitempty"`
	NamedDefinitions  map[string]string `yaml:"named_definitions,omitempty" json:"named_definitions,omitempty"`

	// ── §2.3 Renderer ───────────────────────────────────────────────
	Renderer Renderer `yaml:"renderer" json:"renderer"`

	// ── §2.4 Versions and compatibility ─────────────────────────────
	CompatibleHostVersions     string             `yaml:"compatible_host_versions" json:"compatible_host_versions"`
	CompatibleProtocolVersions string             `yaml:"compatible_protocol_versions" json:"compatible_protocol_versions"`
	CompatibleSDKVersions      string             `yaml:"compatible_sdk_versions,omitempty" json:"compatible_sdk_versions,omitempty"`
	Supersedes                 []string           `yaml:"supersedes,omitempty" json:"supersedes,omitempty"`
	CompatibilityClass         CompatibilityClass `yaml:"compatibility_class" json:"compatibility_class"`
	// CompatibilityResponseSchema is the C4 marker. `absent` preserves
	// today's response-kind-only check rather than pretending a schema
	// exists; it must never be authored alongside a response_schema.
	CompatibilityResponseSchema ResponseSchemaCompatibility `yaml:"compatibility_response_schema,omitempty" json:"compatibility_response_schema,omitempty"`

	// ── §2.5 Capabilities (renderer-effect namespace) ───────────────
	RequiredCapabilities []Capability `yaml:"required_capabilities" json:"required_capabilities"`

	// ── §2.6 Persistence and sensitivity ────────────────────────────
	DraftCustody DraftCustody `yaml:"draft_custody" json:"draft_custody"`
	// RetentionClass is nil when the publisher authored nothing. See the
	// RetentionClass doc comment for why every shipped kind leaves it nil.
	RetentionClass              *RetentionClass `yaml:"retention_class,omitempty" json:"retention_class,omitempty"`
	SensitivityDefault          Sensitivity     `yaml:"sensitivity_default" json:"sensitivity_default"`
	InlinePayloadLimitBytes     int64           `yaml:"inline_payload_limit_bytes" json:"inline_payload_limit_bytes"`
	ClientPersistenceProhibited bool            `yaml:"client_persistence_prohibited" json:"client_persistence_prohibited"`

	// ── §2.7 Trust evidence ─────────────────────────────────────────
	Trust Trust `yaml:"trust" json:"trust"`

	// ── §2.8 Telemetry ──────────────────────────────────────────────
	Telemetry Telemetry `yaml:"telemetry,omitempty" json:"telemetry,omitzero"`
}

// Parse decodes and validates one authored manifest. It rejects any attempt to
// author a derived field, because a manifest that could name its own digest
// could lie about it.
//
// A manifest whose format major is unsupported parses successfully: the
// registry has to be able to list and explain a definition it will not serve
// (ADR 0003 §8 C7), and Materialize is where that becomes `incompatible`.
func Parse(source []byte) (*Manifest, error) {
	var probe map[string]any
	if err := yaml.Unmarshal(source, &probe); err != nil {
		return nil, fmt.Errorf("%w: parse YAML: %w", ErrInvalidManifest, err)
	}
	for _, derived := range derivedFieldNames {
		if _, present := probe[derived]; present {
			return nil, fmt.Errorf(
				"%w: %q is derived by Tangent and must not be authored", ErrInvalidManifest, derived)
		}
	}

	var manifest Manifest
	decoder := yaml.NewDecoder(strings.NewReader(string(source)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("%w: decode manifest: %w", ErrInvalidManifest, err)
	}
	if err := manifest.validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// derivedFieldNames are the §2.9 fields plus the two §2.2 derived identities.
// Authoring any of them is a publisher error rather than a value Tangent
// silently overwrites.
var derivedFieldNames = []string{
	"manifest_digest", "contract_digest", "binding_digest", "validator_revision",
	"request_schema_identity", "request_schema_digest", "response_schema_digest",
	"granted_capabilities", "source",
}

func (m *Manifest) validate() error {
	if m.ManifestVersion == "" {
		return fmt.Errorf("%w: manifest_version is required", ErrInvalidManifest)
	}
	if m.Publisher == "" {
		return fmt.Errorf("%w: publisher is required", ErrInvalidManifest)
	}
	if m.Kind == "" {
		return fmt.Errorf("%w: kind is required", ErrInvalidManifest)
	}
	if m.Version == "" {
		return fmt.Errorf("%w: %s: version is required", ErrInvalidManifest, m.Kind)
	}
	if m.Revision < 1 {
		// revision is monotonic within a version and starts at 1. Zero would
		// be indistinguishable from "unauthored", and ADR 0003 §3 keeps this
		// field genuinely separate from version precisely so it can advance.
		return fmt.Errorf("%w: %s: revision must be >= 1", ErrInvalidManifest, m.Kind)
	}
	if m.PackageID == "" || m.PackageVersion == "" {
		return fmt.Errorf("%w: %s: package_id and package_version are required", ErrInvalidManifest, m.Kind)
	}
	if !m.OwnershipClass.valid() {
		return fmt.Errorf("%w: %s: unknown ownership_class %q", ErrInvalidManifest, m.Kind, m.OwnershipClass)
	}
	if m.RequestSchemaRef == "" {
		return fmt.Errorf("%w: %s: request_schema is required", ErrInvalidManifest, m.Kind)
	}
	switch m.ResponseKind {
	case "data", "ack", "ui", "async-ack", "error":
	default:
		return fmt.Errorf("%w: %s: unknown response_kind %q", ErrInvalidManifest, m.Kind, m.ResponseKind)
	}
	if err := m.validateResponseSchemaCompatibility(); err != nil {
		return err
	}
	if err := m.validateRenderer(); err != nil {
		return err
	}
	if m.CompatibleHostVersions == "" || m.CompatibleProtocolVersions == "" {
		return fmt.Errorf(
			"%w: %s: compatible_host_versions and compatible_protocol_versions are required",
			ErrInvalidManifest, m.Kind)
	}
	if _, err := ParseRange(m.CompatibleHostVersions); err != nil {
		return fmt.Errorf("%w: %s: compatible_host_versions: %w", ErrInvalidManifest, m.Kind, err)
	}
	if _, err := ParseRange(m.CompatibleProtocolVersions); err != nil {
		return fmt.Errorf("%w: %s: compatible_protocol_versions: %w", ErrInvalidManifest, m.Kind, err)
	}
	if m.CompatibleSDKVersions != "" {
		if _, err := ParseRange(m.CompatibleSDKVersions); err != nil {
			return fmt.Errorf("%w: %s: compatible_sdk_versions: %w", ErrInvalidManifest, m.Kind, err)
		}
	}
	if !m.CompatibilityClass.valid() {
		return fmt.Errorf("%w: %s: unknown compatibility_class %q", ErrInvalidManifest, m.Kind, m.CompatibilityClass)
	}
	for i, capability := range m.RequiredCapabilities {
		if capability.ID == "" {
			return fmt.Errorf("%w: %s: required_capabilities[%d] has no id", ErrInvalidManifest, m.Kind, i)
		}
		if !strings.Contains(capability.ID, ".") {
			return fmt.Errorf(
				"%w: %s: capability %q must be namespaced (e.g. file.read_scoped)",
				ErrInvalidManifest, m.Kind, capability.ID)
		}
	}
	if !m.DraftCustody.valid() {
		return fmt.Errorf("%w: %s: unknown draft_custody %q", ErrInvalidManifest, m.Kind, m.DraftCustody)
	}
	if m.RetentionClass != nil && !m.RetentionClass.valid() {
		return fmt.Errorf("%w: %s: invalid retention_class %+v", ErrInvalidManifest, m.Kind, *m.RetentionClass)
	}
	if !m.SensitivityDefault.valid() {
		return fmt.Errorf("%w: %s: unknown sensitivity_default %q", ErrInvalidManifest, m.Kind, m.SensitivityDefault)
	}
	if m.InlinePayloadLimitBytes <= 0 {
		return fmt.Errorf("%w: %s: inline_payload_limit_bytes must be > 0", ErrInvalidManifest, m.Kind)
	}
	if !m.Trust.Assurance.valid() {
		return fmt.Errorf("%w: %s: unknown trust.assurance %q", ErrInvalidManifest, m.Kind, m.Trust.Assurance)
	}
	if m.Trust.Assurance == AssuranceUnverified {
		// The value is reserved so signed-package and future classes are
		// expressible without a format change, but no v0.x path produces it.
		// Refusing it at parse time is what keeps it from becoming the
		// permissive fallback ADR 0003 rejected by name.
		return fmt.Errorf(
			"%w: %s: trust.assurance %q is reserved and has no v0.x producer",
			ErrInvalidManifest, m.Kind, AssuranceUnverified)
	}
	return m.validateOwnershipNamespace()
}

func (m *Manifest) validateResponseSchemaCompatibility() error {
	switch m.CompatibilityResponseSchema {
	case ResponseSchemaAbsent:
		if m.ResponseSchemaRef != "" {
			return fmt.Errorf(
				"%w: %s: compatibility_response_schema: absent contradicts an authored response_schema",
				ErrInvalidManifest, m.Kind)
		}
		return nil
	case ResponseSchemaPresent, "":
		if m.ResponseSchemaRef == "" {
			// ADR 0003 §9 S2: response_schema is mandatory for every new
			// definition. A kind that genuinely has none must say so with the
			// C4 marker, which is a legible compatibility statement rather
			// than an omission.
			return fmt.Errorf(
				"%w: %s: response_schema is mandatory; a shipped kind without one must declare "+
					"compatibility_response_schema: absent", ErrInvalidManifest, m.Kind)
		}
		return nil
	default:
		return fmt.Errorf("%w: %s: unknown compatibility_response_schema %q",
			ErrInvalidManifest, m.Kind, m.CompatibilityResponseSchema)
	}
}

func (m *Manifest) validateRenderer() error {
	if m.Renderer.ID == "" {
		return fmt.Errorf("%w: %s: renderer.id is required", ErrInvalidManifest, m.Kind)
	}
	if !m.Renderer.Class.valid() {
		return fmt.Errorf("%w: %s: unknown renderer.class %q", ErrInvalidManifest, m.Kind, m.Renderer.Class)
	}
	if !m.Renderer.TrustClass.valid() {
		return fmt.Errorf("%w: %s: unknown renderer.trust_class %q", ErrInvalidManifest, m.Kind, m.Renderer.TrustClass)
	}
	if m.Renderer.Fallback.Degradation != "" && !m.Renderer.Fallback.Degradation.valid() {
		return fmt.Errorf("%w: %s: unknown renderer.fallback.degradation %q",
			ErrInvalidManifest, m.Kind, m.Renderer.Fallback.Degradation)
	}
	if m.Renderer.Fallback.PreservesMeaning && m.Renderer.Fallback.RendererID == "" {
		return fmt.Errorf(
			"%w: %s: renderer.fallback.preserves_meaning is true but no fallback renderer_id is declared",
			ErrInvalidManifest, m.Kind)
	}
	// ADR 0003 §7 T6, generalized. The narrow form of this rule was
	// "sandboxed-frame cannot request core-trusted", which caught one pair out
	// of twenty. The general rule is that a renderer's *shape* and its trust
	// class have to describe the same thing: a `react-component` calling
	// itself `sandboxed-code` is not sandboxed, it is mislabeled, and a
	// `sandboxed-frame` calling itself `core-trusted` is untrusted markup
	// claiming the host's own authority. Both are refused by the same table
	// (see trust.go), so the trust decision cannot be smuggled in through a
	// renderer convention in either direction.
	profile, ok := TrustProfileFor(m.Renderer.TrustClass)
	if !ok {
		return fmt.Errorf("%w: %s: renderer.trust_class %q has no profile in this build",
			ErrInvalidManifest, m.Kind, m.Renderer.TrustClass)
	}
	if !profile.AdmitsRendererClass(m.Renderer.Class) {
		return fmt.Errorf(
			"%w: %s: renderer.class %q cannot request trust_class %q",
			ErrInvalidManifest, m.Kind, m.Renderer.Class, m.Renderer.TrustClass)
	}
	return nil
}

// validateOwnershipNamespace rejects a manifest whose declared ownership class
// contradicts its publisher namespace (§2.1). It is the one ownership check
// Tangent can make mechanically; the §7 decision tests are a review
// obligation, not a compiler.
func (m *Manifest) validateOwnershipNamespace() error {
	if m.OwnershipClass == OwnershipHostPackage && m.Publisher != TangentPublisher {
		return fmt.Errorf(
			"%w: %s: ownership_class host-package requires publisher %q, got %q",
			ErrInvalidManifest, m.Kind, TangentPublisher, m.Publisher)
	}
	if m.OwnershipClass == OwnershipGenericCatalog && !strings.HasPrefix(m.PackageID, "tangent.") &&
		m.Publisher != CorePublisher {
		return fmt.Errorf(
			"%w: %s: ownership_class generic-catalog requires publisher %q or a bundled tangent.* package",
			ErrInvalidManifest, m.Kind, CorePublisher)
	}
	return nil
}

// TangentPublisher is the publisher identity of every kind Tangent itself
// registers. It matches the go-envelopes plugin id so TypeSpec.PluginID and
// the manifest agree without a translation table.
const TangentPublisher = "tangent"

// CorePublisher is the publisher identity go-envelopes core definitions carry.
const CorePublisher = "hollis-labs/go-envelopes"
