package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/room"
)

// implementationName / implementationVersion are advertised in the MCP
// initialize response. Keep them stable across minor versions; v0.1
// clients pin against this string.
const (
	implementationName    = "tangent"
	implementationVersion = "v0.1.0"
)

// Server wraps the SDK's *mcp.Server with Tangent's envelope service +
// dispatcher. One Server instance backs both the streamable-HTTP and the
// SSE transports — the same MCP server is reused per-request, so tool
// registration is global. The triage room URL prefix is plumbed through
// NewTriageHandler in cmd/tangent/main.go (see internal/mcp/triage_handler.go),
// not stored on the Server.
type Server struct {
	envSvc      *envelope.Service
	dispatcher  *envelope.Dispatcher
	manager     *room.Manager
	roomURLBase string

	mcp *mcpsdk.Server
}

// New constructs a Server, registers the Tangent MCP tool surface,
// and returns it ready to
// expose via HTTPHandler / SSEHandler.
//
// Both envSvc and dispatcher are required. dispatcher is the same
// instance future PRs (PR 4 WS bridge, PR 5+ kinds) attach handlers to;
// passing it through here keeps the MCP layer agnostic to which
// transport ultimately fulfills the envelope.
func New(envSvc *envelope.Service, dispatcher *envelope.Dispatcher, manager *room.Manager, roomURLBase string) (*Server, error) {
	if envSvc == nil {
		return nil, fmt.Errorf("mcp: envelope service is required")
	}
	if dispatcher == nil {
		return nil, fmt.Errorf("mcp: dispatcher is required")
	}
	if manager == nil {
		return nil, fmt.Errorf("mcp: room manager is required")
	}

	mcpServer := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    implementationName,
		Version: implementationVersion,
	}, nil)

	s := &Server{
		envSvc:      envSvc,
		dispatcher:  dispatcher,
		manager:     manager,
		roomURLBase: roomURLBase,
		mcp:         mcpServer,
	}

	if err := s.registerTools(); err != nil {
		return nil, fmt.Errorf("mcp: register tools: %w", err)
	}
	return s, nil
}

// MCP returns the underlying SDK server. Exposed for tests that need to
// connect via NewInMemoryTransports; not intended for production callers.
func (s *Server) MCP() *mcpsdk.Server { return s.mcp }

// HTTPHandler returns the streamable-HTTP handler suitable for mounting
// at /mcp. The SDK reuses the same *mcp.Server for every request via the
// getServer factory closure.
//
// We run in Stateless + JSONResponse mode for v0.1:
//   - Stateless lets clients invoke tools/list and tools/call without
//     completing the MCP initialize handshake first. The integration
//     workflow Tangent targets in v0.1 (and the curl smoke probe in
//     docs/mcp-smoketest.md) sends one POST and expects one JSON reply;
//     a session-bound mode would force every probe to do `initialize`
//     first, which adds friction without protecting anything we care
//     about (localhost-only, no auth in v0.1).
//   - JSONResponse forces application/json replies even when the client
//     advertises text/event-stream Accept. SSE-style streaming is used
//     by the dedicated /sse mount; mixing both on /mcp would surprise
//     callers that just want a single JSON-RPC round trip.
//
// PR 4+ may revisit the trade-off when the WebSocket bridge introduces
// long-running, stateful workflows. For PR 3 the surface is request/
// response only.
func (s *Server) HTTPHandler() http.Handler {
	return mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return s.mcp },
		&mcpsdk.StreamableHTTPOptions{
			Stateless:    true,
			JSONResponse: true,
		},
	)
}

// SSEHandler returns the SSE-fallback handler suitable for mounting at
// /sse. Older Claude Code clients (and other MCP clients that pre-date
// streamable-HTTP) connect here; the SDK manages the long-lived event
// stream.
func (s *Server) SSEHandler() http.Handler {
	return mcpsdk.NewSSEHandler(func(*http.Request) *mcpsdk.Server {
		return s.mcp
	}, nil)
}

