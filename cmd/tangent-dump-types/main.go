// Command tangent-dump-types emits a JSON description of the loaded
// go-envelopes core registry on stdout. It is the input to
// scripts/generate-envelope-types.mjs, which transforms the JSON into
// TypeScript types committed under ui/src/generated/.
//
// The dump tool is the bridge between two source-of-truth files that
// neither side directly owns:
//   - go-envelopes' embedded YAML manifest defines the catalog.
//   - go-envelopes' embedded per-type JSON Schemas define payload shapes.
//   - Tangent's TS UI needs typed access to both without re-parsing the
//     manifest itself.
//
// Running this binary loads the registry exactly the way the Tangent
// server does (envelopes.LoadCore), then walks Registry.All() to produce
// a stable, sorted JSON document. The Node script consumes that JSON and
// also reads the embedded YAML's accompanying schema files via the
// envelope package's own filesystem (see compileSchemaFromFS in
// go-envelopes/manifest.go). For Tangent we don't need to re-emit the
// raw schemas — the registry already exposes UIMetadata, and the
// frontend generator reads schemas from the manifest filesystem.
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
//	      "hasSchema": true,
//	      "ui": {"component": "...", "export": "...", "props": "..."},
//	      "schemaPath": "manifest/schemas/info-card.schema.json"
//	    },
//	    ...
//	  ]
//	}
//
// schemaPath is included so the Node script can resolve the schema files
// relative to a lockfile-derived path or via "go env GOPATH" lookup. To
// keep the TS-side simple, schemas themselves are inlined too.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"

	envelopes "github.com/hollis-labs/go-envelopes"
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
	Name         string         `json:"name"`
	Version      string         `json:"version,omitempty"`
	ResponseKind string         `json:"responseKind"`
	Description  string         `json:"description,omitempty"`
	Source       string         `json:"source"`
	HasSchema    bool           `json:"hasSchema"`
	UI           map[string]any `json:"ui,omitempty"`
	// SchemaPath is the path within the embedded manifest filesystem at
	// which the per-type schema lives. Empty when HasSchema is false.
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
	reg, err := envelopes.LoadCore(context.Background())
	if err != nil {
		return fmt.Errorf("load core registry: %w", err)
	}
	manifestFS := envelopes.EmbeddedFS()

	specs := reg.All()
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
			HasSchema:    spec.DataSchema != nil,
			UI:           spec.UIMetadata,
		}
		if dt.HasSchema {
			schemaPath := "manifest/schemas/" + spec.Name + ".schema.json"
			dt.SchemaPath = schemaPath
			schema, err := readSchema(manifestFS, schemaPath)
			if err != nil {
				return fmt.Errorf("read schema for %q: %w", spec.Name, err)
			}
			dt.Schema = schema
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

// readSchema parses the embedded schema file as a generic JSON document
// so the Node generator can transform it without re-parsing.
func readSchema(f fs.FS, path string) (map[string]any, error) {
	raw, err := fs.ReadFile(f, path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	return doc, nil
}
