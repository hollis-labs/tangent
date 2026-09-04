package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

type wizardInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

type wizardSubmitDraft struct {
	WizardID         string                       `json:"wizard_id"`
	Title            string                       `json:"title,omitempty"`
	Description      string                       `json:"description,omitempty"`
	CurrentStepID    string                       `json:"current_step_id,omitempty"`
	Steps            []room.WizardStep            `json:"steps,omitempty"`
	Progress         []room.WizardStepProgress    `json:"progress,omitempty"`
	BranchSelections []room.WizardBranchSelection `json:"branch_selections,omitempty"`
	Summary          *room.WizardSummary          `json:"summary,omitempty"`
	UpdatedAt        string                       `json:"updated_at,omitempty"`
}

func (s *Server) handleWizard(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args wizardInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != wizardEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("tangent.wizard rejects envelope type %q; want %q", args.Envelope.Type, wizardEnvelopeType),
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
		s.logWorkflowRoomCreated("wizard", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("wizard", roomID, args.Envelope.ID)
	}

	phaseState, found, err := s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room wizard state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	snapshot := wizardSnapshotFromEnvelope(args.Envelope, room.ProjectWizardState(phaseState))
	if _, saveErr := s.manager.SaveWizardSnapshot(roomID, snapshot); saveErr != nil {
		return sessionPhaseStateError(roomID, saveErr), nil, nil
	}
	phaseState, found, err = s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("reload room wizard state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	return s.advanceRoomEnvelope(ctx, roomID, &args.Envelope, buildVisibleWizardEnvelope(&args.Envelope, room.ProjectWizardState(phaseState)), args.Completion)
}

func wizardSnapshotFromEnvelope(env envelopes.Envelope, persisted *room.WizardStateView) room.WizardSnapshot {
	data := env.Data
	wizardID := readStringValue(data, "wizard_id")
	if wizardID == "" && persisted != nil {
		wizardID = persisted.WizardID
	}
	reusePersisted := persisted != nil && persisted.WizardID != "" && persisted.WizardID == wizardID

	steps := readWizardStepsValue(data["steps"])
	if len(steps) == 0 && persisted != nil {
		steps = persisted.Steps
	}
	currentStepID := readStringValue(data, "current_step_id")
	if currentStepID == "" && persisted != nil {
		currentStepID = persisted.CurrentStepID
	}
	progress := readWizardProgressValue(data["progress"])
	if reusePersisted && len(progress) == 0 {
		progress = persisted.Progress
	}
	branchSelections := readWizardBranchSelectionsValue(data["branch_selections"])
	if reusePersisted && len(branchSelections) == 0 {
		branchSelections = persisted.BranchSelections
	}
	summary := readWizardSummaryValue(data["summary"])
	if reusePersisted && summary == nil {
		summary = persisted.Summary
	}

	return room.WizardSnapshot{
		WizardID:         wizardID,
		Title:            fallbackString(readStringValue(data, "title"), persistedTitle(persisted)),
		Description:      fallbackString(readStringValue(data, "description"), persistedDescription(persisted)),
		CurrentStepID:    currentStepID,
		Steps:            steps,
		Progress:         progress,
		BranchSelections: branchSelections,
		Summary:          summary,
		UpdatedAt:        fallbackString(readStringValue(data, "updated_at"), nowRFC3339()),
	}
}

func buildVisibleWizardEnvelope(env *envelopes.Envelope, view *room.WizardStateView) *envelopes.Envelope {
	clone := cloneEnvelopeForDispatch(env)
	if clone.Data == nil {
		clone.Data = map[string]any{}
	}
	if view == nil {
		return clone
	}
	data := cloneAnyMapForDispatch(clone.Data)
	data["wizard_id"] = view.WizardID
	if view.Title != "" {
		data["title"] = view.Title
	}
	if view.Description != "" {
		data["description"] = view.Description
	}
	data["steps"] = wizardStepsPayload(view.Steps)
	data["current_step_id"] = view.CurrentStepID
	data["progress"] = wizardProgressPayload(view.Progress)
	data["branch_selections"] = wizardBranchSelectionsPayload(view.BranchSelections)
	data["updated_at"] = view.UpdatedAt
	if view.Summary != nil {
		data["summary"] = wizardSummaryPayload(view.Summary)
	}
	clone.Data = data
	return clone
}

func (s *Server) normalizeWizardSubmitResponse(
	roomID string,
	_ *envelopes.Envelope,
	resp *envelopes.Response,
) (*envelopes.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("%w: response is required", envelopes.ErrSchemaValidation)
	}
	if resp.Kind != envelopes.ResponseKindData {
		return nil, fmt.Errorf("%w: kind must be %q", envelopes.ErrSchemaValidation, envelopes.ResponseKindData)
	}
	if resp.Status != envelopes.ResponseStatusPartial && resp.Status != envelopes.ResponseStatusSubmitted {
		return nil, fmt.Errorf("%w: status must be %q or %q", envelopes.ErrSchemaValidation, envelopes.ResponseStatusPartial, envelopes.ResponseStatusSubmitted)
	}

	phaseState, found, err := s.manager.GetPhaseState(context.Background(), roomID)
	if err != nil {
		return nil, fmt.Errorf("wizard submit: load room state: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", room.ErrRoomNotFound, roomID)
	}
	persisted := room.ProjectWizardState(phaseState)
	if persisted == nil {
		return nil, fmt.Errorf("%w: room has no persisted wizard state", envelopes.ErrSchemaValidation)
	}

	draft, err := decodeWizardSubmitDraft(resp.Payload)
	if err != nil {
		return nil, err
	}
	if draft.WizardID == "" || draft.WizardID != persisted.WizardID {
		return nil, fmt.Errorf("%w: payload.wizard_id %q does not match room wizard_id %q", envelopes.ErrSchemaValidation, draft.WizardID, persisted.WizardID)
	}

	updatedAt := draft.UpdatedAt
	if updatedAt == "" {
		updatedAt = nowRFC3339()
	}
	summary := fallbackWizardSummary(draft.Summary, persisted.Summary)
	if summary == nil {
		summary = &room.WizardSummary{}
	}
	if resp.Status == envelopes.ResponseStatusSubmitted {
		summary.Status = "completed"
		summary.CompletedAt = updatedAt
	}
	snapshot := room.WizardSnapshot{
		WizardID:         persisted.WizardID,
		Title:            fallbackString(draft.Title, persisted.Title),
		Description:      fallbackString(draft.Description, persisted.Description),
		CurrentStepID:    fallbackString(draft.CurrentStepID, persisted.CurrentStepID),
		Steps:            fallbackWizardSteps(draft.Steps, persisted.Steps),
		Progress:         fallbackWizardProgress(draft.Progress, persisted.Progress),
		BranchSelections: fallbackWizardBranchSelections(draft.BranchSelections, persisted.BranchSelections),
		Summary:          summary,
		UpdatedAt:        updatedAt,
	}
	if _, err := s.manager.SaveWizardSnapshot(roomID, snapshot); err != nil {
		return nil, err
	}

	return &envelopes.Response{
		V:           envelopes.ProtocolVersion,
		EnvelopeID:  resp.EnvelopeID,
		Kind:        envelopes.ResponseKindData,
		Status:      resp.Status,
		Payload:     wizardSnapshotPayload(snapshot),
		CompletedAt: updatedAt,
	}, nil
}

