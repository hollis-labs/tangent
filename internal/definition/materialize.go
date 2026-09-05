package definition

import (
	"fmt"
	"sort"
	"time"
)

// State is the materialization state vocabulary from the interactive
// collaboration direction document, locked by ADR 0003 §1:
//
//	registered -> resolved -> verified -> materialized -> available
//	                                      \-> incompatible
//	                                      \-> quarantined
//	                                      \-> unavailable
//
// `registered` never implies `available`. The three terminal projections are
// distinguishable in the registry and in tangent.interaction_resolve_definition
// because "unavailable is a state, not an error to paper over" (§8 C7).
type State string

const (
	// StateRegistered means the manifest was accepted into the registry. It
	// says nothing about whether the definition can be served.
	StateRegistered State = "registered"
	// StateResolved means every file the manifest references was found.
	StateResolved State = "resolved"
	// StateVerified means trust evidence was evaluated and accepted.
	StateVerified State = "verified"
	// StateMaterialized means digests are derived and material is retained.
	StateMaterialized State = "materialized"
	// StateAvailable means new interactions may be submitted against it.
	StateAvailable State = "available"

	// StateIncompatible means the host, protocol, or manifest format is out
	// of the declared range. The definition is intact; this host cannot serve
	// it.
	StateIncompatible State = "incompatible"
	// StateQuarantined means trust or capability evaluation refused it.
	StateQuarantined State = "quarantined"
	// StateUnavailable means it cannot currently be served for an operational
	// reason — disabled by host policy, renderer missing, material not
	// retained.
	StateUnavailable State = "unavailable"
)

// Servable reports whether a state permits a *new* submission. Only
// StateAvailable does. A pinned interaction is never re-evaluated against
// this: ADR 0003 §8 C1 says a registry change makes a definition unavailable
// for new submissions and never rewrites an existing record.
func (s State) Servable() bool { return s == StateAvailable }

// Error codes reused from the go-envelopes vocabulary. ADR 0003 §8 C7 is
// explicit that the existing vocabulary is sufficient and is reused rather
// than extended, so these are aliases of the upstream constants' values rather
// than a parallel Tangent vocabulary.
const (
	ErrorCodeUnsupportedType     = "unsupported-type"
	ErrorCodeUnsupportedVersion  = "unsupported-version"
	ErrorCodeValidationFailed    = "validation-failed"
	ErrorCodeCapabilityDenied    = "capability-denied"
	ErrorCodeComponentLoadFailed = "component-load-failed"
)

// HostPolicy is everything Tangent brings to materialization that the
// publisher does not author. It is the host side of §2.5's capability
// intersection and §2.7's trust derivation.
type HostPolicy struct {
	// HostVersion is mcp.HostVersion, checked against
	// compatible_host_versions.
	HostVersion string
	// ProtocolVersion is the go-envelopes wire model version.
	ProtocolVersion string
	// SDKVersion is the plugin SDK version, for out-of-tree packages. Empty
	// means no SDK is present, and a manifest declaring a range is then
	// incompatible rather than silently accepted.
	SDKVersion string
	// GrantableCapabilities is the set of host-mediated *effect* capability
	// ids this installation is willing to grant. It is empty in v0.x:
	// CW-20260825-0077 is what populates it. Until then a kind requiring a
	// non-optional capability is quarantined, which is the fail-closed
	// behavior §8 C7 requires — not a reason to grant by default.
	//
	// This is not ADR 0004 §2's object-access namespace. The two never
	// substitute for one another.
	GrantableCapabilities map[string]bool
	// DisabledKinds lets an operator turn one kind off without unregistering
	// it, so the registry can still explain why it is not being served.
	DisabledKinds map[string]bool
	// InlinePayloadCeilingBytes caps what a publisher may declare (§2.6:
	// "publisher, capped by Tangent"). Zero means uncapped.
	InlinePayloadCeilingBytes int64
	// Now is injectable so materialization is deterministic under test.
	Now func() time.Time
}

func (p HostPolicy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now().UTC()
}

