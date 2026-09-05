package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/envelope"
)

// reusable authored material for a synthetic package. Written inline rather
// than pulled from internal/envelope/extensions so this test exercises the
// registry rather than the shipped catalog, and so a change to a shipped kind
// cannot make it pass or fail for the wrong reason.
const registryTestManifestV1 = `manifest_version: "1.0.0"
publisher: tangent
kind: tangent.registry-fixture
version: "1.0"
revision: 1
title: "Registry fixture"
description: "Synthetic definition used to exercise the versioned registry."
package_id: tangent.fixture
package_version: "1.0.0"
ownership_class: host-package
request_schema: request.schema.json
response_kind: data
response_schema: response.schema.json
compatibility_response_schema: present
renderer:
  id: tangent.renderer.registry-fixture
  class: react-component
  entry: "components/envelopes/Fixture#Fixture"
  trust_class: core-trusted
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

const registryTestRequestSchemaV1 = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "required":["prompt"],
  "properties":{"prompt":{"type":"string"}},
  "additionalProperties":false
}`

const registryTestResponseSchema = `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "required":["decision"],
  "properties":{"decision":{"enum":["accept","reject"]}},
  "additionalProperties":false
}`

func registryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "definition-registry.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return database
}

func registryTestService(t *testing.T, manifest, requestSchema string) *envelope.Service {
	t.Helper()
	service, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if registerErr := service.RegisterDefinition(
		[]byte(manifest), []byte(requestSchema), []byte(registryTestResponseSchema), "tangent",
	); registerErr != nil {
		t.Fatalf("RegisterDefinition: %v", registerErr)
	}
	return service
}

// TestPinnedInteractionReplaysAfterVersionChange is the acceptance test for the
// versioned registry: a submitted interaction pins one exact definition
// revision and can still be validated and replayed against that material after
// the process restarts, after the installed catalog changes, and after the
// current version moves on.
//
// The three failures it rules out are different. A restart loses the in-memory
// index. A catalog change replaces what the registry answers for the kind. A
// version bump means the pinned version is not even the current one any more.
// All three are the same fix — resolve retained material by the pinned digest,
// never through the current registry entry — and all three are asserted here
// because only the third one is caught by looking at the code.
func TestPinnedInteractionReplaysAfterVersionChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := registryTestDB(t)
	store := NewStore(database)

	service := registryTestService(t, registryTestManifestV1, registryTestRequestSchemaV1)
	catalog := NewEnvelopeDefinitionCatalog(service, "v0.12.0", WithRetainedMaterialStore(store))

	binding, err := catalog.ResolveInteractionDefinition(ctx, DefinitionRef{
		Kind: "tangent.registry-fixture", Version: "1.0",
	})
	if err != nil {
		t.Fatalf("ResolveInteractionDefinition: %v", err)
	}
	if binding.Digest == "" || binding.ManifestDigest == "" || binding.ContractDigest == "" {
		t.Fatalf("binding does not carry manifest identity: %#v", binding)
	}
	if binding.Revision != 1 {
		t.Fatalf("binding revision = %d, want the manifest's own revision 1", binding.Revision)
	}
	if err := catalog.RetainDefinitionMaterial(ctx, binding); err != nil {
		t.Fatalf("RetainDefinitionMaterial: %v", err)
	}

	// A different process, with a registry that has moved on: the fixture is
	// now at version 2.0 with an incompatible request schema, and 1.0 is gone.
	// Nothing about the restarted host knows the pinned revision existed.
	restartedManifest := replaceAll(registryTestManifestV1, `version: "1.0"`, `version: "2.0"`)
	restartedSchema := `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "required":["replacement"],
  "properties":{"replacement":{"type":"boolean"}},
  "additionalProperties":false
}`
	restarted := registryTestService(t, restartedManifest, restartedSchema)
	restartedStore := NewStore(database)
	restartedCatalog := NewEnvelopeDefinitionCatalog(
		restarted, "v0.12.0", WithRetainedMaterialStore(restartedStore))

	if _, err := restartedCatalog.ResolveInteractionDefinition(ctx, DefinitionRef{
		Kind: "tangent.registry-fixture", Version: "1.0",
	}); !errors.Is(err, ErrDefinitionNotFound) {
		t.Fatalf("resolving the superseded version for a NEW submission = %v, want ErrDefinitionNotFound", err)
	}

	// The pinned interaction still validates against the material it pinned,
	// not against the version the registry now serves.
	if err := restartedCatalog.ValidateInteractionRequest(
		ctx, binding, json.RawMessage(`{"prompt":"pinned"}`),
	); err != nil {
		t.Fatalf("pinned request no longer validates after a version change: %v", err)
	}
	if err := restartedCatalog.ValidateInteractionRequest(
		ctx, binding, json.RawMessage(`{"replacement":true}`),
	); !errors.Is(err, ErrDefinitionValidation) {
		t.Fatalf("pinned interaction was reinterpreted through the current version: %v", err)
	}
	if err := restartedCatalog.ValidateInteractionResponse(
		ctx, binding, "data", json.RawMessage(`{"decision":"accept"}`),
	); err != nil {
		t.Fatalf("pinned response no longer validates after a version change: %v", err)
	}

	// Without the durable tier there is nothing to replay from, which is the
	// negative control: the mechanism, not merely the in-memory index, is what
	// makes the assertions above true.
	orphaned := NewEnvelopeDefinitionCatalog(restarted, "v0.12.0")
	if err := orphaned.ValidateInteractionRequest(
		ctx, binding, json.RawMessage(`{"prompt":"pinned"}`),
	); !errors.Is(err, ErrDefinitionUnavailable) {
		t.Fatalf("catalog without retained material = %v, want ErrDefinitionUnavailable", err)
	}
}

