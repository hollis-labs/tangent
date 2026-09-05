package interaction

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RetainedDefinition is the exact material one binding digest was cut from,
// durable across process restarts and across registry changes.
//
// This is the mechanism behind ADR 0001 §3's "pins the exact definition Tangent
// used". The in-memory version index is rebuilt from the embedded package tree
// at every boot, so it can only answer for definitions this build still ships.
// A release that bumps a kind's version, removes it, or changes its schema
// leaves every interaction pinned to the previous material with nothing to
// validate against — unless the material itself was retained. It is retained
// here, keyed on the pin, at the moment the pin is taken.
//
// Rows are content-addressed and immutable (migration 0007). Retaining the same
// definition twice is a no-op rather than a conflict.
type RetainedDefinition struct {
	BindingDigest string
	Publisher     string
	Kind          string
	Version       string
	Revision      int64

	ManifestDigest string
	ContractDigest string

	// The authored bytes. ValidateInteractionRequest and
	// ValidateInteractionResponse compile against these and never against the
	// live registry's compiled schema.
	ManifestSource []byte
	RequestSchema  []byte
	ResponseSchema []byte
	ErrorSchema    []byte

	ResponseKind string
	// CompatibilityResponseSchema is "present" or "absent" (ADR 0003 §8 C4).
	// "absent" preserves the response-kind-only check for a kind whose real
	// response schema is backfilled later.
	CompatibilityResponseSchema string

	SchemaIdentity       string
	RequestSchemaDigest  string
	ResponseSchemaDigest string

	PackageID          string
	PackageVersion     string
	OwnershipClass     string
	CompatibilityClass string

	RendererID         string
	RendererClass      string
	RendererTrustClass string

	RequiredCapabilities string
	GrantedCapabilities  string

	TrustAssurance     string
	TrustSourceLocator string

	HostVersion string
	// ValidatorRevision is the value of envelopeDefinitionValidatorRevision in
	// force when the pin was taken. Retained beside the material so a bump
	// makes old records fail closed rather than be reinterpreted.
	ValidatorRevision    string
	MaterializationState string
	MaterializedAt       time.Time
}

// DefinitionMaterialStore is the durable half of the retained-material
// resolver. The interaction Store implements it; the catalog takes it as an
// option so a catalog constructed without a database still works from its
// in-memory index alone.
type DefinitionMaterialStore interface {
	PutDefinitionMaterial(context.Context, RetainedDefinition) error
	GetDefinitionMaterial(context.Context, string) (RetainedDefinition, bool, error)
}

// PutDefinitionMaterial retains one definition's exact material under its
// binding digest.
//
// INSERT OR IGNORE rather than an upsert: the key is a content digest, so a row
// that already exists necessarily holds identical bytes, and the immutability
// triggers would abort an UPDATE anyway. Retention is therefore safe to call on
// every submission without a read-first round trip.
func (s *Store) PutDefinitionMaterial(ctx context.Context, retained RetainedDefinition) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	if retained.BindingDigest == "" || retained.Kind == "" || retained.Version == "" {
		return fmt.Errorf("%w: retained definition needs a digest, kind, and version", ErrInvalidRecord)
	}
	if len(retained.ManifestSource) == 0 {
		return fmt.Errorf("%w: retained definition %s has no manifest source", ErrInvalidRecord, retained.Kind)
	}
	if retained.MaterializedAt.IsZero() {
		retained.MaterializedAt = s.now()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO definition_manifests (
  binding_digest, publisher, kind, version, revision,
  manifest_digest, contract_digest,
  manifest_source, request_schema, response_schema, error_schema,
  response_kind, compatibility_response_schema,
  schema_identity, request_schema_digest, response_schema_digest,
  package_id, package_version, ownership_class, compatibility_class,
  renderer_id, renderer_class, renderer_trust_class,
  required_capabilities, granted_capabilities,
  trust_assurance, trust_source_locator,
  host_version, validator_revision, materialization_state, materialized_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		retained.BindingDigest, retained.Publisher, retained.Kind, retained.Version, retained.Revision,
		retained.ManifestDigest, retained.ContractDigest,
		retained.ManifestSource, blobOrNil(retained.RequestSchema),
		blobOrNil(retained.ResponseSchema), blobOrNil(retained.ErrorSchema),
		retained.ResponseKind, retained.CompatibilityResponseSchema,
		nullString(retained.SchemaIdentity), nullString(retained.RequestSchemaDigest),
		nullString(retained.ResponseSchemaDigest),
		retained.PackageID, retained.PackageVersion, retained.OwnershipClass, retained.CompatibilityClass,
		retained.RendererID, retained.RendererClass, retained.RendererTrustClass,
		jsonArrayOrEmpty(retained.RequiredCapabilities), jsonArrayOrEmpty(retained.GrantedCapabilities),
		retained.TrustAssurance, retained.TrustSourceLocator,
		nullString(retained.HostVersion), retained.ValidatorRevision,
		retained.MaterializationState, retained.MaterializedAt,
	)
	if err != nil {
		return fmt.Errorf("retain definition material for %s@%s: %w", retained.Kind, retained.Version, err)
	}
	return nil
}

