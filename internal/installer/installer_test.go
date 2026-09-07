package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/launchagent"
)

// fakeDaemon stands in for the Tangent process on the stable port. Tests set
// what it says; the fake launchctl flips it when the agent is bootstrapped.
type fakeDaemon struct {
	mu             sync.Mutex
	serving        bool
	version        string
	migrationsPass bool
}

func (d *fakeDaemon) set(serving bool, version string, migrationsPass bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.serving, d.version, d.migrationsPass = serving, version, migrationsPass
}

func (d *fakeDaemon) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		if !d.serving {
			// Simulate "nothing listening": hijack and close.
			if hijacker, ok := w.(http.Hijacker); ok {
				if conn, _, err := hijacker.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		status, migrations, detail := "ok", "pass", "schema at expected version"
		if !d.migrationsPass {
			status, migrations, detail = "degraded", "fail", "schema is marked dirty at version 12"
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": status, "probe": "readiness",
			"checks": []map[string]string{
				{"name": "database", "status": "pass", "detail": "reachable"},
				{"name": "migrations", "status": migrations, "detail": detail},
			},
		})
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, _ *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		if !d.serving {
			if hijacker, ok := w.(http.Hijacker); ok {
				if conn, _, err := hijacker.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"protocolVersion": "2025-03-26", "serverInfo": map[string]string{"name": "tangent", "version": d.version}},
		})
	})
	return mux
}

// fakeLaunchctl records calls and, on bootstrap, brings the fake daemon up at
// the version of the binary the plist names (read back from the fixture).
type fakeLaunchctl struct {
	daemon *fakeDaemon
	loaded map[string]bool
	calls  []string
	// versionOnBootstrap is what the daemon reports after bootstrap; tests
	// set it to model a stale or foreign process answering the port.
	versionOnBootstrap string
	keepDown           bool
}

func (f *fakeLaunchctl) Bootstrap(_ context.Context, domain, plistPath string) error {
	f.calls = append(f.calls, "bootstrap "+filepath.Base(plistPath))
	f.loaded[domain+"/"+launchagent.Label] = true
	if !f.keepDown {
		f.daemon.set(true, f.versionOnBootstrap, true)
	}
	return nil
}

func (f *fakeLaunchctl) Bootout(_ context.Context, target string) error {
	f.calls = append(f.calls, "bootout")
	if !f.loaded[target] {
		return launchagent.ErrNotLoaded
	}
	delete(f.loaded, target)
	f.daemon.set(false, "", false)
	return nil
}

func (f *fakeLaunchctl) Loaded(_ context.Context, target string) (bool, error) {
	return f.loaded[target], nil
}

// fakeRunner answers `--version` from a per-binary table and `--db-check`
// from a switch, so the tests never execute anything.
type fakeRunner struct {
	versions    map[string]string // binary path -> "tangent vX.Y.Z"
	dbCheckFail bool
	dbChecks    []string // env seen on each --db-check
}

func (r *fakeRunner) run(_ context.Context, binary string, env []string, args ...string) (string, error) {
	switch strings.Join(args, " ") {
	case "--version":
		out, ok := r.versions[binary]
		if !ok {
			return "", errors.New("flag provided but not defined: -version")
		}
		return out + "\n", nil
	case "--db-check":
		r.dbChecks = append(r.dbChecks, strings.Join(env, " "))
		if r.dbCheckFail {
			return "tangent: db-check: the database is at schema 14; this binary understands 12", errors.New("exit status 1")
		}
		return `{"verification":{"healthy":true}}`, nil
	}
	return "", fmt.Errorf("unexpected invocation %s %v", binary, args)
}

// fixture builds an artifacts directory with a daemon stand-in and an app
// bundle stamped with version, plus a home directory.
type fixture struct {
	t         *testing.T
	home      string
	artifacts Artifacts
	daemon    *fakeDaemon
	server    *httptest.Server
	launchctl *fakeLaunchctl
	runner    *fakeRunner
	out       strings.Builder
}

func newFixture(t *testing.T, artifactVersion string) *fixture {
	t.Helper()
	f := &fixture{t: t, home: t.TempDir(), daemon: &fakeDaemon{}}
	dir := t.TempDir()
	f.artifacts = ArtifactsIn(dir)
	writeExecutable(t, f.artifacts.Binary)
	writeApp(t, f.artifacts.App, artifactVersion)
	f.server = httptest.NewServer(f.daemon.handler())
	t.Cleanup(f.server.Close)
	f.launchctl = &fakeLaunchctl{daemon: f.daemon, loaded: map[string]bool{}, versionOnBootstrap: "v" + artifactVersion}
	f.runner = &fakeRunner{versions: map[string]string{f.artifacts.Binary: "tangent v" + artifactVersion}}
	return f
}

