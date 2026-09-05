package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Single-writer is a contract, not a hope.
//
// internal/db opens SQLite with `SetMaxOpenConns(1)`, which makes one *process*
// a single writer. It says nothing about two processes: SQLite's own locking
// would happily interleave a serving Tangent and a maintenance command, and
// the maintenance path in retention.go drops immutability triggers for the
// length of one transaction. A second process writing during that window would
// write into a database without its guards. That is the failure mode ADR 0002
// records as the cost of having a deletion path at all, and it is why the
// contract is enforced rather than documented.
//
// The enforcement is an advisory whole-file lock on a sidecar next to the
// database:
//
//   - The lock is `flock(LOCK_EX|LOCK_NB)` on `<database>.owner`. It is held
//     for the life of the owning process and released by the kernel when that
//     process exits — including when it is killed, which is what makes a crash
//     recoverable without an operator clearing anything by hand.
//   - The file's contents are advisory metadata only: pid, role, host, start
//     time. They are what a refusal message quotes so an operator learns *what*
//     holds the database rather than only that something does. The lock is the
//     authority; the file is the explanation.
//   - A second acquirer is refused immediately (`LOCK_NB`) rather than queued.
//     A maintenance command that blocks for an unbounded time behind a serving
//     process is worse than one that says "the server is running".
//
// What it does not claim: it is advisory, so a process that never calls
// AcquireOwnership can still open the database. Tangent's own binary always
// calls it, and `sqlite3` at a shell does not — the honest statement is that
// this stops two Tangents, not that it stops everything. It also does not
// travel over a network filesystem with any reliability, which is not a
// configuration a single-user local host has.
//
// In-memory databases take no lock: there is no file to lock and no second
// process that could reach one.

// ownerSuffix is appended to the database path to name the lock sidecar. It is
// deliberately not inside the database: a lock that lives in the thing being
// locked cannot be consulted while that thing is being replaced by a restore.
const ownerSuffix = ".owner"

// OwnerRole distinguishes what holds the database, because the two kinds fail
// differently. A serving process holds it for hours and an operator should be
// told to stop the service; a maintenance command holds it for seconds and an
// operator should be told to wait.
type OwnerRole string

const (
	// RoleServer is the HTTP server plus every component it composes.
	RoleServer OwnerRole = "server"
	// RoleMaintenance is a one-shot operator command: migrate, backup,
	// restore, repair, compact, or a retention operation.
	RoleMaintenance OwnerRole = "maintenance"
)

// OwnerRecord is the advisory metadata written into the lock sidecar.
type OwnerRecord struct {
	PID       int       `json:"pid"`
	Role      OwnerRole `json:"role"`
	Host      string    `json:"host"`
	Command   string    `json:"command"`
	StartedAt time.Time `json:"started_at"`
}

// Ownership is a held single-writer claim. Release it, or exit; both work.
type Ownership struct {
	path   string
	file   *os.File
	record OwnerRecord
	// noop is true for an in-memory database, where there is nothing to lock.
	noop bool
}

// OwnershipConflict is returned when another process holds the database. It
// carries the holder's advisory record when one could be read, so a refusal
// names the process rather than describing a condition.
type OwnershipConflict struct {
	DatabasePath string
	LockPath     string
	// Holder is the record the current owner wrote. It is zero when the file
	// could not be read or was written by an older build.
	Holder OwnerRecord
	// HolderKnown distinguishes "held by pid 4711, serving" from "held by
	// something that left no record". Both are refusals; only one is useful.
	HolderKnown bool
}

func (e *OwnershipConflict) Error() string {
	if !e.HolderKnown {
		return fmt.Sprintf(
			"another process holds the Tangent database (%s); it left no owner record", e.LockPath)
	}
	return fmt.Sprintf(
		"another process holds the Tangent database: pid %d, role %s, on %s since %s",
		e.Holder.PID, e.Holder.Role, e.Holder.Host,
		e.Holder.StartedAt.UTC().Format(time.RFC3339))
}

// ErrOwnershipUnsupported is returned on platforms without advisory file
// locking. It is a refusal rather than a silent pass: a maintenance path that
// suspends immutability triggers must never run on a platform where it cannot
// tell whether something else is writing.
var ErrOwnershipUnsupported = errors.New("advisory file locking is unavailable on this platform")

