// Package extensions registers Tangent-owned envelope types via the
// go-envelopes plugin extension API. Tangent does NOT fork the upstream
// manifest — it ships small in-tree manifest fragments here and registers
// them at boot through Registry.RegisterTypeFromManifest with
// pluginID="tangent". When go-envelopes upstreams a kind into core (e.g.
// `triage` planned for v0.3), the corresponding fragment can be deleted
// without touching call sites.
package extensions

import (
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

// PluginID is the namespace recorded on every Tangent-registered TypeSpec.
// Used both as the registration tag (so UnregisterPlugin can sweep the
// whole set on hot-reload) and as a debugging aid in registry dumps.
const PluginID = "tangent"

// TriageEnvelopeType is the canonical wire name for the v0.1 triage
// kind, registered via the plugin extension API. The dotted-namespace
// form is required by go-envelopes' Registry — un-namespaced names are
// reserved for core types and rejected with ErrInvalidName. When core
// upstreams its own `triage` kind in v0.3, this constant flips to the
// bare name and the manifest fragment is deleted.
const TriageEnvelopeType = "tangent.triage"

// triageManifest is the YAML manifest fragment for the triage kind.
// Type is in the dotted-namespace form so RegisterTypeFromManifest
// preserves it verbatim (no prefix mangling). Bare "triage" would
// trigger automatic "<pluginID>.<type>" namespacing which is the same
// final string but encoded indirectly; spelling it out keeps the wire
// shape obvious from the source.
var triageManifest = []byte(`type: tangent.triage
version: "0.1"
description: "Triage envelope: ask a human to accept, reject, or annotate an item. Tangent v0.1 plugin-registered; planned for go-envelopes core in v0.3."
responseKind: data
ui:
  component: TriageView
`)

// triageSchema is the JSON Schema for the triage envelope's `data` field.
// Intentionally permissive in v0.1: clients populate `items` (array of
// strings or objects) and an optional `prompt`; the WS-bridged frontend
// renders whatever shape arrives. The hand-rolled MCP InputSchema in
// internal/mcp/triage_schema.go tightens the envelope-level constraints
// for direct MCP calls; this schema only governs envelope.data.
var triageSchema = []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "title": "Triage envelope data",
  "description": "Tangent v0.1 triage payload. Permissive shape: items + optional prompt + optional context fields. v0.3 will tighten when triage upstreams to go-envelopes core.",
  "properties": {
    "prompt": {"type": "string", "description": "Optional human-facing instruction shown above the items."},
    "items": {
      "type": "array",
      "description": "Items to triage. Strings are rendered as labels; objects pass through to the frontend untouched.",
      "items": {
        "oneOf": [
          {"type": "string"},
          {"type": "object"}
        ]
      }
    },
    "context": {"type": "object", "description": "Free-form context bag (links, metadata) passed through to the frontend."}
  },
  "additionalProperties": true
}`)

// RegisterTriage registers the triage envelope type with the given
// envelope service via the plugin extension API. Idempotent across
// process lifetimes (the registry rejects duplicate names with a
// well-defined error from go-envelopes); idempotent within a process
// only if the caller checks Has() first — RegisterTypeFromManifest will
// error on a re-register, which is the correct behavior for boot-time
// callers that expect a single registration site.
//
// Returns nil on success or a non-nil error if the manifest fails to
// parse or the schema fails to compile.
func RegisterTriage(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	if err := svc.RegisterTypeFromManifest(TriageEnvelopeType, triageManifest, triageSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register triage: %w", err)
	}
	return nil
}
