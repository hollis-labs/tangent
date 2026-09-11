package pluginpkg

import (
	"fmt"
	"os"
	"path/filepath"
)

// Where installed plugins live.
//
// # `~/.tangent/plugins/`, and why not XDG
//
// Tangent has two path conventions today and they disagree: the database is at
// `~/.tangent/tangent.db` (`internal/db/db.go`), and the desktop shell's
// preferences are under `os.UserConfigDir()` (`internal/appshell/prefs.go`). A
// plugin directory cannot be consistent with both; it picks one or invents a
// third.
//
// It picks the database's, and the deciding reason is not the obvious one.
//
// The obvious reason is semantic: an installed binary is durable state, not
// configuration, so it belongs beside the database rather than under a config
// root. True, and not sufficient on its own.
//
// The reason that decides it is what happens NEXT. `CW-20260517-0058` is an
// open portfolio task to standardize every app on `libs/go-apppaths`, which
// resolves XDG locations and — importantly — already carries an `adoptLegacy`
// path that migrates a legacy root as a unit. Plugins inside `~/.tangent/`
// migrate with the database, for free, when that lands. Plugins placed at an
// XDG location TODAY would be the one thing in Tangent's tree that does not
// need migrating: a special case created by us, inside someone else's task, for
// a tidiness nobody gets to enjoy in the interim. Choosing the less-correct
// location now is what makes the correct one cheaper to reach.
//
// A third convention during an interim is also plainly worse than the two that
// exist, and an interim with no committed end date is just a state.
//
// This file takes NO position on whether Tangent should adopt `go-apppaths`.
// That is `CW-20260517-0058`'s call, and nothing here preempts it either way.

const (
	// defaultDataDirName matches internal/db's. It is spelled again rather than
	// imported because internal/db does not export it, and a plugin directory
	// that silently followed a database path change would be worse than one
	// that visibly did not.
	defaultDataDirName = ".tangent"
	defaultPluginsName = "plugins"

	// DirEnv overrides the install root.
	//
	// It exists for the same reasons TANGENT_DB_PATH does, and it is wired the
	// same way — including into the launch agent plist
	// (`internal/launchagent`), which is the half that is not about tests. A
	// packaged install that relocates the database and not the plugins would
	// split a plugin from the database it was installed alongside.
	DirEnv = "TANGENT_PLUGIN_DIR"
)

// DefaultRoot returns the install root: TANGENT_PLUGIN_DIR when set, otherwise
// ~/.tangent/plugins.
func DefaultRoot() (string, error) {
	if override := os.Getenv(DirEnv); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("pluginpkg: resolve home dir for the plugin directory: %w", err)
	}
	return filepath.Join(home, defaultDataDirName, defaultPluginsName), nil
}
