package smoke_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/smoke"
)

// This file is the documentation gate.
//
// # Why it exists
//
// Tangent's MCP tool count was wrong in this repository's own documentation
// six times in a single day — 25, 39, 40, 43, 44, 45, 46 — and every one of
// those was a number somebody typed into prose and then had no way to notice
// going stale. `internal/smoke` already refuses to hold such a literal: its
// reference surface always comes from asking the shipped binary. These tests
// extend the same rule to the documents, because a derived check that nobody
// reads is not what an operator or a new contributor is holding at 2am. They
// are holding README.md.
//
// The rule the tests enforce is narrow and mechanical:
//
//   - every `tangent.<tool>` name a document mentions must exist in the surface
//     the shipped binary advertises, and
//   - every tool the binary advertises must appear somewhere in the reference
//     documentation, so a tool cannot be added and left undocumented, and
//   - the few counts prose genuinely needs must match what the build reports.
//
// A failure here is never "the test is wrong". It is either a document that
// drifted or a surface that changed without its documentation.

// documentedToolFiles are the documents that name tools. A tool name appearing
// in any of them must be real; a real tool must appear in at least one.
var documentedToolFiles = []string{
	"README.md",
	"AGENTS.md",
	"docs/architecture.md",
	"docs/mcp-integration.md",
	"docs/developing.md",
	"docs/writing-a-plugin.md",
	"docs/mcp-smoketest.md",
	"docs/database-operations.md",
}

// toolMention matches a `tangent.<name>` token as documents write it. Both the
// dotted and underscored halves are permitted because the shipped surface uses
// both (`tangent.file-picker`, `tangent.session_get`).
//
// The leading group excludes a `/`, so a clone URL (`hollis-labs/tangent.git`)
// and a file path (`projects/tangent.md`) are not mistaken for tools. Go's
// regexp has no lookbehind, so the preceding byte is captured and discarded.
var toolMention = regexp.MustCompile(`(^|[^/\w.])(tangent\.[a-z][a-z0-9_-]*)`)

// wildcardFamilies are the abbreviations documents legitimately use for a
// family of tools (`tangent.session_*`). They are stripped before matching so
// they are not mistaken for a tool that does not exist.
var wildcardFamilies = []string{
	"tangent.session_*",
	"tangent.hitl_*",
	"tangent.interaction_*",
	"tangent.surface_*",
}

// nonToolMentions are `tangent.`-prefixed identifiers that are deliberately
// not MCP tools: envelope kinds, definition ids, and package ids. Documents
// name them for good reasons, so they are excluded rather than reported.
var nonToolMentions = map[string]bool{
	"tangent.external-review": true,
	"tangent.appboard":        true, // approved definition package id, not a tool
	// Envelope kinds are hyphenated wire names, several of which collide
	// with a tool name spelled the same way; the ones that do are real
	// tools and stay out of this set.
	"tangent.hitl-item":          true,
	"tangent.doc-item":           true,
	"tangent.interview-question": true,
	"tangent.synthesis-notes":    true,
	"tangent.block-draft":        true,
	"tangent.prose-revision":     true,
	"tangent.output-render":      true,
	"tangent.canvas":             true,
	"tangent.compound":           true,
	"tangent.review":             true,
	"tangent.workspace":          true,
	"tangent.writing":            true,
	"tangent.hitl":               true,
	"tangent.generic-candidate":  true,
	"tangent.cerberus":           true,
	"tangent.db":                 true,
}

func repoPath(parts ...string) string {
	return filepath.Join(append([]string{repoRoot()}, parts...)...)
}

func readDoc(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(repoPath(rel)) // #nosec G304 -- rel is a literal from this file.
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	body := string(raw)
	for _, family := range wildcardFamilies {
		body = strings.ReplaceAll(body, family, "")
	}
	return body
}

