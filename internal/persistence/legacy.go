package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing/fstest"
	"time"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"
)

// ErrLegacyLayout is returned when a database file has the pre-Substrate layout.
var ErrLegacyLayout = errors.New("the database has the pre-Substrate layout")

// legacyFinalVersion is the last version of the pre-Substrate migrator (schema_migrations).
const legacyFinalVersion = 24

// MigrateReport is what a migration measured.
type MigrateReport struct {
	From, To string
	Tables   int
	Rows     map[string]int
	Duration time.Duration
}

// RefuseLegacy refuses a database file that has the pre-Substrate layout, so the database module
// never migrates one in place. A missing or empty file is fine. The file is opened read-only and
// left as found.
func RefuseLegacy(path string) error {
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		return nil //nolint:nilerr // nothing to judge yet
	}
	db, err := openReadOnly(path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&n); err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if n > 0 {
		return fmt.Errorf("%w (%s): it was written by the previous Tangent; run `tangent migrate-data --from %s --to <new file>` (see docs/conversion-notes.md), never open it in place", ErrLegacyLayout, path, path)
	}
	return nil
}

// MigrateLegacy copies every row of the pre-Substrate database at from into a new database at to.
// It reads from read-only and never writes to it, builds to under a temporary name and renames
// it into place only after every check passes, so a failed or interrupted run leaves nothing at
// to. It refuses when to exists.
//
// Order: the tables and indexes are created, the rows are copied (foreign keys off, one
// transaction), then the view and the triggers are created, so no trigger fires during the
// copy. Then the copy is proven, by two methods and the object definitions: per-table EXCEPT
// in both directions, a digest of every row, the typeof() histogram of every column, row
// counts, equal definitions of every index, view and trigger, integrity_check and
// foreign_key_check. The old files are fingerprinted before and after; any change aborts.
func MigrateLegacy(ctx context.Context, from, to string) (MigrateReport, error) {
	start := time.Now()
	rep := MigrateReport{From: from, To: to, Rows: map[string]int{}}
	if from == to {
		return rep, errors.New("migrate: from and to are the same file")
	}
	if _, err := os.Stat(from); err != nil {
		return rep, fmt.Errorf("migrate: old database: %w", err)
	}
	if _, err := os.Stat(to); err == nil {
		return rep, fmt.Errorf("migrate: %s already exists; refusing to overwrite it", to)
	}
	before, err := fingerprint(from)
	if err != nil {
		return rep, err
	}
	if err = requireFinalLegacy(ctx, from); err != nil {
		return rep, err
	}

	tmp := to + ".migrating"
	removeAll := func() {
		for _, f := range []string{tmp, tmp + "-wal", tmp + "-shm", tmp + "-journal"} {
			_ = os.Remove(f)
		}
	}
	removeAll() // a stale temporary file is what an earlier interrupted run left; the old file is untouched
	cfg := store.Config{Driver: store.DriverSQLite, SQLite: store.SQLiteConfig{Path: tmp, CreateParentDir: true}}
	st, err := store.Open(ctx, cfg)
	if err != nil {
		return rep, fmt.Errorf("migrate: create the new database: %w", err)
	}
	cleanup := func() { _ = st.Close(); removeAll() }

	fsys, root := Migrations()
	if err = migrate.Ensure(ctx, cfg, st, migrate.Source{FS: only(fsys, root, "00001_"), Root: root}); err != nil {
		cleanup()
		return rep, fmt.Errorf("migrate: create the tables: %w", err)
	}
	tables, err := copyTables(ctx, st, from, rep.Rows)
	if err != nil {
		cleanup()
		return rep, err
	}
	rep.Tables = len(tables)
	if err = migrate.Ensure(ctx, cfg, st, migrate.Source{FS: fsys, Root: root}); err != nil {
		cleanup()
		return rep, fmt.Errorf("migrate: create the view and triggers: %w", err)
	}
	if err = verify(ctx, st, from, tables); err != nil {
		cleanup()
		return rep, err
	}
	if afterChecksHook != nil {
		afterChecksHook()
	}
	after, err := fingerprint(from)
	if err != nil {
		cleanup()
		return rep, err
	}
	if after != before {
		cleanup()
		return rep, errors.New("migrate: the old database changed during the migration; stop whatever is writing to it (or migrate an online-backup copy) and run again")
	}
	if _, err = st.DB().ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		cleanup()
		return rep, fmt.Errorf("migrate: checkpoint: %w", err)
	}
	if err = st.Close(); err != nil {
		removeAll()
		return rep, err
	}
	for _, f := range []string{tmp + "-wal", tmp + "-shm"} {
		_ = os.Remove(f)
	}
	if err = os.Rename(tmp, to); err != nil {
		removeAll()
		return rep, fmt.Errorf("migrate: install the new database: %w", err)
	}
	rep.Duration = time.Since(start)
	return rep, nil
}

