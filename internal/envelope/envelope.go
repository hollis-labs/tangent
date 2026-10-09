// Package envelope wires Tangent into the go-envelopes registry. It is the
// single integration point for envelope validation and dispatch: every
// inbound envelope (from MCP in PR 3, from the WebSocket channel in PR 4)
// flows through Service.Validate before any handler sees it, and every
// produced Response flows through Service.ValidateResponse before it
// leaves the host.
//
// The package owns one *envelopes.Registry per Tangent process. Tangent
// does NOT define its own envelope manifest — go-envelopes is the source
// of truth. Tangent-specific kinds, when introduced, register through the
// extension API (Registry.RegisterTypeFromManifest with pluginID="tangent")
// rather than by forking the core manifest.
package envelope

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	envelopes "github.com/hollis-labs/libs/ui-go/envelopes"

	"github.com/hollis-labs/tangent/internal/definition"
)

// jsonUnmarshal is aliased so definitions.go can decode schema documents
// without importing encoding/json separately; keeping one import of it in the
// package makes the "schemas are decoded exactly once, here" rule visible.
var jsonUnmarshal = json.Unmarshal

// Service is Tangent's wrapper around a loaded *envelopes.Registry. It is
// safe for concurrent use; method receivers either consult the underlying
// registry (which holds its own RWMutex) or the Dispatcher (likewise
// concurrent-safe).
//
// Construct one Service per Tangent process via New, pass it down to any
// subsystem that needs to validate or dispatch envelopes, and never copy
// it. The registry is moderately expensive to load (it parses YAML and
// compiles ~25 JSON Schemas) so creation is reserved for startup.
type Service struct {
	registry   *envelopes.Registry
	materialMu sync.RWMutex
	// materials is the version-indexed definition registry ADR 0003 §3
	// places in Tangent rather than upstream: kind -> version -> material.
	// go-envelopes keys on name alone and rejects a second registration of
	// the same name, so it cannot hold two versions of one kind; this index
	// can, and it is what lets a pinned interaction outlive a version bump.
	materials map[string]map[string]DefinitionMaterial
	// current maps a kind to the version the upstream registry is currently
	// validating against. LookupDefinitionMaterial resolves through it, which
	// preserves that method's original single-version meaning.
	current map[string]string
	policy  definition.HostPolicy
}

// DefinitionMaterial is the exact source material used to register a plugin
// definition. Compiled registry schemas do not retain source bytes, so the
// interaction service uses this copy to create content-addressed bindings and
// to re-validate a pinned interaction against the bytes it was pinned to.
type DefinitionMaterial struct {
	// Manifest is the authored manifest file verbatim — the extended
	// interaction definition manifest for definitions registered through
	// RegisterDefinition, or the small go-envelopes manifest fragment for
	// the legacy RegisterTypeFromManifest path.
	Manifest      []byte
	RequestSchema []byte
	// ResponseSchema is nil for a kind carrying
	// compatibility_response_schema: absent (ADR 0003 §8 C4), which is every
	// kind except tangent.hitl-item until CW-20260825-0074 backfills them.
	ResponseSchema []byte
	ErrorSchema    []byte
	ResponseKind   string
	// SchemaIdentity is the compiled request schema's resource URI —
	// TypeSpec.DataSchema.Location, a stable identity and not a content hash.
	SchemaIdentity string
	// Definition is the parsed and materialized manifest. Nil for the legacy
	// RegisterTypeFromManifest path, which has no manifest to materialize.
	Definition *definition.Materialized
}

// clone returns a defensive copy. The byte slices are the retained material a
// pinned interaction is re-validated against, so handing out the originals
// would let one caller invalidate every future replay.
func (m DefinitionMaterial) clone() DefinitionMaterial {
	m.Manifest = bytes.Clone(m.Manifest)
	m.RequestSchema = bytes.Clone(m.RequestSchema)
	m.ResponseSchema = bytes.Clone(m.ResponseSchema)
	m.ErrorSchema = bytes.Clone(m.ErrorSchema)
	return m
}

// Kind reports the wire name this material was registered under. Only
// definitions carrying a manifest can answer; the legacy path stores its name
// in the index key rather than in the material.
func (m DefinitionMaterial) Kind() string {
	if m.Definition == nil || m.Definition.Manifest == nil {
		return ""
	}
	return m.Definition.Manifest.Kind
}

// Option customizes a Service at construction.
type Option func(*Service)

// WithHostPolicy sets the materialization policy definitions are evaluated
// against. Without it the service uses this build's HostVersion and the
// go-envelopes ProtocolVersion, and grants no host-mediated capabilities —
// the fail-closed default CW-20260825-0077 later widens.
func WithHostPolicy(policy definition.HostPolicy) Option {
	return func(service *Service) { service.policy = policy }
}

