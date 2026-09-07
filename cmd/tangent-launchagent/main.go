// Command tangent-launchagent installs, removes, inspects, or renders the
// macOS LaunchAgent that starts the headless tangent daemon at login. It is
// the facility CW-20260905-0031 delivers; the real install on a machine is
// performed by the install script of CW-20260907-0020, which calls this.
//
//	tangent-launchagent render   --binary /path/to/tangent [--port N] [--db PATH] [--log-dir DIR]
//	tangent-launchagent install  --binary /path/to/tangent [--port N] [--db PATH] [--log-dir DIR]
//	tangent-launchagent uninstall
//	tangent-launchagent status
//
// `render` prints the plist and touches nothing, so it can be inspected or
// diffed anywhere. `install`, `uninstall`, and `status` run launchctl against
// the current user's gui domain and read or write
// ~/Library/LaunchAgents/com.hollislabs.tangent.plist; they refuse to run on
// anything but macOS. `install` refuses, with the reason, when the binary is
// not at the path the plist would hardcode. `status` re-validates that path
// so a binary that has moved is reported rather than silently not started at
// the next login.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strconv"

	"github.com/hollis-labs/tangent/internal/launchagent"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	verb, args := os.Args[1], os.Args[2:]
	var err error
	switch verb {
	case "render":
		err = render(args)
	case "install":
		err = install(args)
	case "uninstall":
		err = uninstall(args)
	case "status":
		err = status(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "tangent-launchagent: unknown command %q\n\n", verb)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tangent-launchagent: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  tangent-launchagent render    --binary PATH [--port N] [--db PATH] [--log-dir DIR]
  tangent-launchagent install   --binary PATH [--port N] [--db PATH] [--log-dir DIR]
  tangent-launchagent uninstall
  tangent-launchagent status

The agent runs the headless tangent daemon at login (RunAtLoad true,
KeepAlive false) from ~/Library/LaunchAgents/`+launchagent.Label+`.plist.
`)
}

type configFlags struct {
	set *flag.FlagSet
	cfg launchagent.Config
}

func newConfigFlags(verb string) *configFlags {
	f := &configFlags{set: flag.NewFlagSet("tangent-launchagent "+verb, flag.ExitOnError)}
	f.set.StringVar(&f.cfg.Binary, "binary", "", "absolute path to the headless tangent daemon (required)")
	f.set.IntVar(&f.cfg.Port, "port", 0, "TANGENT_HTTP_PORT for the daemon; 0 keeps the daemon default")
	f.set.StringVar(&f.cfg.DBPath, "db", "", "TANGENT_DB_PATH for the daemon; empty keeps the daemon default")
	f.set.StringVar(&f.cfg.LogDir, "log-dir", "", "directory for tangent.log; empty means ~/Library/Logs/Tangent")
	return f
}

func (f *configFlags) parse(args []string) error {
	if err := f.set.Parse(args); err != nil {
		return err
	}
	if f.set.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", f.set.Args())
	}
	if f.cfg.Binary == "" {
		return errors.New("--binary is required: the plist hardcodes the daemon's path, so it has to be given, not guessed")
	}
	return nil
}

func render(args []string) error {
	f := newConfigFlags("render")
	if err := f.parse(args); err != nil {
		return err
	}
	home, err := homeDir()
	if err != nil {
		return err
	}
	raw, err := launchagent.Render(f.cfg, home)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(raw)
	return err
}

func install(args []string) error {
	f := newConfigFlags("install")
	if err := f.parse(args); err != nil {
		return err
	}
	installer, err := realInstaller()
	if err != nil {
		return err
	}
	result, err := installer.Install(context.Background(), f.cfg)
	if err != nil {
		return err
	}
	switch {
	case result.Changed && result.WasLoaded:
		fmt.Printf("updated and reloaded %s\n", result.PlistPath)
	case result.Changed:
		fmt.Printf("installed and loaded %s\n", result.PlistPath)
	default:
		fmt.Printf("already installed; reloaded %s\n", result.PlistPath)
	}
	fmt.Printf("the tangent daemon at %s will start at login (RunAtLoad true, KeepAlive false)\n", f.cfg.Binary)
	return nil
}

func uninstall(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("uninstall takes no arguments, got %v", args)
	}
	installer, err := realInstaller()
	if err != nil {
		return err
	}
	result, err := installer.Uninstall(context.Background())
	if err != nil {
		return err
	}
	switch {
	case result.Changed:
		fmt.Printf("removed %s (agent %s)\n", result.PlistPath, loadedWord(result.WasLoaded))
	default:
		fmt.Printf("nothing to do: %s is not installed\n", result.PlistPath)
	}
	return nil
}

func status(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("status takes no arguments, got %v", args)
	}
	installer, err := realInstaller()
	if err != nil {
		return err
	}
	st, err := installer.Status(context.Background())
	if err != nil {
		return err
	}
	if !st.Installed {
		fmt.Printf("not installed: %s does not exist\n", st.PlistPath)
		return nil
	}
	fmt.Printf("installed: %s\n", st.PlistPath)
	fmt.Printf("loaded:    %s\n", strconv.FormatBool(st.Loaded))
	fmt.Printf("binary:    %s\n", st.Binary)
	if st.Problem != "" {
		fmt.Printf("problem:   %s\n", st.Problem)
		return errors.New("the installed agent will not start the daemon as it stands")
	}
	fmt.Println("binary ok: the daemon is where the plist says")
	return nil
}

func loadedWord(loaded bool) string {
	if loaded {
		return "was loaded and has been booted out"
	}
	return "was not loaded"
}

func homeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return home, nil
}

func realInstaller() (launchagent.Installer, error) {
	if runtime.GOOS != "darwin" {
		return launchagent.Installer{}, fmt.Errorf("launchd agents exist only on macOS; this is %s (use `render` to inspect the plist)", runtime.GOOS)
	}
	home, err := homeDir()
	if err != nil {
		return launchagent.Installer{}, err
	}
	current, err := user.Current()
	if err != nil {
		return launchagent.Installer{}, fmt.Errorf("determine current user: %w", err)
	}
	uid, err := strconv.Atoi(current.Uid)
	if err != nil {
		return launchagent.Installer{}, fmt.Errorf("parse uid %q: %w", current.Uid, err)
	}
	return launchagent.Installer{Home: home, UID: uid, Launchctl: launchagent.ExecLaunchctl{}}, nil
}
