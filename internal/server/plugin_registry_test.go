package server

import (
	"encoding/json"
	"testing"

	"github.com/hollis-labs/plugin-sdk/registry"
)

// TestRegistryResponseShape validates that buildRegistryResponse returns a
// well-formed registry.Response that serializes correctly (CW-20260911-0035
// minimal proof).
func TestRegistryResponseShape(t *testing.T) {
	resp := buildRegistryResponse("test-host")

	// Registry version is independent of subprocess protocol.
	if resp.RegistryVersion != registry.RegistryVersion {
		t.Errorf("got registry version %d, want %d", resp.RegistryVersion, registry.RegistryVersion)
	}
	if resp.RegistryVersion != 2 {
		t.Errorf("got registry version %d, want 2", resp.RegistryVersion)
	}

	if resp.HostInstance != "test-host" || resp.Revision != 1 {
		t.Fatalf("invalid snapshot identity: %+v", resp)
	}
	// Maps and refusal list must be non-nil, even when empty.
	if resp.Kinds == nil || resp.Regions == nil || resp.Refusals == nil {
		t.Fatal("required registry collection is nil")
	}
	if resp.Plugins == nil {
		t.Error("Plugins map is nil; must be non-nil empty map")
	}
	if resp.Contributions == nil {
		t.Error("Contributions map is nil; must be non-nil empty map")
	}

	// Must serialize to valid JSON.
	payload, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to serialize registry response: %v", err)
	}

	// Deserialize back and verify shape.
	var decoded registry.Response
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("failed to deserialize registry response: %v", err)
	}

	if decoded.RegistryVersion != 2 {
		t.Errorf("roundtrip: got registry version %d, want 2", decoded.RegistryVersion)
	}
	if decoded.Plugins == nil {
		t.Error("roundtrip: Plugins is nil after deserialize")
	}
	if decoded.Contributions == nil {
		t.Error("roundtrip: Contributions is nil after deserialize")
	}
}

// TestRegistryResponseValidates verifies that buildRegistryResponse returns
// a response that passes the SDK's own Validate check.
func TestRegistryResponseValidates(t *testing.T) {
	resp := buildRegistryResponse("test-host")
	if err := resp.Validate(); err != nil {
		t.Errorf("registry response validation failed: %v", err)
	}
}
