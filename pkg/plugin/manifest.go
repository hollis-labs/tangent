package plugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"

	sdkmanifest "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
)

const (
	ManifestName = sdkmanifest.Filename
	// ContractVersion versions the public Tangent plugin declaration contract,
	// independently of the application release and interaction definitions.
	ContractVersion = "1.0.0"
	// BinaryContractVersion is the host's native subprocess runner contract.
	BinaryContractVersion = "1.0.0"
	TangentSchemaVersion  = 1
)

// Manifest retains the reviewed SDK declaration and its validated host bindings.
// It is not an interaction definition or an authorization grant.
type Manifest struct {
	sdkmanifest.Manifest
	Bindings TangentExtension
}

type ToolDecl = sdkmanifest.Tool

type TangentExtension struct {
	SchemaVersion int         `json:"schema_version"`
	Kinds         []KindRef   `json:"kinds,omitempty"`
	Routes        []RouteDecl `json:"routes,omitempty"`
	MCPTools      []string    `json:"mcp_tools,omitempty"`
}

type KindRef struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Package string `json:"package"`
}

type RouteDecl struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Capability string `json:"capability"`
}

// DecodeManifest is the only process-manifest decoder. Broader YAML, legacy
// entrypoints and unknown/duplicate/null/trailing fields have no fallback.
func DecodeManifest(r io.Reader) (Manifest, error) {
	common, err := sdkmanifest.Decode(r)
	if err != nil {
		return Manifest{}, fmt.Errorf("plugin: %s: %w", ManifestName, err)
	}
	var bindings TangentExtension
	if err := sdkmanifest.DecodeExtension(common.Tangent, &bindings); err != nil {
		return Manifest{}, fmt.Errorf("plugin: %s tangent: %w", common.ID, err)
	}
	m := Manifest{Manifest: common, Bindings: bindings}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

var declarationName = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)
var manifestToolName = regexp.MustCompile(`^tangent\.[a-z][a-z0-9_-]*$`)
var manifestRoutePath = regexp.MustCompile(`^/api/plugins/[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._/-]*$`)

func (m Manifest) Validate() error {
	if checkErr := m.Manifest.Validate(); checkErr != nil {
		return fmt.Errorf("plugin: %s: %w", m.ID, checkErr)
	}
	if checkErr := m.validateBindings(); checkErr != nil {
		return checkErr
	}
	// Bindings must match the reviewed raw declaration, including when host Go
	// code constructs a Manifest rather than calling DecodeManifest.
	var raw TangentExtension
	if checkErr := sdkmanifest.DecodeExtension(m.Tangent, &raw); checkErr != nil {
		return fmt.Errorf("plugin: %s tangent: %w", m.ID, checkErr)
	}
	if raw.SchemaVersion != TangentSchemaVersion {
		return fmt.Errorf("plugin: %s unsupported tangent schema_version %d", m.ID, raw.SchemaVersion)
	}
	if m.Server.Runtime != "binary" {
		return fmt.Errorf("plugin: %s unsupported server runtime %q; Tangent runs native binary bundles", m.ID, m.Server.Runtime)
	}
	if m.UI != nil || len(m.Hooks) != 0 {
		return fmt.Errorf("plugin: %s browser assets and hooks require separate host adoption", m.ID)
	}
	seenKinds := map[string]bool{}
	for _, ref := range raw.Kinds {
		if !declarationName.MatchString(ref.Kind) || !declarationName.MatchString(ref.Package) || strings.TrimSpace(ref.Version) == "" || seenKinds[ref.Kind] {
			return fmt.Errorf("plugin: %s invalid or duplicate kind reference %q", m.ID, ref.Kind)
		}
		seenKinds[ref.Kind] = true
	}
	tools := map[string]bool{}
	for _, tool := range m.Tools {
		if !manifestToolName.MatchString(tool.Name) {
			return fmt.Errorf("plugin: %s invalid Tangent tool name %q", m.ID, tool.Name)
		}
		switch tool.Effect {
		case "read", "write", "destructive":
		default:
			return fmt.Errorf("plugin: %s tool %s has unsupported effect %q", m.ID, tool.Name, tool.Effect)
		}
		tools[tool.Name] = true
	}
	refs := map[string]bool{}
	for _, name := range raw.MCPTools {
		if !tools[name] || refs[name] {
			return fmt.Errorf("plugin: %s unknown or duplicate MCP binding %q", m.ID, name)
		}
		refs[name] = true
	}
	// No common declaration may accidentally be advertised outside its host binding.
	if len(refs) != len(tools) {
		return fmt.Errorf("plugin: %s every common tool must have one tangent MCP binding", m.ID)
	}
	seenRoutes := map[string]bool{}
	owner := m.ID[strings.LastIndex(m.ID, ".")+1:]
	for _, route := range raw.Routes {
		key := route.Method + " " + route.Path
		segment := strings.Split(strings.TrimPrefix(route.Path, RoutePrefix), "/")[0]
		if (route.Method != http.MethodGet && route.Method != http.MethodPost) || !manifestRoutePath.MatchString(route.Path) || path.Clean(route.Path) != route.Path || seenRoutes[key] || (segment != owner && !strings.HasPrefix(segment, owner+"-")) {
			return fmt.Errorf("plugin: %s invalid, duplicate or foreign route %q", m.ID, key)
		}
		switch route.Capability {
		case "view", "draft", "resolve", "cancel":
		default:
			return fmt.Errorf("plugin: %s route %s has unholdable participant capability %q", m.ID, key, route.Capability)
		}
		seenRoutes[key] = true
	}
	return nil
}

