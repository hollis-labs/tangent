package definition

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// This file is ADR 0003 §3's identity rule as code.
//
// §3 says a `revision` "may advance within a `version` only when
// `contract_digest`, `renderer.class`, `renderer.trust_class`, and
// `required_capabilities` are all unchanged. Otherwise it is a new `version`."
// Until CW-20260911-0008 nothing compared those, and the rule was violated for
// a year in one manifest without a single test noticing: `tangent.app-board`
// advanced `revision` to 2, 3 and 4, each time adding an optional request-schema
// field, each time moving `contract_digest`.
//
// The rule was right and the manifest was wrong, so §3 was not amended. What it
// lacked was a mechanism, and this is it. Nothing here is a new policy — every
// line is §3 restated in a form that can fail a build.
//
// # Why this needs a previous publication at all
//
// "The contract moved" is not a property of one manifest. It is a comparison
// against what was published before, and a build has no history. So the caller
// supplies the previously published identity — for the shipped set that is the
// reviewed table in internal/envelope/extensions, committed alongside the
// manifests it pins — and this package owns what the comparison *means*.

// ErrIllegalRevisionAdvance reports a manifest that moved its contract while
// holding its version: either a `revision` bump carrying a change §3 says is a
// version bump, or a straight republication of different content under one
// immutable `(publisher, kind, version)` triple.
var ErrIllegalRevisionAdvance = errors.New("definition: contract moved without a version bump")

// Identity is the part of a published definition a `revision` may not move
// underneath — the four fields §3 names, and no others.
//
// `required_capabilities` is carried as a digest rather than as the slice so
// that one Identity is four comparable scalars. A pinned table of them stays
// readable, and a difference reports as one named field rather than as a
// structural diff a reader has to interpret.
type Identity struct {
	ContractDigest    string        `json:"contract_digest"`
	RendererClass     RendererClass `json:"renderer_class"`
	RendererIsolation Isolation     `json:"renderer_isolation"`
	// CapabilitiesDigest is empty for a definition requiring none, which is
	// every kind this build ships. Empty rather than the digest of `[]` for
	// the same reason Digest returns "" for absent material: "requires
	// nothing" and "requires an empty thing" should not be one value.
	CapabilitiesDigest string `json:"required_capabilities_digest,omitempty"`
}

// CapabilitiesDigest is the content digest over a definition's declared
// capabilities, ordered by id so a manifest that lists the same grants in a
// different order is the same identity.
func CapabilitiesDigest(capabilities []Capability) string {
	if len(capabilities) == 0 {
		return ""
	}
	ordered := make([]Capability, len(capabilities))
	copy(ordered, capabilities)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	encoded, err := json.Marshal(ordered)
	if err != nil {
		// Capability carries only JSON-encodable fields; a failure here would
		// mean the struct changed underneath this function, which is a build
		// error rather than a runtime condition a caller could handle.
		return DigestPrefix + "unencodable"
	}
	return Digest(encoded)
}

// Publication is one manifest as it stands at a point in time: the version and
// revision it declares, the identity §3 protects, and the response-schema
// marker the one granted exception turns on.
type Publication struct {
	Kind     string   `json:"kind"`
	Version  string   `json:"version"`
	Revision int64    `json:"revision"`
	Identity Identity `json:"identity"`
	// ResponseSchema is the §8 C4 marker. It is part of a publication rather
	// than of Identity because it is not a field §3 freezes — it is the field
	// the §3 exception is *about*.
	ResponseSchema ResponseSchemaCompatibility `json:"compatibility_response_schema"`
}

// PublicationOf projects one parsed manifest and its derived digests.
func PublicationOf(manifest *Manifest, derived Derived) Publication {
	if manifest == nil {
		return Publication{}
	}
	return Publication{
		Kind:     manifest.Kind,
		Version:  manifest.Version,
		Revision: manifest.Revision,
		Identity: Identity{
			ContractDigest:     derived.ContractDigest,
			RendererClass:      manifest.Renderer.Class,
			RendererIsolation:  manifest.Renderer.Isolation,
			CapabilitiesDigest: CapabilitiesDigest(manifest.RequiredCapabilities),
		},
		ResponseSchema: manifest.CompatibilityResponseSchema,
	}
}

