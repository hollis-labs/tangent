// Package plugintemplate is the scaffold every Tangent plugin after the second
// one starts from (CW-20260910-0035).
//
// # It is extracted, not designed
//
// Three plugins exist and the template is what they have in common:
// internal/plugins/appboard contributes a domain-free kind, and
// internal/plugins/torqueboard and internal/plugins/tesseract fill it with two
// different applications' records. Everything here was measured off those
// three. Nothing here anticipates a fourth.
//
// The two presets are that split, and keeping them apart is the point rather
// than a packaging convenience:
//
//   - PresetApplication puts an application's records on a kind that already
//     exists. It authors no manifest, contributes no kind, and ships no
//     renderer. Both working application plugins are this shape, and the
//     decision record `app_plugins_are_self_contained` says the next one should
//     be too: reuse a domain-free kind, map in userland, contribute no new kind
//     unless a surface genuinely cannot fit one.
//   - PresetKind contributes a kind. It authors an ADR 0003 manifest, a request
//     schema and a renderer, and it knows nothing about any application.
//
// A plugin that did both would be a kind with one application's concepts in it,
// which is the specific way this boundary rots. The generator refuses to emit
// both at once for that reason, and says so.
//
// # The boundary the second plugin corrected
//
// From docs/architecture.md, and it is encoded here rather than restated:
//
//	A plugin applies what is mechanical in the owning application's own
//	terms, and hands back what that application makes an authored act. Which
//	side a disposition falls on is the application's answer, not the
//	plugin's.
//
// The earlier wording — "a sync applies what is mechanical" — described Torque,
// where every disposition a board offers completes inside Torque's own API. It
// did not describe application plugins. Tesseract makes promotion an authored
// write, so the same board shape has to hand that back. Options.HandsBackWork
// is that question asked at scaffold time, because answering it later means
// discovering it in production.
//
// docs/writing-a-plugin.md carries the rest: the eight measured differences
// between the two application plugins, which of them is essential, which was
// incidental to its application, and which is a trap this scaffold prevents.
package plugintemplate

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

//go:embed all:templates
var templatesFS embed.FS

// Preset is which of the two shapes to scaffold.
type Preset string

const (
	// PresetApplication puts an application's records on an existing kind.
	PresetApplication Preset = "application"
	// PresetKind contributes an envelope kind and its renderer.
	PresetKind Preset = "kind"
)

// Presets lists what the generator will emit, for a usage message.
func Presets() []string { return []string{string(PresetApplication), string(PresetKind)} }

// identifier constrains the one option everything else is derived from.
var identifier = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// Options is one scaffold. Package is the only field a caller must set;
// Derive fills the rest from it, and a caller overrides what it disagrees with.
type Options struct {
	// Preset picks the shape. Empty means PresetApplication.
	Preset Preset

	// Package is the Go package name and the directory name, lowercase and
	// alphanumeric: `torqueboard`, `tesseract`.
	Package string
	// App is the application's human name, used in prose and in the board's
	// source label: `Torque`, `Tesseract`.
	App string
	// Slug is the route segment under /api/plugins/: `torque-board`.
	Slug string
	// Tool is the tool base name; the open tool is `tangent.<Tool>` and the
	// sync tool is `tangent.<Tool>_sync`.
	Tool string
	// EnvPrefix names the environment variables the plugin reads for itself:
	// <EnvPrefix>_API_URL and <EnvPrefix>_TOKEN.
	EnvPrefix string
	// BaseURL is where the application listens on this machine by default.
	BaseURL string
	// Kind is the envelope kind this plugin fills (application) or contributes
	// (kind).
	Kind string
	// KindPackage is the ADR 0003 package id a contributed kind ships under.
	KindPackage string
	// Task is the Torque id this plugin is being built under, for the comments
	// that cite it. Empty leaves the citation out rather than inventing one.
	Task string

	// HandsBackWork says at least one disposition this board offers is an
	// authored act in the owning application's terms, so the sync hands it
	// back to the agent instead of applying it.
	//
	// This is the one question the two working plugins answered differently
	// and the reason it is asked here. Torque's status transition is a status
	// transition: the API performs it, the write completes, and the sync is
	// stateless afterwards. Tesseract's promotion is a new immutable revision
	// with `supersedes` — the same act as a reword, which nobody would call
	// mechanical — so the plugin makes one write (a deprecation) and hands the
	// rest back. Which one your application is is your application's answer.
	HandsBackWork bool
	// Auth says the application takes a bearer token. Tesseract does and
	// Torque does not, so it is a field rather than an assumption.
	Auth bool
}

// Derive fills the fields a caller left empty and validates the result.
func (o Options) Derive() (Options, error) {
	if o.Preset == "" {
		o.Preset = PresetApplication
	}
	if o.Preset != PresetApplication && o.Preset != PresetKind {
		return o, fmt.Errorf("plugintemplate: unknown preset %q; want one of %s",
			o.Preset, strings.Join(Presets(), ", "))
	}
	if !identifier.MatchString(o.Package) {
		return o, fmt.Errorf(
			"plugintemplate: package %q must be lowercase alphanumeric starting with a letter",
			o.Package)
	}
	if o.App == "" {
		o.App = strings.ToUpper(o.Package[:1]) + o.Package[1:]
	}
	if o.Slug == "" {
		o.Slug = o.Package + "-board"
	}
	if o.Tool == "" {
		o.Tool = o.Package + "_board"
	}
	if o.EnvPrefix == "" {
		o.EnvPrefix = "TANGENT_" + strings.ToUpper(o.Package)
	}
	if o.BaseURL == "" {
		o.BaseURL = "http://127.0.0.1:8080"
	}
	if o.Kind == "" {
		if o.Preset == PresetKind {
			o.Kind = "tangent." + o.Package
		} else {
			o.Kind = "tangent.app-board"
		}
	}
	if o.KindPackage == "" {
		o.KindPackage = "tangent." + o.Package
	}
	if o.Preset == PresetKind && (o.HandsBackWork || o.Auth) {
		return o, fmt.Errorf(
			"plugintemplate: the kind preset contributes a domain-free kind and knows about " +
				"no application, so it has nothing to hand work back from or " +
				"authenticate against. Scaffold the kind and the application plugin separately")
	}
	return o, nil
}

