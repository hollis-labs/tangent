package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

const (
	errorCodeSessionBusy  = "SESSION_BUSY"
	errorCodeRoomNotFound = "ROOM_NOT_FOUND"
)

type sessionCreateInput struct {
	Title string         `json:"title"`
	Meta  map[string]any `json:"meta"`
}

type sessionAdvanceInput struct {
	RoomID   string             `json:"roomID"`
	Envelope envelopes.Envelope `json:"envelope"`
}

type sessionGetInput struct {
	RoomID string `json:"roomID"`
}

type sessionCloseInput struct {
	RoomID string `json:"roomID"`
	Status string `json:"status"`
}

type sessionCreateResult struct {
	RoomID string `json:"roomID"`
	URL    string `json:"url"`
}

type sessionGetResult struct {
	Phase            string                 `json:"phase"`
	EnvelopesHistory []room.EnvelopeHistory `json:"envelopes_history"`
	CurrentEnvelope  *envelopes.Envelope    `json:"current_envelope,omitempty"`
}

type sessionCloseResult struct {
	OK     bool   `json:"ok"`
	RoomID string `json:"roomID"`
	Status string `json:"status"`
}

func (s *Server) handleSessionCreate(
	_ context.Context,
	_ *mcpsdk.CallToolRequest,
	args sessionCreateInput,
) (*mcpsdk.CallToolResult, sessionCreateResult, error) {
	meta := stringifyMeta(args.Meta)
	if args.Title != "" {
		if _, exists := meta["title"]; !exists {
			meta["title"] = args.Title
		}
	}
	rm, err := s.manager.CreateWithError(meta)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("create room: %v", err)), sessionCreateResult{}, nil
	}
	result := sessionCreateResult{
		RoomID: rm.ID,
		URL:    s.roomURL(rm.ID),
	}
	toolRes, payload := toolJSONResult(result)
	return toolRes, payload, nil
}

func (s *Server) handleSessionAdvance(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args sessionAdvanceInput,
) (*mcpsdk.CallToolResult, any, error) {
	return s.advanceRoomEnvelope(ctx, args.RoomID, &args.Envelope)
}

func (s *Server) handleSessionGet(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args sessionGetInput,
) (*mcpsdk.CallToolResult, sessionGetResult, error) {
	history, err := s.manager.History(ctx, args.RoomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room history: %v", err)), sessionGetResult{}, nil
	}

	rm, ok := s.manager.Get(args.RoomID)
	if !ok && len(history) == 0 {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", args.RoomID)), sessionGetResult{}, nil
	}

	result := sessionGetResult{
		Phase:            "closed",
		EnvelopesHistory: history,
	}
	if ok {
		result.Phase = "active"
		result.CurrentEnvelope = rm.CurrentEnvelope()
	}
	toolRes, payload := toolJSONResult(result)
	return toolRes, payload, nil
}

func (s *Server) handleSessionClose(
	_ context.Context,
	_ *mcpsdk.CallToolRequest,
	args sessionCloseInput,
) (*mcpsdk.CallToolResult, sessionCloseResult, error) {
	status := args.Status
	if status == "" {
		status = "closed"
	}
	if err := s.manager.Close(args.RoomID, status); err != nil {
		if errors.Is(err, room.ErrRoomNotFound) {
			return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", args.RoomID)), sessionCloseResult{}, nil
		}
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("close room: %v", err)), sessionCloseResult{}, nil
	}
	toolRes, payload := toolJSONResult(sessionCloseResult{
		OK:     true,
		RoomID: args.RoomID,
		Status: status,
	})
	return toolRes, payload, nil
}

func (s *Server) advanceRoomEnvelope(
	ctx context.Context,
	roomID string,
	env *envelopes.Envelope,
) (*mcpsdk.CallToolResult, any, error) {
	if env == nil {
		return toolErrorResult(envelopes.ErrorCodeValidationFailed, "envelope is required"), nil, nil
	}
	if err := s.envSvc.Validate(env); err != nil {
		return triageErrorResult(err), nil, nil
	}

	rm, ok := s.manager.Get(roomID)
	if !ok {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}
	if !rm.TryClaimAdvance() {
		return toolErrorResult(errorCodeSessionBusy, fmt.Sprintf("room %q already has a pending envelope", roomID)), nil, nil
	}
	defer rm.ReleaseAdvance()
	if rm.HasPending() {
		return toolErrorResult(errorCodeSessionBusy, fmt.Sprintf("room %q already has a pending envelope", roomID)), nil, nil
	}

	resp, err := rm.Push(ctx, env)
	if err != nil {
		if errors.Is(err, room.ErrUserCancelled) {
			cancelled := &envelopes.Response{
				V:           envelopes.ProtocolVersion,
				EnvelopeID:  env.ID,
				Kind:        envelopes.ResponseKindAck,
				Status:      envelopes.ResponseStatusCancelled,
				CompletedAt: nowRFC3339(),
			}
			toolRes, payload := toolJSONResult(cancelled)
			return toolRes, payload, nil
		}
		return triageErrorResult(err), nil, nil
	}
	toolRes, payload := toolJSONResult(resp)
	return toolRes, payload, nil
}

func (s *Server) roomURL(roomID string) string {
	if s.roomURLBase == "" {
		return ""
	}
	return s.roomURLBase + fmt.Sprintf(triageRoomURLPath, roomID)
}

func stringifyMeta(meta map[string]any) map[string]string {
	if len(meta) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(meta))
	for k, v := range meta {
		switch typed := v.(type) {
		case string:
			out[k] = typed
		default:
			raw, err := json.Marshal(v)
			if err != nil {
				out[k] = fmt.Sprint(v)
				continue
			}
			out[k] = string(raw)
		}
	}
	return out
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func toolJSONResult[T any](payload T) (*mcpsdk.CallToolResult, T) {
	body, err := json.Marshal(payload)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("marshal tool result: %v", err)), payload
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
	}, payload
}

func (s *Server) logWorkflowRoomCreated(workflow, roomID, envelopeID string) {
	url := s.roomURL(roomID)
	if url != "" {
		slog.Default().Info(workflow+" room created", "room", roomID, "url", url, "envelope", envelopeID)
		return
	}
	slog.Default().Info(workflow+" room created", "room", roomID, "envelope", envelopeID)
}

func (s *Server) logWorkflowRoomReused(workflow, roomID, envelopeID string) {
	slog.Default().Info(workflow+" routed to existing room", "room", roomID, "envelope", envelopeID)
}
