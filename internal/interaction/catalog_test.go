package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hollis-labs/tangent/internal/envelope"
)

func TestEnvelopeDefinitionCatalogValidatesAgainstPinnedContent(t *testing.T) {
	t.Parallel()
	service, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	manifest := []byte(`type: test.pinned
version: "1.0"
responseKind: data
`)
	originalSchema := []byte(`{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "$id":"https://schemas.example.test/pinned/1.0/schema.json",
  "type":"object",
  "required":["prompt"],
  "properties":{"prompt":{"type":"string"}},
  "additionalProperties":false
}`)
	if registerErr := service.RegisterTypeFromManifest("test.pinned", manifest, originalSchema, "test"); registerErr != nil {
		t.Fatalf("RegisterTypeFromManifest: %v", registerErr)
	}
	catalog := NewEnvelopeDefinitionCatalog(service, "test-host")
	binding, err := catalog.ResolveInteractionDefinition(context.Background(), DefinitionRef{
		Kind: "test.pinned", Version: "1.0",
	})
	if err != nil {
		t.Fatalf("ResolveInteractionDefinition: %v", err)
	}
	if binding.Digest == "" || binding.SchemaDigest == "" || binding.Assurance != "content-addressed-registry" {
		t.Fatalf("binding is not content addressed: %#v", binding)
	}

	// Simulate a same-name/version registry validator replacement performed
	// outside Tangent's registration boundary. Validation must continue using
	// the exact retained bytes, not reinterpret the pinned interaction through
	// the registry's new compiled schema.
	service.Registry().UnregisterPlugin("test")
	replacementSchema := []byte(`{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "$id":"https://schemas.example.test/pinned/1.0/schema.json",
  "type":"object",
  "required":["replacement"],
  "properties":{"replacement":{"type":"boolean"}},
  "additionalProperties":false
}`)
	if err := service.Registry().RegisterTypeFromManifest(manifest, replacementSchema, "test"); err != nil {
		t.Fatalf("replace registry definition: %v", err)
	}
	if err := catalog.ValidateInteractionRequest(
		context.Background(), binding, json.RawMessage(`{"prompt":"original"}`),
	); err != nil {
		t.Fatalf("pinned original request was reinterpreted: %v", err)
	}
	if err := catalog.ValidateInteractionRequest(
		context.Background(), binding, json.RawMessage(`{"replacement":true}`),
	); !errors.Is(err, ErrDefinitionValidation) {
		t.Fatalf("replacement-only request error = %v, want ErrDefinitionValidation", err)
	}
}

func TestEnvelopeDefinitionCatalogFailsClosedWithoutExactMaterial(t *testing.T) {
	t.Parallel()
	service, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	catalog := NewEnvelopeDefinitionCatalog(service, "test-host")
	if _, err := catalog.ResolveInteractionDefinition(
		context.Background(), DefinitionRef{Kind: "info-card"},
	); !errors.Is(err, ErrDefinitionUnavailable) {
		t.Fatalf("core definition without retained source error = %v, want ErrDefinitionUnavailable", err)
	}
}
