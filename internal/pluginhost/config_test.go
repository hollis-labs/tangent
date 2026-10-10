package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	manifest "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/tangent/internal/pluginconfig"
)

type configKeys struct {
	mu     sync.Mutex
	values map[string]string
}

func (k *configKeys) Get(_ context.Context, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.values[key]
	if !ok {
		return "", errors.New("missing fixture")
	}
	return v, nil
}
func (k *configKeys) Set(_ context.Context, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.values[key] = value
	return nil
}
func (k *configKeys) Delete(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.values, key)
	return nil
}
func ownerConfigFixture(t *testing.T) (*Host, *pluginconfig.Store) {
	t.Helper()
	h, _ := newHost(t)
	keys := &configKeys{values: map[string]string{}}
	s, err := pluginconfig.Open(context.Background(), filepath.Join(t.TempDir(), "config"), keys, []pluginconfig.Scope{{Kind: "client", ID: "test-owner"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.UnloadAll(); _ = s.Close() })
	if err = h.ConfigureConfig(s); err != nil {
		t.Fatal(err)
	}
	return h, s
}
func registerOwnerConfig(t *testing.T, s *pluginconfig.Store, id string) {
	t.Helper()
	if err := s.Register(context.Background(), id, manifest.Config{Fields: map[string]manifest.Field{"channel": {Type: "string", Default: "original"}}, Secrets: map[string]manifest.Secret{"token": {}}}); err != nil {
		t.Fatal(err)
	}
}
func saveOwnerConfig(t *testing.T, s *pluginconfig.Store, id string, changes pluginconfig.Changes) pluginconfig.Snapshot {
	t.Helper()
	snap, err := s.Read(context.Background(), id, s.Scopes()[0])
	if err != nil {
		t.Fatal(err)
	}
	next, v, err := s.Save(context.Background(), id, s.Scopes()[0], snap.Revision, changes)
	if err != nil || !v.Valid {
		t.Fatal("save refused", err)
	}
	return next
}

func TestScopedConfigSnapshotAndStaleOwnerRefusal(t *testing.T) {
	h, s := ownerConfigFixture(t)
	registerOwnerConfig(t, s, "one")
	registerOwnerConfig(t, s, "two")
	saveOwnerConfig(t, s, "one", pluginconfig.Changes{Set: map[string]any{"token": "synthetic-config-secret"}})
	one, two := ownerFixture("one"), ownerFixture("two")
	if err := h.Load(one); err != nil {
		t.Fatal(err)
	}
	if err := h.Load(two); err != nil {
		t.Fatal(err)
	}
	if _, err := h.GetConfig("channel"); !errors.Is(err, ErrSurfaceNotHonored) {
		t.Fatal("global host gained config authority")
	}
	if value, err := one.handle.GetConfig("token"); err != nil || value != "synthetic-config-secret" {
		t.Fatal("own declared runtime secret unavailable", err)
	}
	if _, err := two.handle.GetConfig("token"); err == nil {
		t.Fatal("other owner acquired secret")
	}
	if err := one.handle.SetConfig("undeclared", "input"); err == nil {
		t.Fatal("undeclared setting created")
	}
	if err := one.handle.SetConfig("channel", "next"); err != nil {
		t.Fatal(err)
	}
	if value, err := one.handle.GetConfig("channel"); err != nil || value != "original" {
		t.Fatal("running snapshot mutated", err)
	}
	if err := one.handle.RegisterConfigSchema([]sdk.ConfigFieldDef{{Key: "channel", Type: "string", Default: "original"}}); err != nil {
		t.Fatal(err)
	}
	if err := one.handle.RegisterConfigSchema([]sdk.ConfigFieldDef{{Key: "unexpected", Type: "secret"}}); err == nil {
		t.Fatal("runtime schema widened")
	}
	if err := h.Unload("one"); err != nil {
		t.Fatal(err)
	}
	replacement := ownerFixture("one")
	if err := h.Load(replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := one.handle.GetConfig("token"); err == nil {
		t.Fatal("stale read")
	}
	if err := one.handle.SetConfig("channel", "stale"); err == nil {
		t.Fatal("stale write")
	}
	if err := one.handle.RegisterConfigSchema(nil); err == nil {
		t.Fatal("stale registration")
	}
	if value, err := replacement.handle.GetConfig("channel"); err != nil || value != "next" {
		t.Fatal("replacement snapshot missing", err)
	}
}
func TestFailedRegistrationKeepsConfigPendingAndScrubsLoadDiagnostic(t *testing.T) {
	h, s := ownerConfigFixture(t)
	registerOwnerConfig(t, s, "bad")
	snap := saveOwnerConfig(t, s, "bad", pluginconfig.Changes{Set: map[string]any{"token": "literal-opaque-config-fixture"}})
	bad := ownerFixture("bad")
	bad.loadFailure = errors.New("literal-opaque-config-fixture")
	err := h.Load(bad)
	if err == nil || strings.Contains(err.Error(), "literal-opaque-config-fixture") {
		t.Fatal("load diagnostic leaked config")
	}
	after, err := s.Read(context.Background(), "bad", s.Scopes()[0])
	if err != nil || !after.PendingRestart || after.Revision != snap.Revision {
		t.Fatal("failed load marked applied", err)
	}
	raw, err := json.Marshal(h.Inventory(context.Background()))
	if err != nil || strings.Contains(string(raw), "literal-opaque-config-fixture") {
		t.Fatal("inventory leaked config", err)
	}
}
func TestApplyConfigRefusesStaleRevisionBeforeReplacingOwner(t *testing.T) {
	h, s := ownerConfigFixture(t)
	registerOwnerConfig(t, s, "one")
	original := ownerFixture("one")
	if err := h.RegisterFactory("one", func() (sdk.Plugin, error) { return ownerFixture("one"), nil }); err != nil {
		t.Fatal(err)
	}
	if err := h.Load(original); err != nil {
		t.Fatal(err)
	}
	old, err := s.Read(context.Background(), "one", s.Scopes()[0])
	if err != nil {
		t.Fatal(err)
	}
	current := saveOwnerConfig(t, s, "one", pluginconfig.Changes{Set: map[string]any{"channel": "applied"}})
	if err := h.ApplyConfig(context.Background(), "one", old.Revision); !errors.Is(err, pluginconfig.ErrConflict) {
		t.Fatal("stale apply accepted", err)
	}
	if _, err := original.handle.GetConfig("channel"); err != nil {
		t.Fatal("stale apply stopped current owner", err)
	}
	if err := h.ApplyConfig(context.Background(), "one", current.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := original.handle.GetConfig("channel"); err == nil {
		t.Fatal("old handle retained")
	}
	after, err := s.Read(context.Background(), "one", s.Scopes()[0])
	if err != nil || after.PendingRestart {
		t.Fatal("successful load not applied", err)
	}
}
