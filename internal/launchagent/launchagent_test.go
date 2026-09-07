package launchagent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeLaunchctl records every call and models a launchd domain as a set of
// loaded service targets. No test in this package ever runs launchctl.
type fakeLaunchctl struct {
	loaded       map[string]bool
	calls        []string
	bootstrapErr error
}

func newFakeLaunchctl() *fakeLaunchctl { return &fakeLaunchctl{loaded: map[string]bool{}} }

func (f *fakeLaunchctl) Bootstrap(_ context.Context, domain, plistPath string) error {
	f.calls = append(f.calls, "bootstrap "+domain+" "+filepath.Base(plistPath))
	if f.bootstrapErr != nil {
		return f.bootstrapErr
	}
	f.loaded[domain+"/"+Label] = true
	return nil
}

func (f *fakeLaunchctl) Bootout(_ context.Context, target string) error {
	f.calls = append(f.calls, "bootout "+target)
	if !f.loaded[target] {
		return ErrNotLoaded
	}
	delete(f.loaded, target)
	return nil
}

func (f *fakeLaunchctl) Loaded(_ context.Context, target string) (bool, error) {
	return f.loaded[target], nil
}

// fakeBinary writes an executable file standing in for the tangent daemon.
func fakeBinary(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "tangent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	return path
}

func newInstaller(t *testing.T) (Installer, *fakeLaunchctl, string) {
	t.Helper()
	home := t.TempDir()
	fake := newFakeLaunchctl()
	return Installer{Home: home, UID: 501, Launchctl: fake}, fake, home
}

func TestRenderWritesTheDecidedShape(t *testing.T) {
	home := "/Users/someone"
	raw, err := Render(Config{Binary: "/usr/local/bin/tangent", Port: 7842, DBPath: "/Users/someone/.tangent/tangent.db"}, home)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		"<key>Label</key>\n\t<string>" + Label + "</string>",
		"<string>/usr/local/bin/tangent</string>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"<key>KeepAlive</key>\n\t<false/>",
		"<key>TANGENT_HTTP_PORT</key>\n\t\t<string>7842</string>",
		"<key>TANGENT_DB_PATH</key>\n\t\t<string>/Users/someone/.tangent/tangent.db</string>",
		"<key>StandardOutPath</key>\n\t<string>/Users/someone/Library/Logs/Tangent/tangent.log</string>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered plist lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "tangent-app") {
		t.Error("the LaunchAgent must run the headless daemon, never the app")
	}
	args, err := ProgramArguments(raw)
	if err != nil || len(args) != 1 || args[0] != "/usr/local/bin/tangent" {
		t.Fatalf("ProgramArguments = %v, %v", args, err)
	}

	// Without a port or database path there is no EnvironmentVariables dict
	// at all: the daemon's own defaults are the configuration.
	bare, err := Render(Config{Binary: "/usr/local/bin/tangent"}, home)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bare), "EnvironmentVariables") {
		t.Error("no port and no db path must render no EnvironmentVariables")
	}

	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("plutil"); err == nil {
			path := filepath.Join(t.TempDir(), Label+".plist")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil { //nolint:gosec // test: plutil on a temp file
				t.Fatalf("plutil -lint rejected the rendered plist: %v\n%s", err, out)
			}
		}
	}
}

func TestRenderEscapesAndRefusesBadInput(t *testing.T) {
	raw, err := Render(Config{Binary: "/Applications/Tangent & Co/tangent"}, "/Users/x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Tangent &amp; Co") {
		t.Error("an ampersand in the path must be XML-escaped")
	}
	if _, err := Render(Config{Binary: "tangent"}, "/Users/x"); err == nil {
		t.Error("a relative binary path must be refused")
	}
	if _, err := Render(Config{}, "/Users/x"); err == nil {
		t.Error("an empty binary path must be refused")
	}
	if _, err := Render(Config{Binary: "/usr/local/bin/tangent", Port: 70000}, "/Users/x"); err == nil {
		t.Error("an out-of-range port must be refused")
	}
}

func TestValidateBinaryRefusesEveryWayThePathCanBeWrong(t *testing.T) {
	dir := t.TempDir()
	good := fakeBinary(t, dir)
	if err := ValidateBinary(good); err != nil {
		t.Fatalf("a real executable must validate: %v", err)
	}
	notExecutable := filepath.Join(dir, "tangent.txt")
	if err := os.WriteFile(notExecutable, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"empty":          "",
		"relative":       "tangent",
		"missing":        filepath.Join(dir, "moved-away", "tangent"),
		"directory":      dir,
		"not executable": notExecutable,
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateBinary(path)
			if err == nil {
				t.Fatalf("%q must be refused", path)
			}
			if !strings.HasPrefix(err.Error(), "launch agent:") {
				t.Fatalf("refusal must be a person-facing message, got %q", err)
			}
		})
	}
}

