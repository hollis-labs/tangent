package mcp_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/plugins/runner"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/turns"
)

// newTurnsRig builds an MCP server over a real durable substrate with the turns
// service attached, and returns the service too so a test can play the operator,
// who acts through the browser API and has no tool.
func newTurnsRig(t *testing.T) (*tangentmcp.Server, *turns.Service) {
	t.Helper()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "mcp-turns.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrationErr := tangentdb.RunMigrations(database); migrationErr != nil {
		t.Fatalf("db.RunMigrations: %v", migrationErr)
	}
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if registerErr := extensions.RegisterAgentTurn(envelopeService); registerErr != nil {
		t.Fatalf("RegisterAgentTurn: %v", registerErr)
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, tangentmcp.HostVersion),
		interaction.WithSurfaceAccessPolicy(turns.SurfaceAccessPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	turnsService, err := turns.NewService(interactions, turns.WithAwaitPollInterval(time.Millisecond))
	if err != nil {
		t.Fatalf("turns.NewService: %v", err)
	}
	server, err := tangentmcp.New(
		envelopeService, envelope.NewDispatcher(envelopeService), room.NewManager(nil), "",
		tangentmcp.WithTurnsService(turnsService),
	)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	return server, turnsService
}

func turnRequest(sessionID, turnID string) map[string]any {
	return map[string]any{
		"contract_version": "1.0",
		"session_id":       sessionID,
		"turn_id":          turnID,
		"idempotency_key":  "mcp:" + sessionID + ":" + turnID,
		"kind":             "question",
		"source":           map[string]any{"agent_id": "worker-1", "application_id": "codex", "agent_label": "Worker"},
		"title":            "Which branch should I target?",
		"content":          "main or release/2.x?",
		"options": []map[string]any{
			{"label": "main", "value": "main", "recommended": true},
			{"label": "release/2.x", "value": "rel"},
		},
	}
}

func TestTurnsToolsAreListedWithTheirContract(t *testing.T) {
	server, _ := newTurnsRig(t)
	client, closeClient := connectInteractionClient(t, server)
	defer closeClient()

	listed, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	byName := map[string]*mcpsdk.Tool{}
	for _, tool := range listed.Tools {
		byName[tool.Name] = tool
	}

	wantHints := map[string]struct{ readOnly, idempotent bool }{
		"tangent.turns_enqueue": {readOnly: false, idempotent: true},
		"tangent.turn_await":    {readOnly: true, idempotent: false},
		"tangent.turn_ack":      {readOnly: false, idempotent: true},
	}
	for name, want := range wantHints {
		tool := byName[name]
		if tool == nil {
			t.Fatalf("%s is not on the surface", name)
		}
		if tool.Annotations == nil ||
			tool.Annotations.ReadOnlyHint != want.readOnly || tool.Annotations.IdempotentHint != want.idempotent {
			t.Errorf("%s annotations = %+v, want readOnly=%v idempotent=%v", name, tool.Annotations, want.readOnly, want.idempotent)
		}
		var schema map[string]any
		raw, _ := json.Marshal(tool.InputSchema)
		if err := json.Unmarshal(raw, &schema); err != nil || schema["type"] != "object" {
			t.Errorf("%s input schema has no object root: %s", name, raw)
		}
	}

	// wait_ms is bounded in the schema, not only in the service, so a client
	// sees the limit before it calls. It must be a plain integer: a pointer
	// field reflects as nullable, and null is not a wire form here.
	var awaitSchema struct {
		Type                 string                    `json:"type"`
		Required             []string                  `json:"required"`
		AdditionalProperties any                       `json:"additionalProperties"`
		Properties           map[string]map[string]any `json:"properties"`
	}
	raw, _ := json.Marshal(byName["tangent.turn_await"].InputSchema)
	if err := json.Unmarshal(raw, &awaitSchema); err != nil {
		t.Fatalf("decode turn_await schema: %v", err)
	}
	wait := awaitSchema.Properties["wait_ms"]
	if wait["type"] != "integer" || wait["minimum"] != float64(0) || wait["maximum"] != float64(50000) {
		t.Errorf("wait_ms schema = %v, want integer in [0, 50000]", wait)
	}
	if len(awaitSchema.Required) != 1 || awaitSchema.Required[0] != "session_id" {
		t.Errorf("turn_await required = %v, want [session_id] (wait_ms is optional)", awaitSchema.Required)
	}
	if awaitSchema.AdditionalProperties == nil || awaitSchema.AdditionalProperties == true {
		t.Errorf("turn_await accepts unknown fields: additionalProperties = %v", awaitSchema.AdditionalProperties)
	}
}

