package formcollect

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	envelopes "github.com/hollis-labs/libs/ui-go/envelopes"

	"github.com/hollis-labs/tangent/internal/interactionpkg"
)

// Package is the tangent.form-collect runtime binding.
//
// It carries no state of its own: everything durable lives in the room blob
// reached through interactionpkg.StateStore, and everything else is derived
// per call. A zero value is usable, which is what lets New be a one-liner and
// lets a test install a second instance without coordination.
type Package struct {
	// now is injectable so submitted_at and updated_at are deterministic under
	// test. Nil means time.Now().UTC().
	now func() time.Time
}

// New returns the shipped package.
func New() *Package { return &Package{} }

// NewWithClock returns a package with an injected clock, for tests that assert
// the derived submission summary.
func NewWithClock(now func() time.Time) *Package { return &Package{now: now} }

// Describe implements interactionpkg.Package.
func (p *Package) Describe() interactionpkg.Descriptor {
	return interactionpkg.Descriptor{
		Kind:          Kind,
		StatePhaseID:  PhaseID,
		ProjectionKey: ProjectionKey,
	}
}

// ProjectState implements interactionpkg.Package. The `any` return is what
// keeps the host from naming StateView; returning a typed nil pointer would
// make the host's omitempty check see a non-nil interface, so the nil case is
// converted explicitly.
func (p *Package) ProjectState(state map[string]any) any {
	view := Project(state)
	if view == nil {
		return nil
	}
	return view
}

// ProjectionSchema implements interactionpkg.Package.
//
// The schema is reflected from StateView with the same reflector the MCP SDK
// uses on the tool's Go return type, so composing it into session_get's
// advertised output schema reproduces byte-for-byte what the SDK produced when
// room.FormStateView was a field on that struct. That equality is asserted by
// TestSessionGetOutputSchemaUnchangedByPackaging.
func (p *Package) ProjectionSchema() any {
	schema, err := jsonschema.ForType(reflect.TypeFor[*StateView](), &jsonschema.ForOptions{})
	if err != nil {
		// ForType on a fixed local struct cannot fail at runtime; contributing
		// no property is still the fail-closed answer if it somehow does,
		// because a wrong schema would reject valid production output.
		return nil
	}
	return schema
}

// PresentRequest implements interactionpkg.Package.
//
// One turn of form-collect is: merge what the caller sent over what the room
// already holds, persist the merge, and present the merged state back. The
// merge rule is the publisher's, not the host's — a caller that omits a field
// keeps what the room has for that field, but only while the form identity is
// unchanged, so re-using a room for a different form starts clean rather than
// inheriting the previous form's answers.
func (p *Package) PresentRequest(
	ctx context.Context,
	store interactionpkg.StateStore,
	roomID string,
	env *envelopes.Envelope,
) (*envelopes.Envelope, error) {
	if env == nil {
		return nil, fmt.Errorf("%w: envelope is required", interactionpkg.ErrInvalidState)
	}
	blob, _, err := store.LoadState(ctx, roomID, PhaseID)
	if err != nil {
		return nil, err
	}

	snapshot := p.snapshotFromEnvelope(*env, Project(blob))
	next, err := Encode(snapshot)
	if err != nil {
		return nil, err
	}
	if saveErr := store.SaveState(ctx, roomID, PhaseID, next); saveErr != nil {
		return nil, saveErr
	}

	// Re-read rather than projecting the just-encoded blob, so what the
	// participant sees is what the room actually holds — the same round trip
	// the pre-package handler made through the room manager.
	stored, _, err := store.LoadState(ctx, roomID, PhaseID)
	if err != nil {
		return nil, err
	}
	return buildVisibleEnvelope(env, Project(stored)), nil
}

