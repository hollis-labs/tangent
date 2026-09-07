package mcp_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/channel"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/relay"
	"github.com/hollis-labs/tangent/internal/room"
)

// relayHarness bundles an MCP client connected to a server wired with
// WithRelay, plus direct access to the two Stores for setup that has no MCP
// path (e.g. anything the operator side would do, since the operator has no
// MCP client — CW-20260906-0017 owns that surface, not this one).
type relayHarness struct {
	client   *mcpsdk.ClientSession
	channels *channel.Store
	relay    *relay.Store
	close    func()
}

func newRelayHarness(t *testing.T) *relayHarness {
	t.Helper()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "mcp-relay.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	err = tangentdb.RunMigrations(database)
	if err != nil {
		t.Fatalf("db.RunMigrations: %v", err)
	}
	channels, err := channel.NewStore(database)
	if err != nil {
		t.Fatalf("channel.NewStore: %v", err)
	}
	relayStore, err := relay.NewStore(database, channels)
	if err != nil {
		t.Fatalf("relay.NewStore: %v", err)
	}
	envSvc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	server, err := tangentmcp.New(
		envSvc, envelope.NewDispatcher(envSvc), room.NewManager(nil), "",
		tangentmcp.WithRelay(channels, relayStore),
	)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	client, closeClient := connectInteractionClient(t, server)
	return &relayHarness{client: client, channels: channels, relay: relayStore, close: closeClient}
}

func relayErrorCode(t *testing.T, result *mcpsdk.CallToolResult) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(extractText(t, result)), &body); err != nil {
		t.Fatalf("decode error body: %v (body %s)", err, extractText(t, result))
	}
	return body.Error.Code
}

func TestRelayToolsRegisterWithFlatSchemasAndNoConditionals(t *testing.T) {
	h := newRelayHarness(t)
	defer h.close()

	listed, err := h.client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := map[string]bool{
		"tangent.relay_open_channel": false, "tangent.relay_attach": false, "tangent.relay_detach": false,
		"tangent.relay_send": false, "tangent.relay_receive": false, "tangent.relay_ack": false,
		"tangent.relay_capabilities": false,
	}
	for _, tool := range listed.Tools {
		if _, tracked := want[tool.Name]; !tracked {
			continue
		}
		want[tool.Name] = true
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal %s input schema: %v", tool.Name, err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("unmarshal %s input schema: %v", tool.Name, err)
		}
		if schema["type"] != "object" {
			t.Errorf("%s input schema has non-object root: %v", tool.Name, schema["type"])
		}
		// The single most load-bearing constraint in the task
		// (CW-20260907-0016): no allOf/if/then/oneOf anywhere in the tree,
		// because mux's discovery schema drops conditional branches and
		// leaves a gated field reaching the model untyped.
		for _, forbidden := range []string{"allOf", "if", "then", "oneOf"} {
			if bytesContainsKey(raw, forbidden) {
				t.Errorf("%s input schema contains %q, which CW-20260907-0016 found gateways drop", tool.Name, forbidden)
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s did not register", name)
		}
	}
}

// bytesContainsKey reports whether a JSON document contains the given
// string as a quoted object key, a cheap approximation that is exactly
// right for the fixed set of conditional keywords this test checks for.
func bytesContainsKey(raw []byte, key string) bool {
	needle := []byte(`"` + key + `"`)
	return jsonBytesIndex(raw, needle) >= 0
}

func jsonBytesIndex(haystack, needle []byte) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func TestRelayOpenChannelCreatesChannelWithTheOperatorAsAMember(t *testing.T) {
	h := newRelayHarness(t)
	defer h.close()

	opened := callInteractionTool[struct {
		ContractVersion       string `json:"contract_version"`
		ChannelID             string `json:"channel_id"`
		OperatorParticipantID string `json:"operator_participant_id"`
	}](t, h.client, "tangent.relay_open_channel", map[string]any{"title": "test channel"})
	if opened.ChannelID == "" || opened.OperatorParticipantID == "" {
		t.Fatalf("relay_open_channel = %+v, want both ids populated", opened)
	}

	members, err := h.channels.ListChannelParticipants(context.Background(), opened.ChannelID)
	if err != nil {
		t.Fatalf("ListChannelParticipants: %v", err)
	}
	if len(members) != 1 || members[0].ID != opened.OperatorParticipantID || members[0].Kind != channel.ParticipantOperator {
		t.Fatalf("members of the opened channel = %+v, want exactly the operator", members)
	}
}