func TestTurnsToolsCarryAnAgentThroughAnswerAndAcknowledgement(t *testing.T) {
	server, operator := newTurnsRig(t)
	client, closeClient := connectInteractionClient(t, server)
	defer closeClient()
	ctx := context.Background()
	request := turnRequest("sess-loop", "t1")

	handle := callInteractionTool[turns.TurnHandle](t, client, "tangent.turns_enqueue", request)
	if handle.ItemID == "" || handle.SessionID != "sess-loop" || handle.TurnID != "t1" ||
		handle.QueueSequence != 1 || handle.State != interaction.InteractionStatePresented {
		t.Fatalf("turns_enqueue = %#v", handle)
	}
	retry := callInteractionTool[turns.TurnHandle](t, client, "tangent.turns_enqueue", request)
	if retry.ItemID != handle.ItemID || retry.QueueSequence != handle.QueueSequence {
		t.Fatalf("repeating the idempotency_key made a second item: %#v vs %#v", retry, handle)
	}

	// Nobody has answered yet: a look-once await reports a timeout and nothing else.
	none := callInteractionTool[turns.AwaitResult](t, client, "tangent.turn_await",
		map[string]any{"session_id": "sess-loop", "wait_ms": 0})
	if none.WaitStatus != turns.AwaitStatusTimeout || none.Replies == nil || len(none.Replies) != 0 {
		t.Fatalf("await before any answer = %#v", none)
	}

	// The operator answers through the browser API, which has no tool.
	if _, err := operator.Reply(ctx, turns.ReplyInput{
		ItemID: handle.ItemID, ExpectedRevision: handle.Revision,
		Action: "respond", ResponseText: "main", SelectedOption: "main",
	}); err != nil {
		t.Fatalf("operator Reply: %v", err)
	}

	got := callInteractionTool[turns.AwaitResult](t, client, "tangent.turn_await",
		map[string]any{"session_id": "sess-loop", "wait_ms": 5000})
	if got.WaitStatus != turns.AwaitStatusReplies || len(got.Replies) != 1 {
		t.Fatalf("await after answer = %#v", got)
	}
	reply := got.Replies[0]
	if reply.ItemID != handle.ItemID || reply.Resolution == nil ||
		reply.Resolution.ResponseText != "main" || reply.Resolution.SelectedOption != "main" {
		t.Fatalf("reply = %#v", reply)
	}

	// Unacknowledged replies come back: a crash between receive and ack loses nothing.
	again := callInteractionTool[turns.AwaitResult](t, client, "tangent.turn_await",
		map[string]any{"session_id": "sess-loop", "wait_ms": 0})
	if len(again.Replies) != 1 {
		t.Fatalf("an unacknowledged reply was not offered again: %#v", again)
	}

	ack := map[string]any{"item_id": handle.ItemID, "reply_id": reply.Resolution.ResolutionID}
	acked := callInteractionTool[map[string]any](t, client, "tangent.turn_ack", ack)
	if acked["status"] != "acknowledged" || acked["item_id"] != handle.ItemID {
		t.Fatalf("turn_ack = %#v", acked)
	}
	callInteractionTool[map[string]any](t, client, "tangent.turn_ack", ack) // idempotent

	done := callInteractionTool[turns.AwaitResult](t, client, "tangent.turn_await",
		map[string]any{"session_id": "sess-loop", "wait_ms": 0})
	if done.WaitStatus != turns.AwaitStatusTimeout || len(done.Replies) != 0 {
		t.Fatalf("an acknowledged reply was returned again: %#v", done)
	}
	view, err := operator.InspectTurn(ctx, handle.ItemID)
	if err != nil {
		t.Fatalf("InspectTurn: %v", err)
	}
	if view.DeliveryState != interaction.DeliveryStateAcknowledged {
		t.Errorf("delivery_state = %q, want acknowledged", view.DeliveryState)
	}
}

func TestTurnAwaitWakesWhenTheOperatorAnswersMidWait(t *testing.T) {
	server, operator := newTurnsRig(t)
	client, closeClient := connectInteractionClient(t, server)
	defer closeClient()
	handle := callInteractionTool[turns.TurnHandle](t, client, "tangent.turns_enqueue", turnRequest("sess-wake", "t1"))

	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = operator.Reply(context.Background(), turns.ReplyInput{
			ItemID: handle.ItemID, ExpectedRevision: handle.Revision, Action: "approve", ResponseText: "go",
		})
	}()

	start := time.Now()
	got := callInteractionTool[turns.AwaitResult](t, client, "tangent.turn_await",
		map[string]any{"session_id": "sess-wake", "wait_ms": 10000})
	if got.WaitStatus != turns.AwaitStatusReplies || len(got.Replies) != 1 {
		t.Fatalf("await = %#v, want the reply that arrived while it waited", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("await held the call %v after the reply landed", elapsed)
	}
}

