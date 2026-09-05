package formcollect_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/interactionpkg"
	"github.com/hollis-labs/tangent/internal/packages/formcollect"
)

// memoryStore is the whole host surface this package needs. That it is eleven
// lines is the boundary claim made concrete: the package's behavior is
// testable without a room, a database, an MCP server, or an envelope registry,
// because none of those are things it depends on.
type memoryStore struct {
	blobs map[string]map[string]any
	saves int
	fail  error
}

func newStore() *memoryStore { return &memoryStore{blobs: map[string]map[string]any{}} }

func (m *memoryStore) LoadState(_ context.Context, roomID, phaseID string) (map[string]any, bool, error) {
	blob, ok := m.blobs[roomID+"|"+phaseID]
	return blob, ok, nil
}

func (m *memoryStore) SaveState(_ context.Context, roomID, phaseID string, data map[string]any) error {
	if m.fail != nil {
		return m.fail
	}
	m.saves++
	// Round-trip through JSON, because the real store persists into a TEXT
	// column. A package that only works against a Go map it just built would
	// pass a naive fake and fail in production.
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		return err
	}
	m.blobs[roomID+"|"+phaseID] = stored
	return nil
}

const fixedNow = "2026-05-09T20:40:00Z"

func fixedClock() time.Time {
	parsed, _ := time.Parse(time.RFC3339, fixedNow)
	return parsed
}

func newPackage() *formcollect.Package {
	return formcollect.NewWithClock(fixedClock)
}

// TestPackageDescribesItsBinding is the registration half of acceptance 4: the
// descriptor is what the registry keys on and what session_get projects under.
func TestPackageDescribesItsBinding(t *testing.T) {
	t.Parallel()
	got := newPackage().Describe()
	want := interactionpkg.Descriptor{
		Kind:          "tangent.form-collect",
		StatePhaseID:  "form-collect",
		ProjectionKey: "form_collect",
	}
	if got != want {
		t.Fatalf("Describe() = %+v, want %+v", got, want)
	}
}

