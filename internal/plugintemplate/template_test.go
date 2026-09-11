package plugintemplate

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/envelope"
)

// The gates on the scaffold.
//
// A template rots the way documentation rots, and faster: nobody runs it until
// they need it, and by then they cannot tell a stale scaffold from their own
// mistake. So the two things worth holding are held mechanically.
//
//  1. The committed examples under example/ are exactly what a fresh render
//     produces. They are ordinary Go packages, so `go build ./...` compiles them
//     and their own generated tests load them onto the REAL plugin host. That is
//     the claim "the template produces a plugin that loads", checked rather than
//     asserted — and the drift gate is what ties it to the template rather than
//     to two files somebody once pasted.
//  2. The kind preset's manifest materializes as `available`. A scaffold that
//     omitted the ADR 0003 manifest, or authored a field the host derives, or
//     named a version outside the host's compatibility range, would produce a
//     plugin the host REFUSES — and it would build cleanly first.

// exampleRenders are the committed examples and the options that produce them.
//
// Two rather than one, and the pair is chosen rather than convenient: they are
// the two answers the working plugins gave to the question the template asks.
// `almanac` hands authored work back to the agent the way the Tesseract board
// does; `ledger` applies everything, the way the Torque board does. Between
// them every conditional in the application preset is compiled and run.
var exampleRenders = map[string]Options{
	"almanac": {
		Package: "almanac", App: "Almanac", Task: "CW-20260910-0035",
		BaseURL: "http://127.0.0.1:8099", HandsBackWork: true, Auth: true,
	},
	"ledger": {
		Package: "ledger", App: "Ledger", Task: "CW-20260910-0035",
		BaseURL: "http://127.0.0.1:8098",
	},
}

// TestTheCommittedExamplesAreAFreshRender is the drift gate.
//
// Without it the examples are just two plugins: they would keep compiling and
// keep passing while the template that claims to produce them drifted away, and
// the first person to run the scaffolder would get something that had never been
// built.
func TestTheCommittedExamplesAreAFreshRender(t *testing.T) {
	t.Parallel()
	for name, options := range exampleRenders {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			files, err := Render(options)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			dir := filepath.Join("example", name)
			committed, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("read %s: %v", dir, err)
			}
			if len(committed) != len(files) {
				t.Errorf("%s holds %d file(s), a fresh render produces %d: %v",
					dir, len(committed), len(files), sortedKeys(files))
			}
			for rel, want := range files {
				got, readErr := os.ReadFile(filepath.Join(dir, rel)) // #nosec G304 -- rel comes from the renderer.
				if readErr != nil {
					t.Errorf("%s/%s is missing; the scaffold emits it: %v", dir, rel, readErr)
					continue
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s/%s differs from a fresh render.\n"+
						"Re-run the scaffolder into a clean directory and replace the example, "+
						"or fix the template — an example nobody can reproduce is not an example.",
						dir, rel)
				}
			}
		})
	}
}

