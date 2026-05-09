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

	sessionCloseSchema, err := buildSchema(sessionCloseInputSchemaJSON, "session_close")
	if err != nil {
		return fmt.Errorf("build session_close input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.session_close",
		Description: "Close a Tangent room explicitly and remove its live UI tab.",
		InputSchema: sessionCloseSchema,
	}, s.handleSessionClose)

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

func buildSchema(raw []byte, name string) (*jsonschema.Schema, error) {
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("unmarshal %s schema: %w", name, err)
	}
	return &s, nil
}