// TestStateRoundTripsThroughAnOpaqueBlob is the port of the room-level
// persistence test this package replaced. The assertions are unchanged; what
// changed is that the state now round-trips through a map the host never
// inspects rather than through a typed struct the host declared.
func TestStateRoundTripsThroughAnOpaqueBlob(t *testing.T) {
	t.Parallel()
	blob, err := formcollect.Encode(formcollect.Snapshot{
		FormID: "intake-form",
		Intent: "  Collect launch inputs  ",
		Schema: map[string]any{"fields": []any{
			map[string]any{"id": "name", "type": "text", "label": "Name"},
		}},
		Answers: map[string]any{
			"name":      "Alpha",
			"attendees": []any{map[string]any{"name": "Beta"}},
		},
		Notes:     "keep this note",
		UpdatedAt: fixedNow,
		SavedDrafts: []formcollect.SavedDraft{{
			ID: "draft-1", Label: "Morning draft",
			Answers: map[string]any{"name": "Draft Alpha"},
			Notes:   "saved note", SavedAt: "2026-05-09T20:30:00Z",
		}},
		Templates: []formcollect.Template{{
			ID: "template-1", Label: "Default template",
			Answers: map[string]any{"name": "Template Alpha"}, Notes: "template note",
		}},
		Actions: []formcollect.Action{{
			ID: "submit-review", Label: "Submit for review", Description: "route to review queue",
		}},
		AttachmentRefs: []formcollect.AttachmentRef{{
			ID: "attachment-1", Name: "brief.pdf", ArtifactID: "artifact-1",
			URI: "artifact://artifact-1", MIMEType: "application/pdf",
			Kind: "brief", SizeBytes: 4096,
		}},
		SubmissionSummary: &formcollect.SubmissionSummary{
			SubmittedAt: fixedNow, ActionID: "submit-review", AnswerCount: 2,
		},
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Persisting means JSON, so the projection has to survive the trip.
	raw, err := json.Marshal(blob)
	if err != nil {
		t.Fatalf("marshal blob: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("unmarshal blob: %v", err)
	}

	view := formcollect.Project(stored)
	if view == nil {
		t.Fatal("Project returned nil for a blob with a form id")
	}
	if view.FormID != "intake-form" {
		t.Errorf("form_id = %q, want intake-form", view.FormID)
	}
	if view.Intent != "Collect launch inputs" {
		t.Errorf("intent = %q, want the trimmed value", view.Intent)
	}
	if len(view.Templates) != 1 || view.Templates[0].Label != "Default template" {
		t.Errorf("templates = %+v, want the one authored template", view.Templates)
	}
	if len(view.AttachmentRefs) != 1 || view.AttachmentRefs[0].URI != "artifact://artifact-1" {
		t.Errorf("attachment_refs = %+v, want the one authored ref", view.AttachmentRefs)
	}
	if view.SubmissionSummary == nil || view.SubmissionSummary.AnswerCount != 2 {
		t.Errorf("submission_summary = %+v, want answer_count 2", view.SubmissionSummary)
	}
}

// TestEncodeRefusesInvalidState ports the room-level validation test. Every
// sentinel now wraps interactionpkg.ErrInvalidState, which is what lets the
// host map a package refusal onto `validation-failed` without naming any of
// them — the seven-arm errors.Is chain this replaced.
func TestEncodeRefusesInvalidState(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		snapshot formcollect.Snapshot
		want     error
	}{
		{"missing form id", formcollect.Snapshot{}, formcollect.ErrInvalidFormID},
		{
			"saved draft without an id",
			formcollect.Snapshot{FormID: "form-1", SavedDrafts: []formcollect.SavedDraft{
				{Label: "Missing ID", Answers: map[string]any{}},
			}},
			formcollect.ErrInvalidSavedDraft,
		},
		{
			"template without a label",
			formcollect.Snapshot{FormID: "form-1", Templates: []formcollect.Template{
				{ID: "template-1", Answers: map[string]any{}},
			}},
			formcollect.ErrInvalidTemplate,
		},
		{
			"action without an id",
			formcollect.Snapshot{FormID: "form-1", Actions: []formcollect.Action{{Label: "Missing ID"}}},
			formcollect.ErrInvalidAction,
		},
		{
			"attachment ref without a name",
			formcollect.Snapshot{FormID: "form-1", AttachmentRefs: []formcollect.AttachmentRef{
				{ID: "attachment-1"},
			}},
			formcollect.ErrInvalidAttachmentRef,
		},
		// An attachment is participant-typed metadata, never bytes this host
		// fetched. A URI that is not `artifact://` is a request for a
		// host-mediated effect (ADR 0003 §2.5), and an effect goes through
		// internal/effect with a declared capability and a receipt — not
		// through a text field a person pasted into a form.
		{
			"attachment ref naming a remote origin",
			formcollect.Snapshot{FormID: "form-1", AttachmentRefs: []formcollect.AttachmentRef{
				{ID: "attachment-1", Name: "spec.pdf", URI: "https://files.example.test/spec.pdf"},
			}},
			formcollect.ErrInvalidAttachmentRef,
		},
		{
			"attachment ref naming a local file",
			formcollect.Snapshot{FormID: "form-1", AttachmentRefs: []formcollect.AttachmentRef{
				{ID: "attachment-1", Name: "spec.pdf", URI: "file:///etc/passwd"},
			}},
			formcollect.ErrInvalidAttachmentRef,
		},
		{
			"attachment ref carrying an inline payload",
			formcollect.Snapshot{FormID: "form-1", AttachmentRefs: []formcollect.AttachmentRef{
				{ID: "attachment-1", Name: "spec.pdf", URI: "data:application/pdf;base64,AAA"},
			}},
			formcollect.ErrInvalidAttachmentRef,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := formcollect.Encode(testCase.snapshot)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("Encode err = %v, want %v", err, testCase.want)
			}
			if !errors.Is(err, interactionpkg.ErrInvalidState) {
				t.Fatalf("Encode err = %v, want it to wrap interactionpkg.ErrInvalidState", err)
			}
		})
	}

	if _, err := formcollect.Encode(formcollect.Snapshot{
		FormID: "form-1", Schema: map[string]any{"invalid": make(chan int)},
	}); err == nil {
		t.Fatal("Encode with an unserializable schema err = nil, want error")
	}
}

