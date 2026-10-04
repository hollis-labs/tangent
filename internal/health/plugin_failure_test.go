package health

import (
	"context"
	"strings"
	"testing"
)

func TestRuntimeFailureAndInitialRefusalAreNamedSeparately(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		inv := PluginInventory{Loaded: 1, Plugins: []PluginRecord{{ID: "crashed", Loaded: true, FailedAfterLoad: true, RuntimeError: "spawn failed"}}}
		if mixed {
			inv.Refused = 1
			inv.Plugins = append(inv.Plugins, PluginRecord{ID: "refused", Error: "protocol 1"})
		}
		reporter := NewReporter(WithPlugins(func(context.Context) PluginInventory { return inv }))
		check := reporter.checkPlugins(context.Background())
		if check.Status != StatusFail || !strings.Contains(check.Detail, "1 plugin(s) failed after load: crashed") {
			t.Fatalf("runtime failure: %+v", check)
		}
		if mixed && !strings.Contains(check.Detail, "1 plugin(s) refused to load: refused;") {
			t.Fatalf("refusal names mixed with runtime failure: %+v", check)
		}
	}
}
