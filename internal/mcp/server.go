package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/health"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/interactionpkg"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/roomflow"
)

// implementationName / implementationVersion are advertised in the MCP
// initialize response. Keep them stable across minor versions; v0.1
// clients pin against this string.
const (
	implementationName = "tangent"
	// implementationVersion is the same string definitions declare
	// compatibility against in `compatible_host_versions`, so it is taken from
	// the package that owns the definition registry rather than declared twice.
	// TestHostVersionHasOneSource keeps the alias honest.
	implementationVersion = envelope.HostVersion
	// HostVersion is persisted in immutable definition bindings created by
	// the production registry adapter.
	HostVersion = implementationVersion
)

// Server wraps the SDK's *mcp.Server with Tangent's envelope service +
// dispatcher. One Server instance backs both the streamable-HTTP and the
// SSE transports — the same MCP server is reused per-request, so tool
// registration is global. The triage room URL prefix is plumbed through
// NewTriageHandler in cmd/tangent/main.go (see internal/mcp/triage_handler.go),
// not stored on the Server.
type Server struct {
	envSvc       *envelope.Service
	dispatcher   *envelope.Dispatcher
	manager      *room.Manager
	roomURLBase  string
	interactions *interaction.Service
	hitl         *hitl.Service
	roomflow     *roomflow.Service

	// packages resolves a wire name to the publisher-owned interaction
	// package that serves it. It is how ADR 0003 §5's ownership split reaches
	// the request path: core looks a kind up here and never learns what the
	// kind means. Nil is a valid state — a build with no packages installed
	// runs every kind on the generic path, which is what the sixteen kinds
	// this task did not migrate still do.
	packages *interactionpkg.Registry

	// health answers liveness, readiness, and per-capability health. Nil is a
	// valid state — a transport-level test constructs an MCP server with no
	// durable substrate — and the tool says so rather than inventing a report.
	health *health.Reporter

	roomflowOptions []roomflow.Option

	mcp *mcpsdk.Server
}

// Option customizes optional MCP application-service dependencies.
type Option func(*Server) error

// WithInteractionService enables the generic durable asynchronous operation
// tools. Omitting it preserves the legacy MCP surface for compatibility tests
// and embedders that have not installed the durable interaction substrate.
func WithInteractionService(service *interaction.Service) Option {
	return func(server *Server) error {
		if service == nil {
			return fmt.Errorf("mcp: interaction service is nil")
		}
		server.interactions = service
		return nil
	}
}

// WithHealthReporter enables tangent.health_report. Omitting it leaves the
// tool registered and answering `health_unavailable`, which is the honest
// answer for a build that cannot measure its own readiness.
func WithHealthReporter(reporter *health.Reporter) Option {
	return func(server *Server) error {
		if reporter == nil {
			return fmt.Errorf("mcp: health reporter is nil")
		}
		server.health = reporter
		return nil
	}
}

// WithCompatibilityWindow overrides how long a wait-mode room workflow blocks
// before returning a durable pending receipt. Production uses the package
// default; tests compress it so the pending path is exercised without a
// wall-clock wait.
func WithCompatibilityWindow(window time.Duration) Option {
	return func(server *Server) error {
		server.roomflowOptions = append(server.roomflowOptions, roomflow.WithCompatibilityWindow(window))
		return nil
	}
}

// WithHITLService enables the stable durable HITL inbox operation surface.
// It is separate from the generic interaction tools and from the legacy
// tangent.approval-queue batch workflow.
func WithHITLService(service *hitl.Service) Option {
	return func(server *Server) error {
		if service == nil {
			return fmt.Errorf("mcp: hitl service is nil")
		}
		server.hitl = service
		return nil
	}
}

// WithInteractionPackages installs the publisher-owned interaction packages
// this build hosts. Omitting it is not an error: an unpackaged kind takes the
// generic path, and a packaged kind whose package is absent fails closed with
// `unsupported-type` rather than silently falling back to a core default that
// knows the kind (ADR 0003 §8 C7).
func WithInteractionPackages(registry *interactionpkg.Registry) Option {
	return func(server *Server) error {
		if registry == nil {
			return fmt.Errorf("mcp: interaction package registry is nil")
		}
		server.packages = registry
		return nil
	}
}

