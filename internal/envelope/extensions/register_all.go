package extensions

import (
	"fmt"
	"path"

	"github.com/hollis-labs/tangent/internal/envelope"
)

// registration pairs a wire name with the package it ships in and the function
// that installs it, so the ordered list below can be read as the catalog it is.
type registration struct {
	name     string
	pkg      string
	register func(*envelope.Service) error
}

// registrations is the single ordered list of every Tangent-owned interaction
// definition. It exists so the server and the codegen dump tool cannot
// describe different registries: cmd/tangent and cmd/tangent-dump-types both
// go through RegisterAll, and adding a kind means adding one line here rather
// than editing two binaries.
//
// The package column is the ADR 0003 §6 ownership assignment made mechanical.
// It is not decoration: registerPackagedDefinition resolves the manifest
// through it, PackagedKinds projects it for the drift walker, and
// TestPackageTreeMatchesRegistrations compares it against the shipped tree, so
// a kind cannot be moved between packages in one place and not the other.
//
// Order is registration order at boot and is otherwise insignificant —
// Registry.All() sorts by name.
var registrations = []registration{
	{TriageEnvelopeType, "tangent.generic-candidate", RegisterTriage},
	{FeedbackEnvelopeType, "tangent.generic-candidate", RegisterFeedback},
	{FormCollectEnvelopeType, "tangent.generic-candidate", RegisterFormCollect},
	{DesignIterationEnvelopeType, "tangent.canvas", RegisterDesignIteration},
	{InterviewQuestionEnvelopeType, "tangent.generic-candidate", RegisterInterviewQuestion},
	{BlockDraftEnvelopeType, "tangent.writing", RegisterBlockDraft},
	{ProseRevisionEnvelopeType, "tangent.writing", RegisterProseRevision},
	{OutputRenderEnvelopeType, "tangent.generic-candidate", RegisterOutputRender},
	{WhiteboardEnvelopeType, "tangent.canvas", RegisterWhiteboard},
	{DashboardEnvelopeType, "tangent.canvas", RegisterDashboard},
	{FilePickerEnvelopeType, "tangent.workspace", RegisterFilePicker},
	{ProgressPanelEnvelopeType, "tangent.generic-candidate", RegisterProgressPanel},
	{WizardEnvelopeType, "tangent.compound", RegisterWizard},
	{DiffReviewEnvelopeType, "tangent.review", RegisterDiffReview},
	{SpreadsheetReviewEnvelopeType, "tangent.review", RegisterSpreadsheetReview},
	{ApprovalQueueEnvelopeType, "tangent.review", RegisterApprovalQueue},
	{SynthesisNotesEnvelopeType, "tangent.writing", RegisterSynthesisNotes},
	{HITLItemEnvelopeType, HITLPackageID, RegisterHITLItem},
}

// RegisterAll registers every Tangent-owned interaction definition on svc. It
// is the only entry point production code should use; the per-kind Register*
// functions stay exported for tests that deliberately boot a partial registry.
//
// Registration is not idempotent within a process: go-envelopes rejects a
// duplicate name, so calling RegisterAll twice on one service is an error,
// which is the correct behavior for a boot-time call site. The returned error
// already names the kind that failed.
//
// A kind whose manifest resolves but which this host cannot serve — an
// unsatisfiable host-version range, an ungranted capability — still registers,
// at its materialization state. `registered` never implies `available` (ADR
// 0003 §1), and the registry has to be able to list and explain a definition
// it will not serve. Refusing a *submission* against it is the interaction
// catalog's job.
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

// RegisteredTypes returns the wire names RegisterAll installs, in registration
// order. Callers must not mutate the returned slice's meaning; it is a fresh
// copy per call.
func RegisteredTypes() []string {
	names := make([]string, 0, len(registrations))
	for _, reg := range registrations {
		names = append(names, reg.name)
	}
	return names
}

// PackagedKinds returns each shipped kind's package directory path,
// "<package-id>/<kind-slug>", in registration order. Compared against
// ShippedPackagePaths to detect a manifest with no registration or a
// registration with no manifest.
func PackagedKinds() []string {
	paths := make([]string, 0, len(registrations))
	for _, reg := range registrations {
		paths = append(paths, path.Join(reg.pkg, kindSlug(reg.name)))
	}
	return paths
}

// packageIDFor resolves a wire name to the package that ships it.
func packageIDFor(kind string) (string, bool) {
	for _, reg := range registrations {
		if reg.name == kind {
			return reg.pkg, true
		}
	}
	return "", false
}

// mustPackageFile reads one file out of the shipped package tree at package
// initialization. It panics on failure because the tree is embedded in the
// binary: a missing file is a build that should never have linked, not a
// runtime condition any caller could handle.
func mustPackageFile(packageID, kind, fileName string) []byte {
	body, err := packagesFS.ReadFile(path.Join(packagesRoot, packageID, kindSlug(kind), fileName))
	if err != nil {
		panic(fmt.Sprintf("extensions: shipped package file is missing: %v", err))
	}
	return body
}
