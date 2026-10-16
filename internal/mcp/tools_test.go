package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	envelopes "github.com/hollis-labs/libs/ui/ui-go/envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
)

// connect spins up a Tangent MCP server and an in-memory MCP client
// session against it. Returns the connected client session plus a
// cleanup func that closes both sides.
//
// Using NewInMemoryTransports avoids running an HTTP listener per test
// — the SDK's own conformance tests use the same pattern. The behavior
// being verified (tool registration, validation, dispatcher routing) is
// transport-independent, so the in-memory transport gives us
// fastest-feedback coverage with no network flakiness.
func connect(t *testing.T) (*mcpsdk.ClientSession, *envelope.Dispatcher, func()) {
	t.Helper()
	client, _, dispatcher, cleanup := connectServer(t)
	return client, dispatcher, cleanup
}

// connectServer is connect that also returns the server, so a test can ask it
// for its registry, and accepts options for the server under test.
func connectServer(t *testing.T, options ...tangentmcp.Option) (*mcpsdk.ClientSession, *tangentmcp.Server, *envelope.Dispatcher, func()) {
	t.Helper()
	ctx := context.Background()

	envSvc, err := envelope.New(ctx)
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if regErr := extensions.RegisterTriage(envSvc); regErr != nil {
		t.Fatalf("RegisterTriage: %v", regErr)
	}
	if regErr := extensions.RegisterFeedback(envSvc); regErr != nil {
		t.Fatalf("RegisterFeedback: %v", regErr)
	}
	if regErr := extensions.RegisterDesignIteration(envSvc); regErr != nil {
		t.Fatalf("RegisterDesignIteration: %v", regErr)
	}
	if regErr := extensions.RegisterInterviewQuestion(envSvc); regErr != nil {
		t.Fatalf("RegisterInterviewQuestion: %v", regErr)
	}
	if regErr := extensions.RegisterBlockDraft(envSvc); regErr != nil {
		t.Fatalf("RegisterBlockDraft: %v", regErr)
	}
	if regErr := extensions.RegisterProseRevision(envSvc); regErr != nil {
		t.Fatalf("RegisterProseRevision: %v", regErr)
	}
	if regErr := extensions.RegisterOutputRender(envSvc); regErr != nil {
		t.Fatalf("RegisterOutputRender: %v", regErr)
	}
	if regErr := extensions.RegisterWhiteboard(envSvc); regErr != nil {
		t.Fatalf("RegisterWhiteboard: %v", regErr)
	}
	if regErr := extensions.RegisterDashboard(envSvc); regErr != nil {
		t.Fatalf("RegisterDashboard: %v", regErr)
	}
	if regErr := extensions.RegisterFilePicker(envSvc); regErr != nil {
		t.Fatalf("RegisterFilePicker: %v", regErr)
	}
	if regErr := extensions.RegisterProgressPanel(envSvc); regErr != nil {
		t.Fatalf("RegisterProgressPanel: %v", regErr)
	}
	if regErr := extensions.RegisterWizard(envSvc); regErr != nil {
		t.Fatalf("RegisterWizard: %v", regErr)
	}
	if regErr := extensions.RegisterDiffReview(envSvc); regErr != nil {
		t.Fatalf("RegisterDiffReview: %v", regErr)
	}
	if regErr := extensions.RegisterSpreadsheetReview(envSvc); regErr != nil {
		t.Fatalf("RegisterSpreadsheetReview: %v", regErr)
	}
	if regErr := extensions.RegisterApprovalQueue(envSvc); regErr != nil {
		t.Fatalf("RegisterApprovalQueue: %v", regErr)
	}
	if regErr := extensions.RegisterSynthesisNotes(envSvc); regErr != nil {
		t.Fatalf("RegisterSynthesisNotes: %v", regErr)
	}
	dispatcher := envelope.NewDispatcher(envSvc)
	manager := room.NewManager(nil)

	srv, err := tangentmcp.New(envSvc, dispatcher, manager, "", options...)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}

	serverT, clientT := mcpsdk.NewInMemoryTransports()
	serverSession, err := srv.MCP().Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tangent-test", Version: "v0.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatalf("client.Connect: %v", err)
	}

	cleanup := func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	}
	return clientSession, srv, dispatcher, cleanup
}

