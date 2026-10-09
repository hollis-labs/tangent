package boot

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/registry"
	"github.com/hollis-labs/tangent/internal/pluginhost"
)

func TestRegistryUsesLifecycleHostEpoch(t *testing.T) {
	root := t.TempDir()
	_, srv, closer, err := Boot(Config{DBPath: filepath.Join(root, "tangent.db"), PluginDir: filepath.Join(root, "plugins"), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer.Close() })
	epoch := closer.(*ownedCloser).plugins.HostInstance()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-done
	})
	client := &http.Client{Timeout: time.Second}
	for i := 0; i < 2; i++ {
		resp, err := client.Get("http://" + ln.Addr().String() + "/api/plugins/registry")
		if err != nil {
			t.Fatal(err)
		}
		var got registry.Response
		decodeErr := json.NewDecoder(resp.Body).Decode(&got)
		_ = resp.Body.Close()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if resp.StatusCode != http.StatusOK || got.HostInstance != epoch || got.Revision != 1 || got.Validate() != nil {
			t.Fatalf("registry epoch differs from lifecycle: %+v", got)
		}
	}
}

func TestPluginInventoryPreservesRuntimeFailureAndExhaustion(t *testing.T) {
	source := pluginhost.PluginInventory{Loaded: 1, Plugins: []pluginhost.PluginRecord{{ID: "crashed", Loaded: true, FailedAfterLoad: true, RuntimeError: "spawn failed", Exhausted: true, HealthMessage: "plugin is gone; restart attempts exhausted"}}}
	got := pluginInventory(source).Plugins[0]
	if !got.Exhausted || !got.FailedAfterLoad || got.RuntimeError != "spawn failed" || got.HealthMessage != source.Plugins[0].HealthMessage {
		t.Fatalf("runtime diagnostics dropped: %+v", got)
	}
}
