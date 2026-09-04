package definition

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// DigestPrefix is the algorithm label every digest in this stack carries. It
// is spelled out rather than implied so a stored value stays self-describing
// if the algorithm ever changes.
const DigestPrefix = "sha256:"

// Digest returns the content digest of exactly these bytes, or the empty
// string for absent material. Absent is distinct from empty: a definition with
// no response schema has no response_schema_digest, and emitting the digest of
// zero bytes would make "absent" and "empty document" indistinguishable.
func Digest(content []byte) string {
	if len(content) == 0 {
		return ""
	}
	sum := sha256.Sum256(content)
	return DigestPrefix + hex.EncodeToString(sum[:])
}

// Material is the exact byte content one manifest references. These are the
// bytes every digest is taken over and the bytes a pinned interaction is
// re-validated against years later — never a recompiled or reserialized copy.
type Material struct {
	// ManifestSource is the authored manifest file verbatim.
	ManifestSource []byte
	RequestSchema  []byte
	// ResponseSchema is nil for a kind carrying
	// compatibility_response_schema: absent.
	ResponseSchema []byte
	ErrorSchema    []byte
	// SourceLocator is where the material came from — the embedded FS path
	// for a bundled package. Recorded as trust.source_locator (§2.7).
	SourceLocator string
}

// Derived holds every value Tangent computes from a manifest and its material.
// No field here is ever authored; Parse rejects a manifest that tries.
type Derived struct {
	ManifestDigest       string `json:"manifest_digest"`
	ContractDigest       string `json:"contract_digest"`
	RequestSchemaDigest  string `json:"request_schema_digest,omitempty"`
	ResponseSchemaDigest string `json:"response_schema_digest,omitempty"`
	ErrorSchemaDigest    string `json:"error_schema_digest,omitempty"`
}

// contractIdentity is the canonical projection contract_digest is taken over
// (ADR 0003 §2.2). It is a separate struct rather than a subset of Manifest so
// that adding a manifest field cannot silently move every contract digest —
// only an edit here can, and that edit is the deliberate act of changing what
// "the contract" means.
type contractIdentity struct {
	RequestSchema    string            `json:"request_schema"`
	ResponseSchema   string            `json:"response_schema"`
	ErrorSchema      string            `json:"error_schema"`
	ResponseKind     string            `json:"response_kind"`
	NamedDefinitions map[string]string `json:"named_definitions"`
}

// Derive computes every derived digest for one manifest and its material.
//
// Canonical serialization is encoding/json over a fixed struct: Go emits
// struct fields in declaration order and map keys in sorted order, which is
// deterministic across builds without a third-party canonicalizer. The schema
// bodies enter the contract digest as *their* digests rather than inline, so a
// 38 KB bundle does not have to be rehashed as part of every enclosing digest.
func Derive(manifest *Manifest, material Material) (Derived, error) {
	if manifest == nil {
		return Derived{}, fmt.Errorf("%w: manifest is nil", ErrInvalidManifest)
	}
	derived := Derived{
		RequestSchemaDigest:  Digest(material.RequestSchema),
		ResponseSchemaDigest: Digest(material.ResponseSchema),
		ErrorSchemaDigest:    Digest(material.ErrorSchema),
	}

	contract := contractIdentity{
		RequestSchema:    derived.RequestSchemaDigest,
		ResponseSchema:   derived.ResponseSchemaDigest,
		ErrorSchema:      derived.ErrorSchemaDigest,
		ResponseKind:     manifest.ResponseKind,
		NamedDefinitions: manifest.NamedDefinitions,
	}
	contractBytes, err := json.Marshal(contract)
	if err != nil {
		return Derived{}, fmt.Errorf("%w: canonicalize contract: %w", ErrInvalidManifest, err)
	}
	derived.ContractDigest = Digest(contractBytes)

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return Derived{}, fmt.Errorf("%w: canonicalize manifest: %w", ErrInvalidManifest, err)
	}
	derived.ManifestDigest = Digest(manifestBytes)
	return derived, nil
}

// SourceEntry is one manifest's contribution to a generated artifact's
// @definition-source stamp (ADR 0003 §4.1).
type SourceEntry struct {
	Kind           string `json:"kind"`
	Version        string `json:"version"`
	Revision       int64  `json:"revision"`
	ManifestDigest string `json:"manifest_digest"`
}

// SourceDigest is the stable hash over the ordered set of (kind, version,
// revision, manifest_digest) for every manifest that contributed to a
// generated artifact.
//
// Entries are sorted here rather than trusting the caller's order, so a
// generator that walks the registry in map order and one that walks a sorted
// package tree produce the same stamp. A stamp that depended on iteration
// order would report drift that does not exist, which is the fastest way to
// teach people to ignore a drift gate.
func SourceDigest(entries []SourceEntry) (string, error) {
	ordered := make([]SourceEntry, len(entries))
	copy(ordered, entries)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Kind != ordered[j].Kind {
			return ordered[i].Kind < ordered[j].Kind
		}
		if ordered[i].Version != ordered[j].Version {
			return ordered[i].Version < ordered[j].Version
		}
		return ordered[i].Revision < ordered[j].Revision
	})
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return "", fmt.Errorf("%w: canonicalize source entries: %w", ErrInvalidManifest, err)
	}
	return Digest(encoded), nil
}
