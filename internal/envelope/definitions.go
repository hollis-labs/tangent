package envelope

import (
	"bytes"
	"fmt"
	"sort"

	envelopes "github.com/hollis-labs/libs/ui-go/envelopes"
	jsonschemav6 "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/hollis-labs/tangent/internal/definition"
)

// HostVersion is the Tangent release definitions declare compatibility
// against via `compatible_host_versions`. It is defined here, in the package
// that owns the definition registry, so there is exactly one such string:
// internal/mcp aliases it rather than declaring its own, and
// TestHostVersionHasOneSource asserts they have not drifted apart.
const HostVersion = "v0.19.0"

// ProtocolVersion is the go-envelopes wire-model version definitions declare
// compatibility against via `compatible_protocol_versions`. Upstream exposes
// it as an int constant; rendering it once here keeps the conversion out of
// every call site.
var ProtocolVersion = fmt.Sprintf("%d", envelopes.ProtocolVersion)

// DefinitionOption customizes one RegisterDefinition call. The two optional
// inputs — an error schema and a source locator — are options rather than
// parameters so RegisterDefinition keeps the exact signature ADR 0003 §3
// specifies.
type DefinitionOption func(*definition.Material)

// WithErrorSchema attaches the typed publisher-error schema (§2.2
// `error_schema`).
func WithErrorSchema(schema []byte) DefinitionOption {
	return func(material *definition.Material) { material.ErrorSchema = bytes.Clone(schema) }
}

// WithSourceLocator records where the material was obtained — the embedded FS
// path for a bundled package. Persisted as trust.source_locator (§2.7).
func WithSourceLocator(locator string) DefinitionOption {
	return func(material *definition.Material) { material.SourceLocator = locator }
}

// RegisterDefinition registers one interaction definition from its authored
// manifest and schema material. It is ADR 0003 §3's response-schema-carrying
// sibling of RegisterTypeFromManifest, and the smallest change that makes
// §2.2's `response_schema` real: it builds the TypeSpec directly so it can
// populate the already-present TypeSpec.PayloadSchema, which no manifest path
// upstream reaches. Nothing in go-envelopes changes.
//
// A definition whose manifest is malformed is rejected outright — there is
// nothing coherent to materialize. A definition that is merely unservable is
// still registered, at its materialization state, because `registered` never
// implies `available` and the registry has to be able to list and explain a
// definition it will not serve (§1, §8 C7). Gating submission is the
// interaction catalog's job, not registration's.
//
// The request schema is compiled with the identical
// plugin://<pluginID>/envelopes/<name>.schema.json URI that
// RegisterTypeFromManifest uses upstream, so `request_schema_identity` — the
// value persisted as `schema_identity` — is byte-identical to what the
// previous registration path produced. That is what makes the C3 compatibility
// adapters a no-op on the wire.
func (s *Service) RegisterDefinition(
	manifestSource []byte,
	requestSchema []byte,
	responseSchema []byte,
	pluginID string,
	options ...DefinitionOption,
) error {
	if pluginID == "" {
		return fmt.Errorf("envelope: plugin id is required")
	}
	manifest, err := definition.Parse(manifestSource)
	if err != nil {
		return err
	}
	if manifest.Publisher != pluginID {
		return fmt.Errorf(
			"%w: %s: publisher %q does not match the registering plugin id %q",
			definition.ErrInvalidManifest, manifest.Kind, manifest.Publisher, pluginID)
	}

	material := definition.Material{
		ManifestSource: bytes.Clone(manifestSource),
		RequestSchema:  bytes.Clone(requestSchema),
		ResponseSchema: bytes.Clone(responseSchema),
	}
	for _, option := range options {
		if option != nil {
			option(&material)
		}
	}

	materialized, err := definition.Materialize(manifest, material, s.hostPolicy())
	if err != nil {
		return err
	}

	spec := envelopes.TypeSpec{
		Name:         manifest.Kind,
		Version:      manifest.Version,
		ResponseKind: envelopes.ResponseKind(manifest.ResponseKind),
		Description:  manifest.Description,
		Source:       envelopes.TypeSourcePlugin,
		PluginID:     pluginID,
		UIMetadata:   uiMetadata(manifest),
	}
	if len(material.RequestSchema) > 0 {
		compiled, compileErr := compileDefinitionSchema(pluginID, manifest.Kind, material.RequestSchema)
		if compileErr != nil {
			return compileErr
		}
		spec.DataSchema = compiled
	}
	if len(material.ResponseSchema) > 0 {
		// The response schema gets its own resource URI so a $ref inside one
		// document can never resolve into the other.
		compiled, compileErr := compileDefinitionSchema(
			pluginID, manifest.Kind+".response", material.ResponseSchema)
		if compileErr != nil {
			return compileErr
		}
		spec.PayloadSchema = compiled
	}
	if registerErr := s.registry.RegisterType(spec); registerErr != nil {
		return fmt.Errorf("envelope: register definition %q: %w", manifest.Kind, registerErr)
	}

	schemaIdentity := ""
	if spec.DataSchema != nil {
		schemaIdentity = spec.DataSchema.Location
	}
	s.putMaterial(DefinitionMaterial{
		Manifest:       material.ManifestSource,
		RequestSchema:  material.RequestSchema,
		ResponseSchema: material.ResponseSchema,
		ErrorSchema:    material.ErrorSchema,
		ResponseKind:   string(spec.ResponseKind),
		SchemaIdentity: schemaIdentity,
		Definition:     &materialized,
	})
	return nil
}

