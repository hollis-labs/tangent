package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tangent/internal/envelope"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/pluginhost"
	"github.com/hollis-labs/tangent/internal/room"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type runtimeToolPlugin struct{ marker string }

func (*runtimeToolPlugin) ID() string               { return "runtime" }
func (*runtimeToolPlugin) Name() string             { return "Runtime fixture" }
func (*runtimeToolPlugin) Version() string          { return "1.0.0" }
func (*runtimeToolPlugin) Description() string      { return "Fixture" }
func (*runtimeToolPlugin) Dependencies() []string   { return nil }
func (*runtimeToolPlugin) Status() sdk.PluginStatus { return sdk.PluginStatus{} }
func (*runtimeToolPlugin) Unload() error            { return nil }
func (p *runtimeToolPlugin) Load(h sdk.Host) error {
	return h.(interface {
		RegisterMCPTool(pluginhost.MCPTool) error
	}).RegisterMCPTool(pluginhost.MCPTool{
		Name: "tangent.runtime-fixture", InputSchema: json.RawMessage(`{"type":"object","required":["text"],"properties":{"text":{"type":"string"}},"additionalProperties":false}`),
		Handler: pluginToolFunc(func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
			return subprocess.MCPCallResult{Content: json.RawMessage(`{"marker":"` + p.marker + `"}`)}, nil
		}),
	})
}

func TestRuntimePluginToolsDisappearAndReloadOnConnectedClient(t *testing.T) {
	ctx := context.Background()
	svc, err := envelope.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	host, err := pluginhost.New(ctx, nil, svc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.UnloadAll() })
	srv, err := tangentmcp.New(svc, envelope.NewDispatcher(svc), room.NewManager(nil), "")
	if err != nil {
		t.Fatal(err)
	}
	if err = host.AttachToolRegistry(srv); err != nil {
		t.Fatal(err)
	}
	st, ct := mcpsdk.NewInMemoryTransports()
	ss, err := srv.MCP().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "runtime-test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	assertListed := func(want bool) {
		t.Helper()
		list, e := client.ListTools(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, tool := range list.Tools {
			if tool.Name == "tangent.runtime-fixture" {
				found = true
			}
		}
		if found != want {
			t.Fatalf("runtime tool listed=%v want%v", found, want)
		}
	}
	if err = host.Load(&runtimeToolPlugin{marker: "first"}); err != nil {
		t.Fatal(err)
	}
	assertListed(true)
	bad, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "tangent.runtime-fixture", Arguments: map[string]any{"text": 123}})
	if err != nil || !bad.IsError {
		t.Fatalf("new runtime schema not enforced: %+v %v", bad, err)
	}
	if err = host.Unload("runtime"); err != nil {
		t.Fatal(err)
	}
	assertListed(false)
	if err = host.Load(&runtimeToolPlugin{marker: "second"}); err != nil {
		t.Fatal(err)
	}
	assertListed(true)
	reply, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "tangent.runtime-fixture", Arguments: map[string]any{"text": "ok"}})
	if err != nil || reply.IsError {
		t.Fatalf("replacement dispatch: %+v %v", reply, err)
	}
	data, _ := json.Marshal(reply)
	if !strings.Contains(string(data), "second") {
		t.Fatalf("wrong owner reply: %s", data)
	}
}
