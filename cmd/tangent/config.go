package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"

	"github.com/hollis-labs/tangent/internal/appdirs"
	"github.com/hollis-labs/tangent/internal/persistence"
)

// bootOptions are the app's two boot hooks.
//
// After the config module has applied the SUBSTRATE_CONFIG override, the data directory (XDG
// data home, or TANGENT_DATA_DIR) is resolved and secured, and every file path that is still
// relative is anchored in it. The manifest holds working-directory defaults; an absolute path
// from the override is the operator's choice and stays.
//
// Immediately before the database module starts (it migrates as soon as it opens the file), a
// pre-Substrate database is refused. The path is read from the config the module will read, so
// the guard needs no copy of the override merge rule.
//
// prepare says whether the data directory may be created: only serving does (see plan). Read-only
// commands start no module that opens a file and create nothing.
func bootOptions(getenv func(string) string, prepare bool, started []string) boot.Options {
	// A hook that names a module that is not started is an error, so the guard is installed only
	// with the database module.
	var before map[string]boot.Hook
	if slices.Contains(started, "database") {
		before = map[string]boot.Hook{"database": databaseGuard}
	}
	return boot.Options{
		AfterConfig: func(_ context.Context, app *module.App) error {
			dirs, err := appdirs.Resolve(getenv)
			if err != nil {
				return err
			}
			if prepare {
				if err = dirs.Prepare(); err != nil {
					return fmt.Errorf("prepare the data directory %s: %w", dirs.Data, err)
				}
			}
			// A path the operator's override made absolute is theirs; a relative one (including
			// the manifest default) is placed in the data directory, never silently replaced.
			anchor := func(def, modName, key string, nested ...string) {
				m, _ := app.Config[modName].(map[string]any)
				if m == nil {
					m = map[string]any{}
					app.Config[modName] = m
				}
				for _, n := range nested {
					child, _ := m[n].(map[string]any)
					if child == nil {
						child = map[string]any{}
						m[n] = child
					}
					m = child
				}
				p, _ := m[key].(string)
				switch {
				case p == "":
					m[key] = def
				case !filepath.IsAbs(p):
					m[key] = filepath.Join(dirs.Data, p)
				}
			}
			anchor(dirs.Database(), "database", "path", "sqlite")
			anchor(dirs.SigningKey(), "identity", "key_file")
			anchor(dirs.Secrets(), "secrets", "state_dir")
			return nil
		},
		Before: before,
	}
}

// databaseGuard refuses a pre-Substrate database, judged from the config the database module
// will read.
func databaseGuard(_ context.Context, app *module.App) error {
	db, _ := app.Config["database"].(map[string]any)
	sq, _ := db["sqlite"].(map[string]any)
	p, _ := sq["path"].(string)
	if p == "" {
		p = app.Name + ".db" // the database module's default, relative to the working directory
	}
	return persistence.RefuseLegacy(filepath.Clean(p))
}
