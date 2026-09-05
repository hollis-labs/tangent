package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
)

// Integrity check, repair, and compaction.
//
// The scope of "repair" here is deliberately narrow, because a tool that
// claims to repair a database and cannot is worse than one that says "restore
// from a backup". Two things are repairable in place:
//
//   - A missing immutability guard. This is recoverable because the reference
//     schema is embedded in the binary: the trigger's exact text can be
//     recreated from the migrations that created it. It is also the failure
//     mode ADR 0002 §6 names as the specific danger of having a maintenance
//     path at all, so it is the one that most needs a way back.
//   - An unbounded WAL. Checkpointing folds it back into the main file.
//
// Everything else — page corruption, a dirty schema, foreign key violations —
// is reported, not fixed. The recovery for those is a restore, and saying so
// is the useful answer.

// CheckReport is the operator's whole-database status.
type CheckReport struct {
	Verification VerificationReport `json:"verification"`

	// Ownership names the process holding the single-writer lock, when one is.
	// A check is safe to run against a serving database; knowing that it was is
	// what stops "the numbers moved while I looked" from being a mystery.
	Ownership      OwnerRecord `json:"ownership,omitempty"`
	OwnershipHeld  bool        `json:"ownership_held"`
	OwnershipError string      `json:"ownership_error,omitempty"`

	// Storage is the physical shape: page counts, freelist, WAL size. It is
	// what tells an operator whether compaction would recover anything.
	Storage StorageReport `json:"storage"`

	// RetentionOperations is the recent erasure log, newest first, bounded.
	RetentionOperations []RetentionOperationRow `json:"retention_operations"`
}

// StorageReport is the physical footprint.
type StorageReport struct {
	PageSize      int64 `json:"page_size"`
	PageCount     int64 `json:"page_count"`
	FreelistPages int64 `json:"freelist_pages"`
	// ReclaimableBytes is what a compaction would return to the filesystem. It
	// is an estimate from the freelist, not a promise.
	ReclaimableBytes int64 `json:"reclaimable_bytes"`
	// WALBytes is the size of the -wal companion. A large one on an idle
	// database means checkpointing has not been happening.
	WALBytes int64 `json:"wal_bytes"`
	// JournalMode should be `wal`. Anything else means Open's pragma did not
	// take, and every guarantee in backup.go is written for WAL.
	JournalMode string `json:"journal_mode"`
	// ForeignKeysEnforced should be true. With it off, the cascades that a
	// purge relies on silently do nothing.
	ForeignKeysEnforced bool `json:"foreign_keys_enforced"`
}

// Check reports everything about a database without changing it.
func Check(ctx context.Context, database *sql.DB, databasePath string) (CheckReport, error) {
	report := CheckReport{RetentionOperations: []RetentionOperationRow{}}

	verification, err := VerifyDatabase(ctx, database)
	if err != nil {
		return report, err
	}
	report.Verification = verification

	holder, held, ownershipErr := InspectOwnership(databasePath)
	if ownershipErr != nil {
		report.OwnershipError = ownershipErr.Error()
	}
	report.Ownership = holder
	report.OwnershipHeld = held

	storage, err := readStorage(ctx, database, databasePath)
	if err != nil {
		return report, err
	}
	report.Storage = storage

	// The erasure log is only readable once the table exists; a database behind
	// migration 0012 is a legitimate thing to check.
	if verification.Migrations.Applied >= 12 {
		history, historyErr := RetentionHistory(ctx, database, 20)
		if historyErr != nil {
			return report, historyErr
		}
		report.RetentionOperations = history
	}
	return report, nil
}

// RepairReport is what a repair changed.
type RepairReport struct {
	// RestoredGuards names the immutability triggers that were missing and
	// have been recreated from the embedded reference schema.
	RestoredGuards []string `json:"restored_guards"`
	// UnrepairableGuards names triggers present in the database that the
	// reference schema does not have. They are reported, never dropped: this
	// command's authority is to restore what a migration created, not to
	// remove what something else did.
	UnrepairableGuards []string `json:"unrepairable_guards"`
	// CheckpointedWAL reports whether the WAL was folded back into the main
	// file.
	CheckpointedWAL bool `json:"checkpointed_wal"`
	// Unfixable lists the conditions a repair found and cannot address, with
	// the action that does.
	Unfixable []string `json:"unfixable"`
	// OperationID is the retention_operations row this repair wrote, present
	// only when it actually restored a guard.
	OperationID string `json:"operation_id,omitempty"`
}