// TestServer_ListsBaseToolSurface asserts the tool surface a server booted
// without the durable interaction substrate exposes: the legacy and session
// tools callers integrate against, plus the definition-registry diagnostics,
// tangent_health_get, and tangent_telemetry_query, none of which depend on
// that substrate — an embedder without it still has to be able to ask why a
// kind is not being served, whether this host is ready to serve at all, and
// what happened to a request it already made. Treat this as a contract test:
// changing names or the count is a public-API change.
func TestServer_ListsBaseToolSurface(t *testing.T) {
	cs, _, done := connect(t)
	defer done()

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 38 {
		names := make([]string, 0, len(res.Tools))
		for _, tt := range res.Tools {
			names = append(names, tt.Name)
		}
		t.Fatalf("expected 38 tools, got %d (%v)", len(res.Tools), names)
	}

	// tangent.app-board is registered unconditionally like every other room
	// workflow. It spent a while looking like the exception — its kind went
	// through the ADR 0007 §4 plugin door until CW-20260911-0036 established
	// that it never should have — and the tool was never conditional on that
	// either way: the door decides whether a KIND is served, not whether a tool
	// exists.
	want := map[string]bool{
		"tangent_workflow_list":            false,
		"tangent_telemetry_query":          false,
		"tangent_triage":                   false,
		"tangent_feedback":                 false,
		"tangent_form_collect":             false,
		"tangent_design_iteration":         false,
		"tangent_interview_question":       false,
		"tangent_block_draft":              false,
		"tangent_prose_revision":           false,
		"tangent_output_render":            false,
		"tangent_whiteboard":               false,
		"tangent_dashboard":                false,
		"tangent_app_board":                false,
		"tangent_file_picker":              false,
		"tangent_progress_panel":           false,
		"tangent_wizard":                   false,
		"tangent_diff_review":              false,
		"tangent_spreadsheet_review":       false,
		"tangent_approval_queue":           false,
		"tangent_synthesis_notes":          false,
		"tangent_session_create":           false,
		"tangent_session_advance":          false,
		"tangent_session_get":              false,
		"tangent_session_phase_advance":    false,
		"tangent_session_phase_output_set": false,
		"tangent_session_close":            false,
		"tangent_session_list":             false,
		// Definition-registry diagnostics (CW-20260825-0065, ADR 0003 §4.4
		// and §8 C7): available whenever an envelope service is, independent
		// of the durable interaction substrate.
		"tangent_definition_list":            false,
		"tangent_definition_get":             false,
		"tangent_definition_diagnostics_get": false,
		// Operability probes (CW-20260825-0066): registered unconditionally so
		// a build without a health reporter answers `health_unavailable`
		// rather than silently lacking the tool.
		"tangent_health_get":      false,
		"tangent_view_get":        false,
		"tangent_ui_navigate":     false,
		"tangent_ui_modal_open":   false,
		"tangent_ui_modal_close":  false,
		"tangent_ui_drawer_open":  false,
		"tangent_ui_drawer_close": false,
		"tangent_ui_item_focus":   false,
	}
	for _, tt := range res.Tools {
		if _, ok := want[tt.Name]; !ok {
			t.Errorf("unexpected tool %q", tt.Name)
			continue
		}
		want[tt.Name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("expected tool %q to be listed", name)
		}
	}
}

// TestListWorkflows_EmptyWhenNoHandlers asserts that
// tangent_workflow_list returns the canonical {workflows: []} shape
// when no dispatcher handlers are registered. This is the v0.1 baseline:
// PR 3 has no handlers so the array MUST be empty, but the field MUST
// exist so downstream callers can key off len().
func TestListWorkflows_EmptyWhenNoHandlers(t *testing.T) {
	cs, _, done := connect(t)
	defer done()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent_workflow_list",
	})
	if err != nil {
		t.Fatalf("CallTool list_workflows: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_workflows returned IsError=true; content: %v", res.Content)
	}

	body := extractText(t, res)
	var parsed struct {
		Workflows []map[string]any `json:"workflows"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("unmarshal list_workflows result %q: %v", body, err)
	}
	if len(parsed.Workflows) != 0 {
		t.Errorf("expected empty workflows list, got %d entries", len(parsed.Workflows))
	}
}

// TestListWorkflows_IncludesRegisteredHandlers asserts the filter
// logic: when a handler exists for an envelope type, list_workflows
// surfaces that type.
//
// We register a handler for `info-card` (a real core type) so the test
// doesn't depend on PR 4-style triage wiring. The shape we're verifying
// is "what list_workflows does with the dispatcher state," not "what
// triage looks like."
func TestListWorkflows_IncludesRegisteredHandlers(t *testing.T) {
	cs, dispatcher, done := connect(t)
	defer done()

	stub := envelope.HandlerFunc(func(_ context.Context, e *envelopes.Envelope) (*envelopes.Response, error) {
		return &envelopes.Response{
			V:          envelopes.ProtocolVersion,
			EnvelopeID: e.ID,
			Kind:       envelopes.ResponseKindAck,
			Status:     envelopes.ResponseStatusSubmitted,
		}, nil
	})
	if err := dispatcher.Register("info-card", stub); err != nil {
		t.Fatalf("dispatcher.Register: %v", err)
	}

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent_workflow_list",
	})
	if err != nil {
		t.Fatalf("CallTool list_workflows: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_workflows returned IsError=true; content: %v", res.Content)
	}

	body := extractText(t, res)
	var parsed struct {
		Workflows []struct {
			Type         string   `json:"type"`
			ResponseKind string   `json:"response_kind"`
			Capabilities []string `json:"capabilities"`
		} `json:"workflows"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("unmarshal list_workflows result %q: %v", body, err)
	}

	if len(parsed.Workflows) != 1 {
		t.Fatalf("expected exactly 1 workflow entry, got %d", len(parsed.Workflows))
	}
	got := parsed.Workflows[0]
	if got.Type != "info-card" {
		t.Errorf("workflow.type = %q, want %q", got.Type, "info-card")
	}
	if got.ResponseKind == "" {
		t.Error("workflow.response_kind unexpectedly empty")
	}
	if got.Capabilities == nil {
		t.Error("workflow.capabilities should be [], not null")
	}
}