func TestRelayAttachRequiresAnExistingChannelRatherThanCreatingOne(t *testing.T) {
	h := newRelayHarness(t)
	defer h.close()

	result, err := h.client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.relay_attach",
		Arguments: map[string]any{
			"channel_id": "does-not-exist",
			"source":     map[string]any{"application_id": "claude-code", "agent_id": "tangent-14"},
			"runtime":    map[string]any{"authority": "claude-code-cli"},
		},
	})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("relay_attach with a nonexistent channel_id unexpectedly succeeded: %s", extractText(t, result))
	}
	if code := relayErrorCode(t, result); code != "not_found" {
		t.Fatalf("relay_attach error code = %q, want not_found (never a silently created channel)", code)
	}
}

func TestRelayFullLoopAttachSendReceiveAckDetach(t *testing.T) {
	h := newRelayHarness(t)
	defer h.close()
	ctx := context.Background()

	opened := callInteractionTool[struct {
		ChannelID             string `json:"channel_id"`
		OperatorParticipantID string `json:"operator_participant_id"`
	}](t, h.client, "tangent.relay_open_channel", map[string]any{})

	agentSource := map[string]any{"application_id": "claude-code", "agent_id": "tangent-14"}
	attached := callInteractionTool[struct {
		ParticipantID string `json:"participant_id"`
		Generation    int64  `json:"generation"`
	}](t, h.client, "tangent.relay_attach", map[string]any{
		"channel_id": opened.ChannelID, "source": agentSource,
		"runtime": map[string]any{"authority": "claude-code-cli", "endpoint_ref": "session-alpha"},
	})
	if attached.ParticipantID == "" || attached.Generation != 1 {
		t.Fatalf("relay_attach = %+v, want a participant id and generation 1", attached)
	}

	// Agent sends without naming a recipient: defaults to the channel's one
	// operator.
	sent := callInteractionTool[struct {
		ExchangeID string `json:"exchange_id"`
		Recipient  struct {
			ParticipantID string `json:"participant_id"`
		} `json:"recipient"`
		RecipientBindingCurrent bool `json:"recipient_binding_current"`
	}](t, h.client, "tangent.relay_send", map[string]any{
		"channel_id": opened.ChannelID, "idempotency_key": "idem-1", "source": agentSource,
		"body": "attached and reporting in",
	})
	if sent.ExchangeID == "" || sent.Recipient.ParticipantID != opened.OperatorParticipantID {
		t.Fatalf("relay_send = %+v, want it addressed to the channel's operator %s", sent, opened.OperatorParticipantID)
	}
	if sent.RecipientBindingCurrent {
		t.Fatal("relay_send reports a current binding for the operator, which has none — operators are not routed via runtime bindings")
	}

	// The operator's reply has no MCP path (CW-20260906-0017 owns that);
	// seed it directly the way a future REST handler would.
	reply, err := h.relay.AcceptExchange(ctx, relay.AcceptExchangeParams{
		IdempotencyKey: "operator-reply-1", ChannelID: opened.ChannelID,
		SenderParticipantID: opened.OperatorParticipantID, RecipientParticipantID: attached.ParticipantID,
		Body: "acknowledged, proceed",
	})
	if err != nil {
		t.Fatalf("seed operator reply: %v", err)
	}

	received := callInteractionTool[struct {
		Status string `json:"status"`
		Items  []struct {
			ExchangeID string `json:"exchange_id"`
			Body       string `json:"body"`
		} `json:"items"`
		NextCursor int64 `json:"next_cursor"`
	}](t, h.client, "tangent.relay_receive", map[string]any{
		"channel_id": opened.ChannelID, "source": agentSource,
	})
	if received.Status != "ok" || len(received.Items) != 1 || received.Items[0].ExchangeID != reply.ID ||
		received.Items[0].Body != "acknowledged, proceed" {
		t.Fatalf("relay_receive = %+v, want the operator's reply", received)
	}

	acked := callInteractionTool[struct {
		AlreadyAcked bool `json:"already_acked"`
	}](t, h.client, "tangent.relay_ack", map[string]any{
		"source": agentSource, "exchange_id": reply.ID,
	})
	if acked.AlreadyAcked {
		t.Fatal("first ack reports already_acked = true")
	}
	ackedAgain := callInteractionTool[struct {
		AlreadyAcked bool `json:"already_acked"`
	}](t, h.client, "tangent.relay_ack", map[string]any{
		"source": agentSource, "exchange_id": reply.ID,
	})
	if !ackedAgain.AlreadyAcked {
		t.Fatal("repeated ack reports already_acked = false")
	}

	if _, err := h.client.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.relay_detach", Arguments: map[string]any{"channel_id": opened.ChannelID, "source": agentSource},
	}); err != nil {
		t.Fatalf("relay_detach transport error: %v", err)
	}
	if _, err := h.channels.CurrentBinding(ctx, opened.ChannelID, attached.ParticipantID); err == nil {
		t.Fatal("CurrentBinding still resolves after relay_detach")
	}
}