func decodeWizardSubmitDraft(payload any) (wizardSubmitDraft, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return wizardSubmitDraft{}, fmt.Errorf("%w: marshal payload: %w", envelopes.ErrSchemaValidation, err)
	}
	var draft wizardSubmitDraft
	if err := json.Unmarshal(raw, &draft); err != nil {
		return wizardSubmitDraft{}, fmt.Errorf("%w: decode payload: %w", envelopes.ErrSchemaValidation, err)
	}
	return draft, nil
}

func readWizardStepsValue(raw any) []room.WizardStep {
	records := readObjectSliceValue(raw)
	out := make([]room.WizardStep, 0, len(records))
	for _, record := range records {
		stepID := readStringValue(record, "step_id")
		title := readStringValue(record, "title")
		if stepID == "" || title == "" {
			continue
		}
		out = append(out, room.WizardStep{
			StepID:      stepID,
			Title:       title,
			Description: readStringValue(record, "description"),
			Kind:        readStringValue(record, "kind"),
			Optional:    readBoolValue(record["optional"]),
			Fields:      readObjectValue(record, "fields"),
			Branches:    readWizardBranchesValue(record["branches"]),
			Metadata:    readObjectValue(record, "metadata"),
		})
	}
	return out
}

func readWizardBranchesValue(raw any) []room.WizardStepBranch {
	records := readObjectSliceValue(raw)
	out := make([]room.WizardStepBranch, 0, len(records))
	for _, record := range records {
		branchID := readStringValue(record, "branch_id")
		label := readStringValue(record, "label")
		targetStepID := readStringValue(record, "target_step_id")
		if branchID == "" || label == "" || targetStepID == "" {
			continue
		}
		out = append(out, room.WizardStepBranch{
			BranchID:     branchID,
			Label:        label,
			Description:  readStringValue(record, "description"),
			TargetStepID: targetStepID,
			Metadata:     readObjectValue(record, "metadata"),
		})
	}
	return out
}

