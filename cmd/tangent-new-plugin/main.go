// Command tangent-new-plugin scaffolds a Tangent plugin (CW-20260910-0035).
//
// It writes a plugin that loads, with the decisions the two working application
// plugins had to make already made and the traps they hit already written down.
// What it deliberately does not do is finish the job: the mapping is yours, the
// card bounds are a measurement you have to take, and the live check is the
// first thing to fill in.
//
//	tangent-new-plugin -package almanac -app Almanac -hands-back-work
//	tangent-new-plugin -package almanac -preset kind
//
// The output directory defaults to internal/plugins/<package> for the
// application preset. The kind preset writes into three places at once — the
// plugin, the shipped package tree, and the UI — so it writes relative to the
// repository root and prints what has to be edited by hand.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/tangent/internal/plugintemplate"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tangent-new-plugin:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		options plugintemplate.Options
		preset  string
		dir     string
	)
	flag.StringVar(&preset, "preset", string(plugintemplate.PresetApplication),
		"what to scaffold: "+strings.Join(plugintemplate.Presets(), " or "))
	flag.StringVar(&options.Package, "package", "",
		"Go package name, lowercase alphanumeric (required)")
	flag.StringVar(&options.App, "app", "",
		"the application's human name; defaults to the package name, title-cased")
	flag.StringVar(&options.Slug, "slug", "", "route segment under /api/plugins/")
	flag.StringVar(&options.Tool, "tool", "",
		"tool base name; the open tool is tangent.<tool>")
	flag.StringVar(&options.BaseURL, "base-url", "",
		"where the application listens on this machine by default")
	flag.StringVar(&options.Kind, "kind", "",
		"the envelope kind to fill (application) or contribute (kind)")
	flag.StringVar(&options.Task, "task", "", "the Torque task id this is built under")
	flag.BoolVar(&options.HandsBackWork, "hands-back-work", false,
		"at least one disposition is an AUTHORED act in the application's own terms, "+
			"so the sync hands it back to the agent instead of applying it")
	flag.BoolVar(&options.Auth, "auth", false, "the application takes a bearer token")
	flag.StringVar(&dir, "dir", "", "output directory; defaults to internal/plugins/<package>")
	flag.Parse()

	options.Preset = plugintemplate.Preset(preset)
	resolved, err := options.Derive()
	if err != nil {
		return err
	}

	files, err := plugintemplate.Render(resolved)
	if err != nil {
		return err
	}
	if dir == "" {
		dir = defaultDir(resolved)
	}
	written, err := plugintemplate.Write(dir, files)
	if err != nil {
		return err
	}

	for _, rel := range written {
		fmt.Println(filepath.Join(dir, rel))
	}
	fmt.Fprint(os.Stderr, nextSteps(resolved))
	return nil
}

func defaultDir(options plugintemplate.Options) string {
	if options.Preset == plugintemplate.PresetKind {
		// Three trees at once, so the paths are already repo-relative.
		return "."
	}
	return filepath.Join("internal", "plugins", options.Package)
}

// nextSteps names what the scaffold cannot do for you.
//
// Every line here is a two-place edit or a measurement. A generator that stayed
// silent about them produces a plugin that builds and does not load, which is
// the worst of both.
func nextSteps(options plugintemplate.Options) string {
	var out strings.Builder
	out.WriteString("\nNot done for you:\n")
	switch options.Preset {
	case plugintemplate.PresetKind:
		fmt.Fprintf(&out,
			"  1. Add %q to internal/envelope/extensions/register_all.go with\n"+
				"     contributedByPlugin: true, and a RegisterX function beside it.\n",
			options.Kind)
		out.WriteString(
			"     A manifest in the package tree that nothing registers fails\n" +
				"     TestPackageTreeMatchesRegistrations, and so does the reverse.\n")
		out.WriteString(
			"  2. Add the plugin to internal/plugins/shipped.go, before any plugin\n" +
				"     that declares it as a dependency.\n")
		out.WriteString(
			"  3. Register the renderer component in the UI and run\n" +
				"     `make generate-envelopes`; `make check-envelopes` is the staleness gate.\n")
		out.WriteString(
			"  4. Re-read the manifest. Every field in it is a claim the host enforces,\n" +
				"     and the scaffold's values are the restrictive defaults (ADR 0003 §8 C4),\n" +
				"     not an endorsement.\n")
	default:
		out.WriteString(
			"  1. Add the plugin to internal/plugins/shipped.go, after the plugin that\n" +
				"     contributes the kind it fills. The host refuses a plugin whose stated\n" +
				"     dependency is not already loaded.\n")
		fmt.Fprintf(&out,
			"  2. Document both tools in a file internal/smoke/docs_test.go names in\n"+
				"     documentedToolFiles. A plugin tool is a shipped tool: an undocumented\n"+
				"     one fails the build, and so does removing the last mention of one.\n"+
				"     Do not write a tool, kind or workflow COUNT into the prose.\n")
		fmt.Fprintf(&out,
			"  3. Fill in %s_LIVE_TEST and run it. Verify every route against the\n"+
				"     running service before writing the mapping — not against its tool\n"+
				"     descriptions, its README or a brief.\n", options.EnvPrefix)
		out.WriteString(
			"  4. MEASURE DefaultCards and MaximumCards against a realistic card and\n" +
				"     the kind's inline_payload_limit_bytes. The scaffold's numbers are\n" +
				"     another application's.\n")
		if options.HandsBackWork {
			out.WriteString(
				"  5. Replace mechanical() with your application's answer. Which side a\n" +
					"     disposition falls on is the application's answer, not the plugin's.\n")
		}
	}
	out.WriteString("\n  make verify-supported && make smoke\n")
	return out.String()
}
