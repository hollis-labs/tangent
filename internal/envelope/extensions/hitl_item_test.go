package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	envelopes "github.com/hollis-labs/libs/ui-go/envelopes"

	"github.com/hollis-labs/tangent/internal/envelope"
)

func TestHITLItemContract_RegistersPinnedDefinition(t *testing.T) {
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if err := RegisterHITLItem(svc); err != nil {
		t.Fatalf("RegisterHITLItem: %v", err)
	}

	spec, ok := svc.Lookup(HITLItemEnvelopeType)
	if !ok {
		t.Fatalf("definition %q was not registered", HITLItemEnvelopeType)
	}
	// The DEFINITION version, which is not HITLItemContractVersion. The two
	// were both "1.0" until ADR 0009 bumped every manifest, and this assertion
	// read as though they were one fact. They are not: HITLItemContractVersion
	// is the `contract_version` required in every v1 request, command, handle
	// and receipt, and it does not move when the manifest does.
	if spec.Version != HITLItemDefinitionVersion {
		t.Fatalf("definition version = %q, want %q", spec.Version, HITLItemDefinitionVersion)
	}
	if HITLItemContractVersion != "1.0" {
		t.Fatalf("the v1 payload contract version moved to %q; every v1 request, command, "+
			"handle and receipt carries it, so this is a wire break rather than a manifest bump",
			HITLItemContractVersion)
	}
	if spec.PluginID != PluginID {
		t.Fatalf("plugin id = %q, want %q", spec.PluginID, PluginID)
	}
	if spec.DataSchema == nil {
		t.Fatal("registered definition has no request schema")
	}
}

func TestHITLItemContract_AllPublishedExamplesValidate(t *testing.T) {
	doc := decodeHITLSchema(t)
	defs := schemaDefinitions(t, doc)

	for definitionName, definitionValue := range defs {
		definition, ok := definitionValue.(map[string]any)
		if !ok {
			continue
		}
		examples, ok := definition["examples"].([]any)
		if !ok {
			continue
		}
		for index, example := range examples {
			if err := validateHITLDefinition(definitionName, example); err != nil {
				t.Errorf("%s example %d: %v", definitionName, index+1, err)
			}
		}
	}
}

func TestHITLItemContract_AttentionRequestAndAcknowledgementVariantsValidate(t *testing.T) {
	request := map[string]any{
		"contract_version": "1.0",
		"kind":             "attention",
		"idempotency_key":  "agent:warning-17",
		"title":            "Worker warning",
		"summary":          "The worker recovered after a transient fault.",
		"request":          "Acknowledge the warning after reviewing it.",
		"source": map[string]any{
			"application_id": "codex",
			"agent_id":       "worker-17",
		},
		"action_labels": map[string]any{
			"acknowledge":           "Mark seen",
			"acknowledge_with_note": "Log context",
			"reply":                 "Respond to worker",
		},
	}
	if err := validateHITLDefinition(HITLItemRequestDefinition, request); err != nil {
		t.Fatalf("attention request: %v", err)
	}

	responses := []map[string]any{
		{"kind": "attention", "decision": "acknowledged"},
		{"kind": "attention", "decision": "acknowledged", "note": "Logged for the next shift."},
		{"kind": "attention", "decision": "acknowledged", "reply": "Worker restarted safely."},
		{
			"kind": "attention", "decision": "acknowledged",
			"note": "Logged for the next shift.", "reply": "Worker restarted safely.",
		},
	}
	for index, response := range responses {
		command := map[string]any{
			"contract_version":              "1.0",
			"item_id":                       "interaction-attention-17",
			"expected_revision":             4,
			"presented_projection_revision": 3,
			"response":                      response,
		}
		if err := validateHITLDefinition(HITLResolutionCommandDefinition, command); err != nil {
			t.Errorf("attention acknowledgement variant %d: %v", index+1, err)
		}
	}
}