// New constructs a Service backed by go-envelopes' core catalog. ctx is
// honored during manifest load — pass the startup context so a canceled
// boot tears down cleanly without finishing schema compilation.
//
// Returns an error if the embedded manifest is malformed or any per-type
// schema fails to compile. Callers should treat that as fatal: an envelope
// host with a half-loaded registry would silently let unknown types
// through.
func New(ctx context.Context, options ...Option) (*Service, error) {
	reg, err := envelopes.LoadCore(ctx)
	if err != nil {
		return nil, fmt.Errorf("envelope: load core registry: %w", err)
	}
	service := &Service{
		registry:  reg,
		materials: make(map[string]map[string]DefinitionMaterial),
		current:   make(map[string]string),
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

// Registry returns the underlying *envelopes.Registry. Exposed primarily
// for the consumer-contract test (envelopestest.RunContract) and for
// future plugin-extension wiring (PR 5). Day-to-day callers should prefer
// the typed Validate/Dispatch methods on Service.
func (s *Service) Registry() *envelopes.Registry { return s.registry }

// RegisterTypeFromManifest registers one plugin type and retains the exact
// manifest/schema bytes required for immutable definition binding.
func (s *Service) RegisterTypeFromManifest(
	name string,
	manifest []byte,
	requestSchema []byte,
	pluginID string,
) error {
	if err := s.registry.RegisterTypeFromManifest(manifest, requestSchema, pluginID); err != nil {
		return err
	}
	spec, ok := s.registry.Lookup(name)
	if !ok {
		return fmt.Errorf("envelope: registered definition %q was not found", name)
	}
	schemaIdentity := ""
	if spec.DataSchema != nil {
		schemaIdentity = spec.DataSchema.Location
	}
	s.putVersionedMaterial(name, spec.Version, DefinitionMaterial{
		Manifest: bytes.Clone(manifest), RequestSchema: bytes.Clone(requestSchema),
		ResponseKind: string(spec.ResponseKind), SchemaIdentity: schemaIdentity,
	})
	return nil
}

// putMaterial indexes material for a definition registered through
// RegisterDefinition, which carries its own kind and version.
func (s *Service) putMaterial(material DefinitionMaterial) {
	s.putVersionedMaterial(material.Definition.Manifest.Kind, material.Definition.Manifest.Version, material)
}

// putVersionedMaterial writes one kind@version into the version index and
// marks it current. "Current" is whatever the upstream registry is validating
// against, which is necessarily the most recent successful registration:
// upstream refuses a duplicate name, so a second version of one kind can only
// arrive after the first was unregistered.
func (s *Service) putVersionedMaterial(kind, version string, material DefinitionMaterial) {
	s.materialMu.Lock()
	byVersion, ok := s.materials[kind]
	if !ok {
		byVersion = make(map[string]DefinitionMaterial)
		s.materials[kind] = byVersion
	}
	byVersion[version] = material
	s.current[kind] = version
	s.materialMu.Unlock()
}

// LookupDefinitionMaterial returns a defensive copy of exact registration
// material. Core definitions currently lack source bytes in go-envelopes and
// deliberately return false rather than fabricating a content digest.
func (s *Service) LookupDefinitionMaterial(name string) (DefinitionMaterial, bool) {
	s.materialMu.RLock()
	version, ok := s.current[name]
	if !ok {
		s.materialMu.RUnlock()
		return DefinitionMaterial{}, false
	}
	material, ok := s.materials[name][version]
	s.materialMu.RUnlock()
	if !ok {
		return DefinitionMaterial{}, false
	}
	return material.clone(), true
}

// Validate checks env against its registered type's schema. Returns nil
// on success, errors.Is(err, envelopes.ErrUnknownType) for unknown types,
// errors.Is(err, envelopes.ErrSchemaValidation) for schema mismatches.
//
// The error is wrapped with "envelope: validate envelope" context so
// upstream logs identify the gate without callers having to format.
func (s *Service) Validate(env *envelopes.Envelope) error {
	if err := s.registry.ValidateEnvelope(env); err != nil {
		return fmt.Errorf("envelope: validate envelope: %w", err)
	}
	return nil
}

// ValidateResponse checks resp against the response-kind contract for the
// given envelope type. Same error-wrapping convention as Validate.
func (s *Service) ValidateResponse(envelopeType string, resp *envelopes.Response) error {
	if err := s.registry.ValidateResponse(envelopeType, resp); err != nil {
		return fmt.Errorf("envelope: validate response: %w", err)
	}
	return nil
}

// Lookup returns the TypeSpec for the named type. The boolean is false if
// the name is not registered. Pass-through to the registry; centralized
// here so callers never have to reach for s.Registry() directly.
func (s *Service) Lookup(name string) (envelopes.TypeSpec, bool) {
	return s.registry.Lookup(name)
}

// All returns every registered type spec, sorted by name. Used by the
// codegen dump tool today; will back tangent.list_workflows in PR 3.
func (s *Service) All() []envelopes.TypeSpec {
	return s.registry.All()
}

// Len reports the number of registered envelope types. Used at startup
// for the "loaded N envelope types" log line.
func (s *Service) Len() int { return s.registry.Len() }

// RegisteredKinds returns every registered envelope type name, sorted.
//
// It is deliberately narrower than All: a caller that only needs to know
// *whether* a kind exists should not have to hold a compiled schema to find
// out. Health reporting is the caller this exists for — it distinguishes a
// kind that carries a definition manifest from one registered through the
// legacy path from one this build has never heard of, and the middle case is
// invisible without this.
func (s *Service) RegisteredKinds() []string {
	specs := s.registry.All()
	kinds := make([]string, 0, len(specs))
	for _, spec := range specs {
		kinds = append(kinds, spec.Name)
	}
	return kinds
}
