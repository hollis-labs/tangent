// Package formcollect is the publisher-owned interaction package for
// tangent.form-collect@0.6.
//
// It is CW-20260825-0074's proof that the registry
// docs/adr/0003-definition-and-package-ownership.md §6 specifies is a package
// boundary and not only a manifest format. Everything this kind means — what a
// form's answers, templates, saved drafts, attachment refs, and submission
// summary are; how a request merges into persisted state; what a submitted
// response has to satisfy and what it turns into — lives here. Before this
// package, all of it lived in internal/room (591 lines) and internal/mcp
// (585 lines), reached from core through a wire-name switch.
//
// The kind is labeled `generic-catalog` by ADR 0003 §6, which is a
// destination and not a plan: it ships bundled and Tangent-published until
// go-envelopes retains source bytes for core kinds and a second host consumes
// one. §6's closing paragraph is why it is the kind under test — the
// generic-catalog candidates are the ones whose migration also exercises the
// upstream boundary.
//
// Nothing here imports internal/room or internal/mcp. State reaches the
// package as an opaque map[string]any through interactionpkg.StateStore, so
// the host stores publisher-owned JSON without interpreting a single key. The
// helpers duplicated from core — clone, read, normalize — are duplicated on
// purpose: sharing an unexported core helper would be the dependency this
// package exists to remove.
package formcollect

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/tangent/internal/interactionpkg"
)

// Kind is the wire name this package serves. It matches the `kind` its
// manifest declares at
// internal/envelope/extensions/packages/tangent.generic-candidate/form-collect/manifest.yaml.
const Kind = "tangent.form-collect"

// PhaseID is the phase-output key the package's state persists under. The
// value is the one internal/room/form_state.go used, so every room row written
// before this package is read back unchanged (ADR 0003 §8 C3).
const PhaseID = "form-collect"

// ProjectionKey is the JSON field name this package's state appears under in
// the tangent.session_get result. Frozen by ADR 0003 §8 C6.
const ProjectionKey = "form_collect"

// Blob keys. These are the publisher's own vocabulary; the host stores them
// without knowing any of them, which is the whole point of the boundary.
const (
	formIDKey            = "form_id"
	schemaKey            = "schema"
	intentKey            = "intent"
	answersKey           = "answers"
	notesKey             = "notes"
	updatedAtKey         = "updated_at"
	savedDraftsKey       = "saved_drafts"
	templatesKey         = "templates"
	actionsKey           = "actions"
	attachmentRefsKey    = "attachment_refs"
	submissionSummaryKey = "submission_summary"
	actionIDKey          = "action_id"

	idKey          = "id"
	labelKey       = "label"
	descriptionKey = "description"
	savedAtKey     = "saved_at"
	nameKey        = "name"
	artifactIDKey  = "artifact_id"
	uriKey         = "uri"
	mimeTypeKey    = "mime_type"
	kindKey        = "kind"
	sizeBytesKey   = "size_bytes"

	submittedAtKey     = "submitted_at"
	answerCountKey     = "answer_count"
	attachmentCountKey = "attachment_count"
	exportTextKey      = "export_text"
	exportNameKey      = "export_name"
)

// Typed publisher errors. They were room.ErrInvalidForm* before this package,
// and internal/mcp named all seven of them by hand inside a 40-arm errors.Is
// chain in order to decide that a refusal was `validation-failed`. Wrapping
// interactionpkg.ErrInvalidState is what lets core keep that mapping without
// knowing any of them.
var (
	ErrInvalidFormID        = fmt.Errorf("%w: form-collect: invalid form id", interactionpkg.ErrInvalidState)
	ErrInvalidSchema        = fmt.Errorf("%w: form-collect: invalid form schema", interactionpkg.ErrInvalidState)
	ErrInvalidAnswers       = fmt.Errorf("%w: form-collect: invalid form answers", interactionpkg.ErrInvalidState)
	ErrInvalidSavedDraft    = fmt.Errorf("%w: form-collect: invalid form saved draft", interactionpkg.ErrInvalidState)
	ErrInvalidTemplate      = fmt.Errorf("%w: form-collect: invalid form template", interactionpkg.ErrInvalidState)
	ErrInvalidAction        = fmt.Errorf("%w: form-collect: invalid form action", interactionpkg.ErrInvalidState)
	ErrInvalidAttachmentRef = fmt.Errorf("%w: form-collect: invalid form attachment ref", interactionpkg.ErrInvalidState)
)

// SavedDraft is one named, participant-saved set of answers.
type SavedDraft struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Answers map[string]any `json:"answers"`
	Notes   string         `json:"notes,omitempty"`
	SavedAt string         `json:"saved_at,omitempty"`
}

