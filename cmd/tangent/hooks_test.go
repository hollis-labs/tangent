package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tangent/internal/persistence"
)

// bootWith starts every manifest module with the app's boot hooks. mutate may edit the generated
// config first (the tests use it to bind a free loopback port).
func bootWith(ctx context.Context, getenv func(string) string, mutate func(map[string]any)) (*boot.Running, error) {
	cfg := map[string]any{}
	if err := json.Unmarshal([]byte(substrateConfigJSON), &cfg); err != nil {
		return nil, err
	}
	if mutate != nil {
		mutate(cfg)
	}
	raw, _ := json.Marshal(cfg)
	fsys, root := persistence.Migrations()
	database.UseMigrationFS(fsys, root)
	return boot.StartWith(ctx, substrateApp, substrateModules, string(raw), bootOptions(getenv, true, substrateModules))
}

// testOwnerPassword is the passphrase of the owner every started stack has. #nosec G101 -- a
// throwaway passphrase for an owner that exists only in a test's temporary data directory.
const testOwnerPassword = "a long test passphrase"

// started boots the real stack on a temp data directory and serves the full handler chain, plus
// mounts, on a free loopback port. It returns the base URL and the app. serve's ctx is
// cancelled and joined on cleanup.
func started(t *testing.T, mounts func(ctx context.Context, tg *tangent) []mountSpec) (string, *tangent) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	env := map[string]string{"TANGENT_DATA_DIR": dir}
	override := filepath.Join(t.TempDir(), "override.yaml")
	if err := os.WriteFile(override, []byte("identity:\n  bcrypt_cost: 4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", override)
	ctx, cancel := context.WithCancel(context.Background())
	run, err := bootWith(ctx, func(k string) string { return env[k] }, func(cfg map[string]any) {
		cfg["httphost"] = map[string]any{"addr": "127.0.0.1:0"}
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	// The owner is made the way production makes it: offline, on the host, before the server
	// serves (identity.Service.SetupOwner is what `tangent identity setup` calls). POST /api/setup
	// is always refused.
	if _, err = identity.From(run.App).SetupOwner(ctx, identity.OwnerInput{Username: "owner", DisplayName: "Owner", Password: testOwnerPassword}); err != nil {
		cancel()
		_ = run.Stop(context.Background())
		t.Fatalf("create the owner: %v", err)
	}
	addr := make(chan net.Addr, 1)
	tg := &tangent{app: run.App, ready: func(a net.Addr) { addr <- a }}
	var extra []mountSpec
	if mounts != nil {
		extra = mounts(ctx, tg)
	}
	done := make(chan error, 1)
	go func() { done <- tg.serveWith(ctx, extra) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve ended with %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("serve did not stop on cancel")
		}
		_ = run.Stop(context.Background())
	})
	select {
	case a := <-addr:
		return "http://" + a.String(), tg
	case err := <-done:
		t.Fatalf("serve returned before listening: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("serve never listened")
	}
	return "", nil
}
