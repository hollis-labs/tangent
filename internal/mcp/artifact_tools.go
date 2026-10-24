package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	gmcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"

	"github.com/hollis-labs/tangent/internal/artifacts"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
)

// WithArtifactsService enables the artifact tools — tangent_artifact_create,
// _update, _get, _message, _await and _withdraw — backing the artifact inbox
// (ADR 0015, CW-20261024-0061).
func WithArtifactsService(service *artifacts.Service) Option {
	return func(server *Server) error {
		if service == nil {
			return fmt.Errorf("mcp: artifacts service is nil")
		}
		server.artifacts = service
		return nil
	}
}

// artifactTypeHelp is the per-type content contract, in the one place an agent
// reads while building the call. The limits are the service's own constants.
var artifactTypeHelp = "What the content is. " +
	"html: one self-contained HTML document (runs sandboxed; https CDN scripts allowed, no network calls). " +
	"markdown: Markdown text. " +
	"image: PNG, JPEG, GIF or WebP in content_base64, or SVG text in content. " +
	"excalidraw: JSON {\"elements\": [...]} (an Excalidraw scene). " +
	"flow: JSON {\"direction\": \"LR\"|\"TB\", \"nodes\": [{\"id\",\"label\",\"kind\"?: trigger|action|condition|output|note}], \"edges\": [{\"source\",\"target\",\"label\"?}]}, at most 500 nodes and 2000 edges. " +
	"mermaid: Mermaid source. d2: D2 source (drawn on the server; no imports). " +
	"vega-lite: a Vega-Lite spec with inline data (a url is refused). " +
	"pdf: bytes in content_base64. video: mp4 or webm bytes in content_base64. figma: a figma.com URL. " +
	fmt.Sprintf("Limits: text %d MiB, binary %d MiB.", artifacts.MaxTextBytes>>20, artifacts.MaxBinaryBytes>>20)

const artifactContentHelp = "The artifact's content. Send exactly one of content (text: html, markdown, mermaid, d2, " +
	"or JSON for excalidraw, flow and vega-lite; a figma.com URL for figma) and content_base64 (binary: " +
	"image, pdf, video). Through the Tether gateway, a trailing closing tag can be stripped (CW-20261024-0130): " +
	"end html and svg with a comment after the root close, e.g. </svg><!-- end -->."

// artifactSourceSchema is the shared shape of the `source` argument every
// follow-up tool takes. Access is partitioned by application_id (or by the
// verified gateway identity when the call arrives through one); agent_id is
// attribution, never authority (ADR 0004 §1). A follow-up must therefore name
// the application_id the artifact was created under.
func artifactSourceSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "object",
		Description: "Who is calling. Use the same application_id you created the artifact under: artifacts are private to that application (or to the verified gateway identity), and agent_id is only recorded as the author.",
		Required:    []string{"agent_id"},
		Properties: map[string]*jsonschema.Schema{
			"agent_id":       {Type: "string", MinLength: jsonschema.Ptr(1), MaxLength: jsonschema.Ptr(256)},
			"application_id": {Type: "string", MinLength: jsonschema.Ptr(1), MaxLength: jsonschema.Ptr(128)},
			// Accepted so an agent can pass the same source object it created with.
			"agent_label": {Type: "string", MaxLength: jsonschema.Ptr(256)},
		},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
}

func contentProperties() map[string]*jsonschema.Schema {
	return map[string]*jsonschema.Schema{
		"content":        {Type: "string", Description: artifactContentHelp},
		"content_base64": {Type: "string", Description: "Base64 of the binary content (image, pdf, video)."},
		"filename":       {Type: "string", MaxLength: jsonschema.Ptr(120), Description: "Optional label for the download."},
		"media_type":     {Type: "string", Description: "Optional. Must match what the content is; it is detected from the bytes."},
		"note":           {Type: "string", MaxLength: jsonschema.Ptr(artifacts.MaxMessageBody), Description: "Optional short note on what changed, shown beside the revision."},
	}
}

// artifactOutputSchema derives a tool's advertised result shape from the
// service type it returns, so an agent can read the handle, view and message
// fields from the schema instead of guessing them from a first call.
func artifactOutputSchema[T any]() (*jsonschema.Schema, error) {
	return jsonschema.ForType(reflect.TypeFor[T](), &jsonschema.ForOptions{})
}

