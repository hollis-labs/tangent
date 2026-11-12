package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"
)

// MigrationStatus is where a database stands against the schema this binary carries.
type MigrationStatus struct {
	// Expected is the newest migration this binary embeds.
	Expected int64
	// Applied is the newest migration the database records (0 when none).
	Applied int64
}

// Initialized reports whether any migration has been applied.
func (s MigrationStatus) Initialized() bool { return s.Applied > 0 }

// Inspect reads the applied schema version without taking a lock and without writing. A database
// that has never been migrated is a state to describe, not an error.
func Inspect(ctx context.Context, db *sql.DB) (MigrationStatus, error) {
	var status MigrationStatus
	if db == nil {
		return status, fmt.Errorf("database handle is nil")
	}
	fsys, root := Migrations()
	src := migrate.Source{FS: fsys, Root: root}
	expected, err := src.Latest(store.DriverSQLite)
	if err != nil {
		return status, fmt.Errorf("read embedded migrations: %w", err)
	}
	status.Expected = expected
	var exists int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, migrate.DefaultTable).Scan(&exists); err != nil {
		return status, fmt.Errorf("read the schema catalog: %w", err)
	}
	if exists == 0 {
		return status, nil
	}
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_id), 0) FROM `+migrate.DefaultTable+` WHERE is_applied`).Scan(&status.Applied); err != nil {
		return status, fmt.Errorf("read the applied schema version: %w", err)
	}
	return status, nil
}
