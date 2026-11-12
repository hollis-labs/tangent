package pluginpkg

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/tangent/internal/appdirs"
)

// Where installed plugins live: the plugins directory inside Tangent's data directory
// (internal/appdirs), so one directory holds everything to back up or move. The pre-Substrate
// location, ~/.tangent/plugins, survives only as LegacyRoot for the one-way file migration.

const (
	// defaultDataDirName and defaultPluginsName spell the pre-Substrate layout, fixed for good.
	defaultDataDirName = ".tangent"
	defaultPluginsName = "plugins"

	// DirEnv overrides the install root.
	//
	// It exists for the same reasons TANGENT_DB_PATH does, and it is wired the
	// same way. A
	// packaged install that relocates the database and not the plugins would
	// split a plugin from the database it was installed alongside.
	DirEnv = "TANGENT_PLUGIN_DIR"
)

// DefaultRoot returns the install root: TANGENT_PLUGIN_DIR when set, otherwise the plugins
// directory inside Tangent's data directory (internal/appdirs), so everything Tangent keeps is
// under one directory to back up and move. The pre-Substrate default, ~/.tangent/plugins, is
// LegacyRoot: only the one-way migration reads it.
func DefaultRoot() (string, error) {
	if override := os.Getenv(DirEnv); override != "" {
		return override, nil
	}
	dirs, err := appdirs.Resolve(os.Getenv)
	if err != nil {
		return "", fmt.Errorf("pluginpkg: resolve the plugin directory: %w", err)
	}
	return dirs.Plugins(), nil
}

// LegacyRoot is where the pre-Substrate Tangent kept installed plugins, their state and their
// configuration: ~/.tangent/plugins.
func LegacyRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("pluginpkg: resolve home dir for the legacy plugin directory: %w", err)
	}
	return filepath.Join(home, defaultDataDirName, defaultPluginsName), nil
}
