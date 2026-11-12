package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/hollis-labs/tangent/internal/persistence"
)

// migrateData is `tangent migrate-data`: a one-way copy of a pre-Substrate Tangent database into
// the new file. It runs before the modules start, because it builds the new database itself and
// an old database makes them refuse to start. The old file is only ever read.
func migrateData(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("migrate-data", flag.ContinueOnError)
	fs.SetOutput(out)
	from := fs.String("from", "", "the pre-Substrate tangent.db to copy (required; copy an online backup, not the live file)")
	to := fs.String("to", "", "the new database to create (required: a default nobody typed is how a file lands in the wrong place)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return errors.New("migrate-data: --from is required")
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("migrate-data: unexpected argument %q", fs.Arg(0))
	}
	if *to == "" {
		return errors.New("migrate-data: --to is required")
	}
	src, err := filepath.Abs(*from)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(*to)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "migrate-data\n  read only: %s\n  create:    %s\n", src, target)
	fmt.Fprintln(out, "  the old file is opened read-only and never written; the new file is built under a temporary name and installed only after every check passes; an existing target is refused")
	rep, err := persistence.MigrateLegacy(ctx, src, target)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(rep.Rows))
	total := 0
	for n, c := range rep.Rows {
		names = append(names, n)
		total += c
	}
	sort.Strings(names)
	fmt.Fprintf(out, "migrated %s -> %s: %d tables, %d rows, in %s\n", rep.From, rep.To, rep.Tables, total, rep.Duration.Round(1e6))
	for _, n := range names {
		if rep.Rows[n] > 0 {
			fmt.Fprintf(out, "  %-40s %d\n", n, rep.Rows[n])
		}
	}
	fmt.Fprintln(out, "verified: row counts, per-row digests, value-type histograms, EXCEPT in both directions, object definitions, AUTOINCREMENT counters (sqlite_sequence), integrity_check, foreign_key_check; the old file was not modified. The old migrator's schema_migrations table is not copied (the new file has goose's own table); no other object was left behind")
	return nil
}