func TestHITLItemContract_GeneratesExplicitMCPInputRoots(t *testing.T) {
	inputDefinitions := []string{
		HITLItemRequestDefinition,
		HITLResolutionCommandDefinition,
		HITLGetCommandDefinition,
		HITLAwaitCommandDefinition,
		HITLWithdrawCommandDefinition,
	}
	for _, definitionName := range inputDefinitions {
		raw, err := HITLContractDefinitionSchema(definitionName)
		if err != nil {
			t.Fatalf("HITLContractDefinitionSchema(%q): %v", definitionName, err)
		}
		var root map[string]any
		if err := json.Unmarshal(raw, &root); err != nil {
			t.Fatalf("decode %q schema: %v", definitionName, err)
		}
		if got := root["type"]; got != "object" {
			t.Errorf("%s root type = %v, want object", definitionName, got)
		}
		if _, ok := root["$defs"].(map[string]any); !ok {
			t.Errorf("%s does not retain shared $defs", definitionName)
		}
		var mcpSchema jsonschema.Schema
		if err := json.Unmarshal(raw, &mcpSchema); err != nil {
			t.Errorf("%s does not decode as MCP SDK JSON Schema: %v", definitionName, err)
		} else if mcpSchema.Type != "object" {
			t.Errorf("%s MCP schema type = %q, want object", definitionName, mcpSchema.Type)
		}
	}

	if _, err := HITLContractDefinitionSchema("MissingContractType"); err == nil {
		t.Fatal("unknown schema definition unexpectedly succeeded")
	}
}