// Repair recreates missing immutability guards and checkpoints the WAL.
//
// It requires the caller to hold the single-writer lock: recreating a trigger
// is DDL, and DDL against a database another process is writing is exactly the
// race this whole file exists to prevent.
func Repair(ctx context.Context, database *sql.DB, actorRef string) (RepairReport, error) {
	report := RepairReport{
		RestoredGuards: []string{}, UnrepairableGuards: []string{}, Unfixable: []string{},
	}

	status, err := InspectMigrations(ctx, database)
	if err != nil {
		return report, err
	}
	if status.Dirty {
		report.Unfixable = append(report.Unfixable,
			fmt.Sprintf("the schema is dirty at version %d: a migration failed part-way. "+
				"Restore from a backup taken before the upgrade; repair cannot finish a migration.",
				status.Applied))
		return report, nil
	}
	if !status.UpToDate() {
		report.Unfixable = append(report.Unfixable,
			fmt.Sprintf("the schema is at version %d and this binary expects %d. "+
				"Run --migrate-only; repair does not migrate.", status.Applied, status.Expected))
		return report, nil
	}

	installed, err := ReadGuards(ctx, database)
	if err != nil {
		return report, err
	}
	reference, err := ReferenceGuards(ctx)
	if err != nil {
		return report, err
	}
	drift := CompareGuards(installed, reference)
	report.UnrepairableGuards = drift.Unexpected

	if len(drift.Missing) > 0 {
		tx, beginErr := database.BeginTx(ctx, nil)
		if beginErr != nil {
			return report, fmt.Errorf("begin guard repair: %w", beginErr)
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		for _, name := range drift.Missing {
			guard, found := reference.Lookup(name)
			if !found || strings.TrimSpace(guard.SQL) == "" {
				report.Unfixable = append(report.Unfixable,
					fmt.Sprintf("guard %q is missing and the reference schema has no text for it", name))
				continue
			}
			if _, execErr := tx.ExecContext(ctx, guard.SQL); execErr != nil {
				return report, fmt.Errorf("recreate guard %q: %w", name, execErr)
			}
			report.RestoredGuards = append(report.RestoredGuards, name)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return report, fmt.Errorf("commit guard repair: %w", commitErr)
		}
		committed = true
	}

	problems, err := integrityProblems(ctx, database)
	if err != nil {
		return report, err
	}
	if len(problems) > 0 {
		report.Unfixable = append(report.Unfixable,
			"integrity_check reports page-level problems: "+strings.Join(problems, "; ")+
				". Restore from a backup; repair does not rebuild pages.")
	}
	var violations int64
	if err := database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_foreign_key_check;`).Scan(&violations); err != nil {
		return report, fmt.Errorf("foreign key check: %w", err)
	}
	if violations > 0 {
		report.Unfixable = append(report.Unfixable,
			fmt.Sprintf("%d foreign key violations. Restore from a backup; repair does not delete "+
				"rows to satisfy a constraint.", violations))
	}

	if err := checkpointTruncate(ctx, database); err == nil {
		report.CheckpointedWAL = true
	} else {
		report.Unfixable = append(report.Unfixable,
			"the WAL could not be checkpointed, which means something else is reading the database")
	}

	if len(report.RestoredGuards) > 0 {
		result := RetentionResult{
			OperationID: newOperationID(), Kind: OperationCapabilityExpiry,
			Outcome: OutcomeApplied, Code: "guards_repaired",
			AffectedRows: len(report.RestoredGuards), Removed: []RemovedContent{},
		}
		// A repair is recorded under the maintenance authority with an
		// affected-row count and no removed content, so the erasure log shows
		// that the guards were absent and when they came back.
		req := RetentionRequest{
			Kind: OperationCapabilityExpiry, ActorRef: actorRef,
			Authority: AuthorityMaintenance, PolicyRef: "guard-repair",
			Now: time.Now().UTC(),
		}
		if writeErr := writeOperationRowDB(ctx, database, req, result, req.at()); writeErr != nil {
			return report, writeErr
		}
		report.OperationID = result.OperationID
	}
	return report, nil
}

// CompactReport is what a compaction reclaimed.
type CompactReport struct {
	Before StorageReport `json:"before"`
	After  StorageReport `json:"after"`
	// BytesReclaimed is measured from the file, not estimated from the
	// freelist.
	BytesReclaimed int64 `json:"bytes_reclaimed"`
}

// Compact rewrites the database, returning free pages to the filesystem.
//
// VACUUM cannot run inside a transaction and needs exclusive access to the
// database file, so the caller must hold the single-writer lock. It preserves
// every row and every trigger — it is a rewrite, not a retention operation —
// and the fingerprint is asserted unchanged across it in the tests.
func Compact(ctx context.Context, database *sql.DB, databasePath string) (CompactReport, error) {
	report := CompactReport{}
	before, err := readStorage(ctx, database, databasePath)
	if err != nil {
		return report, err
	}
	report.Before = before

	if checkpointErr := checkpointTruncate(ctx, database); checkpointErr != nil {
		return report, fmt.Errorf("compact: %w", checkpointErr)
	}
	if _, vacuumErr := database.ExecContext(ctx, `VACUUM;`); vacuumErr != nil {
		return report, fmt.Errorf("compact: vacuum: %w", vacuumErr)
	}
	if checkpointErr := checkpointTruncate(ctx, database); checkpointErr != nil {
		return report, fmt.Errorf("compact: post-vacuum checkpoint: %w", checkpointErr)
	}

	after, err := readStorage(ctx, database, databasePath)
	if err != nil {
		return report, err
	}
	report.After = after
	report.BytesReclaimed = (before.PageSize * before.PageCount) - (after.PageSize * after.PageCount)
	return report, nil
}

// checkpointTruncate folds the WAL into the main database and truncates it.
//
// TRUNCATE is the strongest checkpoint: it blocks until every reader is done
// and leaves a zero-length WAL. That is what makes it the drain — if it cannot
// complete, something else is using the database, which is the thing the
// caller needed to know.
func checkpointTruncate(ctx context.Context, database *sql.DB) error {
	var busy, logFrames, checkpointed int
	err := database.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE);`).
		Scan(&busy, &logFrames, &checkpointed)
	if err != nil {
		// An in-memory database has no WAL and answers with no row.
		if strings.Contains(err.Error(), "no rows") {
			return nil
		}
		return fmt.Errorf("wal checkpoint: %w", err)
	}
	if busy != 0 {
		return fmt.Errorf("wal checkpoint did not complete: another connection holds the database")
	}
	return nil
}

