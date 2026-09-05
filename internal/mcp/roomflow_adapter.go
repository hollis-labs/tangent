package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/roomflow"
)

// errorCodeIdempotencyConflict is returned when a caller reuses one
// workflow-kind + envelope-id identity with a different payload. It is a hard
// error on purpose: silently executing the new payload would give one durable
// identity two meanings, and silently returning the old result would hide that
// the caller asked for something else.
const errorCodeIdempotencyConflict = "IDEMPOTENCY_CONFLICT"

// completionInput selects how a named room workflow returns.
//
// The default is deliberately "wait": every third-party integration written
// against v0.12 sends no completion object at all, and those callers must keep
// their exact behavior for interactions a human answers quickly.
type completionInput struct {
	Mode string `json:"mode,omitempty"`
}

func (c completionInput) mode() string {
	if c.Mode == roomflow.ModeAsync {
		return roomflow.ModeAsync
	}
	return roomflow.ModeWait
}

// completionPropertySchemaJSON is the shared completion selector advertised by
// every room-backed tool. It is injected into each tool's input schema at
// registration rather than duplicated across eighteen hand-written schema
// documents, so a new room workflow cannot accidentally ship without it.
var completionPropertySchemaJSON = []byte(`{
  "type": "object",
  "title": "completion mode",
  "description": "How this call returns. Omit for the default 'wait'.",
  "properties": {
    "mode": {
      "type": "string",
      "enum": ["wait", "async"],
      "default": "wait",
      "description": "'wait' blocks up to 45 seconds and then returns a durable pending receipt. 'async' returns that receipt immediately. Both are successful results; neither cancels the interaction."
    }
  },
  "additionalProperties": false
}`)

// withCompletionMode returns schema with the shared completion selector added.
func withCompletionMode(schema *jsonschema.Schema, name string) (*jsonschema.Schema, error) {
	completion, err := buildSchema(completionPropertySchemaJSON, name+" completion")
	if err != nil {
		return nil, err
	}
	if schema.Properties == nil {
		schema.Properties = map[string]*jsonschema.Schema{}
	}
	schema.Properties["completion"] = completion
	return schema, nil
}

// PinnedResponseKind implements roomflow.Normalizer. The registry's declared
// response kind for a type is what the interaction catalog validates a
// resolution against, so the adapter must record that value rather than
// whatever kind the browser happened to send.
func (s *Server) PinnedResponseKind(envelopeType string) (string, error) {
	material, ok := s.envSvc.LookupDefinitionMaterial(envelopeType)
	if !ok {
		return "", fmt.Errorf("%w: %q", envelopes.ErrUnknownType, envelopeType)
	}
	return material.ResponseKind, nil
}

// NormalizeResponse implements roomflow.Normalizer. It is the exact
// validation and per-workflow normalization the v0.12 blocking path applied,
// factored out so the durable path and the legacy path cannot drift.
func (s *Server) NormalizeResponse(
	roomID string,
	env *envelopes.Envelope,
	resp *envelopes.Response,
) (*envelopes.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("room response is nil")
	}
	if resp.EnvelopeID != env.ID {
		return nil, fmt.Errorf(
			"%w: response envelopeId %q does not match pending envelope %q",
			envelopes.ErrSchemaValidation, resp.EnvelopeID, env.ID)
	}
	if err := s.envSvc.ValidateResponse(env.Type, resp); err != nil {
		return nil, err
	}
	// A packaged kind's response interpretation belongs to its package. The
	// lookup comes before the switch so a kind cannot be served by both, and
	// so removing a package removes its normalization rather than silently
	// reverting to a core arm that still knows the kind.
	if pkg, ok := s.packages.Lookup(env.Type); ok {
		return pkg.NormalizeResponse(context.Background(), s.packageStore(), roomID, env, resp)
	}
	switch env.Type {
	case whiteboardEnvelopeType:
		return s.normalizeWhiteboardSubmitResponse(roomID, env, resp)
	case dashboardEnvelopeType:
		return s.normalizeDashboardSubmitResponse(roomID, env, resp)
	case filePickerEnvelopeType:
		return s.normalizeFilePickerSubmitResponse(roomID, env, resp)
	case progressPanelEnvelopeType:
		return s.normalizeProgressPanelSubmitResponse(roomID, env, resp)
	case wizardEnvelopeType:
		return s.normalizeWizardSubmitResponse(roomID, env, resp)
	case diffReviewEnvelopeType:
		return s.normalizeDiffReviewSubmitResponse(roomID, env, resp)
	case spreadsheetReviewEnvelopeType:
		return s.normalizeSpreadsheetReviewSubmitResponse(roomID, env, resp)
	case approvalQueueEnvelopeType:
		return s.normalizeApprovalQueueSubmitResponse(roomID, env, resp)
	default:
		return resp, nil
	}
}

