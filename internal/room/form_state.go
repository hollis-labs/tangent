package room

import (
	"fmt"
	"strings"
)

const (
	FormCollectPhaseID                 = "form-collect"
	formCollectFormIDKey               = "form_id"
	formCollectSchemaKey               = "schema"
	formCollectIntentKey               = "intent"
	formCollectAnswersKey              = "answers"
	formCollectNotesKey                = "notes"
	formCollectUpdatedAtKey            = "updated_at"
	formCollectSavedDraftsKey          = "saved_drafts"
	formCollectTemplatesKey            = "templates"
	formCollectActionsKey              = "actions"
	formCollectAttachmentRefsKey       = "attachment_refs"
	formCollectSubmissionSummaryKey    = "submission_summary"
	formCollectSavedDraftIDKey         = "id"
	formCollectSavedDraftLabelKey      = "label"
	formCollectSavedDraftSavedAtKey    = "saved_at"
	formCollectSavedDraftAnswersKey    = "answers"
	formCollectSavedDraftNotesKey      = "notes"
	formCollectTemplateIDKey           = "id"
	formCollectTemplateLabelKey        = "label"
	formCollectTemplateAnswersKey      = "answers"
	formCollectTemplateNotesKey        = "notes"
	formCollectActionIDKey             = "id"
	formCollectActionLabelKey          = "label"
	formCollectActionDescriptionKey    = "description"
	formCollectAttachmentRefIDKey      = "id"
	formCollectAttachmentRefNameKey    = "name"
	formCollectAttachmentRefArtifactID = "artifact_id"
	formCollectAttachmentRefURIKey     = "uri"
	formCollectAttachmentRefMIMEKey    = "mime_type"
	formCollectAttachmentRefKindKey    = "kind"
	formCollectAttachmentRefSizeKey    = "size_bytes"
	formCollectSummarySubmittedAtKey   = "submitted_at"
	formCollectSummaryActionIDKey      = "action_id"
	formCollectSummaryAnswerCountKey   = "answer_count"
	formCollectSummaryAttachCountKey   = "attachment_count"
	formCollectSummaryExportTextKey    = "export_text"
	formCollectSummaryExportNameKey    = "export_name"
)

type FormSavedDraft struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Answers map[string]any `json:"answers"`
	Notes   string         `json:"notes,omitempty"`
	SavedAt string         `json:"saved_at,omitempty"`
}

type FormTemplate struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Answers map[string]any `json:"answers"`
	Notes   string         `json:"notes,omitempty"`
}