func TestHITLItemContract_RejectsUnsafeOrMismatchedShapes(t *testing.T) {
	tests := []struct {
		name       string
		definition string
		value      string
	}{
		{
			name:       "attention cannot carry approval impact",
			definition: "HITLItemRequestV1",
			value: `{
				"contract_version":"1.0",
				"kind":"attention",
				"idempotency_key":"agent:attention-1",
				"title":"Warning",
				"summary":"A warning needs attention.",
				"request":"Acknowledge it.",
				"source":{"application_id":"codex","agent_id":"worker"},
				"impact":{"approve":"continue","deny":"stop"}
			}`,
		},
		{
			name:       "artifact raw path is not authority",
			definition: "HITLItemRequestV1",
			value: `{
				"contract_version":"1.0",
				"kind":"approval",
				"idempotency_key":"agent:approval-1",
				"title":"Review",
				"summary":"Review an artifact.",
				"request":"Approve it.",
				"source":{"application_id":"codex","agent_id":"worker"},
				"evidence":[{
					"type":"artifact_ref",
					"label":"local file",
					"authority":"caller",
					"artifact_id":"artifact-1",
					"digest":"sha256:abc",
					"path":"/tmp/report.md"
				}]
			}`,
		},
		{
			name:       "artifact id cannot be a local path",
			definition: "ArtifactRefEvidenceV1",
			value: `{
				"type":"artifact_ref",
				"label":"local file",
				"authority":"caller",
				"artifact_id":"/tmp/report.md",
				"digest":"sha256:abc"
			}`,
		},
		{
			name:       "artifact id cannot be a file URI",
			definition: "ArtifactRefEvidenceV1",
			value: `{
				"type":"artifact_ref",
				"label":"local file URI",
				"authority":"caller",
				"artifact_id":"file:///tmp/report.md",
				"digest":"sha256:abc"
			}`,
		},
		{
			name:       "safe preview id cannot be a local path",
			definition: "ArtifactRefEvidenceV1",
			value: `{
				"type":"artifact_ref",
				"label":"unsafe preview",
				"authority":"caller",
				"artifact_id":"artifact-1",
				"digest":"sha256:abc",
				"safe_preview_artifact_id":"../preview.png"
			}`,
		},
		{
			name:       "get requires stable caller assertion",
			definition: "HITLGetCommandV1",
			value:      `{"contract_version":"1.0","item_id":"interaction-1"}`,
		},
		{
			name:       "await cannot exceed HTTP-safe wait ceiling",
			definition: "HITLAwaitCommandV1",
			value: `{
				"contract_version":"1.0",
				"item_id":"interaction-1",
				"caller":{"application_id":"codex"},
				"wait_ms":50001
			}`,
		},
		{
			name:       "with-note action cannot submit an empty note",
			definition: "HITLResolutionCommandV1",
			value: `{
				"contract_version":"1.0",
				"item_id":"interaction-1",
				"expected_revision":4,
				"presented_projection_revision":3,
				"response":{"kind":"approval","decision":"approved","note":""}
			}`,
		},
		{
			name:       "attention cannot use an approval decision",
			definition: "HITLResolutionCommandV1",
			value: `{
				"contract_version":"1.0",
				"item_id":"interaction-1",
				"expected_revision":4,
				"presented_projection_revision":3,
				"response":{"kind":"attention","decision":"approved"}
			}`,
		},
		{
			name:       "attention reply cannot be empty",
			definition: "HITLResolutionCommandV1",
			value: `{
				"contract_version":"1.0",
				"item_id":"interaction-1",
				"expected_revision":4,
				"presented_projection_revision":3,
				"response":{"kind":"attention","decision":"acknowledged","reply":""}
			}`,
		},
		{
			name:       "attention acknowledgement rejects approval-only fields",
			definition: "HITLResolutionCommandV1",
			value: `{
				"contract_version":"1.0",
				"item_id":"interaction-1",
				"expected_revision":4,
				"presented_projection_revision":3,
				"response":{"kind":"attention","decision":"acknowledged","approve":true}
			}`,
		},
		{
			name:       "unknown cancellation cause is rejected",
			definition: "HITLTerminalOutcomeV1",
			value: `{
				"contract_version":"1.0",
				"state":"canceled",
				"item_id":"interaction-1",
				"interaction_revision":5,
				"cause":"transport_canceled",
				"terminated_at":"2026-09-04T03:30:00Z"
			}`,
		},
		{
			name:       "terminal view requires terminal outcome and no pending position",
			definition: "HITLItemViewV1",
			value: `{
				"contract_version":"1.0",
				"surface_id":"surface-1",
				"item_id":"interaction-1",
				"state":"resolved",
				"revision":5,
				"queue_sequence":1,
				"queue_position":1,
				"request_snapshot":{
					"contract_version":"1.0",
					"kind":"approval",
					"idempotency_key":"agent:approval-1",
					"title":"Review",
					"summary":"Review a change.",
					"request":"Approve it.",
					"source":{"application_id":"codex","agent_id":"worker"}
				},
				"enqueued_at":"2026-09-04T03:20:00Z",
				"updated_at":"2026-09-04T03:30:00Z"
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(tt.value), &value); err != nil {
				t.Fatalf("decode test value: %v", err)
			}
			if err := validateHITLDefinition(tt.definition, value); err == nil {
				t.Fatalf("%s unexpectedly accepted invalid value", tt.definition)
			}
		})
	}
}

func TestHITLItemContract_EnforcesHandleRetrievalAndStaleDiscriminants(t *testing.T) {
	nonterminal := hitlItemViewFixture(false)
	terminal := hitlItemViewFixture(true)

	validRetrievals := []map[string]any{
		hitlRetrievalFixture("get", "not_waited", nonterminal),
		hitlRetrievalFixture("get", "not_waited", terminal),
		hitlRetrievalFixture("await", "terminal", terminal),
		hitlRetrievalFixture("await", "timeout", nonterminal),
	}
	for index, value := range validRetrievals {
		if err := validateHITLDefinition(HITLRetrievalResultDefinition, value); err != nil {
			t.Errorf("valid retrieval %d: %v", index+1, err)
		}
	}

	invalidRetrievals := []map[string]any{
		hitlRetrievalFixture("get", "timeout", nonterminal),
		hitlRetrievalFixture("await", "not_waited", nonterminal),
		hitlRetrievalFixture("await", "terminal", nonterminal),
		hitlRetrievalFixture("await", "timeout", terminal),
	}
	for index, value := range invalidRetrievals {
		if err := validateHITLDefinition(HITLRetrievalResultDefinition, value); err == nil {
			t.Errorf("invalid retrieval %d unexpectedly validated", index+1)
		}
	}

	terminalHandle := map[string]any{
		"contract_version": "1.0",
		"surface_id":       "surface-1",
		"item_id":          "interaction-1",
		"state":            "resolved",
		"revision":         5,
		"queue_sequence":   1,
		"queue_position":   1,
		"inbox_url":        "/hitl",
		"item_url":         "/hitl/items/interaction-1",
	}
	if err := validateHITLDefinition(HITLItemHandleDefinition, terminalHandle); err == nil {
		t.Error("terminal handle with a pending queue position unexpectedly validated")
	}

	withdrawProjectionStale := map[string]any{
		"contract_version":  "1.0",
		"code":              "stale_revision",
		"operation":         "withdraw",
		"item_id":           "interaction-1",
		"revision_kind":     "presented_projection",
		"expected_revision": 3,
		"actual_revision":   4,
		"current_state":     "presented",
	}
	if err := validateHITLDefinition(HITLStaleRevisionDefinition, withdrawProjectionStale); err == nil {
		t.Error("withdraw projection-staleness combination unexpectedly validated")
	}
}

func hitlRetrievalFixture(mode, waitStatus string, item map[string]any) map[string]any {
	return map[string]any{
		"contract_version": "1.0",
		"mode":             mode,
		"wait_status":      waitStatus,
		"retrieved_at":     "2026-09-04T03:29:00Z",
		"item":             item,
	}
}

func hitlItemViewFixture(terminal bool) map[string]any {
	item := map[string]any{
		"contract_version": "1.0",
		"surface_id":       "surface-1",
		"item_id":          "interaction-1",
		"state":            "staged",
		"revision":         3,
		"queue_sequence":   1,
		"queue_position":   1,
		"request_snapshot": map[string]any{
			"contract_version": "1.0",
			"kind":             "approval",
			"idempotency_key":  "codex:approval-1",
			"title":            "Review",
			"summary":          "Review a change.",
			"request":          "Approve it.",
			"source": map[string]any{
				"application_id": "codex",
				"agent_id":       "worker",
			},
		},
		"enqueued_at": "2026-09-04T03:20:00Z",
		"updated_at":  "2026-09-04T03:29:00Z",
	}
	if terminal {
		item["state"] = "resolved"
		item["revision"] = 5
		item["queue_position"] = nil
		item["terminal_outcome"] = map[string]any{
			"contract_version":     "1.0",
			"state":                "resolved",
			"item_id":              "interaction-1",
			"interaction_revision": 5,
			"resolution": map[string]any{
				"resolution_id":                 "resolution-1",
				"response":                      map[string]any{"kind": "approval", "decision": "approved"},
				"participant":                   map[string]any{"principal_ref": "local-operator", "authority": "tangent-loopback", "assurance": "loopback-unverified"},
				"resolved_at":                   "2026-09-04T03:29:00Z",
				"interaction_revision":          5,
				"presented_projection_revision": 3,
			},
		}
	}
	return item
}

func decodeHITLSchema(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(hitlItemSchema, &doc); err != nil {
		t.Fatalf("decode embedded HITL schema: %v", err)
	}
	return doc
}

func schemaDefinitions(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatal("HITL schema has no $defs object")
	}
	return defs
}

func validateHITLDefinition(definitionName string, value any) error {
	raw, err := HITLContractDefinitionSchema(definitionName)
	if err != nil {
		return err
	}
	registry := envelopes.NewRegistry()
	manifest := []byte("type: tangent.hitl-contract-test\nversion: \"1.0\"\nresponseKind: data\n")
	if err := registry.RegisterTypeFromManifest(manifest, raw, "tangent-test"); err != nil {
		return fmt.Errorf("compile %s: %w", definitionName, err)
	}
	spec, ok := registry.Lookup("tangent.hitl-contract-test")
	if !ok || spec.DataSchema == nil {
		return fmt.Errorf("compiled definition %s is unavailable", definitionName)
	}
	if err := spec.DataSchema.Validate(value); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	return nil
}