func TestTurnsToolsRefuseWithStableCodes(t *testing.T) {
	server, operator := newTurnsRig(t)
	client, closeClient := connectInteractionClient(t, server)
	defer closeClient()
	handle := callInteractionTool[turns.TurnHandle](t, client, "tangent.turns_enqueue", turnRequest("sess-err", "t1"))
	answeredItem := callInteractionTool[turns.TurnHandle](t, client, "tangent.turns_enqueue", turnRequest("sess-err", "t2"))
	if _, err := operator.Reply(context.Background(), turns.ReplyInput{
		ItemID: answeredItem.ItemID, ExpectedRevision: answeredItem.Revision, Action: "respond", ResponseText: "ok",
	}); err != nil {
		t.Fatalf("operator Reply: %v", err)
	}

	// Each case reaches the service, so the refusal carries the turns error body.
	for name, tc := range map[string]struct {
		tool string
		args map[string]any
		code string
	}{
		"await with an empty session":   {"tangent.turn_await", map[string]any{"session_id": ""}, "validation_failed"},
		"ack of an unanswered turn":     {"tangent.turn_ack", map[string]any{"item_id": handle.ItemID}, "validation_failed"},
		"ack naming the wrong reply":    {"tangent.turn_ack", map[string]any{"item_id": answeredItem.ItemID, "reply_id": "nope"}, "validation_failed"},
		"ack of an item that is absent": {"tangent.turn_ack", map[string]any{"item_id": "does-not-exist"}, "not_found"},
	} {
		result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: tc.tool, Arguments: tc.args})
		if err != nil {
			t.Fatalf("%s: transport error %v", name, err)
		}
		if !result.IsError {
			t.Errorf("%s: want a tool error, got %s", name, extractText(t, result))
			continue
		}
		var body struct {
			Code            string `json:"code"`
			ContractVersion string `json:"contract_version"`
		}
		if err := json.Unmarshal([]byte(extractText(t, result)), &body); err != nil {
			t.Errorf("%s: error body is not JSON: %v (%s)", name, err, extractText(t, result))
			continue
		}
		if body.Code != tc.code || body.ContractVersion != turns.ContractVersion {
			t.Errorf("%s: error body = %+v, want code %q", name, body, tc.code)
		}
	}

	// The schema refuses what the service would, before the handler runs.
	for name, args := range map[string]map[string]any{
		"wait past the ceiling": {"session_id": "s", "wait_ms": 50001},
		"negative wait":         {"session_id": "s", "wait_ms": -1},
		"unknown field":         {"session_id": "s", "surprise": true},
	} {
		result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "tangent.turn_await", Arguments: args})
		if err == nil && (result == nil || !result.IsError) {
			t.Errorf("turn_await accepted %s", name)
		}
	}

	// A malformed turn is refused and creates nothing.
	bad := turnRequest("sess-err", "t3")
	delete(bad, "title")
	result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "tangent.turns_enqueue", Arguments: bad})
	if err == nil && (result == nil || !result.IsError) {
		t.Errorf("turns_enqueue accepted a turn with no title")
	}
	inbox, err := operator.Inbox(context.Background())
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if got := inbox.TotalPending + inbox.TotalTerminal; got != 2 {
		t.Errorf("inbox holds %d turns, want the 2 valid ones", got)
	}
}

// runnerRig is a runner engine wired to the real turns tools over a loopback
// MCP session, with the operator's side of the inbox and an agent client beside
// it. The agent is cat: it echoes whatever the runner writes to its stdin, so an
// answer the runner delivers comes straight back out as the agent's next turn,
// which is what lets a test see that delivery happened.
type runnerRig struct {
	ctx      context.Context
	engine   *runner.Engine
	operator *turns.Service
	client   *mcpsdk.ClientSession
}

