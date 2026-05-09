package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

type formCollectInput struct {
	Envelope envelopes.Envelope `json:"envelope"`
}

type formCollectSubmitDraft struct {
	FormID            string                      `json:"form_id"`
	Answers           map[string]any              `json:"answers"`
	Notes             string                      `json:"notes,omitempty"`
	ActionID          string                      `json:"action_id,omitempty"`
	SavedDrafts       []room.FormSavedDraft       `json:"saved_drafts,omitempty"`
	Templates         []room.FormTemplate         `json:"templates,omitempty"`
	AttachmentRefs    []room.FormAttachmentRef    `json:"attachment_refs,omitempty"`
	SubmissionSummary *room.FormSubmissionSummary `json:"submission_summary,omitempty"`
}

func (s *Server) handleFormCollect(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args formCollectInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != formCollectEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("tangent.form-collect rejects envelope type %q; want %q", args.Envelope.Type, formCollectEnvelopeType),
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
		s.logWorkflowRoomCreated("form-collect", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("form-collect", roomID, args.Envelope.ID)
	}

	phaseState, found, err := s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room form state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	snapshot := formSnapshotFromEnvelope(args.Envelope, room.ProjectFormState(phaseState))
	if _, saveErr := s.manager.SaveFormSnapshot(roomID, snapshot); saveErr != nil {
		return sessionPhaseStateError(roomID, saveErr), nil, nil
	}
	phaseState, found, err = s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("reload room form state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	return s.advanceRoomEnvelope(ctx, roomID, buildVisibleFormCollectEnvelope(&args.Envelope, room.ProjectFormState(phaseState)))
}

func formSnapshotFromEnvelope(env envelopes.Envelope, persisted *room.FormStateView) room.FormSnapshot {
	data := env.Data
	formID := readStringValue(data, "form_id")
	if formID == "" && persisted != nil {
		formID = persisted.FormID
	}
	reusePersisted := persisted != nil && persisted.FormID != "" && persisted.FormID == formID

	schema := readObjectValue(data, "schema")
	if len(schema) == 0 && persisted != nil {
		schema = persisted.Schema
	}
	answers := readObjectValue(data, "answers")
	if reusePersisted && len(answers) == 0 {
		answers = persisted.Answers
	}
	notes := readStringValue(data, "notes")
	if reusePersisted && notes == "" {
		notes = persisted.Notes
	}

	snapshot := room.FormSnapshot{
		FormID:    formID,
		Intent:    readStringValue(data, "intent"),
		Schema:    schema,
		Answers:   answers,
		Notes:     notes,
		UpdatedAt: nowRFC3339(),
		Actions:   readFormActionsValue(data["actions"]),
	}
	if len(snapshot.Actions) == 0 && persisted != nil {
		snapshot.Actions = persisted.Actions
	}
	snapshot.SavedDrafts = readFormSavedDraftsValue(data["saved_drafts"], nil)
	snapshot.Templates = readFormTemplatesValue(data["templates"], nil)
	snapshot.AttachmentRefs = readFormAttachmentRefsValue(data["attachment_refs"], nil)
	if summary := readFormSubmissionSummaryValue(data["submission_summary"]); summary != nil {
		snapshot.SubmissionSummary = summary
	}
	if reusePersisted {
		if len(snapshot.SavedDrafts) == 0 {
			snapshot.SavedDrafts = persisted.SavedDrafts
		}
		if len(snapshot.Templates) == 0 {
			snapshot.Templates = persisted.Templates
		}
		if len(snapshot.AttachmentRefs) == 0 {
			snapshot.AttachmentRefs = persisted.AttachmentRefs
		}
		if snapshot.SubmissionSummary == nil {
			snapshot.SubmissionSummary = persisted.SubmissionSummary
		}
	}
	return snapshot
}

func buildVisibleFormCollectEnvelope(env *envelopes.Envelope, view *room.FormStateView) *envelopes.Envelope {
	clone := cloneEnvelopeForDispatch(env)
	if clone.Data == nil {
		clone.Data = map[string]any{}
	}
	if view == nil {
		return clone
	}
	data := cloneAnyMapForDispatch(clone.Data)
	data["form_id"] = view.FormID
	data["schema"] = cloneAnyMapForDispatch(view.Schema)
	data["answers"] = cloneAnyMapForDispatch(view.Answers)
	data["saved_drafts"] = formSavedDraftsPayload(view.SavedDrafts)
	data["templates"] = formTemplatesPayload(view.Templates)
	data["actions"] = formActionsPayload(view.Actions)
	data["attachment_refs"] = formAttachmentRefsPayload(view.AttachmentRefs)
	if view.Intent != "" {
		data["intent"] = view.Intent
	}
	if view.Notes != "" {
		data["notes"] = view.Notes
	}
	if view.UpdatedAt != "" {
		data["updated_at"] = view.UpdatedAt
	}
	if view.SubmissionSummary != nil {
		data["submission_summary"] = formSubmissionSummaryPayload(view.SubmissionSummary)
	}
	clone.Data = data
	return clone
}

func (s *Server) normalizeFormCollectSubmitResponse(
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
	if resp.Status != envelopes.ResponseStatusSubmitted {
		return nil, fmt.Errorf("%w: status must be %q", envelopes.ErrSchemaValidation, envelopes.ResponseStatusSubmitted)
	}

	phaseState, found, err := s.manager.GetPhaseState(context.Background(), roomID)
	if err != nil {
		return nil, fmt.Errorf("form-collect submit: load room state: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", room.ErrRoomNotFound, roomID)
	}
	persisted := room.ProjectFormState(phaseState)
	if persisted == nil {
		return nil, fmt.Errorf("%w: room has no persisted form state", envelopes.ErrSchemaValidation)
	}

	draft, err := decodeFormCollectSubmitDraft(resp.Payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(draft.FormID) == "" {
		return nil, fmt.Errorf("%w: payload.form_id is required", envelopes.ErrSchemaValidation)
	}
	if draft.FormID != persisted.FormID {
		return nil, fmt.Errorf("%w: payload.form_id %q does not match room form_id %q", envelopes.ErrSchemaValidation, draft.FormID, persisted.FormID)
	}
	if draft.Answers == nil {
		draft.Answers = map[string]any{}
	}

	summary := draft.SubmissionSummary
	if summary == nil {
		summary = &room.FormSubmissionSummary{}
	}
	summary.SubmittedAt = nowRFC3339()
	summary.ActionID = strings.TrimSpace(draft.ActionID)
	summary.AnswerCount = countFormAnswers(draft.Answers)
	summary.AttachmentCount = len(draft.AttachmentRefs)
	exportText, exportName := buildFormCollectExport(draft.FormID, draft.Answers, draft.Notes, draft.ActionID, draft.AttachmentRefs)
	summary.ExportText = exportText
	summary.ExportName = exportName

	snapshot := room.FormSnapshot{
		FormID:            persisted.FormID,
		Intent:            persisted.Intent,
		Schema:            persisted.Schema,
		Answers:           draft.Answers,
		Notes:             draft.Notes,
		UpdatedAt:         summary.SubmittedAt,
		SavedDrafts:       fallbackFormSavedDrafts(draft.SavedDrafts, persisted.SavedDrafts),
		Templates:         fallbackFormTemplates(draft.Templates, persisted.Templates),
		Actions:           persisted.Actions,
		AttachmentRefs:    fallbackFormAttachmentRefs(draft.AttachmentRefs, persisted.AttachmentRefs),
		SubmissionSummary: summary,
	}
	if _, err := s.manager.SaveFormSnapshot(roomID, snapshot); err != nil {
		return nil, err
	}

	return &envelopes.Response{
		V:          envelopes.ProtocolVersion,
		EnvelopeID: resp.EnvelopeID,
		Kind:       envelopes.ResponseKindData,
		Status:     envelopes.ResponseStatusSubmitted,
		Payload: map[string]any{
			"form_id":         snapshot.FormID,
			"answers":         cloneAnyMapForDispatch(snapshot.Answers),
			"notes":           snapshot.Notes,
			"action_id":       summary.ActionID,
			"attachment_refs": formAttachmentRefsPayload(snapshot.AttachmentRefs),
		},
		CompletedAt: summary.SubmittedAt,
	}, nil
}

func decodeFormCollectSubmitDraft(raw any) (formCollectSubmitDraft, error) {
	data, ok := raw.(map[string]any)
	if !ok {
		return formCollectSubmitDraft{}, fmt.Errorf("%w: payload must be an object", envelopes.ErrSchemaValidation)
	}
	return formCollectSubmitDraft{
		FormID:            readStringValue(data, "form_id"),
		Answers:           readObjectValue(data, "answers"),
		Notes:             readStringValue(data, "notes"),
		ActionID:          readStringValue(data, "action_id"),
		SavedDrafts:       readFormSavedDraftsValue(data["saved_drafts"], nil),
		Templates:         readFormTemplatesValue(data["templates"], nil),
		AttachmentRefs:    readFormAttachmentRefsValue(data["attachment_refs"], nil),
		SubmissionSummary: readFormSubmissionSummaryValue(data["submission_summary"]),
	}, nil
}

func readFormSavedDraftsValue(raw any, fallback []room.FormSavedDraft) []room.FormSavedDraft {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return fallback
	}
	out := make([]room.FormSavedDraft, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := strings.TrimSpace(readStringValue(record, "id"))
		label := strings.TrimSpace(readStringValue(record, "label"))
		if id == "" || label == "" {
			continue
		}
		out = append(out, room.FormSavedDraft{
			ID:      id,
			Label:   label,
			Answers: readObjectValue(record, "answers"),
			Notes:   readStringValue(record, "notes"),
			SavedAt: readStringValue(record, "saved_at"),
		})
	}
	return out
}

func readFormTemplatesValue(raw any, fallback []room.FormTemplate) []room.FormTemplate {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return fallback
	}
	out := make([]room.FormTemplate, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := strings.TrimSpace(readStringValue(record, "id"))
		label := strings.TrimSpace(readStringValue(record, "label"))
		if id == "" || label == "" {
			continue
		}
		out = append(out, room.FormTemplate{
			ID:      id,
			Label:   label,
			Answers: readObjectValue(record, "answers"),
			Notes:   readStringValue(record, "notes"),
		})
	}
	return out
}

