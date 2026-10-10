package pluginui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const MaxGraphBytes = 16 << 20
const MaxGraphArtifacts = 256

// Module and Import describe the complete host-reviewed executable inventory.
// Bytes are canonical post-build bytes, not paths or URLs to resolve later.
type Module struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
	Bytes  []byte `json:"bytes"`
}
type Import struct {
	Specifier string `json:"specifier"`
	Artifact  string `json:"artifact"`
}

// GraphReview must examine executable imports against the complete mapping and
// host-approved runtime inventory. This package does not implement a JavaScript
// parser. There is no permissive default or child-supplied review result.
type GraphReview func([]Module, []Import) error

var bareSpecifier = regexp.MustCompile(`^[A-Za-z0-9@_][A-Za-z0-9@_./-]*$`)

func reservedIdentifier(value string) bool {
	return value == "__proto__" || value == "prototype" || value == "constructor" || value == "." || value == ".."
}

type Graph struct {
	manifest string
	digest   string
	modules  []Module
	imports  []Import
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
func digestBytes(data []byte) string { hash := sha256.Sum256(data); return hex.EncodeToString(hash[:]) }
func cloneModules(modules []Module) []Module {
	out := append([]Module(nil), modules...)
	for i := range out {
		out[i].Bytes = bytes.Clone(out[i].Bytes)
	}
	return out
}

// AdmitGraph validates bounds and hashes, calls the explicit host reviewer on a
// detached snapshot, and captures a separate immutable snapshot for custody.
func AdmitGraph(manifestDigest string, modules []Module, imports []Import, review GraphReview) (*Graph, error) {
	if !validDigest(manifestDigest) || review == nil || len(modules) == 0 || len(modules) > MaxGraphArtifacts || len(imports) > MaxGraphArtifacts {
		return nil, errors.New("pluginui: missing or oversized reviewed graph")
	}
	modules = cloneModules(modules)
	imports = append([]Import(nil), imports...)
	ids := make(map[string]bool, len(modules))
	total := 0
	for _, module := range modules {
		if !token.MatchString(module.ID) || reservedIdentifier(module.ID) || len(module.ID) > 128 || ids[module.ID] || len(module.Bytes) == 0 || len(module.Bytes) > MaxArtifactBytes || !utf8.Valid(module.Bytes) || !validDigest(module.SHA256) || digestBytes(module.Bytes) != module.SHA256 {
			return nil, errors.New("pluginui: invalid module inventory")
		}
		ids[module.ID] = true
		total += len(module.Bytes)
		if total > MaxGraphBytes {
			return nil, errors.New("pluginui: graph exceeds byte bound")
		}
	}
	if !ids["plugin"] {
		return nil, errors.New("pluginui: missing plugin module")
	}
	specifiers := make(map[string]bool, len(imports))
	for _, entry := range imports {
		if !bareSpecifier.MatchString(entry.Specifier) || entry.Specifier == "import.meta" || strings.Contains(entry.Specifier, "//") || strings.Contains(entry.Specifier, "..") || reservedIdentifier(entry.Specifier) || len(entry.Specifier) > 256 || !utf8.ValidString(entry.Specifier) || specifiers[entry.Specifier] || !ids[entry.Artifact] {
			return nil, errors.New("pluginui: invalid import mapping")
		}
		specifiers[entry.Specifier] = true
	}
	if err := review(cloneModules(modules), append([]Import(nil), imports...)); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(struct {
		Manifest string
		Modules  []Module
		Imports  []Import
	}{manifestDigest, modules, imports})
	if err != nil {
		return nil, err
	}
	return &Graph{manifest: manifestDigest, digest: digestBytes(encoded), modules: modules, imports: imports}, nil
}
func (g *Graph) Digest() string         { return g.digest }
func (g *Graph) ManifestDigest() string { return g.manifest }
func (g *Graph) Snapshot() ([]Module, []Import) {
	return cloneModules(g.modules), append([]Import(nil), g.imports...)
}
