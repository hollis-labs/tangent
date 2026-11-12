package secretmigrate_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	libsecrets "github.com/hollis-labs/libs/security/secrets"
	"github.com/zalando/go-keyring"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/tangent/internal/pluginconfig"
	"github.com/hollis-labs/tangent/internal/secretmigrate"
)

type fakeKeyring map[string]string

func (f fakeKeyring) Get(account string) (string, error) {
	if v, ok := f[account]; ok {
		return v, nil
	}
	return "", keyring.ErrNotFound
}

func store(t *testing.T) *libsecrets.Resolver {
	t.Helper()
	return libsecrets.New(libsecrets.WithStateDir(t.TempDir()))
}

func TestReferencesComeFromTheConfigDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE plugin_config_override(plugin TEXT, kind TEXT, scope_id TEXT, key TEXT, value TEXT, secret_ref TEXT);
		INSERT INTO plugin_config_override VALUES ('p','client','local','token','','acct-1'),('p','client','local','mode','x',''),('q','client','local','key','','acct-2')`); err != nil {
		t.Fatal(err)
	}
	refs, err := secretmigrate.References(context.Background(), path)
	if err != nil || len(refs) != 2 || refs[0].Account != "acct-1" || refs[1].Plugin != "q" {
		t.Fatalf("refs = %+v, %v", refs, err)
	}
}

func TestMoveCopiesVerifiesAndPrintsNothingSecret(t *testing.T) {
	ctx := context.Background()
	dest := store(t)
	old := fakeKeyring{"acct-1": " first-secret-value \n", "acct-2": "second"}
	refs := []secretmigrate.Ref{{Plugin: "p", Key: "token", Account: "acct-1"}, {Plugin: "q", Key: "key", Account: "acct-2"}, {Plugin: "r", Key: "gone", Account: "acct-3"}}

	dry := secretmigrate.Move(ctx, refs, old, dest, true)
	if dry[0].Status != "would move" || dry[2].Status != "missing from keychain" {
		t.Fatalf("dry run: %+v", dry)
	}
	if _, err := dest.Resolve(ctx, pluginconfig.Ref("acct-1")); err == nil {
		t.Fatal("a dry run wrote to the store")
	}

	moved := secretmigrate.Move(ctx, refs, old, dest, false)
	if moved[0].Status != "moved" || moved[0].Length != len("first-secret-value") || moved[1].Status != "moved" || moved[2].OK {
		t.Fatalf("real run: %+v", moved)
	}
	for _, it := range moved {
		if strings.Contains(it.Status, "first-secret-value") {
			t.Fatal("a status line carries a secret")
		}
	}
	m := pluginconfig.Managed{Resolver: dest}
	if got, err := m.Get(ctx, "acct-1"); err != nil || got != "first-secret-value" {
		t.Fatalf("the plugin config store reads %q, %v", got, err)
	}
	if again := secretmigrate.Move(ctx, refs[:1], old, dest, false); again[0].Status != "already present" {
		t.Fatalf("second run: %+v", again)
	}
}

func TestMoveNeverOverwritesADifferentValue(t *testing.T) {
	ctx := context.Background()
	dest := store(t)
	if err := dest.Set(ctx, pluginconfig.Ref("acct-1"), strings.NewReader("newer")); err != nil {
		t.Fatal(err)
	}
	out := secretmigrate.Move(ctx, []secretmigrate.Ref{{Plugin: "p", Key: "k", Account: "acct-1"}}, fakeKeyring{"acct-1": "older"}, dest, false)
	if out[0].OK || !strings.Contains(out[0].Status, "different value") {
		t.Fatalf("%+v", out)
	}
	if got, _ := (pluginconfig.Managed{Resolver: dest}).Get(ctx, "acct-1"); got != "newer" {
		t.Fatalf("the existing value was replaced with %q", got)
	}
}
