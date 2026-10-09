package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/tangent/internal/turns"
	"github.com/hollis-labs/tangent/pkg/plugin"
)

func TestTurnsToolsPublicAnnotatedPublication(t *testing.T) {
	server, operator := newTurnsRig(t)
	client, closeClient := connectInteractionClient(t, server)
	defer closeClient()
	request := plugin.AgentTurnRequest{
		ContractVersion: plugin.AgentTurnContractVersion,
		IdempotencyKey:  "synthetic-publication:42", Kind: "checkpoint",
		Source: plugin.AgentTurnSource{AgentID: "msg://agent/local/test-sender"},
		Title:  "Ordinary publication", Summary: "Plain summary", Content: "  Original\n😺 **body**\n",
		Annotations:   []plugin.TurnAnnotation{{SchemaVersion: 1, StageID: "summarize", StageVersion: "1", Kind: "summary", Summary: plugin.TurnSummary{Text: "Plain summary"}}},
		StageTrace:    []plugin.TurnStageTrace{{StageID: "summarize", StageVersion: "1", Outcome: "passed", DurationMS: 5}},
		SourceMessage: &plugin.TurnSourceMessage{SchemaVersion: 1, Origin: "publication", EndpointRef: "test-endpoint", Channel: "test-inbox", MessageID: "source-42", Sequence: 42, SenderURN: "msg://agent/local/test-sender"},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var arguments map[string]any
	if err = json.Unmarshal(raw, &arguments); err != nil {
		t.Fatal(err)
	}
	handle := callInteractionTool[turns.TurnHandle](t, client, "tangent.turns_enqueue", arguments)
	if handle.ContractVersion != "1.1" || handle.SessionID != "" || handle.TurnID != "" {
		t.Fatalf("publication handle invented runtime identity: %#v", handle)
	}
	retry := callInteractionTool[turns.TurnHandle](t, client, "tangent.turns_enqueue", arguments)
	if retry.ItemID != handle.ItemID {
		t.Fatal("replay produced another publication")
	}
	view, err := operator.InspectTurn(context.Background(), handle.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Replyable || view.Content != request.Content || view.Summary != request.Summary || len(view.Annotations) != 1 || len(view.StageTrace) != 1 || view.SourceMessage.MessageID != "source-42" {
		t.Fatalf("public MCP payload lost the original or metadata: %#v", view)
	}
}