// TestPresentRequestMergesOverPersistedState holds the publisher's merge rule:
// a caller that omits a field keeps what the room holds, but only while the
// form identity is unchanged.
func TestPresentRequestMergesOverPersistedState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newStore()
	pkg := newPackage()

	first := &envelopes.Envelope{
		V: envelopes.ProtocolVersion, ID: "env-1", Type: formcollect.Kind,
		Data: map[string]any{
			"form_id": "form-1",
			"schema":  map[string]any{"fields": []any{map[string]any{"field_id": "scope"}}},
			"answers": map[string]any{"scope": "everything"},
			"notes":   "first pass",
		},
	}
	if _, err := pkg.PresentRequest(ctx, store, "room-1", first); err != nil {
		t.Fatalf("PresentRequest: %v", err)
	}

	// A second turn that names the same form and says nothing else must see
	// the room's answers, not an empty form.
	second := &envelopes.Envelope{
		V: envelopes.ProtocolVersion, ID: "env-2", Type: formcollect.Kind,
		Data: map[string]any{"form_id": "form-1"},
	}
	presented, err := pkg.PresentRequest(ctx, store, "room-1", second)
	if err != nil {
		t.Fatalf("PresentRequest second turn: %v", err)
	}
	answers, _ := presented.Data["answers"].(map[string]any)
	if answers["scope"] != "everything" {
		t.Errorf("presented answers = %v, want the persisted answers carried forward", presented.Data["answers"])
	}
	if presented.Data["notes"] != "first pass" {
		t.Errorf("presented notes = %v, want the persisted note carried forward", presented.Data["notes"])
	}

	// A different form in the same room starts clean rather than inheriting.
	third := &envelopes.Envelope{
		V: envelopes.ProtocolVersion, ID: "env-3", Type: formcollect.Kind,
		Data: map[string]any{"form_id": "form-2", "schema": map[string]any{}},
	}
	presented, err = pkg.PresentRequest(ctx, store, "room-1", third)
	if err != nil {
		t.Fatalf("PresentRequest third turn: %v", err)
	}
	if answers, _ := presented.Data["answers"].(map[string]any); len(answers) != 0 {
		t.Errorf("presented answers for a new form = %v, want empty", answers)
	}

	// The request envelope itself is never mutated: what the caller sent is
	// the immutable request snapshot ADR 0001 pins.
	if _, mutated := third.Data["answers"]; mutated {
		t.Error("PresentRequest mutated the caller's request envelope")
	}
}