// TestResponseSchemaIsPartOfThePinnedContract covers the gap ADR 0003 §2.2
// names: before this work a pinned binding could not answer "what may come
// back", because ValidateInteractionResponse compared a kind string and checked
// json.Valid. A definition carrying a response schema now validates against the
// pinned copy of it.
func TestResponseSchemaIsPartOfThePinnedContract(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service := registryTestService(t, registryTestManifestV1, registryTestRequestSchemaV1)
	catalog := NewEnvelopeDefinitionCatalog(service, "v0.12.0")
	binding, err := catalog.ResolveInteractionDefinition(ctx, DefinitionRef{Kind: "tangent.registry-fixture"})
	if err != nil {
		t.Fatalf("ResolveInteractionDefinition: %v", err)
	}
	if binding.ResponseSchemaDigest == "" {
		t.Fatal("binding carries no response schema digest")
	}
	if err := catalog.ValidateInteractionResponse(
		ctx, binding, "data", json.RawMessage(`{"decision":"accept"}`),
	); err != nil {
		t.Fatalf("conforming response rejected: %v", err)
	}
	if err := catalog.ValidateInteractionResponse(
		ctx, binding, "data", json.RawMessage(`{"decision":"maybe"}`),
	); !errors.Is(err, ErrDefinitionValidation) {
		t.Fatalf("non-conforming response error = %v, want ErrDefinitionValidation", err)
	}
	// A wrong response kind still fails on the kind, before the schema.
	if err := catalog.ValidateInteractionResponse(
		ctx, binding, "ack", json.RawMessage(`{"decision":"accept"}`),
	); !errors.Is(err, ErrDefinitionValidation) {
		t.Fatalf("mismatched response kind error = %v, want ErrDefinitionValidation", err)
	}
}

