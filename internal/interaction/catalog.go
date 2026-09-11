package interaction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"
	jsonschemav6 "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/envelope"
)

var (
	ErrDefinitionNotFound    = errors.New("interaction service: definition not found")
	ErrDefinitionUnavailable = errors.New("interaction service: pinned definition unavailable")
	ErrDefinitionValidation  = errors.New("interaction service: definition validation failed")
)

// envelopeDefinitionValidatorRevision is part of every binding digest. Any
// future semantic change to request or response validation must change this
// value so old records fail closed instead of being reinterpreted by new code.
//
// It is at v2 because this build changed what validation means: a pinned
// binding now resolves its material by digest from a retained, durable copy
// rather than from the live registry, and a response is validated against the
// pinned response schema when the definition carries one. Old records must not
// be re-read as though they had been validated that way.
const envelopeDefinitionValidatorRevision = "tangent-envelope-definition-validator-v2"

type DefinitionRef struct {
	Kind    string `json:"kind"`
	Version string `json:"version,omitempty"`
}

// InteractionKind is one entry in the registry listing. State and its
// explanation are part of the projection because ADR 0003 §8 C7 requires
// incompatible, quarantined, and unavailable to be distinguishable without
// reading a log — "unavailable is a state, not an error to paper over".
type InteractionKind struct {
	Kind         string            `json:"kind"`
	Version      string            `json:"version"`
	Revision     int64             `json:"revision,omitempty"`
	Title        string            `json:"title,omitempty"`
	Description  string            `json:"description,omitempty"`
	ResponseKind string            `json:"response_kind"`
	PackageID    string            `json:"package_id,omitempty"`
	State        string            `json:"materialization_state,omitempty"`
	StateReason  string            `json:"state_reason,omitempty"`
	ErrorCode    string            `json:"error_code,omitempty"`
	Available    bool              `json:"available"`
	Binding      DefinitionBinding `json:"binding"`
}

// DefinitionStateError explains a refusal in the ADR 0003 §8 C7 vocabulary. It
// unwraps to ErrDefinitionUnavailable so every existing errors.Is check — and
// the definition_unavailable MCP code — keeps working, while a caller that
// wants to know *which* unservable state it hit can ask.
type DefinitionStateError struct {
	Kind    string
	Version string
	// State is one of incompatible, quarantined, or unavailable.
	State string
	// Reason is the human explanation from materialization.
	Reason string
	// ErrorCode is the reused upstream code: unsupported-type,
	// unsupported-version, validation-failed, capability-denied, or
	// component-load-failed. ADR 0003 §8 C7 is explicit that the existing
	// vocabulary is sufficient and is reused rather than extended.
	ErrorCode string
	// Fallback names the safe fallback renderer, when the manifest declared
	// one whose preserves_meaning is true. Empty otherwise, and an empty
	// value means resolution fails closed — never that a degraded surface may
	// be presented anyway.
	Fallback string
}

func (e *DefinitionStateError) Error() string {
	base := fmt.Sprintf("%s@%s is %s (%s): %s", e.Kind, e.Version, e.State, e.ErrorCode, e.Reason)
	if e.Fallback != "" {
		return base + "; safe fallback renderer " + e.Fallback
	}
	return base
}

func (e *DefinitionStateError) Unwrap() error { return ErrDefinitionUnavailable }

// DefinitionCatalog resolves and validates immutable interaction definitions.
// Implementations must compare response validation against the pinned binding,
// never silently reinterpret it through a different current definition.
type DefinitionCatalog interface {
	ListInteractionKinds(context.Context) ([]InteractionKind, error)
	ResolveInteractionDefinition(context.Context, DefinitionRef) (DefinitionBinding, error)
	// RetainDefinitionMaterial durably retains the exact material a binding
	// was cut from. The service calls it at the moment of pinning, which is
	// what makes the pin mean something after the registry moves on.
	RetainDefinitionMaterial(context.Context, DefinitionBinding) error
	ValidateInteractionRequest(context.Context, DefinitionBinding, json.RawMessage) error
	ValidateInteractionResponse(context.Context, DefinitionBinding, string, json.RawMessage) error
}