func TestRelaySendRefusesAnAmbiguousDefaultRecipient(t *testing.T) {
	h := newRelayHarness(t)
	defer h.close()
	ctx := context.Background()

	opened := callInteractionTool[struct {
		ChannelID string `json:"channel_id"`
	}](t, h.client, "tangent.relay_open_channel", map[string]any{})
	agentSource := map[string]any{"application_id": "claude-code", "agent_id": "tangent-14"}
	callInteractionTool[struct {
		ParticipantID string `json:"participant_id"`
	}](t, h.client, "tangent.relay_attach", map[string]any{
		"channel_id": opened.ChannelID, "source": agentSource,
		"runtime": map[string]any{"authority": "claude-code-cli"},
	})

	// Zero operators: remove the one OpenChannel created.
	members, err := h.channels.ListChannelParticipants(ctx, opened.ChannelID)
	if err != nil {
		t.Fatalf("ListChannelParticipants: %v", err)
	}
	var operatorID string
	for _, m := range members {
		if m.Kind == channel.ParticipantOperator {
			operatorID = m.ID
		}
	}
	if operatorID == "" {
		t.Fatal("opened channel has no operator to remove")
	}
	err = h.channels.RemoveParticipant(ctx, opened.ChannelID, operatorID)
	if err != nil {
		t.Fatalf("RemoveParticipant: %v", err)
	}

	zeroResult, err := h.client.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.relay_send",
		Arguments: map[string]any{
			"channel_id": opened.ChannelID, "idempotency_key": "idem-zero", "source": agentSource, "body": "hello?",
		},
	})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !zeroResult.IsError || relayErrorCode(t, zeroResult) != "ambiguous_recipient" {
		t.Fatalf("relay_send with zero operators = IsError %v code %q, want ambiguous_recipient",
			zeroResult.IsError, relayErrorCode(t, zeroResult))
	}

	// Two operators: add a second one alongside a freshly re-added original.
	_, err = h.channels.AddParticipant(ctx, opened.ChannelID, operatorID)
	if err != nil {
		t.Fatalf("re-add original operator: %v", err)
	}
	secondOperator, err := h.channels.UpsertParticipant(ctx, channel.UpsertParticipantParams{
		Kind: channel.ParticipantOperator, ExternalAuthority: "operator", ExternalRef: "second",
	})
	if err != nil {
		t.Fatalf("UpsertParticipant (second operator): %v", err)
	}
	_, err = h.channels.AddParticipant(ctx, opened.ChannelID, secondOperator.ID)
	if err != nil {
		t.Fatalf("AddParticipant (second operator): %v", err)
	}

	twoResult, err := h.client.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.relay_send",
		Arguments: map[string]any{
			"channel_id": opened.ChannelID, "idempotency_key": "idem-two", "source": agentSource, "body": "hello?",
		},
	})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !twoResult.IsError || relayErrorCode(t, twoResult) != "ambiguous_recipient" {
		t.Fatalf("relay_send with two operators = IsError %v code %q, want ambiguous_recipient",
			twoResult.IsError, relayErrorCode(t, twoResult))
	}
}