// TestAbsentResponseSchemaPreservesTodaysCheck is the other half of ADR 0003
// §8 C4: a shipped kind that declares compatibility_response_schema: absent
// must keep the response-kind-only check exactly, and must not start rejecting
// responses production accepts.
func TestAbsentResponseSchemaPreservesTodaysCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	manifest := replaceAll(registryTestManifestV1,
		"response_schema: response.schema.json\ncompatibility_response_schema: present",
		"compatibility_response_schema: absent")
	service, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if registerErr := service.RegisterDefinition(
		[]byte(manifest), []byte(registryTestRequestSchemaV1), nil, "tangent",
	); registerErr != nil {
		t.Fatalf("RegisterDefinition: %v", registerErr)
	}
	catalog := NewEnvelopeDefinitionCatalog(service, "v0.12.0")
	binding, err := catalog.ResolveInteractionDefinition(ctx, DefinitionRef{Kind: "tangent.registry-fixture"})
	if err != nil {
		t.Fatalf("ResolveInteractionDefinition: %v", err)
	}
	if binding.ResponseSchemaDigest != "" {
		t.Fatalf("absent response schema still produced a digest %q", binding.ResponseSchemaDigest)
	}
	// Any well-formed JSON object is accepted, exactly as before.
	if err := catalog.ValidateInteractionResponse(
		ctx, binding, "data", json.RawMessage(`{"anything":"at all"}`),
	); err != nil {
		t.Fatalf("absent response schema rejected a response production accepts: %v", err)
	}
}

// TestIncompatibleDefinitionFailsClosedWithATypedState covers ADR 0003 §8 C7:
// a definition this host cannot serve is a distinguishable state carrying a
// reused upstream error code, not an opaque failure, and never a degraded
// surface.
func TestIncompatibleDefinitionFailsClosedWithATypedState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	manifest := replaceAll(registryTestManifestV1,
		`compatible_host_versions: ">=0.12.0 <1.0.0"`,
		`compatible_host_versions: ">=9.0.0"`)
	service, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if registerErr := service.RegisterDefinition(
		[]byte(manifest), []byte(registryTestRequestSchemaV1), []byte(registryTestResponseSchema), "tangent",
	); registerErr != nil {
		t.Fatalf("an unservable definition must still register: %v", registerErr)
	}
	if _, ok := service.Lookup("tangent.registry-fixture"); !ok {
		t.Fatal("registered never implies available, but it must still imply registered")
	}

	catalog := NewEnvelopeDefinitionCatalog(service, "v0.12.0")
	_, err = catalog.ResolveInteractionDefinition(ctx, DefinitionRef{Kind: "tangent.registry-fixture"})
	if !errors.Is(err, ErrDefinitionUnavailable) {
		t.Fatalf("resolve error = %v, want ErrDefinitionUnavailable", err)
	}
	var stateErr *DefinitionStateError
	if !errors.As(err, &stateErr) {
		t.Fatalf("resolve error %v does not carry a typed state", err)
	}
	if stateErr.State != string(definition.StateIncompatible) {
		t.Fatalf("state = %q, want incompatible", stateErr.State)
	}
	if stateErr.ErrorCode != definition.ErrorCodeUnsupportedVersion {
		t.Fatalf("error code = %q, want %q", stateErr.ErrorCode, definition.ErrorCodeUnsupportedVersion)
	}
	if stateErr.Fallback != "" {
		t.Fatalf("no fallback declares preserves_meaning, but one was offered: %q", stateErr.Fallback)
	}

	// The listing still shows it, with its state — "unavailable is a state,
	// not an error to paper over".
	kinds, listErr := catalog.ListInteractionKinds(ctx)
	if listErr != nil {
		t.Fatalf("ListInteractionKinds: %v", listErr)
	}
	var found bool
	for _, kind := range kinds {
		if kind.Kind != "tangent.registry-fixture" {
			continue
		}
		found = true
		if kind.Available {
			t.Error("an incompatible definition is listed as available")
		}
		if kind.State != string(definition.StateIncompatible) || kind.StateReason == "" {
			t.Errorf("listing does not explain the state: %+v", kind)
		}
	}
	if !found {
		t.Fatal("an unservable definition was hidden from the listing instead of explained")
	}
}