// uiMetadata reproduces the legacy `ui.component` bag from the manifest's
// renderer block. The manifest's renderer.entry is the authoritative binding;
// the slug survives because the generated EnvelopeKindMap and every committed
// TypeScript consumer are keyed on it, and ADR 0003 changes no wire shape.
func uiMetadata(manifest *definition.Manifest) map[string]any {
	if manifest.Renderer.Component == "" {
		return nil
	}
	return map[string]any{"component": manifest.Renderer.Component}
}

// compileDefinitionSchema mirrors go-envelopes' unexported compilePluginSchema
// exactly, URI included. Divergence here would move `schema_identity` on every
// binding, which is precisely the wire breakage C3 forbids.
func compileDefinitionSchema(pluginID, typeName string, schemaBytes []byte) (*jsonschemav6.Schema, error) {
	var document any
	if err := jsonUnmarshal(schemaBytes, &document); err != nil {
		return nil, fmt.Errorf("envelope: parse schema for %q: %w", typeName, err)
	}
	uri := fmt.Sprintf("plugin://%s/envelopes/%s.schema.json", pluginID, typeName)
	compiler := jsonschemav6.NewCompiler()
	if err := compiler.AddResource(uri, document); err != nil {
		return nil, fmt.Errorf("envelope: register schema for %q: %w", typeName, err)
	}
	compiled, err := compiler.Compile(uri)
	if err != nil {
		return nil, fmt.Errorf("envelope: compile schema for %q: %w", typeName, err)
	}
	return compiled, nil
}

// LookupDefinitionMaterialVersion returns the retained material for one exact
// kind@version. This is the version index ADR 0003 §3 places in Tangent rather
// than upstream: go-envelopes keys on name alone and returns ErrConflict for a
// second registration of the same name, so two versions of one kind cannot
// coexist there. The upstream registry stays the single-current-version
// validator; this index is what lets a pinned interaction outlive a version
// bump.
func (s *Service) LookupDefinitionMaterialVersion(kind, version string) (DefinitionMaterial, bool) {
	s.materialMu.RLock()
	byVersion, ok := s.materials[kind]
	if !ok {
		s.materialMu.RUnlock()
		return DefinitionMaterial{}, false
	}
	material, ok := byVersion[version]
	s.materialMu.RUnlock()
	if !ok {
		return DefinitionMaterial{}, false
	}
	return material.clone(), true
}

// MaterializedDefinitions returns every retained definition's materialization
// result, sorted by kind then version. Definitions registered through the
// legacy RegisterTypeFromManifest path carry no manifest and are skipped: they
// have a binding but not a materialization.
func (s *Service) MaterializedDefinitions() []definition.Materialized {
	s.materialMu.RLock()
	out := make([]definition.Materialized, 0, len(s.materials))
	for _, byVersion := range s.materials {
		for _, material := range byVersion {
			if material.Definition == nil {
				continue
			}
			out = append(out, *material.Definition)
		}
	}
	s.materialMu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Manifest.Kind != out[j].Manifest.Kind {
			return out[i].Manifest.Kind < out[j].Manifest.Kind
		}
		return out[i].Manifest.Version < out[j].Manifest.Version
	})
	return out
}

// DefinitionSourceDigest is the @definition-source stamp for the whole
// registry: the stable hash over every materialized definition's (kind,
// version, revision, manifest_digest). Generated artifacts carry it so drift
// is reported rather than inferred from a successful build (ADR 0003 §4).
func (s *Service) DefinitionSourceDigest() (string, error) {
	materialized := s.MaterializedDefinitions()
	entries := make([]definition.SourceEntry, 0, len(materialized))
	for _, item := range materialized {
		entries = append(entries, item.SourceEntry())
	}
	return definition.SourceDigest(entries)
}

// HostPolicy returns the materialization policy this service applies.
func (s *Service) HostPolicy() definition.HostPolicy { return s.hostPolicy() }

func (s *Service) hostPolicy() definition.HostPolicy {
	if s.policy.HostVersion == "" && s.policy.ProtocolVersion == "" {
		return definition.HostPolicy{HostVersion: HostVersion, ProtocolVersion: ProtocolVersion}
	}
	return s.policy
}
