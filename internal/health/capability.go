package health

import (
	"sort"

	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/effect"
)

// Capability health is per-kind, and it reuses the materialization-state
// vocabulary internal/definition already owns.
//
// That reuse is the design, not a convenience. A kind can be registered and
// still unusable for four different reasons that need four different fixes:
// `incompatible` is a version-range problem, `quarantined` is a trust or
// capability refusal, `unavailable` is operational (disabled, material
// missing), and an intermediate state means materialization never finished.
// Inventing a second vocabulary here would let a report say "unhealthy" where
// the registry already says which of those four it is.

// Presence says whether this build has anything to report about a kind at all.
type Presence string

const (
	// PresenceManaged means a definition manifest carries the kind, so it has
	// a materialization state.
	PresenceManaged Presence = "managed"
	// PresenceUnmanaged means the envelope registry knows the kind but no
	// manifest carries it — the legacy registration path, which produces a
	// binding without a materialization.
	PresenceUnmanaged Presence = "unmanaged"
	// PresenceUnknown means nothing in this build answers to the name.
	PresenceUnknown Presence = "unknown"
)

// Grant classifies one declared capability's outcome. The three refusals are
// kept apart because an operator who widens host policy lifts exactly one of
// them, and being told which is the difference between a fix and an afternoon.
const (
	GrantGranted            = "granted"
	GrantDeniedByHostPolicy = "denied-by-host-policy"
	GrantDeniedByTrustClass = "denied-by-trust-class"
	// GrantUnknownCapability is a capability id this build does not recognize.
	// It is never a grant.
	GrantUnknownCapability = "unknown-capability"
)

// EffectCapability is one host-mediated effect a kind declared, and what this
// build would actually do with it.
type EffectCapability struct {
	ID       string `json:"capability_id"`
	Optional bool   `json:"optional"`
	Grant    string `json:"grant"`
	// Mediation is how much of the capability the host can enforce for this
	// kind's renderer isolation — `host`, `declared`, or `unimplemented`.
	// A `declared` mediation is a record, not a barrier, and saying so here
	// stops a granted capability from reading as an enforced one.
	Mediation string `json:"mediation"`
}

// EffectPosture is what an effect request against a kind would actually meet.
type EffectPosture struct {
	Declared []EffectCapability `json:"declared_capabilities"`
	// RequestOutcome is the refusal code an effect request naming any
	// capability against this kind is answered with today, or empty when at
	// least one declared capability is granted.
	//
	// It is populated rather than omitted for the shipped kinds, which declare
	// nothing: reporting "no capabilities" as silence would let a reader infer
	// that effects work. They do not — the broker refuses every one of them
	// with effect_capability_undeclared, and that is the truthful answer.
	RequestOutcome string `json:"effect_request_outcome,omitempty"`
	Note           string `json:"note,omitempty"`
}

// CapabilityReport answers "can this build serve this interaction kind".
type CapabilityReport struct {
	Probe    string   `json:"probe"`
	Kind     string   `json:"kind"`
	Presence Presence `json:"presence"`
	Status   Status   `json:"status"`
	// Usable is the single boolean a caller wants: may a new interaction be
	// submitted against this kind right now.
	Usable bool `json:"usable"`

	Version        string `json:"version,omitempty"`
	Revision       int64  `json:"revision,omitempty"`
	ManifestDigest string `json:"manifest_digest,omitempty"`

	State       string `json:"materialization_state,omitempty"`
	StateReason string `json:"state_reason,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`

	TrustClass string `json:"renderer_trust_class,omitempty"`
	Isolation  string `json:"renderer_isolation,omitempty"`

	Effects EffectPosture `json:"effects"`

	Action string `json:"operator_action,omitempty"`
}

// CapabilitySummaryReport is every kind at once, bounded.
type CapabilitySummaryReport struct {
	Probe  string  `json:"probe"`
	Status Summary `json:"status"`
	// RegisteredKinds counts everything the envelope registry holds, core
	// included; ManagedDefinitions counts the subset carrying a manifest.
	// The gap is not a fault — it is the legacy registration path.
	RegisteredKinds    int `json:"registered_kinds"`
	ManagedDefinitions int `json:"managed_definitions"`
	Usable             int `json:"usable_definitions"`

	StateCounts map[string]int `json:"materialization_state_counts"`

	// DeclaredEffectCapabilities is every host-mediated effect capability any
	// shipped definition asks for. It is empty in this build.
	DeclaredEffectCapabilities []string `json:"declared_effect_capabilities"`
	EffectPostureNote          string   `json:"effect_posture_note,omitempty"`

	// Unusable carries one entry per kind that cannot be served, bounded by
	// maxListedKinds. Truncated says whether the list is the whole answer.
	Unusable  []CapabilityReport `json:"unusable"`
	Truncated bool               `json:"truncated"`

	Action string `json:"operator_action,omitempty"`
}