func readWizardProgressValue(raw any) []room.WizardStepProgress {
	records := readObjectSliceValue(raw)
	out := make([]room.WizardStepProgress, 0, len(records))
	for _, record := range records {
		stepID := readStringValue(record, "step_id")
		status := readStringValue(record, "status")
		if stepID == "" || status == "" {
			continue
		}
		out = append(out, room.WizardStepProgress{
			StepID:      stepID,
			Status:      status,
			RevisionID:  readStringValue(record, "revision_id"),
			Response:    readObjectValue(record, "response"),
			Summary:     readStringValue(record, "summary"),
			CompletedAt: readStringValue(record, "completed_at"),
			UpdatedAt:   readStringValue(record, "updated_at"),
		})
	}
	return out
}

func readWizardBranchSelectionsValue(raw any) []room.WizardBranchSelection {
	records := readObjectSliceValue(raw)
	out := make([]room.WizardBranchSelection, 0, len(records))
	for _, record := range records {
		stepID := readStringValue(record, "step_id")
		optionID := readStringValue(record, "option_id")
		if stepID == "" || optionID == "" {
			continue
		}
		out = append(out, room.WizardBranchSelection{
			StepID:       stepID,
			OptionID:     optionID,
			SelectedAt:   readStringValue(record, "selected_at"),
			TargetStepID: readStringValue(record, "target_step_id"),
		})
	}
	return out
}

func readWizardSummaryValue(raw any) *room.WizardSummary {
	record, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return &room.WizardSummary{
		Status:              readStringValue(record, "status"),
		Headline:            readStringValue(record, "headline"),
		Detail:              readStringValue(record, "detail"),
		CompletedStepCount:  readIntValue(record["completed_step_count"]),
		TotalStepCount:      readIntValue(record["total_step_count"]),
		CompletedAt:         readStringValue(record, "completed_at"),
		LastCompletedStepID: readStringValue(record, "last_completed_step_id"),
		CurrentStepID:       readStringValue(record, "current_step_id"),
	}
}

func wizardStepsPayload(items []room.WizardStep) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"step_id":  item.StepID,
			"title":    item.Title,
			"fields":   cloneAnyMapForDispatch(item.Fields),
			"branches": wizardBranchesPayload(item.Branches),
			"metadata": cloneAnyMapForDispatch(item.Metadata),
		}
		if item.Description != "" {
			record["description"] = item.Description
		}
		if item.Kind != "" {
			record["kind"] = item.Kind
		}
		if item.Optional {
			record["optional"] = true
		}
		out = append(out, record)
	}
	return out
}