// Materialized is one manifest carried all the way through materialization: the
// authored document, the exact bytes, the derived digests, and the host's own
// decisions about trust, capabilities, and servability.
//
// The fields Tangent owns — Assurance, GrantedCapabilities, State — are never
// copied from the manifest. ADR 0003 §5 assigns them to the host explicitly.
type Materialized struct {
	Manifest *Manifest `json:"manifest"`
	Material Material  `json:"-"`
	Derived  Derived   `json:"derived"`

	// State and the two explanation fields are the §8 C7 projection: a
	// caller must be able to tell incompatible from quarantined from
	// unavailable without reading a log.
	State       State  `json:"state"`
	StateReason string `json:"state_reason,omitempty"`
	// ErrorCode is the upstream code a refusal surfaces as.
	ErrorCode string `json:"error_code,omitempty"`

	// Assurance is Tangent's derivation, not the manifest's request.
	Assurance Assurance `json:"assurance"`
	// SourceLocator is where the material was obtained (trust.source_locator).
	SourceLocator string `json:"source_locator,omitempty"`
	// GrantedCapabilities is the intersection of required_capabilities with
	// host policy. Persisted into the binding so a resolution records what
	// the renderer could actually do.
	GrantedCapabilities []Capability `json:"granted_capabilities"`
	// DeniedCapabilities records what was asked for and refused by *host
	// policy*, so a quarantine explains itself.
	DeniedCapabilities []Capability `json:"denied_capabilities,omitempty"`
	// TrustDeniedCapabilities records what the renderer's own trust class
	// refuses, which is a different fact from host policy declining to grant
	// it and is reported separately for the same reason the broker has two
	// refusal codes for `undeclared` and `denied`: an operator who widens
	// policy will not widen this one, and needs to be told so.
	TrustDeniedCapabilities []Capability `json:"trust_denied_capabilities,omitempty"`
	// QuarantineReason is set only when State is StateQuarantined (§2.7).
	QuarantineReason string `json:"quarantine_reason,omitempty"`

	// TrustClass is the class Tangent *granted*, which is the manifest's
	// request only when the trust evidence supported it. A request the
	// evidence does not support is quarantined rather than downgraded — a
	// silently demoted renderer is a renderer running somewhere its author did
	// not design for, and ADR 0003 §8 C2 makes a change of class a version
	// bump rather than something a host does behind the publisher's back.
	//
	// It is empty when the evidence check refused, and populated on every
	// state reached after it, including a quarantine for a capability the
	// class does not permit — where naming the class is the explanation.
	TrustClass TrustClass `json:"renderer_trust_class,omitempty"`
	// Isolation is where the granted class runs the renderer. It is the fact
	// every browser-side and effect-side enforcement decision reads, and it is
	// derived here rather than by each consumer so there is one answer.
	Isolation Isolation `json:"renderer_isolation,omitempty"`

	VerifiedAt  time.Time `json:"verified_at"`
	HostVersion string    `json:"host_version"`
	// EffectiveInlineLimitBytes is the publisher's declared limit after the
	// host ceiling is applied.
	EffectiveInlineLimitBytes int64 `json:"effective_inline_payload_limit_bytes"`
}

