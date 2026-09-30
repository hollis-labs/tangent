// Package installer places a tagged Tangent release on a Mac and keeps it
// running: the headless daemon under a user LaunchAgent, the desktop app in
// the Applications folder, one database. Install and upgrade are the same
// operation (CW-20260907-0020); uninstall removes what install placed and
// never the database.
//
// Everything that can go wrong is checked before anything is changed, and
// each refusal names the thing to fix:
//
//   - the artifacts must be a matching pair (the daemon's `--version` and the
//     app bundle's Info.plist name the same release);
//   - a daemon that is serving the port must be the installed stable one and
//     must not be mid-migration; anything else serving there is refused, so
//     the dev instance has to be moved first rather than silently replaced;
//   - the artifact daemon's `--db-check` must pass against the existing
//     database, which is what refuses a downgrade (bab0b89: an older binary
//     refuses a newer database rather than repairing it);
//   - after the LaunchAgent is (re)loaded, /readyz must answer ok and the
//     serving version must be the artifact's, or the install fails non-zero.
//
// Nothing here runs launchctl, executes a binary, or touches the filesystem
// unless a caller wires the real Launchctl, the real runner, and real paths;
// tests use fakes, a temporary home, and an httptest server standing in for
// the daemon. DryRun performs every read-only probe and prints every action
// without performing it.
package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tangent/internal/launchagent"
)

// Names of the two artifacts inside an artifacts directory, and of the placed
// files. `make build` writes the first; `make build-app` writes the second.
const (
	BinaryName = "tangent"
	AppName    = "Tangent.app"
)

// Layout is where the release goes and how the daemon is configured.
type Layout struct {
	// Home is the user's home directory (LaunchAgents live under it).
	Home string
	// BinDir receives the daemon binary; default ~/.local/bin.
	BinDir string
	// AppsDir receives Tangent.app; default ~/Applications.
	AppsDir string
	// Port is the stable daemon's port. The daemon's own default is 7842.
	Port int
	// DBPath is the stable database. The daemon's own default is
	// ~/.tangent/tangent.db; it is written into the LaunchAgent explicitly so
	// the plist says which database it means.
	DBPath string
	// LogDir receives tangent.log; default ~/.tangent/logs.
	LogDir string
	// Env is further daemon environment for the LaunchAgent (plugin settings,
	// for example). The agent keeps whatever extra variables the installed
	// plist already carries, and Env adds to or overrides them.
	Env map[string]string
	// UID selects the launchd domain gui/<UID>.
	UID int
}

// DefaultLayout fills the defaults for a home directory and uid.
func DefaultLayout(home string, uid int) Layout {
	return Layout{
		Home:    home,
		BinDir:  filepath.Join(home, ".local", "bin"),
		AppsDir: filepath.Join(home, "Applications"),
		Port:    7842,
		DBPath:  filepath.Join(home, ".tangent", "tangent.db"),
		LogDir:  filepath.Join(home, ".tangent", "logs"),
		UID:     uid,
	}
}

// InstalledBinary is where the daemon lives once placed.
func (l Layout) InstalledBinary() string { return filepath.Join(l.BinDir, BinaryName) }

// InstalledApp is where the app lives once placed.
func (l Layout) InstalledApp() string { return filepath.Join(l.AppsDir, AppName) }

// BaseURL is the stable daemon's loopback base URL.
func (l Layout) BaseURL() string { return "http://127.0.0.1:" + strconv.Itoa(l.Port) }

// Artifacts are the two files a tagged build produces.
type Artifacts struct {
	Binary string // the headless daemon
	App    string // the Tangent.app bundle directory
}

// ArtifactsIn names the artifacts inside a directory.
func ArtifactsIn(dir string) Artifacts {
	return Artifacts{Binary: filepath.Join(dir, BinaryName), App: filepath.Join(dir, AppName)}
}

// Runner executes a Tangent binary and returns its combined output. The real
// one is exec; tests substitute a fake.
type Runner func(ctx context.Context, binary string, env []string, args ...string) (string, error)