func readFormActionsValue(raw any) []room.FormAction {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return []room.FormAction{}
	}
	out := make([]room.FormAction, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := strings.TrimSpace(readStringValue(record, "id"))
		if id == "" {
			continue
		}
		label := strings.TrimSpace(readStringValue(record, "label"))
		if label == "" {
			label = id
		}
		out = append(out, room.FormAction{
			ID:          id,
			Label:       label,
			Description: readStringValue(record, "description"),
		})
	}
	return out
}

func readFormAttachmentRefsValue(raw any, fallback []room.FormAttachmentRef) []room.FormAttachmentRef {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return fallback
	}
	out := make([]room.FormAttachmentRef, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(readStringValue(record, "name"))
		if name == "" {
			continue
		}
		out = append(out, room.FormAttachmentRef{
			ID:         readStringValue(record, "id"),
			Name:       name,
			ArtifactID: readStringValue(record, "artifact_id"),
			URI:        readStringValue(record, "uri"),
			MIMEType:   readStringValue(record, "mime_type"),
			Kind:       readStringValue(record, "kind"),
			SizeBytes:  int(readNumberValue(record, "size_bytes")),
		})
	}
	return out
}

func readFormSubmissionSummaryValue(raw any) *room.FormSubmissionSummary {
	record, _ := raw.(map[string]any)
	if len(record) == 0 {
		return nil
	}
	return &room.FormSubmissionSummary{
		SubmittedAt:     readStringValue(record, "submitted_at"),
		ActionID:        readStringValue(record, "action_id"),
		AnswerCount:     int(readNumberValue(record, "answer_count")),
		AttachmentCount: int(readNumberValue(record, "attachment_count")),
		ExportText:      readStringValue(record, "export_text"),
		ExportName:      readStringValue(record, "export_name"),
	}
}