// Optional plugin tools ship on their own schedule. Their manuals participate
// in the same strict gate when the plugin is installed, rather than promising
// an uninstalled tool in a host-only build.
func documentsForSurface(t *testing.T, shipped map[string]bool) []string {
	t.Helper()
	files := append([]string{}, documentedToolFiles...)
	matches, err := filepath.Glob(repoPath("docs", "plugins", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range matches {
		rel, err := filepath.Rel(repoRoot(), path)
		if err != nil {
			t.Fatal(err)
		}
		body := readDoc(t, rel)
		header := regexp.MustCompile(`(?m)^<!-- requires-tool: (tangent\.[a-z][a-z0-9_-]*) -->$`).FindStringSubmatch(body)
		if len(header) != 2 {
			t.Fatalf("%s needs an installed-tool declaration", rel)
		}
		if shipped[header[1]] {
			files = append(files, rel)
		}
	}
	return files
}

func shippedNames(names []string) map[string]bool {
	result := make(map[string]bool, len(names))
	for _, name := range names {
		result[name] = true
	}
	return result
}

// TestDocumentationNamesOnlyToolsTheBuildServes catches the direction that
// costs an operator the most: a document promising a tool that is not there.
func TestDocumentationNamesOnlyToolsTheBuildServes(t *testing.T) {
	endpoint := bootShippedBinary(t)
	ctx := context.Background()

	surface, finding := endpoint.StreamableSurface(ctx)
	if finding != nil {
		t.Fatalf("derive shipped surface:\n%s", finding)
	}
	shipped := map[string]bool{}
	for _, name := range surface.Names {
		shipped[name] = true
	}

	for _, rel := range documentsForSurface(t, shipped) {
		body := readDoc(t, rel)
		for _, groups := range toolMention.FindAllStringSubmatch(body, -1) {
			mention := strings.TrimRight(groups[2], "-_")
			if shipped[mention] || nonToolMentions[mention] {
				continue
			}
			t.Errorf("%s names %q, which the shipped build does not serve "+
				"(surface: %s). Either the document is stale or the tool was removed.",
				rel, mention, surface.Describe())
		}
	}
}

// TestEveryShippedToolIsDocumented is the other direction: a tool that exists
// and is named nowhere is a tool nobody can find.
func TestEveryShippedToolIsDocumented(t *testing.T) {
	endpoint := bootShippedBinary(t)
	ctx := context.Background()

	surface, finding := endpoint.StreamableSurface(ctx)
	if finding != nil {
		t.Fatalf("derive shipped surface:\n%s", finding)
	}

	var corpus strings.Builder
	for _, rel := range documentsForSurface(t, shippedNames(surface.Names)) {
		corpus.WriteString(readDoc(t, rel))
	}
	// The wildcard families stand in for their members.
	documented := corpus.String()
	families := map[string]string{
		"tangent.session_":     "tangent.session_*",
		"tangent.hitl_":        "tangent.hitl_*",
		"tangent.interaction_": "tangent.interaction_*",
		"tangent.surface_":     "tangent.surface_*",
	}

	var undocumented []string
	for _, name := range surface.Names {
		if strings.Contains(documented, name) {
			continue
		}
		covered := false
		for prefix, family := range families {
			if strings.HasPrefix(name, prefix) && strings.Contains(corpus.String()+family, family) {
				covered = true
				break
			}
		}
		if !covered {
			undocumented = append(undocumented, name)
		}
	}
	sort.Strings(undocumented)
	if len(undocumented) > 0 {
		t.Errorf("the shipped build serves %d tools that appear in none of %v: %s",
			len(undocumented), documentedToolFiles, strings.Join(undocumented, ", "))
	}
}

// TestDocumentedWorkflowTableMatchesListWorkflows pins the one table an
// operator actually works from.
//
// docs/manual-tests/workflow-smoke-tests.md carries a row per bundled room
// workflow. It is a runbook rather than a registry, which is exactly why it
// drifts: nothing about adding a workflow forces anyone to open it.
func TestDocumentedWorkflowTableMatchesListWorkflows(t *testing.T) {
	endpoint := bootShippedBinary(t)
	ctx := context.Background()

	kinds, err := listWorkflowKinds(ctx, endpoint.BaseURL)
	if err != nil {
		t.Fatalf("call tangent.list_workflows: %v", err)
	}
	if len(kinds) == 0 {
		t.Fatal("tangent.list_workflows returned no workflows")
	}

	body := readDoc(t, "docs/manual-tests/workflow-smoke-tests.md")
	// The runbook covers the workflows an operator drives by hand. The
	// writing-flow kinds are exercised by their own recipe rather than by a
	// row in this table, so they are not required here.
	writingFlow := map[string]bool{
		"tangent.interview-question": true,
		"tangent.synthesis-notes":    true,
		"tangent.block-draft":        true,
		"tangent.prose-revision":     true,
		"tangent.output-render":      true,
	}
	var missing []string
	for _, kind := range kinds {
		if writingFlow[kind] {
			continue
		}
		if !strings.Contains(body, kind) {
			missing = append(missing, kind)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("docs/manual-tests/workflow-smoke-tests.md has no row for %s; "+
			"tangent.list_workflows reports %d workflows",
			strings.Join(missing, ", "), len(kinds))
	}
}

// TestNoDocumentPinsTheToolCount is the standing rule, enforced.
//
// The surface changes; a sentence saying how big it is does not. Every
// document that pinned it has been wrong within a week, so none of them may
// pin it again — not even correctly, because a correct literal today is the
// stale literal that gets believed tomorrow.
func TestNoDocumentPinsTheToolCount(t *testing.T) {
	endpoint := bootShippedBinary(t)
	ctx := context.Background()

	surface, finding := endpoint.StreamableSurface(ctx)
	if finding != nil {
		t.Fatalf("derive shipped surface:\n%s", finding)
	}
	count := fmt.Sprintf("%d", surface.Count())

	// Phrases that would be a pinned count if the current number appeared
	// next to them.
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)advertises\s+` + count + `\s+tools`),
		regexp.MustCompile(`(?i)` + count + `\s+MCP tools`),
		regexp.MustCompile(`(?i)tools/list\D{0,20}` + count),
	}
	files := append(append([]string{}, documentedToolFiles...), "CHANGELOG.md")
	pluginDocs, err := filepath.Glob(repoPath("docs", "plugins", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range pluginDocs {
		rel, err := filepath.Rel(repoRoot(), path)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, rel)
	}
	for _, rel := range files {
		body := readDoc(t, rel)
		for _, pattern := range patterns {
			if match := pattern.FindString(body); match != "" {
				t.Errorf("%s pins the tool count: %q. Derive it with `make smoke` "+
					"instead — this number has been wrong more often than right.",
					rel, match)
			}
		}
	}
}

// listWorkflowKinds calls tangent.list_workflows over the stateless
// Streamable HTTP endpoint and returns the wire kinds it reports.
//
// It posts directly rather than reaching for a helper in the smoke package,
// because a documentation gate must not require widening the production probe
// surface to run.
func listWorkflowKinds(ctx context.Context, baseURL string) ([]string, error) {
	payload := `{"jsonrpc":"2.0","id":1,"method":"tools/call",` +
		`"params":{"name":"tangent.list_workflows","arguments":{}}}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(baseURL, "/")+"/mcp", bytes.NewBufferString(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Result struct {
			StructuredContent json.RawMessage `json:"structuredContent"`
			Content           []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode tools/call response: %w (body: %s)", err, bound(string(raw)))
	}

	body := envelope.Result.StructuredContent
	if len(body) == 0 {
		if len(envelope.Result.Content) == 0 {
			return nil, fmt.Errorf("tools/call returned no content: %s", bound(string(raw)))
		}
		body = json.RawMessage(envelope.Result.Content[0].Text)
	}

	var listing struct {
		Workflows []struct {
			Type string `json:"type"`
		} `json:"workflows"`
	}
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("decode workflow listing: %w", err)
	}
	kinds := make([]string, 0, len(listing.Workflows))
	for _, workflow := range listing.Workflows {
		kinds = append(kinds, workflow.Type)
	}
	sort.Strings(kinds)
	return kinds, nil
}

// bound keeps a failure message readable when a body is large.
func bound(text string) string {
	if len(text) <= 400 {
		return text
	}
	return text[:400] + "…"
}

var _ = smoke.NewSurface
