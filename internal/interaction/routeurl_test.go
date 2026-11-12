package interaction

import (
	"errors"
	"testing"
)

func TestValidPluginRoutePath(t *testing.T) {
	for _, good := range []string{
		"/api/plugins/example-review/action", "/api/plugins/tangent.plugin.torque/board/sync", "/api/plugins/a/b.c/d_e-f",
	} {
		if !ValidPluginRoutePath(good) {
			t.Errorf("refused %q", good)
		}
	}
	for _, bad := range []string{
		"", "/api/plugins/x", "/api/plugins/x/", "/api/plugins/x/a/../b", "/api/plugins/x/a/./b", "/api/plugins/x/../a", "/api/plugins/../a",
		"/api/plugins/x//a", "/api/plugins/x/a%2e%2e/b", "/api/plugins/x/a?q", "/api/plugins/x/a#f", "/api/plugins/x/a b",
		`/api/plugins/x/a\b`, "/api/plugins/x/.a", "/api/inbox/items/i/archive", "https://example.com/api/plugins/x/a",
		"/api/plugins/x/A", "/api/plugins/x/a/",
	} {
		if ValidPluginRoutePath(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestRefuseUnsafeRouteURLsByKind(t *testing.T) {
	bad := map[string]any{"action_url": "/api/plugins/x/a/../../../hitl/items/i/resolve", "state_url": "/api/plugins/x/state"}
	if err := refuseUnsafeRouteURLs("tangent.external-review", bad); !errors.Is(err, ErrDefinitionValidation) {
		t.Fatalf("external-review: %v", err)
	}
	board := map[string]any{"sync": map[string]any{"endpoint": "/api/plugins/x/../../logout"}}
	if err := refuseUnsafeRouteURLs("tangent.app-board", board); !errors.Is(err, ErrDefinitionValidation) {
		t.Fatalf("app-board: %v", err)
	}
	if err := refuseUnsafeRouteURLs("tangent.app-board", map[string]any{"cards": []any{}}); err != nil {
		t.Fatalf("a board with no sync block: %v", err)
	}
	if err := refuseUnsafeRouteURLs("tangent.hitl-item", bad); err != nil {
		t.Fatalf("another kind is not governed: %v", err)
	}
}
