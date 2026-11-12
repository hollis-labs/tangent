// Package persistence holds Tangent's schema (two goose migrations, SQLite only) and the one-way
// copy of a pre-Substrate database into it.
//
// The schema is, object for object, what the pre-Substrate binary's twenty-four migrations ended
// at. It is split in two so the copy can load rows before the triggers exist: 00001 holds the
// tables and indexes, 00002 the view and the triggers (several of which make rows immutable or
// write other rows, which a copy must not set off).
package persistence

import (
	"embed"
	"io/fs"
)

//go:embed migrations/sqlite/*.sql
var migrations embed.FS

// Migrations returns the file system and root to hand to database.UseMigrationFS.
func Migrations() (fs.FS, string) { return migrations, "migrations" }
