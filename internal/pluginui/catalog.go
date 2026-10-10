// Package pluginui holds host-admitted, immutable browser artifacts. It does not
// discover plugins, grant authority, execute JavaScript or weaken frame policy.
package pluginui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"sync"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/registry"
)

const MaxArtifactBytes = 8 << 20

var token = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var exportName = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// Declaration is a host-reviewed contribution, never a child registration call.
// Component surfaces run only under the exact accepted isolation.
type Declaration struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Export   string `json:"export"`
	Region   string `json:"region"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
}

type Prepared struct {
	owner        string
	isolation    string
	bundle       []byte
	style        []byte
	declarations []Declaration
}

// Prepare verifies the complete reviewed artifact tree, then captures UI bytes
// through a confined root and verifies their individual inventory digests again.
// Publication never serves mutable installation paths.
func Prepare(m manifest.Manifest, directory string, declarations []Declaration) (*Prepared, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if m.UI == nil {
		return nil, errors.New("pluginui: missing browser declaration")
	}
	if err := m.VerifyBundle(directory); err != nil {
		return nil, err
	}
	if len(declarations) == 0 || len(declarations) > 32 {
		return nil, errors.New("pluginui: invalid contribution inventory")
	}
	seen := map[string]bool{}
	for _, d := range declarations {
		if !token.MatchString(d.Key) || !exportName.MatchString(d.Export) || len(d.Key) > 128 || len(d.Export) > 128 || len(d.Title) > 128 || seen[d.Key] {
			return nil, errors.New("pluginui: invalid or duplicate contribution")
		}
		if (d.Kind != "panel" && d.Kind != "widget") || d.Region != "right" {
			return nil, errors.New("pluginui: unsupported contribution kind or region")
		}
		seen[d.Key] = true
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	read := func(name string) ([]byte, error) {
		f, openErr := root.Open(name)
		if openErr != nil {
			return nil, openErr
		}
		defer f.Close()
		data, readErr := io.ReadAll(io.LimitReader(f, MaxArtifactBytes+1))
		if readErr != nil {
			return nil, readErr
		}
		if len(data) > MaxArtifactBytes {
			return nil, errors.New("pluginui: artifact exceeds bound")
		}
		// SDK inventory uses raw hex; registry bundle identity carries its algorithm.
		digest := registry.BundleDigest(data)[len("sha256:"):]
		for _, item := range m.Artifact.Files {
			if item.Path == name && item.SHA256 == digest {
				return data, nil
			}
		}
		return nil, registry.ErrIntegrity
	}
	bundle, err := read(m.UI.Bundle)
	if err != nil {
		return nil, err
	}
	var style []byte
	if m.UI.Stylesheet != "" {
		style, err = read(m.UI.Stylesheet)
		if err != nil {
			return nil, err
		}
	}
	return &Prepared{m.ID, m.UI.Isolation, bundle, style, append([]Declaration(nil), declarations...)}, nil
}

type admission struct {
	prepared   *Prepared
	generation string
	live       func() bool
}

type Catalog struct {
	mu       sync.Mutex
	host     string
	revision uint64
	owners   map[string]admission
	last     []byte
}

func New(hostInstance string) (*Catalog, error) {
	if hostInstance == "" {
		return nil, errors.New("pluginui: missing host instance")
	}
	return &Catalog{host: hostInstance, revision: 1, owners: map[string]admission{}}, nil
}

// Publish is called by the host after successful activation and explicit
// isolation admission. Main-origin code has ambient document authority: it is
// refused unless the host explicitly selects it. Frame isolation requires the
// actual frame runtime; this catalog never substitutes main-origin execution.
func (c *Catalog) Publish(p *Prepared, generation string, allowMainOrigin bool, live func() bool) error {
	if p == nil || !token.MatchString(generation) || len(generation) > 128 || live == nil {
		return errors.New("pluginui: incomplete owner binding")
	}
	if p.isolation != "main-origin" || !allowMainOrigin {
		return errors.New("pluginui: isolation runtime not admitted")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.owners[p.owner]; exists {
		return errors.New("pluginui: owner already published")
	}
	c.owners[p.owner] = admission{p, generation, live}
	return nil
}

// Withdraw is exact-generation scoped; a late old-owner cleanup cannot erase
// its replacement. Already imported main-origin code remains cooperative.
func (c *Catalog) Withdraw(owner, generation string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a, ok := c.owners[owner]; ok && a.generation == generation {
		delete(c.owners, owner)
	}
}

// Response projects only live, admitted owners. URLs identify exact immutable
// bytes and generation. The revision moves only when that visible snapshot moves.
func (c *Catalog) Response() registry.Response {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := registry.NewResponse(c.host, c.revision)
	schema := json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"priority":{"type":"integer"}},"required":["title","priority"],"additionalProperties":false}`)
	for _, kind := range []string{"panel", "widget"} {
		r.Kinds[kind] = registry.KindDescriptor{SchemaVersion: 1, MetadataSchema: schema, Representations: []registry.Representation{registry.Component}, Regions: []string{"right"}, RequiredCapabilities: []string{}}
	}
	r.Regions["right"] = registry.RegionDescriptor{Kinds: []string{"panel", "widget"}, Representations: []registry.Representation{registry.Component}, ContextSchema: json.RawMessage(`{"type":"object"}`), Ordering: "priority-ascending"}
	ids := make([]string, 0, len(c.owners))
	for id := range c.owners {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := c.owners[id]
		if !a.live() {
			continue
		}
		digest := registry.BundleDigest(a.prepared.bundle)
		base := fmt.Sprintf("/api/plugin-ui/%s/%s/%s", id, a.generation, digest[len("sha256:"):])
		plugin := registry.Plugin{OwnerGeneration: a.generation, BundleURL: base + "/bundle.js", BundleVersion: digest}
		if len(a.prepared.style) > 0 {
			plugin.StylesheetURL = base + "/style.css"
		}
		r.Plugins[id] = plugin
		for _, d := range a.prepared.declarations {
			metadata, _ := json.Marshal(struct {
				Title    string `json:"title"`
				Priority int    `json:"priority"`
			}{d.Title, d.Priority})
			_ = r.Set(registry.Contribution{Status: registry.StatusAccepted, OwnerID: id, OwnerGeneration: a.generation, LocalKey: d.Key, Kind: d.Kind, SchemaVersion: 1, Representation: registry.Component, Metadata: metadata, Component: &registry.ComponentRef{Export: d.Export, Region: d.Region}})
		}
	}
	// Compare without revision, which is the consequence rather than the input.
	raw, _ := json.Marshal(r)
	if c.last != nil && !bytes.Equal(raw, c.last) {
		c.revision++
		r.Revision = c.revision
		raw, _ = json.Marshal(r)
	}
	c.last = raw
	return r
}

// Artifact rechecks the actual owner lease at delivery. Returned bytes are a
// copy so callers cannot alter future responses or verified digest identity.
func (c *Catalog) Artifact(owner, generation, digest, name string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.owners[owner]
	if !ok || a.generation != generation || !a.live() || registry.BundleDigest(a.prepared.bundle) != "sha256:"+digest {
		return nil, false
	}
	switch name {
	case "bundle.js":
		return bytes.Clone(a.prepared.bundle), true
	case "style.css":
		if len(a.prepared.style) > 0 {
			return bytes.Clone(a.prepared.style), true
		}
	}
	return nil, false
}
