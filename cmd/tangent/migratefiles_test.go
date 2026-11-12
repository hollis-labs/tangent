package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
	_ "modernc.org/sqlite"
)

type keychain map[string]string

func (k keychain) Get(account string) (string, error) {
	if v, ok := k[account]; ok {
		return v, nil
	}
	return "", keyring.ErrNotFound
}

func TestMigrateSecretsDryRunThenMove(t *testing.T) {
	data := t.TempDir()
	cfgDir := filepath.Join(data, "plugins", ".state", "config")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(cfgDir, "config.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE plugin_config_override(plugin TEXT, kind TEXT, scope_id TEXT, key TEXT, value TEXT, secret_ref TEXT);
		INSERT INTO plugin_config_override VALUES ('torque','client','local','api','','acct-9')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	secret := "pretend-keychain" + "-value"
	src := keychain{"acct-9": secret}
	ctx := context.Background()

	var out bytes.Buffer
	if err = runMigrateSecrets(ctx, []string{"--to", data, "--dry-run"}, &out, src); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "would move") {
		t.Fatalf("dry run output:\n%s", out.String())
	}
	if _, err = os.Stat(filepath.Join(data, "secrets")); err == nil {
		t.Fatal("a dry run created the secrets store")
	}

	out.Reset()
	if err = runMigrateSecrets(ctx, []string{"--to", data}, &out, src); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), secret) {
		t.Fatal("the output carries the secret value")
	}
	got, err := os.ReadFile(filepath.Join(data, "secrets", "tangent-plugin-config", "acct-9")) // #nosec G304 -- a file in this test's temp directory.
	if err != nil || strings.TrimSpace(string(got)) != secret {
		t.Fatalf("stored file: %q, %v", got, err)
	}
	if err = runMigrateSecrets(ctx, []string{"--to", t.TempDir()}, &out, src); err == nil {
		t.Fatal("a data directory with no plugin configuration database was accepted")
	}
}