func (f *fixture) installer(dryRun bool) *Installer {
	layout := DefaultLayout(f.home, 501)
	return &Installer{
		Layout: layout, Launchctl: f.launchctl, Run: f.runner.run,
		Out: &f.out, DryRun: dryRun, ReadyTimeout: 2 * time.Second,
		probeBaseURL: f.server.URL,
	}
}

// preinstall models an existing stable install at version, serving.
func (f *fixture) preinstall(version string) {
	layout := DefaultLayout(f.home, 501)
	writeExecutable(f.t, layout.InstalledBinary())
	writeApp(f.t, layout.InstalledApp(), version)
	f.runner.versions[layout.InstalledBinary()] = "tangent v" + version
	if err := os.MkdirAll(filepath.Dir(layout.DBPath), 0o750); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(layout.DBPath, []byte("sqlite stand-in"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	// Loaded agent, daemon serving that version.
	f.launchctl.loaded["gui/501/"+launchagent.Label] = true
	f.daemon.set(true, "v"+version, true)
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
}

func writeApp(t *testing.T, app, version string) {
	t.Helper()
	writeExecutable(t, filepath.Join(app, "Contents", "MacOS", "tangent-app"))
	if err := os.MkdirAll(filepath.Join(app, "Contents", "Resources"), 0o750); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.hollislabs.tangent</string>
<key>CFBundleShortVersionString</key><string>` + version + `</string>
<key>CFBundleVersion</key><string>` + version + `</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFreshInstallPlacesEverythingAndProvesTheDaemonIsUp(t *testing.T) {
	f := newFixture(t, "0.13.0")
	report, err := f.installer(false).Install(context.Background(), f.artifacts)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, f.out.String())
	}
	layout := DefaultLayout(f.home, 501)
	if report.BeforeInstalled != "none" || report.BeforeServing != "none" || report.Artifact != "v0.13.0" || report.AfterServing != "v0.13.0" {
		t.Fatalf("report = %+v", report)
	}
	for _, path := range []string{layout.InstalledBinary(), filepath.Join(layout.InstalledApp(), "Contents", "MacOS", "tangent-app"), launchagent.PlistPath(f.home)} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("%s not placed: %v", path, statErr)
		}
	}
	info, _ := os.Stat(layout.InstalledBinary())
	if info.Mode()&0o111 == 0 {
		t.Fatal("placed daemon is not executable")
	}
	raw, _ := os.ReadFile(launchagent.PlistPath(f.home))
	for _, want := range []string{layout.InstalledBinary(), "TANGENT_HTTP_PORT</key>\n\t\t<string>7842", layout.DBPath, filepath.Join(layout.LogDir, "tangent.log")} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("plist lacks %q:\n%s", want, raw)
		}
	}
	if strings.Join(f.launchctl.calls, "|") != "bootout|bootstrap "+launchagent.Label+".plist" {
		t.Fatalf("launchctl calls = %v", f.launchctl.calls)
	}
	if len(f.runner.dbChecks) != 0 {
		t.Fatal("a fresh install has no database to check")
	}
	if entries, _ := os.ReadDir(layout.BinDir); len(entries) != 1 {
		t.Fatalf("temp file left in %s: %v", layout.BinDir, entries)
	}
}

func TestUpgradeIsTheSamePathAndChecksTheDatabaseFirst(t *testing.T) {
	f := newFixture(t, "0.14.0")
	f.preinstall("0.13.0")
	report, err := f.installer(false).Install(context.Background(), f.artifacts)
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, f.out.String())
	}
	if report.BeforeInstalled != "v0.13.0" || report.BeforeServing != "v0.13.0" || report.Artifact != "v0.14.0" || report.AfterServing != "v0.14.0" {
		t.Fatalf("report = %+v", report)
	}
	layout := DefaultLayout(f.home, 501)
	if len(f.runner.dbChecks) != 1 || !strings.Contains(f.runner.dbChecks[0], "TANGENT_DB_PATH="+layout.DBPath) {
		t.Fatalf("--db-check must run once against the stable database with the artifact binary, got %v", f.runner.dbChecks)
	}
	if strings.Join(f.launchctl.calls, "|") != "bootout|bootstrap "+launchagent.Label+".plist" {
		t.Fatalf("upgrade must boot out then bootstrap, got %v", f.launchctl.calls)
	}
	if entries, _ := os.ReadDir(layout.AppsDir); len(entries) != 1 {
		t.Fatalf("staging or previous app left behind: %v", entries)
	}
	got, _ := os.ReadFile(filepath.Join(layout.InstalledApp(), "Contents", "Info.plist"))
	if !strings.Contains(string(got), "0.14.0") {
		t.Fatal("the placed app is not the new artifact")
	}
}

