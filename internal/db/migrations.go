package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

const migrationsDir = "migrations"

// migrationsTable mirrors golang-migrate's sqlite driver default
// (`DefaultMigrationsTable`). It is read directly rather than through the
// migrator because reading the version through `migrate.Migrate` opens a
// migration lock, and a readiness probe must never be able to block the thing
// it is reporting on.
const migrationsTable = "schema_migrations"

//go:embed all:migrations
var migrationsFS embed.FS

// MigrationStatus is what a readiness probe needs to know about the schema:
// what this binary expects, what the database has applied, and whether a
// previous migration left the schema half-applied.
//
// It carries no file path and no SQL — a probe result is read during an
// incident and is the wrong place to disclose where the database lives.
type MigrationStatus struct {
	// Expected is the highest up-migration this binary embeds.
	Expected int64 `json:"expected_version"`
	// Applied is the version recorded in the database. Zero with
	// Initialized false means the schema has never been migrated.
	Applied int64 `json:"applied_version"`
	// Dirty is golang-migrate's marker for a migration that failed part-way.
	// A dirty schema is never ready: the tables are in a state no migration
	// describes.
	Dirty bool `json:"dirty"`
	// Initialized reports whether the bookkeeping table exists at all.
	Initialized bool `json:"initialized"`
}

// UpToDate reports whether the applied schema is exactly what this binary
// expects. Ahead is as much a failure as behind: a binary running against a
// newer schema is a binary whose queries were written for a different shape.
func (s MigrationStatus) UpToDate() bool {
	return s.Initialized && !s.Dirty && s.Applied == s.Expected
}

// ExpectedMigrationVersion returns the highest up-migration embedded in this
// binary. It is derived from the embedded filesystem rather than a constant so
// adding a migration cannot forget to update it.
func ExpectedMigrationVersion() (int64, error) {
	entries, err := fs.ReadDir(migrationsFS, migrationsDir)
	if err != nil {
		return 0, fmt.Errorf("read embedded migrations: %w", err)
	}
	var highest int64
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		prefix, _, found := strings.Cut(name, "_")
		if !found {
			continue
		}
		version, parseErr := strconv.ParseInt(prefix, 10, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("embedded migration %q has no numeric version prefix", name)
		}
		if version > highest {
			highest = version
		}
	}
	if highest == 0 {
		return 0, errors.New("no embedded up-migrations found")
	}
	return highest, nil
}

// InspectMigrations reads the applied schema version without taking the
// migration lock and without writing anything.
//
// A missing bookkeeping table is not an error: a database that has never been
// migrated is a legitimate state a probe has to be able to describe. A
// database that cannot be queried at all *is* an error, and it is returned so
// the caller can distinguish "never migrated" from "cannot be read".
func InspectMigrations(ctx context.Context, db *sql.DB) (MigrationStatus, error) {
	expected, err := ExpectedMigrationVersion()
	if err != nil {
		return MigrationStatus{}, err
	}
	status := MigrationStatus{Expected: expected}
	if db == nil {
		return status, errors.New("database handle is nil")
	}

	var exists int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?;`,
		migrationsTable).Scan(&exists)
	if err != nil {
		return status, fmt.Errorf("read sqlite schema catalog: %w", err)
	}
	if exists == 0 {
		return status, nil
	}
	status.Initialized = true

	// LIMIT 1 matches the driver's own read: the table holds a single row.
	err = db.QueryRowContext(ctx,
		`SELECT version, dirty FROM `+migrationsTable+` LIMIT 1;`).Scan(&status.Applied, &status.Dirty)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// The table exists but records nothing — the same state as never
		// having migrated, reached by a rollback past the first migration.
		status.Initialized = false
		return status, nil
	case err != nil:
		return status, fmt.Errorf("read %s: %w", migrationsTable, err)
	}
	return status, nil
}