// CatalogOption customizes an EnvelopeDefinitionCatalog.
type CatalogOption func(*EnvelopeDefinitionCatalog)

// WithRetainedMaterialStore gives the catalog durable retention. Without it the
// catalog resolves pinned material from its in-memory version index alone,
// which is correct but only survives while this process is up and this build
// still ships the definition — enough for a unit test, not enough for a record
// that has to outlive a release.
func WithRetainedMaterialStore(store DefinitionMaterialStore) CatalogOption {
	return func(catalog *EnvelopeDefinitionCatalog) { catalog.materials = store }
}

// EnvelopeDefinitionCatalog adapts the in-process go-envelopes registry to the
// generic interaction service. Its binding digest is derived deterministically
// from the exact schema material supplied by the registry.
type EnvelopeDefinitionCatalog struct {
	service     *envelope.Service
	hostVersion string
	materials   DefinitionMaterialStore
}

func NewEnvelopeDefinitionCatalog(
	service *envelope.Service,
	hostVersion string,
	options ...CatalogOption,
) *EnvelopeDefinitionCatalog {
	catalog := &EnvelopeDefinitionCatalog{service: service, hostVersion: hostVersion}
	for _, option := range options {
		if option != nil {
			option(catalog)
		}
	}
	return catalog
}

func (c *EnvelopeDefinitionCatalog) ListInteractionKinds(_ context.Context) ([]InteractionKind, error) {
	if c == nil || c.service == nil {
		return nil, fmt.Errorf("%w: envelope registry is nil", ErrDefinitionUnavailable)
	}
	specs := c.service.All()
	out := make([]InteractionKind, 0, len(specs))
	for _, spec := range specs {
		material, ok := c.service.LookupDefinitionMaterial(spec.Name)
		if !ok {
			// go-envelopes discards manifest and schema source after
			// compiling, so a core definition has no retained bytes and
			// cannot be content-addressed at all. Failing closed by skipping
			// it is the shipped behavior and stays that way until upstream
			// retains source (ADR 0003 §5).
			continue
		}
		binding, err := c.binding(spec, material)
		if err != nil {
			if errors.Is(err, ErrDefinitionUnavailable) {
				continue
			}
			return nil, err
		}
		entry := InteractionKind{
			Kind: spec.Name, Version: spec.Version, Revision: binding.Revision,
			Description: spec.Description, ResponseKind: string(spec.ResponseKind),
			PackageID: binding.PackageID, Binding: binding,
			// A definition with no manifest predates the registry and is
			// treated as available, exactly as it was before: withdrawing it
			// would be a behavior change dressed as a refactor.
			Available: true,
		}
		if material.Definition != nil {
			entry.Title = material.Definition.Manifest.Title
			entry.State = string(material.Definition.State)
			entry.StateReason = material.Definition.StateReason
			entry.ErrorCode = material.Definition.ErrorCode
			entry.Available = material.Definition.State.Servable()
		}
		out = append(out, entry)
	}
	return out, nil
}