func TestInstallIsIdempotentAndAlwaysReloads(t *testing.T) {
	installer, fake, home := newInstaller(t)
	binary := fakeBinary(t, t.TempDir())
	ctx := context.Background()

	first, err := installer.Install(ctx, Config{Binary: binary, Port: 7842})
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	if first.PlistPath != PlistPath(home) || !first.Changed || first.WasLoaded || !first.Loaded {
		t.Fatalf("first install result = %+v", first)
	}
	if _, statErr := os.Stat(first.PlistPath); statErr != nil {
		t.Fatalf("plist not written: %v", statErr)
	}
	if _, statErr := os.Stat(DefaultLogDir(home)); statErr != nil {
		t.Fatalf("log directory not created: %v", statErr)
	}
	if entries, _ := os.ReadDir(filepath.Dir(first.PlistPath)); len(entries) != 1 {
		t.Fatalf("temp file left beside the plist: %v", entries)
	}
	wantCalls := []string{"bootout gui/501/" + Label, "bootstrap gui/501 " + Label + ".plist"}
	if strings.Join(fake.calls, "|") != strings.Join(wantCalls, "|") {
		t.Fatalf("launchctl calls = %v, want %v", fake.calls, wantCalls)
	}

	fake.calls = nil
	second, err := installer.Install(ctx, Config{Binary: binary, Port: 7842})
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if second.Changed || !second.WasLoaded || !second.Loaded {
		t.Fatalf("second identical install result = %+v, want unchanged, was loaded, loaded", second)
	}
	if strings.Join(fake.calls, "|") != strings.Join(wantCalls, "|") {
		t.Fatalf("second install must boot out and bootstrap again so launchd matches the file, got %v", fake.calls)
	}

	third, err := installer.Install(ctx, Config{Binary: binary, Port: 7843})
	if err != nil {
		t.Fatalf("third install: %v", err)
	}
	if !third.Changed {
		t.Fatal("a different port must report the plist as changed")
	}
}

func TestInstallRefusesWhenTheBinaryIsNotWhereThePlistWouldSay(t *testing.T) {
	installer, fake, home := newInstaller(t)
	missing := filepath.Join(t.TempDir(), "Tangent.app", "Contents", "MacOS", "tangent")
	_, err := installer.Install(context.Background(), Config{Binary: missing})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("install must refuse a missing binary with a clear message, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("a refused install must not touch launchd, got %v", fake.calls)
	}
	if _, statErr := os.Stat(PlistPath(home)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("a refused install must not write a plist")
	}
}

func TestInstallBootstrapFailureLeavesThePlistAndReportsIt(t *testing.T) {
	installer, fake, _ := newInstaller(t)
	fake.bootstrapErr = errors.New("Bootstrap failed: 5: Input/output error")
	binary := fakeBinary(t, t.TempDir())
	result, err := installer.Install(context.Background(), Config{Binary: binary})
	if err == nil || !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("bootstrap failure must surface, got %v", err)
	}
	if result.Loaded {
		t.Fatal("a failed bootstrap must not report loaded")
	}
}

func TestUninstallIsIdempotent(t *testing.T) {
	installer, fake, home := newInstaller(t)
	binary := fakeBinary(t, t.TempDir())
	ctx := context.Background()
	if _, err := installer.Install(ctx, Config{Binary: binary}); err != nil {
		t.Fatal(err)
	}

	first, err := installer.Uninstall(ctx)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !first.Changed || !first.WasLoaded || first.Loaded {
		t.Fatalf("uninstall result = %+v", first)
	}
	if _, statErr := os.Stat(PlistPath(home)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("plist must be removed")
	}
	if fake.loaded["gui/501/"+Label] {
		t.Fatal("agent must be booted out")
	}

	second, err := installer.Uninstall(ctx)
	if err != nil {
		t.Fatalf("second uninstall must be a no-op, got %v", err)
	}
	if second.Changed || second.WasLoaded {
		t.Fatalf("second uninstall result = %+v, want nothing to do", second)
	}
}

func TestStatusReportsAMovedBinary(t *testing.T) {
	installer, _, home := newInstaller(t)
	ctx := context.Background()

	none, err := installer.Status(ctx)
	if err != nil || none.Installed || none.Loaded {
		t.Fatalf("status with no plist = %+v, %v", none, err)
	}

	binaryDir := t.TempDir()
	binary := fakeBinary(t, binaryDir)
	if _, installErr := installer.Install(ctx, Config{Binary: binary}); installErr != nil {
		t.Fatal(installErr)
	}
	ok, err := installer.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ok.Installed || !ok.Loaded || !ok.BinaryOK || ok.Binary != binary || ok.Problem != "" {
		t.Fatalf("status after install = %+v", ok)
	}

	// The binary moves (or Tangent.app is dragged somewhere else). The plist
	// still names the old path; status must say so instead of a silent
	// no-start at next login.
	if renameErr := os.Rename(binary, binary+".moved"); renameErr != nil {
		t.Fatal(renameErr)
	}
	moved, err := installer.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if moved.BinaryOK || !strings.Contains(moved.Problem, "does not exist") || !strings.Contains(moved.Problem, "install again") {
		t.Fatalf("status with a moved binary = %+v", moved)
	}
	if moved.PlistPath != PlistPath(home) {
		t.Fatalf("status plist path = %q", moved.PlistPath)
	}
}

func TestProgramArgumentsSkipsUnrelatedKeys(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>EnvironmentVariables</key><dict><key>TANGENT_HTTP_PORT</key><string>7842</string></dict>
  <key>Nested</key><array><dict><key>ProgramArguments</key><array><string>decoy</string></array></dict></array>
  <key>ProgramArguments</key><array><string>/opt/tangent</string><string>--flag</string></array>
  <key>RunAtLoad</key><true/>
</dict></plist>`)
	args, err := ProgramArguments(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 2 || args[0] != "/opt/tangent" || args[1] != "--flag" {
		t.Fatalf("ProgramArguments = %v", args)
	}
	if _, err := ProgramArguments([]byte(`<plist version="1.0"><dict><key>Label</key><string>x</string></dict></plist>`)); err == nil {
		t.Fatal("a plist without ProgramArguments must be an error")
	}
	if _, err := ProgramArguments([]byte(`not xml`)); err == nil {
		t.Fatal("garbage must be an error")
	}
}