// Template is one caller-supplied answer preset.
type Template struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Answers map[string]any `json:"answers"`
	Notes   string         `json:"notes,omitempty"`
}

// Action is one submit affordance the form offers.
type Action struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// AttachmentRef points at content held outside the form payload.
type AttachmentRef struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	ArtifactID string `json:"artifact_id,omitempty"`
	URI        string `json:"uri,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
	Kind       string `json:"kind,omitempty"`
	SizeBytes  int    `json:"size_bytes,omitempty"`
}

// SubmissionSummary is what the package derives at submit time. Every field is
// computed here, from the submitted answers — none of it is host policy.
type SubmissionSummary struct {
	SubmittedAt     string `json:"submitted_at,omitempty"`
	ActionID        string `json:"action_id,omitempty"`
	AnswerCount     int    `json:"answer_count,omitempty"`
	AttachmentCount int    `json:"attachment_count,omitempty"`
	ExportText      string `json:"export_text,omitempty"`
	ExportName      string `json:"export_name,omitempty"`
}

// StateView is the package's session_get projection.
//
// The struct is field-for-field and tag-for-tag what room.FormStateView was.
// That is deliberate and load-bearing: tangent.session_get's advertised output
// schema is derived from this shape by reflection, and ADR 0003 §8 C6 freezes
// that tool's contract. Moving the type without moving the schema would have
// been a wire change dressed as a refactor.
type StateView struct {
	FormID            string             `json:"form_id"`
	Intent            string             `json:"intent,omitempty"`
	Schema            map[string]any     `json:"schema"`
	Answers           map[string]any     `json:"answers"`
	Notes             string             `json:"notes,omitempty"`
	UpdatedAt         string             `json:"updated_at,omitempty"`
	SavedDrafts       []SavedDraft       `json:"saved_drafts"`
	Templates         []Template         `json:"templates"`
	Actions           []Action           `json:"actions"`
	AttachmentRefs    []AttachmentRef    `json:"attachment_refs"`
	SubmissionSummary *SubmissionSummary `json:"submission_summary,omitempty"`
}

// Snapshot is the authored form of the state, before normalization.
type Snapshot struct {
	FormID            string
	Intent            string
	Schema            map[string]any
	Answers           map[string]any
	Notes             string
	UpdatedAt         string
	SavedDrafts       []SavedDraft
	Templates         []Template
	Actions           []Action
	AttachmentRefs    []AttachmentRef
	SubmissionSummary *SubmissionSummary
}

// Project renders a persisted blob as the session_get view. A blob with no
// form id is not a form: it projects as absent rather than as an empty form,
// which is what makes the `omitempty` on the host's side truthful.
func Project(blob map[string]any) *StateView {
	if len(blob) == 0 {
		return nil
	}
	formID := readString(blob, formIDKey)
	if formID == "" {
		return nil
	}
	view := &StateView{
		FormID:         formID,
		Intent:         readString(blob, intentKey),
		Schema:         readObjectMap(blob[schemaKey]),
		Answers:        readObjectMap(blob[answersKey]),
		Notes:          readString(blob, notesKey),
		UpdatedAt:      readString(blob, updatedAtKey),
		SavedDrafts:    readSavedDrafts(blob[savedDraftsKey]),
		Templates:      readTemplates(blob[templatesKey]),
		Actions:        readActions(blob[actionsKey]),
		AttachmentRefs: readAttachmentRefs(blob[attachmentRefsKey]),
	}
	if summary := readSubmissionSummary(blob[submissionSummaryKey]); summary != nil {
		view.SubmissionSummary = summary
	}
	return view
}

// Encode normalizes a snapshot and renders it as the persisted blob. It is the
// only writer of this package's state; a caller that wants to persist has to
// go through the validation here.
func Encode(snapshot Snapshot) (map[string]any, error) {
	normalized, err := normalize(snapshot)
	if err != nil {
		return nil, err
	}
	blob := map[string]any{
		formIDKey:         normalized.FormID,
		schemaKey:         cloneMap(normalized.Schema),
		answersKey:        cloneMap(normalized.Answers),
		savedDraftsKey:    savedDraftsAny(normalized.SavedDrafts),
		templatesKey:      templatesAny(normalized.Templates),
		actionsKey:        actionsAny(normalized.Actions),
		attachmentRefsKey: attachmentRefsAny(normalized.AttachmentRefs),
	}
	if normalized.Intent != "" {
		blob[intentKey] = normalized.Intent
	}
	if normalized.Notes != "" {
		blob[notesKey] = normalized.Notes
	}
	if normalized.UpdatedAt != "" {
		blob[updatedAtKey] = normalized.UpdatedAt
	}
	if normalized.SubmissionSummary != nil {
		blob[submissionSummaryKey] = submissionSummaryAny(normalized.SubmissionSummary)
	}
	return blob, nil
}

func normalize(snapshot Snapshot) (Snapshot, error) {
	formID := strings.TrimSpace(snapshot.FormID)
	if formID == "" {
		return Snapshot{}, ErrInvalidFormID
	}
	schema, err := normalizeMap(snapshot.Schema, ErrInvalidSchema)
	if err != nil {
		return Snapshot{}, err
	}
	answers, err := normalizeMap(snapshot.Answers, ErrInvalidAnswers)
	if err != nil {
		return Snapshot{}, err
	}
	savedDrafts, err := normalizeSavedDrafts(snapshot.SavedDrafts)
	if err != nil {
		return Snapshot{}, err
	}
	templates, err := normalizeTemplates(snapshot.Templates)
	if err != nil {
		return Snapshot{}, err
	}
	actions, err := normalizeActions(snapshot.Actions)
	if err != nil {
		return Snapshot{}, err
	}
	attachmentRefs, err := normalizeAttachmentRefs(snapshot.AttachmentRefs)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
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
		SubmissionSummary: normalizeSubmissionSummary(snapshot.SubmissionSummary),
	}, nil
}

func normalizeMap(input map[string]any, invalid error) (map[string]any, error) {
	if input == nil {
		return map[string]any{}, nil
	}
	normalized, err := normalizeJSON(cloneMap(input))
	if err != nil {
		return nil, fmt.Errorf("form-collect: normalize map: %w", err)
	}
	record, ok := normalized.(map[string]any)
	if !ok {
		return nil, invalid
	}
	return record, nil
}

func normalizeSavedDrafts(items []SavedDraft) ([]SavedDraft, error) {
	out := make([]SavedDraft, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		label := strings.TrimSpace(item.Label)
		if id == "" || label == "" {
			return nil, ErrInvalidSavedDraft
		}
		answers, err := normalizeMap(item.Answers, ErrInvalidAnswers)
		if err != nil {
			return nil, err
		}
		out = append(out, SavedDraft{
			ID:      id,
			Label:   label,
			Answers: answers,
			Notes:   item.Notes,
			SavedAt: strings.TrimSpace(item.SavedAt),
		})
	}
	return out, nil
}

func normalizeTemplates(items []Template) ([]Template, error) {
	out := make([]Template, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		label := strings.TrimSpace(item.Label)
		if id == "" || label == "" {
			return nil, ErrInvalidTemplate
		}
		answers, err := normalizeMap(item.Answers, ErrInvalidAnswers)
		if err != nil {
			return nil, err
		}
		out = append(out, Template{ID: id, Label: label, Answers: answers, Notes: item.Notes})
	}
	return out, nil
}

func normalizeActions(items []Action) ([]Action, error) {
	out := make([]Action, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return nil, ErrInvalidAction
		}
		label := strings.TrimSpace(item.Label)
		if label == "" {
			label = id
		}
		out = append(out, Action{ID: id, Label: label, Description: strings.TrimSpace(item.Description)})
	}
	return out, nil
}

func normalizeAttachmentRefs(items []AttachmentRef) ([]AttachmentRef, error) {
	out := make([]AttachmentRef, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return nil, ErrInvalidAttachmentRef
		}
		out = append(out, AttachmentRef{
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

func normalizeSubmissionSummary(summary *SubmissionSummary) *SubmissionSummary {
	if summary == nil {
		return nil
	}
	return &SubmissionSummary{
		SubmittedAt:     strings.TrimSpace(summary.SubmittedAt),
		ActionID:        strings.TrimSpace(summary.ActionID),
		AnswerCount:     max(summary.AnswerCount, 0),
		AttachmentCount: max(summary.AttachmentCount, 0),
		ExportText:      summary.ExportText,
		ExportName:      strings.TrimSpace(summary.ExportName),
	}
}

func savedDraftsAny(items []SavedDraft) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{idKey: item.ID, labelKey: item.Label, answersKey: cloneMap(item.Answers)}
		if item.Notes != "" {
			record[notesKey] = item.Notes
		}
		if item.SavedAt != "" {
			record[savedAtKey] = item.SavedAt
		}
		out = append(out, record)
	}
	return out
}

func templatesAny(items []Template) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{idKey: item.ID, labelKey: item.Label, answersKey: cloneMap(item.Answers)}
		if item.Notes != "" {
			record[notesKey] = item.Notes
		}
		out = append(out, record)
	}
	return out
}

func actionsAny(items []Action) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{idKey: item.ID, labelKey: item.Label}
		if item.Description != "" {
			record[descriptionKey] = item.Description
		}
		out = append(out, record)
	}
	return out
}

func attachmentRefsAny(items []AttachmentRef) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{nameKey: item.Name}
		if item.ID != "" {
			record[idKey] = item.ID
		}
		if item.ArtifactID != "" {
			record[artifactIDKey] = item.ArtifactID
		}
		if item.URI != "" {
			record[uriKey] = item.URI
		}
		if item.MIMEType != "" {
			record[mimeTypeKey] = item.MIMEType
		}
		if item.Kind != "" {
			record[kindKey] = item.Kind
		}
		if item.SizeBytes > 0 {
			record[sizeBytesKey] = item.SizeBytes
		}
		out = append(out, record)
	}
	return out
}

func submissionSummaryAny(summary *SubmissionSummary) any {
	if summary == nil {
		return nil
	}
	record := map[string]any{}
	if summary.SubmittedAt != "" {
		record[submittedAtKey] = summary.SubmittedAt
	}
	if summary.ActionID != "" {
		record[actionIDKey] = summary.ActionID
	}
	if summary.AnswerCount > 0 {
		record[answerCountKey] = summary.AnswerCount
	}
	if summary.AttachmentCount > 0 {
		record[attachmentCountKey] = summary.AttachmentCount
	}
	if summary.ExportText != "" {
		record[exportTextKey] = summary.ExportText
	}
	if summary.ExportName != "" {
		record[exportNameKey] = summary.ExportName
	}
	return record
}

func readSavedDrafts(raw any) []SavedDraft {
	records := readObjectSlice(raw)
	out := make([]SavedDraft, 0, len(records))
	for _, record := range records {
		id := strings.TrimSpace(readString(record, idKey))
		label := strings.TrimSpace(readString(record, labelKey))
		if id == "" || label == "" {
			continue
		}
		out = append(out, SavedDraft{
			ID:      id,
			Label:   label,
			Answers: readObjectMap(record[answersKey]),
			Notes:   readString(record, notesKey),
			SavedAt: readString(record, savedAtKey),
		})
	}
	return out
}

func readTemplates(raw any) []Template {
	records := readObjectSlice(raw)
	out := make([]Template, 0, len(records))
	for _, record := range records {
		id := strings.TrimSpace(readString(record, idKey))
		label := strings.TrimSpace(readString(record, labelKey))
		if id == "" || label == "" {
			continue
		}
		out = append(out, Template{
			ID:      id,
			Label:   label,
			Answers: readObjectMap(record[answersKey]),
			Notes:   readString(record, notesKey),
		})
	}
	return out
}

func readActions(raw any) []Action {
	records := readObjectSlice(raw)
	out := make([]Action, 0, len(records))
	for _, record := range records {
		id := strings.TrimSpace(readString(record, idKey))
		if id == "" {
			continue
		}
		label := strings.TrimSpace(readString(record, labelKey))
		if label == "" {
			label = id
		}
		out = append(out, Action{ID: id, Label: label, Description: readString(record, descriptionKey)})
	}
	return out
}

func readAttachmentRefs(raw any) []AttachmentRef {
	records := readObjectSlice(raw)
	out := make([]AttachmentRef, 0, len(records))
	for _, record := range records {
		name := strings.TrimSpace(readString(record, nameKey))
		if name == "" {
			continue
		}
		out = append(out, AttachmentRef{
			ID:         readString(record, idKey),
			Name:       name,
			ArtifactID: readString(record, artifactIDKey),
			URI:        readString(record, uriKey),
			MIMEType:   readString(record, mimeTypeKey),
			Kind:       readString(record, kindKey),
			SizeBytes:  int(readNumber(record, sizeBytesKey)),
		})
	}
	return out
}

func readSubmissionSummary(raw any) *SubmissionSummary {
	record, _ := raw.(map[string]any)
	if len(record) == 0 {
		return nil
	}
	return &SubmissionSummary{
		SubmittedAt:     readString(record, submittedAtKey),
		ActionID:        readString(record, actionIDKey),
		AnswerCount:     int(readNumber(record, answerCountKey)),
		AttachmentCount: int(readNumber(record, attachmentCountKey)),
		ExportText:      readString(record, exportTextKey),
		ExportName:      readString(record, exportNameKey),
	}
}

func readObjectSlice(raw any) []map[string]any {
	items, _ := raw.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if record, ok := item.(map[string]any); ok {
			out = append(out, record)
		}
	}
	return out
}

func readObjectMap(raw any) map[string]any {
	record, _ := raw.(map[string]any)
	if record == nil {
		return map[string]any{}
	}
	return cloneMap(record)
}

func readString(record map[string]any, key string) string {
	if record == nil {
		return ""
	}
	value, _ := record[key].(string)
	return value
}

func readNumber(record map[string]any, key string) float64 {
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

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func normalizeJSON(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
