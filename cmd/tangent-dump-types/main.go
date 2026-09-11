// Command tangent-dump-types emits a JSON description of the envelope
// registry Tangent actually serves. It is the input to
// scripts/generate-envelope-types.mjs, which transforms the JSON into
// TypeScript types committed under ui/src/generated/.
//
// The dump tool is the bridge between two source-of-truth files that
// neither side directly owns:
//   - go-envelopes' embedded YAML manifest defines the core catalog.
//   - internal/envelope/extensions defines the Tangent-owned kinds.
//   - Tangent's TS UI needs typed access to both without re-parsing the
//     manifests itself.
//
// Running this binary loads the registry exactly the way the Tangent
// server does — envelope.New (go-envelopes LoadCore) followed by
// extensions.RegisterAll — then walks Service.All() to produce a stable,
// sorted JSON document. Going through the same RegisterAll the server
// uses is the mechanism that stops the two drifting apart: a kind added
// to the server is a kind this dump sees, without a second edit here.
// (ADR 0003 §9 S1.)
//
// Core schemas come from go-envelopes' embedded filesystem; extension
// schemas come from the exact registration bytes the service retained
// (Service.LookupDefinitionMaterial), which is the same material the
// interaction catalog content-addresses.
//
// Wire shape (one JSON document per run):
//
//	{
//	  "envelopesVersion": "v0.4.0",
//	  "types": [
//	    {
//	      "name": "info-card",
//	      "version": "1.0.0",
//	      "responseKind": "data",
//	      "description": "...",
//	      "source": "core",
//	      "pluginId": "",
//	      "hasSchema": true,
//	      "ui": {"component": "...", "export": "...", "props": "..."},
//	      "schemaPath": "manifest/schemas/info-card.schema.json"
//	    },
//	    ...
//	  ]
//	}
//
// schemaPath is a provenance breadcrumb only — for core kinds it is the
// path inside the embedded manifest filesystem, for Tangent kinds it is
// the plugin:// resource identity go-envelopes assigned at registration.
// The schemas themselves are inlined so the Node generator never has to
// traverse the module cache.
//
// Every Tangent-owned kind additionally carries its manifest identity —
// revision, manifest digest, contract digest, package, ownership class, and
// renderer binding — and the document carries the registry-wide
// definitionSourceDigest. That is what lets a generated artifact be stamped
// with the source that produced it, so drift is *detected* rather than
// inferred from a successful build (ADR 0003 §4).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"

	envelopes "github.com/hollis-labs/go-envelopes"
	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/plugins"
)

// envelopesModulePath is the module whose version the JSON dump banner
// reports. The Node generator embeds that version in the generated file
// banner so a reviewer can see at a glance which version produced the
// committed types.
const envelopesModulePath = "github.com/hollis-labs/go-envelopes"

// envelopesVersion reads the selected go-envelopes version out of the
// binary's own build info rather than repeating it here. The previous
// hand-maintained constant carried the instruction "bump in lockstep with
// go.mod", which is the kind of manual sync that silently rots: it still
// read v0.1.0 while go.mod had moved on, so every regenerated artifact
// was stamped with a version that had not produced it. Reading build info
// cannot drift, because it is the same module graph the dump was built
// from.
//
// Returns "unknown" when build info is unavailable (an interpreter or a
// test binary stripped of module data) or when the module is absent from
// the graph. That is deliberately a visible non-version rather than a
// plausible-looking default — a wrong version in a provenance banner is
// worse than an obviously missing one.
func envelopesVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if info.Main.Path == envelopesModulePath && info.Main.Version != "" {
		return info.Main.Version
	}
	for _, dep := range info.Deps {
		if dep == nil || dep.Path != envelopesModulePath {
			continue
		}
		// A replaced module reports the replacement's version; that is
		// the one that actually produced these types.
		if dep.Replace != nil && dep.Replace.Version != "" {
			return dep.Replace.Version
		}
		if dep.Version != "" {
			return dep.Version
		}
		return "unknown"
	}
	return "unknown"
}

