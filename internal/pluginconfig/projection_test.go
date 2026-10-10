package pluginconfig

import (
	"context"
	"testing"

	sdk "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
)

func TestProjectionUsesTypedDefaultsAndWriteOnlySecretPresence(t *testing.T) {
	s, _, _ := testStore(t)
	register(t, s)
	group, err := s.Group(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	properties := group.Schema["properties"].(map[string]any)
	if properties["enabled"].(map[string]any)["default"] != false || properties["limit"].(map[string]any)["default"] != int64(0) {
		t.Fatal("SDK strings not converted to typed defaults")
	}
	token := properties["token"].(map[string]any)
	if token["writeOnly"] != true || token["default"] != nil {
		t.Fatal("secret projection lent value")
	}
	if err = s.Register(context.Background(), "unsafe", sdk.Config{Fields: map[string]sdk.Field{"integer": {Type: "integer", Default: "9007199254740992"}}}); err == nil {
		t.Fatal("unsafe JS integer projected")
	}
}
