package pluginpkg

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Finding discovery of installed plugins.
//
// # Two properties, both chosen against a defect in the reference
//
// Nanite runs subprocess plugins in production, so its `DiscoverPlugins` is the
// shape to read. It has two behaviors worth inverting rather than copying, and
// both were found by reading it for defects instead of for patterns — the same
// posture that kept its CW-20260902-0062 out of `internal/pluginhost/wire.go`.
//
//  1. **ONE BAD MANIFEST MUST NOT ABORT THE SCAN.** Nanite's returns
//     `nil, err` on the first manifest that fails to parse, so a single
//     malformed file means ZERO plugins discovered rather than one plugin
//     missing. That is the same defect class as the read-timeout that killed a
//     whole connection: one bad element taking out the entire surface.
//
//     Here every directory is evaluated on its own. A broken plugin is one
//     [Rejected] entry the host can report and every other plugin loads.
//     That also matches what `internal/pluginhost` already does for load
//     failures, so it is consistency rather than invention.
//
//  2. **DISCOVERY DOES NOT WRITE.** Nanite's renames `plugin.yaml.disabled` to
//     `plugin.yaml` mid-scan as a legacy migration. A scan that mutates is
//     surprising, hard to test, and racy: two Tangents booting at once have one
//     renaming files the other is reading. Scan is a pure read. If a migration
//     is ever needed it is an explicit command, not a side effect of looking.

// Installed is one plugin found under the install root and ready to spawn.
type Installed struct {
	// Manifest is the host's record of what this plugin is and serves.
	Manifest Manifest
	// Dir is the plugin's own directory.
	Dir string
	// Entrypoint is the absolute path to its executable.
	Entrypoint string
}

// Rejected is one directory that looked like a plugin and is not usable.
//
// It is returned rather than logged and dropped, because "installed but not
// running, and here is why" is the answer an operator needs and an empty
// plugin list does not give them. The health inventory reports these.
type Rejected struct {
	Dir    string
	Reason error
}

// Scan reads the install root and returns what is usable and what is not.
//
// A missing root is not an error: a Tangent with no plugins installed is an
// ordinary Tangent, not a broken one.
//
// The returned slices are ordered by plugin id and directory name so that boot
// order is stable across runs. Nothing here depends on that order — installed
// plugins declare no inter-plugin dependencies, because a plugin's dependencies
// live in its own process, which is the whole point — but a stable order makes
// logs and health reports diffable.
func Scan(root string) ([]Installed, []Rejected, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("pluginpkg: read plugin directory %s: %w", root, err)
	}

	var installed []Installed
	var rejected []Rejected
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		manifestPath := filepath.Join(dir, ManifestName)
		if _, statErr := os.Stat(manifestPath); statErr != nil {
			// No manifest is not a rejection. A directory under the install
			// root that is not a plugin — a leftover, an editor's backup, a
			// half-finished copy — is not something to report as broken.
			continue
		}

		found, reason := evaluate(dir, manifestPath)
		if reason != nil {
			rejected = append(rejected, Rejected{Dir: dir, Reason: reason})
			continue
		}
		installed = append(installed, found)
	}

	sort.Slice(installed, func(i, j int) bool {
		if installed[i].Manifest.ID != installed[j].Manifest.ID {
			return installed[i].Manifest.ID < installed[j].Manifest.ID
		}
		return installed[i].Dir < installed[j].Dir
	})
	sort.Slice(rejected, func(i, j int) bool { return rejected[i].Dir < rejected[j].Dir })
	return installed, rejected, nil
}

// evaluate decides one directory, and every failure it can produce is that
// directory's own. It never returns an error that would end a scan.
func evaluate(dir, manifestPath string) (Installed, error) {
	manifest, err := ParseManifest(manifestPath)
	if err != nil {
		return Installed{}, err
	}
	if err := CheckCompatible(manifest); err != nil {
		return Installed{}, err
	}

	entrypoint := filepath.Join(dir, filepath.Clean(manifest.Entrypoint))
	// Belt and braces on top of Manifest.Validate: a cleaned join that still
	// escapes the plugin's directory would make an install a way to run
	// something else. Validate refuses the manifest forms that reach here, and
	// this refuses the result.
	if !strings.HasPrefix(entrypoint, dir+string(filepath.Separator)) {
		return Installed{}, fmt.Errorf(
			"pluginpkg: %s entrypoint resolves to %s, outside its own directory",
			manifest.ID, entrypoint)
	}
	info, statErr := os.Stat(entrypoint)
	if statErr != nil {
		return Installed{}, fmt.Errorf(
			"pluginpkg: %s declares entrypoint %q which is not there: %w",
			manifest.ID, manifest.Entrypoint, statErr)
	}
	if info.IsDir() {
		return Installed{}, fmt.Errorf(
			"pluginpkg: %s entrypoint %q is a directory", manifest.ID, manifest.Entrypoint)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return Installed{}, fmt.Errorf(
			"pluginpkg: %s entrypoint %q is not executable (mode %s)",
			manifest.ID, manifest.Entrypoint, info.Mode().Perm())
	}
	return Installed{Manifest: manifest, Dir: dir, Entrypoint: entrypoint}, nil
}