// afterChecksHook is called between the checks and the second fingerprint; tests use it to write
// to the old file at that moment.
var afterChecksHook func()

// only returns an fs holding the migrations under root whose names start with prefix.
func only(fsys fs.FS, root, prefix string) fs.FS {
	out := fstest.MapFS{}
	ents, _ := fs.ReadDir(fsys, root+"/sqlite")
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), prefix) {
			b, _ := fs.ReadFile(fsys, root+"/sqlite/"+e.Name())
			out[root+"/sqlite/"+e.Name()] = &fstest.MapFile{Data: b}
		}
	}
	return out
}

// fingerprint is the size, modification time and sha256 of the old database and of its -wal and
// -journal files (absent files count as absent).
func fingerprint(path string) (string, error) {
	var b strings.Builder
	for _, f := range []string{path, path + "-wal", path + "-journal"} {
		info, err := os.Stat(f)
		if errors.Is(err, os.ErrNotExist) {
			b.WriteString(f + ": absent\n")
			continue
		}
		if err != nil {
			return "", fmt.Errorf("migrate: inspect %s: %w", f, err)
		}
		fh, err := os.Open(f) //nolint:gosec // the operator names the old database
		if err != nil {
			return "", fmt.Errorf("migrate: read %s: %w", f, err)
		}
		h := sha256.New()
		_, cerr := io.Copy(h, fh)
		_ = fh.Close()
		if cerr != nil {
			return "", fmt.Errorf("migrate: read %s: %w", f, cerr)
		}
		fmt.Fprintf(&b, "%s: %d %d %x\n", f, info.Size(), info.ModTime().UnixNano(), h.Sum(nil))
	}
	return b.String(), nil
}

func openReadOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", readOnlyURI(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// readOnlyURI is how the old file is opened. mode=ro alone is not enough: on a WAL-mode file
// SQLite still creates a -shm (and -wal) beside it, and the old file must be left exactly as
// found. With no WAL content to read (no -wal, or an empty one) the file is opened immutable,
// which takes no locks and writes nothing; with a -wal that has content it is opened mode=ro so
// that content is still read.
func readOnlyURI(path string) string {
	q := "mode=ro"
	if fi, err := os.Stat(path + "-wal"); err != nil || fi.Size() == 0 {
		q = "mode=ro&immutable=1"
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: q}).String()
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// requireFinalLegacy checks the old file is a complete pre-Substrate Tangent database: one
// schema_migrations row at the final version, not dirty, integrity_check ok.
func requireFinalLegacy(ctx context.Context, from string) error {
	db, err := openReadOnly(from)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var version int64
	var dirty bool
	var n int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		return fmt.Errorf("migrate: %s is not a pre-Substrate Tangent database (no schema_migrations): %w", from, err)
	}
	if n != 1 {
		return fmt.Errorf("migrate: %s has %d schema_migrations rows, want 1", from, n)
	}
	if err = db.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("migrate: %s is marked dirty by the old migrator; repair it with the previous Tangent first", from)
	}
	if version != legacyFinalVersion {
		return fmt.Errorf("migrate: %s is at old schema version %d, want %d; open it once with the previous Tangent to finish its migrations", from, version, legacyFinalVersion)
	}
	// The copy carries the AUTOINCREMENT counters. SQLite creates sqlite_sequence with the first
	// AUTOINCREMENT table, and the final schema has several, so a file without it is not that schema.
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = 'sqlite_sequence'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("migrate: %s has no sqlite_sequence, so it has no AUTOINCREMENT table: it is not the final pre-Substrate schema", from)
	}
	var integrity string
	if err = db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("migrate: integrity_check of %s: %q", from, integrity)
	}
	var virtual int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE sql LIKE 'CREATE VIRTUAL TABLE%'`).Scan(&virtual); err != nil {
		return err
	}
	if virtual > 0 {
		return fmt.Errorf("migrate: %s has a virtual table, which the copy would drop; stopping", from)
	}
	return nil
}

// requireKnownTablesAndIndexes refuses an old file with a table or index the new schema does not
// have, before any row is copied. (Views and triggers are compared by definition once the new
// file has them: sameDefinitions.) schema_migrations and its index are the old migrator's own
// bookkeeping and are deliberately not carried: the new file has goose's table.
func requireKnownTablesAndIndexes(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx, `SELECT type, name FROM old.sqlite_master
		WHERE type IN ('table','index') AND name NOT LIKE 'sqlite\_%' ESCAPE '\' AND name NOT IN ('schema_migrations','version_unique')
		AND NOT EXISTS (SELECT 1 FROM main.sqlite_master m WHERE m.type = old.sqlite_master.type AND m.name = old.sqlite_master.name)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var typ, name string
		if err = rows.Scan(&typ, &name); err != nil {
			return err
		}
		return fmt.Errorf("migrate: the old database has a %s %q the new schema does not know; stopping rather than dropping it", typ, name)
	}
	return rows.Err()
}