// EncodeManifest emits the SDK declaration after checking its Tangent bindings.
// Artifact inventory must already cover the built payload; this grants nothing.
func EncodeManifest(w io.Writer, common sdkmanifest.Manifest, bindings TangentExtension) error {
	raw, err := json.Marshal(bindings)
	if err != nil {
		return err
	}
	common.Tangent = raw
	m := Manifest{Manifest: common, Bindings: bindings}
	if checkErr := m.Validate(); checkErr != nil {
		return checkErr
	}
	return sdkmanifest.Encode(w, common)
}

// validateBindings prevents a mutated projection from bypassing the reviewed raw
// declaration while retaining the SDK's strict extension decoder.
func (m Manifest) validateBindings() error {
	raw, err := json.Marshal(m.Bindings)
	if err != nil {
		return err
	}
	var reviewed TangentExtension
	if checkErr := sdkmanifest.DecodeExtension(m.Tangent, &reviewed); checkErr != nil {
		return checkErr
	}
	canonical, err := json.Marshal(reviewed)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, canonical) {
		return fmt.Errorf("plugin: %s tangent bindings differ from reviewed declaration", m.ID)
	}
	return nil
}

// EncodeNativeManifest inventories one built native executable. Call it with
// the final executable bytes, before writing plugin.yaml into a fresh bundle.
// Additional payload files need an explicit SDK artifact inventory instead.
func EncodeNativeManifest(w io.Writer, payload []byte, entry string, common sdkmanifest.Manifest, bindings TangentExtension) error {
	digest := sha256.Sum256(payload)
	files := []sdkmanifest.ArtifactFile{{Path: entry, SHA256: hex.EncodeToString(digest[:]), Executable: true}}
	common.SchemaVersion = sdkmanifest.SchemaVersion
	common.Protocol = sdkmanifest.RequiredProtocol
	common.Runtime = sdkmanifest.Runtime
	common.Server = sdkmanifest.Server{Runtime: "binary", Entry: entry, Engines: map[string]sdkmanifest.HostRange{"binary": {Min: BinaryContractVersion, Max: "1.99.99"}}}
	common.Hosts = map[string]sdkmanifest.HostRange{"tangent": {Min: ContractVersion, Max: "1.99.99"}}
	tree, err := sdkmanifest.TreeDigest(files)
	if err != nil {
		return err
	}
	common.Artifact = sdkmanifest.Artifact{Files: files, TreeSHA256: tree}
	return EncodeManifest(w, common, bindings)
}
