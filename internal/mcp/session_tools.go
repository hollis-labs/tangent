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

type sessionAdvancePhaseInput struct {
	RoomID  string `json:"roomID"`
	ToPhase string `json:"to_phase"`
	Reason  string `json:"reason"`
}

type sessionSetPhaseOutputInput struct {
	RoomID string `json:"roomID"`
	Phase  string `json:"phase"`
	Key    string `json:"key"`
	Value  any    `json:"value"`
}

type sessionCloseInput struct {
	RoomID string `json:"roomID"`
	Status string `json:"status"`
}

type sessionListInput struct {
	ActiveOnly bool `json:"active_only"`
}

type sessionCreateResult struct {
	RoomID string `json:"roomID"`
	URL    string `json:"url"`
}

type sessionGetResult struct {
	Status                string                           `json:"status"`
	Phase                 string                           `json:"phase"`
	CurrentPhase          string                           `json:"current_phase"`
	PhasesVisited         []string                         `json:"phases_visited"`
	PhaseOutputs          map[string]room.PhaseOutput      `json:"phase_outputs"`
	ProgressPanel         *room.ProgressPanelStateView     `json:"progress_panel,omitempty"`
	FilePicker            *room.FilePickerStateView        `json:"file_picker,omitempty"`
	DiffReview            *room.DiffReviewStateView        `json:"diff_review,omitempty"`
	ApprovalQueue         *room.ApprovalQueueStateView     `json:"approval_queue,omitempty"`
	FormCollect           *room.FormStateView              `json:"form_collect,omitempty"`
	SpreadsheetReview     *room.SpreadsheetReviewStateView `json:"spreadsheet_review,omitempty"`
	Whiteboard            *room.WhiteboardStateView        `json:"whiteboard,omitempty"`
	SynthesisNotes        *room.SynthesisNotesView         `json:"synthesis_notes,omitempty"`
	AcceptedDraftBlocks   []room.DraftBlock                `json:"accepted_draft_blocks"`
	CurrentDraft          *room.CurrentDraftView           `json:"current_draft,omitempty"`
	ProseRevisionOutcomes []room.ProseRevisionOutcome      `json:"prose_revision_outcomes"`
	FinalOutput           *room.FinalOutputView            `json:"final_output,omitempty"`
	EnvelopesHistory      []room.EnvelopeHistory           `json:"envelopes_history"`
	CurrentEnvelope       *envelopes.Envelope              `json:"current_envelope,omitempty"`
}

type sessionPhaseStateResult struct {
	RoomID        string                      `json:"roomID"`
	CurrentPhase  string                      `json:"current_phase"`
	PhasesVisited []string                    `json:"phases_visited"`
	PhaseOutputs  map[string]room.PhaseOutput `json:"phase_outputs"`
}

type sessionCloseResult struct {
	OK     bool   `json:"ok"`
	RoomID string `json:"roomID"`
	Status string `json:"status"`
}

type sessionListResult struct {
	Rooms []room.RoomSummary `json:"rooms"`
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
	phaseState, found, err := s.manager.GetPhaseState(ctx, args.RoomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room phase state: %v", err)), emptySessionGetResult(), nil
	}
	history, err := s.manager.History(ctx, args.RoomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room history: %v", err)), emptySessionGetResult(), nil
	}

	rm, ok := s.manager.Get(args.RoomID)
	if !found && !ok && len(history) == 0 {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", args.RoomID)), emptySessionGetResult(), nil
	}

	result := sessionGetResult{
		Status:                "closed",
		Phase:                 "closed",
		CurrentPhase:          phaseState.CurrentPhase,
		PhasesVisited:         phaseState.PhasesVisited,
		PhaseOutputs:          phaseState.PhaseOutputs,
		ProgressPanel:         room.ProjectProgressPanelState(phaseState),
		FilePicker:            room.ProjectFilePickerState(phaseState),
		DiffReview:            room.ProjectDiffReviewState(phaseState),
		ApprovalQueue:         room.ProjectApprovalQueueState(phaseState),
		FormCollect:           room.ProjectFormState(phaseState),
		SpreadsheetReview:     room.ProjectSpreadsheetReviewState(phaseState),
		Whiteboard:            room.ProjectWhiteboardState(phaseState),
		SynthesisNotes:        room.ProjectSynthesisNotes(phaseState),
		AcceptedDraftBlocks:   room.ProjectAcceptedDraftBlocks(phaseState),
		CurrentDraft:          room.ProjectCurrentDraft(phaseState),
		ProseRevisionOutcomes: room.ProjectProseRevisionOutcomes(phaseState),
		FinalOutput:           room.ProjectFinalOutput(phaseState),
		EnvelopesHistory:      history,
	}
	if ok {
		result.Status = "active"
		result.Phase = "active"
		result.CurrentEnvelope = rm.CurrentEnvelope()
	}
	toolRes, payload := toolJSONResult(result)
	return toolRes, payload, nil
}