// ResolveInteractionDefinition resolves a kind and optional version to the
// exact binding a submission would pin.
//
// It refuses a definition the host cannot currently serve, with the state and
// the reused upstream error code, rather than returning a binding that would
// then fail somewhere less legible. A registry change never rewrites an
// existing record — it only stops new submissions (ADR 0003 §8 C1).
func (c *EnvelopeDefinitionCatalog) ResolveInteractionDefinition(
	_ context.Context,
	ref DefinitionRef,
) (DefinitionBinding, error) {
	if c == nil || c.service == nil || ref.Kind == "" {
		return DefinitionBinding{}, fmt.Errorf("%w: %q", ErrDefinitionNotFound, ref.Kind)
	}
	spec, ok := c.service.Lookup(ref.Kind)
	if !ok || (ref.Version != "" && ref.Version != spec.Version) {
		return DefinitionBinding{}, fmt.Errorf("%w: %s@%s", ErrDefinitionNotFound, ref.Kind, ref.Version)
	}
	material, ok := c.service.LookupDefinitionMaterial(spec.Name)
	if !ok {
		return DefinitionBinding{}, fmt.Errorf(
			"%w: exact source material for %s@%s is not retained",
			ErrDefinitionUnavailable, spec.Name, spec.Version)
	}
	if material.Definition != nil && !material.Definition.State.Servable() {
		return DefinitionBinding{}, unservableError(*material.Definition)
	}
	return c.binding(spec, material)
}

// unservableError projects a non-available materialization into the typed
// refusal a caller sees. The safe fallback is named only when the manifest
// declared one and preserves_meaning is true (ADR 0003 §8 C5): Tangent never
// downgrades a structured decision to a free-text box, and with no qualifying
// fallback the interaction stays non-terminal and the caller is told the
// definition is unavailable — a renderer problem is not a participant
// cancellation.
func unservableError(materialized definition.Materialized) error {
	failure := &DefinitionStateError{
		Kind:      materialized.Manifest.Kind,
		Version:   materialized.Manifest.Version,
		State:     string(materialized.State),
		Reason:    materialized.StateReason,
		ErrorCode: materialized.ErrorCode,
	}
	if fallback, ok := materialized.SafeFallback(); ok {
		failure.Fallback = fallback.RendererID
	}
	return failure
}

// RetainDefinitionMaterial writes the exact material behind one binding into
// durable storage, keyed on the binding digest. It is called at pin time, and
// it is the whole reason a pinned interaction can still be validated after the
// process restarts, the installed catalog changes, or the current version moves
// on.
//
// Retention is a no-op without a material store — a catalog booted for a unit
// test has no database and does not need one — and a no-op for a definition
// with no manifest, which has nothing beyond what the binding already carries.
func (c *EnvelopeDefinitionCatalog) RetainDefinitionMaterial(
	ctx context.Context,
	binding DefinitionBinding,
) error {
	if c == nil || c.materials == nil || binding.Digest == "" {
		return nil
	}
	material, ok := c.service.LookupDefinitionMaterialVersion(binding.Kind, binding.Version)
	if !ok || material.Definition == nil {
		return nil
	}
	materialized := material.Definition
	compatibility := string(materialized.Manifest.CompatibilityResponseSchema)
	if compatibility == "" {
		compatibility = string(definition.ResponseSchemaPresent)
	}
	return c.materials.PutDefinitionMaterial(ctx, RetainedDefinition{
		BindingDigest: binding.Digest,
		Publisher:     binding.Publisher,
		Kind:          binding.Kind,
		Version:       binding.Version,
		Revision:      binding.Revision,

		ManifestDigest: materialized.Derived.ManifestDigest,
		ContractDigest: materialized.Derived.ContractDigest,

		ManifestSource: material.Manifest,
		RequestSchema:  material.RequestSchema,
		ResponseSchema: material.ResponseSchema,
		ErrorSchema:    material.ErrorSchema,

		ResponseKind:                material.ResponseKind,
		CompatibilityResponseSchema: compatibility,

		SchemaIdentity:       binding.SchemaIdentity,
		RequestSchemaDigest:  materialized.Derived.RequestSchemaDigest,
		ResponseSchemaDigest: materialized.Derived.ResponseSchemaDigest,

		PackageID:          materialized.Manifest.PackageID,
		PackageVersion:     materialized.Manifest.PackageVersion,
		OwnershipClass:     string(materialized.Manifest.OwnershipClass),
		CompatibilityClass: string(materialized.Manifest.CompatibilityClass),

		RendererID:        materialized.Manifest.Renderer.ID,
		RendererClass:     string(materialized.Manifest.Renderer.Class),
		RendererIsolation: string(materialized.Manifest.Renderer.Isolation),

		RequiredCapabilities: string(binding.RequiredCapabilities),
		GrantedCapabilities:  string(binding.GrantedCapabilities),

		TrustAssurance:     binding.Assurance,
		TrustSourceLocator: materialized.SourceLocator,

		HostVersion:          binding.HostVersion,
		ValidatorRevision:    envelopeDefinitionValidatorRevision,
		MaterializationState: string(materialized.State),
		MaterializedAt:       materialized.VerifiedAt,
	})
}

