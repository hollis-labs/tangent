// Package packagecheck asserts that a built macOS .app bundle is
// well-formed before it ships. It is modeled on Tachyon's
// cmd/tachyon-packagecheck (~/dev/projects/tachyon/cmd/tachyon-packagecheck)
// — a build-time Go assertion instead of discovering a malformed bundle at
// runtime — but checks a different thing: Tachyon's packagecheck verifies
// embedded frontend assets; Tangent's frontend is already gated by `make
// check-envelopes` and the Go/vitest suites, so this package instead
// verifies the bundle shape itself: Info.plist validity, the main
// executable, and the icon.
package packagecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExpectedBundleID is the bundle identifier CW-20260905-0032 wires into
// packaging/macos/Info.plist. Every bundle this package verifies must carry
// exactly this CFBundleIdentifier.
const ExpectedBundleID = "com.hollislabs.tangent"

// templatePlaceholder is scripts/build-macos-app.sh's version substitution
// token. Seeing it survive into a built bundle means the sed step was
// skipped or broke — a real bug, not a cosmetic one, so it fails the build.
const templatePlaceholder = "__TANGENT_VERSION__"

// infoPlist is the subset of Info.plist keys this package inspects.
// encoding/json ignores every key it doesn't name when decoding into it.
type infoPlist struct {
	CFBundleExecutable         string `json:"CFBundleExecutable"`
	CFBundleIdentifier         string `json:"CFBundleIdentifier"`
	CFBundleIconFile           string `json:"CFBundleIconFile"`
	CFBundleShortVersionString string `json:"CFBundleShortVersionString"`
	CFBundleVersion            string `json:"CFBundleVersion"`
}

// VerifyMacOSBundle checks that appPath — e.g. "Tangent.app", or the
// pre-rename "Tangent.app-bin" staging directory scripts/build-macos-app.sh
// verifies before promoting it into place — is a launchable, internally
// consistent macOS application bundle:
//
//   - Contents/Info.plist exists and is valid plist XML (plutil -lint).
//   - Its CFBundleIdentifier matches ExpectedBundleID.
//   - Neither version key was left holding the unsubstituted
//     __TANGENT_VERSION__ template placeholder.
//   - Its CFBundleExecutable names a file under Contents/MacOS that exists
//     and is executable.
//   - Its CFBundleIconFile names an .icns file under Contents/Resources
//     that exists and is non-empty.
//
// Every problem found is reported together (errors.Join) rather than only
// the first, since a CI log is more useful with the complete list than one
// fix-rerun-discover-the-next-one cycle at a time.
//
// This shells out to plutil, which is macOS-only; calling it on any other
// platform fails with a plain "executable file not found" error.
func VerifyMacOSBundle(ctx context.Context, appPath string) error {
	info, err := os.Stat(appPath)
	if err != nil {
		return fmt.Errorf("packagecheck: stat bundle %q: %w", appPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("packagecheck: %q is not a directory", appPath)
	}

	plistPath := filepath.Join(appPath, "Contents", "Info.plist")
	if err = lintPlist(ctx, plistPath); err != nil {
		return err
	}

	plist, err := readPlist(ctx, plistPath)
	if err != nil {
		return err
	}

	var problems []error
	if plist.CFBundleIdentifier != ExpectedBundleID {
		problems = append(problems, fmt.Errorf("packagecheck: CFBundleIdentifier is %q, want %q", plist.CFBundleIdentifier, ExpectedBundleID))
	}
	if err := verifyVersionSubstituted("CFBundleShortVersionString", plist.CFBundleShortVersionString); err != nil {
		problems = append(problems, err)
	}
	if err := verifyVersionSubstituted("CFBundleVersion", plist.CFBundleVersion); err != nil {
		problems = append(problems, err)
	}
	if err := verifyExecutable(appPath, plist.CFBundleExecutable); err != nil {
		problems = append(problems, err)
	}
	if err := verifyIcon(appPath, plist.CFBundleIconFile); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

func lintPlist(ctx context.Context, plistPath string) error {
	if _, err := os.Stat(plistPath); err != nil {
		return fmt.Errorf("packagecheck: stat Info.plist: %w", err)
	}
	// #nosec G204 -- plistPath is built from the caller-supplied bundle path
	// (this build's own staged output directory), not untrusted input.
	cmd := exec.CommandContext(ctx, "plutil", "-lint", plistPath)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return fmt.Errorf("packagecheck: plutil -lint %s: %s", plistPath, detail)
		}
		return fmt.Errorf("packagecheck: plutil -lint %s: %w", plistPath, err)
	}
	return nil
}

func readPlist(ctx context.Context, plistPath string) (infoPlist, error) {
	// plutil -convert json -o - prints the JSON form to stdout and leaves
	// the original file untouched. The Go standard library has no plist
	// decoder, and plutil is already a hard requirement for lintPlist above,
	// so this adds no new dependency.
	// #nosec G204 -- see lintPlist.
	cmd := exec.CommandContext(ctx, "plutil", "-convert", "json", "-o", "-", plistPath)
	out, err := cmd.Output()
	if err != nil {
		return infoPlist{}, fmt.Errorf("packagecheck: plutil -convert json %s: %w", plistPath, err)
	}
	var parsed infoPlist
	if err := json.Unmarshal(out, &parsed); err != nil {
		return infoPlist{}, fmt.Errorf("packagecheck: parse %s as JSON: %w", plistPath, err)
	}
	return parsed, nil
}

func verifyVersionSubstituted(key, value string) error {
	if value == "" {
		return fmt.Errorf("packagecheck: %s is empty", key)
	}
	if strings.Contains(value, templatePlaceholder) {
		return fmt.Errorf("packagecheck: %s still holds the unsubstituted template placeholder: %q", key, value)
	}
	return nil
}

func verifyExecutable(appPath, name string) error {
	if name == "" {
		return errors.New("packagecheck: CFBundleExecutable is empty")
	}
	execPath := filepath.Join(appPath, "Contents", "MacOS", name)
	info, err := os.Stat(execPath)
	if err != nil {
		return fmt.Errorf("packagecheck: CFBundleExecutable %q: %w", name, err)
	}
	if info.IsDir() {
		return fmt.Errorf("packagecheck: CFBundleExecutable %q is a directory, not a file", name)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("packagecheck: %s is not executable (mode %s)", execPath, info.Mode())
	}
	return nil
}

func verifyIcon(appPath, name string) error {
	if name == "" {
		return errors.New("packagecheck: CFBundleIconFile is empty")
	}
	// CFBundleIconFile conventionally omits the .icns extension and macOS
	// appends it; accept either form so a plist that already spells it out
	// in full still verifies correctly.
	iconName := name
	if filepath.Ext(iconName) == "" {
		iconName += ".icns"
	}
	iconPath := filepath.Join(appPath, "Contents", "Resources", iconName)
	info, err := os.Stat(iconPath)
	if err != nil {
		return fmt.Errorf("packagecheck: CFBundleIconFile %q: %w", name, err)
	}
	if info.IsDir() {
		return fmt.Errorf("packagecheck: icon %q is a directory, not a file", iconPath)
	}
	if info.Size() == 0 {
		return fmt.Errorf("packagecheck: icon %q is empty", iconPath)
	}
	return nil
}