// dumpType is the JSON shape emitted per registered envelope type.
type dumpType struct {
	Name         string `json:"name"`
	Version      string `json:"version,omitempty"`
	ResponseKind string `json:"responseKind"`
	Description  string `json:"description,omitempty"`
	Source       string `json:"source"`
	// PluginID is empty for core kinds and "tangent" for the in-tree
	// extensions. The generator uses it only for the banner counts.
	PluginID  string         `json:"pluginId,omitempty"`
	HasSchema bool           `json:"hasSchema"`
	UI        map[string]any `json:"ui,omitempty"`
	// SchemaPath records where the schema came from: a path inside the
	// go-envelopes embedded filesystem for core kinds, the plugin://
	// resource identity for extension kinds. Empty when HasSchema is
	// false.
	SchemaPath string `json:"schemaPath,omitempty"`
	// Schema is the raw JSON Schema document (parsed). Inlined so the
	// Node generator does not need to traverse the Go module cache to
	// resolve schemas. Empty when HasSchema is false.
	Schema map[string]any `json:"schema,omitempty"`

	// Definition is the manifest identity, present only for kinds that ship
	// an authored manifest. Core kinds have none: go-envelopes retains no
	// source bytes for them, so there is nothing to digest.
	Definition *dumpDefinition `json:"definition,omitempty"`
}

// dumpDefinition is the manifest half of one dumped kind. It carries no schema
// bodies — those are already in Schema — only the identity a generated artifact
// needs to stamp itself and the renderer binding a generated registry needs to
// key on.
type dumpDefinition struct {
	Revision       int64  `json:"revision"`
	Title          string `json:"title,omitempty"`
	ManifestDigest string `json:"manifestDigest"`
	ContractDigest string `json:"contractDigest"`
	PackageID      string `json:"packageId"`
	PackageVersion string `json:"packageVersion"`
	OwnershipClass string `json:"ownershipClass"`
	// State is the materialization state. A kind that is registered but not
	// available still appears in the dump, and the generated artifact records
	// the state rather than silently omitting the kind.
	State string `json:"state"`
	// RendererID / RendererClass / RendererEntry / RendererIsolation replace
	// ui/src/main.tsx's string-literal registration as the authority for what
	// draws a kind (ADR 0003 §4.5).
	RendererID    string `json:"rendererId"`
	RendererClass string `json:"rendererClass"`
	RendererEntry string `json:"rendererEntry"`
	// RendererIsolation is where the *granted* trust class runs the renderer
	// (CW-20260825-0073). It is derived by the host from the granted class and
	// dumped rather than re-derived in TypeScript, because a second mapping is
	// a second answer.
	RendererIsolation string `json:"rendererIsolation"`
	// InlinePayloadLimitBytes is §2.6's publisher limit after the host ceiling.
	// The SPA reads it to bound untrusted display content before it reaches a
	// renderer, which is the browser half of the same limit.
	InlinePayloadLimitBytes int64 `json:"inlinePayloadLimitBytes"`
	// Fallback is §2.3's declared safe fallback, projected exactly as
	// Materialized.SafeFallback decides it: a renderer id appears only when the
	// publisher declared one and preserves_meaning is true.
	FallbackRendererID       string `json:"fallbackRendererId,omitempty"`
	FallbackPreservesMeaning bool   `json:"fallbackPreservesMeaning"`
	FallbackDegradation      string `json:"fallbackDegradation,omitempty"`
	// CompatibilityResponseSchema is "present" or "absent" (ADR 0003 §8 C4).
	CompatibilityResponseSchema string `json:"compatibilityResponseSchema"`
	// NamedDefinitions are the stable $defs entry points and what each is for.
	NamedDefinitions map[string]string `json:"namedDefinitions,omitempty"`
	// DefsDigest is the digest of the request schema's $defs sub-document, when
	// it has one. It is what a hand-written adapter over that bundle stamps
	// itself with — see ADR 0003 §4.7 and ui/src/lib/hitl-api.ts.
	DefsDigest string `json:"defsDigest,omitempty"`
}