func (s *Server) registerArtifactTools() error {
	createSchema, err := buildArtifactCreateSchema()
	if err != nil {
		return fmt.Errorf("build tangent_artifact_create input schema: %w", err)
	}
	handleOut, err := artifactOutputSchema[artifacts.Handle]()
	if err != nil {
		return fmt.Errorf("build artifact handle output schema: %w", err)
	}
	viewOut, err := artifactOutputSchema[artifacts.View]()
	if err != nil {
		return fmt.Errorf("build artifact view output schema: %w", err)
	}
	messageOut, err := artifactOutputSchema[artifacts.Message]()
	if err != nil {
		return fmt.Errorf("build artifact message output schema: %w", err)
	}
	awaitOut, err := artifactOutputSchema[artifacts.AwaitResult]()
	if err != nil {
		return fmt.Errorf("build artifact await output schema: %w", err)
	}
	itemID := &jsonschema.Schema{Type: "string", MinLength: jsonschema.Ptr(1), Description: "The item_id returned by tangent_artifact_create."}
	source := artifactSourceSchema()

	updateProps := contentProperties()
	updateProps["source"] = source
	updateProps["item_id"] = itemID
	updateSchema := &jsonschema.Schema{
		Type: "object", Required: []string{"source", "item_id"}, Properties: updateProps,
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
	getSchema := &jsonschema.Schema{
		Type: "object", Required: []string{"source", "item_id"},
		Properties: map[string]*jsonschema.Schema{
			"source":    source,
			"item_id":   itemID,
			"after_seq": {Type: "integer", Minimum: jsonschema.Ptr(0.0), Description: "Return only messages with a sequence greater than this. Omit for all."},
		},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
	messageSchema := &jsonschema.Schema{
		Type: "object", Required: []string{"source", "item_id", "body"},
		Properties: map[string]*jsonschema.Schema{
			"source":  source,
			"item_id": itemID,
			"body":    {Type: "string", MinLength: jsonschema.Ptr(1), MaxLength: jsonschema.Ptr(artifacts.MaxMessageBody)},
		},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
	awaitSchema := &jsonschema.Schema{
		Type: "object", Required: []string{"source", "item_id"},
		Properties: map[string]*jsonschema.Schema{
			"source":    source,
			"item_id":   itemID,
			"after_seq": {Type: "integer", Minimum: jsonschema.Ptr(0.0), Description: "Only messages after this sequence count as new."},
			"wait_ms":   {Type: "integer", Minimum: jsonschema.Ptr(0.0), Maximum: jsonschema.Ptr(120000.0), Description: "How long to wait, in milliseconds. Omit for 30000."},
		},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
	withdrawSchema := &jsonschema.Schema{
		Type: "object", Required: []string{"source", "item_id"},
		Properties: map[string]*jsonschema.Schema{
			"source":  source,
			"item_id": itemID,
			"reason":  {Type: "string", MaxLength: jsonschema.Ptr(600)},
		},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}

	addTool(s, gmcpserver.Tool{
		Name: "tangent_artifact_create",
		Description: "Show the operator an artifact — an HTML wireframe or mock, a screenshot or image, a diagram, a PDF, a chart — " +
			"as a durable item in their inbox, and return its handle (item_id, inbox_path, artifact_revision). It waits there until " +
			"they review it; they can reply, request changes, or sign off if you set requires_signoff. Repeating an idempotency_key " +
			"returns the original item. Change it later with tangent_artifact_update. An html mock is one self-contained file that runs " +
			"sandboxed with https CDN scripts and no network calls; for a shadcn/ui mock see the tangent-artifacts skill " +
			"(.agents/skills/tangent-artifacts/SKILL.md).",
		InputSchema:  createSchema,
		OutputSchema: handleOut,
	}, s.handleArtifactCreate)
	addTool(s, gmcpserver.Tool{
		Name: "tangent_artifact_update",
		Description: "Add a new revision to an artifact you created, so the operator iterates on one preview instead of a stream of new items. " +
			"The type cannot change. Refused once the artifact is signed off, archived or withdrawn.",
		InputSchema:  updateSchema,
		OutputSchema: handleOut,
	}, s.handleArtifactUpdate)
	addTool(s, gmcpserver.Tool{
		Name: "tangent_artifact_get",
		Description: "Read an artifact you created: its state, revisions, and the message thread (operator replies, change requests, sign-off). " +
			"Pass after_seq to get only newer messages.",
		InputSchema:  getSchema,
		OutputSchema: viewOut,
	}, s.handleArtifactGet)
	addTool(s, gmcpserver.Tool{
		Name:         "tangent_artifact_message",
		Description:  "Reply to the operator in an artifact's message thread, for example to say what changed in a new revision.",
		InputSchema:  messageSchema,
		OutputSchema: messageOut,
	}, s.handleArtifactMessage)
	addTool(s, gmcpserver.Tool{
		Name: "tangent_artifact_await",
		Description: "Wait up to two minutes for the operator to write on an artifact or finish it, and return the artifact with the new " +
			"messages. A timeout is a normal result (timed_out true) and changes nothing.",
		InputSchema:  awaitSchema,
		OutputSchema: awaitOut,
	}, s.handleArtifactAwait)
	addTool(s, gmcpserver.Tool{
		Name:         "tangent_artifact_withdraw",
		Description:  "Take back an artifact you no longer want shown. It moves to the operator's history. Refused once it is already finished.",
		InputSchema:  withdrawSchema,
		OutputSchema: viewOut,
	}, s.handleArtifactWithdraw)
	return nil
}

// buildArtifactCreateSchema is the packaged identity schema plus the content
// fields. The packaged schema alone describes what Tangent stores; the tool
// accepts the content too.
func buildArtifactCreateSchema() (*jsonschema.Schema, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(extensions.ArtifactItemContractSchema(), &schema); err != nil {
		return nil, err
	}
	if schema.Type != "object" {
		return nil, fmt.Errorf("artifact-item request schema has non-object root %q", schema.Type)
	}
	for name, property := range contentProperties() {
		schema.Properties[name] = property
	}
	// Advertise only the types this build accepts (the packaged enum also lists
	// one that shipped in contract 1.0 and is no longer supported).
	if property := schema.Properties["type"]; property != nil {
		property.Enum = property.Enum[:0:0]
		for _, name := range artifacts.SupportedTypes() {
			property.Enum = append(property.Enum, name)
		}
	}
	describe := func(name, text string) {
		if property := schema.Properties[name]; property != nil && property.Description == "" {
			property.Description = text
		}
	}
	describe("idempotency_key", "Your own unique key for this artifact. Sending it again with the same request returns the original item instead of creating a second one; the content is not compared.")
	describe("title", "Short name shown in the operator's inbox.")
	describe("summary", "One or two lines on what this is or what you want from the operator.")
	describe("type", artifactTypeHelp)
	describe("requires_signoff", "Set true to ask the operator to approve the artifact. Approve resolves it; Request changes leaves it open and sends you a message.")
	describe("tags", "Optional labels for filtering in the inbox.")
	describe("correlations", "Optional free-form references to your own work, such as a task id or branch. Stored and returned unchanged.")
	describe("contract_version", "Optional; defaults to the current version.")
	describe("source", "Who is sending. Artifacts are private to this application_id (or to the verified gateway identity); agent_id and agent_label are shown to the operator as the author.")
	// contract_version defaults to the current version when omitted.
	required := schema.Required[:0:0]
	for _, name := range schema.Required {
		if name != "contract_version" {
			required = append(required, name)
		}
	}
	schema.Required = required
	return &schema, nil
}

func (s *Server) artifactCaller(ctx context.Context, input map[string]any) (interactionActor, error) {
	source, ok := objectField(input, "source")
	if !ok {
		return interactionActor{}, fmt.Errorf("%w: source is required", artifacts.ErrInvalidRequest)
	}
	applicationID, _ := source["application_id"].(string)
	agentID, _ := source["agent_id"].(string)
	return directHITLActor(ctx, applicationID, agentID), nil
}

type interactionActor = interaction.ActorBinding

func contentFieldsOf(input map[string]any) artifacts.ContentFields {
	return artifacts.ContentFields{
		Content:       stringField(input, "content"),
		ContentBase64: stringField(input, "content_base64"),
		Filename:      stringField(input, "filename"),
		MediaType:     stringField(input, "media_type"),
		Note:          stringField(input, "note"),
	}
}

func (s *Server) handleArtifactCreate(ctx context.Context, input map[string]any) (any, error) {
	caller, err := s.artifactCaller(ctx, input)
	if err != nil {
		return nil, s.artifactError(err)
	}
	return s.artifactResult(s.artifacts.Create(ctx, artifacts.CreateInput{Request: rawJSON(input), Caller: caller}))
}

func (s *Server) handleArtifactUpdate(ctx context.Context, input map[string]any) (any, error) {
	caller, err := s.artifactCaller(ctx, input)
	if err != nil {
		return nil, s.artifactError(err)
	}
	return s.artifactResult(s.artifacts.Update(ctx, artifacts.UpdateInput{
		ItemID: stringField(input, "item_id"), Caller: caller, Content: contentFieldsOf(input),
	}))
}

func (s *Server) handleArtifactGet(ctx context.Context, input map[string]any) (any, error) {
	caller, err := s.artifactCaller(ctx, input)
	if err != nil {
		return nil, s.artifactError(err)
	}
	after, _ := integerField(input["after_seq"])
	return s.artifactResult(s.artifacts.Get(ctx, artifacts.GetInput{ItemID: stringField(input, "item_id"), Caller: caller, AfterSeq: after}))
}

func (s *Server) handleArtifactMessage(ctx context.Context, input map[string]any) (any, error) {
	caller, err := s.artifactCaller(ctx, input)
	if err != nil {
		return nil, s.artifactError(err)
	}
	return s.artifactResult(s.artifacts.AgentMessage(ctx, artifacts.MessageInput{
		ItemID: stringField(input, "item_id"), Caller: caller, Body: stringField(input, "body"),
	}))
}

func (s *Server) handleArtifactAwait(ctx context.Context, input map[string]any) (any, error) {
	caller, err := s.artifactCaller(ctx, input)
	if err != nil {
		return nil, s.artifactError(err)
	}
	after, _ := integerField(input["after_seq"])
	wait := 30 * time.Second
	if raw, exists := input["wait_ms"]; exists {
		milliseconds, ok := integerField(raw)
		if !ok {
			return nil, s.artifactError(fmt.Errorf("%w: wait_ms must be an integer", artifacts.ErrInvalidRequest))
		}
		wait = time.Duration(milliseconds) * time.Millisecond
	}
	return s.artifactResult(s.artifacts.Await(ctx, artifacts.AwaitInput{
		ItemID: stringField(input, "item_id"), Caller: caller, AfterSeq: after, Wait: wait,
	}))
}

func (s *Server) handleArtifactWithdraw(ctx context.Context, input map[string]any) (any, error) {
	caller, err := s.artifactCaller(ctx, input)
	if err != nil {
		return nil, s.artifactError(err)
	}
	return s.artifactResult(s.artifacts.Withdraw(ctx, artifacts.WithdrawInput{
		ItemID: stringField(input, "item_id"), Caller: caller, Reason: stringField(input, "reason"),
	}))
}

func (s *Server) artifactResult(value any, err error) (any, error) {
	if err != nil {
		return nil, s.artifactError(err)
	}
	return value, nil
}

func (s *Server) artifactError(err error) error {
	body := map[string]any{
		"contract_version": artifacts.ContractVersion,
		"code":             "artifact_error",
		"message":          err.Error(),
	}
	switch {
	case errors.Is(err, interaction.ErrIdempotencyConflict):
		body["code"] = "idempotency_conflict"
	case errors.Is(err, artifacts.ErrNotFound), errors.Is(err, interaction.ErrNotFound):
		body["code"] = "not_found"
		// Missing IDs and another producer's IDs must be indistinguishable.
		body["message"] = artifacts.ErrNotFound.Error()
	case errors.Is(err, interaction.ErrUnauthorized):
		body["code"] = "not_found"
		body["message"] = artifacts.ErrNotFound.Error()
	case errors.Is(err, artifacts.ErrTooLarge):
		body["code"] = "too_large"
	case errors.Is(err, artifacts.ErrTerminalConflict), errors.Is(err, interaction.ErrTerminal):
		body["code"] = "terminal_conflict"
	case errors.Is(err, interaction.ErrDefinitionValidation), errors.Is(err, artifacts.ErrInvalidRequest):
		body["code"] = "validation_failed"
	case errors.Is(err, context.Canceled):
		body["code"] = "wait_canceled"
	}
	return &rawToolError{message: body["message"].(string), body: body}
}
