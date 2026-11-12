package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	libsecrets "github.com/hollis-labs/libs/security/secrets"

	"github.com/hollis-labs/tangent/internal/appdirs"
	"github.com/hollis-labs/tangent/internal/filemigrate"
	"github.com/hollis-labs/tangent/internal/secretmigrate"
)

// migrateFiles is `tangent migrate-files`: a one-way copy of the artifact bodies and the installed
// plugins (with their state) from a pre-Substrate data directory into the new one. Like
// migrate-data it runs before the modules start and reads the old tree only.
func migrateFiles(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("migrate-files", flag.ContinueOnError)
	fs.SetOutput(out)
	from := fs.String("from", "", "the pre-Substrate data directory (the one holding tangent.db, artifacts/ and plugins/); required")
	to := fs.String("to", "", "the new data directory (required: a default nobody typed is how files land in the wrong place)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" || *to == "" || fs.NArg() > 0 {
		return errors.New("usage: tangent migrate-files --from <old data dir> --to <new data dir>")
	}
	src, err := filepath.Abs(*from)
	if err != nil {
		return err
	}
	dst, err := filepath.Abs(*to)
	if err != nil {
		return err
	}
	db := filepath.Join(dst, appdirs.DatabaseFile)
	if _, statErr := os.Stat(db); statErr != nil {
		db = ""
	}
	fmt.Fprintf(out, "migrate-files\n  read only: %s\n  create in: %s\n", src, dst)
	fmt.Fprintln(out, "  stop the old Tangent first so no file changes while it is copied; an existing non-empty target is refused")
	rep, err := filemigrate.Copy(ctx, filemigrate.Options{From: src, To: dst, Database: db})
	if err != nil {
		return err
	}
	for _, name := range filemigrate.Trees {
		if tr, ok := rep.Trees[name]; ok {
			fmt.Fprintf(out, "  %-10s %d files, %d bytes\n", name, tr.Files, tr.Bytes)
		}
	}
	fmt.Fprintln(out, "verified: every path, size, mode and sha256 equals the source; the old tree was not modified")
	if db == "" {
		fmt.Fprintf(out, "NOT CHECKED against the database: no %s yet. Run migrate-data first (or check again) to compare each artifact with its recorded sha256\n", filepath.Join(dst, appdirs.DatabaseFile))
	} else {
		fmt.Fprintf(out, "verified: %d artifact revisions match the sha256 in %s\n", rep.Revisions, db)
	}
	return nil
}

// migrateSecrets is `tangent migrate-secrets`: moves the plugin configuration secrets from the OS
// keychain into the secrets module's store. The keychain entries are left in place as the way
// back. --dry-run reads the keychain and the store and reports what would move, writing nothing.
func migrateSecrets(ctx context.Context, args []string, out io.Writer) error {
	return runMigrateSecrets(ctx, args, out, secretmigrate.OSKeychain{})
}

func runMigrateSecrets(ctx context.Context, args []string, out io.Writer, src secretmigrate.Source) error {
	fs := flag.NewFlagSet("migrate-secrets", flag.ContinueOnError)
	fs.SetOutput(out)
	to := fs.String("to", "", "the new data directory, after migrate-files (required)")
	dry := fs.Bool("dry-run", false, "report which references would move; write nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" || fs.NArg() > 0 {
		return errors.New("usage: tangent migrate-secrets --to <new data dir> [--dry-run]")
	}
	dst, err := filepath.Abs(*to)
	if err != nil {
		return err
	}
	dirs := appdirs.Dirs{Data: dst}
	configDB := filepath.Join(dirs.Plugins(), ".state", "config", "config.sqlite")
	refs, err := secretmigrate.References(ctx, configDB)
	if err != nil {
		return err
	}
	mode := "move"
	if *dry {
		mode = "dry run (nothing is written)"
	}
	fmt.Fprintf(out, "migrate-secrets: %s\n  references from: %s\n  store:           %s\n", mode, configDB, dirs.Secrets())
	fmt.Fprintln(out, "  values are never printed; keychain entries are left in place as the way back")
	if len(refs) == 0 {
		fmt.Fprintln(out, "no plugin setting holds a keychain secret; nothing to move")
		return nil
	}
	dest := libsecrets.New(libsecrets.WithStateDir(dirs.Secrets()))
	failed := 0
	for _, it := range secretmigrate.Move(ctx, refs, src, dest, *dry) {
		fmt.Fprintf(out, "  %s.%s  %d bytes  %s\n", it.Plugin, it.Key, it.Length, it.Status)
		if !it.OK {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("migrate-secrets: %d of %d references did not move", failed, len(refs))
	}
	if !*dry {
		fmt.Fprintln(out, "verified: each value read back from the store matches the keychain by length and keyed digest")
	}
	return nil
}