func TestRelayAckRefusesACallerThatIsNotTheRecipient(t *testing.T) {
	h := newRelayHarness(t)
	defer h.close()
	ctx := context.Background()

	opened := callInteractionTool[struct {
		ChannelID             string `json:"channel_id"`
		OperatorParticipantID string `json:"operator_participant_id"`
	}](t, h.client, "tangent.relay_open_channel", map[string]any{})

	agentA := map[string]any{"application_id": "claude-code", "agent_id": "agent-a"}
	agentB := map[string]any{"application_id": "claude-code", "agent_id": "agent-b"}
	for _, source := range []map[string]any{agentA, agentB} {
		callInteractionTool[struct {
			ParticipantID string `json:"participant_id"`
		}](t, h.client, "tangent.relay_attach", map[string]any{
			"channel_id": opened.ChannelID, "source": source, "runtime": map[string]any{"authority": "claude-code-cli"},
		})
	}
	agentAParticipant, err := h.channels.UpsertParticipant(ctx, channel.UpsertParticipantParams{
		Kind: channel.ParticipantAgent, ExternalAuthority: "claude-code", ExternalRef: "agent-a",
	})
	if err != nil {
		t.Fatalf("resolve agent A: %v", err)
	}
	exchange, err := h.relay.AcceptExchange(ctx, relay.AcceptExchangeParams{
		IdempotencyKey: "operator-to-a", ChannelID: opened.ChannelID,
		SenderParticipantID: opened.OperatorParticipantID, RecipientParticipantID: agentAParticipant.ID,
		Body: "for agent A only",
	})
	if err != nil {
		t.Fatalf("seed exchange to agent A: %v", err)
	}

	result, err := h.client.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.relay_ack", Arguments: map[string]any{"source": agentB, "exchange_id": exchange.ID},
	})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !result.IsError || relayErrorCode(t, result) != "unauthorized" {
		t.Fatalf("agent B acking agent A's exchange = IsError %v code %q, want unauthorized",
			result.IsError, relayErrorCode(t, result))
	}
}

func TestRelayReceiveTimesOutWithStatusNotError(t *testing.T) {
	h := newRelayHarness(t)
	defer h.close()

	opened := callInteractionTool[struct {
		ChannelID string `json:"channel_id"`
	}](t, h.client, "tangent.relay_open_channel", map[string]any{})
	agentSource := map[string]any{"application_id": "claude-code", "agent_id": "tangent-14"}
	callInteractionTool[struct {
		ParticipantID string `json:"participant_id"`
	}](t, h.client, "tangent.relay_attach", map[string]any{
		"channel_id": opened.ChannelID, "source": agentSource, "runtime": map[string]any{"authority": "claude-code-cli"},
	})

	received := callInteractionTool[struct {
		Status string `json:"status"`
		Items  []any  `json:"items"`
	}](t, h.client, "tangent.relay_receive", map[string]any{
		"channel_id": opened.ChannelID, "source": agentSource, "wait_ms": 150,
	})
	if received.Status != "timeout" || len(received.Items) != 0 {
		t.Fatalf("relay_receive with nothing pending = %+v, want status timeout and no items", received)
	}
}

func TestRelayReceiveReplaysEverythingSinceAnOldCursorNoGaps(t *testing.T) {
	h := newRelayHarness(t)
	defer h.close()
	ctx := context.Background()

	opened := callInteractionTool[struct {
		ChannelID             string `json:"channel_id"`
		OperatorParticipantID string `json:"operator_participant_id"`
	}](t, h.client, "tangent.relay_open_channel", map[string]any{})
	agentSource := map[string]any{"application_id": "claude-code", "agent_id": "tangent-14"}
	callInteractionTool[struct {
		ParticipantID string `json:"participant_id"`
	}](t, h.client, "tangent.relay_attach", map[string]any{
		"channel_id": opened.ChannelID, "source": agentSource, "runtime": map[string]any{"authority": "claude-code-cli"},
	})
	agentParticipant, err := h.channels.UpsertParticipant(ctx, channel.UpsertParticipantParams{
		Kind: channel.ParticipantAgent, ExternalAuthority: "claude-code", ExternalRef: "tangent-14",
	})
	if err != nil {
		t.Fatalf("resolve agent: %v", err)
	}
	for _, key := range []string{"op-1", "op-2", "op-3"} {
		if _, err := h.relay.AcceptExchange(ctx, relay.AcceptExchangeParams{
			IdempotencyKey: key, ChannelID: opened.ChannelID,
			SenderParticipantID: opened.OperatorParticipantID, RecipientParticipantID: agentParticipant.ID,
			Body: key,
		}); err != nil {
			t.Fatalf("seed exchange %s: %v", key, err)
		}
	}

	received := callInteractionTool[struct {
		Items      []any `json:"items"`
		NextCursor int64 `json:"next_cursor"`
	}](t, h.client, "tangent.relay_receive", map[string]any{
		"channel_id": opened.ChannelID, "source": agentSource, "cursor": 0,
	})
	if len(received.Items) != 3 {
		t.Fatalf("relay_receive from cursor 0 with three exchanges already durable = %+v, want all 3, no gap", received)
	}
}

