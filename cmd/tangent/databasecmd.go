package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/hollis-labs/tangent/internal/appdirs"
	"github.com/hollis-labs/tangent/internal/dbtool"
)

// databaseCommand is `tangent database check|backup`. Both run before the modules start and open
// the database read-only: neither creates the data directory or writes the source, so they are
// safe beside a running server and in an installer preflight.
func databaseCommand(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	if len(args) == 0 || (args[0] != "check" && args[0] != "backup") {
		return errors.New("usage: tangent database check [--db PATH] | tangent database backup --to PATH [--db PATH]")
	}
	fs := flag.NewFlagSet("database "+args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	db := fs.String("db", "", "the database (default: the one in the data directory)")
	to := fs.String("to", "", "backup only: the new file to write (required; an existing one is refused)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("database %s: unexpected argument %q", args[0], fs.Arg(0))
	}
	src := *db
	if src == "" {
		dirs, resolveErr := appdirs.Resolve(getenv)
		if resolveErr != nil {
			return resolveErr
		}
		src = dirs.Database()
	}
	src, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	if args[0] == "check" {
		rep, checkErr := dbtool.Check(ctx, src)
		if checkErr != nil {
			return checkErr
		}
		report(out, "check", rep)
		if !rep.OK() {
			return errors.New("database check: the database is damaged")
		}
		return nil
	}
	if *to == "" {
		return errors.New("database backup: --to is required")
	}
	dst, err := filepath.Abs(*to)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "database backup\n  read only: %s\n  create:    %s\n", src, dst)
	rep, err := dbtool.Backup(ctx, src, dst)
	if err != nil {
		return err
	}
	report(out, "backup", rep)
	fmt.Fprintln(out, "the backup is a point-in-time copy from a read transaction; the source was not written")
	return nil
}

func report(out io.Writer, what string, r dbtool.Report) {
	fmt.Fprintf(out, "%s %s: integrity %s, %d foreign-key problems, migration %d\n", what, r.Path, r.Integrity, r.ForeignKeyRows, r.Migration)
}
