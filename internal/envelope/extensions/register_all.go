package extensions

import (
	"errors"
	"fmt"
	"path"

	"github.com/hollis-labs/tangent/internal/envelope"
)

// ErrNotContributable reports that a kind cannot be installed through the
// ADR 0007 §4 plugin door — either this host ships no manifest for it, or it is
// a host-package kind that RegisterAll owns.
var ErrNotContributable = errors.New("extensions: kind is not contributable by a plugin")

// registration pairs a wire name with the package it ships in and the function
// that installs it, so the ordered list below can be read as the catalog it is.
type registration struct {
	name     string
	pkg      string
	register func(*envelope.Service) error
	// contributedByPlugin marks a kind that arrives through the ADR 0007 §4
	// plugin path instead of being installed directly by RegisterAll.
	//
	// It changes which door the kind comes through and nothing else. The row
	// still appears in RegisteredTypes and PackagedKinds, so
	// TestPackageTreeMatchesRegistrations covers both doors — ADR 0007 §4 is
	// explicit that if the drift tests only walked one of them, the ownership
	// guarantee ADR 0003 §6 makes mechanical would silently narrow to half the
	// registry.
	//
	// NO ROW SETS IT TODAY, and that is a correction rather than a gap
	// (CW-20260911-0036). `tangent.app-board` set it from the day the plugin
	// host landed, and it should not have: it is a domain-free board shape
	// published by `tangent`, owned by `host-package`, rendered from a
	// component compiled into `ui_dist` with the release. It is plumbing the
	// host owns, and the plugin that "contributed" it was a registration
	// statement wearing the plugin contract — no dependency to isolate, no
	// independent distribution, no independent versioning, no domain knowledge
	// to keep out of core.
	//
	// The door stays, on the same terms as the host surfaces pluginhost leaves
	// unimplemented: a contributable kind is one this host does not own, and
	// none has arrived yet. What keeps the door honest with no shipped user is
	// contributedDoorFixture in register_all_test.go — a fixture table that
	// exercises every branch of RegisterContributedKind against a real
	// manifest. Deleting that fixture, or letting this field decay because
	// nothing sets it, is the narrowing ADR 0003 §6 names.
	contributedByPlugin bool
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
	{TriageEnvelopeType, "tangent.generic-candidate", RegisterTriage, false},
	{FeedbackEnvelopeType, "tangent.generic-candidate", RegisterFeedback, false},
	{FormCollectEnvelopeType, "tangent.generic-candidate", RegisterFormCollect, false},
	{DesignIterationEnvelopeType, "tangent.canvas", RegisterDesignIteration, false},
	{InterviewQuestionEnvelopeType, "tangent.generic-candidate", RegisterInterviewQuestion, false},
	{BlockDraftEnvelopeType, "tangent.writing", RegisterBlockDraft, false},
	{ProseRevisionEnvelopeType, "tangent.writing", RegisterProseRevision, false},
	{OutputRenderEnvelopeType, "tangent.generic-candidate", RegisterOutputRender, false},
	{WhiteboardEnvelopeType, "tangent.canvas", RegisterWhiteboard, false},
	{DashboardEnvelopeType, "tangent.canvas", RegisterDashboard, false},
	{FilePickerEnvelopeType, "tangent.workspace", RegisterFilePicker, false},
	{ProgressPanelEnvelopeType, "tangent.generic-candidate", RegisterProgressPanel, false},
	{WizardEnvelopeType, "tangent.compound", RegisterWizard, false},
	{DiffReviewEnvelopeType, "tangent.review", RegisterDiffReview, false},
	{SpreadsheetReviewEnvelopeType, "tangent.review", RegisterSpreadsheetReview, false},
	{ApprovalQueueEnvelopeType, "tangent.review", RegisterApprovalQueue, false},
	{SynthesisNotesEnvelopeType, "tangent.writing", RegisterSynthesisNotes, false},
	{HITLItemEnvelopeType, HITLPackageID, RegisterHITLItem, false},
	{AppBoardEnvelopeType, AppBoardPackageID, RegisterAppBoard, false},
	{AgentTurnEnvelopeType, TurnsPackageID, RegisterAgentTurn, false},
	{DocItemEnvelopeType, DocsPackageID, RegisterDocItem, false},
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
	return registerAll(registrations, svc)
}

// registerAll is RegisterAll over a supplied table. The table is a parameter so
// that contributedDoorFixture can drive the same code the production table
// drives: the ADR 0007 §4 door has no shipped user, and a door tested through a
// copy of its logic is not the door.
func registerAll(regs []registration, svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	for _, reg := range regs {
		// A plugin-contributed kind is installed by the plugin host, through
		// RegisterContributedKind, after the host has resolved its manifest.
		// Registering it here as well would be a duplicate, which go-envelopes
		// correctly rejects — and it would also route the kind around the
		// manifest requirement ADR 0007 §4 exists to impose.
		if reg.contributedByPlugin {
			continue
		}
		if err := reg.register(svc); err != nil {
			return err
		}
	}
	return nil
}

// RegisterContributedKind installs one plugin-contributed kind by wire name.
// It is the ADR 0007 §4 door, and internal/pluginhost is its only production
// caller: a plugin declares an envelope UI component, the host resolves the
// kind's ADR 0003 manifest, and this is what finally installs it.
//
// A kind this package does not ship a manifest for is refused with
// ErrNotContributable. That is the whole point — "a registration without a
// manifest is refused" is only a rule if the failure path exists — and it is
// why the host cannot install a kind by handing over bytes of its own. The
// manifest is the host's, always; a plugin names a kind, it does not author
// what the host will let that kind do.
//
// Naming a kind that IS shipped but is not marked contributedByPlugin is also
// refused. Those arrive through RegisterAll, and letting the plugin door
// install one would mean the same kind could be registered by two paths with
// no test able to say which one production used.
func RegisterContributedKind(svc *envelope.Service, kind string) error {
	return registerContributedKind(registrations, svc, kind)
}

// registerContributedKind is RegisterContributedKind over a supplied table. See
// registerAll for why the table is a parameter.
func registerContributedKind(regs []registration, svc *envelope.Service, kind string) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	for _, reg := range regs {
		if reg.name != kind {
			continue
		}
		if !reg.contributedByPlugin {
			return fmt.Errorf(
				"%w: %s is a host-package kind installed by RegisterAll, not a plugin contribution",
				ErrNotContributable, kind)
		}
		return reg.register(svc)
	}
	return fmt.Errorf("%w: %s ships no manifest in this host", ErrNotContributable, kind)
}

// PluginContributedTypes returns the wire names that arrive through the plugin
// host rather than through RegisterAll, in registration order.
func PluginContributedTypes() []string {
	return pluginContributedTypes(registrations)
}

// pluginContributedTypes is PluginContributedTypes over a supplied table. See
// registerAll for why the table is a parameter.
func pluginContributedTypes(regs []registration) []string {
	names := make([]string, 0, 1)
	for _, reg := range regs {
		if reg.contributedByPlugin {
			names = append(names, reg.name)
		}
	}
	return names
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