// Materialize walks one manifest through the state machine and returns the
// result. It never returns an error for a definition that is merely
// unservable: an unservable definition is a *state*, and the registry has to
// be able to list and explain it (§8 C7). It returns an error only when the
// input is incoherent — a nil manifest, or material that cannot be digested.
func Materialize(manifest *Manifest, material Material, policy HostPolicy) (Materialized, error) {
	if manifest == nil {
		return Materialized{}, fmt.Errorf("%w: manifest is nil", ErrInvalidManifest)
	}
	derived, err := Derive(manifest, material)
	if err != nil {
		return Materialized{}, err
	}

	out := Materialized{
		Manifest:                  manifest,
		Material:                  material,
		Derived:                   derived,
		State:                     StateRegistered,
		SourceLocator:             material.SourceLocator,
		HostVersion:               policy.HostVersion,
		VerifiedAt:                policy.now(),
		EffectiveInlineLimitBytes: manifest.InlinePayloadLimitBytes,
		GrantedCapabilities:       []Capability{},
	}
	if policy.InlinePayloadCeilingBytes > 0 && out.EffectiveInlineLimitBytes > policy.InlinePayloadCeilingBytes {
		out.EffectiveInlineLimitBytes = policy.InlinePayloadCeilingBytes
	}

	// registered -> resolved: every referenced file was found.
	if len(material.RequestSchema) == 0 {
		return out.unavailable(
			fmt.Sprintf("request schema %q is not present in the package", manifest.RequestSchemaRef),
			ErrorCodeComponentLoadFailed), nil
	}
	if manifest.ResponseSchemaRef != "" && len(material.ResponseSchema) == 0 {
		return out.unavailable(
			fmt.Sprintf("response schema %q is not present in the package", manifest.ResponseSchemaRef),
			ErrorCodeComponentLoadFailed), nil
	}
	if manifest.ErrorSchemaRef != "" && len(material.ErrorSchema) == 0 {
		return out.unavailable(
			fmt.Sprintf("error schema %q is not present in the package", manifest.ErrorSchemaRef),
			ErrorCodeComponentLoadFailed), nil
	}
	out.State = StateResolved

	// resolved -> verified: trust evidence. §2.7 reserves `unverified` with
	// no producer, so anything not grantable is quarantined rather than
	// materialized at a weaker assurance.
	if !manifest.Trust.Assurance.Grantable() {
		out.Assurance = manifest.Trust.Assurance
		return out.quarantined(
			fmt.Sprintf("trust.assurance %q has no verifier in this build", manifest.Trust.Assurance)), nil
	}
	out.Assurance = manifest.Trust.Assurance

	// Trust evidence is evaluated against the *requested class*, not against
	// the manifest in general. This is the step ADR 0003 §2.3 describes as
	// "a manifest requests a class; Tangent policy decides", and until
	// CW-20260825-0073 it had no implementation at all: the field was
	// validated for spelling and then copied through.
	profile, known := TrustProfileFor(manifest.Renderer.TrustClass)
	if !known {
		return out.quarantined(
			fmt.Sprintf("renderer.trust_class %q has no profile in this build",
				manifest.Renderer.TrustClass)), nil
	}
	if supported, reason := trustEvidenceSupports(manifest, out.Assurance, profile); !supported {
		return out.quarantined(reason), nil
	}
	out.TrustClass = profile.Class
	out.Isolation = profile.Isolation
	out.State = StateVerified

	// verified -> materialized: compatibility ranges. Checked after trust so
	// an untrusted definition is never described as merely out-of-range.
	if incompatible, reason := checkCompatibility(manifest, policy); incompatible {
		return out.incompatible(reason), nil
	}
	out.State = StateMaterialized

	// materialized -> available: the trust ceiling, then the host's own
	// intersection, then host switches.
	//
	// The ceiling comes first and the order is the decision. A capability the
	// renderer's trust class does not permit is refused whatever host policy
	// says, so an operator who widens `GrantableCapabilities` widens nothing
	// the class already closed — which is what stops "grant it and see" from
	// being an escalation path for a sandboxed renderer.
	if trustDenied := trustCeilingDenials(manifest.RequiredCapabilities, profile); len(trustDenied) > 0 {
		out.TrustDeniedCapabilities = trustDenied
		for _, capability := range trustDenied {
			if capability.Optional {
				continue
			}
			return out.quarantined(
				fmt.Sprintf("renderer.trust_class %q does not permit required capability %q",
					profile.Class, capability.ID)), nil
		}
	}
	permitted := make([]Capability, 0, len(manifest.RequiredCapabilities))
	for _, capability := range manifest.RequiredCapabilities {
		if profile.Permits(capability.ID) {
			permitted = append(permitted, capability)
		}
	}
	granted, denied := intersectCapabilities(permitted, policy.GrantableCapabilities)
	out.GrantedCapabilities = granted
	out.DeniedCapabilities = denied
	for _, capability := range denied {
		if !capability.Optional {
			return out.quarantined(
				fmt.Sprintf("required capability %q is not granted by host policy", capability.ID)), nil
		}
	}
	if policy.DisabledKinds[manifest.Kind] {
		return out.unavailable("kind is disabled by host policy", ErrorCodeUnsupportedType), nil
	}
	if manifest.Renderer.Entry == "" && manifest.Renderer.Class != RendererExternalSurface {
		return out.unavailable("renderer declares no entry point", ErrorCodeComponentLoadFailed), nil
	}

	out.State = StateAvailable
	return out, nil
}