// RestoreRoomPresentations rebuilds every live room's UI from canonical
// records after a restart. Callers wire it at boot, after the room manager has
// hydrated. It is a no-op when the durable substrate is not installed.
func (s *Server) RestoreRoomPresentations(ctx context.Context) (roomflow.RestoreReport, error) {
	if s.roomflow == nil {
		return roomflow.RestoreReport{}, nil
	}
	return s.roomflow.RestorePresentations(ctx)
}

// advanceRoomEnvelopeDurable runs one named workflow through the canonical
// substrate.
func (s *Server) advanceRoomEnvelopeDurable(
	ctx context.Context,
	roomID string,
	request *envelopes.Envelope,
	presented *envelopes.Envelope,
	completion completionInput,
	caller interaction.ActorBinding,
) (*mcpsdk.CallToolResult, any, error) {
	// Recovery outranks admission control, and outranks the room's own
	// existence. A caller retrying a request Tangent already owns must reach
	// its own result even when the room has moved on to other work or been
	// closed entirely — otherwise the busy gate, or a torn-down room, would
	// hide the very outcome the retry exists to recover.
	_, known, err := s.roomflow.Recognize(ctx, caller, request)
	if err != nil {
		return triageErrorResult(err), nil, nil
	}
	if !known {
		rm, ok := s.manager.Get(roomID)
		if !ok {
			return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
		}
		if !rm.TryClaimAdvance() {
			return toolErrorResult(errorCodeSessionBusy,
				fmt.Sprintf("room %q already has a pending envelope", roomID)), nil, nil
		}
		defer rm.ReleaseAdvance()
		if rm.HasPendingOther(request.ID) {
			return toolErrorResult(errorCodeSessionBusy,
				fmt.Sprintf("room %q already has a pending envelope", roomID)), nil, nil
		}
	}

	outcome, err := s.roomflow.Run(ctx, roomflow.Request{
		RoomID: roomID, Envelope: request, Presented: presented,
		Mode: completion.mode(), Caller: caller,
	})
	if err != nil {
		if errors.Is(err, roomflow.ErrRoomNotFound) {
			return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
		}
		return triageErrorResult(err), nil, nil
	}
	switch outcome.Status {
	case roomflow.StatusResolved, roomflow.StatusCancelled:
		toolRes, payload := toolJSONResult(outcome.Response)
		return toolRes, payload, nil
	case roomflow.StatusPendingOutcome:
		toolRes, payload := toolJSONResult(outcome.Receipt)
		return toolRes, payload, nil
	case roomflow.StatusConflict:
		return toolErrorResult(errorCodeIdempotencyConflict, fmt.Sprintf(
			"envelope %q of type %q is already bound to interaction %q with a different payload; "+
				"use a new envelope id or retry the identical payload",
			request.ID, request.Type, outcome.ExistingInteractionID)), nil, nil
	default:
		code := outcome.TerminalErrorCode
		if code == "" {
			code = envelopes.ErrorCodeHostError
		}
		return toolErrorResult(code, outcome.TerminalMessage), nil, nil
	}
}

// interactionAcknowledgeInput names one caller acknowledgement.
type interactionAcknowledgeInput struct {
	InteractionID        string         `json:"interaction_id"`
	RequesterScope       string         `json:"requester_scope"`
	TransportCorrelation map[string]any `json:"transport_correlation,omitempty"`
}

func (s *Server) handleInteractionAcknowledge(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input interactionAcknowledgeInput,
) (*mcpsdk.CallToolResult, any, error) {
	return s.interactionResult(s.interactions.AcknowledgeTerminalOutcome(ctx, interaction.AcknowledgeTerminalOutcomeInput{
		InteractionID:        input.InteractionID,
		RequesterScope:       requesterScope(input.RequesterScope),
		TransportCorrelation: rawJSON(input.TransportCorrelation),
	}))
}

// closeRoomDurably terminalizes a room's outstanding interactions under the
// named surface policy before the room's own projection is torn down.
func (s *Server) closeRoomDurably(
	ctx context.Context,
	roomID, status string,
	caller interaction.ActorBinding,
) error {
	if s.roomflow == nil {
		return nil
	}
	err := s.roomflow.CloseRoom(ctx, roomID, status, caller)
	if err == nil || errors.Is(err, interaction.ErrNotFound) {
		return nil
	}
	return err
}