func TestInstallRefusesAMidMigrationDaemon(t *testing.T) {
	f := newFixture(t, "0.14.0")
	f.preinstall("0.13.0")
	f.daemon.set(true, "v0.13.0", false)
	_, err := f.installer(false).Install(context.Background(), f.artifacts)
	if err == nil || !strings.Contains(err.Error(), "not ready") || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("want a mid-migration refusal, got %v", err)
	}
	assertNothingChanged(t, f)
}

func TestInstallRefusesAForeignDaemonOnThePort(t *testing.T) {
	// Nothing installed, but something answers the port (the dev instance
	// still on 7842). The install must not replace it under the person.
	f := newFixture(t, "0.13.0")
	f.daemon.set(true, "v0.12.0", true)
	_, err := f.installer(false).Install(context.Background(), f.artifacts)
	if err == nil || !strings.Contains(err.Error(), "something other than the installed stable daemon") {
		t.Fatalf("want a foreign-daemon refusal, got %v", err)
	}
	assertNothingChanged(t, f)

	// Installed, but the port answers with a different version than the
	// installed binary reports: also not ours.
	g := newFixture(t, "0.14.0")
	g.preinstall("0.13.0")
	g.daemon.set(true, "v0.12.0", true)
	_, err = g.installer(false).Install(context.Background(), g.artifacts)
	if err == nil || !strings.Contains(err.Error(), "something other than the installed stable daemon") {
		t.Fatalf("want a foreign-daemon refusal, got %v", err)
	}
}

func TestInstallRefusesWhenDBCheckFails(t *testing.T) {
	f := newFixture(t, "0.14.0")
	f.preinstall("0.13.0")
	f.runner.dbCheckFail = true
	_, err := f.installer(false).Install(context.Background(), f.artifacts)
	if err == nil || !strings.Contains(err.Error(), "--db-check refused") || !strings.Contains(err.Error(), "schema 14") {
		t.Fatalf("want a db-check refusal carrying the tool's output, got %v", err)
	}
	assertNothingChanged(t, f)
}

func TestInstallRefusesADowngrade(t *testing.T) {
	f := newFixture(t, "0.12.0")
	f.preinstall("0.13.0")
	_, err := f.installer(false).Install(context.Background(), f.artifacts)
	if err == nil || !strings.Contains(err.Error(), "refusing to downgrade") {
		t.Fatalf("want a downgrade refusal, got %v", err)
	}
	assertNothingChanged(t, f)
}

func TestInstallRefusesMismatchedArtifacts(t *testing.T) {
	f := newFixture(t, "0.13.0")
	f.runner.versions[f.artifacts.Binary] = "tangent v0.14.0"
	_, err := f.installer(false).Install(context.Background(), f.artifacts)
	if err == nil || !strings.Contains(err.Error(), "not a pair") {
		t.Fatalf("want an artifact-pair refusal, got %v", err)
	}
	assertNothingChanged(t, f)
}

func TestInstallRefusesBadArtifactPaths(t *testing.T) {
	f := newFixture(t, "0.13.0")
	for name, artifacts := range map[string]Artifacts{
		"missing binary": {Binary: filepath.Join(t.TempDir(), "tangent"), App: f.artifacts.App},
		"missing app":    {Binary: f.artifacts.Binary, App: filepath.Join(t.TempDir(), "Tangent.app")},
		"app is a file":  {Binary: f.artifacts.Binary, App: f.artifacts.Binary},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := f.installer(false).Install(context.Background(), artifacts); err == nil {
				t.Fatal("must refuse")
			}
		})
	}
	unstamped := filepath.Join(t.TempDir(), "Tangent.app")
	writeApp(t, unstamped, "__TANGENT_VERSION__")
	if _, err := f.installer(false).Install(context.Background(), Artifacts{Binary: f.artifacts.Binary, App: unstamped}); err == nil || !strings.Contains(err.Error(), "substituted") {
		t.Fatalf("an unstamped Info.plist must be refused, got %v", err)
	}
	assertNothingChanged(t, f)
}

func TestInstallFailsNonZeroWhenReadyzNeverComes(t *testing.T) {
	f := newFixture(t, "0.13.0")
	f.launchctl.keepDown = true
	installer := f.installer(false)
	installer.ReadyTimeout = 600 * time.Millisecond
	_, err := installer.Install(context.Background(), f.artifacts)
	if err == nil || !strings.Contains(err.Error(), "nothing answered /readyz") || !strings.Contains(err.Error(), "tangent.log") {
		t.Fatalf("want a readiness timeout naming the log, got %v", err)
	}
}

