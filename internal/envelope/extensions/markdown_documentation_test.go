package extensions

import (
	"path"
	"regexp"
	"strings"
	"testing"
)

// Where the markdown inventory lives, held mechanically (CW-20260911-0004).
//
// Two schema surfaces describe the same fields: the MCP tool schemas an agent
// reads while composing a call, and the ADR 0003 package request schemas the
// host validates against. Commit 3840027 documented markdown rendering on the
// first and not the second, which read as drift until it was examined. It is
// not drift — it is the only surface where the fact can be corrected without
// moving a contract. packages.go's doc comment carries the reasoning; this
// file is what keeps the next reader from helpfully undoing it, and
// internal/mcp's TestEveryProseBearingSchemaSaysWhatRendersAsMarkdown is the
// other half: without it, deleting the inventory would leave this gate green
// and the repository with no markdown documentation at all.

// renderingMention matches the vocabulary the MCP schemas settled on. It is
// deliberately narrow: a schema is free to describe a field whose CONTENT is
// markdown (`prompt_markdown`, `details_markdown`, `output_render.markdown`),
// because that is a shape fact the contract really does own. What it must not
// carry is a claim about what a RENDERER does with the bytes.
var renderingMention = regexp.MustCompile(`(?i)(renders? as markdown|as markdown[,.]|not as markdown|stays? literal|displayed literally)`)

// TestPackageRequestSchemasDocumentNoRendering is the pin on the decision.
//
// A field `description` in one of these files changes the request schema
// bytes, which changes `contract_digest` and `binding_digest`. ADR 0003 §8 C1
// then makes every pending interaction of that kind `unavailable` for new
// submissions, and §3 forbids a `contract_digest` change from riding a
// `revision` bump — it is a new `version`, which §8 C3 freezes for the shipped
// kinds. A rendering fact must not be able to do that to a live surface.
//
// The failure message names the remedy rather than only the rule, because the
// instinct this test catches is a good one aimed at the wrong file.
func TestPackageRequestSchemasDocumentNoRendering(t *testing.T) {
	t.Parallel()
	kinds, err := ShippedPackagePaths()
	if err != nil {
		t.Fatalf("ShippedPackagePaths: %v", err)
	}
	if len(kinds) == 0 {
		t.Fatal("no shipped package paths; the walk found nothing to check")
	}
	for _, kindPath := range kinds {
		schemaPath := path.Join(packagesRoot, kindPath, requestSchemaFileName)
		body, readErr := packagesFS.ReadFile(schemaPath)
		if readErr != nil {
			// A kind may legitimately ship no request schema; the manifest
			// names what it references, and validateSchemaRef covers that.
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			if renderingMention.MatchString(line) {
				t.Errorf(
					"%s documents markdown rendering:\n\t%s\n"+
						"A request schema is contract bytes: this edit moves contract_digest "+
						"and binding_digest, so ADR 0003 §8 C1 makes every pending interaction "+
						"of this kind unavailable for new submissions and §3 turns the revision "+
						"bump into a version bump §8 C3 has frozen. Put it on the kind's MCP "+
						"tool schema in internal/mcp instead, where a correction costs a "+
						"rebuild and nothing else.",
					schemaPath, strings.TrimSpace(line))
			}
		}
	}
}
