package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// The immutability guards are the triggers that make ADR 0001's immutability
// claim true at the storage layer rather than by convention, and ADR 0002 §6
// is the decision that they are suspended — never weakened — for the length of
// one transaction when content has to be removed.
//
// Two facts about them are load-bearing and neither is written down anywhere
// else, so they are derived rather than asserted:
//
//  1. What the inventory *should* be. Hard-coding a list means the next
//     migration that adds a table adds a guard the repair path does not know
//     about, and a database missing that guard reports healthy. So the expected
//     inventory is produced by applying this binary's own embedded migrations
//     to a throwaway in-memory database and reading its catalog. The reference
//     is the migrations; nothing can drift from itself.
//
//  2. What each guard's SQL is. The maintenance path reads it from
//     `sqlite_master` immediately before dropping the trigger and replays that
//     exact text to recreate it, inside the same transaction. It never
//     reconstructs the SQL from a Go string, because a guard recreated from a
//     paraphrase is a guard that no longer says what the migration said.
//
// ADR 0002 §6 called this out as "genuinely dangerous" and required tests. The
// containment is layered: SQLite DDL is transactional, so a rollback restores
// every dropped trigger; `SetMaxOpenConns(1)` means the transaction holds the
// only connection this process has, so no other statement here can observe the
// window; and the single-writer lock in ownership.go means no other process
// can either.

// Guard is one trigger in the schema, with the exact text that created it.
type Guard struct {
	Name string `json:"name"`
	// Table is the table the trigger is attached to.
	Table string `json:"table"`
	// Event is INSERT, UPDATE, or DELETE — what the trigger fires on. It is
	// parsed from the SQL rather than stored by SQLite, so an unrecognized
	// shape reports "unknown" rather than guessing.
	Event string `json:"event"`
	// SQL is the verbatim CREATE TRIGGER statement from sqlite_master.
	SQL string `json:"-"`
}

// GuardInventory is a comparable snapshot of the trigger catalog.
type GuardInventory struct {
	Guards []Guard `json:"guards"`
}

// Names returns the guard names, sorted.
func (i GuardInventory) Names() []string {
	names := make([]string, 0, len(i.Guards))
	for _, guard := range i.Guards {
		names = append(names, guard.Name)
	}
	sort.Strings(names)
	return names
}

// Lookup returns one guard by name.
func (i GuardInventory) Lookup(name string) (Guard, bool) {
	for _, guard := range i.Guards {
		if guard.Name == name {
			return guard, true
		}
	}
	return Guard{}, false
}