// pinnedMaterial is the exact material behind one binding, from whichever tier
// still holds it.
type pinnedMaterial struct {
	RequestSchema  []byte
	ResponseSchema []byte
	ResponseKind   string
	SchemaIdentity string
	// ResponseSchemaAuthored is false for a kind carrying
	// compatibility_response_schema: absent, which preserves today's
	// response-kind-only check rather than silently rejecting responses
	// production accepts (ADR 0003 §8 C4).
	ResponseSchemaAuthored bool
}

// resolvePinnedMaterial finds the exact bytes a binding was cut from, without
// consulting the *current* registry entry.
//
// Two tiers, in order:
//
//  1. the in-memory version index, accepted only when recomputing the binding
//     digest over its material reproduces the pinned digest. That check is what
//     makes the fast path safe: a same-name/same-version registry replacement
//     performed outside Tangent's registration boundary changes the bytes but
//     not the index key, and serving those bytes would reinterpret the pinned
//     interaction through a definition it was never validated against;
//  2. the durable retained-material table, keyed on the pinned digest alone.
//
// Neither tier asks whether the definition is still current, still registered,
// or still servable. It asks only whether these are the bytes that produced
// this digest. Anything else would make a record's meaning depend on what
// happens to be installed today, which is precisely what ADR 0001 §3's pin
// exists to prevent.
func (c *EnvelopeDefinitionCatalog) resolvePinnedMaterial(
	ctx context.Context,
	binding DefinitionBinding,
) (pinnedMaterial, error) {
	if c == nil || c.service == nil {
		return pinnedMaterial{}, fmt.Errorf("%w: envelope registry is nil", ErrDefinitionUnavailable)
	}
	if indexed, ok := c.service.LookupDefinitionMaterialVersion(binding.Kind, binding.Version); ok {
		if c.digestFor(binding, indexed) == binding.Digest {
			authored := indexed.Definition == nil ||
				indexed.Definition.Manifest.CompatibilityResponseSchema != definition.ResponseSchemaAbsent
			return pinnedMaterial{
				RequestSchema:          indexed.RequestSchema,
				ResponseSchema:         indexed.ResponseSchema,
				ResponseKind:           indexed.ResponseKind,
				SchemaIdentity:         indexed.SchemaIdentity,
				ResponseSchemaAuthored: authored && len(indexed.ResponseSchema) > 0,
			}, nil
		}
	}
	if c.materials != nil {
		retained, found, err := c.materials.GetDefinitionMaterial(ctx, binding.Digest)
		if err != nil {
			return pinnedMaterial{}, fmt.Errorf("%w: %w", ErrDefinitionUnavailable, err)
		}
		if found {
			if retained.ValidatorRevision != envelopeDefinitionValidatorRevision {
				// The validator's semantics changed since this record was
				// pinned. Failing closed is the point of the revision: the
				// alternative is silently re-reading an old record under new
				// rules.
				return pinnedMaterial{}, fmt.Errorf(
					"%w: %s@%s was pinned under validator %q, this build is %q",
					ErrDefinitionUnavailable, binding.Kind, binding.Version,
					retained.ValidatorRevision, envelopeDefinitionValidatorRevision)
			}
			return pinnedMaterial{
				RequestSchema:  retained.RequestSchema,
				ResponseSchema: retained.ResponseSchema,
				ResponseKind:   retained.ResponseKind,
				SchemaIdentity: retained.SchemaIdentity,
				ResponseSchemaAuthored: retained.CompatibilityResponseSchema !=
					string(definition.ResponseSchemaAbsent) && len(retained.ResponseSchema) > 0,
			}, nil
		}
	}
	return pinnedMaterial{}, fmt.Errorf(
		"%w: exact source material for %s@%s is not retained",
		ErrDefinitionUnavailable, binding.Kind, binding.Version)
}

