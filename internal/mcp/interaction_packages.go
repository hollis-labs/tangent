package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/interactionpkg"
	"github.com/hollis-labs/tangent/internal/room"
)

// packageStateStore adapts the room manager's workflow-neutral phase blob to
// interactionpkg.StateStore.
//
// It is the entire persistence surface a package gets, and it is deliberately
// map[string]any in both directions: core writes and returns publisher-owned
// JSON without inspecting a key. No migration and no table is involved — the
// blob is the same `phase_outputs` column migration 0002 added, which is why a
// room written before the package boundary reads back through it unchanged.
type packageStateStore struct {
	manager *room.Manager
}

// LoadState implements interactionpkg.StateStore.
func (p packageStateStore) LoadState(
	ctx context.Context,
	roomID, phaseID string,
) (map[string]any, bool, error) {
	return p.manager.PhaseOutputData(ctx, roomID, phaseID)
}

// SaveState implements interactionpkg.StateStore.
func (p packageStateStore) SaveState(
	_ context.Context,
	roomID, phaseID string,
	data map[string]any,
) error {
	return p.manager.ReplacePhaseOutput(roomID, phaseID, data)
}

func (s *Server) packageStore() interactionpkg.StateStore {
	return packageStateStore{manager: s.manager}
}

// packagedWorkflowInput is the input shape every packaged room workflow tool
// shares. It is the same two fields the per-kind input structs carried; the
// kind is a parameter of the call rather than a field of the type, which is
// what lets one handler serve every packaged kind.
type packagedWorkflowInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

// handlePackagedRoomWorkflow is the tool body for a kind whose behavior lives
// in an interaction package.
//
// It is the C3 compatibility adapter for the tool surface: the tool keeps its
// frozen name, description, and input schema, and the request still creates or
// reuses a room and routes through advanceRoomEnvelope exactly as before. What
// changed is that the request projection between those two steps is now asked
// of the package instead of being a per-kind block written here.
//
// A kind with no installed or enabled package fails closed with
// `unsupported-type`. That is the deliberate answer rather than "present the
// request unprojected": a form presented without the room's persisted answers
// is not a degraded form, it is a different one, and ADR 0003 §8 C5 forbids
// silently downgrading a structured decision.
func (s *Server) handlePackagedRoomWorkflow(
	ctx context.Context,
	kind string,
	args packagedWorkflowInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != kind {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("%s rejects envelope type %q; want %q", kind, args.Envelope.Type, kind),
		), nil, nil
	}

	pkg, ok := s.packages.Lookup(kind)
	if !ok {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("%s: %v: no interaction package is installed for this kind", kind, interactionpkg.ErrPackageUnavailable),
		), nil, nil
	}

	roomID, reused := metaRoomID(args.Envelope.Meta)
	if !reused || roomID == "" {
		createRes, _, err := s.handleSessionCreate(ctx, nil, sessionCreateInput{
			Meta: map[string]any{
				"envelopeID":   args.Envelope.ID,
				"envelopeType": args.Envelope.Type,
			},
		})
		if err != nil {
			return nil, nil, err
		}
		if createRes.IsError {
			return createRes, nil, nil
		}
		var created sessionCreateResult
		if err := json.Unmarshal([]byte(extractToolText(createRes)), &created); err != nil {
			return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("decode session_create result: %v", err)), nil, nil
		}
		roomID = created.RoomID
		s.logWorkflowRoomCreated(workflowLogLabel(kind), roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused(workflowLogLabel(kind), roomID, args.Envelope.ID)
	}

	// The room's existence is checked before the package runs so a missing
	// room still surfaces as ROOM_NOT_FOUND rather than as whatever the
	// package's first store call happens to return.
	if _, found, err := s.manager.GetPhaseState(ctx, roomID); err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room state: %v", err)), nil, nil
	} else if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	presented, err := pkg.PresentRequest(ctx, s.packageStore(), roomID, &args.Envelope)
	if err != nil {
		return sessionPhaseStateError(roomID, err), nil, nil
	}
	return s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, presented, args.Completion)
}

// packagedWorkflowHandler binds one kind to the shared packaged-workflow body,
// producing the typed handler the MCP SDK's AddTool generic wants. It is what
// replaces a hand-written handle<Kind> function per packaged kind.
func (s *Server) packagedWorkflowHandler(
	kind string,
) func(context.Context, *mcpsdk.CallToolRequest, packagedWorkflowInput) (*mcpsdk.CallToolResult, any, error) {
	return func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		args packagedWorkflowInput,
	) (*mcpsdk.CallToolResult, any, error) {
		return s.handlePackagedRoomWorkflow(ctx, kind, args)
	}
}

// workflowLogLabel is the short name the workflow room logs use. It is the
// wire name minus its publisher namespace, which reproduces the literals the
// per-kind handlers passed by hand ("form-collect", "triage", …).
func workflowLogLabel(kind string) string {
	if _, slug, found := strings.Cut(kind, "."); found {
		return slug
	}
	return kind
}

// sessionGetOutputSchema composes tangent.session_get's advertised output
// schema: the reflected core projection plus one property per installed
// package, contributed by that package.
//
// This is the other half of sessionGetResult.MarshalJSON, and it is what makes
// moving a projection type out of core a non-event on the wire. The MCP SDK
// infers a tool's output schema from its Go return type when none is supplied,
// so deleting a projection field from sessionGetResult would have silently
// removed the corresponding property from what tools/list advertises — a
// change to a contract ADR 0003 §8 C6 freezes. Supplying the schema explicitly
// keeps the advertised shape identical while the type lives in the package
// that owns it.
//
// Package schemas are contributed by every *registered* package, enabled or
// not: an operator toggling a kind off at runtime must not reshape a tool
// contract. Equality with the pre-package schema is asserted byte-for-byte by
// TestSessionGetOutputSchemaMatchesPrePackageContract.
func (s *Server) sessionGetOutputSchema() (*jsonschema.Schema, error) {
	schema, err := jsonschema.ForType(reflect.TypeFor[sessionGetResult](), &jsonschema.ForOptions{})
	if err != nil {
		return nil, fmt.Errorf("reflect session_get output schema: %w", err)
	}
	contributions := s.packages.ProjectionSchemas()
	if len(contributions) == 0 {
		return schema, nil
	}
	if schema.Properties == nil {
		schema.Properties = map[string]*jsonschema.Schema{}
	}
	for key, contributed := range contributions {
		projection, ok := contributed.(*jsonschema.Schema)
		if !ok {
			return nil, fmt.Errorf(
				"session_get output schema: package projection %q is %T, want *jsonschema.Schema", key, contributed)
		}
		if _, taken := schema.Properties[key]; taken {
			return nil, fmt.Errorf(
				"session_get output schema: package projection %q collides with a core field", key)
		}
		schema.Properties[key] = projection
	}
	return schema, nil
}
