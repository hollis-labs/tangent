package extensions

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

// HITLPackageID is the package tangent.hitl-item ships in. ADR 0003 §6
// designates it the reference implementation of the package boundary.
const HITLPackageID = "tangent.hitl"

// HITLItemEnvelopeType is the immutable definition kind used by items in the
// durable operator-owned HITL inbox. The public MCP tools remain operation
// names (tangent.hitl_enqueue/get/await/withdraw); this name identifies the
// request definition pinned by an InteractionRecord.
const HITLItemEnvelopeType = "tangent.hitl-item"

// HITLItemContractVersion is the PAYLOAD contract version, required in every v1
// request, command, handle, terminal outcome, and retrieval projection.
//
// It is not the definition version, and until ADR 0009 nothing made that
// visible: both were "1.0", and one constant was used for both jobs — including
// as the `Version` on the DefinitionRef that pins an interaction. The ADR bumped
// every manifest, the two numbers separated, and every HITL enqueue started
// failing with "definition not found: tangent.hitl-item@1.0".
//
// They move for different reasons and must stay apart. This one moving is a
// WIRE BREAK: every v1 payload in flight carries it. The definition version
// moving is a manifest change, which ADR 0003 §3 says happens whenever the
// contract or the renderer identity does.
const HITLItemContractVersion = "1.0"

// HITLItemDefinitionVersion is the version of the shipped manifest, and is what
// pins an interaction to a definition. It must equal the `version` field in
// packages/tangent.hitl/hitl-item/manifest.yaml; TestHITLItemContract_RegistersPinnedDefinition
// is what holds the two together.
const HITLItemDefinitionVersion = "1.1"

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

// hitlItemSchema is the canonical JSON Schema bundle for the HITL v1 contract.
// Its root validates enqueue/item requests. Reusable $defs specify resolution
// commands, terminal outcomes, handles, retrieval views, and stale-revision
// errors for the HTTP, WebSocket, MCP, and TypeScript adapters built on top.
//
// It is read from the shipped package tree rather than embedded separately, so
// the bundle the contract adapters validate against and the bundle the
// registry digests as this definition's request_schema are necessarily the
// same bytes. A read failure here is a broken build, not a runtime condition.
var hitlItemSchema = mustPackageFile(HITLPackageID, HITLItemEnvelopeType, requestSchemaFileName)

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
	return registerPackagedDefinition(svc, HITLPackageID, HITLItemEnvelopeType)
}