// Capability reports on one requested interaction kind.
func (r *Reporter) Capability(kind string) CapabilityReport {
	// The effect posture is initialized rather than left nil so a report has
	// the same shape for every kind. A consumer that has to distinguish
	// `null` from `[]` before it can read a health document is a consumer that
	// will get it wrong during an incident.
	report := CapabilityReport{
		Probe:   ProbeCapability,
		Kind:    bound(kind),
		Effects: EffectPosture{Declared: []EffectCapability{}},
	}
	if r.registry == nil {
		report.Presence = PresenceUnknown
		report.Status = StatusFail
		report.Action = "This binary was constructed without a definition registry, so no kind " +
			"can be reported on. Report the build as defective." + r.redeployHint()
		return report
	}
	for _, materialized := range r.registry.MaterializedDefinitions() {
		if materialized.Manifest == nil || materialized.Manifest.Kind != kind {
			continue
		}
		return r.describe(materialized)
	}
	for _, registered := range r.registry.RegisteredKinds() {
		if registered != kind {
			continue
		}
		report.Presence = PresenceUnmanaged
		// A kind on the legacy registration path is usable — it validates and
		// dispatches — it simply has no materialization to report. Saying
		// `warn` rather than `pass` records that this report is not evidence
		// of the same checks a managed definition passed.
		report.Usable = true
		report.Status = StatusWarn
		report.Action = "This kind is registered but carries no definition manifest, so it has " +
			"no materialization state, no trust class, and no capability intersection. It is " +
			"served on the generic path; capability health cannot vouch for it."
		return report
	}
	report.Presence = PresenceUnknown
	report.Status = StatusFail
	report.Action = "No definition manifest and no registered envelope type in this build " +
		"answers to that kind. Check the spelling against the kind list at /healthz/capability."
	return report
}

// CapabilitySummary reports every kind at once.
func (r *Reporter) CapabilitySummary() CapabilitySummaryReport {
	summary := CapabilitySummaryReport{
		Probe:                      ProbeCapability,
		Status:                     SummaryOK,
		StateCounts:                map[string]int{},
		DeclaredEffectCapabilities: []string{},
		Unusable:                   []CapabilityReport{},
	}
	if r.registry == nil {
		summary.Status = SummaryUnavailable
		summary.Action = "This binary was constructed without a definition registry and can " +
			"serve no interaction. Report the build as defective." + r.redeployHint()
		return summary
	}

	materialized := r.registry.MaterializedDefinitions()
	summary.RegisteredKinds = len(r.registry.RegisteredKinds())
	summary.ManagedDefinitions = len(materialized)

	declared := map[string]bool{}
	for _, item := range materialized {
		summary.StateCounts[string(item.State)]++
		for _, capability := range item.Manifest.RequiredCapabilities {
			declared[capability.ID] = true
		}
		if item.State.Servable() {
			summary.Usable++
			continue
		}
		if len(summary.Unusable) < maxListedKinds {
			summary.Unusable = append(summary.Unusable, r.describe(item))
			continue
		}
		summary.Truncated = true
	}
	for id := range declared {
		summary.DeclaredEffectCapabilities = append(summary.DeclaredEffectCapabilities, id)
	}
	sort.Strings(summary.DeclaredEffectCapabilities)

	if len(summary.DeclaredEffectCapabilities) == 0 {
		summary.EffectPostureNote = "no definition in this build declares a host-mediated effect " +
			"capability, so the effect broker refuses every request with " +
			effect.CodeCapabilityUndeclared + ". That is the designed posture, not a fault: no " +
			"shipped workflow performs a host effect."
	}

	switch {
	case summary.ManagedDefinitions == 0:
		summary.Status = SummaryUnavailable
		summary.Action = "The registry materialized no definitions; no interaction kind can be " +
			"served. Rebuild with `make build`." + r.redeployHint()
	case summary.Usable == 0:
		summary.Status = SummaryUnavailable
		summary.Action = "Every materialized definition is unservable. Read the per-kind reason " +
			"below: incompatible, quarantined, and unavailable need three different fixes."
	case len(summary.Unusable) > 0 || summary.Truncated:
		summary.Status = SummaryDegraded
		summary.Action = "Some interaction kinds will be refused. Fix them before a caller " +
			"discovers it as a failed workflow."
	}
	return summary
}