func TestRelaySendCorrelatesToAStructuredSubject(t *testing.T) {
	h := newRelayHarness(t)
	defer h.close()
	ctx := context.Background()

	opened := callInteractionTool[struct {
		ChannelID string `json:"channel_id"`
	}](t, h.client, "tangent.relay_open_channel", map[string]any{})
	agentSource := map[string]any{"application_id": "claude-code", "agent_id": "tangent-14"}
	callInteractionTool[struct {
		ParticipantID string `json:"participant_id"`
	}](t, h.client, "tangent.relay_attach", map[string]any{
		"channel_id": opened.ChannelID, "source": agentSource, "runtime": map[string]any{"authority": "claude-code-cli"},
	})
	subject, err := h.channels.AddSubject(ctx, channel.CreateSubjectParams{
		ChannelID: opened.ChannelID, Type: channel.SubjectFreeform, Title: "the thread this message belongs to",
	})
	if err != nil {
		t.Fatalf("AddSubject: %v", err)
	}

	sent := callInteractionTool[struct {
		SubjectID string `json:"subject_id"`
	}](t, h.client, "tangent.relay_send", map[string]any{
		"channel_id": opened.ChannelID, "idempotency_key": "idem-subject", "source": agentSource,
		"subject_id": subject.ID, "body": "about that thread",
	})
	if sent.SubjectID != subject.ID {
		t.Fatalf("relay_send subject_id = %q, want %q", sent.SubjectID, subject.ID)
	}
}

func TestRelayCapabilitiesValidatesPairingInCodeNotSchema(t *testing.T) {
	h := newRelayHarness(t)
	defer h.close()
	ctx := context.Background()

	listed, err := h.client.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "tangent.relay_capabilities" {
			continue
		}
		raw, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Required []string `json:"required"`
		}
		unmarshalErr := json.Unmarshal(raw, &schema)
		if unmarshalErr != nil {
			t.Fatalf("unmarshal schema: %v", unmarshalErr)
		}
		if len(schema.Required) != 0 {
			t.Fatalf("tangent.relay_capabilities schema requires %v, want both fields independently optional", schema.Required)
		}
	}

	// Neither field: succeeds, generic answer.
	generic := callInteractionTool[struct {
		Adapter string `json:"adapter"`
	}](t, h.client, "tangent.relay_capabilities", map[string]any{})
	if generic.Adapter != "cli-relay" {
		t.Fatalf("relay_capabilities (generic) = %+v", generic)
	}

	opened := callInteractionTool[struct {
		ChannelID string `json:"channel_id"`
	}](t, h.client, "tangent.relay_open_channel", map[string]any{})

	// channel_id without source: the pairing rule refuses in code.
	paired, err := h.client.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.relay_capabilities", Arguments: map[string]any{"channel_id": opened.ChannelID},
	})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !paired.IsError || relayErrorCode(t, paired) != "validation_failed" {
		t.Fatalf("relay_capabilities with channel_id and no source = IsError %v code %q, want validation_failed",
			paired.IsError, relayErrorCode(t, paired))
	}

	// Both fields, a real channel and a resolvable source: succeeds.
	agentSource := map[string]any{"application_id": "claude-code", "agent_id": "tangent-14"}
	scoped := callInteractionTool[struct {
		Adapter string `json:"adapter"`
	}](t, h.client, "tangent.relay_capabilities", map[string]any{
		"channel_id": opened.ChannelID, "source": agentSource,
	})
	if scoped.Adapter != "cli-relay" {
		t.Fatalf("relay_capabilities (scoped) = %+v", scoped)
	}
}
