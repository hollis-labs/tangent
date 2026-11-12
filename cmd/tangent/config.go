package main

import (
	"context"
	"fmt"
	"path/filepath"

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
func bootOptions(getenv func(string) string) boot.Options {
	return boot.Options{
		AfterConfig: func(_ context.Context, app *module.App) error {
			dirs, err := appdirs.Resolve(getenv)
			if err != nil {
				return err
			}
			if err = dirs.Prepare(); err != nil {
				return fmt.Errorf("prepare the data directory %s: %w", dirs.Data, err)
			}
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
				if p, _ := m[key].(string); p == "" || !filepath.IsAbs(p) {
					m[key] = def
				}
			}
			anchor(dirs.Database(), "database", "path", "sqlite")
			anchor(dirs.SigningKey(), "identity", "key_file")
			anchor(dirs.Secrets(), "secrets", "state_dir")
			return nil
		},
		Before: map[string]boot.Hook{
			"database": func(_ context.Context, app *module.App) error {
				db, _ := app.Config["database"].(map[string]any)
				sq, _ := db["sqlite"].(map[string]any)
				p, _ := sq["path"].(string)
				if p == "" {
					p = app.Name + ".db" // the database module's default, relative to the working directory
				}
				return persistence.RefuseLegacy(filepath.Clean(p))
			},
		},
	}
}
