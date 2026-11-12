package pluginconfig_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	libsecrets "github.com/hollis-labs/libs/security/secrets"

	"github.com/hollis-labs/tangent/internal/pluginconfig"
)

func managed(t *testing.T) (pluginconfig.Managed, string) {
	t.Helper()
	dir := t.TempDir()
	return pluginconfig.Managed{Resolver: libsecrets.New(libsecrets.WithStateDir(dir))}, dir
}

func TestManagedRoundTripsThroughAPrivateFile(t *testing.T) {
	m, dir := managed(t)
	ctx := context.Background()
	if err := m.Set(ctx, "acct-1", "  value-one \n"); err != nil {
		t.Fatal(err)
	}
	if got, err := m.Get(ctx, "acct-1"); err != nil || got != "value-one" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	path := filepath.Join(dir, pluginconfig.ManagedNamespace, "acct-1")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("secret file mode = %v, want 0600", info.Mode().Perm())
	}
	if err := m.Delete(ctx, "acct-1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "acct-1"); err != nil {
		t.Fatalf("deleting a missing entry must not fail: %v", err)
	}
	if _, err := m.Get(ctx, "acct-1"); !errors.Is(err, pluginconfig.ErrSecret) {
		t.Fatalf("Get after delete = %v, want ErrSecret", err)
	}
}

func TestManagedRefusesAccountsThatAreNotPlainNames(t *testing.T) {
	m, _ := managed(t)
	for _, account := range []string{"", "../escape", "a/b", `a\b`, ".hidden"} {
		if err := m.Set(context.Background(), account, "v"); !errors.Is(err, pluginconfig.ErrSecret) {
			t.Errorf("Set(%q) = %v, want ErrSecret", account, err)
		}
	}
	if _, err := (pluginconfig.Managed{}).Get(context.Background(), "x"); !errors.Is(err, pluginconfig.ErrSecret) {
		t.Errorf("a Managed without a resolver must fail closed, got %v", err)
	}
}
