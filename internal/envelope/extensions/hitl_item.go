package extensions

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

// HITLItemEnvelopeType is the immutable definition kind used by items in the
// durable operator-owned HITL inbox. The public MCP tools remain operation
// names (tangent.hitl_enqueue/get/await/withdraw); this name identifies the
// request definition pinned by an InteractionRecord.
const HITLItemEnvelopeType = "tangent.hitl-item"

// HITLItemContractVersion is required in every v1 request, command, handle,
// terminal outcome, and retrieval projection. It intentionally matches the
// definition version in hitlItemManifest.
const HITLItemContractVersion = "1.0"

// Stable $defs entry points used to generate MCP schemas and TypeScript types.
const (
	HITLItemRequestDefinition         = "HITLItemRequestV1"
	HITLResolutionCommandDefinition   = "HITLResolutionCommandV1"
	HITLTerminalOutcomeDefinition     = "HITLTerminalOutcomeV1"
	HITLGetCommandDefinition          = "HITLGetCommandV1"
	HITLAwaitCommandDefinition        = "HITLAwaitCommandV1"
	HITLWithdrawCommandDefinition     = "HITLWithdrawCommandV1"
	HITLItemHandleDefinition          = "HITLItemHandleV1"
	HITLItemViewDefinition            = "HITLItemViewV1"
	HITLRetrievalResultDefinition     = "HITLRetrievalResultV1"
	HITLStaleRevisionDefinition       = "HITLStaleRevisionErrorV1"
	HITLIdempotencyConflictDefinition = "HITLIdempotencyConflictErrorV1"
)

var hitlItemManifest = []byte(`type: tangent.hitl-item
version: "1.0"
description: "Durable HITL inbox item: one operator-owned approval or persistent-attention interaction with typed evidence and an immutable per-item terminal outcome."
responseKind: data
`)

// hitlItemSchema is the canonical JSON Schema bundle for the HITL v1 contract.
// Its root validates enqueue/item requests. Reusable $defs specify resolution
// commands, terminal outcomes, handles, retrieval views, and stale-revision
// errors for the HTTP, WebSocket, MCP, and TypeScript adapters built on top.
//
//go:embed hitl_item_schema.json
var hitlItemSchema []byte

// HITLItemContractSchema returns a defensive copy of the complete v1 schema
// bundle. Callers that need one operation schema should use
// HITLContractDefinitionSchema so the selected definition becomes the root.
func HITLItemContractSchema() []byte {
	return bytes.Clone(hitlItemSchema)
}

// HITLContractDefinitionSchema promotes one named $defs entry to the document
// root while retaining the shared definitions it references. MCP adapters use
// this to derive explicit object-rooted input schemas without copying fields.
func HITLContractDefinitionSchema(name string) ([]byte, error) {
	var bundle map[string]json.RawMessage
	if err := json.Unmarshal(hitlItemSchema, &bundle); err != nil {
		return nil, fmt.Errorf("extensions: decode hitl-item schema: %w", err)
	}
	var definitions map[string]json.RawMessage
	if err := json.Unmarshal(bundle["$defs"], &definitions); err != nil {
		return nil, fmt.Errorf("extensions: decode hitl-item definitions: %w", err)
	}
	selected, ok := definitions[name]
	if !ok {
		return nil, fmt.Errorf("extensions: unknown hitl-item schema definition %q", name)
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(selected, &root); err != nil {
		return nil, fmt.Errorf("extensions: decode hitl-item definition %q: %w", name, err)
	}
	root["$schema"] = bundle["$schema"]
	root["$defs"] = bundle["$defs"]
	return json.Marshal(root)
}

// RegisterHITLItem registers the v1 request definition. Production installs it
// together with the durable HITL application service before MCP tool
// registration so the persisted binding and advertised operation schemas are
// derived from the same immutable contract material.
func RegisterHITLItem(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	if err := svc.RegisterTypeFromManifest(HITLItemEnvelopeType, hitlItemManifest, hitlItemSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register hitl-item: %w", err)
	}
	return nil
}