type FormAction struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type FormAttachmentRef struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	ArtifactID string `json:"artifact_id,omitempty"`
	URI        string `json:"uri,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
	Kind       string `json:"kind,omitempty"`
	SizeBytes  int    `json:"size_bytes,omitempty"`
}

type FormSubmissionSummary struct {
	SubmittedAt     string `json:"submitted_at,omitempty"`
	ActionID        string `json:"action_id,omitempty"`
	AnswerCount     int    `json:"answer_count,omitempty"`
	AttachmentCount int    `json:"attachment_count,omitempty"`
	ExportText      string `json:"export_text,omitempty"`
	ExportName      string `json:"export_name,omitempty"`
}

type FormStateView struct {
	FormID            string                 `json:"form_id"`
	Intent            string                 `json:"intent,omitempty"`
	Schema            map[string]any         `json:"schema"`
	Answers           map[string]any         `json:"answers"`
	Notes             string                 `json:"notes,omitempty"`
	UpdatedAt         string                 `json:"updated_at,omitempty"`
	SavedDrafts       []FormSavedDraft       `json:"saved_drafts"`
	Templates         []FormTemplate         `json:"templates"`
	Actions           []FormAction           `json:"actions"`
	AttachmentRefs    []FormAttachmentRef    `json:"attachment_refs"`
	SubmissionSummary *FormSubmissionSummary `json:"submission_summary,omitempty"`
}

type FormSnapshot struct {
	FormID            string
	Intent            string
	Schema            map[string]any
	Answers           map[string]any
	Notes             string
	UpdatedAt         string
	SavedDrafts       []FormSavedDraft
	Templates         []FormTemplate
	Actions           []FormAction
	AttachmentRefs    []FormAttachmentRef
	SubmissionSummary *FormSubmissionSummary
}

func (r *Room) SaveFormSnapshot(snapshot FormSnapshot) error {
	normalized, err := normalizeFormSnapshot(snapshot)
	if err != nil {
		return err
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	nextOutputs[FormCollectPhaseID] = formCollectBlobFromSnapshot(normalized)
	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (m *Manager) SaveFormSnapshot(roomID string, snapshot FormSnapshot) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.SaveFormSnapshot(snapshot); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func ProjectFormState(state PhaseState) *FormStateView {
	return projectFormStateFromBlob(state.PhaseOutputs[FormCollectPhaseID])
}

func projectFormStateFromBlob(blob PhaseOutput) *FormStateView {
	if len(blob.Data) == 0 {
		return nil
	}
	formID := readString(blob.Data, formCollectFormIDKey)
	if formID == "" {
		return nil
	}
	view := &FormStateView{
		FormID:         formID,
		Intent:         readString(blob.Data, formCollectIntentKey),
		Schema:         readObjectValueMap(blob.Data[formCollectSchemaKey]),
		Answers:        readObjectValueMap(blob.Data[formCollectAnswersKey]),
		Notes:          readString(blob.Data, formCollectNotesKey),
		UpdatedAt:      readString(blob.Data, formCollectUpdatedAtKey),
		SavedDrafts:    readFormSavedDrafts(blob.Data[formCollectSavedDraftsKey]),
		Templates:      readFormTemplates(blob.Data[formCollectTemplatesKey]),
		Actions:        readFormActions(blob.Data[formCollectActionsKey]),
		AttachmentRefs: readFormAttachmentRefs(blob.Data[formCollectAttachmentRefsKey]),
	}
	if summary := readFormSubmissionSummary(blob.Data[formCollectSubmissionSummaryKey]); summary != nil {
		view.SubmissionSummary = summary
	}
	return view
}

func normalizeFormSnapshot(snapshot FormSnapshot) (FormSnapshot, error) {
	formID := strings.TrimSpace(snapshot.FormID)
	if formID == "" {
		return FormSnapshot{}, ErrInvalidFormID
	}
	schema, err := normalizeFormMap(snapshot.Schema, ErrInvalidFormSchema)
	if err != nil {
		return FormSnapshot{}, err
	}
	answers, err := normalizeFormMap(snapshot.Answers, ErrInvalidFormAnswers)
	if err != nil {
		return FormSnapshot{}, err
	}
	savedDrafts, err := normalizeFormSavedDrafts(snapshot.SavedDrafts)
	if err != nil {
		return FormSnapshot{}, err
	}
	templates, err := normalizeFormTemplates(snapshot.Templates)
	if err != nil {
		return FormSnapshot{}, err
	}
	actions, err := normalizeFormActions(snapshot.Actions)
	if err != nil {
		return FormSnapshot{}, err
	}
	attachmentRefs, err := normalizeFormAttachmentRefs(snapshot.AttachmentRefs)
	if err != nil {
		return FormSnapshot{}, err
	}
	return FormSnapshot{
		FormID:            formID,
		Intent:            strings.TrimSpace(snapshot.Intent),
		Schema:            schema,
		Answers:           answers,
		Notes:             snapshot.Notes,
		UpdatedAt:         strings.TrimSpace(snapshot.UpdatedAt),
		SavedDrafts:       savedDrafts,
		Templates:         templates,
		Actions:           actions,
		AttachmentRefs:    attachmentRefs,
		SubmissionSummary: normalizeFormSubmissionSummary(snapshot.SubmissionSummary),
	}, nil
}

func normalizeFormMap(input map[string]any, invalid error) (map[string]any, error) {
	if input == nil {
		return map[string]any{}, nil
	}
	normalized, err := normalizeJSONValue(cloneAnyMap(input))
	if err != nil {
		return nil, fmt.Errorf("room: normalize form map: %w", err)
	}
	record, ok := normalized.(map[string]any)
	if !ok {
		return nil, invalid
	}
	return record, nil
}

func normalizeFormSavedDrafts(items []FormSavedDraft) ([]FormSavedDraft, error) {
	out := make([]FormSavedDraft, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		label := strings.TrimSpace(item.Label)
		if id == "" || label == "" {
			return nil, ErrInvalidFormSavedDraft
		}
		answers, err := normalizeFormMap(item.Answers, ErrInvalidFormAnswers)
		if err != nil {
			return nil, err
		}
		out = append(out, FormSavedDraft{
			ID:      id,
			Label:   label,
			Answers: answers,
			Notes:   item.Notes,
			SavedAt: strings.TrimSpace(item.SavedAt),
		})
	}
	return out, nil
}

func normalizeFormTemplates(items []FormTemplate) ([]FormTemplate, error) {
	out := make([]FormTemplate, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		label := strings.TrimSpace(item.Label)
		if id == "" || label == "" {
			return nil, ErrInvalidFormTemplate
		}
		answers, err := normalizeFormMap(item.Answers, ErrInvalidFormAnswers)
		if err != nil {
			return nil, err
		}
		out = append(out, FormTemplate{
			ID:      id,
			Label:   label,
			Answers: answers,
			Notes:   item.Notes,
		})
	}
	return out, nil
}

func normalizeFormActions(items []FormAction) ([]FormAction, error) {
	out := make([]FormAction, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return nil, ErrInvalidFormAction
		}
		label := strings.TrimSpace(item.Label)
		if label == "" {
			label = id
		}
		out = append(out, FormAction{
			ID:          id,
			Label:       label,
			Description: strings.TrimSpace(item.Description),
		})
	}
	return out, nil
}

func normalizeFormAttachmentRefs(items []FormAttachmentRef) ([]FormAttachmentRef, error) {
	out := make([]FormAttachmentRef, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return nil, ErrInvalidFormAttachmentRef
		}
		out = append(out, FormAttachmentRef{
			ID:         strings.TrimSpace(item.ID),
			Name:       name,
			ArtifactID: strings.TrimSpace(item.ArtifactID),
			URI:        strings.TrimSpace(item.URI),
			MIMEType:   strings.TrimSpace(item.MIMEType),
			Kind:       strings.TrimSpace(item.Kind),
			SizeBytes:  item.SizeBytes,
		})
	}
	return out, nil
}

func normalizeFormSubmissionSummary(summary *FormSubmissionSummary) *FormSubmissionSummary {
	if summary == nil {
		return nil
	}
	return &FormSubmissionSummary{
		SubmittedAt:     strings.TrimSpace(summary.SubmittedAt),
		ActionID:        strings.TrimSpace(summary.ActionID),
		AnswerCount:     max(summary.AnswerCount, 0),
		AttachmentCount: max(summary.AttachmentCount, 0),
		ExportText:      summary.ExportText,
		ExportName:      strings.TrimSpace(summary.ExportName),
	}
}

func formCollectBlobFromSnapshot(snapshot FormSnapshot) PhaseOutput {
	data := map[string]any{
		formCollectFormIDKey:         snapshot.FormID,
		formCollectSchemaKey:         cloneAnyMap(snapshot.Schema),
		formCollectAnswersKey:        cloneAnyMap(snapshot.Answers),
		formCollectSavedDraftsKey:    formSavedDraftsAny(snapshot.SavedDrafts),
		formCollectTemplatesKey:      formTemplatesAny(snapshot.Templates),
		formCollectActionsKey:        formActionsAny(snapshot.Actions),
		formCollectAttachmentRefsKey: formAttachmentRefsAny(snapshot.AttachmentRefs),
	}
	if snapshot.Intent != "" {
		data[formCollectIntentKey] = snapshot.Intent
	}
	if snapshot.Notes != "" {
		data[formCollectNotesKey] = snapshot.Notes
	}
	if snapshot.UpdatedAt != "" {
		data[formCollectUpdatedAtKey] = snapshot.UpdatedAt
	}
	if snapshot.SubmissionSummary != nil {
		data[formCollectSubmissionSummaryKey] = formSubmissionSummaryAny(snapshot.SubmissionSummary)
	}
	return PhaseOutput{
		Version: phaseOutputVersion,
		Data:    data,
	}
}

func formSavedDraftsAny(items []FormSavedDraft) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			formCollectSavedDraftIDKey:      item.ID,
			formCollectSavedDraftLabelKey:   item.Label,
			formCollectSavedDraftAnswersKey: cloneAnyMap(item.Answers),
		}
		if item.Notes != "" {
			record[formCollectSavedDraftNotesKey] = item.Notes
		}
		if item.SavedAt != "" {
			record[formCollectSavedDraftSavedAtKey] = item.SavedAt
		}
		out = append(out, record)
	}
	return out
}

func formTemplatesAny(items []FormTemplate) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			formCollectTemplateIDKey:      item.ID,
			formCollectTemplateLabelKey:   item.Label,
			formCollectTemplateAnswersKey: cloneAnyMap(item.Answers),
		}
		if item.Notes != "" {
			record[formCollectTemplateNotesKey] = item.Notes
		}
		out = append(out, record)
	}
	return out
}

func formActionsAny(items []FormAction) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			formCollectActionIDKey:    item.ID,
			formCollectActionLabelKey: item.Label,
		}
		if item.Description != "" {
			record[formCollectActionDescriptionKey] = item.Description
		}
		out = append(out, record)
	}
	return out
}

func formAttachmentRefsAny(items []FormAttachmentRef) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			formCollectAttachmentRefNameKey: item.Name,
		}
		if item.ID != "" {
			record[formCollectAttachmentRefIDKey] = item.ID
		}
		if item.ArtifactID != "" {
			record[formCollectAttachmentRefArtifactID] = item.ArtifactID
		}
		if item.URI != "" {
			record[formCollectAttachmentRefURIKey] = item.URI
		}
		if item.MIMEType != "" {
			record[formCollectAttachmentRefMIMEKey] = item.MIMEType
		}
		if item.Kind != "" {
			record[formCollectAttachmentRefKindKey] = item.Kind
		}
		if item.SizeBytes > 0 {
			record[formCollectAttachmentRefSizeKey] = item.SizeBytes
		}
		out = append(out, record)
	}
	return out
}

func formSubmissionSummaryAny(summary *FormSubmissionSummary) any {
	if summary == nil {
		return nil
	}
	record := map[string]any{}
	if summary.SubmittedAt != "" {
		record[formCollectSummarySubmittedAtKey] = summary.SubmittedAt
	}
	if summary.ActionID != "" {
		record[formCollectSummaryActionIDKey] = summary.ActionID
	}
	if summary.AnswerCount > 0 {
		record[formCollectSummaryAnswerCountKey] = summary.AnswerCount
	}
	if summary.AttachmentCount > 0 {
		record[formCollectSummaryAttachCountKey] = summary.AttachmentCount
	}
	if summary.ExportText != "" {
		record[formCollectSummaryExportTextKey] = summary.ExportText
	}
	if summary.ExportName != "" {
		record[formCollectSummaryExportNameKey] = summary.ExportName
	}
	return record
}

func readObjectValueMap(raw any) map[string]any {
	record, _ := raw.(map[string]any)
	if record == nil {
		return map[string]any{}
	}
	return cloneAnyMap(record)
}

func readFormSavedDrafts(raw any) []FormSavedDraft {
	records := readObjectSlice(raw)
	out := make([]FormSavedDraft, 0, len(records))
	for _, record := range records {
		id := strings.TrimSpace(readString(record, formCollectSavedDraftIDKey))
		label := strings.TrimSpace(readString(record, formCollectSavedDraftLabelKey))
		if id == "" || label == "" {
			continue
		}
		out = append(out, FormSavedDraft{
			ID:      id,
			Label:   label,
			Answers: readObjectValueMap(record[formCollectSavedDraftAnswersKey]),
			Notes:   readString(record, formCollectSavedDraftNotesKey),
			SavedAt: readString(record, formCollectSavedDraftSavedAtKey),
		})
	}
	return out
}

func readFormTemplates(raw any) []FormTemplate {
	records := readObjectSlice(raw)
	out := make([]FormTemplate, 0, len(records))
	for _, record := range records {
		id := strings.TrimSpace(readString(record, formCollectTemplateIDKey))
		label := strings.TrimSpace(readString(record, formCollectTemplateLabelKey))
		if id == "" || label == "" {
			continue
		}
		out = append(out, FormTemplate{
			ID:      id,
			Label:   label,
			Answers: readObjectValueMap(record[formCollectTemplateAnswersKey]),
			Notes:   readString(record, formCollectTemplateNotesKey),
		})
	}
	return out
}

func readFormActions(raw any) []FormAction {
	records := readObjectSlice(raw)
	out := make([]FormAction, 0, len(records))
	for _, record := range records {
		id := strings.TrimSpace(readString(record, formCollectActionIDKey))
		if id == "" {
			continue
		}
		label := strings.TrimSpace(readString(record, formCollectActionLabelKey))
		if label == "" {
			label = id
		}
		out = append(out, FormAction{
			ID:          id,
			Label:       label,
			Description: readString(record, formCollectActionDescriptionKey),
		})
	}
	return out
}

func readFormAttachmentRefs(raw any) []FormAttachmentRef {
	records := readObjectSlice(raw)
	out := make([]FormAttachmentRef, 0, len(records))
	for _, record := range records {
		name := strings.TrimSpace(readString(record, formCollectAttachmentRefNameKey))
		if name == "" {
			continue
		}
		out = append(out, FormAttachmentRef{
			ID:         readString(record, formCollectAttachmentRefIDKey),
			Name:       name,
			ArtifactID: readString(record, formCollectAttachmentRefArtifactID),
			URI:        readString(record, formCollectAttachmentRefURIKey),
			MIMEType:   readString(record, formCollectAttachmentRefMIMEKey),
			Kind:       readString(record, formCollectAttachmentRefKindKey),
			SizeBytes:  int(readNumberFromRecord(record, formCollectAttachmentRefSizeKey)),
		})
	}
	return out
}

func readFormSubmissionSummary(raw any) *FormSubmissionSummary {
	record, _ := raw.(map[string]any)
	if len(record) == 0 {
		return nil
	}
	return &FormSubmissionSummary{
		SubmittedAt:     readString(record, formCollectSummarySubmittedAtKey),
		ActionID:        readString(record, formCollectSummaryActionIDKey),
		AnswerCount:     int(readNumberFromRecord(record, formCollectSummaryAnswerCountKey)),
		AttachmentCount: int(readNumberFromRecord(record, formCollectSummaryAttachCountKey)),
		ExportText:      readString(record, formCollectSummaryExportTextKey),
		ExportName:      readString(record, formCollectSummaryExportNameKey),
	}
}

func readNumberFromRecord(record map[string]any, key string) float64 {
	if record == nil {
		return 0
	}
	switch value := record[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int32:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}
