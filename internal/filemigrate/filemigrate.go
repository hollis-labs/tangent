// Package filemigrate copies the files a pre-Substrate Tangent kept beside its database (the
// artifact bodies and the installed plugins with their state) into the new data directory. It is
// one-way and conservative: the old tree is only read, each tree is built under a staging name,
// compared byte for byte with its source, checked against the database's own digests, and only then
// renamed into place; an existing non-empty target is refused.
package filemigrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // the driver the database check reads through
)

// Trees are the directories copied, by name under both the old and the new data directory.
var Trees = []string{"artifacts", "plugins"}

// Report is what a run did.
type Report struct {
	Trees     map[string]TreeReport
	Revisions int // artifact revisions checked against the database digests; 0 when no database was given
}

// TreeReport counts one copied tree.
type TreeReport struct {
	Files int
	Bytes int64
}

// Options select the run.
type Options struct {
	From, To string
	// Database is the new database whose artifact_revisions rows the copied artifact bodies are
	// checked against. Empty skips that check (the caller says so).
	Database string
}

// Copy runs the migration.
func Copy(ctx context.Context, opts Options) (Report, error) {
	from, to := filepath.Clean(opts.From), filepath.Clean(opts.To)
	if err := checkRoots(from, to); err != nil {
		return Report{}, err
	}
	rep := Report{Trees: map[string]TreeReport{}}
	var present []string
	for _, name := range Trees {
		info, err := os.Lstat(filepath.Join(from, name))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return rep, err
		case !info.IsDir():
			return rep, fmt.Errorf("%s is not a directory", filepath.Join(from, name))
		}
		if err := targetFree(filepath.Join(to, name)); err != nil {
			return rep, err
		}
		present = append(present, name)
	}
	if len(present) == 0 {
		return rep, fmt.Errorf("nothing to copy: %s holds neither artifacts nor plugins", from)
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return rep, err
	}
	var staged []string
	cleanup := func() {
		for _, s := range staged {
			_ = os.RemoveAll(s)
		}
	}
	for _, name := range present {
		stage := filepath.Join(to, name+".migrating")
		if _, err := os.Lstat(stage); err == nil {
			cleanup()
			return rep, fmt.Errorf("%s exists from an earlier run; remove it and run again", stage)
		}
		staged = append(staged, stage)
		tr, err := copyTree(ctx, filepath.Join(from, name), stage)
		if err != nil {
			cleanup()
			return rep, fmt.Errorf("copy %s: %w", name, err)
		}
		if err := verifyTree(ctx, filepath.Join(from, name), stage); err != nil {
			cleanup()
			return rep, fmt.Errorf("verify %s: %w", name, err)
		}
		rep.Trees[name] = tr
	}
	if opts.Database != "" && contains(present, "artifacts") {
		n, err := checkArtifacts(ctx, opts.Database, filepath.Join(to, "artifacts.migrating"))
		if err != nil {
			cleanup()
			return rep, err
		}
		rep.Revisions = n
	}
	for i, name := range present {
		if err := installTree(staged[i], filepath.Join(to, name)); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func checkRoots(from, to string) error {
	if !filepath.IsAbs(from) || !filepath.IsAbs(to) {
		return errors.New("both directories must be absolute paths")
	}
	info, err := os.Stat(from)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%s is not a directory", from)
	}
	for _, pair := range [][2]string{{from, to}, {to, from}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%s and %s are the same directory or one is inside the other", from, to)
		}
	}
	return nil
}

// targetFree accepts a missing or empty directory and refuses anything else.
func targetFree(path string) error {
	entries, err := os.ReadDir(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case len(entries) > 0:
		return fmt.Errorf("%s already holds files; this migration only fills an empty target", path)
	}
	return nil
}

// installTree puts a verified staging tree in place, replacing an empty directory if one is there.
func installTree(stage, final string) error {
	if err := os.Remove(final); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Rename(stage, final)
}