// registerTools wires the Tangent MCP tool surface. Called once during
// construction; the SDK's tool registry is mutex-protected so future
// dynamic registration (PR 5+) is safe, but in v0.1 the surface is
// fixed.
//
// We use the typed AddTool generic so the SDK auto-unmarshals input
// JSON into our Go struct and validates against the InputSchema we
// supply (overriding the SDK's auto-inferred schema for triage so the
// hand-rolled v0.1 stub can constrain shape until go-envelopes core
// learns about `triage` in v0.3).
func (s *Server) registerTools() error {
	listSchema, err := buildEmptyObjectSchema()
	if err != nil {
		return fmt.Errorf("build list_workflows input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.list_workflows",
		Description: "List the envelope-typed workflows Tangent currently exposes. Returns each registered handler's type, response kind, description, and (v0.4+) capabilities.",
		InputSchema: listSchema,
	}, s.handleListWorkflows)

	triageSchema, err := buildTriageInputSchema()
	if err != nil {
		return fmt.Errorf("build triage input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.triage",
		Description: "Dispatch a triage-kind envelope through Tangent. Public contract is unchanged from v0.1; internally this creates or reuses a room and advances the session.",
		InputSchema: triageSchema,
	}, s.handleTriage)

	feedbackSchema, err := buildFeedbackInputSchema()
	if err != nil {
		return fmt.Errorf("build feedback input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.feedback",
		Description: "Dispatch a feedback-kind envelope through Tangent. Creates or reuses a room and waits for a structured questionnaire response.",
		InputSchema: feedbackSchema,
	}, s.handleFeedback)

	designIterationSchema, err := buildDesignIterationInputSchema()
	if err != nil {
		return fmt.Errorf("build design-iteration input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.design-iteration",
		Description: "Dispatch a design-iteration envelope through Tangent. Renders sandboxed HTML and returns the user's selected action for each iteration.",
		InputSchema: designIterationSchema,
	}, s.handleDesignIteration)

	interviewQuestionSchema, err := buildInterviewQuestionInputSchema()
	if err != nil {
		return fmt.Errorf("build interview-question input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.interview_question",
		Description: "Dispatch an interview-question envelope through Tangent. Creates or reuses a room and waits for one long-form answer.",
		InputSchema: interviewQuestionSchema,
	}, s.handleInterviewQuestion)

	blockDraftSchema, err := buildBlockDraftInputSchema()
	if err != nil {
		return fmt.Errorf("build block-draft input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.block_draft",
		Description: "Dispatch a block-draft envelope through Tangent. Accepted responses append durable draft blocks on the room.",
		InputSchema: blockDraftSchema,
	}, s.handleBlockDraft)

	proseRevisionSchema, err := buildProseRevisionInputSchema()
	if err != nil {
		return fmt.Errorf("build prose-revision input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.prose_revision",
		Description: "Dispatch a prose-revision envelope through Tangent. Persists explicit accept/reject/comment outcomes per suggestion on the room.",
		InputSchema: proseRevisionSchema,
	}, s.handleProseRevision)

	outputRenderSchema, err := buildOutputRenderInputSchema()
	if err != nil {
		return fmt.Errorf("build output-render input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.output_render",
		Description: "Dispatch an output-render envelope through Tangent. Persists the room's final markdown artifact and renders it with copy/export affordances.",
		InputSchema: outputRenderSchema,
	}, s.handleOutputRender)

	whiteboardSchema, err := buildWhiteboardInputSchema()
	if err != nil {
		return fmt.Errorf("build whiteboard input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.whiteboard",
		Description: "Dispatch a whiteboard envelope through Tangent. Persists the room's current board snapshot and renders a tldraw host with explicit submit/cancel.",
		InputSchema: whiteboardSchema,
	}, s.handleWhiteboard)

	spreadsheetReviewSchema, err := buildSpreadsheetReviewInputSchema()
	if err != nil {
		return fmt.Errorf("build spreadsheet-review input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.spreadsheet-review",
		Description: "Dispatch a spreadsheet-review envelope through Tangent. Persists canonical table state and renders a room-backed table host with explicit submit/cancel.",
		InputSchema: spreadsheetReviewSchema,
	}, s.handleSpreadsheetReview)

	synthesisNotesSchema, err := buildSynthesisNotesInputSchema()
	if err != nil {
		return fmt.Errorf("build synthesis-notes input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.synthesis_notes",
		Description: "Dispatch a synthesis-notes envelope through Tangent. Persists private notes on the room and only exposes the phase-gated preview to the user.",
		InputSchema: synthesisNotesSchema,
	}, s.handleSynthesisNotes)

	sessionCreateSchema, err := buildSchema(sessionCreateInputSchemaJSON, "session_create")
	if err != nil {
		return fmt.Errorf("build session_create input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.session_create",
		Description: "Create a Tangent room and return its room ID plus SPA URL.",
		InputSchema: sessionCreateSchema,
	}, s.handleSessionCreate)

	sessionAdvanceSchema, err := buildSchema(sessionAdvanceInputSchemaJSON, "session_advance")
	if err != nil {
		return fmt.Errorf("build session_advance input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.session_advance",
		Description: "Push an envelope onto an existing Tangent room and wait for the user to resolve it.",
		InputSchema: sessionAdvanceSchema,
	}, s.handleSessionAdvance)

	sessionGetSchema, err := buildSchema(sessionGetInputSchemaJSON, "session_get")
	if err != nil {
		return fmt.Errorf("build session_get input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.session_get",
		Description: "Read the current room state and persisted envelope history for a Tangent room.",
		InputSchema: sessionGetSchema,
	}, s.handleSessionGet)

	sessionAdvancePhaseSchema, err := buildSchema(sessionAdvancePhaseInputSchemaJSON, "session_advance_phase")
	if err != nil {
		return fmt.Errorf("build session_advance_phase input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.session_advance_phase",
		Description: "Set the current workflow phase for a Tangent room and append it to the room's visited phase history.",
		InputSchema: sessionAdvancePhaseSchema,
	}, s.handleSessionAdvancePhase)

	sessionSetPhaseOutputSchema, err := buildSchema(sessionSetPhaseOutputInputSchemaJSON, "session_set_phase_output")
	if err != nil {
		return fmt.Errorf("build session_set_phase_output input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.session_set_phase_output",
		Description: "Write a top-level key into a room's versioned JSON output blob for a workflow phase.",
		InputSchema: sessionSetPhaseOutputSchema,
	}, s.handleSessionSetPhaseOutput)

	sessionCloseSchema, err := buildSchema(sessionCloseInputSchemaJSON, "session_close")
	if err != nil {
		return fmt.Errorf("build session_close input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.session_close",
		Description: "Close a Tangent room explicitly and remove its live UI tab.",
		InputSchema: sessionCloseSchema,
	}, s.handleSessionClose)

	sessionListSchema, err := buildSchema(sessionListInputSchemaJSON, "session_list")
	if err != nil {
		return fmt.Errorf("build session_list input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.session_list",
		Description: "List Tangent rooms with title, timestamps, and the current pending envelope type when present.",
		InputSchema: sessionListSchema,
	}, s.handleSessionList)

	return nil
}

