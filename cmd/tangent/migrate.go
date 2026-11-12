package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/hollis-labs/tangent/internal/appdirs"
	"github.com/hollis-labs/tangent/internal/persistence"
)

// migrateData is `tangent migrate-data`: a one-way copy of a pre-Substrate Tangent database into
// the new file. It runs before the modules start, because it builds the new database itself and
// an old database makes them refuse to start. The old file is only ever read.
func migrateData(ctx context.Context, getenv func(string) string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("migrate-data", flag.ContinueOnError)
	fs.SetOutput(out)
	from := fs.String("from", "", "the pre-Substrate tangent.db to copy (required; copy an online backup, not the live file)")
	to := fs.String("to", "", "the new database to create (default: tangent.db in the data directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return errors.New("migrate-data: --from is required")
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("migrate-data: unexpected argument %q", fs.Arg(0))
	}
	target := *to
	if target == "" {
		dirs, err := appdirs.Resolve(getenv)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(dirs.Data, 0o700); err != nil {
			return err
		}
		target = dirs.Database()
		// An empty file is what a start that failed early leaves; nothing in it is data.
		if fi, err := os.Stat(target); err == nil && fi.Size() == 0 {
			if err = os.Remove(target); err != nil {
				return err
			}
		}
	}
	rep, err := persistence.MigrateLegacy(ctx, *from, target)
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
	fmt.Fprintln(out, "verified: row counts, per-row digests, value-type histograms, EXCEPT in both directions, object definitions, integrity_check, foreign_key_check; the old file was not modified")
	return nil
}
