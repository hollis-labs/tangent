package pluginpkg

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Snapshot pins a private payload copy. Writable state lives outside both the
// installation and snapshot. It never moves or deletes existing plugin data.
// Read-only modes prevent accidental writes, not a hostile same-user process;
// VerifyBundle is repeated at the spawn boundary, including crash recovery.
func Snapshot(source Installed, root string) (Installed, func() error, error) {
	runtimeRoot := filepath.Join(root, ".runtime")
	if err := privateDirectory(runtimeRoot); err != nil {
		return Installed{}, nil, err
	}
	state := filepath.Join(root, ".state")
	if err := privateDirectory(state); err != nil {
		return Installed{}, nil, err
	}
	if err := privateDirectory(filepath.Join(state, source.Manifest.ID)); err != nil {
		return Installed{}, nil, err
	}
	for _, name := range []string{"data", "cache"} {
		if err := privateDirectory(filepath.Join(root, ".state", source.Manifest.ID, name)); err != nil {
			return Installed{}, nil, err
		}
	}
	dir, err := os.MkdirTemp(runtimeRoot, "bundle-")
	if err != nil {
		return Installed{}, nil, err
	}
	var once sync.Once
	var cleanupErr error
	cleanup := func() error {
		once.Do(func() {
			// Restore only private snapshot directory modes so it can be removed.
			snapshotRoot, openErr := os.OpenRoot(dir)
			if os.IsNotExist(openErr) {
				return
			}
			if openErr != nil {
				cleanupErr = openErr
				return
			}
			cleanupErr = fs.WalkDir(snapshotRoot.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					return snapshotRoot.Chmod(name, 0700)
				}
				return nil
			})
			if closeErr := snapshotRoot.Close(); cleanupErr == nil {
				cleanupErr = closeErr
			}
			if cleanupErr == nil {
				cleanupErr = os.RemoveAll(dir)
			}
		})
		return cleanupErr
	}
	fail := func(err error) (Installed, func() error, error) { _ = cleanup(); return Installed{}, nil, err }
	if copyErr := copyTree(source.Dir, dir); copyErr != nil {
		return fail(copyErr)
	}
	if verifyErr := source.Manifest.VerifyBundle(dir); verifyErr != nil {
		return fail(verifyErr)
	}
	entry := filepath.Join(dir, filepath.FromSlash(source.Manifest.Server.Entry))
	if nativeErr := nativeExecutable(entry); nativeErr != nil {
		return fail(nativeErr)
	}
	snapshotRoot, err := os.OpenRoot(dir)
	if err != nil {
		return fail(err)
	}
	modeErr := fs.WalkDir(snapshotRoot.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0400)
		if entry.IsDir() || info.Mode().Perm()&0111 != 0 {
			mode = 0500
		}
		return snapshotRoot.Chmod(name, mode)
	})
	closeErr := snapshotRoot.Close()
	if modeErr != nil {
		return fail(modeErr)
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	return Installed{Manifest: source.Manifest, Dir: dir, Entrypoint: entry}, cleanup, nil
}

func privateDirectory(name string) error {
	if err := os.MkdirAll(name, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("pluginpkg: %s must be a private real directory", name)
	}
	return nil
}
