package health

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestPluginFailureDetailKeepsBothLongNameGroups(t *testing.T) {
	inv := PluginInventory{Refused: 5, Loaded: 5}
	for index := range 5 {
		inv.Plugins = append(inv.Plugins, PluginRecord{ID: filepath.Join("/very/long/installation/path", strings.Repeat("path-", 10), fmt.Sprintf("refused-%d", index)), Error: "refusal"})
		inv.Plugins = append(inv.Plugins, PluginRecord{ID: filepath.Join("/another/long/path", fmt.Sprintf("crashed-%d", index)), Loaded: true, FailedAfterLoad: true})
	}
	reporter := &Reporter{plugins: func(context.Context) PluginInventory { return inv }}
	check := reporter.checkPlugins(context.Background())
	if check.Status != StatusFail || !strings.Contains(check.Detail, "refused-0") || !strings.Contains(check.Detail, "crashed-0") || !strings.Contains(check.Detail, "failed after load:") || !strings.Contains(check.Detail, "more)") || len(check.Detail) > maxDetailBytes {
		t.Fatal(check)
	}
	if strings.Contains(check.Detail, "/very/long") || strings.Contains(check.Detail, "/another/long") {
		t.Fatal("paths consumed group budget", check.Detail)
	}
}

func TestLongPluginNamesKeepDistinctSuffixesAndNoStraySpace(t *testing.T) {
	first := strings.Repeat("same-prefix-", 4) + "refused-alpha"
	second := strings.Repeat("same-prefix-", 4) + "refused-bravo"
	text := pluginNamesForBudget([]string{first, second}, 100)
	if !strings.Contains(text, "refused-alpha") || !strings.Contains(text, "refused-bravo") || len(text) > 100 {
		t.Fatal(text)
	}
	if text := pluginNamesForBudget([]string{first}, 1); text != "(+1 more)" {
		t.Fatalf("empty name list has stray space: %q", text)
	}
	unicodeName := strings.Repeat("é", 20) + "failure"
	if text := pluginNamesForBudget([]string{unicodeName}, 40); !utf8.ValidString(text) || !strings.HasSuffix(text, "failure") {
		t.Fatal(text)
	}
}