func copyTree(ctx context.Context, from, to string) (TreeReport, error) {
	var tr TreeReport
	type dirMode struct {
		path string
		mode fs.FileMode
	}
	var dirs []dirMode
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		target := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			dirs = append(dirs, dirMode{target, info.Mode().Perm()})
			return os.MkdirAll(target, 0o700)
		case info.Mode().IsRegular():
			n, err := copyFile(path, target, info)
			tr.Files++
			tr.Bytes += n
			return err
		default:
			return fmt.Errorf("%s is not a regular file or directory (%s); this migration copies neither links nor devices", path, info.Mode().Type())
		}
	})
	if err != nil {
		return tr, err
	}
	// A directory takes its source's mode after its contents are in, so a read-only one is filled
	// first; plugin config requires its directory to stay 0700.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i].path, dirs[i].mode); err != nil { // #nosec G302 -- the source directory's own mode.
			return tr, err
		}
	}
	return tr, nil
}

func copyFile(from, to string, info fs.FileInfo) (int64, error) {
	in, err := os.Open(from) // #nosec G304 -- a path the walk produced under the old data directory.
	if err != nil {
		return 0, err
	}
	defer in.Close()                                                      //nolint:errcheck // read-only
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- under the staging directory.
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	if err = os.Chmod(to, info.Mode().Perm()); err != nil { // #nosec G302 -- the source file's own mode.
		return n, err
	}
	return n, os.Chtimes(to, info.ModTime(), info.ModTime())
}

// listing is every path under root with its size, mode and digest.
func listing(ctx context.Context, root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			out[rel+"/"] = fmt.Sprintf("dir %o", info.Mode().Perm())
			return nil
		}
		sum, err := digest(path)
		if err != nil {
			return err
		}
		out[rel] = fmt.Sprintf("%d %o %s", info.Size(), info.Mode().Perm(), sum)
		return nil
	})
	return out, err
}

func digest(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- a path the walk produced.
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // read-only
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verifyTree fails unless both trees hold the same paths with the same size, mode and sha256.
func verifyTree(ctx context.Context, from, to string) error {
	a, err := listing(ctx, from)
	if err != nil {
		return err
	}
	b, err := listing(ctx, to)
	if err != nil {
		return err
	}
	var bad []string
	for p, v := range a {
		if b[p] != v {
			bad = append(bad, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			bad = append(bad, p)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("%d path(s) differ from the source, first: %s", len(bad), bad[0])
	}
	return nil
}

// checkArtifacts compares every artifact_revisions row's sha256 with the copied body.
func checkArtifacts(ctx context.Context, database, artifacts string) (int, error) {
	if _, err := os.Stat(database); err != nil {
		return 0, fmt.Errorf("database %s: %w", database, err)
	}
	dsn := (&url.URL{Scheme: "file", Path: database, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, err
	}
	defer db.Close() //nolint:errcheck // read-only
	rows, err := db.QueryContext(ctx, "SELECT rel_path, sha256 FROM artifact_revisions")
	if err != nil {
		return 0, fmt.Errorf("read artifact_revisions: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only
	n := 0
	var bad []string
	for rows.Next() {
		var rel, want string
		if err := rows.Scan(&rel, &want); err != nil {
			return n, err
		}
		n++
		clean := filepath.Clean(rel)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			bad = append(bad, rel+" (path leaves the artifact directory)")
			continue
		}
		got, err := digest(filepath.Join(artifacts, clean))
		if err != nil || got != want {
			bad = append(bad, rel)
		}
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	if len(bad) > 0 {
		if len(bad) > 10 {
			bad = append(bad[:10], fmt.Sprintf("... and %d more", len(bad)-10))
		}
		return n, fmt.Errorf("%d of %d artifact revisions do not match the database's sha256 (missing or different in the old tree): %s", len(bad), n, strings.Join(bad, ", "))
	}
	return n, rows.Close()
}