// describe projects one materialized definition into a capability report.
func (r *Reporter) describe(materialized definition.Materialized) CapabilityReport {
	manifest := materialized.Manifest
	report := CapabilityReport{
		Probe:          ProbeCapability,
		Kind:           manifest.Kind,
		Presence:       PresenceManaged,
		Usable:         materialized.State.Servable(),
		Version:        manifest.Version,
		Revision:       manifest.Revision,
		ManifestDigest: materialized.Derived.ManifestDigest,
		State:          string(materialized.State),
		StateReason:    bound(materialized.StateReason),
		ErrorCode:      materialized.ErrorCode,
		TrustClass:     string(materialized.TrustClass),
		Isolation:      string(materialized.Isolation),
		Effects:        effectPosture(materialized),
	}
	if report.Usable {
		report.Status = StatusPass
		return report
	}
	report.Status = StatusFail
	report.Action = unusableAction(materialized.State)
	return report
}

// unusableAction is the one sentence an operator needs per terminal state.
// Each of the four is a different fix, which is exactly why the states are not
// collapsed into "unhealthy".
func unusableAction(state definition.State) string {
	switch state {
	case definition.StateIncompatible:
		return "This host cannot serve the kind: the definition's declared compatible range " +
			"excludes this build. Deploy a Tangent inside that range, or install a definition " +
			"revision that declares this one. Widening the range in the manifest is a version " +
			"bump, not a config change."
	case definition.StateQuarantined:
		return "Trust or capability evaluation refused the kind. Read state_reason before " +
			"granting anything: a refusal from the renderer's trust class is not lifted by " +
			"widening host policy, and granting to see what happens is the escalation path the " +
			"ceiling exists to close."
	case definition.StateUnavailable:
		return "The definition is intact but not servable — disabled by host policy, missing " +
			"material, or no renderer entry. If it was disabled deliberately, nothing is wrong; " +
			"otherwise rebuild with `make build` and re-check."
	case definition.StateRegistered, definition.StateResolved,
		definition.StateVerified, definition.StateMaterialized:
		return "Materialization stopped at " + string(state) + " and never reached `available`. " +
			"This is a build or manifest defect rather than an operator setting; report it with " +
			"the state_reason."
	case definition.StateAvailable:
		return ""
	}
	return "The kind is not servable and its state is not one this build recognizes. Report it."
}

// effectPosture classifies each declared capability and says what an effect
// request would actually meet.
func effectPosture(materialized definition.Materialized) EffectPosture {
	manifest := materialized.Manifest
	posture := EffectPosture{Declared: []EffectCapability{}}
	isolation := effect.Isolation(materialized.Isolation)

	granted := capabilitySet(materialized.GrantedCapabilities)
	trustDenied := capabilitySet(materialized.TrustDeniedCapabilities)
	hostDenied := capabilitySet(materialized.DeniedCapabilities)

	for _, capability := range manifest.RequiredCapabilities {
		entry := EffectCapability{ID: capability.ID, Optional: capability.Optional}
		switch {
		case !effect.Known(effect.Capability(capability.ID)):
			entry.Grant = GrantUnknownCapability
			entry.Mediation = string(effect.MediationUnimplemented)
		case granted[capability.ID]:
			entry.Grant = GrantGranted
			entry.Mediation = string(effect.MediationFor(effect.Capability(capability.ID), isolation))
		case trustDenied[capability.ID]:
			entry.Grant = GrantDeniedByTrustClass
			entry.Mediation = string(effect.MediationFor(effect.Capability(capability.ID), isolation))
		case hostDenied[capability.ID]:
			entry.Grant = GrantDeniedByHostPolicy
			entry.Mediation = string(effect.MediationFor(effect.Capability(capability.ID), isolation))
		default:
			entry.Grant = GrantDeniedByHostPolicy
			entry.Mediation = string(effect.MediationFor(effect.Capability(capability.ID), isolation))
		}
		posture.Declared = append(posture.Declared, entry)
	}

	switch {
	case len(posture.Declared) == 0:
		posture.RequestOutcome = effect.CodeCapabilityUndeclared
		posture.Note = "this kind declares no host-mediated effect, so the broker refuses every " +
			"effect request naming one; a renderer cannot acquire a capability by asking at runtime"
	case len(materialized.GrantedCapabilities) == 0:
		posture.RequestOutcome = effect.CodeCapabilityDenied
		posture.Note = "every capability this kind declares was refused, so no effect request " +
			"against it can be granted"
	}
	return posture
}

func capabilitySet(capabilities []definition.Capability) map[string]bool {
	set := make(map[string]bool, len(capabilities))
	for _, capability := range capabilities {
		set[capability.ID] = true
	}
	return set
}