func newRunnerRig(t *testing.T, sessionID, prompt string) *runnerRig {
	t.Helper()
	server, operator := newTurnsRig(t)
	client, closeClient := connectInteractionClient(t, server)
	t.Cleanup(closeClient)
	ctx := context.Background()

	caller, err := server.NewLoopbackCaller(ctx)
	if err != nil {
		t.Fatalf("NewLoopbackCaller: %v", err)
	}
	t.Cleanup(func() { _ = caller.Close() })

	engine := runner.NewEngine(nil)
	engine.SetToolCaller(caller)
	engine.SetReplyPollInterval(10 * time.Millisecond)
	launched, err := engine.Launch(ctx, runner.LaunchParams{
		SessionID: sessionID, AgentID: "agent-claude", AgentLabel: "Claude",
		Prompt: prompt, Command: "cat", Lifecycle: runner.LifecycleStreamingStdio,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	t.Cleanup(func() {
		_ = engine.Stop(launched.SessionID)
		waitFor(t, 5*time.Second, "the runner's session to stop", func() bool {
			health, healthErr := engine.Health(ctx, launched.SessionID)
			return healthErr == nil && !health.Alive
		})
	})
	return &runnerRig{ctx: ctx, engine: engine, operator: operator, client: client}
}

// nextPresented waits for a turn from sessionID other than any in seen to reach
// the inbox and be presented, and returns it. An item is listed as pending while
// it is still being staged; the operator acts on it once it is presented.
func (r *runnerRig) nextPresented(t *testing.T, sessionID string, seen ...string) turns.TurnItemView {
	t.Helper()
	var found turns.TurnItemView
	waitFor(t, 10*time.Second, "a turn from "+sessionID+" to reach the inbox", func() bool {
		inbox, err := r.operator.Inbox(r.ctx)
		if err != nil {
			return false
		}
	candidates:
		for _, item := range inbox.Pending {
			if item.SessionID != sessionID || item.State != interaction.InteractionStatePresented {
				continue
			}
			for _, id := range seen {
				if item.ItemID == id {
					continue candidates
				}
			}
			found = item
			return true
		}
		return false
	})
	return found
}

// TestRunnerTurnsReachTheInboxThroughTheRealTool exists because the runner's own
// tests use a caller that accepts any tool name: they passed for as long as the
// tool they call did not exist. This one puts the runner in front of the real
// surface, so a renamed tool, a changed argument shape or a payload the schema
// refuses fails here.
func TestRunnerTurnsReachTheInboxThroughTheRealTool(t *testing.T) {
	rig := newRunnerRig(t, "sess-runner", "Please confirm you want to proceed with the deployment?")
	item := rig.nextPresented(t, "sess-runner")

	if item.SessionID != "sess-runner" || item.AgentID != "agent-claude" || item.ApplicationID != "runner" {
		t.Errorf("runner turn identity = session %q agent %q application %q", item.SessionID, item.AgentID, item.ApplicationID)
	}
	if item.Kind != "approval" || !strings.Contains(item.Content, "confirm you want to proceed") {
		t.Errorf("runner turn = kind %q content %q", item.Kind, item.Content)
	}
}

// TestRunnerCarriesTheOperatorsAnswerBackToTheAgent is the whole loop over the
// real tools: a turn goes out, the operator answers it in the inbox, the runner
// finds the answer through tangent.turn_await, writes it to the agent, and
// acknowledges it through tangent.turn_ack.
func TestRunnerCarriesTheOperatorsAnswerBackToTheAgent(t *testing.T) {
	const session = "sess-loop-runner"
	rig := newRunnerRig(t, session, "Please confirm you want to proceed with the deployment?")
	first := rig.nextPresented(t, session)

	if _, err := rig.operator.Reply(rig.ctx, turns.ReplyInput{
		ItemID: first.ItemID, ExpectedRevision: first.Revision, Action: "approve", ResponseText: "proceed",
	}); err != nil {
		t.Fatalf("operator Reply: %v", err)
	}

	// The answer reached the agent if the agent's next turn is the answer: cat
	// echoes it, the sieve reads it as prose, and the runner enqueues it.
	echoed := rig.nextPresented(t, session, first.ItemID)
	if echoed.Content != "proceed" {
		t.Errorf("the agent received %q, want the operator's answer %q", echoed.Content, "proceed")
	}
	if echoed.TurnID == first.TurnID {
		t.Errorf("the agent's reply reused turn_id %q: the answer did not advance the turn", first.TurnID)
	}

	// And the runner acknowledged it, so the operator's own view of the turn
	// shows the answer delivered rather than sitting queued.
	waitFor(t, 10*time.Second, "the runner to acknowledge the delivered answer", func() bool {
		view, err := rig.operator.InspectTurn(rig.ctx, first.ItemID)
		return err == nil && view.DeliveryState == interaction.DeliveryStateAcknowledged
	})
	again := callInteractionTool[turns.AwaitResult](t, rig.client, "tangent.turn_await",
		map[string]any{"session_id": session, "wait_ms": 0})
	if len(again.Replies) != 0 {
		t.Errorf("an acknowledged answer is still being offered: %#v", again)
	}
}

func waitFor(t *testing.T, limit time.Duration, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s", limit, what)
}