type dumpDoc struct {
	EnvelopesVersion string `json:"envelopesVersion"`
	// HostVersion is the release definitions declare compatibility against.
	HostVersion string `json:"hostVersion"`
	// DefinitionSourceDigest is the @definition-source stamp for the whole
	// registry: a stable hash over the ordered (kind, version, revision,
	// manifest_digest) set. A generated artifact carries it, and
	// tangent.definition_registry_list reports the live value, so a client can
	// refuse to submit against a definition it was not generated for.
	DefinitionSourceDigest string `json:"definitionSourceDigest"`
	// TrustProfiles is the whole renderer trust model, dumped so the SPA's copy
	// is generated from the host's table rather than hand-mirrored. ADR 0003
	// §2.3 makes Tangent policy the decider of a trust class; a second,
	// hand-typed policy table in TypeScript would be a second decider.
	TrustProfiles []dumpTrustProfile `json:"trustProfiles"`
	Types         []dumpType         `json:"types"`
}

// dumpTrustProfile is one renderer trust class's projection: where it runs,
// whether publisher code executes there, whether that place carries Tangent's
// ambient authority, and the host-mediated effect capabilities the class may
// ever declare.
type dumpTrustProfile struct {
	Isolation             string   `json:"isolation"`
	ExecutesPublisherCode bool     `json:"executesPublisherCode"`
	AmbientHostAuthority  bool     `json:"ambientHostAuthority"`
	RendererClasses       []string `json:"rendererClasses"`
}

// trustProfiles projects internal/definition's isolation table for the dump.
func trustProfiles() []dumpTrustProfile {
	out := make([]dumpTrustProfile, 0, len(definition.Isolations()))
	for _, isolation := range definition.Isolations() {
		profile, ok := definition.IsolationProfileFor(isolation)
		if !ok {
			continue
		}
		rendererClasses := make([]string, 0, len(profile.RendererClasses))
		for _, rendererClass := range profile.RendererClasses {
			rendererClasses = append(rendererClasses, string(rendererClass))
		}
		out = append(out, dumpTrustProfile{
			Isolation:             string(profile.Isolation),
			ExecutesPublisherCode: profile.ExecutesPublisherCode,
			AmbientHostAuthority:  profile.Isolation.AmbientHostAuthority(),
			RendererClasses:       rendererClasses,
		})
	}
	return out
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "tangent-dump-types: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	svc, err := envelope.New(context.Background())
	if err != nil {
		return fmt.Errorf("load core registry: %w", err)
	}
	if regErr := extensions.RegisterAll(svc); regErr != nil {
		return fmt.Errorf("register tangent extensions: %w", regErr)
	}
	// The plugin door too (ADR 0007 §4). Skipping it here would leave every
	// plugin-contributed kind out of the generated TypeScript while the server
	// served it — which is exactly the two-registries drift this tool going
	// through the same registration path exists to prevent.
	if _, pluginErr := plugins.LoadShipped(
		context.Background(), slog.New(slog.DiscardHandler), svc,
	); pluginErr != nil {
		return fmt.Errorf("load shipped plugins: %w", pluginErr)
	}
	manifestFS := envelopes.EmbeddedFS()

	sourceDigest, digestErr := svc.DefinitionSourceDigest()
	if digestErr != nil {
		return fmt.Errorf("derive definition source digest: %w", digestErr)
	}

	specs := svc.All()
	out := dumpDoc{
		EnvelopesVersion:       envelopesVersion(),
		HostVersion:            envelope.HostVersion,
		DefinitionSourceDigest: sourceDigest,
		TrustProfiles:          trustProfiles(),
		Types:                  make([]dumpType, 0, len(specs)),
	}
	for _, spec := range specs {
		dt := dumpType{
			Name:         spec.Name,
			Version:      spec.Version,
			ResponseKind: string(spec.ResponseKind),
			Description:  spec.Description,
			Source:       spec.Source.String(),
			PluginID:     spec.PluginID,
			HasSchema:    spec.DataSchema != nil,
			UI:           spec.UIMetadata,
		}
		if dt.HasSchema {
			dt.SchemaPath, dt.Schema, err = schemaFor(svc, manifestFS, spec)
			if err != nil {
				return fmt.Errorf("read schema for %q: %w", spec.Name, err)
			}
		}
		if material, ok := svc.LookupDefinitionMaterial(spec.Name); ok && material.Definition != nil {
			dt.Definition = describeDefinition(*material.Definition, material.RequestSchema)
		}
		out.Types = append(out.Types, dt)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode dump: %w", err)
	}
	return nil
}