// Title renders the plugin's display name.
func (o Options) Title() string { return o.App + " board" }

// ID renders the plugin identifier the host keys on.
func (o Options) ID() string { return "tangent.plugin." + o.Package }

// OpenTool and SyncTool render the two tool wire names.
func (o Options) OpenTool() string { return "tangent." + o.Tool }
func (o Options) SyncTool() string { return "tangent." + o.Tool + "_sync" }

// KindSlug renders the kind's directory name inside its package, which is the
// wire name with the `tangent.` prefix dropped.
func (o Options) KindSlug() string { return strings.TrimPrefix(o.Kind, "tangent.") }

// Component renders the React component name a contributed kind registers.
func (o Options) Component() string { return o.Exported() + "View" }

// Exported renders the package name as a Go exported identifier.
func (o Options) Exported() string { return strings.ToUpper(o.Package[:1]) + o.Package[1:] }

// TaskRef renders a parenthesized task citation, or nothing.
func (o Options) TaskRef() string {
	if o.Task == "" {
		return ""
	}
	return " (" + o.Task + ")"
}

// Render produces the scaffold as relative path → file bytes.
//
// Nothing is written to disk here, which is what lets the drift gate compare a
// fresh render against the committed example without touching the tree.
func Render(options Options) (map[string][]byte, error) {
	resolved, err := options.Derive()
	if err != nil {
		return nil, err
	}
	root := filepath.Join("templates", string(resolved.Preset))
	entries, err := templatesFS.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("plugintemplate: read preset %s: %w", resolved.Preset, err)
	}

	out := map[string][]byte{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		source, readErr := templatesFS.ReadFile(filepath.Join(root, name))
		if readErr != nil {
			return nil, fmt.Errorf("plugintemplate: read %s: %w", name, readErr)
		}
		rendered, renderErr := renderOne(name, source, resolved)
		if renderErr != nil {
			return nil, renderErr
		}
		if rendered == nil {
			continue
		}
		out[outputPath(name, resolved)] = rendered
	}
	return out, nil
}

// renderOne executes one template. A template that renders to nothing but
// whitespace is omitted entirely — that is how a file conditional on an option
// (the live check, the work-list carry) is left out rather than emitted empty.
func renderOne(name string, source []byte, options Options) ([]byte, error) {
	parsed, err := template.New(name).Option("missingkey=error").Parse(string(source))
	if err != nil {
		return nil, fmt.Errorf("plugintemplate: parse %s: %w", name, err)
	}
	var buffer bytes.Buffer
	if execErr := parsed.Execute(&buffer, options); execErr != nil {
		return nil, fmt.Errorf("plugintemplate: render %s: %w", name, execErr)
	}
	body := buffer.Bytes()
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	if strings.HasSuffix(name, ".go.tmpl") {
		// gofmt here rather than in the caller: a scaffold that emits
		// unformatted Go fails this repository's own lint gate on the first
		// build, which is a bad first impression of a tool whose whole job is
		// to save the author that discovery.
		formatted, fmtErr := format.Source(body)
		if fmtErr != nil {
			return nil, fmt.Errorf("plugintemplate: %s does not parse as Go: %w", name, fmtErr)
		}
		body = formatted
	}
	return body, nil
}

// outputPath maps a template file name onto the path it is written to.
//
// Two substitutions and nothing clever: `client.go.tmpl` becomes
// `<package>.go`, because the single file that knows the application exists is
// named after it and a grep for the application's name should land there; and
// the kind preset's manifest and schema go under the shipped package tree where
// internal/envelope/extensions embeds them from.
func outputPath(name string, options Options) string {
	trimmed := strings.TrimSuffix(name, ".tmpl")
	switch trimmed {
	case "client.go":
		return options.Package + ".go"
	case "plugin.go":
		if options.Preset == PresetKind {
			return filepath.Join("internal", "plugins", options.Package, trimmed)
		}
		return trimmed
	case "manifest.yaml", "request.schema.json":
		return filepath.Join("internal", "envelope", "extensions", "packages",
			options.KindPackage, options.KindSlug(), trimmed)
	case "renderer.tsx":
		return filepath.Join("ui", "src", "components", "envelopes", options.Exported()+".tsx")
	case "renderer.test.tsx":
		return filepath.Join("ui", "src", "components", "envelopes", options.Exported()+".test.tsx")
	default:
		return trimmed
	}
}

// Write renders into dir. It refuses to overwrite, because the second run of a
// scaffolder over a plugin somebody has been editing is never what was meant.
func Write(dir string, files map[string][]byte) ([]string, error) {
	written := make([]string, 0, len(files))
	for _, rel := range sortedKeys(files) {
		target := filepath.Join(dir, rel)
		if _, err := os.Stat(target); err == nil {
			return written, fmt.Errorf("plugintemplate: %s already exists; refusing to overwrite", target)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return written, fmt.Errorf("plugintemplate: create %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, files[rel], 0o600); err != nil {
			return written, fmt.Errorf("plugintemplate: write %s: %w", target, err)
		}
		written = append(written, rel)
	}
	return written, nil
}

func sortedKeys(files map[string][]byte) []string {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