// Installer performs install, upgrade, and uninstall against one Layout.
type Installer struct {
	Layout
	Launchctl launchagent.Launchctl
	Run       Runner
	// HTTP probes the daemon. Nil means a client with a short timeout.
	HTTP *http.Client
	// Out receives the human-readable step log. Nil discards it.
	Out io.Writer
	// DryRun performs read-only probes and prints every action instead of
	// performing it.
	DryRun bool
	// ReadyTimeout bounds the wait for /readyz after (re)loading the agent.
	// Zero means 30 seconds.
	ReadyTimeout time.Duration
	// probeBaseURL overrides Layout.BaseURL; tests point it at httptest.
	probeBaseURL string
}

// Report is what an operation observed and did.
type Report struct {
	BeforeInstalled string // version of the previously installed daemon binary, or "none"
	BeforeServing   string // version answering the port before, or "none"
	Artifact        string // version of the artifacts
	AfterServing    string // version answering the port after
	Steps           []string
}

func (i *Installer) say(format string, args ...any) {
	if i.Out != nil {
		fmt.Fprintf(i.Out, format+"\n", args...)
	}
}

func (i *Installer) step(report *Report, format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if i.DryRun {
		line = "[dry-run] " + line
	}
	report.Steps = append(report.Steps, line)
	i.say("%s", line)
}

func (i *Installer) client() *http.Client {
	if i.HTTP != nil {
		return i.HTTP
	}
	return &http.Client{Timeout: 2 * time.Second}
}

func (i *Installer) baseURL() string {
	if i.probeBaseURL != "" {
		return i.probeBaseURL
	}
	return i.BaseURL()
}

// Install places the artifacts and (re)loads the LaunchAgent. It is the
// upgrade path too: the same call with a newer artifact.
func (i *Installer) Install(ctx context.Context, artifacts Artifacts) (Report, error) {
	var report Report
	if err := i.requireDeps(); err != nil {
		return report, err
	}

	// 1. The artifacts, and that they are a pair.
	if err := launchagent.ValidateBinary(artifacts.Binary); err != nil {
		return report, fmt.Errorf("install: artifact %w", err)
	}
	appVersion, err := validateApp(artifacts.App)
	if err != nil {
		return report, fmt.Errorf("install: %w", err)
	}
	artifactVersion, err := i.binaryVersion(ctx, artifacts.Binary)
	if err != nil {
		return report, fmt.Errorf("install: artifact daemon: %w", err)
	}
	if artifactVersion != "v"+appVersion {
		return report, fmt.Errorf("install: the artifacts are not a pair: %s reports %s but %s is stamped %s; build both from one tagged tree",
			artifacts.Binary, artifactVersion, artifacts.App, appVersion)
	}
	report.Artifact = artifactVersion
	i.step(&report, "artifacts: daemon %s and %s agree on %s", artifacts.Binary, AppName, artifactVersion)

	// 2. What is here now.
	report.BeforeInstalled = "none"
	if _, statErr := os.Stat(i.InstalledBinary()); statErr == nil {
		installed, versionErr := i.binaryVersion(ctx, i.InstalledBinary())
		if versionErr != nil {
			return report, fmt.Errorf("install: the installed daemon at %s did not report a version: %w; remove it or fix it before upgrading", i.InstalledBinary(), versionErr)
		}
		report.BeforeInstalled = installed
	}
	serving := i.probe(ctx)
	report.BeforeServing = "none"
	if serving.answering {
		report.BeforeServing = serving.version
	}
	i.step(&report, "before: installed daemon %s; serving on %s: %s", report.BeforeInstalled, i.baseURL(), report.BeforeServing)

	// 3. Refusals about the running daemon.
	if serving.answering {
		if !serving.migrationsPass {
			return report, fmt.Errorf("install: the daemon on %s is not ready: %s. It may be mid-migration; wait for /readyz to pass, or stop it, then retry",
				i.baseURL(), serving.detail)
		}
		if report.BeforeInstalled == "none" || serving.version != report.BeforeInstalled {
			return report, fmt.Errorf("install: something other than the installed stable daemon is serving %s (version %s; installed %s). Stop it first — a dev instance on this port has to move to its own port (CW-20260907-0018) before the stable install can take it",
				i.baseURL(), report.BeforeServing, report.BeforeInstalled)
		}
	}
	if report.BeforeInstalled != "none" && compareVersions(artifactVersion, report.BeforeInstalled) < 0 {
		return report, fmt.Errorf("install: refusing to downgrade the daemon from %s to %s; the database may be newer than the artifact understands (bab0b89 refuses, it does not repair)",
			report.BeforeInstalled, artifactVersion)
	}

	// 4. The artifact daemon must accept the existing database.
	if _, statErr := os.Stat(i.DBPath); statErr == nil {
		env := []string{"TANGENT_DB_PATH=" + i.DBPath, "TANGENT_HTTP_PORT=" + strconv.Itoa(i.Port)}
		out, checkErr := i.Run(ctx, artifacts.Binary, env, "--db-check")
		if checkErr != nil {
			return report, fmt.Errorf("install: the artifact daemon's --db-check refused %s: %w\n%s", i.DBPath, checkErr, strings.TrimSpace(out))
		}
		i.step(&report, "db-check: %s accepted %s", artifactVersion, i.DBPath)
	} else {
		i.step(&report, "db-check: no database at %s yet; the daemon creates it on first start", i.DBPath)
	}

	// 5. Place the binary and the app.
	if placeErr := i.placeBinary(&report, artifacts.Binary); placeErr != nil {
		return report, placeErr
	}
	if placeErr := i.placeApp(&report, artifacts.App); placeErr != nil {
		return report, placeErr
	}

	// 6. The LaunchAgent: boot out then bootstrap, so upgrade and install are
	//    one path and launchd always runs what is on disk.
	agent := launchagent.Installer{Home: i.Home, UID: i.UID, Launchctl: i.Launchctl}
	config := launchagent.Config{Binary: i.InstalledBinary(), Port: i.Port, DBPath: i.DBPath, LogDir: i.LogDir, Env: i.Env}
	if i.DryRun {
		i.step(&report, "launch agent: would write %s and bootout/bootstrap gui/%d/%s", launchagent.PlistPath(i.Home), i.UID, launchagent.Label)
		i.step(&report, "after: would wait for %s/readyz and confirm the serving version is %s", i.baseURL(), artifactVersion)
		report.AfterServing = "(dry-run)"
		return report, nil
	}
	result, err := agent.Install(ctx, config)
	if err != nil {
		return report, fmt.Errorf("install: %w", err)
	}
	i.step(&report, "launch agent: %s written and loaded (was loaded before: %v)", result.PlistPath, result.WasLoaded)

	// 7. Prove it is up, and that it is the artifact.
	after, err := i.waitReady(ctx)
	if err != nil {
		return report, fmt.Errorf("install: %w; the files are in place and the agent is loaded, but nothing healthy answered. Check %s/tangent.log", err, i.LogDir)
	}
	report.AfterServing = after.version
	if after.version != artifactVersion {
		return report, fmt.Errorf("install: %s answers with version %s, expected the artifact's %s; something else is serving that port", i.baseURL(), after.version, artifactVersion)
	}
	i.step(&report, "after: serving on %s: %s", i.baseURL(), report.AfterServing)
	return report, nil
}