// TestTriage_UnknownRoomID asserts the v0.2 room-reuse contract:
// tangent.triage accepts Meta.roomID, but the room must already exist.
func TestTriage_UnknownRoomID(t *testing.T) {
	cs, _, done := connect(t)
	defer done()

	envelopeArg := map[string]any{
		"v":    envelopes.ProtocolVersion,
		"id":   "triage-missing-room-1",
		"type": "tangent.triage",
		"data": map[string]any{},
		"meta": map[string]any{"roomID": "does-not-exist"},
	}

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent_triage",
		Arguments: map[string]any{"envelope": envelopeArg},
	})
	if err != nil {
		t.Fatalf("CallTool triage: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true for unknown roomID, got false; content: %v", res.Content)
	}

	body := extractText(t, res)
	var parsed struct {
		Kind  string `json:"kind"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("unmarshal error body %q: %v", body, err)
	}
	if parsed.Kind != string(envelopes.ResponseKindError) {
		t.Errorf("error body kind = %q, want %q", parsed.Kind, envelopes.ResponseKindError)
	}
	if parsed.Error.Code != "ROOM_NOT_FOUND" {
		t.Errorf("error body code = %q, want ROOM_NOT_FOUND", parsed.Error.Code)
	}
}

// TestTriage_RejectsInvalidInput asserts the SDK input-schema gate: a
// triage call with the wrong envelope type fails before reaching the
// dispatcher. The schema we declared in triage_schema.go pins
// envelope.type to the literal "triage" via JSON Schema `const`, so
// this surfaces as an SDK-side validation error.
func TestTriage_RejectsInvalidInput(t *testing.T) {
	cs, _, done := connect(t)
	defer done()

	envelopeArg := map[string]any{
		"v":    envelopes.ProtocolVersion,
		"id":   "triage-bad-1",
		"type": "info-card", // wrong type — schema requires "triage"
		"data": map[string]any{},
	}

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent_triage",
		Arguments: map[string]any{"envelope": envelopeArg},
	})
	// The SDK either returns a transport-level error here OR a
	// CallToolResult with IsError=true; both are acceptable failure
	// signals. We accept either as long as the call did NOT silently
	// succeed.
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("expected error or IsError=true for malformed triage envelope, got success: %+v", res)
	}
}

// TestTriage_RejectsMissingRequiredField asserts the schema enforces
// the v=integer / id=string / type=triage required tuple.
func TestTriage_RejectsMissingRequiredField(t *testing.T) {
	cs, _, done := connect(t)
	defer done()

	envelopeArg := map[string]any{
		// missing "id" — required by the triage input schema.
		"v":    envelopes.ProtocolVersion,
		"type": "tangent.triage",
		"data": map[string]any{},
	}

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent_triage",
		Arguments: map[string]any{"envelope": envelopeArg},
	})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("expected error or IsError=true for envelope missing required id, got success: %+v", res)
	}
}

// extractText pulls the first text content block out of a tool result,
// failing the test if the structure is unexpected. v0.1 tools always
// return a single text-content block; multi-block returns are a v0.4+
// concept (capabilities) and would be a contract change.
func extractText(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatal("nil CallToolResult")
	}
	if len(res.Content) == 0 {
		t.Fatal("CallToolResult has no content blocks")
	}
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("expected first content block to be TextContent, got %T", res.Content[0])
	}
	return tc.Text
}