func (c *EnvelopeDefinitionCatalog) ValidateInteractionRequest(
	ctx context.Context,
	binding DefinitionBinding,
	request json.RawMessage,
) error {
	material, materialErr := c.resolvePinnedMaterial(ctx, binding)
	if materialErr != nil {
		return materialErr
	}
	var data any
	if err := json.Unmarshal(request, &data); err != nil {
		return fmt.Errorf("%w: decode request JSON: %w", ErrDefinitionValidation, err)
	}
	if len(material.RequestSchema) == 0 {
		return nil
	}
	return validateAgainstPinnedSchema(
		material.RequestSchema, defaultSchemaURI(binding, material.SchemaIdentity, "request"),
		data, binding, "request")
}

// ValidateInteractionResponse checks a terminal response against the pinned
// definition.
//
// Before ADR 0003 this compared the response *kind* string and confirmed the
// payload was valid JSON, and nothing more — so a pinned binding could not
// answer "what may come back", which is exactly what a durable resolution
// record needs in order to be re-validatable years later. A definition that
// carries a response schema is now validated against the pinned copy of it.
//
// A definition carrying compatibility_response_schema: absent keeps the old
// behavior verbatim. ADR 0003 §8 C4 is explicit that a missing response schema
// must not silently start rejecting responses production accepts; the
// seventeen shipped kinds are backfilled one at a time in CW-20260825-0074.
func (c *EnvelopeDefinitionCatalog) ValidateInteractionResponse(
	ctx context.Context,
	binding DefinitionBinding,
	responseKind string,
	payload json.RawMessage,
) error {
	material, err := c.resolvePinnedMaterial(ctx, binding)
	if err != nil {
		return err
	}
	if responseKind != material.ResponseKind {
		return fmt.Errorf(
			"%w: response kind %q does not match pinned kind %q",
			ErrDefinitionValidation,
			responseKind,
			material.ResponseKind,
		)
	}
	if !json.Valid(payload) {
		return fmt.Errorf("%w: response payload is not valid JSON", ErrDefinitionValidation)
	}
	if !material.ResponseSchemaAuthored {
		return nil
	}
	var decoded any
	if unmarshalErr := json.Unmarshal(payload, &decoded); unmarshalErr != nil {
		return fmt.Errorf("%w: decode response JSON: %w", ErrDefinitionValidation, unmarshalErr)
	}
	return validateAgainstPinnedSchema(
		material.ResponseSchema, defaultSchemaURI(binding, "", "response"), decoded, binding, "response")
}

// validateAgainstPinnedSchema compiles the retained schema bytes fresh and
// validates against them. Compiling per call rather than caching a compiled
// schema is deliberate: the cache key would have to be the binding digest, and
// a cache that ever answered for the wrong digest would reintroduce exactly the
// reinterpretation the pin prevents. Validation is not on a hot path.
func validateAgainstPinnedSchema(
	schemaBytes []byte,
	schemaURI string,
	value any,
	binding DefinitionBinding,
	role string,
) error {
	var schemaDocument any
	if err := json.Unmarshal(schemaBytes, &schemaDocument); err != nil {
		return fmt.Errorf("%w: decode pinned %s schema: %w", ErrDefinitionUnavailable, role, err)
	}
	compiler := jsonschemav6.NewCompiler()
	if err := compiler.AddResource(schemaURI, schemaDocument); err != nil {
		return fmt.Errorf("%w: load pinned %s schema: %w", ErrDefinitionUnavailable, role, err)
	}
	compiled, err := compiler.Compile(schemaURI)
	if err != nil {
		return fmt.Errorf("%w: compile pinned %s schema: %w", ErrDefinitionUnavailable, role, err)
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf(
			"%w: %s does not match %s@%s: %w",
			ErrDefinitionValidation, role, binding.Kind, binding.Version, err)
	}
	return nil
}