// Uninstall boots the agent out and removes the binary and the app. The
// database and its directory are never touched; that is a deliberate refusal
// to be the thing that loses a person's rooms.
func (i *Installer) Uninstall(ctx context.Context) (Report, error) {
	var report Report
	if i.Launchctl == nil {
		return report, errors.New("uninstall: no launchctl implementation")
	}
	agent := launchagent.Installer{Home: i.Home, UID: i.UID, Launchctl: i.Launchctl}
	if i.DryRun {
		i.step(&report, "launch agent: would bootout gui/%d/%s and remove %s", i.UID, launchagent.Label, launchagent.PlistPath(i.Home))
	} else {
		result, err := agent.Uninstall(ctx)
		if err != nil {
			return report, fmt.Errorf("uninstall: %w", err)
		}
		i.step(&report, "launch agent: removed=%v (was loaded: %v)", result.Changed, result.WasLoaded)
	}
	for _, path := range []string{i.InstalledBinary(), i.InstalledApp()} {
		_, statErr := os.Stat(path)
		switch {
		case errors.Is(statErr, os.ErrNotExist):
			i.step(&report, "%s: not present", path)
		case statErr != nil:
			return report, fmt.Errorf("uninstall: %s: %w", path, statErr)
		case i.DryRun:
			i.step(&report, "would remove %s", path)
		default:
			if err := os.RemoveAll(path); err != nil {
				return report, fmt.Errorf("uninstall: remove %s: %w", path, err)
			}
			i.step(&report, "removed %s", path)
		}
	}
	i.step(&report, "kept %s and %s: the database is never removed by uninstall", i.DBPath, i.LogDir)
	return report, nil
}