// buildEmptyObjectSchema returns a permissive empty-object input schema —
// MCP requires every tool's inputSchema to be of type "object", so we
// hand the SDK the minimal valid schema.
func buildEmptyObjectSchema() (*jsonschema.Schema, error) {
	raw := []byte(`{"type":"object","properties":{}}`)
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// buildTriageInputSchema parses the hand-rolled triage input schema (see
// triage_schema.go for the rationale).
func buildTriageInputSchema() (*jsonschema.Schema, error) {
	return buildSchema(triageInputSchemaJSON, "triage")
}

func buildFeedbackInputSchema() (*jsonschema.Schema, error) {
	return buildSchema(feedbackInputSchemaJSON, "feedback")
}

func buildDesignIterationInputSchema() (*jsonschema.Schema, error) {
	return buildSchema(designIterationInputSchemaJSON, "design-iteration")
}

func buildInterviewQuestionInputSchema() (*jsonschema.Schema, error) {
	return buildSchema(interviewQuestionInputSchemaJSON, "interview_question")
}

func buildBlockDraftInputSchema() (*jsonschema.Schema, error) {
	return buildSchema(blockDraftInputSchemaJSON, "block_draft")
}

func buildProseRevisionInputSchema() (*jsonschema.Schema, error) {
	return buildSchema(proseRevisionInputSchemaJSON, "prose_revision")
}

func buildOutputRenderInputSchema() (*jsonschema.Schema, error) {
	return buildSchema(outputRenderInputSchemaJSON, "output_render")
}

func buildWhiteboardInputSchema() (*jsonschema.Schema, error) {
	return buildSchema(whiteboardInputSchemaJSON, "whiteboard")
}

func buildSpreadsheetReviewInputSchema() (*jsonschema.Schema, error) {
	return buildSchema(spreadsheetReviewInputSchemaJSON, "spreadsheet_review")
}

func buildSynthesisNotesInputSchema() (*jsonschema.Schema, error) {
	return buildSchema(synthesisNotesInputSchemaJSON, "synthesis_notes")
}

func buildSchema(raw []byte, name string) (*jsonschema.Schema, error) {
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("unmarshal %s schema: %w", name, err)
	}
	return &s, nil
}