func defaultSchemaURI(binding DefinitionBinding, identity, role string) string {
	if identity != "" {
		return identity
	}
	return "memory://tangent/definitions/" + binding.Kind + "/" + binding.Version + "/" + role + ".schema.json"
}

// bindingDigestInputs is every value that participates in the composite pin.
// It is a struct so the digest cannot silently change when a caller forgets an
// argument — adding a field here is a deliberate act that must be accompanied
// by a validator-revision bump.
type bindingDigestInputs struct {
	Manifest             []byte
	RequestSchema        []byte
	ResponseSchema       []byte
	Publisher            string
	Kind                 string
	Version              string
	Revision             int64
	Source               string
	PluginID             string
	ResponseKind         string
	ContractDigest       string
	ManifestDigest       string
	RendererID           string
	RendererClass        string
	RendererIsolation    string
	RequiredCapabilities string
	GrantedCapabilities  string
	Assurance            string
}

// bindingDigest is the composite ADR 0003 §2.9 calls the pin: the manifest and
// schema bytes, the identity triple, the derived contract identity, the
// renderer binding, both capability sets, the trust assurance, and the
// validator revision.
//
// Length-prefixing every byte slice and separating every string with NUL keeps
// the digest unambiguous: without it, moving a character across a field
// boundary would produce the same hash, and two genuinely different
// definitions could share a pin.
func bindingDigest(inputs bindingDigestInputs) string {
	hash := sha256.New()
	for _, blob := range [][]byte{inputs.Manifest, inputs.RequestSchema, inputs.ResponseSchema} {
		_, _ = fmt.Fprintf(hash, "%d:", len(blob))
		_, _ = hash.Write(blob)
		_, _ = hash.Write([]byte{0})
	}
	fields := []string{
		inputs.Publisher, inputs.Kind, inputs.Version, fmt.Sprintf("%d", inputs.Revision),
		inputs.Source, inputs.PluginID, inputs.ResponseKind,
		inputs.ContractDigest, inputs.ManifestDigest,
		inputs.RendererID, inputs.RendererClass, inputs.RendererIsolation,
		inputs.RequiredCapabilities, inputs.GrantedCapabilities,
		inputs.Assurance, envelopeDefinitionValidatorRevision,
	}
	for _, field := range fields {
		_, _ = hash.Write([]byte(field))
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// digestFor recomputes a binding digest over candidate material, using the
// pinned binding's own identity for the fields the material does not carry.
// Used by the in-memory tier of resolvePinnedMaterial to decide whether the
// indexed bytes really are the pinned bytes.
func (c *EnvelopeDefinitionCatalog) digestFor(
	binding DefinitionBinding,
	material envelope.DefinitionMaterial,
) string {
	inputs := bindingDigestInputs{
		Manifest: material.Manifest, RequestSchema: material.RequestSchema,
		ResponseSchema: material.ResponseSchema,
		Publisher:      binding.Publisher, Kind: binding.Kind, Version: binding.Version,
		Revision: binding.Revision, Source: binding.Source,
		ResponseKind:   material.ResponseKind,
		ContractDigest: binding.ContractDigest, ManifestDigest: binding.ManifestDigest,
		RendererID: binding.RendererID, RendererClass: binding.RendererClass,
		RendererIsolation:    binding.RendererIsolation,
		RequiredCapabilities: string(binding.RequiredCapabilities),
		GrantedCapabilities:  string(binding.GrantedCapabilities),
		Assurance:            binding.Assurance,
	}
	if spec, ok := c.service.Lookup(binding.Kind); ok {
		inputs.PluginID = spec.PluginID
	}
	return bindingDigest(inputs)
}

// binding derives the pinned projection of one registered definition.
//
// Definitions registered through the legacy RegisterTypeFromManifest path carry
// no manifest. They still bind — identity, schema digest, host version, and
// assurance are all derivable — they simply contribute nothing to the manifest
// half of the digest. That is what keeps the compatibility path in ADR 0003 §8
// C3 a no-op on the wire.
func (c *EnvelopeDefinitionCatalog) binding(
	spec envelopes.TypeSpec,
	material envelope.DefinitionMaterial,
) (DefinitionBinding, error) {
	schemaIdentity := ""
	schemaDigest := ""
	if spec.DataSchema != nil {
		schemaIdentity = spec.DataSchema.Location
		schemaDigest = definition.Digest(material.RequestSchema)
	}
	publisher := definition.CorePublisher
	if spec.PluginID != "" {
		publisher = spec.PluginID
	}

	out := DefinitionBinding{
		Publisher: publisher, Kind: spec.Name, Version: spec.Version,
		Revision: 1, Source: spec.Source.String(),
		SchemaIdentity: schemaIdentity, SchemaDigest: schemaDigest,
		HostVersion: c.hostVersion, Assurance: string(definition.AssuranceContentAddressedRegistry),
	}
	if materialized := material.Definition; materialized != nil {
		manifest := materialized.Manifest
		required, err := json.Marshal(manifest.RequiredCapabilities)
		if err != nil {
			return DefinitionBinding{}, fmt.Errorf("%w: encode required capabilities: %w", ErrDefinitionUnavailable, err)
		}
		granted, err := json.Marshal(materialized.GrantedCapabilities)
		if err != nil {
			return DefinitionBinding{}, fmt.Errorf("%w: encode granted capabilities: %w", ErrDefinitionUnavailable, err)
		}
		out.Revision = manifest.Revision
		out.Assurance = string(materialized.Assurance)
		out.ManifestDigest = materialized.Derived.ManifestDigest
		out.ContractDigest = materialized.Derived.ContractDigest
		out.ResponseSchemaDigest = materialized.Derived.ResponseSchemaDigest
		out.PackageID = manifest.PackageID
		out.PackageVersion = manifest.PackageVersion
		out.OwnershipClass = string(manifest.OwnershipClass)
		out.CompatibilityClass = string(manifest.CompatibilityClass)
		out.RendererID = manifest.Renderer.ID
		out.RendererClass = string(manifest.Renderer.Class)
		out.RendererIsolation = string(manifest.Renderer.Isolation)
		out.RequiredCapabilities = required
		out.GrantedCapabilities = granted
		out.MaterializationState = string(materialized.State)
	}

	out.Digest = bindingDigest(bindingDigestInputs{
		Manifest: material.Manifest, RequestSchema: material.RequestSchema,
		ResponseSchema: material.ResponseSchema,
		Publisher:      publisher, Kind: spec.Name, Version: spec.Version,
		Revision: out.Revision, Source: spec.Source.String(), PluginID: spec.PluginID,
		ResponseKind:   material.ResponseKind,
		ContractDigest: out.ContractDigest, ManifestDigest: out.ManifestDigest,
		RendererID: out.RendererID, RendererClass: out.RendererClass,
		RendererIsolation:    out.RendererIsolation,
		RequiredCapabilities: string(out.RequiredCapabilities),
		GrantedCapabilities:  string(out.GrantedCapabilities),
		Assurance:            out.Assurance,
	})
	return out, nil
}