func TestInstallFailsWhenTheServingVersionIsNotTheArtifact(t *testing.T) {
	f := newFixture(t, "0.13.0")
	f.launchctl.versionOnBootstrap = "v0.9.0"
	_, err := f.installer(false).Install(context.Background(), f.artifacts)
	if err == nil || !strings.Contains(err.Error(), "expected the artifact's v0.13.0") {
		t.Fatalf("want a version mismatch after start, got %v", err)
	}
}

func TestDryRunProbesEverythingAndChangesNothing(t *testing.T) {
	f := newFixture(t, "0.14.0")
	f.preinstall("0.13.0")
	report, err := f.installer(true).Install(context.Background(), f.artifacts)
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, f.out.String())
	}
	if report.BeforeInstalled != "v0.13.0" || report.BeforeServing != "v0.13.0" || report.Artifact != "v0.14.0" {
		t.Fatalf("dry run must still report what it found: %+v", report)
	}
	if len(f.launchctl.calls) != 0 {
		t.Fatalf("dry run touched launchd: %v", f.launchctl.calls)
	}
	if len(f.runner.dbChecks) != 1 {
		t.Fatal("dry run must still run the read-only --db-check")
	}
	got, _ := os.ReadFile(filepath.Join(DefaultLayout(f.home, 501).InstalledApp(), "Contents", "Info.plist"))
	if !strings.Contains(string(got), "0.13.0") {
		t.Fatal("dry run replaced the app")
	}
	for _, line := range report.Steps {
		if !strings.HasPrefix(line, "[dry-run]") {
			t.Fatalf("every dry-run step must be labeled, got %q", line)
		}
	}
	if !strings.Contains(f.out.String(), "would place") {
		t.Fatalf("dry run must print the actions it would take:\n%s", f.out.String())
	}
}

func TestUninstallRemovesWhatInstallPlacedAndKeepsTheDatabase(t *testing.T) {
	f := newFixture(t, "0.13.0")
	f.preinstall("0.13.0")
	// Write the plist too, as a real install would have.
	agent := launchagent.Installer{Home: f.home, UID: 501, Launchctl: f.launchctl}
	layout := DefaultLayout(f.home, 501)
	if _, err := agent.Install(context.Background(), launchagent.Config{Binary: layout.InstalledBinary(), Port: 7842}); err != nil {
		t.Fatal(err)
	}
	f.launchctl.calls = nil

	report, err := f.installer(false).Uninstall(context.Background())
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	for _, path := range []string{layout.InstalledBinary(), layout.InstalledApp(), launchagent.PlistPath(f.home)} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("%s should be gone", path)
		}
	}
	if _, statErr := os.Stat(layout.DBPath); statErr != nil {
		t.Fatal("uninstall must never remove the database")
	}
	if f.launchctl.calls[0] != "bootout" {
		t.Fatalf("uninstall must boot the agent out, got %v", f.launchctl.calls)
	}
	if !strings.Contains(strings.Join(report.Steps, "\n"), "never removed") {
		t.Fatal("uninstall must say the database is kept")
	}

	second, err := f.installer(false).Uninstall(context.Background())
	if err != nil {
		t.Fatalf("second uninstall must be a no-op, got %v", err)
	}
	if !strings.Contains(strings.Join(second.Steps, "\n"), "not present") {
		t.Fatalf("second uninstall should report nothing to remove: %v", second.Steps)
	}
}

func TestUninstallDryRunTouchesNothing(t *testing.T) {
	f := newFixture(t, "0.13.0")
	f.preinstall("0.13.0")
	layout := DefaultLayout(f.home, 501)
	if _, err := f.installer(true).Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(layout.InstalledBinary()); statErr != nil {
		t.Fatal("dry-run uninstall removed the daemon")
	}
	if len(f.launchctl.calls) != 0 {
		t.Fatalf("dry-run uninstall touched launchd: %v", f.launchctl.calls)
	}
}

// assertNothingChanged: a refused install leaves the filesystem and launchd as
// they were.
func assertNothingChanged(t *testing.T, f *fixture) {
	t.Helper()
	if len(f.launchctl.calls) != 0 {
		t.Fatalf("a refused install must not touch launchd, got %v", f.launchctl.calls)
	}
	layout := DefaultLayout(f.home, 501)
	for _, dir := range []string{layout.BinDir, layout.AppsDir} {
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				t.Fatalf("a refused install left %s in %s", entry.Name(), dir)
			}
		}
	}
	if _, statErr := os.Stat(launchagent.PlistPath(f.home)); statErr == nil && !f.launchctl.loaded["gui/501/"+launchagent.Label] {
		t.Fatal("a refused install wrote a plist")
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v0.13.0", "v0.13.0", 0}, {"v0.14.0", "v0.13.0", 1}, {"v0.13.1", "v0.13.0", 1},
		{"v0.12.9", "v0.13.0", -1}, {"v1.0.0", "v0.99.99", 1},
	} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
