package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
	manifest "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
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
	snap := saveOwnerConfig(t, s, "bad", pluginconfig.Changes{Set: map[string]any{"token": "synthetic-opaque-config-fixture"}})
	bad := ownerFixture("bad")
	bad.loadFailure = errors.New("synthetic-opaque-config-fixture")
	err := h.Load(bad)
	if err == nil || strings.Contains(err.Error(), "synthetic-opaque-config-fixture") {
		t.Fatal("load diagnostic leaked config")
	}
	after, err := s.Read(context.Background(), "bad", s.Scopes()[0])
	if err != nil || !after.PendingRestart || after.Revision != snap.Revision {
		t.Fatal("failed load marked applied", err)
	}
	raw, err := json.Marshal(h.Inventory(context.Background()))
	if err != nil || strings.Contains(string(raw), "synthetic-opaque-config-fixture") {
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
	if err = h.ApplyConfig(context.Background(), "one", old.Revision); !errors.Is(err, pluginconfig.ErrConflict) {
		t.Fatal("stale apply accepted", err)
	}
	if _, err = original.handle.GetConfig("channel"); err != nil {
		t.Fatal("stale apply stopped current owner", err)
	}
	if err = h.ApplyConfig(context.Background(), "one", current.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = original.handle.GetConfig("channel"); err == nil {
		t.Fatal("old handle retained")
	}
	after, err := s.Read(context.Background(), "one", s.Scopes()[0])
	if err != nil || after.PendingRestart {
		t.Fatal("successful load not applied", err)
	}
}

func TestChildConfigIsFreshPerIncarnationAndRedacted(t *testing.T) {
	h, s := ownerConfigFixture(t)
	id := "tangent.plugin.echo"
	registerOwnerConfig(t, s, id)
	saveOwnerConfig(t, s, id, pluginconfig.Changes{Set: map[string]any{"token": "synthetic-literal-runtime-key"}})
	binary := buildEchoPlugin(t)
	factory := func() (sdk.Plugin, error) {
		spec := echoSpec(t, binary)
		spec.Env = append(spec.Env, "ECHO_PLUGIN_REPORT_CONFIG_STDERR=1", "ECHO_PLUGIN_HEALTH=synthetic-literal-runtime-key")
		spec.ResolveConfig = h.ResolveConfiguration(id)
		spec.ConfigApplied = func(ctx context.Context, revision string) error { return s.MarkApplied(ctx, id, revision) }
		return NewChildPlugin(spec, []MCPTool{{Name: "tangent.echo", Description: "Config fixture", InputSchema: json.RawMessage(`{"type":"object"}`)}}, nil, WithHealthGate(false)), nil
	}
	if err := h.RegisterFactory(id, factory); err != nil {
		t.Fatal(err)
	}
	if err := h.SetEnabled(context.Background(), id, true); err != nil {
		t.Fatal(err)
	}
	read := func() (*ChildPlugin, capability.RuntimeIdentity, string) {
		t.Helper()
		loaded, ok := h.GetPlugin(id)
		if !ok {
			t.Fatal("missing child")
		}
		child := loaded.(*ChildPlugin)
		result, err := child.MCPCallTool(context.Background(), subprocess.MCPCallRequest{ToolName: "tangent.echo"})
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Channel     string                     `json:"configured_channel"`
			Token       bool                       `json:"configured_token_present"`
			Incarnation capability.RuntimeIdentity `json:"incarnation"`
		}
		if err = json.Unmarshal(result.Content, &got); err != nil {
			t.Fatal(err)
		}
		if !got.Token {
			t.Fatal("declared secret absent from Init")
		}
		return child, got.Incarnation, got.Channel
	}
	child, initial, channel := read()
	if channel != "original" {
		t.Fatal("wrong initial config")
	}
	saveOwnerConfig(t, s, id, pluginconfig.Changes{Set: map[string]any{"channel": "replacement"}})
	_, same, channel := read()
	if channel != "original" || same != initial {
		t.Fatal("save mutated current instance")
	}
	raw, err := json.Marshal(h.Inventory(context.Background()))
	if err != nil || strings.Contains(string(raw), "synthetic-literal-runtime-key") {
		t.Fatal("health inventory leaked runtime secret")
	}
	if strings.Contains(child.process.Diagnostics(), "synthetic-literal-runtime-key") {
		t.Fatal("stderr tail leaked runtime secret")
	}
	firstPID := child.pid()
	if err = killCurrent(child); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && (child.pid() == 0 || child.pid() == firstPID || !child.Status().Loaded) {
		time.Sleep(10 * time.Millisecond)
	}
	if child.pid() == 0 || child.pid() == firstPID || !child.Status().Loaded {
		t.Fatal("recovery did not activate")
	}
	_, recovered, channel := read()
	if recovered == initial || channel != "replacement" {
		t.Fatal("retry reused old config or incarnation")
	}
	snap, err := s.Read(context.Background(), id, s.Scopes()[0])
	if err != nil || snap.PendingRestart {
		t.Fatal("recovered activation not applied", err)
	}
	saved := saveOwnerConfig(t, s, id, pluginconfig.Changes{Set: map[string]any{"channel": "explicit"}})
	if err = h.ApplyConfig(context.Background(), id, saved.Revision); err != nil {
		t.Fatal(err)
	}
	_, next, channel := read()
	if next == recovered || channel != "explicit" {
		t.Fatal("explicit apply reused snapshot")
	}
}