// TestQuarantinedDefinitionOffersOnlyASemanticsPreservingFallback covers ADR
// 0003 §8 C5. The host grants no effect capabilities in v0.x, so a definition
// requiring a non-optional one is quarantined; a fallback is offered only
// because this manifest declares one whose preserves_meaning is true.
func TestQuarantinedDefinitionOffersOnlyASemanticsPreservingFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	manifest := replaceAll(registryTestManifestV1,
		"required_capabilities: []",
		"required_capabilities:\n  - id: file.read_scoped\n    rationale: \"Read the selected file.\"")
	manifest = replaceAll(manifest,
		"    preserves_meaning: false\n    degradation: none",
		"    renderer_id: tangent.renderer.fixture-readonly\n    preserves_meaning: true\n    degradation: read-only")

	service, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if registerErr := service.RegisterDefinition(
		[]byte(manifest), []byte(registryTestRequestSchemaV1), []byte(registryTestResponseSchema), "tangent",
	); registerErr != nil {
		t.Fatalf("RegisterDefinition: %v", registerErr)
	}
	catalog := NewEnvelopeDefinitionCatalog(service, "v0.12.0")
	_, err = catalog.ResolveInteractionDefinition(ctx, DefinitionRef{Kind: "tangent.registry-fixture"})
	var stateErr *DefinitionStateError
	if !errors.As(err, &stateErr) {
		t.Fatalf("resolve error %v does not carry a typed state", err)
	}
	if stateErr.State != string(definition.StateQuarantined) {
		t.Fatalf("state = %q, want quarantined", stateErr.State)
	}
	if stateErr.ErrorCode != definition.ErrorCodeCapabilityDenied {
		t.Fatalf("error code = %q, want %q", stateErr.ErrorCode, definition.ErrorCodeCapabilityDenied)
	}
	if stateErr.Fallback != "tangent.renderer.fixture-readonly" {
		t.Fatalf("declared semantics-preserving fallback was not offered: %q", stateErr.Fallback)
	}
}

// TestDisabledKindIsUnavailableNotUnknown keeps the two apart: a kind an
// operator switched off must not look like a kind that was never registered,
// because the caller's next move is different in each case.
func TestDisabledKindIsUnavailableNotUnknown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, err := envelope.New(context.Background(), envelope.WithHostPolicy(definition.HostPolicy{
		HostVersion:     "v0.12.0",
		ProtocolVersion: envelope.ProtocolVersion,
		DisabledKinds:   map[string]bool{"tangent.registry-fixture": true},
	}))
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if registerErr := service.RegisterDefinition(
		[]byte(registryTestManifestV1), []byte(registryTestRequestSchemaV1),
		[]byte(registryTestResponseSchema), "tangent",
	); registerErr != nil {
		t.Fatalf("RegisterDefinition: %v", registerErr)
	}
	catalog := NewEnvelopeDefinitionCatalog(service, "v0.12.0")
	_, err = catalog.ResolveInteractionDefinition(ctx, DefinitionRef{Kind: "tangent.registry-fixture"})
	if errors.Is(err, ErrDefinitionNotFound) {
		t.Fatal("a disabled kind must not be reported as unknown")
	}
	var stateErr *DefinitionStateError
	if !errors.As(err, &stateErr) {
		t.Fatalf("resolve error %v does not carry a typed state", err)
	}
	if stateErr.State != string(definition.StateUnavailable) {
		t.Fatalf("state = %q, want unavailable", stateErr.State)
	}
	if stateErr.ErrorCode != definition.ErrorCodeUnsupportedType {
		t.Fatalf("error code = %q, want %q", stateErr.ErrorCode, definition.ErrorCodeUnsupportedType)
	}
}

