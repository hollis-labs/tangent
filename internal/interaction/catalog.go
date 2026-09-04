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
const envelopeDefinitionValidatorRevision = "tangent-envelope-definition-validator-v1"

type DefinitionRef struct {
	Kind    string `json:"kind"`
	Version string `json:"version,omitempty"`
}

type InteractionKind struct {
	Kind         string            `json:"kind"`
	Version      string            `json:"version"`
	Description  string            `json:"description,omitempty"`
	ResponseKind string            `json:"response_kind"`
	Binding      DefinitionBinding `json:"binding"`
}

// DefinitionCatalog resolves and validates immutable interaction definitions.
// Implementations must compare response validation against the pinned binding,
// never silently reinterpret it through a different current definition.
type DefinitionCatalog interface {
	ListInteractionKinds(context.Context) ([]InteractionKind, error)
	ResolveInteractionDefinition(context.Context, DefinitionRef) (DefinitionBinding, error)
	ValidateInteractionRequest(context.Context, DefinitionBinding, json.RawMessage) error
	ValidateInteractionResponse(context.Context, DefinitionBinding, string, json.RawMessage) error
}

// EnvelopeDefinitionCatalog adapts the in-process go-envelopes registry to the
// generic interaction service. Its binding digest is derived deterministically
// from the exact schema material supplied by the registry.
type EnvelopeDefinitionCatalog struct {
	service     *envelope.Service
	hostVersion string
}

func NewEnvelopeDefinitionCatalog(service *envelope.Service, hostVersion string) *EnvelopeDefinitionCatalog {
	return &EnvelopeDefinitionCatalog{service: service, hostVersion: hostVersion}
}

func (c *EnvelopeDefinitionCatalog) ListInteractionKinds(_ context.Context) ([]InteractionKind, error) {
	if c == nil || c.service == nil {
		return nil, fmt.Errorf("%w: envelope registry is nil", ErrDefinitionUnavailable)
	}
	specs := c.service.All()
	out := make([]InteractionKind, 0, len(specs))
	for _, spec := range specs {
		binding, err := c.binding(spec)
		if err != nil {
			if errors.Is(err, ErrDefinitionUnavailable) {
				continue
			}
			return nil, err
		}
		out = append(out, InteractionKind{
			Kind: spec.Name, Version: spec.Version, Description: spec.Description,
			ResponseKind: string(spec.ResponseKind), Binding: binding,
		})
	}
	return out, nil
}

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
	return c.binding(spec)
}

func (c *EnvelopeDefinitionCatalog) ValidateInteractionRequest(
	_ context.Context,
	binding DefinitionBinding,
	request json.RawMessage,
) error {
	material, materialErr := c.currentPinnedMaterial(binding)
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
	var schemaDocument any
	if err := json.Unmarshal(material.RequestSchema, &schemaDocument); err != nil {
		return fmt.Errorf("%w: decode pinned request schema: %w", ErrDefinitionUnavailable, err)
	}
	schemaURI := binding.SchemaIdentity
	if schemaURI == "" {
		schemaURI = "memory://tangent/definitions/" + binding.Kind + "/" + binding.Version + "/request.schema.json"
	}
	compiler := jsonschemav6.NewCompiler()
	if err := compiler.AddResource(schemaURI, schemaDocument); err != nil {
		return fmt.Errorf("%w: load pinned request schema: %w", ErrDefinitionUnavailable, err)
	}
	compiled, err := compiler.Compile(schemaURI)
	if err != nil {
		return fmt.Errorf("%w: compile pinned request schema: %w", ErrDefinitionUnavailable, err)
	}
	if err := compiled.Validate(data); err != nil {
		return fmt.Errorf("%w: request does not match %s@%s: %w", ErrDefinitionValidation, binding.Kind, binding.Version, err)
	}
	return nil
}

func (c *EnvelopeDefinitionCatalog) ValidateInteractionResponse(
	_ context.Context,
	binding DefinitionBinding,
	responseKind string,
	payload json.RawMessage,
) error {
	material, err := c.currentPinnedMaterial(binding)
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
	return nil
}

func (c *EnvelopeDefinitionCatalog) currentPinnedMaterial(
	binding DefinitionBinding,
) (envelope.DefinitionMaterial, error) {
	if _, err := c.currentPinnedSpec(binding); err != nil {
		return envelope.DefinitionMaterial{}, err
	}
	material, ok := c.service.LookupDefinitionMaterial(binding.Kind)
	if !ok {
		return envelope.DefinitionMaterial{}, fmt.Errorf(
			"%w: exact source material for %s@%s is not retained",
			ErrDefinitionUnavailable,
			binding.Kind,
			binding.Version,
		)
	}
	return material, nil
}

func (c *EnvelopeDefinitionCatalog) currentPinnedSpec(binding DefinitionBinding) (envelopes.TypeSpec, error) {
	if c == nil || c.service == nil {
		return envelopes.TypeSpec{}, fmt.Errorf("%w: envelope registry is nil", ErrDefinitionUnavailable)
	}
	spec, ok := c.service.Lookup(binding.Kind)
	if !ok {
		return envelopes.TypeSpec{}, fmt.Errorf("%w: %s@%s", ErrDefinitionUnavailable, binding.Kind, binding.Version)
	}
	current, err := c.binding(spec)
	if err != nil {
		return envelopes.TypeSpec{}, err
	}
	if current.Version != binding.Version || current.Revision != binding.Revision || current.Digest != binding.Digest ||
		current.SchemaIdentity != binding.SchemaIdentity || current.SchemaDigest != binding.SchemaDigest {
		return envelopes.TypeSpec{}, fmt.Errorf("%w: %s@%s no longer matches its pinned binding", ErrDefinitionUnavailable, binding.Kind, binding.Version)
	}
	return spec, nil
}

func (c *EnvelopeDefinitionCatalog) binding(spec envelopes.TypeSpec) (DefinitionBinding, error) {
	material, ok := c.service.LookupDefinitionMaterial(spec.Name)
	if !ok {
		return DefinitionBinding{}, fmt.Errorf(
			"%w: exact source material for %s@%s is not retained",
			ErrDefinitionUnavailable,
			spec.Name,
			spec.Version,
		)
	}
	schemaIdentity := ""
	schemaDigest := ""
	if spec.DataSchema != nil {
		schemaIdentity = spec.DataSchema.Location
		schemaSum := sha256.Sum256(material.RequestSchema)
		schemaDigest = "sha256:" + hex.EncodeToString(schemaSum[:])
	}
	publisher := "hollis-labs/go-envelopes"
	if spec.PluginID != "" {
		publisher = spec.PluginID
	}
	bindingHash := sha256.New()
	_, _ = bindingHash.Write(material.Manifest)
	_, _ = bindingHash.Write([]byte{0})
	_, _ = bindingHash.Write(material.RequestSchema)
	_, _ = bindingHash.Write([]byte{0})
	_, _ = bindingHash.Write([]byte(publisher + "\x00" + spec.Name + "\x00" + spec.Version + "\x00" +
		spec.Source.String() + "\x00" + spec.PluginID + "\x00" + material.ResponseKind + "\x00" +
		envelopeDefinitionValidatorRevision))
	return DefinitionBinding{
		Publisher: publisher, Kind: spec.Name, Version: spec.Version,
		Revision: spec.Version, Digest: "sha256:" + hex.EncodeToString(bindingHash.Sum(nil)),
		Source: spec.Source.String(), SchemaIdentity: schemaIdentity,
		SchemaDigest: schemaDigest, HostVersion: c.hostVersion, Assurance: "content-addressed-registry",
	}, nil
}