// integrityProblems runs PRAGMA integrity_check and returns the problems, with
// SQLite's single "ok" row normalized to an empty slice.
func integrityProblems(ctx context.Context, database *sql.DB) ([]string, error) {
	rows, err := database.QueryContext(ctx, `PRAGMA integrity_check;`)
	if err != nil {
		return nil, fmt.Errorf("integrity check: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// The rows read before a failure are kept and returned alongside it. A
	// database damaged badly enough that integrity_check cannot finish still
	// names some of what is wrong before it stops, and that is the most
	// specific thing anyone is going to get.
	problems := []string{}
	for rows.Next() {
		var line string
		if scanErr := rows.Scan(&line); scanErr != nil {
			return problems, fmt.Errorf("scan integrity check: %w", scanErr)
		}
		if strings.EqualFold(strings.TrimSpace(line), "ok") {
			continue
		}
		problems = append(problems, line)
	}
	if err := rows.Err(); err != nil {
		return problems, fmt.Errorf("iterate integrity check: %w", err)
	}
	return problems, nil
}

func readStorage(ctx context.Context, database *sql.DB, databasePath string) (StorageReport, error) {
	report := StorageReport{}
	scalars := []struct {
		pragma string
		into   *int64
	}{
		{`PRAGMA page_size;`, &report.PageSize},
		{`PRAGMA page_count;`, &report.PageCount},
		{`PRAGMA freelist_count;`, &report.FreelistPages},
	}
	for _, scalar := range scalars {
		if err := database.QueryRowContext(ctx, scalar.pragma).Scan(scalar.into); err != nil {
			return report, fmt.Errorf("read %s: %w", scalar.pragma, err)
		}
	}
	report.ReclaimableBytes = report.PageSize * report.FreelistPages

	if err := database.QueryRowContext(ctx, `PRAGMA journal_mode;`).Scan(&report.JournalMode); err != nil {
		return report, fmt.Errorf("read journal mode: %w", err)
	}
	var foreignKeys int
	if err := database.QueryRowContext(ctx, `PRAGMA foreign_keys;`).Scan(&foreignKeys); err != nil {
		return report, fmt.Errorf("read foreign_keys pragma: %w", err)
	}
	report.ForeignKeysEnforced = foreignKeys == 1

	if resolved, err := resolvePath(databasePath); err == nil && !isMemoryPath(resolved) {
		report.WALBytes = fileSize(resolved + "-wal")
	}
	return report, nil
}

// fileSize reports a file's size, or zero when it does not exist. A missing
// -wal companion is the normal state of a freshly checkpointed database, not
// an error worth propagating.
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