func (s *Server) handleSessionAdvancePhase(
	_ context.Context,
	_ *mcpsdk.CallToolRequest,
	args sessionAdvancePhaseInput,
) (*mcpsdk.CallToolResult, sessionPhaseStateResult, error) {
	phaseState, err := s.manager.AdvancePhase(args.RoomID, args.ToPhase, args.Reason)
	if err != nil {
		return sessionPhaseStateError(args.RoomID, err), emptySessionPhaseStateResult(args.RoomID), nil
	}
	toolRes, payload := toolJSONResult(sessionPhaseStateResult{
		RoomID:        args.RoomID,
		CurrentPhase:  phaseState.CurrentPhase,
		PhasesVisited: phaseState.PhasesVisited,
		PhaseOutputs:  phaseState.PhaseOutputs,
	})
	return toolRes, payload, nil
}

func (s *Server) handleSessionSetPhaseOutput(
	_ context.Context,
	_ *mcpsdk.CallToolRequest,
	args sessionSetPhaseOutputInput,
) (*mcpsdk.CallToolResult, sessionPhaseStateResult, error) {
	phaseState, err := s.manager.SetPhaseOutput(args.RoomID, args.Phase, args.Key, args.Value)
	if err != nil {
		return sessionPhaseStateError(args.RoomID, err), emptySessionPhaseStateResult(args.RoomID), nil
	}
	toolRes, payload := toolJSONResult(sessionPhaseStateResult{
		RoomID:        args.RoomID,
		CurrentPhase:  phaseState.CurrentPhase,
		PhasesVisited: phaseState.PhasesVisited,
		PhaseOutputs:  phaseState.PhaseOutputs,
	})
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

func (s *Server) handleSessionList(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args sessionListInput,
) (*mcpsdk.CallToolResult, sessionListResult, error) {
	rooms, err := s.manager.List(ctx, args.ActiveOnly)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("list rooms: %v", err)), sessionListResult{}, nil
	}
	toolRes, payload := toolJSONResult(sessionListResult{Rooms: rooms})
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

	resp, err := rm.PushWithResponseTransform(ctx, env, func(resp *envelopes.Response) (*envelopes.Response, error) {
		if resp == nil {
			return nil, fmt.Errorf("room response is nil")
		}
		if resp.EnvelopeID != env.ID {
			return nil, fmt.Errorf("%w: response envelopeId %q does not match pending envelope %q", envelopes.ErrSchemaValidation, resp.EnvelopeID, env.ID)
		}
		if err := s.envSvc.ValidateResponse(env.Type, resp); err != nil {
			return nil, err
		}
		if env.Type == whiteboardEnvelopeType {
			return s.normalizeWhiteboardSubmitResponse(roomID, env, resp)
		}
		if env.Type == filePickerEnvelopeType {
			return s.normalizeFilePickerSubmitResponse(roomID, env, resp)
		}
		if env.Type == progressPanelEnvelopeType {
			return s.normalizeProgressPanelSubmitResponse(roomID, env, resp)
		}
		if env.Type == diffReviewEnvelopeType {
			return s.normalizeDiffReviewSubmitResponse(roomID, env, resp)
		}
		if env.Type == spreadsheetReviewEnvelopeType {
			return s.normalizeSpreadsheetReviewSubmitResponse(roomID, env, resp)
		}
		if env.Type == approvalQueueEnvelopeType {
			return s.normalizeApprovalQueueSubmitResponse(roomID, env, resp)
		}
		if env.Type == formCollectEnvelopeType {
			return s.normalizeFormCollectSubmitResponse(roomID, env, resp)
		}
		return resp, nil
	})
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