// AcquireOwnership claims the single-writer role for this process.
//
// It returns *OwnershipConflict when another process already holds it. The
// returned Ownership must be released, though exiting is equally effective:
// the kernel drops the lock with the file descriptor.
func AcquireOwnership(databasePath string, role OwnerRole, command string) (*Ownership, error) {
	resolved, err := resolvePath(databasePath)
	if err != nil {
		return nil, err
	}
	if isMemoryPath(resolved) {
		return &Ownership{noop: true, record: newOwnerRecord(role, command)}, nil
	}
	if dirErr := ensureParentDir(resolved); dirErr != nil {
		return nil, dirErr
	}

	lockPath := resolved + ownerSuffix
	//nolint:gosec // lockPath is the configured database path plus a fixed suffix
	file, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open owner lock %q: %w", lockPath, err)
	}

	locked, lockErr := tryLockExclusive(file)
	if lockErr != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock %q: %w", lockPath, lockErr)
	}
	if !locked {
		holder, known := readOwnerRecord(lockPath)
		_ = file.Close()
		return nil, &OwnershipConflict{
			DatabasePath: resolved, LockPath: lockPath,
			Holder: holder, HolderKnown: known,
		}
	}

	record := newOwnerRecord(role, command)
	if writeErr := writeOwnerRecord(file, record); writeErr != nil {
		_ = unlock(file)
		_ = file.Close()
		return nil, writeErr
	}
	return &Ownership{path: lockPath, file: file, record: record}, nil
}

// Record returns the advisory metadata this process wrote.
func (o *Ownership) Record() OwnerRecord {
	if o == nil {
		return OwnerRecord{}
	}
	return o.record
}

// Release drops the claim. It is safe on a nil receiver and safe to call twice
// so a deferred release next to an explicit one is not a bug.
func (o *Ownership) Release() error {
	if o == nil || o.noop || o.file == nil {
		return nil
	}
	file := o.file
	o.file = nil
	// The record is cleared before the lock is dropped so a reader that wins
	// the race sees an empty file rather than a plausible-looking stale owner.
	_ = file.Truncate(0)
	unlockErr := unlock(file)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("unlock %q: %w", o.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close owner lock %q: %w", o.path, closeErr)
	}
	return nil
}

// InspectOwnership reports whether the database is currently owned, without
// claiming it.
//
// It answers by attempting the same non-blocking exclusive lock and releasing
// it immediately on success, because the sidecar's contents alone cannot tell
// a live owner from a record a killed process left behind. A stale file with
// no lock reports unowned, which is correct: the kernel already released it.
func InspectOwnership(databasePath string) (OwnerRecord, bool, error) {
	resolved, err := resolvePath(databasePath)
	if err != nil {
		return OwnerRecord{}, false, err
	}
	if isMemoryPath(resolved) {
		return OwnerRecord{}, false, nil
	}
	lockPath := resolved + ownerSuffix
	//nolint:gosec // lockPath is the configured database path plus a fixed suffix
	file, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return OwnerRecord{}, false, nil
	}
	if err != nil {
		return OwnerRecord{}, false, fmt.Errorf("open owner lock %q: %w", lockPath, err)
	}
	defer func() { _ = file.Close() }()

	locked, lockErr := tryLockExclusive(file)
	if lockErr != nil {
		return OwnerRecord{}, false, fmt.Errorf("probe %q: %w", lockPath, lockErr)
	}
	if locked {
		_ = unlock(file)
		return OwnerRecord{}, false, nil
	}
	holder, _ := readOwnerRecord(lockPath)
	return holder, true, nil
}

func newOwnerRecord(role OwnerRole, command string) OwnerRecord {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	if command == "" {
		command = filepath.Base(os.Args[0])
	}
	return OwnerRecord{
		PID:       os.Getpid(),
		Role:      role,
		Host:      host,
		Command:   command,
		StartedAt: time.Now().UTC(),
	}
}

func writeOwnerRecord(file *os.File, record OwnerRecord) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode owner record: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncate owner lock: %w", err)
	}
	if _, err := file.WriteAt(append(encoded, '\n'), 0); err != nil {
		return fmt.Errorf("write owner record: %w", err)
	}
	return file.Sync()
}

func readOwnerRecord(path string) (OwnerRecord, bool) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is derived from the configured database path
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return OwnerRecord{}, false
	}
	var record OwnerRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return OwnerRecord{}, false
	}
	return record, record.PID != 0
}