// ReadGuards reads the trigger catalog of a live database.
func ReadGuards(ctx context.Context, q queryer) (GuardInventory, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT name, tbl_name, sql FROM sqlite_master WHERE type = 'trigger' ORDER BY name;`)
	if err != nil {
		return GuardInventory{}, fmt.Errorf("read trigger catalog: %w", err)
	}
	defer func() { _ = rows.Close() }()

	inventory := GuardInventory{}
	for rows.Next() {
		var guard Guard
		var text sql.NullString
		if err := rows.Scan(&guard.Name, &guard.Table, &text); err != nil {
			return GuardInventory{}, fmt.Errorf("scan trigger catalog: %w", err)
		}
		guard.SQL = text.String
		guard.Event = triggerEvent(guard.SQL)
		inventory.Guards = append(inventory.Guards, guard)
	}
	if err := rows.Err(); err != nil {
		return GuardInventory{}, fmt.Errorf("iterate trigger catalog: %w", err)
	}
	return inventory, nil
}

// ReferenceGuards is the inventory this binary's migrations produce, built by
// migrating a throwaway in-memory database. It is what "the guards are intact"
// is measured against, and it costs one in-memory migration run.
func ReferenceGuards(ctx context.Context) (GuardInventory, error) {
	expected, err := ExpectedMigrationVersion()
	if err != nil {
		return GuardInventory{}, err
	}
	return ReferenceGuardsAt(ctx, expected)
}

// ReferenceGuardsAt is the inventory this binary's migrations produce at one
// schema version, built by migrating a throwaway in-memory database to exactly
// that version.
//
// The version is the whole point. Comparing a schema-10 database against the
// inventory at migration 12 reports the guards migrations 0011 and 0012 create
// as *missing*, which reads as "this database has lost its immutability
// guards" when the truth is "this database predates them". That mistake is
// what CW-20260905-0014 cost: a sound pre-upgrade backup declared damaged. A
// guard a schema never had is not a guard that went away.
//
// Version 0 is a database that has never been migrated: it defines no guards,
// and saying so is not the same as failing.
func ReferenceGuardsAt(ctx context.Context, version int64) (GuardInventory, error) {
	if version <= 0 {
		return GuardInventory{}, nil
	}
	expected, err := ExpectedMigrationVersion()
	if err != nil {
		return GuardInventory{}, err
	}
	if version > expected {
		return GuardInventory{}, fmt.Errorf(
			"no reference schema for version %d: this binary embeds %d migrations",
			version, expected)
	}
	reference, err := Open(":memory:")
	if err != nil {
		return GuardInventory{}, fmt.Errorf("open reference schema: %w", err)
	}
	defer func() { _ = reference.Close() }()
	if err := migrateTo(reference, version); err != nil {
		return GuardInventory{}, fmt.Errorf("migrate reference schema to %d: %w", version, err)
	}
	return ReadGuards(ctx, reference)
}

// GuardDrift is the difference between a database's guards and the reference.
// Missing is the dangerous half — a table whose immutability is no longer
// enforced — and Unexpected is the half that says something added a trigger
// this binary does not know about.
type GuardDrift struct {
	Missing    []string `json:"missing"`
	Unexpected []string `json:"unexpected"`
}

// Intact reports whether the guards match the reference exactly.
func (d GuardDrift) Intact() bool {
	return len(d.Missing) == 0 && len(d.Unexpected) == 0
}

// CompareGuards diffs an installed inventory against a reference one.
func CompareGuards(installed, reference GuardInventory) GuardDrift {
	have := make(map[string]struct{}, len(installed.Guards))
	for _, guard := range installed.Guards {
		have[guard.Name] = struct{}{}
	}
	want := make(map[string]struct{}, len(reference.Guards))
	for _, guard := range reference.Guards {
		want[guard.Name] = struct{}{}
	}

	drift := GuardDrift{}
	for name := range want {
		if _, ok := have[name]; !ok {
			drift.Missing = append(drift.Missing, name)
		}
	}
	for name := range have {
		if _, ok := want[name]; !ok {
			drift.Unexpected = append(drift.Unexpected, name)
		}
	}
	sort.Strings(drift.Missing)
	sort.Strings(drift.Unexpected)
	return drift
}

// withGuardsSuspended is the custody maintenance path of ADR 0002 §6, and the
// only code in Tangent permitted to run without the immutability triggers in
// place.
//
// It drops exactly the named guards, runs fn, recreates them from the SQL it
// read before dropping, verifies they are back, and commits — all inside one
// transaction. Any error rolls back, and because SQLite DDL participates in
// the transaction, a rollback restores the triggers as surely as the explicit
// recreation does. The verification before commit is therefore belt and braces
// rather than the only line of defense, and it is there because "the guards
// came back" is the one thing this function must never merely assume.
//
// The bool it returns is that verification: true means the named guards were
// observed present again in the same transaction that removed them.
func withGuardsSuspended(
	ctx context.Context,
	database *sql.DB,
	guardNames []string,
	fn func(ctx context.Context, tx *sql.Tx) error,
) (restored bool, err error) {
	if database == nil {
		return false, fmt.Errorf("suspend guards: nil database")
	}
	if len(guardNames) == 0 {
		return false, fmt.Errorf("suspend guards: no guards named")
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin guard-suspended transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	saved := make([]Guard, 0, len(guardNames))
	for _, name := range guardNames {
		var text sql.NullString
		scanErr := tx.QueryRowContext(ctx,
			`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?;`, name).Scan(&text)
		if scanErr != nil {
			return false, fmt.Errorf("read guard %q before suspending it: %w", name, scanErr)
		}
		if strings.TrimSpace(text.String) == "" {
			return false, fmt.Errorf("guard %q has no recorded SQL; refusing to drop what cannot be recreated", name)
		}
		saved = append(saved, Guard{Name: name, SQL: text.String})
	}

	for _, guard := range saved {
		// The identifier comes from sqlite_master, matched against a name the
		// caller supplied from a package-level constant; it is not user input.
		if _, dropErr := tx.ExecContext(ctx, `DROP TRIGGER `+quoteIdentifier(guard.Name)+`;`); dropErr != nil {
			return false, fmt.Errorf("suspend guard %q: %w", guard.Name, dropErr)
		}
	}

	if fnErr := fn(ctx, tx); fnErr != nil {
		return false, fnErr
	}

	for _, guard := range saved {
		if _, createErr := tx.ExecContext(ctx, guard.SQL); createErr != nil {
			return false, fmt.Errorf("restore guard %q: %w", guard.Name, createErr)
		}
	}
	for _, guard := range saved {
		var count int
		if checkErr := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?;`,
			guard.Name).Scan(&count); checkErr != nil {
			return false, fmt.Errorf("verify guard %q: %w", guard.Name, checkErr)
		}
		if count != 1 {
			return false, fmt.Errorf("guard %q was not restored; refusing to commit", guard.Name)
		}
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return false, fmt.Errorf("commit guard-suspended transaction: %w", commitErr)
	}
	committed = true
	return true, nil
}

// triggerEvent extracts INSERT/UPDATE/DELETE from a CREATE TRIGGER statement.
func triggerEvent(text string) string {
	upper := strings.ToUpper(text)
	for _, event := range []string{"DELETE", "UPDATE", "INSERT"} {
		if strings.Contains(upper, "BEFORE "+event) || strings.Contains(upper, "AFTER "+event) ||
			strings.Contains(upper, "INSTEAD OF "+event) {
			return event
		}
	}
	return "unknown"
}

// quoteIdentifier renders a SQLite identifier safely. Every caller passes a
// package constant or a name read back from sqlite_master, but DDL cannot take
// a bound parameter and an unquoted identifier concatenated into DDL is a
// pattern worth never establishing.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// queryer is the read half shared by *sql.DB and *sql.Tx.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
