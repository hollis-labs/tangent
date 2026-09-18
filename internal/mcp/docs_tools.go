package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	gmcpserver "github.com/hollis-labs/go-mcp/server"

	"github.com/hollis-labs/tangent/internal/docs"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
)

func (s *Server) registerDocsTools() error {
	schema, err := buildDocsInputSchema()
	if err != nil {
		return fmt.Errorf("build tangent.docs_enqueue input schema: %w", err)
	}
	addTool(s, gmcpserver.Tool{
		Name:        "tangent.docs_enqueue",
		Description: "Enqueue one durable document in the operator's Docs inbox for reading at their own pace, with an optional acknowledgment request.",
		InputSchema: schema,
	}, s.handleDocsEnqueue)
	return nil
}

func buildDocsInputSchema() (*jsonschema.Schema, error) {
	raw := extensions.DocItemContractSchema()
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	if schema.Type != "object" {
		return nil, fmt.Errorf("doc-item request schema has non-object root %q", schema.Type)
	}
	return &schema, nil
}

func (s *Server) handleDocsEnqueue(
	ctx context.Context,
	input map[string]any,
) (any, error) {
	source, ok := objectField(input, "source")
	if !ok {
		return nil, s.docsError(fmt.Errorf("%w: source is required", docs.ErrInvalidRequest))
	}
	applicationID, _ := source["application_id"].(string)
	agentID, _ := source["agent_id"].(string)
	return s.docsResult(s.docs.Enqueue(ctx, docs.EnqueueInput{
		Request: rawJSON(input), Caller: directHITLActor(applicationID, agentID),
	}))
}

func (s *Server) docsResult(value any, err error) (any, error) {
	if err != nil {
		return nil, s.docsError(err)
	}
	return value, nil
}

func (s *Server) docsError(err error) error {
	body := map[string]any{
		"contract_version": docs.ContractVersion,
		"code":             "docs_error",
		"message":          err.Error(),
	}
	switch {
	case errors.Is(err, interaction.ErrIdempotencyConflict):
		body["code"] = "idempotency_conflict"
	case errors.Is(err, interaction.ErrNotFound):
		body["code"] = "not_found"
	case errors.Is(err, interaction.ErrUnauthorized):
		body["code"] = "unauthorized"
	case errors.Is(err, interaction.ErrDefinitionValidation), errors.Is(err, docs.ErrInvalidRequest):
		body["code"] = "validation_failed"
	}
	return &rawToolError{message: err.Error(), body: body}
}
