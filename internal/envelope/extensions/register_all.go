package extensions

import (
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

// registration pairs a wire name with the function that installs it, so the
// ordered list below can be read as the catalog it is.
type registration struct {
	name     string
	register func(*envelope.Service) error
}

// registrations is the single ordered list of every Tangent-owned envelope
// kind. It exists so the server and the codegen dump tool cannot describe
// different registries: cmd/tangent and cmd/tangent-dump-types both go
// through RegisterAll, and adding a kind means adding one line here rather
// than editing two binaries.
//
// Order is registration order at boot and is otherwise insignificant —
// Registry.All() sorts by name.
var registrations = []registration{
	{TriageEnvelopeType, RegisterTriage},
	{FeedbackEnvelopeType, RegisterFeedback},
	{FormCollectEnvelopeType, RegisterFormCollect},
	{DesignIterationEnvelopeType, RegisterDesignIteration},
	{InterviewQuestionEnvelopeType, RegisterInterviewQuestion},
	{BlockDraftEnvelopeType, RegisterBlockDraft},
	{ProseRevisionEnvelopeType, RegisterProseRevision},
	{OutputRenderEnvelopeType, RegisterOutputRender},
	{WhiteboardEnvelopeType, RegisterWhiteboard},
	{DashboardEnvelopeType, RegisterDashboard},
	{FilePickerEnvelopeType, RegisterFilePicker},
	{ProgressPanelEnvelopeType, RegisterProgressPanel},
	{WizardEnvelopeType, RegisterWizard},
	{DiffReviewEnvelopeType, RegisterDiffReview},
	{SpreadsheetReviewEnvelopeType, RegisterSpreadsheetReview},
	{ApprovalQueueEnvelopeType, RegisterApprovalQueue},
	{SynthesisNotesEnvelopeType, RegisterSynthesisNotes},
	{HITLItemEnvelopeType, RegisterHITLItem},
}

// RegisterAll registers every Tangent-owned envelope kind on svc. It is the
// only entry point production code should use; the per-kind Register*
// functions stay exported for tests that deliberately boot a partial
// registry.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, so calling RegisterAll twice on one service is an error,
// which is the correct behavior for a boot-time call site. The returned
// error already names the kind that failed.
func RegisterAll(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	for _, reg := range registrations {
		if err := reg.register(svc); err != nil {
			return err
		}
	}
	return nil
}

// RegisteredTypes returns the wire names RegisterAll installs, in
// registration order. Callers must not mutate the returned slice's meaning;
// it is a fresh copy per call.
func RegisteredTypes() []string {
	names := make([]string, 0, len(registrations))
	for _, reg := range registrations {
		names = append(names, reg.name)
	}
	return names
}
