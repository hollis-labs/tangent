package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	migrate "github.com/golang-migrate/migrate/v4"
	migratedb "github.com/golang-migrate/migrate/v4/database"
	sqlitemigrate "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	// Register the pure-Go SQLite driver for database/sql.
	_ "modernc.org/sqlite"
)

const (
	driverName          = "sqlite"
	defaultDataDirName  = ".tangent"
	defaultDatabaseName = "tangent.db"
	defaultMaxOpenConns = 1
	defaultMaxIdleConns = 1
)

// Open opens the shared Tangent SQLite handle, creating the parent
// directory when needed. Empty path falls back to ~/.tangent/tangent.db.
func Open(path string) (*sql.DB, error) {
	resolved, pathErr := resolvePath(path)
	if pathErr != nil {
		return nil, pathErr
	}
	if dirErr := ensureParentDir(resolved); dirErr != nil {
		return nil, dirErr
	}

	db, err := sql.Open(driverName, resolved)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", resolved, err)
	}
	db.SetMaxOpenConns(defaultMaxOpenConns)
	db.SetMaxIdleConns(defaultMaxIdleConns)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", resolved, err)
	}
	if err := applyPragmas(db, resolved); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

// RunMigrations applies all embedded migrations.
func RunMigrations(db *sql.DB) error {
	return withMigrator(db, func(m *migrate.Migrate) error {
		err := m.Up()
		if err == nil || errors.Is(err, migrate.ErrNoChange) {
			return nil
		}
		return fmt.Errorf("apply migrations: %w", err)
	})
}

// RollbackOne rolls back the most recent migration.
func RollbackOne(db *sql.DB) error {
	return withMigrator(db, func(m *migrate.Migrate) error {
		err := m.Steps(-1)
		if err == nil || errors.Is(err, migrate.ErrNoChange) {
			return nil
		}
		return fmt.Errorf("rollback migration: %w", err)
	})
}

// Close gracefully closes the shared DB handle.
func Close(db *sql.DB) error {
	if db == nil {
		return nil
	}
	return db.Close()
}

func resolvePath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir for tangent db: %w", err)
	}
	return filepath.Join(home, defaultDataDirName, defaultDatabaseName), nil
}

func ensureParentDir(path string) error {
	if isMemoryPath(path) {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	//nolint:gosec // dir is the parent of the operator-configured database path
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create sqlite dir %q: %w", dir, err)
	}
	return nil
}

func applyPragmas(db *sql.DB, path string) error {
	if !isMemoryPath(path) {
		var journalMode string
		if err := db.QueryRow(`PRAGMA journal_mode = WAL;`).Scan(&journalMode); err != nil {
			return fmt.Errorf("set sqlite journal_mode=WAL: %w", err)
		}
		if strings.ToLower(journalMode) != "wal" {
			return fmt.Errorf("sqlite journal_mode=%q, want WAL", journalMode)
		}
	}

	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		return fmt.Errorf("set sqlite foreign_keys=ON: %w", err)
	}
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys;`).Scan(&foreignKeys); err != nil {
		return fmt.Errorf("verify sqlite foreign_keys pragma: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("sqlite foreign_keys=%d, want 1", foreignKeys)
	}

	return nil
}

func withMigrator(db *sql.DB, fn func(*migrate.Migrate) error) (err error) {
	driver, err := sqlitemigrate.WithInstance(db, &sqlitemigrate.Config{})
	if err != nil {
		return fmt.Errorf("build sqlite migrate driver: %w", err)
	}
	sourceDriver, err := iofs.New(migrationsFS, migrationsDir)
	if err != nil {
		return fmt.Errorf("build iofs migrate source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, driverName, noCloseDriver{Driver: driver})
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if err == nil {
			switch {
			case srcErr != nil:
				err = fmt.Errorf("close migrator source: %w", srcErr)
			case dbErr != nil:
				err = fmt.Errorf("close migrator db: %w", dbErr)
			}
		}
	}()

	err = fn(m)
	return err
}

func isMemoryPath(path string) bool {
	return path == ":memory:" || strings.HasPrefix(path, "file::memory:")
}

type noCloseDriver struct {
	migratedb.Driver
}

func (d noCloseDriver) Close() error {
	return nil
}