// TestRetainedMaterialIsImmutableAndIdempotent covers the storage contract that
// makes retention safe to call on every submission.
func TestRetainedMaterialIsImmutableAndIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := registryTestDB(t)
	store := NewStore(database)
	service := registryTestService(t, registryTestManifestV1, registryTestRequestSchemaV1)
	catalog := NewEnvelopeDefinitionCatalog(service, "v0.12.0", WithRetainedMaterialStore(store))

	binding, err := catalog.ResolveInteractionDefinition(ctx, DefinitionRef{Kind: "tangent.registry-fixture"})
	if err != nil {
		t.Fatalf("ResolveInteractionDefinition: %v", err)
	}
	for range 3 {
		if retainErr := catalog.RetainDefinitionMaterial(ctx, binding); retainErr != nil {
			t.Fatalf("repeated retention must be a no-op: %v", retainErr)
		}
	}
	count, err := store.CountRetainedDefinitions(ctx)
	if err != nil {
		t.Fatalf("CountRetainedDefinitions: %v", err)
	}
	if count != 1 {
		t.Fatalf("retained rows = %d, want 1 — retention is content addressed", count)
	}

	if _, updateErr := database.ExecContext(ctx,
		`UPDATE definition_manifests SET request_schema = '{}' WHERE binding_digest = ?`, binding.Digest,
	); updateErr == nil {
		t.Fatal("retained material was mutable; a pin that can be rewritten guarantees nothing")
	}
	if _, deleteErr := database.ExecContext(ctx,
		`DELETE FROM definition_manifests WHERE binding_digest = ?`, binding.Digest,
	); deleteErr == nil {
		t.Fatal("retained material was deletable")
	}

	retained, found, err := store.GetDefinitionMaterial(ctx, binding.Digest)
	if err != nil || !found {
		t.Fatalf("GetDefinitionMaterial: %v found=%v", err, found)
	}
	if retained.ValidatorRevision != envelopeDefinitionValidatorRevision {
		t.Fatalf("retained validator revision = %q, want %q",
			retained.ValidatorRevision, envelopeDefinitionValidatorRevision)
	}
	if retained.CompatibilityResponseSchema != string(definition.ResponseSchemaPresent) {
		t.Fatalf("retained compatibility marker = %q", retained.CompatibilityResponseSchema)
	}
	if retained.TrustSourceLocator == "" && retained.TrustAssurance == "" {
		t.Fatal("retained material carries no trust evidence")
	}
}

// TestValidatorRevisionBumpFailsClosed asserts the mechanism ADR 0003 §2.9
// keeps "exactly as shipped": a record pinned under a different validator
// revision is refused rather than reinterpreted by new validation code.
func TestValidatorRevisionBumpFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := registryTestDB(t)
	store := NewStore(database)
	service := registryTestService(t, registryTestManifestV1, registryTestRequestSchemaV1)
	catalog := NewEnvelopeDefinitionCatalog(service, "v0.12.0", WithRetainedMaterialStore(store))
	binding, err := catalog.ResolveInteractionDefinition(ctx, DefinitionRef{Kind: "tangent.registry-fixture"})
	if err != nil {
		t.Fatalf("ResolveInteractionDefinition: %v", err)
	}
	if err := catalog.RetainDefinitionMaterial(ctx, binding); err != nil {
		t.Fatalf("RetainDefinitionMaterial: %v", err)
	}

	// Stand in for a future build whose validator semantics changed: the row
	// was written by an older one. The in-memory tier must not paper over it,
	// so the registry is emptied of this kind first.
	service.Registry().UnregisterPlugin("tangent")
	stale := NewEnvelopeDefinitionCatalog(service, "v0.12.0", WithRetainedMaterialStore(
		revisionRewritingStore{DefinitionMaterialStore: store, revision: "some-older-validator"}))
	if err := stale.ValidateInteractionRequest(
		ctx, binding, json.RawMessage(`{"prompt":"pinned"}`),
	); !errors.Is(err, ErrDefinitionUnavailable) {
		t.Fatalf("stale validator revision = %v, want ErrDefinitionUnavailable", err)
	}
}

// revisionRewritingStore reports a different validator revision than the one
// this build uses, without touching the immutable row.
type revisionRewritingStore struct {
	DefinitionMaterialStore
	revision string
}

func (s revisionRewritingStore) GetDefinitionMaterial(
	ctx context.Context,
	digest string,
) (RetainedDefinition, bool, error) {
	retained, found, err := s.DefinitionMaterialStore.GetDefinitionMaterial(ctx, digest)
	retained.ValidatorRevision = s.revision
	return retained, found, err
}

// replaceAll edits the fixture manifest. Named rather than inlined so each
// call site reads as "the same manifest, with one field changed", which is the
// point of every variant below.
func replaceAll(source, old, replacement string) string {
	if !strings.Contains(source, old) {
		panic("definition registry fixture no longer contains " + old)
	}
	return strings.ReplaceAll(source, old, replacement)
}
