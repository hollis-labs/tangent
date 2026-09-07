package smoke_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/envelope"
)

// This file is the version gate (CW-20260907-0019).
//
// The release version has exactly one source: the "version" field of
// ui/package.json. scripts/build-macos-app.sh already stamps Tangent.app's
// Info.plist from it; this test makes every other place a version appears in
// prose agree with it, so a release cannot be cut with a README, CHANGELOG,
// and bundle that disagree — which is how the tree came to say 0.1.0, v0.11.0,
// and v0.12.0 about itself at the same time.
//
// The same rule the tool-count gate applies: a number in prose is only allowed
// where a test fails when it drifts.

// versionSource is the one file the release version is read from.
const versionSource = "ui/package.json"

// releaseVersion reads the source of truth.
func releaseVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(repoPath(versionSource)) // #nosec G304 -- constant path inside the repository.
	if err != nil {
		t.Fatalf("read %s: %v", versionSource, err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatalf("parse %s: %v", versionSource, err)
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(pkg.Version) {
		t.Fatalf("%s version %q is not MAJOR.MINOR.PATCH", versionSource, pkg.Version)
	}
	return pkg.Version
}

// TestGoHostVersionIsTheReleaseVersion: the version the MCP server advertises
// and definitions declare compatibility against (internal/envelope.HostVersion,
// aliased by internal/mcp and asserted equal there) is the same release the
// source names, with the "v" prefix that package uses.
func TestGoHostVersionIsTheReleaseVersion(t *testing.T) {
	version := releaseVersion(t)
	if envelope.HostVersion != "v"+version {
		t.Fatalf("internal/envelope.HostVersion = %q; %s says %s — bump both in the same commit", envelope.HostVersion, versionSource, version)
	}
}

// TestChangelogLeadsWithTheReleaseVersion: the first released section under
// [Unreleased] is the version the source names. A CHANGELOG that describes a
// release the source does not carry, or a source bumped without a CHANGELOG
// section, both fail here.
func TestChangelogLeadsWithTheReleaseVersion(t *testing.T) {
	version := releaseVersion(t)
	body := readDoc(t, "CHANGELOG.md")
	headings := regexp.MustCompile(`(?m)^## \[(v?[^\]]+)\]`).FindAllStringSubmatch(body, -1)
	if len(headings) < 2 || headings[0][1] != "Unreleased" {
		t.Fatalf("CHANGELOG.md must open with an [Unreleased] section followed by a release, got %v", headings)
	}
	if got := headings[1][1]; got != "v"+version {
		t.Fatalf("CHANGELOG.md's first release section is [%s]; %s says %s", got, versionSource, version)
	}
	if !strings.Contains(body, "[v"+version+"]: https://github.com/hollis-labs/tangent/releases/tag/v"+version) {
		t.Fatalf("CHANGELOG.md has no link reference for [v%s]", version)
	}
}

// TestReadmeNamesTheReleaseVersionOnly: the README's install line and status
// line name the source version, and no other v0.x is presented as the current
// release. Older versions may appear in the roadmap history, so the check is
// on the two lines a reader acts on.
func TestReadmeNamesTheReleaseVersionOnly(t *testing.T) {
	version := releaseVersion(t)
	body := readDoc(t, "README.md")
	install := regexp.MustCompile(`go install github\.com/hollis-labs/tangent/cmd/tangent@(v[\d.]+)`)
	matches := install.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatal("README.md has no `go install ...@vX.Y.Z` line")
	}
	for _, match := range matches {
		if match[1] != "v"+version {
			t.Fatalf("README.md installs %s; %s says %s", match[1], versionSource, version)
		}
	}
	if !strings.Contains(body, "**`v"+version+"`**") {
		t.Fatalf("README.md's Status section must name the release as **`v%s`**", version)
	}
}

// TestBundleTemplateDerivesTheVersion: the desktop shell's Info.plist template
// carries the placeholder the build script substitutes from the source, never
// a literal version. Skips while the desktop shell is not in the tree.
func TestBundleTemplateDerivesTheVersion(t *testing.T) {
	plist := repoPath("packaging", "macos", "Info.plist")
	raw, err := os.ReadFile(plist) // #nosec G304 -- constant path inside the repository.
	if os.IsNotExist(err) {
		t.Skip("packaging/macos/Info.plist is not in this tree")
	}
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, key := range []string{"CFBundleShortVersionString", "CFBundleVersion"} {
		pattern := regexp.MustCompile(`<key>` + key + `</key>\s*<string>([^<]*)</string>`)
		match := pattern.FindStringSubmatch(body)
		if match == nil {
			t.Fatalf("Info.plist has no %s", key)
		}
		if match[1] != "__TANGENT_VERSION__" {
			t.Fatalf("Info.plist %s is %q; it must be the __TANGENT_VERSION__ placeholder that scripts/build-macos-app.sh fills from %s", key, match[1], versionSource)
		}
	}
	script, err := os.ReadFile(repoPath("scripts", "build-macos-app.sh")) // #nosec G304 -- constant path inside the repository.
	if err != nil {
		t.Fatalf("read build script: %v", err)
	}
	if !strings.Contains(string(script), "require('./"+versionSource+"').version") {
		t.Fatalf("scripts/build-macos-app.sh must read the version from %s", versionSource)
	}
}

// TestNoOtherFileClaimsToBeTheVersionSource: the two files that historically
// carried their own version (README status, CHANGELOG) are covered above; this
// guards the one Go string that reads like a release version. The HTML
// placeholder served when ui_dist is missing is a build-time message, not a
// release claim, and says so.
func TestNoOtherFileClaimsToBeTheVersionSource(t *testing.T) {
	raw, err := os.ReadFile(repoPath("internal", "server", "static.go")) // #nosec G304 -- constant path inside the repository.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "v0.1.0-dev") {
		t.Fatal("internal/server/static.go still carries a literal v0.1.0-dev; the placeholder page must not look like a release version")
	}
}
