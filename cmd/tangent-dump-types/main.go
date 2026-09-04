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
//	  "envelopesVersion": "v0.1.0",
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
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"strings"

	envelopes "github.com/hollis-labs/go-envelopes"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
)

// envelopesVersion is the version line printed in the JSON dump banner.
// Matches go.mod's `require github.com/hollis-labs/go-envelopes <ver>`;
// the Node generator embeds this in the file banner so reviewers can see
// at a glance which version produced the committed types.
//
// Bump in lockstep with go.mod when upgrading the dep.
const envelopesVersion = "v0.1.0"

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
}

type dumpDoc struct {
	EnvelopesVersion string     `json:"envelopesVersion"`
	Types            []dumpType `json:"types"`
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
	manifestFS := envelopes.EmbeddedFS()

	specs := svc.All()
	out := dumpDoc{
		EnvelopesVersion: envelopesVersion,
		Types:            make([]dumpType, 0, len(specs)),
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

// parseSchema decodes schema bytes as a generic JSON document so the Node
// generator can transform them without re-parsing.
func parseSchema(raw []byte) (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	return doc, nil
}
