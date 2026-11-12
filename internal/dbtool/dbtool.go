// Package dbtool is the two operator database commands that survive the Substrate port:
// a read-only check and an online backup. Neither writes the source: it is opened read-only
// (mode=ro), a check creates nothing, and a backup refuses an existing destination.
package dbtool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // the driver
)

// Report is what Check found. Nothing in it is row content.
type Report struct {
	Path           string
	Integrity      string // "ok" or the first problem SQLite reports
	ForeignKeyRows int    // rows foreign_key_check reports
	Migration      int64  // the newest applied goose version, 0 when the file has no goose table
}

// OK says whether the file is sound.
func (r Report) OK() bool { return r.Integrity == "ok" && r.ForeignKeyRows == 0 }

func openReadOnly(path string) (*sql.DB, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// Check runs integrity_check and foreign_key_check on the file, opened read-only.
func Check(ctx context.Context, path string) (Report, error) {
	rep := Report{Path: path}
	db, err := openReadOnly(path)
	if err != nil {
		return rep, err
	}
	defer db.Close() //nolint:errcheck // read-only
	if err = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&rep.Integrity); err != nil {
		return rep, fmt.Errorf("integrity_check: %w", err)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return rep, fmt.Errorf("foreign_key_check: %w", err)
	}
	for rows.Next() {
		rep.ForeignKeyRows++
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return rep, err
	}
	_ = rows.Close()
	// A file without goose's table is not an error here (it may be a legacy file): version 0.
	if err = db.QueryRowContext(ctx, "SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied = 1").Scan(&rep.Migration); err != nil && !strings.Contains(err.Error(), "no such table") {
		return rep, err
	}
	return rep, nil
}

// Backup writes a complete, checkpointed copy of src to dst with VACUUM INTO, from a read
// transaction, so it is consistent while a server runs. It refuses an existing dst (or one with
// a -wal/-shm/-journal beside it), never writes src, verifies the copy with Check, and removes
// the copy if it is damaged. The copy is private (0600).
func Backup(ctx context.Context, src, dst string) (Report, error) {
	if !filepath.IsAbs(src) || !filepath.IsAbs(dst) {
		return Report{}, errors.New("both paths must be absolute")
	}
	if filepath.Clean(src) == filepath.Clean(dst) {
		return Report{}, errors.New("the destination is the source")
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(dst + suffix); err == nil {
			return Report{}, fmt.Errorf("%s already exists; a backup never overwrites", dst+suffix)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Report{}, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return Report{}, err
	}
	db, err := openReadOnly(src)
	if err != nil {
		return Report{}, err
	}
	defer db.Close() //nolint:errcheck // read-only
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		_ = os.Remove(dst)
		return Report{}, fmt.Errorf("backup: %w", err)
	}
	if err = os.Chmod(dst, 0o600); err != nil {
		return Report{}, err
	}
	rep, err := Check(ctx, dst)
	if err != nil || !rep.OK() {
		_ = os.Remove(dst)
		if err == nil {
			err = fmt.Errorf("the copy is damaged (integrity %q, %d foreign key rows); it was removed", rep.Integrity, rep.ForeignKeyRows)
		}
		return rep, err
	}
	return rep, nil
}