func (i *Installer) requireDeps() error {
	if i.Launchctl == nil {
		return errors.New("install: no launchctl implementation")
	}
	if i.Run == nil {
		return errors.New("install: no binary runner")
	}
	if i.Home == "" || i.BinDir == "" || i.AppsDir == "" || i.DBPath == "" || i.LogDir == "" || i.Port <= 0 {
		return errors.New("install: incomplete layout")
	}
	return nil
}

var versionLine = regexp.MustCompile(`\bv?(\d+\.\d+\.\d+)\b`)

// binaryVersion runs `<binary> --version` and normalises the answer to
// "vX.Y.Z", the spelling internal/envelope.HostVersion uses.
func (i *Installer) binaryVersion(ctx context.Context, binary string) (string, error) {
	out, err := i.Run(ctx, binary, nil, "--version")
	if err != nil {
		return "", fmt.Errorf("%s --version: %w (%s)", binary, err, strings.TrimSpace(out))
	}
	match := versionLine.FindStringSubmatch(out)
	if match == nil {
		return "", fmt.Errorf("%s --version printed %q, not a version", binary, strings.TrimSpace(out))
	}
	return "v" + match[1], nil
}

// validateApp checks the bundle shape the install relies on and returns the
// version Info.plist is stamped with (without a "v").
func validateApp(app string) (string, error) {
	info, err := os.Stat(app)
	if err != nil {
		return "", fmt.Errorf("artifact app %s: %w", app, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("artifact app %s is not a bundle directory", app)
	}
	executable := filepath.Join(app, "Contents", "MacOS", "tangent-app")
	if validateErr := launchagent.ValidateBinary(executable); validateErr != nil {
		return "", fmt.Errorf("artifact app %s: %w", app, validateErr)
	}
	raw, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist")) // #nosec G304 -- operator-chosen artifact path
	if err != nil {
		return "", fmt.Errorf("artifact app %s: Info.plist: %w", app, err)
	}
	version := plistString(raw, "CFBundleShortVersionString")
	if version == "" || strings.Contains(version, "__") {
		return "", fmt.Errorf("artifact app %s: Info.plist has no substituted CFBundleShortVersionString (got %q)", app, version)
	}
	return version, nil
}

func plistString(raw []byte, key string) string {
	pattern := regexp.MustCompile(`<key>` + regexp.QuoteMeta(key) + `</key>\s*<string>([^<]*)</string>`)
	match := pattern.FindSubmatch(raw)
	if match == nil {
		return ""
	}
	return strings.TrimSpace(string(match[1]))
}

// compareVersions orders "vX.Y.Z" strings; non-numeric parts compare as 0.
func compareVersions(a, b string) int {
	parse := func(v string) [3]int {
		var out [3]int
		for index, part := range strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3) {
			out[index], _ = strconv.Atoi(part)
		}
		return out
	}
	pa, pb := parse(a), parse(b)
	for index := range pa {
		if pa[index] != pb[index] {
			if pa[index] < pb[index] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// servingState is what the probes learned about the port.
type servingState struct {
	answering      bool
	migrationsPass bool
	version        string
	detail         string
}

// probe asks /readyz and then MCP initialize. It never fails the install by
// itself: not answering is a valid state (fresh install, stopped daemon).
func (i *Installer) probe(ctx context.Context) servingState {
	var state servingState
	client := i.client()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, i.baseURL()+"/readyz", nil)
	if err != nil {
		return state
	}
	response, err := client.Do(request)
	if err != nil {
		return state
	}
	defer func() { _ = response.Body.Close() }()
	state.answering = true
	var readiness struct {
		Status string `json:"status"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if decodeErr := json.NewDecoder(response.Body).Decode(&readiness); decodeErr != nil {
		state.detail = "/readyz answered " + response.Status + " with an unreadable body"
	} else {
		state.migrationsPass = true
		for _, check := range readiness.Checks {
			if check.Name == "migrations" && check.Status != "pass" {
				state.migrationsPass = false
				state.detail = "migrations: " + check.Detail
			}
		}
		if readiness.Status != "ok" && state.detail == "" {
			state.detail = "/readyz status " + readiness.Status
		}
	}
	state.version = i.servingVersion(ctx)
	return state
}

// servingVersion reads serverInfo.version from a stateless MCP initialize,
// which is the one place a running Tangent states its release.
func (i *Installer) servingVersion(ctx context.Context) string {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"tangent-install","version":"0"}}}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, i.baseURL()+"/mcp", strings.NewReader(body))
	if err != nil {
		return "unknown"
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := i.client().Do(request)
	if err != nil {
		return "unknown"
	}
	defer func() { _ = response.Body.Close() }()
	var initialized struct {
		Result struct {
			ServerInfo struct {
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if decodeErr := json.NewDecoder(response.Body).Decode(&initialized); decodeErr != nil || initialized.Result.ServerInfo.Version == "" {
		return "unknown"
	}
	return initialized.Result.ServerInfo.Version
}

// waitReady polls until /readyz is ok and migrations pass, or the timeout.
func (i *Installer) waitReady(ctx context.Context) (servingState, error) {
	timeout := i.ReadyTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var last servingState
	for {
		last = i.probe(ctx)
		if last.answering && last.migrationsPass && last.detail == "" {
			return last, nil
		}
		if time.Now().After(deadline) {
			why := "nothing answered /readyz"
			if last.answering {
				why = "/readyz never passed (" + last.detail + ")"
			}
			return last, fmt.Errorf("%s within %s", why, timeout)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (i *Installer) placeBinary(report *Report, source string) error {
	dest := i.InstalledBinary()
	if i.DryRun {
		i.step(report, "would place %s at %s", source, dest)
		return nil
	}
	if err := os.MkdirAll(i.BinDir, 0o755); err != nil { //nolint:gosec // a bin directory is conventionally 0755
		return fmt.Errorf("install: create %s: %w", i.BinDir, err)
	}
	tmp := filepath.Join(i.BinDir, "."+BinaryName+".installing")
	if err := copyFile(source, tmp, 0o755); err != nil { //nolint:gosec // the daemon must be executable
		return fmt.Errorf("install: copy daemon: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install: place daemon: %w", err)
	}
	i.step(report, "placed daemon at %s", dest)
	return nil
}

func (i *Installer) placeApp(report *Report, source string) error {
	dest := i.InstalledApp()
	if i.DryRun {
		i.step(report, "would place %s at %s", source, dest)
		return nil
	}
	if err := os.MkdirAll(i.AppsDir, 0o755); err != nil { //nolint:gosec // ~/Applications is conventionally 0755
		return fmt.Errorf("install: create %s: %w", i.AppsDir, err)
	}
	staging := filepath.Join(i.AppsDir, "."+AppName+".installing")
	_ = os.RemoveAll(staging)
	if err := copyTree(source, staging); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("install: copy app: %w", err)
	}
	previous := filepath.Join(i.AppsDir, "."+AppName+".previous")
	_ = os.RemoveAll(previous)
	if _, statErr := os.Stat(dest); statErr == nil {
		if err := os.Rename(dest, previous); err != nil {
			_ = os.RemoveAll(staging)
			return fmt.Errorf("install: set aside the existing app: %w", err)
		}
	}
	if err := os.Rename(staging, dest); err != nil {
		// Put the previous app back so the person is not left without one.
		_ = os.Rename(previous, dest)
		_ = os.RemoveAll(staging)
		return fmt.Errorf("install: place app: %w", err)
	}
	_ = os.RemoveAll(previous)
	i.step(report, "placed %s at %s", AppName, dest)
	return nil
}

func copyFile(source, dest string, mode os.FileMode) error {
	in, err := os.Open(source) // #nosec G304 -- operator-chosen artifact path
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode) // #nosec G304 -- derived from the layout
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Chmod(mode); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// copyTree copies a directory recursively, preserving file modes. Symlinks
// inside an app bundle are copied as links. The source is the operator's own
// artifact directory and the destination a fresh staging directory this
// package just created, so the walk-then-operate pattern gosec flags (G122)
// has no hostile writer to race here.
func copyTree(source, dest string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&os.ModeSymlink != 0:
			link, readErr := os.Readlink(path)
			if readErr != nil {
				return readErr
			}
			return os.Symlink(link, target) //nolint:gosec // G122: fresh staging dir, operator-owned source; see the function comment
		default:
			return copyFile(path, target, info.Mode().Perm())
		}
	})
}