func countFormAnswers(value any) int {
	switch typed := value.(type) {
	case map[string]any:
		total := 0
		for _, child := range typed {
			total += countFormAnswers(child)
		}
		if len(typed) == 0 {
			return 0
		}
		return max(total, len(typed))
	case []any:
		total := 0
		for _, child := range typed {
			total += countFormAnswers(child)
		}
		return max(total, len(typed))
	case []string:
		return len(typed)
	case string:
		if strings.TrimSpace(typed) == "" {
			return 0
		}
		return 1
	case nil:
		return 0
	default:
		return 1
	}
}

func buildFormCollectExport(
	formID string,
	answers map[string]any,
	notes string,
	actionID string,
	attachmentRefs []room.FormAttachmentRef,
) (string, string) {
	payload := map[string]any{
		"form_id":         formID,
		"answers":         cloneAnyMapForDispatch(answers),
		"notes":           notes,
		"action_id":       strings.TrimSpace(actionID),
		"attachment_refs": formAttachmentRefsPayload(attachmentRefs),
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", ""
	}
	name := strings.TrimSpace(formID)
	if name == "" {
		name = "form"
	}
	return string(raw), name + "-submission.json"
}

func fallbackFormSavedDrafts(next, persisted []room.FormSavedDraft) []room.FormSavedDraft {
	if len(next) > 0 {
		return next
	}
	return persisted
}

