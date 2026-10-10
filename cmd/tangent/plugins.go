package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/hollis-labs/tangent/internal/pluginintent"
	"github.com/hollis-labs/tangent/internal/pluginpkg"
)

// The `tangent plugin` commands (CW-20260911-0070).
//
// # Why these exist at all
//
// Plugins are installed rather than compiled in, so "which plugins does this
// Tangent have" stopped being answerable by reading one file in the repository.
// These are how it is answered now, and `list` in particular is not a
// convenience: an installed plugin that will not load is invisible without it.

// runPluginCommand dispatches `tangent plugin <subcommand>`.
func runPluginCommand(args []string) int {
	if len(args) == 0 {
		pluginUsage(os.Stderr)
		return 2
	}
	root, err := pluginRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tangent: %v\n", err)
		return 1
	}

	switch args[0] {
	case "install":
		return installPlugin(args[1:], root)
	case "remove", "uninstall":
		return removePlugin(args[1:], root)
	case "list", "ls":
		return listPlugins(root)
	case "enable", "disable":
		return setPluginIntent(args[1:], root, args[0] == "enable")
	case "dir":
		fmt.Println(root)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "tangent: unknown plugin command %q\n", args[0])
		pluginUsage(os.Stderr)
		return 2
	}
}

func pluginRoot() (string, error) { return pluginpkg.DefaultRoot() }

func pluginUsage(out io.Writer) {
	fmt.Fprint(out, `usage: tangent plugin <command>

  install [--enable|--disable] <dir>  install or upgrade; preserve existing intent
  enable <id>     explicitly enable an installed plugin on the next host start
  disable <id>    disable an installed plugin on the next host start
  remove <id>     uninstall a plugin by id
  list            show installed plugins, including ones that will not load
  dir             print the install directory

The install directory is `+pluginpkg.DirEnv+` when set, otherwise
~/.tangent/plugins.
New plugins are disabled until explicitly enabled. Settings provides live
enable, disable and reload controls for a running host; CLI changes require restart.
`)
}

func installPlugin(args []string, root string) int {
	set := flag.NewFlagSet("tangent plugin install", flag.ContinueOnError)
	enable := set.Bool("enable", false, "explicitly enable after installing")
	disable := set.Bool("disable", false, "explicitly disable after installing")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if set.NArg() != 1 || (*enable && *disable) {
		fmt.Fprintln(os.Stderr, "usage: tangent plugin install [--enable|--disable] <dir>")
		return 2
	}
	source, err := filepath.Abs(set.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "tangent: %v\n", err)
		return 1
	}
	if mkErr := os.MkdirAll(root, 0o750); mkErr != nil {
		fmt.Fprintf(os.Stderr, "tangent: create plugin directory: %v\n", mkErr)
		return 1
	}
	result, err := pluginpkg.Install(source, root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tangent: %v\n", err)
		return 1
	}
	verb := "installed"
	if result.Replaced {
		verb = "upgraded"
	}
	fmt.Printf("%s %s %s -> %s\n", verb, result.ID, result.Version, result.Dir)
	intent, err := pluginintent.Read(pluginintent.Path(root))
	if err == nil && (*enable || *disable) {
		intent, err = pluginintent.Set(context.Background(), pluginintent.Path(root), result.ID, *enable)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tangent: artifact installed; enable intent unavailable: %v\n", err)
		return 1
	}
	if intent[result.ID] {
		fmt.Println("enabled intent; restart tangent to load the installed artifact")
	} else {
		fmt.Println("disabled; explicitly enable in Settings or with tangent plugin enable <id>")
		fmt.Println("restart tangent to discover the installed artifact")
	}
	return 0
}

func setPluginIntent(args []string, root string, enabled bool) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: tangent plugin enable|disable <id>")
		return 2
	}
	installed, _, err := pluginpkg.Scan(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tangent: %v\n", err)
		return 1
	}
	for _, entry := range installed {
		if entry.Manifest.ID != args[0] {
			continue
		}
		if _, err = pluginintent.Set(context.Background(), pluginintent.Path(root), args[0], enabled); err != nil {
			fmt.Fprintf(os.Stderr, "tangent: %v\n", err)
			return 1
		}
		fmt.Printf("%s enabled intent: %t; restart tangent to apply (or use Settings for live controls)\n", args[0], enabled)
		return 0
	}
	fmt.Fprintf(os.Stderr, "tangent: %s is not a usable installed plugin\n", args[0])
	return 1
}

func removePlugin(args []string, root string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: tangent plugin remove <id>")
		return 2
	}
	if err := pluginpkg.Remove(args[0], root); err != nil {
		fmt.Fprintf(os.Stderr, "tangent: %v\n", err)
		return 1
	}
	fmt.Printf("removed %s\n", args[0])
	fmt.Println("restart tangent to stop it")
	return 0
}

// listPlugins prints what is installed, INCLUDING what will not load.
//
// The rejected half is the point. A plugin that is present and unusable is more
// confusing than one that is absent — the operator put it there — and without
// this the only symptom is a tool that does not appear.
func listPlugins(root string) int {
	return listPluginsTo(root, os.Stdout, os.Stderr)
}

func listPluginsTo(root string, out, failures io.Writer) int {
	installed, rejected, err := pluginpkg.Scan(root)
	if err != nil {
		fmt.Fprintf(failures, "tangent: %v\n", err)
		return 1
	}
	if len(installed) == 0 && len(rejected) == 0 {
		fmt.Fprintf(out, "no plugins installed in %s\n", root)
		return 0
	}

	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	intent, err := pluginintent.Read(pluginintent.Path(root))
	if err != nil {
		fmt.Fprintf(failures, "tangent: enable intent unavailable: %v\n", err)
		return 1
	}
	fmt.Fprintln(writer, "ID\tVERSION\tPROTOCOL\tSTATUS\tENABLE INTENT (NEXT START)")
	for _, entry := range installed {
		fmt.Fprintf(writer, "%s\t%s\t%d\tok\t%t\n",
			entry.Manifest.ID, entry.Manifest.Version, entry.Manifest.Protocol, intent[entry.Manifest.ID])
	}
	_ = writer.Flush()

	for _, reject := range rejected {
		fmt.Fprintf(failures, "\n%s will not load:\n  %v\n", reject.Dir, reject.Reason)
	}
	if len(rejected) > 0 {
		return 1
	}
	return 0
}
