package filemigrate_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hollis-labs/tangent/internal/filemigrate"
)

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil { // #nosec G302,G306 -- modes under test, in a temp directory.
		t.Fatal(err)
	}
}

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// oldTree builds a pre-Substrate directory and a new database whose digests describe its artifacts.
func oldTree(t *testing.T) (from, to, db string) {
	t.Helper()
	root := t.TempDir()
	from, to = filepath.Join(root, "old"), filepath.Join(root, "new")
	write(t, filepath.Join(from, "artifacts", "i1", "1", "a.txt"), "one", 0o640)
	write(t, filepath.Join(from, "artifacts", "i1", "2", "a.txt"), "two", 0o640)
	write(t, filepath.Join(from, "plugins", "p", "bin"), "#!/bin/sh\n", 0o750)
	write(t, filepath.Join(from, "plugins", ".state", "config", "config.sqlite"), "state", 0o600)
	if err := os.Chmod(filepath.Join(from, "plugins", ".state", "config"), 0o700); err != nil { // #nosec G302 -- a directory.
		t.Fatal(err)
	}
	db = filepath.Join(root, "new.db")
	conn, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.Exec(`CREATE TABLE artifact_revisions(rel_path TEXT, sha256 TEXT)`); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{"i1/1/a.txt": "one", "i1/2/a.txt": "two"} {
		if _, err = conn.Exec("INSERT INTO artifact_revisions VALUES (?, ?)", rel, sum(body)); err != nil {
			t.Fatal(err)
		}
	}
	return from, to, db
}

func snapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		info, _ := d.Info()
		body, _ := os.ReadFile(p) // #nosec G304,G122 -- a path under this test's temp directory.
		b.WriteString(p + " " + info.Mode().String() + " " + info.ModTime().String() + " " + string(body) + "\n")
		return err
	})
	return b.String()
}

func TestCopyMovesBothTreesVerifiedAndLeavesTheOldOneUntouched(t *testing.T) {
	from, to, db := oldTree(t)
	before := snapshot(t, from)
	rep, err := filemigrate.Copy(context.Background(), filemigrate.Options{From: from, To: to, Database: db})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Trees["artifacts"].Files != 2 || rep.Trees["plugins"].Files != 2 || rep.Revisions != 2 {
		t.Fatalf("report %+v", rep)
	}
	got, _ := os.ReadFile(filepath.Join(to, "artifacts", "i1", "2", "a.txt")) // #nosec G304 -- a path under this test's temp directory.
	if string(got) != "two" {
		t.Fatalf("copied body %q", got)
	}
	info, err := os.Stat(filepath.Join(to, "plugins", ".state", "config"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("plugin config directory mode %v, %v: it must stay 0700", info, err)
	}
	if after := snapshot(t, from); after != before {
		t.Fatalf("the old tree changed:\n%s\n---\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(to, "artifacts.migrating")); err == nil {
		t.Fatal("a staging directory was left behind")
	}
}

func TestCopyRefusesANonEmptyTarget(t *testing.T) {
	from, to, db := oldTree(t)
	write(t, filepath.Join(to, "plugins", "x"), "keep", 0o600)
	if _, err := filemigrate.Copy(context.Background(), filemigrate.Options{From: from, To: to, Database: db}); err == nil {
		t.Fatal("copied over a non-empty plugins directory")
	}
	if _, err := os.Stat(filepath.Join(to, "artifacts")); err == nil {
		t.Fatal("artifacts were installed although the run was refused")
	}
}

func TestCopyInstallsNothingWhenAnArtifactDoesNotMatchTheDatabase(t *testing.T) {
	from, to, db := oldTree(t)
	write(t, filepath.Join(from, "artifacts", "i1", "2", "a.txt"), "tampered", 0o640)
	_, err := filemigrate.Copy(context.Background(), filemigrate.Options{From: from, To: to, Database: db})
	if err == nil || !strings.Contains(err.Error(), "i1/2/a.txt") {
		t.Fatalf("err = %v, want the mismatching revision named", err)
	}
	for _, name := range []string{"artifacts", "plugins", "artifacts.migrating", "plugins.migrating"} {
		if _, statErr := os.Stat(filepath.Join(to, name)); statErr == nil {
			t.Fatalf("%s exists after a failed verification", name)
		}
	}
}

func TestCopyRefusesLinksAndNestedDirectories(t *testing.T) {
	from, to, db := oldTree(t)
	if err := os.Symlink("/etc/hostname", filepath.Join(from, "plugins", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := filemigrate.Copy(context.Background(), filemigrate.Options{From: from, To: to, Database: db}); err == nil {
		t.Fatal("a symlink was copied")
	}
	if _, err := filemigrate.Copy(context.Background(), filemigrate.Options{From: from, To: filepath.Join(from, "inside")}); err == nil {
		t.Fatal("a target inside the source was accepted")
	}
}
