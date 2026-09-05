package extensions

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The HITL v1 contract ships one 40-entry `$defs` bundle whose root validates
// an enqueue request. ADR 0003 §2.2 requires a definition to also carry a
// `response_schema` and, where it has typed publisher errors, an
// `error_schema` — and docs/contracts/hitl-inbox-v1.md already names those
// `$defs` as code-generation targets.
//
// Rather than hand-copying two more documents out of the bundle and letting
// them rot, the response and error schemas are *projections* of it: a root
// promoted out of `$defs`, carrying only the definitions reachable from that
// root. The projections are committed as authored files in the package
// directory, because a manifest references files and a file has a digest-able
// identity — and TestHITLContractProjectionsMatchBundle asserts the committed
// bytes still equal what the bundle projects. Change the bundle without
// regenerating, and the drift is a named test failure rather than a contract
// that silently disagrees with itself.

// HITLResponseDefinition is the `$defs` entry describing the resolution
// response payload an operator submits — the exact value
// hitl.Service.Resolve passes as ResponsePayload.
const HITLResponseDefinition = "HITLResponseV1"

// HITLResponseContractSchema returns the standalone response schema for
// tangent.hitl-item: HITLResponseV1 promoted to the document root with its
// reachable definitions.
func HITLResponseContractSchema() ([]byte, error) {
	bundle, definitions, err := hitlBundle()
	if err != nil {
		return nil, err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(definitions[HITLResponseDefinition], &root); err != nil {
		return nil, fmt.Errorf("extensions: decode %s: %w", HITLResponseDefinition, err)
	}
	root["$schema"] = bundle["$schema"]
	return hitlProjection(root, definitions)
}

// HITLErrorContractSchema returns the standalone typed-publisher-error schema
// for tangent.hitl-item — the "HITLStaleRevisionErrorV1 /
// HITLIdempotencyConflictErrorV1 pattern, generalized" that ADR 0003 §2.2
// names as the model for `error_schema`.
func HITLErrorContractSchema() ([]byte, error) {
	bundle, definitions, err := hitlBundle()
	if err != nil {
		return nil, err
	}
	root := map[string]json.RawMessage{
		"$schema": bundle["$schema"],
		"title":   json.RawMessage(`"HITL v1 typed publisher error"`),
		"description": json.RawMessage(
			`"A typed error tangent.hitl-item returns to a caller: a stale-revision conflict or an idempotency conflict."`),
		"oneOf": json.RawMessage(fmt.Sprintf(
			`[{"$ref":"#/$defs/%s"},{"$ref":"#/$defs/%s"}]`,
			HITLStaleRevisionDefinition, HITLIdempotencyConflictDefinition)),
	}
	return hitlProjection(root, definitions)
}

func hitlBundle() (bundle, definitions map[string]json.RawMessage, err error) {
	if err := json.Unmarshal(hitlItemSchema, &bundle); err != nil {
		return nil, nil, fmt.Errorf("extensions: decode hitl-item schema: %w", err)
	}
	if err := json.Unmarshal(bundle["$defs"], &definitions); err != nil {
		return nil, nil, fmt.Errorf("extensions: decode hitl-item definitions: %w", err)
	}
	return bundle, definitions, nil
}

// hitlProjection attaches the transitive closure of `$defs` entries root
// references, and nothing else. Copying the whole 40-entry bundle into every
// projection would triple the embedded bytes and would make an unrelated
// definition's edit move this document's digest, which is the opposite of what
// a content digest is for.
func hitlProjection(root map[string]json.RawMessage, definitions map[string]json.RawMessage) ([]byte, error) {
	reachable := map[string]bool{}
	pending := hitlReferencedDefinitions(root)
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if reachable[name] {
			continue
		}
		body, ok := definitions[name]
		if !ok {
			return nil, fmt.Errorf("extensions: hitl-item projection references unknown $defs entry %q", name)
		}
		reachable[name] = true
		var decoded any
		if err := json.Unmarshal(body, &decoded); err != nil {
			return nil, fmt.Errorf("extensions: decode $defs/%s: %w", name, err)
		}
		pending = append(pending, hitlReferencedDefinitions(decoded)...)
	}

	names := make([]string, 0, len(reachable))
	for name := range reachable {
		names = append(names, name)
	}
	sort.Strings(names)
	closure := make(map[string]json.RawMessage, len(names))
	for _, name := range names {
		closure[name] = definitions[name]
	}
	if len(closure) > 0 {
		encoded, err := json.Marshal(closure)
		if err != nil {
			return nil, fmt.Errorf("extensions: encode hitl-item projection definitions: %w", err)
		}
		root["$defs"] = encoded
	}
	return json.Marshal(root)
}

// hitlReferencedDefinitions walks any decoded JSON value and returns the
// `$defs` entry names it references. Only local `#/$defs/<name>` pointers are
// recognized; the HITL bundle uses no other reference form, and silently
// following a remote one would be a contract change wearing a helper's
// clothing.
func hitlReferencedDefinitions(value any) []string {
	var found []string
	switch typed := value.(type) {
	case map[string]json.RawMessage:
		for key, raw := range typed {
			if key == "$ref" {
				var pointer string
				if err := json.Unmarshal(raw, &pointer); err == nil {
					if name, ok := strings.CutPrefix(pointer, "#/$defs/"); ok {
						found = append(found, name)
					}
				}
				continue
			}
			var decoded any
			if err := json.Unmarshal(raw, &decoded); err == nil {
				found = append(found, hitlReferencedDefinitions(decoded)...)
			}
		}
	case map[string]any:
		for key, nested := range typed {
			if key == "$ref" {
				if pointer, ok := nested.(string); ok {
					if name, cut := strings.CutPrefix(pointer, "#/$defs/"); cut {
						found = append(found, name)
					}
				}
				continue
			}
			found = append(found, hitlReferencedDefinitions(nested)...)
		}
	case []any:
		for _, nested := range typed {
			found = append(found, hitlReferencedDefinitions(nested)...)
		}
	}
	return found
}