func sessionPhaseStateError(roomID string, err error) *mcpsdk.CallToolResult {
	switch {
	case errors.Is(err, room.ErrRoomNotFound):
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID))
	case errors.Is(err, room.ErrInvalidPhaseID),
		errors.Is(err, room.ErrInvalidPhaseKey),
		errors.Is(err, room.ErrInvalidFilePickerID),
		errors.Is(err, room.ErrInvalidFilePickerBrowseRoot),
		errors.Is(err, room.ErrInvalidFilePickerSelectionRef),
		errors.Is(err, room.ErrInvalidFilePickerQueryState),
		errors.Is(err, room.ErrInvalidFilePickerSelectionRevision),
		errors.Is(err, room.ErrInvalidApprovalQueueID),
		errors.Is(err, room.ErrInvalidApprovalQueueItem),
		errors.Is(err, room.ErrInvalidApprovalQueueDecision),
		errors.Is(err, room.ErrInvalidApprovalQueueAuditEntry),
		errors.Is(err, room.ErrInvalidApprovalQueueExportRef),
		errors.Is(err, room.ErrInvalidSpreadsheetTableID),
		errors.Is(err, room.ErrInvalidSpreadsheetColumn),
		errors.Is(err, room.ErrInvalidSpreadsheetRow),
		errors.Is(err, room.ErrInvalidSpreadsheetSavedView),
		errors.Is(err, room.ErrInvalidSpreadsheetRowAction),
		errors.Is(err, room.ErrInvalidSpreadsheetActionID),
		errors.Is(err, room.ErrInvalidSpreadsheetExportRef),
		errors.Is(err, room.ErrInvalidWhiteboardBoardID),
		errors.Is(err, room.ErrInvalidWhiteboardSceneSnapshot),
		errors.Is(err, room.ErrInvalidWhiteboardAssetRef),
		errors.Is(err, room.ErrInvalidWhiteboardExportRef),
		errors.Is(err, room.ErrInvalidWhiteboardRevisionID),
		errors.Is(err, room.ErrInvalidDraftBlockID),
		errors.Is(err, room.ErrInvalidDraftBlockContent),
		errors.Is(err, room.ErrInvalidFormID),
		errors.Is(err, room.ErrInvalidFormSchema),
		errors.Is(err, room.ErrInvalidFormAnswers),
		errors.Is(err, room.ErrInvalidFormSavedDraft),
		errors.Is(err, room.ErrInvalidFormTemplate),
		errors.Is(err, room.ErrInvalidFormAction),
		errors.Is(err, room.ErrInvalidFormAttachmentRef),
		errors.Is(err, room.ErrInvalidProseRevisionID),
		errors.Is(err, room.ErrInvalidProseRevisionLens),
		errors.Is(err, room.ErrInvalidProseRevisionSourceText),
		errors.Is(err, room.ErrInvalidProseRevisionSuggestionID),
		errors.Is(err, room.ErrInvalidProseRevisionSuggestionText),
		errors.Is(err, room.ErrInvalidProseRevisionDecision),
		errors.Is(err, room.ErrInvalidProseRevisionOutcome),
		errors.Is(err, room.ErrInvalidFinalOutputMarkdown),
		errors.Is(err, room.ErrInvalidFinalOutputFormat):
		return toolErrorResult(envelopes.ErrorCodeValidationFailed, err.Error())
	default:
		return toolErrorResult(envelopes.ErrorCodeHostError, err.Error())
	}
}

func emptySessionPhaseStateResult(roomID string) sessionPhaseStateResult {
	return sessionPhaseStateResult{
		RoomID:        roomID,
		PhasesVisited: []string{},
		PhaseOutputs:  map[string]room.PhaseOutput{},
	}
}

func emptySessionGetResult() sessionGetResult {
	return sessionGetResult{
		PhasesVisited:         []string{},
		PhaseOutputs:          map[string]room.PhaseOutput{},
		AcceptedDraftBlocks:   []room.DraftBlock{},
		ProseRevisionOutcomes: []room.ProseRevisionOutcome{},
		EnvelopesHistory:      []room.EnvelopeHistory{},
	}
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
