package pluginpkg

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Installing a plugin.
//
// # What install is, and what it deliberately is not
//
// It copies a plugin's own directory — its `plugin.yaml` and the files that
// manifest names — into the install root, under the plugin's id.
//
// It does NOT build anything, and that is the defect it is avoiding. Nanite's
// `plugin install <dir>` strips `ui/dist` without rebuilding it and leaves an
// install that looks complete and does not work (their CW-20260423-0005). An
// install step that transforms its input has to be right about what the input
// needs, forever, for every plugin — and it is wrong the first time a plugin's
// layout differs from the one the installer was written against.
//
// So this one is a copy with a verification: what arrives is what was built,
// and the check is that the result actually runs rather than that the copy
// succeeded. A plugin whose build is incomplete fails here, at install, with
// the reason — not later, at boot, as a plugin that will not start.

// ErrNotAPlugin reports a source directory with no manifest.
var ErrNotAPlugin = errors.New("pluginpkg: source is not a plugin directory")

// InstallResult describes what an install did.
type InstallResult struct {
	ID       string
	Dir      string
	Version  string
	Replaced bool
}

// Install copies the plugin at src into root and returns what it installed.
//
// Installing over an existing plugin of the same id replaces it, which is how
// an upgrade works. The replacement is not atomic across the whole directory —
// see the comment at the swap — and the failure mode is stated rather than
// papered over.
func Install(src, root string) (InstallResult, error) {
	manifestPath := filepath.Join(src, ManifestName)
	if _, err := os.Stat(manifestPath); err != nil {
		return InstallResult{}, fmt.Errorf("%w: %s has no %s", ErrNotAPlugin, src, ManifestName)
	}
	// Pin the parsed declaration before staging; copying must preserve that
	// same reviewed identity and inventory, even if the source changes.
	reviewed, err := evaluate(src, manifestPath)
	if err != nil {
		return InstallResult{}, err
	}
	manifest := reviewed.Manifest

	// The id is a directory name, so it has to be one that cannot escape the
	// root. A plugin id is not operator input, but an install root is not a
	// place anyone reviews, and "../" in an id would write outside it.
	if filepath.Base(manifest.ID) != manifest.ID || manifest.ID == "." || manifest.ID == ".." {
		return InstallResult{}, fmt.Errorf(
			"pluginpkg: plugin id %q cannot be a directory name", manifest.ID)
	}

	destination := filepath.Join(root, manifest.ID)
	_, replaced := os.Stat(destination)

	// Staged beside the destination, then swapped. A copy directly into place
	// would leave a half-written plugin discoverable if it failed partway, and
	// discovery is a pure read that cannot tell a partial install from a
	// finished one.
	staging := destination + ".installing"
	if err := os.RemoveAll(staging); err != nil {
		return InstallResult{}, fmt.Errorf("pluginpkg: clear staging directory: %w", err)
	}
	if err := copyTree(src, staging); err != nil {
		_ = os.RemoveAll(staging)
		return InstallResult{}, err
	}

	// Verify the staged copy the way discovery will read it, before anything
	// existing is disturbed. This is what turns Nanite's class of defect — an
	// install that completes and does not work — into a refusal.
	if verifyErr := manifest.VerifyBundle(staging); verifyErr != nil {
		_ = os.RemoveAll(staging)
		return InstallResult{}, fmt.Errorf("pluginpkg: staged bundle differs from reviewed source: %w", verifyErr)
	}
	if _, evalErr := evaluate(staging, filepath.Join(staging, ManifestName)); evalErr != nil {
		_ = os.RemoveAll(staging)
		return InstallResult{}, fmt.Errorf(
			"pluginpkg: %s would not load after installing, so it was not installed: %w",
			manifest.ID, evalErr)
	}

	// The swap. Removing before renaming is not atomic, and on a failure
	// between the two the plugin is absent rather than corrupt — which is the
	// direction to fail in, because discovery reports an absent plugin as
	// nothing and a corrupt one as broken. A rename-aside-then-restore would
	// narrow the window and is not worth the second failure mode until
	// somebody is upgrading plugins under load.
	if err := os.RemoveAll(destination); err != nil {
		_ = os.RemoveAll(staging)
		return InstallResult{}, fmt.Errorf("pluginpkg: remove previous install: %w", err)
	}
	if err := os.Rename(staging, destination); err != nil {
		return InstallResult{}, fmt.Errorf("pluginpkg: install %s: %w", manifest.ID, err)
	}

	return InstallResult{
		ID: manifest.ID, Dir: destination, Version: manifest.Version,
		Replaced: replaced == nil,
	}, nil
}

// copyTree copies a directory recursively, preserving the executable bit.
//
// The executable bit is the part that matters: a plugin whose entrypoint
// arrives non-executable is one discovery refuses, and losing the bit during a
// copy would make every install fail for a reason that has nothing to do with
// the plugin.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, relative)

		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		// Symlinks are not followed and not copied. A plugin directory that
		// links outside itself would make the install root a view onto
		// somewhere else, and what is installed should be what is there.
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("pluginpkg: %s is a symlink; a plugin directory must be self-contained", path)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("pluginpkg: bundle file %s is not regular", path)
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) // #nosec G304 -- src is inside the operator-named source directory.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	if mkErr := os.MkdirAll(filepath.Dir(dst), 0o750); mkErr != nil {
		return mkErr
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode) // #nosec G304 -- dst is inside the install root.
	if err != nil {
		return err
	}
	if _, copyErr := io.Copy(out, in); copyErr != nil {
		_ = out.Close()
		return copyErr
	}
	return out.Close()
}

// Remove uninstalls a plugin by id.
func Remove(id, root string) error {
	if filepath.Base(id) != id || id == "." || id == ".." {
		return fmt.Errorf("pluginpkg: plugin id %q cannot be a directory name", id)
	}
	destination := filepath.Join(root, id)
	if _, err := os.Stat(destination); err != nil {
		return fmt.Errorf("pluginpkg: %s is not installed", id)
	}
	return os.RemoveAll(destination)
}