func wizardBranchesPayload(items []room.WizardStepBranch) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"branch_id":      item.BranchID,
			"label":          item.Label,
			"target_step_id": item.TargetStepID,
			"metadata":       cloneAnyMapForDispatch(item.Metadata),
		}
		if item.Description != "" {
			record["description"] = item.Description
		}
		out = append(out, record)
	}
	return out
}

func wizardProgressPayload(items []room.WizardStepProgress) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"step_id":  item.StepID,
			"status":   item.Status,
			"response": cloneAnyMapForDispatch(item.Response),
		}
		if item.RevisionID != "" {
			record["revision_id"] = item.RevisionID
		}
		if item.Summary != "" {
			record["summary"] = item.Summary
		}
		if item.CompletedAt != "" {
			record["completed_at"] = item.CompletedAt
		}
		if item.UpdatedAt != "" {
			record["updated_at"] = item.UpdatedAt
		}
		out = append(out, record)
	}
	return out
}

func wizardBranchSelectionsPayload(items []room.WizardBranchSelection) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"step_id":        item.StepID,
			"option_id":      item.OptionID,
			"target_step_id": item.TargetStepID,
		}
		if item.SelectedAt != "" {
			record["selected_at"] = item.SelectedAt
		}
		out = append(out, record)
	}
	return out
}

func wizardSummaryPayload(summary *room.WizardSummary) map[string]any {
	if summary == nil {
		return map[string]any{}
	}
	record := map[string]any{}
	if summary.Status != "" {
		record["status"] = summary.Status
	}
	if summary.Headline != "" {
		record["headline"] = summary.Headline
	}
	if summary.Detail != "" {
		record["detail"] = summary.Detail
	}
	if summary.CompletedStepCount >= 0 {
		record["completed_step_count"] = summary.CompletedStepCount
	}
	if summary.TotalStepCount >= 0 {
		record["total_step_count"] = summary.TotalStepCount
	}
	if summary.CompletedAt != "" {
		record["completed_at"] = summary.CompletedAt
	}
	if summary.LastCompletedStepID != "" {
		record["last_completed_step_id"] = summary.LastCompletedStepID
	}
	if summary.CurrentStepID != "" {
		record["current_step_id"] = summary.CurrentStepID
	}
	return record
}

func wizardSnapshotPayload(snapshot room.WizardSnapshot) map[string]any {
	payload := map[string]any{
		"wizard_id":         snapshot.WizardID,
		"title":             snapshot.Title,
		"description":       snapshot.Description,
		"steps":             wizardStepsPayload(snapshot.Steps),
		"current_step_id":   snapshot.CurrentStepID,
		"progress":          wizardProgressPayload(snapshot.Progress),
		"branch_selections": wizardBranchSelectionsPayload(snapshot.BranchSelections),
		"updated_at":        snapshot.UpdatedAt,
	}
	if snapshot.Summary != nil {
		payload["summary"] = wizardSummaryPayload(snapshot.Summary)
	}
	return payload
}

func fallbackWizardSteps(items, fallback []room.WizardStep) []room.WizardStep {
	if len(items) > 0 {
		return items
	}
	return fallback
}

func fallbackWizardProgress(items, fallback []room.WizardStepProgress) []room.WizardStepProgress {
	if len(items) > 0 {
		return items
	}
	return fallback
}

func fallbackWizardBranchSelections(items, fallback []room.WizardBranchSelection) []room.WizardBranchSelection {
	if len(items) > 0 {
		return items
	}
	return fallback
}

func fallbackWizardSummary(summary, fallback *room.WizardSummary) *room.WizardSummary {
	if summary != nil {
		return summary
	}
	return fallback
}

func persistedTitle(view *room.WizardStateView) string {
	if view == nil {
		return ""
	}
	return view.Title
}

func persistedDescription(view *room.WizardStateView) string {
	if view == nil {
		return ""
	}
	return view.Description
}

func fallbackString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func readBoolValue(raw any) bool {
	value, _ := raw.(bool)
	return value
}