// New constructs a Server, registers the Tangent MCP tool surface, and returns
// it ready to expose via HTTPHandler / SSEHandler.
//
// Both envSvc and dispatcher are required. dispatcher is the same instance
// future PRs attach handlers to; passing it through here keeps the MCP layer
// agnostic to which transport ultimately fulfills the envelope.
func New(
	envSvc *envelope.Service,
	dispatcher *envelope.Dispatcher,
	manager *room.Manager,
	roomURLBase string,
	options ...Option,
) (*Server, error) {
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
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(s); err != nil {
			return nil, err
		}
	}
	if s.interactions != nil && s.roomflow == nil {
		// Every named room workflow routes through the durable compatibility
		// adapter whenever the substrate is present, so installing the
		// interaction service is the only switch: there is no per-workflow
		// opt-in that a new workflow could forget to set.
		flow, flowErr := roomflow.New(s.interactions, manager, s, roomURLBase, s.roomflowOptions...)
		if flowErr != nil {
			return nil, flowErr
		}
		s.roomflow = flow
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

	formCollectSchema, err := buildRoomWorkflowSchema(formCollectInputSchemaJSON, "form_collect")
	if err != nil {
		return fmt.Errorf("build form-collect input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.form-collect",
		Description: "Dispatch a generalized schema-driven form through Tangent. Persists canonical form state on the room and waits for explicit submit/cancel.",
		InputSchema: formCollectSchema,
	}, s.packagedWorkflowHandler(formCollectEnvelopeType))

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

	dashboardSchema, err := buildRoomWorkflowSchema(dashboardInputSchemaJSON, "dashboard")
	if err != nil {
		return fmt.Errorf("build dashboard input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.dashboard",
		Description: "Dispatch a dashboard envelope through Tangent. Persists room-backed tiles, layouts, query state, and explicit refresh/update affordances.",
		InputSchema: dashboardSchema,
	}, s.handleDashboard)

	filePickerSchema, err := buildRoomWorkflowSchema(filePickerInputSchemaJSON, "file_picker")
	if err != nil {
		return fmt.Errorf("build file-picker input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.file-picker",
		Description: "Dispatch a file-picker envelope through Tangent. Persists allowed roots, selected artifact refs, and canonical picker query state with explicit submit.",
		InputSchema: filePickerSchema,
	}, s.handleFilePicker)

	progressPanelSchema, err := buildRoomWorkflowSchema(progressPanelInputSchemaJSON, "progress_panel")
	if err != nil {
		return fmt.Errorf("build progress-panel input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.progress-panel",
		Description: "Dispatch a progress-panel envelope through Tangent. Persists canonical progress items, summary state, and explicit operator updates.",
		InputSchema: progressPanelSchema,
	}, s.handleProgressPanel)

	wizardSchema, err := buildRoomWorkflowSchema(wizardInputSchemaJSON, "wizard")
	if err != nil {
		return fmt.Errorf("build wizard input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.wizard",
		Description: "Dispatch a guided wizard envelope through Tangent. Persists room-backed step definitions, progress, branch selections, and explicit partial/final completion state.",
		InputSchema: wizardSchema,
	}, s.handleWizard)

	diffReviewSchema, err := buildRoomWorkflowSchema(diffReviewInputSchemaJSON, "diff_review")
	if err != nil {
		return fmt.Errorf("build diff-review input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.diff-review",
		Description: "Dispatch a diff-review envelope through Tangent. Persists file and hunk decisions, filter state, comments, artifact refs, and exportable review summaries.",
		InputSchema: diffReviewSchema,
	}, s.handleDiffReview)

	spreadsheetReviewSchema, err := buildSpreadsheetReviewInputSchema()
	if err != nil {
		return fmt.Errorf("build spreadsheet-review input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.spreadsheet-review",
		Description: "Dispatch a spreadsheet-review envelope through Tangent. Persists canonical table state and renders a room-backed table host with explicit submit/cancel.",
		InputSchema: spreadsheetReviewSchema,
	}, s.handleSpreadsheetReview)

	approvalQueueSchema, err := buildRoomWorkflowSchema(approvalQueueInputSchemaJSON, "approval_queue")
	if err != nil {
		return fmt.Errorf("build approval-queue input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.approval-queue",
		Description: "Dispatch an approval-queue envelope through Tangent. Persists queue decisions, evidence context, and audit export metadata with an explicit submit boundary.",
		InputSchema: approvalQueueSchema,
	}, s.handleApprovalQueue)

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
		Description: "Create a Tangent room and return its room ID plus SPA URL. The room is owned by your caller scope. The URL is a locator, not a credential: opening it in a browser is what grants access, so sharing or logging it transfers nothing.",
		InputSchema: sessionCreateSchema,
	}, s.handleSessionCreate)

	sessionAdvanceSchema, err := buildRoomWorkflowSchema(sessionAdvanceInputSchemaJSON, "session_advance")
	if err != nil {
		return fmt.Errorf("build session_advance input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.session_advance",
		Description: "Push an envelope onto an existing Tangent room and wait for the user to resolve it. Only rooms in your own caller partition accept an advance.",
		InputSchema: sessionAdvanceSchema,
	}, s.handleSessionAdvance)

	sessionGetSchema, err := buildSchema(sessionGetInputSchemaJSON, "session_get")
	if err != nil {
		return fmt.Errorf("build session_get input schema: %w", err)
	}
	sessionGetOutput, err := s.sessionGetOutputSchema()
	if err != nil {
		return fmt.Errorf("build session_get output schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:         "tangent.session_get",
		Description:  "Read the current room state and persisted envelope history for a Tangent room. Reads span every local caller partition, not just your own; standalone-local partitions are advisory and are not a security boundary.",
		InputSchema:  sessionGetSchema,
		OutputSchema: sessionGetOutput,
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
		Description: "Close a Tangent room explicitly and remove its live UI tab. Only rooms in your own caller partition can be closed, because closing dispositions another caller's outstanding human work.",
		InputSchema: sessionCloseSchema,
	}, s.handleSessionClose)

	sessionListSchema, err := buildSchema(sessionListInputSchemaJSON, "session_list")
	if err != nil {
		return fmt.Errorf("build session_list input schema: %w", err)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        "tangent.session_list",
		Description: "List Tangent rooms with title, timestamps, and the current pending envelope type when present. The listing spans every local caller partition, not just your own; standalone-local partitions are advisory and are not a security boundary.",
		InputSchema: sessionListSchema,
	}, s.handleSessionList)

	// Registry diagnostics do not depend on the durable interaction substrate:
	// the definition registry exists whenever an envelope service does, and an
	// embedder without the substrate still needs to be able to ask why a kind
	// is not being served.
	if err := s.registerDefinitionTools(); err != nil {
		return err
	}
	if err := s.registerHealthTool(); err != nil {
		return err
	}
	if s.interactions != nil {
		if err := s.registerInteractionTools(); err != nil {
			return fmt.Errorf("register interaction tools: %w", err)
		}
	}
	if s.hitl != nil {
		if err := s.registerHITLTools(); err != nil {
			return fmt.Errorf("register hitl tools: %w", err)
		}
	}

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
	return buildRoomWorkflowSchema(triageInputSchemaJSON, "triage")
}

