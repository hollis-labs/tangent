// Command tangent-install places a tagged Tangent release on this Mac and
// runs it under a user LaunchAgent, or removes it again. It is the code half
// of CW-20260907-0020; scripts/install-macos.sh and `make install-macos`
// wrap it.
//
//	tangent-install install   [--artifacts DIR] [--bin-dir DIR] [--apps-dir DIR] [--port N] [--db PATH] [--log-dir DIR] [--wait SECONDS] [--dry-run]
//	tangent-install uninstall [--bin-dir DIR] [--apps-dir DIR] [--dry-run]
//
// Install and upgrade are the same command: it validates the artifact pair,
// refuses a foreign or mid-migration daemon on the port, runs the artifact's
// `--db-check` against the existing database, places the binary and the app,
// boots the LaunchAgent out and back in, waits for /readyz, and prints the
// versions before and after. `--dry-run` runs every probe and prints every
// action without changing anything. Off macOS only `--dry-run` is allowed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/hollis-labs/tangent/internal/installer"
	"github.com/hollis-labs/tangent/internal/launchagent"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "install":
		err = run(os.Args[2:], true)
	case "uninstall":
		err = run(os.Args[2:], false)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "tangent-install: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tangent-install: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  tangent-install install   [--artifacts DIR] [--bin-dir DIR] [--apps-dir DIR] [--port N] [--db PATH] [--log-dir DIR] [--wait SECONDS] [--dry-run]
  tangent-install uninstall [--bin-dir DIR] [--apps-dir DIR] [--dry-run]

install is also the upgrade: same command, newer artifacts. Defaults:
artifacts . (tangent and Tangent.app from make build / make build-app),
bin-dir ~/.local/bin, apps-dir ~/Applications, port 7842,
db ~/.tangent/tangent.db, log-dir ~/.tangent/logs. The database is never
removed by uninstall.
`)
}

func run(args []string, install bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("determine current user: %w", err)
	}
	uid, err := strconv.Atoi(current.Uid)
	if err != nil {
		return fmt.Errorf("parse uid %q: %w", current.Uid, err)
	}
	layout := installer.DefaultLayout(home, uid)

	verb := "uninstall"
	if install {
		verb = "install"
	}
	flags := flag.NewFlagSet("tangent-install "+verb, flag.ExitOnError)
	artifactsDir := flags.String("artifacts", ".", "directory holding tangent and Tangent.app")
	flags.StringVar(&layout.BinDir, "bin-dir", layout.BinDir, "where the daemon binary goes")
	flags.StringVar(&layout.AppsDir, "apps-dir", layout.AppsDir, "where Tangent.app goes")
	flags.IntVar(&layout.Port, "port", layout.Port, "the stable daemon's port")
	flags.StringVar(&layout.DBPath, "db", layout.DBPath, "the stable database")
	flags.StringVar(&layout.LogDir, "log-dir", layout.LogDir, "directory for tangent.log")
	wait := flags.Int("wait", 30, "seconds to wait for /readyz after loading the agent")
	dryRun := flags.Bool("dry-run", false, "probe and print every action without performing it")
	if parseErr := flags.Parse(args); parseErr != nil {
		return parseErr
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if runtime.GOOS != "darwin" && !*dryRun {
		return fmt.Errorf("this installs a launchd agent and exists only on macOS; this is %s (use --dry-run to see the plan)", runtime.GOOS)
	}
	for name, value := range map[string]*string{"bin-dir": &layout.BinDir, "apps-dir": &layout.AppsDir, "db": &layout.DBPath, "log-dir": &layout.LogDir} {
		absolute, absErr := filepath.Abs(*value)
		if absErr != nil {
			return fmt.Errorf("--%s: %w", name, absErr)
		}
		*value = absolute
	}

	inst := &installer.Installer{
		Layout:       layout,
		Launchctl:    launchagent.ExecLaunchctl{},
		Run:          execRunner,
		Out:          os.Stdout,
		DryRun:       *dryRun,
		ReadyTimeout: time.Duration(*wait) * time.Second,
	}
	ctx := context.Background()
	if !install {
		_, uninstallErr := inst.Uninstall(ctx)
		return uninstallErr
	}
	absoluteArtifacts, err := filepath.Abs(*artifactsDir)
	if err != nil {
		return fmt.Errorf("--artifacts: %w", err)
	}
	report, err := inst.Install(ctx, installer.ArtifactsIn(absoluteArtifacts))
	if err != nil {
		return err
	}
	fmt.Printf("\ninstalled %s: daemon %s -> %s; serving %s -> %s\n",
		report.Artifact, report.BeforeInstalled, report.Artifact, report.BeforeServing, report.AfterServing)
	fmt.Printf("Tangent.app at %s adopts the daemon when opened; `tangent-install uninstall` removes both and keeps %s\n",
		layout.InstalledApp(), layout.DBPath)
	return nil
}

// execRunner runs a Tangent binary with a bounded wait and returns its
// combined output. --version and --db-check are the only invocations.
func execRunner(ctx context.Context, binary string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// #nosec G204 -- binary is the operator's artifact or the installed daemon; args are fixed verbs.
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(out), fmt.Errorf("exit status %d", exitErr.ExitCode())
		}
		return string(out), err
	}
	return string(out), nil
}