// dataTables lists the tables of schema db ("main" or "old"), excluding bookkeeping.
func dataTables(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, schema string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT name FROM `+schema+`.sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' AND name NOT IN ('schema_migrations','goose_db_version') ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

type column struct {
	Name    string
	Type    string
	NotNull int
	PK      int
}

func columns(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, schema, table string) ([]column, error) {
	rows, err := q.QueryContext(ctx, `SELECT name, type, "notnull", pk FROM pragma_table_xinfo(?, ?)`, table, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []column
	for rows.Next() {
		var c column
		if err = rows.Scan(&c.Name, &c.Type, &c.NotNull, &c.PK); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func columnList(cols []column) string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = quote(c.Name)
	}
	return strings.Join(names, ", ")
}

// copyTables attaches the old file read-only and copies every table by column name in one
// transaction with foreign keys off (the new file is checked with foreign_key_check afterwards).
// It returns the tables copied.
func copyTables(ctx context.Context, st *store.Store, from string, counts map[string]int) ([]string, error) {
	conn, err := st.DB().Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if _, err = conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return nil, err
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`) }()
	if _, err = conn.ExecContext(ctx, `ATTACH DATABASE ? AS old`, readOnlyURI(from)); err != nil {
		return nil, fmt.Errorf("migrate: attach the old database: %w", err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), `DETACH DATABASE old`) }()

	if err = requireKnownTablesAndIndexes(ctx, conn); err != nil {
		return nil, err
	}
	oldTables, err := dataTables(ctx, conn, "old")
	if err != nil {
		return nil, err
	}
	newTables, err := dataTables(ctx, conn, "main")
	if err != nil {
		return nil, err
	}
	if strings.Join(oldTables, ",") != strings.Join(newTables, ",") {
		return nil, fmt.Errorf("migrate: the old and new databases do not have the same tables\nold: %v\nnew: %v", oldTables, newTables)
	}
	for _, t := range oldTables {
		oc, cerr := columns(ctx, conn, "old", t)
		if cerr != nil {
			return nil, cerr
		}
		nc, cerr := columns(ctx, conn, "main", t)
		if cerr != nil {
			return nil, cerr
		}
		if fmt.Sprint(oc) != fmt.Sprint(nc) {
			return nil, fmt.Errorf("migrate: table %s differs between the old and new schema\nold: %v\nnew: %v", t, oc, nc)
		}
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, t := range oldTables {
		cols, cerr := columns(ctx, tx, "main", t)
		if cerr != nil {
			return nil, cerr
		}
		// Generated columns cannot be inserted; the schema has none, and a test keeps it so.
		list := columnList(cols)
		//nolint:gosec // names come from the schema, quoted
		res, xerr := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO main.%s (%s) SELECT %s FROM old.%s`, quote(t), list, list, quote(t)))
		if xerr != nil {
			return nil, fmt.Errorf("migrate: copy %s: %w", t, xerr)
		}
		n, _ := res.RowsAffected()
		counts[t] = int(n)
	}
	// AUTOINCREMENT counters: sqlite_sequence is not a table the loop copies. goose's own version
	// table is AUTOINCREMENT too, and its counter stays.
	if _, err = tx.ExecContext(ctx, `DELETE FROM main.sqlite_sequence WHERE name != 'goose_db_version'`); err != nil {
		return nil, fmt.Errorf("migrate: reset the sequence counters: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO main.sqlite_sequence (name, seq) SELECT name, seq FROM old.sqlite_sequence`); err != nil {
		return nil, fmt.Errorf("migrate: copy the sequence counters: %w", err)
	}
	for _, t := range oldTables {
		if err = exceptBothWays(ctx, tx, t); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return oldTables, nil
}

// exceptBothWays proves the copy by content inside the copy transaction, on the attached files.
func exceptBothWays(ctx context.Context, tx *sql.Tx, table string) error {
	cols, err := columns(ctx, tx, "main", table)
	if err != nil {
		return err
	}
	list := columnList(cols)
	q := quote(table)
	for _, d := range []struct{ a, b, what string }{
		{"old", "main", "in the old database but missing or changed in the new one"},
		{"main", "old", "in the new database but not in the old one"},
	} {
		var n int
		//nolint:gosec // names come from the schema, quoted
		query := fmt.Sprintf(`SELECT count(*) FROM (SELECT %s FROM %s.%s EXCEPT SELECT %s FROM %s.%s)`, list, d.a, q, list, d.b, q)
		if err := tx.QueryRowContext(ctx, query).Scan(&n); err != nil {
			return fmt.Errorf("migrate: compare %s: %w", table, err)
		}
		if n != 0 {
			return fmt.Errorf("migrate: %d rows of %s are %s", n, table, d.what)
		}
	}
	return nil
}

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// verify checks the finished new file against the old one by a second, independent method set:
// row counts, a digest of every row, the typeof histogram of every column, equal definitions of
// every object, integrity_check and foreign_key_check.
func verify(ctx context.Context, st *store.Store, from string, tables []string) error {
	old, err := openReadOnly(from)
	if err != nil {
		return err
	}
	defer func() { _ = old.Close() }()
	cur := st.Reader()
	for _, t := range tables {
		cols, cerr := columns(ctx, cur, "main", t)
		if cerr != nil {
			return cerr
		}
		var cOld, cNew int
		if err = old.QueryRowContext(ctx, `SELECT count(*) FROM `+quote(t)).Scan(&cOld); err != nil {
			return fmt.Errorf("migrate: count old %s: %w", t, err)
		}
		if err = cur.QueryRowContext(ctx, `SELECT count(*) FROM `+quote(t)).Scan(&cNew); err != nil {
			return fmt.Errorf("migrate: count new %s: %w", t, err)
		}
		if cOld != cNew {
			return fmt.Errorf("migrate: %s has %d rows in the old database and %d in the new one", t, cOld, cNew)
		}
		dOld, derr := tableDigest(ctx, old, t, cols)
		if derr != nil {
			return derr
		}
		dNew, derr := tableDigest(ctx, cur, t, cols)
		if derr != nil {
			return derr
		}
		if dOld != dNew {
			return fmt.Errorf("migrate: the digest of %s differs between the old and new database", t)
		}
		for _, c := range cols {
			hOld, herr := typeHistogram(ctx, old, t, c.Name)
			if herr != nil {
				return herr
			}
			hNew, herr := typeHistogram(ctx, cur, t, c.Name)
			if herr != nil {
				return herr
			}
			if hOld != hNew {
				return fmt.Errorf("migrate: the value types of %s.%s differ: old %s, new %s", t, c.Name, hOld, hNew)
			}
		}
	}
	if err = sameDefinitions(ctx, old, cur); err != nil {
		return err
	}
	if err = sameSequences(ctx, old, cur); err != nil {
		return err
	}
	var integrity string
	if err = cur.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("migrate: integrity_check of the new database: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("migrate: integrity_check of the new database: %q", integrity)
	}
	var oldFK, newFK int
	if err = countRows(ctx, old, `PRAGMA foreign_key_check`, &oldFK); err != nil {
		return err
	}
	if err = countRows(ctx, cur, `PRAGMA foreign_key_check`, &newFK); err != nil {
		return err
	}
	if newFK != oldFK {
		return fmt.Errorf("migrate: foreign_key_check reports %d violations in the new database, %d in the old one", newFK, oldFK)
	}
	return nil
}

func countRows(ctx context.Context, q querier, query string, n *int) error {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		*n++
	}
	return rows.Err()
}