func buildFeedbackInputSchema() (*jsonschema.Schema, error) {
	return buildRoomWorkflowSchema(feedbackInputSchemaJSON, "feedback")
}

func buildDesignIterationInputSchema() (*jsonschema.Schema, error) {
	return buildRoomWorkflowSchema(designIterationInputSchemaJSON, "design-iteration")
}

func buildInterviewQuestionInputSchema() (*jsonschema.Schema, error) {
	return buildRoomWorkflowSchema(interviewQuestionInputSchemaJSON, "interview_question")
}

func buildBlockDraftInputSchema() (*jsonschema.Schema, error) {
	return buildRoomWorkflowSchema(blockDraftInputSchemaJSON, "block_draft")
}

func buildProseRevisionInputSchema() (*jsonschema.Schema, error) {
	return buildRoomWorkflowSchema(proseRevisionInputSchemaJSON, "prose_revision")
}

func buildOutputRenderInputSchema() (*jsonschema.Schema, error) {
	return buildRoomWorkflowSchema(outputRenderInputSchemaJSON, "output_render")
}

func buildWhiteboardInputSchema() (*jsonschema.Schema, error) {
	return buildRoomWorkflowSchema(whiteboardInputSchemaJSON, "whiteboard")
}

func buildSpreadsheetReviewInputSchema() (*jsonschema.Schema, error) {
	return buildRoomWorkflowSchema(spreadsheetReviewInputSchemaJSON, "spreadsheet_review")
}

func buildSynthesisNotesInputSchema() (*jsonschema.Schema, error) {
	return buildRoomWorkflowSchema(synthesisNotesInputSchemaJSON, "synthesis_notes")
}

// buildRoomWorkflowSchema parses a room-backed tool's input schema and adds
// the shared completion selector. Every named room workflow and
// tangent.session_advance goes through it, so a workflow cannot ship without
// advertising the async mode its callers need.
func buildRoomWorkflowSchema(raw []byte, name string) (*jsonschema.Schema, error) {
	schema, err := buildSchema(raw, name)
	if err != nil {
		return nil, err
	}
	return withCompletionMode(schema, name)
}

func buildSchema(raw []byte, name string) (*jsonschema.Schema, error) {
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("unmarshal %s schema: %w", name, err)
	}
	return &s, nil
}