// schemaFor resolves one type's request schema. Extension kinds are
// answered from the retained registration bytes — go-envelopes discards
// schema source after compiling, so the service's own material table is
// the only place those bytes survive. Core kinds fall back to the
// embedded manifest filesystem, which is the only source they have.
//
// A registered extension with no retained material is a bug in the
// registration path rather than a missing file, so it is an error rather
// than a silent skip.
func schemaFor(
	svc *envelope.Service,
	manifestFS fs.FS,
	spec envelopes.TypeSpec,
) (string, map[string]any, error) {
	if material, ok := svc.LookupDefinitionMaterial(spec.Name); ok {
		doc, err := parseSchema(material.RequestSchema)
		if err != nil {
			return "", nil, err
		}
		// Location carries an empty JSON-pointer fragment ("...json#");
		// trim it so the emitted path reads as the resource identity.
		return strings.TrimSuffix(spec.DataSchema.Location, "#"), doc, nil
	}
	if spec.Source != envelopes.TypeSourceCore {
		return "", nil, fmt.Errorf("no retained registration material for non-core type")
	}
	path := "manifest/schemas/" + spec.Name + ".schema.json"
	raw, err := fs.ReadFile(manifestFS, path)
	if err != nil {
		return "", nil, err
	}
	doc, err := parseSchema(raw)
	if err != nil {
		return "", nil, err
	}
	return path, doc, nil
}

// describeDefinition projects one materialized manifest into the dump. It
// deliberately reads from the same materialization the server holds rather than
// re-parsing the manifest file: a generator that parsed the tree itself could
// disagree with the running host, which is the class of drift this whole
// pipeline exists to make impossible.
func describeDefinition(materialized definition.Materialized, requestSchema []byte) *dumpDefinition {
	manifest := materialized.Manifest
	out := &dumpDefinition{
		Revision:       manifest.Revision,
		Title:          manifest.Title,
		ManifestDigest: materialized.Derived.ManifestDigest,
		ContractDigest: materialized.Derived.ContractDigest,
		PackageID:      manifest.PackageID,
		PackageVersion: manifest.PackageVersion,
		OwnershipClass: string(manifest.OwnershipClass),
		State:          string(materialized.State),

		RendererID:        manifest.Renderer.ID,
		RendererClass:     string(manifest.Renderer.Class),
		RendererEntry:     manifest.Renderer.Entry,
		RendererIsolation: string(materialized.Isolation),

		InlinePayloadLimitBytes:  materialized.EffectiveInlineLimitBytes,
		FallbackPreservesMeaning: manifest.Renderer.Fallback.PreservesMeaning,
		FallbackDegradation:      string(manifest.Renderer.Fallback.Degradation),

		CompatibilityResponseSchema: string(manifest.CompatibilityResponseSchema),
		NamedDefinitions:            manifest.NamedDefinitions,
		DefsDigest:                  schemaDefsDigest(requestSchema),
	}
	if out.CompatibilityResponseSchema == "" {
		out.CompatibilityResponseSchema = string(definition.ResponseSchemaPresent)
	}
	// Named only when Tangent is actually permitted to use it (§8 C5). A
	// declared fallback whose preserves_meaning is false is not a fallback, and
	// putting its id in a generated table would invite a consumer to render it.
	if fallback, ok := materialized.SafeFallback(); ok {
		out.FallbackRendererID = fallback.RendererID
	}
	return out
}

// schemaDefsDigest digests one schema's $defs sub-document, or returns empty
// when it has none. Re-encoding the decoded map rather than slicing the source
// bytes is what makes the digest stable against formatting: a reindented schema
// file must not read as a contract change.
func schemaDefsDigest(schema []byte) string {
	if len(schema) == 0 {
		return ""
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(schema, &document); err != nil {
		return ""
	}
	defs, ok := document["$defs"]
	if !ok {
		return ""
	}
	var normalized map[string]any
	if err := json.Unmarshal(defs, &normalized); err != nil {
		return ""
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return ""
	}
	return definition.Digest(encoded)
}

// parseSchema decodes schema bytes as a generic JSON document so the Node
// generator can transform them without re-parsing.
func parseSchema(raw []byte) (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	return doc, nil
}