func checkCompatibility(manifest *Manifest, policy HostPolicy) (bool, string) {
	manifestVersion, err := ParseVersion(manifest.ManifestVersion)
	if err != nil {
		return true, fmt.Sprintf("manifest_version %q is not a version", manifest.ManifestVersion)
	}
	if manifestVersion.Major != SupportedManifestMajor {
		return true, fmt.Sprintf(
			"manifest format major %d is not implemented by this host (supports %d)",
			manifestVersion.Major, SupportedManifestMajor)
	}
	checks := []struct {
		label   string
		rangeIn string
		actual  string
	}{
		{"host", manifest.CompatibleHostVersions, policy.HostVersion},
		{"protocol", manifest.CompatibleProtocolVersions, policy.ProtocolVersion},
		{"sdk", manifest.CompatibleSDKVersions, policy.SDKVersion},
	}
	for _, check := range checks {
		if check.rangeIn == "" {
			continue
		}
		allowed, rangeErr := ParseRange(check.rangeIn)
		if rangeErr != nil {
			return true, fmt.Sprintf("compatible_%s_versions %q is malformed", check.label, check.rangeIn)
		}
		if check.actual == "" {
			return true, fmt.Sprintf(
				"compatible_%s_versions %q is declared but this host reports no %s version",
				check.label, check.rangeIn, check.label)
		}
		actual, versionErr := ParseVersion(check.actual)
		if versionErr != nil {
			return true, fmt.Sprintf("%s version %q is not a version", check.label, check.actual)
		}
		if !allowed.Contains(actual) {
			return true, fmt.Sprintf(
				"%s version %s is outside compatible_%s_versions %q",
				check.label, check.actual, check.label, check.rangeIn)
		}
	}
	return false, ""
}

// intersectCapabilities returns the granted and denied halves of §2.5's
// intersection. Order is stabilized so two runs of the same materialization
// produce byte-identical bindings.
func intersectCapabilities(required []Capability, grantable map[string]bool) (granted, denied []Capability) {
	granted = []Capability{}
	for _, capability := range required {
		if grantable[capability.ID] {
			granted = append(granted, capability)
			continue
		}
		denied = append(denied, capability)
	}
	sort.Slice(granted, func(i, j int) bool { return granted[i].ID < granted[j].ID })
	sort.Slice(denied, func(i, j int) bool { return denied[i].ID < denied[j].ID })
	return granted, denied
}

func (m Materialized) incompatible(reason string) Materialized {
	m.State = StateIncompatible
	m.StateReason = reason
	m.ErrorCode = ErrorCodeUnsupportedVersion
	return m
}

// quarantined is trust and capability evaluation refusing a definition.
//
// It takes no error code because there is only one: ADR 0003 §8 C7 reuses the
// upstream vocabulary rather than extending it, and every reason a definition
// reaches this state — an assurance with no verifier, a trust class the
// evidence does not support, a capability the class does not permit, a
// capability host policy did not grant — is `capability-denied` in that
// vocabulary. The reason string carries which one; the code carries that it was
// refused rather than merely unavailable.
func (m Materialized) quarantined(reason string) Materialized {
	m.State = StateQuarantined
	m.StateReason = reason
	m.QuarantineReason = reason
	m.ErrorCode = ErrorCodeCapabilityDenied
	return m
}

func (m Materialized) unavailable(reason, code string) Materialized {
	m.State = StateUnavailable
	m.StateReason = reason
	m.ErrorCode = code
	return m
}

// SourceEntry projects this definition's contribution to a generated
// artifact's @definition-source stamp.
func (m Materialized) SourceEntry() SourceEntry {
	return SourceEntry{
		Kind:           m.Manifest.Kind,
		Version:        m.Manifest.Version,
		Revision:       m.Manifest.Revision,
		ManifestDigest: m.Derived.ManifestDigest,
	}
}

// SafeFallback returns the fallback renderer Tangent is permitted to use, and
// whether one exists.
//
// ADR 0003 §8 C5: a fallback is used only when the manifest declares one and
// preserves_meaning is true. It must never downgrade a structured decision to
// a free-text box or drop required evidence. With no qualifying fallback,
// resolution fails closed with component-load-failed, the interaction stays
// non-terminal, and the caller is told the definition is unavailable — a
// renderer problem is not a participant cancellation.
//
// The JSON debug renderer in EnvelopeRouter.tsx is a development affordance
// and is deliberately not reachable through this function.
func (m Materialized) SafeFallback() (Fallback, bool) {
	fallback := m.Manifest.Renderer.Fallback
	if !fallback.PreservesMeaning || fallback.RendererID == "" {
		return Fallback{}, false
	}
	return fallback, true
}
