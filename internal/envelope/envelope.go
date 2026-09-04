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
	"fmt"
	"sync"

	envelopes "github.com/hollis-labs/go-envelopes"
)

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
	materials  map[string]DefinitionMaterial
}

// DefinitionMaterial is the exact source material used to register a plugin
// definition. Compiled registry schemas do not retain source bytes, so the
// interaction service uses this copy to create content-addressed bindings.
type DefinitionMaterial struct {
	Manifest      []byte
	RequestSchema []byte
	ResponseKind  string
}

// New constructs a Service backed by go-envelopes' core catalog. ctx is
// honored during manifest load — pass the startup context so a canceled
// boot tears down cleanly without finishing schema compilation.
//
// Returns an error if the embedded manifest is malformed or any per-type
// schema fails to compile. Callers should treat that as fatal: an envelope
// host with a half-loaded registry would silently let unknown types
// through.
func New(ctx context.Context) (*Service, error) {
	reg, err := envelopes.LoadCore(ctx)
	if err != nil {
		return nil, fmt.Errorf("envelope: load core registry: %w", err)
	}
	return &Service{registry: reg, materials: make(map[string]DefinitionMaterial)}, nil
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
	s.materialMu.Lock()
	s.materials[name] = DefinitionMaterial{
		Manifest: bytes.Clone(manifest), RequestSchema: bytes.Clone(requestSchema),
		ResponseKind: string(spec.ResponseKind),
	}
	s.materialMu.Unlock()
	return nil
}

// LookupDefinitionMaterial returns a defensive copy of exact registration
// material. Core definitions currently lack source bytes in go-envelopes and
// deliberately return false rather than fabricating a content digest.
func (s *Service) LookupDefinitionMaterial(name string) (DefinitionMaterial, bool) {
	s.materialMu.RLock()
	material, ok := s.materials[name]
	s.materialMu.RUnlock()
	if !ok {
		return DefinitionMaterial{}, false
	}
	material.Manifest = bytes.Clone(material.Manifest)
	material.RequestSchema = bytes.Clone(material.RequestSchema)
	return material, true
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
