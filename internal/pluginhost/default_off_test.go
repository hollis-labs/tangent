package pluginhost

import (
	"context"
	"errors"
	"testing"

	plugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/tangent/internal/pluginintent"
)

func TestScopedPluginCannotOptInOrReloadAnotherPlugin(t *testing.T) {
	h, _ := newHost(t)
	owner := ownerFixture("owner")
	if err := h.Load(owner); err != nil {
		t.Fatal(err)
	}
	var other *owningPlugin
	if err := h.RegisterFactory("other", func() (plugin.Plugin, error) { other = ownerFixture("other"); return other, nil }); err != nil {
		t.Fatal(err)
	}
	control := any(owner.handle).(interface {
		SetEnabled(context.Context, string, bool) error
		Reload(context.Context, string) error
	})
	if err := control.SetEnabled(context.Background(), "other", true); !errors.Is(err, ErrSurfaceNotHonored) {
		t.Errorf("scoped opt-in: %v", err)
	}
	if other != nil || h.DesiredEnabled("other") {
		t.Errorf("scoped opt-in changed intent or spawned: intent=%t spawned=%t", h.DesiredEnabled("other"), other != nil)
	}
	if err := h.SetEnabled(context.Background(), "other", true); err != nil {
		t.Fatal("host opt-in refused", err)
	}
	first := other
	if err := control.Reload(context.Background(), "other"); !errors.Is(err, ErrSurfaceNotHonored) {
		t.Errorf("scoped reload: %v", err)
	}
	if other != first || first.unloads.Load() != 0 {
		t.Errorf("scoped reload replaced owner: replaced=%t unloads=%d", other != first, first.unloads.Load())
	}
	if err := h.Reload(context.Background(), "other"); err != nil {
		t.Fatal(err)
	}
	if other == first || first.unloads.Load() != 1 {
		t.Fatal("host reload ineffective")
	}
	t.Cleanup(func() { _ = h.UnloadAll() })
}

func TestUnknownInstalledPluginRequiresExplicitOptIn(t *testing.T) {
	h, _ := newHost(t)
	path := pluginintent.Path(t.TempDir())
	if err := h.ConfigureIntent(path); err != nil {
		t.Fatal(err)
	}
	created := false
	if err := h.RegisterFactory("new", func() (plugin.Plugin, error) { created = true; return ownerFixture("new"), nil }); err != nil {
		t.Fatal(err)
	}
	if h.DesiredEnabled("new") || h.DesiredEnabled("unknown") {
		t.Fatal("unknown IDs default enabled")
	}
	record := h.Inventory(context.Background()).Plugins[0]
	if record.Enabled || record.Loaded || record.State != "disabled" || created {
		t.Fatal("discovery activated plugin", record)
	}
	if err := h.Reload(context.Background(), "new"); err == nil || created {
		t.Fatal("reload implicitly opted in")
	}
	if err := h.SetEnabled(context.Background(), "new", true); err != nil {
		t.Fatal(err)
	}
	if !created || !h.DesiredEnabled("new") {
		t.Fatal("explicit enable not effective")
	}
	t.Cleanup(func() { _ = h.UnloadAll() })
}

func TestExistingEnabledAndDisabledIntentIsNotMigrated(t *testing.T) {
	path := pluginintent.Path(t.TempDir())
	for id, enabled := range map[string]bool{"enabled": true, "disabled": false} {
		if _, err := pluginintent.Set(context.Background(), path, id, enabled); err != nil {
			t.Fatal(err)
		}
	}
	h, _ := newHost(t)
	if err := h.ConfigureIntent(path); err != nil {
		t.Fatal(err)
	}
	if !h.DesiredEnabled("enabled") || h.DesiredEnabled("disabled") || h.DesiredEnabled("new") {
		t.Fatal("existing intent changed")
	}
	if err := h.RegisterFactory("new", func() (plugin.Plugin, error) { return ownerFixture("new"), nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginintent.Set(context.Background(), path, "offline.sibling", true); err != nil {
		t.Fatal(err)
	}
	if err := h.SetEnabled(context.Background(), "new", false); err != nil {
		t.Fatal(err)
	}
	intent, err := pluginintent.Read(path)
	if err != nil || !intent["enabled"] || intent["disabled"] || !intent["offline.sibling"] {
		t.Fatal("host save erased sibling", intent, err)
	}
}