// GetDefinitionMaterial resolves retained material by binding digest. The
// digest is the whole lookup key: nothing about the current registry
// participates, which is exactly what makes a pinned interaction replayable
// after the installed catalog moves on.
func (s *Store) GetDefinitionMaterial(
	ctx context.Context,
	bindingDigest string,
) (RetainedDefinition, bool, error) {
	if s == nil || s.db == nil {
		return RetainedDefinition{}, false, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	if bindingDigest == "" {
		return RetainedDefinition{}, false, nil
	}
	var retained RetainedDefinition
	var requestSchema, responseSchema, errorSchema []byte
	var schemaIdentity, requestDigest, responseDigest, hostVersion sql.NullString
	var materializedAt any
	err := s.db.QueryRowContext(ctx, `
SELECT binding_digest, publisher, kind, version, revision,
       manifest_digest, contract_digest,
       manifest_source, request_schema, response_schema, error_schema,
       response_kind, compatibility_response_schema,
       schema_identity, request_schema_digest, response_schema_digest,
       package_id, package_version, ownership_class, compatibility_class,
       renderer_id, renderer_class, renderer_trust_class,
       required_capabilities, granted_capabilities,
       trust_assurance, trust_source_locator,
       host_version, validator_revision, materialization_state, materialized_at
FROM definition_manifests WHERE binding_digest = ?`, bindingDigest).Scan(
		&retained.BindingDigest, &retained.Publisher, &retained.Kind, &retained.Version, &retained.Revision,
		&retained.ManifestDigest, &retained.ContractDigest,
		&retained.ManifestSource, &requestSchema, &responseSchema, &errorSchema,
		&retained.ResponseKind, &retained.CompatibilityResponseSchema,
		&schemaIdentity, &requestDigest, &responseDigest,
		&retained.PackageID, &retained.PackageVersion, &retained.OwnershipClass, &retained.CompatibilityClass,
		&retained.RendererID, &retained.RendererClass, &retained.RendererTrustClass,
		&retained.RequiredCapabilities, &retained.GrantedCapabilities,
		&retained.TrustAssurance, &retained.TrustSourceLocator,
		&hostVersion, &retained.ValidatorRevision, &retained.MaterializationState, &materializedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RetainedDefinition{}, false, nil
	}
	if err != nil {
		return RetainedDefinition{}, false, fmt.Errorf("load retained definition %s: %w", bindingDigest, err)
	}
	retained.RequestSchema = requestSchema
	retained.ResponseSchema = responseSchema
	retained.ErrorSchema = errorSchema
	retained.SchemaIdentity = schemaIdentity.String
	retained.RequestSchemaDigest = requestDigest.String
	retained.ResponseSchemaDigest = responseDigest.String
	retained.HostVersion = hostVersion.String
	parsed, parseErr := parseTime(materializedAt)
	if parseErr != nil {
		return RetainedDefinition{}, false, fmt.Errorf("scan retained definition materialized_at: %w", parseErr)
	}
	retained.MaterializedAt = parsed
	return retained, true, nil
}

// CountRetainedDefinitions reports how many definitions have been retained.
// Exposed for the registry diagnostics, which report retained-material
// coverage without streaming the material itself.
func (s *Store) CountRetainedDefinitions(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("%w: nil database", ErrInvalidRecord)
	}
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM definition_manifests`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count retained definitions: %w", err)
	}
	return count, nil
}

func blobOrNil(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

// jsonArrayOrEmpty normalizes an absent capability list to "[]" so the column's
// json_valid CHECK holds and a reader never has to distinguish NULL from an
// empty grant.
func jsonArrayOrEmpty(value string) string {
	if value == "" {
		return "[]"
	}
	return value
}