// CheckRevisionAdvance holds §3 against a pair of publications of one kind.
//
// A version bump is always legal and is checked no further: a new
// `(publisher, kind, version)` may carry any contract, which is what a version
// is for. Everything below is the same-version case.
//
//   - Nothing moved: legal, whatever the revision did.
//   - The contract moved and the revision advanced: the §3 violation this gate
//     exists for. It is a version bump.
//   - The contract moved and the revision held: a straight republication of
//     different content under an immutable triple, which §3 calls a publisher
//     error detected by digest. Same rule, and it is worth a distinct message
//     because the remedy a reader reaches for is different.
//
// The one granted exception is honored rather than hard-failed: §3 permits the
// once-per-kind transition from `compatibility_response_schema: absent` to
// `present` to move `contract_digest` while holding `version`. It is
// structurally once-per-kind — a definition that is `present` can never
// transition to `present` again — so nothing has to count it. The half this
// cannot check is the ADR's requirement that the manifest say so at the field;
// that stays a reviewer's obligation, and pretending otherwise by grepping a
// YAML comment would be a gate that passes on the wrong evidence.
//
// The exception covers the contract digest and nothing else. A backfill that
// also moved a renderer class, a trust class or a capability set is refused,
// because §8 C2 makes each of those a version bump on its own terms.
func CheckRevisionAdvance(previous, current Publication) error {
	if previous.Version != current.Version {
		return nil
	}
	moved := previous.Identity.Moved(current.Identity)
	if len(moved) == 0 {
		return nil
	}

	backfill := previous.ResponseSchema == ResponseSchemaAbsent &&
		current.ResponseSchema == ResponseSchemaPresent
	if backfill {
		var beyond []string
		for _, field := range moved {
			if field != "contract_digest" {
				beyond = append(beyond, field)
			}
		}
		if len(beyond) == 0 {
			if current.Revision <= previous.Revision {
				return fmt.Errorf(
					"%w: %s backfilled its response schema at version %s without advancing "+
						"revision (still %d); ADR 0003 §3's exception advances revision and holds version",
					ErrIllegalRevisionAdvance, current.Kind, current.Version, current.Revision)
			}
			return nil
		}
		return fmt.Errorf(
			"%w: %s backfilled its response schema at version %s but also moved %v; ADR 0003 §3's "+
				"exception covers contract_digest alone, and §8 C2 makes each of those a version bump",
			ErrIllegalRevisionAdvance, current.Kind, current.Version, beyond)
	}

	if current.Revision > previous.Revision {
		return fmt.Errorf(
			"%w: %s advanced revision %d → %d while %v moved at version %s; ADR 0003 §3 makes that a "+
				"new version, not a revision — revision is a non-semantic edit counter within a version",
			ErrIllegalRevisionAdvance, current.Kind, previous.Revision, current.Revision,
			moved, current.Version)
	}
	return fmt.Errorf(
		"%w: %s@%s revision %d republishes different content (%v moved); ADR 0003 §3 makes that "+
			"triple immutable, so the change is a new version",
		ErrIllegalRevisionAdvance, current.Kind, current.Version, current.Revision, moved)
}

// Moved returns the §3 field names that differ, in the order §3 lists them, so
// a failure names what changed rather than dumping two structs.
func (i Identity) Moved(other Identity) []string {
	var moved []string
	if i.ContractDigest != other.ContractDigest {
		moved = append(moved, "contract_digest")
	}
	if i.RendererClass != other.RendererClass {
		moved = append(moved, "renderer.class")
	}
	if i.RendererIsolation != other.RendererIsolation {
		moved = append(moved, "renderer.isolation")
	}
	if i.CapabilitiesDigest != other.CapabilitiesDigest {
		moved = append(moved, "required_capabilities")
	}
	return moved
}