// TestNormalizeResponseDerivesTheSubmissionSummary is the resolution half of
// acceptance 4, and pins the exact response payload the pre-package handler
// produced.
func TestNormalizeResponseDerivesTheSubmissionSummary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newStore()
	pkg := newPackage()

	request := &envelopes.Envelope{
		V: envelopes.ProtocolVersion, ID: "env-1", Type: formcollect.Kind,
		Data: map[string]any{
			"form_id": "form-1",
			"schema":  map[string]any{"fields": []any{map[string]any{"field_id": "scope"}}},
		},
	}
	if _, err := pkg.PresentRequest(ctx, store, "room-1", request); err != nil {
		t.Fatalf("PresentRequest: %v", err)
	}

	normalized, err := pkg.NormalizeResponse(ctx, store, "room-1", request, &envelopes.Response{
		V: envelopes.ProtocolVersion, EnvelopeID: "env-1",
		Kind: envelopes.ResponseKindData, Status: envelopes.ResponseStatusSubmitted,
		Payload: map[string]any{
			"form_id":   "form-1",
			"action_id": "submit",
			"answers":   map[string]any{"scope": "everything"},
			"notes":     "looks right",
		},
	})
	if err != nil {
		t.Fatalf("NormalizeResponse: %v", err)
	}

	payload, ok := normalized.Payload.(map[string]any)
	if !ok {
		t.Fatalf("normalized payload is %T, want map[string]any", normalized.Payload)
	}
	for key, want := range map[string]any{
		"form_id": "form-1", "notes": "looks right", "action_id": "submit",
	} {
		if payload[key] != want {
			t.Errorf("payload[%q] = %v, want %v", key, payload[key], want)
		}
	}
	if answers, _ := payload["answers"].(map[string]any); answers["scope"] != "everything" {
		t.Errorf("payload answers = %v, want the submitted answers", payload["answers"])
	}
	if refs, _ := payload["attachment_refs"].([]any); refs == nil || len(refs) != 0 {
		t.Errorf("payload attachment_refs = %v, want an empty array", payload["attachment_refs"])
	}
	if normalized.CompletedAt != fixedNow {
		t.Errorf("completedAt = %q, want %q", normalized.CompletedAt, fixedNow)
	}

	// The derived summary is the package's own business meaning and is
	// persisted, not returned.
	view := formcollect.Project(store.blobs["room-1|form-collect"])
	if view == nil || view.SubmissionSummary == nil {
		t.Fatal("submit did not persist a submission summary")
	}
	if view.SubmissionSummary.AnswerCount != 1 {
		t.Errorf("answer_count = %d, want 1", view.SubmissionSummary.AnswerCount)
	}
	if view.SubmissionSummary.ExportName != "form-1-submission.json" {
		t.Errorf("export_name = %q, want form-1-submission.json", view.SubmissionSummary.ExportName)
	}
	if view.SubmissionSummary.ExportText == "" {
		t.Error("export_text is empty; the package derives it at submit time")
	}
}

// TestNormalizeResponseRefusesAMismatchedForm is the one validation a schema
// cannot express, and the reason the package owns response interpretation
// rather than the schema owning all of it.
func TestNormalizeResponseRefusesAMismatchedForm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newStore()
	pkg := newPackage()

	request := &envelopes.Envelope{
		V: envelopes.ProtocolVersion, ID: "env-1", Type: formcollect.Kind,
		Data: map[string]any{"form_id": "form-1", "schema": map[string]any{}},
	}
	if _, err := pkg.PresentRequest(ctx, store, "room-1", request); err != nil {
		t.Fatalf("PresentRequest: %v", err)
	}

	_, err := pkg.NormalizeResponse(ctx, store, "room-1", request, &envelopes.Response{
		V: envelopes.ProtocolVersion, EnvelopeID: "env-1",
		Kind: envelopes.ResponseKindData, Status: envelopes.ResponseStatusSubmitted,
		Payload: map[string]any{"form_id": "form-2", "answers": map[string]any{}},
	})
	if !errors.Is(err, envelopes.ErrSchemaValidation) {
		t.Fatalf("NormalizeResponse err = %v, want ErrSchemaValidation", err)
	}
	if saves := store.saves; saves != 1 {
		t.Errorf("store saw %d saves; a refused submission must not persist", saves)
	}
}

// TestProjectStateOmitsAnEmptyRoom keeps the host's `omitempty` truthful: a
// typed nil pointer returned through an `any` would be a non-nil interface and
// would start emitting `"form_collect": null` into session_get.
func TestProjectStateOmitsAnEmptyRoom(t *testing.T) {
	t.Parallel()
	if projection := newPackage().ProjectState(nil); projection != nil {
		t.Fatalf("ProjectState(nil) = %#v, want an untyped nil", projection)
	}
	if projection := newPackage().ProjectState(map[string]any{"notes": "no form id"}); projection != nil {
		t.Fatalf("ProjectState without a form id = %#v, want an untyped nil", projection)
	}
}