// NormalizeResponse implements interactionpkg.Package.
//
// This is the response contract the manifest's response_schema now describes.
// The schema states the shape; this states the two things a schema cannot —
// that the submitted form_id must be the one the room is holding, and what the
// derived submission summary is.
func (p *Package) NormalizeResponse(
	ctx context.Context,
	store interactionpkg.StateStore,
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

	blob, found, err := store.LoadState(ctx, roomID, PhaseID)
	if err != nil {
		return nil, fmt.Errorf("form-collect submit: load room state: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("%w: room %s has no form state", interactionpkg.ErrPackageUnavailable, roomID)
	}
	persisted := Project(blob)
	if persisted == nil {
		return nil, fmt.Errorf("%w: room has no persisted form state", envelopes.ErrSchemaValidation)
	}

	draft, err := decodeSubmitDraft(resp.Payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(draft.FormID) == "" {
		return nil, fmt.Errorf("%w: payload.form_id is required", envelopes.ErrSchemaValidation)
	}
	if draft.FormID != persisted.FormID {
		return nil, fmt.Errorf(
			"%w: payload.form_id %q does not match room form_id %q",
			envelopes.ErrSchemaValidation, draft.FormID, persisted.FormID)
	}
	if draft.Answers == nil {
		draft.Answers = map[string]any{}
	}

	summary := draft.SubmissionSummary
	if summary == nil {
		summary = &SubmissionSummary{}
	}
	summary.SubmittedAt = p.timestamp()
	summary.ActionID = strings.TrimSpace(draft.ActionID)
	summary.AnswerCount = countAnswers(draft.Answers)
	summary.AttachmentCount = len(draft.AttachmentRefs)
	summary.ExportText, summary.ExportName = buildExport(
		draft.FormID, draft.Answers, draft.Notes, draft.ActionID, draft.AttachmentRefs)

	snapshot := Snapshot{
		FormID:            persisted.FormID,
		Intent:            persisted.Intent,
		Schema:            persisted.Schema,
		Answers:           draft.Answers,
		Notes:             draft.Notes,
		UpdatedAt:         summary.SubmittedAt,
		SavedDrafts:       orSavedDrafts(draft.SavedDrafts, persisted.SavedDrafts),
		Templates:         orTemplates(draft.Templates, persisted.Templates),
		Actions:           persisted.Actions,
		AttachmentRefs:    orAttachmentRefs(draft.AttachmentRefs, persisted.AttachmentRefs),
		SubmissionSummary: summary,
	}
	next, err := Encode(snapshot)
	if err != nil {
		return nil, err
	}
	if err := store.SaveState(ctx, roomID, PhaseID, next); err != nil {
		return nil, err
	}

	return &envelopes.Response{
		V:          envelopes.ProtocolVersion,
		EnvelopeID: resp.EnvelopeID,
		Kind:       envelopes.ResponseKindData,
		Status:     envelopes.ResponseStatusSubmitted,
		Payload: map[string]any{
			formIDKey:         snapshot.FormID,
			answersKey:        cloneMap(snapshot.Answers),
			notesKey:          snapshot.Notes,
			actionIDKey:       summary.ActionID,
			attachmentRefsKey: attachmentRefsAny(snapshot.AttachmentRefs),
		},
		CompletedAt: summary.SubmittedAt,
	}, nil
}

func (p *Package) timestamp() string {
	if p.now != nil {
		return p.now().UTC().Format(time.RFC3339)
	}
	return time.Now().UTC().Format(time.RFC3339)
}

// snapshotFromEnvelope is the merge rule: request wins where it says
// something, room state fills the rest, and the fill only applies while the
// form identity is unchanged.
func (p *Package) snapshotFromEnvelope(env envelopes.Envelope, persisted *StateView) Snapshot {
	data := env.Data
	formID := readString(data, formIDKey)
	if formID == "" && persisted != nil {
		formID = persisted.FormID
	}
	reusePersisted := persisted != nil && persisted.FormID != "" && persisted.FormID == formID

	schema := readObjectMap(data[schemaKey])
	if len(schema) == 0 && persisted != nil {
		schema = persisted.Schema
	}
	answers := readObjectMap(data[answersKey])
	if reusePersisted && len(answers) == 0 {
		answers = persisted.Answers
	}
	notes := readString(data, notesKey)
	if reusePersisted && notes == "" {
		notes = persisted.Notes
	}

	snapshot := Snapshot{
		FormID:    formID,
		Intent:    readString(data, intentKey),
		Schema:    schema,
		Answers:   answers,
		Notes:     notes,
		UpdatedAt: p.timestamp(),
		Actions:   readActions(data[actionsKey]),
	}
	if len(snapshot.Actions) == 0 && persisted != nil {
		snapshot.Actions = persisted.Actions
	}
	snapshot.SavedDrafts = orSavedDrafts(readSavedDraftsIfPresent(data[savedDraftsKey]), nil)
	snapshot.Templates = orTemplates(readTemplatesIfPresent(data[templatesKey]), nil)
	snapshot.AttachmentRefs = orAttachmentRefs(readAttachmentRefsIfPresent(data[attachmentRefsKey]), nil)
	if summary := readSubmissionSummary(data[submissionSummaryKey]); summary != nil {
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

// buildVisibleEnvelope hydrates the request envelope with the room's canonical
// state, so a participant joining mid-form sees what the room holds rather
// than what the latest caller happened to send.
func buildVisibleEnvelope(env *envelopes.Envelope, view *StateView) *envelopes.Envelope {
	clone := cloneEnvelope(env)
	if clone.Data == nil {
		clone.Data = map[string]any{}
	}
	if view == nil {
		return clone
	}
	data := cloneMap(clone.Data)
	data[formIDKey] = view.FormID
	data[schemaKey] = cloneMap(view.Schema)
	data[answersKey] = cloneMap(view.Answers)
	data[savedDraftsKey] = savedDraftsAny(view.SavedDrafts)
	data[templatesKey] = templatesAny(view.Templates)
	data[actionsKey] = actionsAny(view.Actions)
	data[attachmentRefsKey] = attachmentRefsAny(view.AttachmentRefs)
	if view.Intent != "" {
		data[intentKey] = view.Intent
	}
	if view.Notes != "" {
		data[notesKey] = view.Notes
	}
	if view.UpdatedAt != "" {
		data[updatedAtKey] = view.UpdatedAt
	}
	if view.SubmissionSummary != nil {
		data[submissionSummaryKey] = submissionSummaryAny(view.SubmissionSummary)
	}
	clone.Data = data
	return clone
}

// submitDraft is the participant's submitted payload, before the package
// derives anything from it.
type submitDraft struct {
	FormID            string
	Answers           map[string]any
	Notes             string
	ActionID          string
	SavedDrafts       []SavedDraft
	Templates         []Template
	AttachmentRefs    []AttachmentRef
	SubmissionSummary *SubmissionSummary
}

func decodeSubmitDraft(raw any) (submitDraft, error) {
	data, ok := raw.(map[string]any)
	if !ok {
		return submitDraft{}, fmt.Errorf("%w: payload must be an object", envelopes.ErrSchemaValidation)
	}
	return submitDraft{
		FormID:            readString(data, formIDKey),
		Answers:           readObjectMap(data[answersKey]),
		Notes:             readString(data, notesKey),
		ActionID:          readString(data, actionIDKey),
		SavedDrafts:       readSavedDraftsIfPresent(data[savedDraftsKey]),
		Templates:         readTemplatesIfPresent(data[templatesKey]),
		AttachmentRefs:    readAttachmentRefsIfPresent(data[attachmentRefsKey]),
		SubmissionSummary: readSubmissionSummary(data[submissionSummaryKey]),
	}, nil
}

// The three IfPresent readers return nil — not an empty slice — for an absent
// list, so "the caller said nothing" stays distinguishable from "the caller
// said none", which is what the carry-forward rule keys on.
func readSavedDraftsIfPresent(raw any) []SavedDraft {
	if items, _ := raw.([]any); len(items) == 0 {
		return nil
	}
	return readSavedDrafts(raw)
}

func readTemplatesIfPresent(raw any) []Template {
	if items, _ := raw.([]any); len(items) == 0 {
		return nil
	}
	return readTemplates(raw)
}

func readAttachmentRefsIfPresent(raw any) []AttachmentRef {
	if items, _ := raw.([]any); len(items) == 0 {
		return nil
	}
	return readAttachmentRefs(raw)
}

func orSavedDrafts(next, fallback []SavedDraft) []SavedDraft {
	if len(next) > 0 {
		return next
	}
	return fallback
}

func orTemplates(next, fallback []Template) []Template {
	if len(next) > 0 {
		return next
	}
	return fallback
}

func orAttachmentRefs(next, fallback []AttachmentRef) []AttachmentRef {
	if len(next) > 0 {
		return next
	}
	return fallback
}

// countAnswers is the publisher's definition of "how many questions were
// answered" — a recursive count that treats a blank string as unanswered and
// any other scalar as one answer. It is business meaning, not host policy,
// which is exactly why ADR 0003 §5 puts it here.
func countAnswers(value any) int {
	switch typed := value.(type) {
	case map[string]any:
		total := 0
		for _, child := range typed {
			total += countAnswers(child)
		}
		if len(typed) == 0 {
			return 0
		}
		return max(total, len(typed))
	case []any:
		total := 0
		for _, child := range typed {
			total += countAnswers(child)
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

// buildExport renders the submission as the downloadable artifact the renderer
// offers. The shape is the publisher's; the host never reads it.
func buildExport(
	formID string,
	answers map[string]any,
	notes string,
	actionID string,
	attachmentRefs []AttachmentRef,
) (string, string) {
	payload := map[string]any{
		formIDKey:         formID,
		answersKey:        cloneMap(answers),
		notesKey:          notes,
		actionIDKey:       strings.TrimSpace(actionID),
		attachmentRefsKey: attachmentRefsAny(attachmentRefs),
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

func cloneEnvelope(env *envelopes.Envelope) *envelopes.Envelope {
	if env == nil {
		return nil
	}
	return &envelopes.Envelope{
		V:            env.V,
		ID:           env.ID,
		Type:         env.Type,
		TypeVersion:  env.TypeVersion,
		Title:        env.Title,
		Context:      env.Context,
		Presentation: env.Presentation,
		Data:         cloneMap(env.Data),
		Trace:        env.Trace,
		Meta:         cloneMap(env.Meta),
	}
}