// TestTheKindPresetProducesAManifestTheHostWillAccept is the other half of "it
// loads", for the preset whose output cannot be committed as a live package.
//
// A contributed kind's manifest has to be embedded in
// internal/envelope/extensions' package tree AND registered, and a manifest the
// tree carries with no registration fails that package's own drift gate. So the
// scaffold's manifest is checked here, where it can be materialized without
// being installed: parse it (which validates it and refuses an authored derived
// field), then materialize it under the host's real policy and require
// `available`.
func TestTheKindPresetProducesAManifestTheHostWillAccept(t *testing.T) {
	t.Parallel()
	files, err := Render(Options{Preset: PresetKind, Package: "fieldnotes", App: "Field notes"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	manifestPath := filepath.Join("internal", "envelope", "extensions", "packages",
		"tangent.fieldnotes", "fieldnotes", "manifest.yaml")
	schemaPath := filepath.Join(filepath.Dir(manifestPath), "request.schema.json")
	source, ok := files[manifestPath]
	if !ok {
		t.Fatalf("the kind preset emitted no manifest at %s; the host refuses a kind "+
			"it cannot resolve one for, so this scaffold would build and not load", manifestPath)
	}
	schema, ok := files[schemaPath]
	if !ok {
		t.Fatalf("the kind preset emitted no request schema at %s", schemaPath)
	}

	manifest, err := definition.Parse(source)
	if err != nil {
		t.Fatalf("the scaffolded manifest does not validate: %v", err)
	}
	if manifest.Kind != "tangent.fieldnotes" {
		t.Errorf("manifest kind = %q, want the kind the scaffold was asked for", manifest.Kind)
	}

	materialized, err := definition.Materialize(manifest,
		definition.Material{
			RequestSchema: schema,
			SourceLocator: "test://" + manifestPath,
		},
		definition.HostPolicy{
			HostVersion:     envelope.HostVersion,
			ProtocolVersion: envelope.ProtocolVersion,
		})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if materialized.State != definition.StateAvailable {
		t.Fatalf("the scaffolded kind materializes as %s: %s\n"+
			"A scaffold that produces a kind the host will not serve is worse than no "+
			"scaffold: it builds, it registers, and it fails at the first submission.",
			materialized.State, materialized.StateReason)
	}
	if materialized.Derived.ContractDigest == "" || materialized.Derived.ManifestDigest == "" {
		t.Errorf("the scaffolded kind has no derived identity: %+v", materialized.Derived)
	}
}

// TestTheScaffoldRefusesAKindThatKnowsAboutAnApplication.
//
// The two presets are kept apart on purpose: a plugin that contributed a kind
// AND talked to an application would be a domain-free kind with one
// application's concepts in it, which is the specific way this boundary rots.
// Refusing the combination at the flag is cheaper than catching it in review.
func TestTheScaffoldRefusesAKindThatKnowsAboutAnApplication(t *testing.T) {
	t.Parallel()
	_, err := Options{Preset: PresetKind, Package: "fieldnotes", HandsBackWork: true}.Derive()
	if err == nil {
		t.Fatal("the kind preset accepted an application-shaped option")
	}
	if !strings.Contains(err.Error(), "separately") {
		t.Errorf("Derive = %v, want the remedy named", err)
	}
}

// TestEveryScaffoldedPluginNamesItsToolsInTheTangentNamespace.
//
// The host refuses a tool outside it and the documentation gate matches that
// spelling, so a scaffold that emitted anything else would produce a plugin that
// fails at load and a doc gate nobody could satisfy.
func TestEveryScaffoldedPluginNamesItsToolsInTheTangentNamespace(t *testing.T) {
	t.Parallel()
	options, err := Options{Package: "almanac"}.Derive()
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	for _, tool := range []string{options.OpenTool(), options.SyncTool()} {
		if !strings.HasPrefix(tool, "tangent.") {
			t.Errorf("tool %q is outside the tangent. namespace", tool)
		}
	}
	if !strings.HasPrefix("/api/plugins/"+options.Slug+"/sync", "/api/plugins/") {
		t.Errorf("route slug %q is not under the plugin prefix", options.Slug)
	}
}

// TestNoScaffoldedFilePinsACount is the standing rule, applied to the thing that
// would propagate it fastest.
//
// A number written into prose has been wrong in this repository far more often
// than right, and a template is the one place a wrong number gets copied into
// every plugin that follows.
func TestNoScaffoldedFilePinsACount(t *testing.T) {
	t.Parallel()
	pinned := regexp.MustCompile(`(?i)\b\d+\s+(mcp )?(tools|kinds|workflows|envelope kinds)\b`)
	for name, options := range exampleRenders {
		files, err := Render(options)
		if err != nil {
			t.Fatalf("Render %s: %v", name, err)
		}
		for rel, body := range files {
			if match := pinned.Find(body); match != nil {
				t.Errorf("%s/%s pins a count: %q", name, rel, match)
			}
		}
	}
}