func TestMissingRequiredConfigRefusesBeforeChildSpawn(t *testing.T) {
	h, s := ownerConfigFixture(t)
	id := "tangent.plugin.echo"
	if err := s.Register(context.Background(), id, manifest.Config{Secrets: map[string]manifest.Secret{"token": {Required: true}}}); err != nil {
		t.Fatal(err)
	}
	spec := echoSpec(t, filepath.Join(t.TempDir(), "not-an-executable"))
	spec.ResolveConfig = h.ResolveConfiguration(id)
	child := NewChildPlugin(spec, nil, nil)
	if err := h.Load(child); !errors.Is(err, pluginconfig.ErrRefused) {
		t.Fatal("missing declared config did not refuse before execution", err)
	}
	if child.lifecycle != nil || child.pid() != 0 {
		t.Fatal("driver started before required input validation")
	}
	if _, ok := h.GetPlugin(id); ok {
		t.Fatal("refused child still loaded")
	}
}

func TestCanceledApplyDoesNotOwnLifecycleGate(t *testing.T) {
	h, _ := ownerConfigFixture(t)
	h.ops.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- h.ApplyConfig(ctx, "one", "revision") }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled apply waited for lifecycle gate")
	}
	h.ops.Unlock()
}

func TestConfigRedactionKeepsRawCauseBoundariesUntilDisplay(t *testing.T) {
	const fixtureValue = "owned-config-value"
	raw := scrubRawConfigText("TOKEN=S3CR01\nIMPORTANT CAUSE: failed "+secret+"\nFINAL CAUSE: schema migration failed", []string{fixtureValue})
	if strings.Contains(raw, fixtureValue) || strings.Contains(raw, "S3CR01") || !strings.Contains(raw, "\nIMPORTANT CAUSE") {
		t.Fatalf("raw secret scrub lost cause boundaries: %q", raw)
	}
	display := scrubConfigText("plugin/init refused\nplugin stderr:\n"+raw, []string{fixtureValue})
	if strings.Contains(display, fixtureValue) || strings.Contains(display, "S3CR01") || strings.Contains(display, "\n") || !strings.Contains(display, "IMPORTANT CAUSE") || !strings.Contains(display, "FINAL CAUSE: schema migration failed") {
		t.Fatalf("unsafe or truncated display: %q", display)
	}
}