func fallbackFormTemplates(next, persisted []room.FormTemplate) []room.FormTemplate {
	if len(next) > 0 {
		return next
	}
	return persisted
}

func fallbackFormAttachmentRefs(next, persisted []room.FormAttachmentRef) []room.FormAttachmentRef {
	if len(next) > 0 {
		return next
	}
	return persisted
}

func formSavedDraftsPayload(items []room.FormSavedDraft) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"id":      item.ID,
			"label":   item.Label,
			"answers": cloneAnyMapForDispatch(item.Answers),
		}
		if item.Notes != "" {
			record["notes"] = item.Notes
		}
		if item.SavedAt != "" {
			record["saved_at"] = item.SavedAt
		}
		out = append(out, record)
	}
	return out
}

func formTemplatesPayload(items []room.FormTemplate) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"id":      item.ID,
			"label":   item.Label,
			"answers": cloneAnyMapForDispatch(item.Answers),
		}
		if item.Notes != "" {
			record["notes"] = item.Notes
		}
		out = append(out, record)
	}
	return out
}

func formActionsPayload(items []room.FormAction) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"id":    item.ID,
			"label": item.Label,
		}
		if item.Description != "" {
			record["description"] = item.Description
		}
		out = append(out, record)
	}
	return out
}

func formAttachmentRefsPayload(items []room.FormAttachmentRef) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"name": item.Name,
		}
		if item.ID != "" {
			record["id"] = item.ID
		}
		if item.ArtifactID != "" {
			record["artifact_id"] = item.ArtifactID
		}
		if item.URI != "" {
			record["uri"] = item.URI
		}
		if item.MIMEType != "" {
			record["mime_type"] = item.MIMEType
		}
		if item.Kind != "" {
			record["kind"] = item.Kind
		}
		if item.SizeBytes > 0 {
			record["size_bytes"] = item.SizeBytes
		}
		out = append(out, record)
	}
	return out
}

func formSubmissionSummaryPayload(summary *room.FormSubmissionSummary) any {
	if summary == nil {
		return nil
	}
	record := map[string]any{}
	if summary.SubmittedAt != "" {
		record["submitted_at"] = summary.SubmittedAt
	}
	if summary.ActionID != "" {
		record["action_id"] = summary.ActionID
	}
	if summary.AnswerCount > 0 {
		record["answer_count"] = summary.AnswerCount
	}
	if summary.AttachmentCount > 0 {
		record["attachment_count"] = summary.AttachmentCount
	}
	if summary.ExportText != "" {
		record["export_text"] = summary.ExportText
	}
	if summary.ExportName != "" {
		record["export_name"] = summary.ExportName
	}
	return record
}