// tableDigest hashes every row, ordered by primary key (or rowid), as the quote()d text of each
// column, so a value's type is part of what is compared.
func tableDigest(ctx context.Context, q querier, table string, cols []column) (string, error) {
	var pk []column
	for _, c := range cols {
		if c.PK > 0 {
			pk = append(pk, c)
		}
	}
	sort.Slice(pk, func(i, j int) bool { return pk[i].PK < pk[j].PK })
	order := "rowid"
	if len(pk) > 0 {
		names := make([]string, len(pk))
		for i, c := range pk {
			names[i] = quote(c.Name)
		}
		order = strings.Join(names, ", ")
	}
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = "quote(" + quote(c.Name) + "), typeof(" + quote(c.Name) + ")"
	}
	//nolint:gosec // names come from the schema, quoted
	rows, err := q.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM %s ORDER BY %s`, strings.Join(parts, ", "), quote(table), order))
	if err != nil {
		return "", fmt.Errorf("migrate: read %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	h := sha256.New()
	dest := make([]any, len(cols)*2)
	vals := make([]sql.NullString, len(cols)*2)
	for i := range dest {
		dest[i] = &vals[i]
	}
	for rows.Next() {
		if err = rows.Scan(dest...); err != nil {
			return "", err
		}
		for _, v := range vals {
			fmt.Fprintf(h, "%d:%s|", len(v.String), v.String)
		}
		h.Write([]byte("\n"))
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func typeHistogram(ctx context.Context, q querier, table, col string) (string, error) {
	//nolint:gosec // names come from the schema, quoted
	rows, err := q.QueryContext(ctx, fmt.Sprintf(`SELECT typeof(%s), count(*) FROM %s GROUP BY 1 ORDER BY 1`, quote(col), quote(table)))
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var t string
		var n int
		if err = rows.Scan(&t, &n); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s=%d;", t, n)
	}
	return b.String(), rows.Err()
}

// sameSequences requires the AUTOINCREMENT counters (sqlite_sequence) to be equal on both sides:
// a counter can be above max(id) when rows were deleted, and a lower one would reuse ids.
func sameSequences(ctx context.Context, old, cur querier) error {
	read := func(q querier) (map[string]int64, error) {
		rows, err := q.QueryContext(ctx, `SELECT name, seq FROM sqlite_sequence WHERE name != 'goose_db_version'`)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		out := map[string]int64{}
		for rows.Next() {
			var n string
			var v int64
			if err = rows.Scan(&n, &v); err != nil {
				return nil, err
			}
			out[n] = v
		}
		return out, rows.Err()
	}
	o, err := read(old)
	if err != nil {
		return err
	}
	n, err := read(cur)
	if err != nil {
		return err
	}
	if len(o) != len(n) {
		return fmt.Errorf("migrate: sqlite_sequence has %d counters in the old database and %d in the new one", len(o), len(n))
	}
	for k, v := range o {
		if n[k] != v {
			return fmt.Errorf("migrate: the counter of %s is %d in the old database and %d in the new one", k, v, n[k])
		}
	}
	return nil
}

// sameDefinitions requires every table, index, view and trigger of the old file to exist in the
// new one with identical SQL text (the old migrator's own bookkeeping excepted).
func sameDefinitions(ctx context.Context, old, cur querier) error {
	read := func(q querier) (map[string]string, error) {
		rows, err := q.QueryContext(ctx, `SELECT type || ':' || name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\' AND name NOT IN ('schema_migrations','version_unique','goose_db_version') AND sql IS NOT NULL`)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		out := map[string]string{}
		for rows.Next() {
			var k, s string
			if err = rows.Scan(&k, &s); err != nil {
				return nil, err
			}
			out[k] = s
		}
		return out, rows.Err()
	}
	o, err := read(old)
	if err != nil {
		return err
	}
	n, err := read(cur)
	if err != nil {
		return err
	}
	for k, s := range o {
		if n[k] != s {
			return fmt.Errorf("migrate: the definition of %s differs between the old and new database", k)
		}
	}
	for k := range n {
		if _, ok := o[k]; !ok {
			return fmt.Errorf("migrate: %s exists in the new database but not in the old one", k)
		}
	}
	return nil
}
