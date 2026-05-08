package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/envelope"
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
// registration is global.
//
// triageRoomURL is the prefix used to log "open this URL" hints when
// tangent.triage creates a Room. PR 4 auto-creates a room per call;
// future PRs may add tray-icon or auto-launch flows so the user never
// needs to copy the URL by hand.
type Server struct {
	envSvc     *envelope.Service
	dispatcher *envelope.Dispatcher

	mcp *mcpsdk.Server

	// triageRoomURL is the base URL for room links emitted by the
	// triage handler. Optional; empty disables logging the hint.
	triageRoomURL string
}

// New constructs a Server, registers the v0.1 tool surface
// (tangent.list_workflows + tangent.triage), and returns it ready to
// expose via HTTPHandler / SSEHandler.
//
// Both envSvc and dispatcher are required. dispatcher is the same
// instance future PRs (PR 4 WS bridge, PR 5+ kinds) attach handlers to;
// passing it through here keeps the MCP layer agnostic to which
// transport ultimately fulfills the envelope.
func New(envSvc *envelope.Service, dispatcher *envelope.Dispatcher) (*Server, error) {
	if envSvc == nil {
		return nil, fmt.Errorf("mcp: envelope service is required")
	}
	if dispatcher == nil {
		return nil, fmt.Errorf("mcp: dispatcher is required")
	}

	mcpServer := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    implementationName,
		Version: implementationVersion,
	}, nil)

	s := &Server{
		envSvc:     envSvc,
		dispatcher: dispatcher,
		mcp:        mcpServer,
	}

	if err := s.registerTools(); err != nil {
		return nil, fmt.Errorf("mcp: register tools: %w", err)
	}
	return s, nil
}

// MCP returns the underlying SDK server. Exposed for tests that need to
// connect via NewInMemoryTransports; not intended for production callers.
func (s *Server) MCP() *mcpsdk.Server { return s.mcp }

// SetTriageRoomURL injects the base URL that the triage handler logs
// when creating a room (e.g. "http://localhost:7842"). The handler
// appends "/r/<roomID>" before logging. Optional — empty value
// disables the hint.
func (s *Server) SetTriageRoomURL(url string) { s.triageRoomURL = url }

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

// registerTools wires the v0.1 tool surface. Called once during
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
		Description: "Dispatch a triage-kind envelope through Tangent. Validates the envelope and routes it to the registered handler; returns the handler's Response. PR 4 wires the real handler — PR 3 returns NOT_WIRED.",
		InputSchema: triageSchema,
	}, s.handleTriage)

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
	var s jsonschema.Schema
	if err := json.Unmarshal(triageInputSchemaJSON, &s); err != nil {
		return nil, fmt.Errorf("unmarshal triage schema: %w", err)
	}
	return &s, nil
}
