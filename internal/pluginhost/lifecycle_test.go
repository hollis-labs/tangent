package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	plugin "github.com/hollis-labs/plugin-sdk"
	"github.com/hollis-labs/plugin-sdk/subprocess"
)

// These tests hold CW-20260910-0036: the lifecycle contract, the failure
// isolation boundary, and the inventory that makes a plugin legible in a health
// report. Each one is a sentence in lifecycle.go or isolation.go, held so the
// next change cannot quietly cross it.

// lifecyclePlugin is a plugin that records whether Unload was called and can be
// told to misbehave on the way out.
type lifecyclePlugin struct {
	id        string
	deps      []string
	unloaded  bool
	unloadErr error
	panicOn   string
	status    plugin.PluginStatus
}

func (p *lifecyclePlugin) ID() string {
	if p.panicOn == "ID" {
		panic("plugin panicked naming itself")
	}
	return p.id
}
func (p *lifecyclePlugin) Name() string {
	if p.panicOn == "Name" {
		panic("plugin panicked describing itself")
	}
	return p.id
}
func (p *lifecyclePlugin) Version() string        { return "0.0.1" }
func (p *lifecyclePlugin) Description() string    { return "lifecycle stub" }
func (p *lifecyclePlugin) Dependencies() []string { return p.deps }

func (p *lifecyclePlugin) Load(plugin.Host) error {
	if p.panicOn == "Load" {
		panic("plugin panicked loading")
	}
	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now().UTC()}
	return nil
}

func (p *lifecyclePlugin) Unload() error {
	if p.panicOn == "Unload" {
		panic("plugin panicked unloading")
	}
	p.unloaded = true
	p.status = plugin.PluginStatus{}
	return p.unloadErr
}

func (p *lifecyclePlugin) Status() plugin.PluginStatus { return p.status }

// TestUnloadRemovesNothingThePluginRegistered is the lifecycle contract itself.
//
// It is the most important test in this file, because the behavior it pins
// looks like a bug to a reader who has not read lifecycle.go. go-envelopes has
// no registry removal and the SDK passes no caller identity to a registration
// call, so a host that appeared to unregister would be reporting a registry
// state that is not the one in force — which is what the first host's
// load-failure cleanup path did.
func TestUnloadRemovesNothingThePluginRegistered(t *testing.T) {
	host, svc, kind := newContributingHost(t)

	p := &lifecyclePlugin{id: "tangent.plugin.lifecycle"}
	if err := host.Load(p); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := host.RegisterUIComponent(plugin.UIComponent{
		ID: kind, Type: plugin.UIComponentTypeEnvelope, Name: "StubView",
	}); err != nil {
		t.Fatalf("RegisterUIComponent: %v", err)
	}
	if err := host.RegisterMCPTool(validTool()); err != nil {
		t.Fatalf("RegisterMCPTool: %v", err)
	}
	if err := host.RegisterHTTPRoute(validRoute()); err != nil {
		t.Fatalf("RegisterHTTPRoute: %v", err)
	}

	if err := host.Unload(p.ID()); err != nil {
		t.Fatalf("Unload: %v", err)
	}

	if !p.unloaded {
		t.Error("the plugin's own Unload was not called")
	}
	if _, held := host.GetPlugin(p.ID()); held {
		t.Error("an unloaded plugin is still on the roster; GetPlugin would hand it to a dependent")
	}
	if _, registered := svc.Lookup(kind); !registered {
		t.Errorf("%s left the envelope registry on unload; go-envelopes has no removal, so a host "+
			"that reported one would be reporting a registry state that is not in force", kind)
	}
	if len(host.MCPTools()) != 1 {
		t.Error("a contributed tool was dropped on unload; the host cannot attribute one to a " +
			"plugin, so it has nothing to select for removal")
	}
	if len(host.HTTPRoutes()) != 1 {
		t.Error("a contributed route was dropped on unload, for the same reason")
	}
	if len(host.ContributedKinds()) != 1 {
		t.Error("ContributedKinds stopped reporting a kind that is still registered")
	}
}

func TestUnloadRefusesAPluginItDoesNotHold(t *testing.T) {
	host, _ := newHost(t)
	if err := host.Unload("tangent.plugin.absent"); !errors.Is(err, ErrPluginNotLoaded) {
		t.Fatalf("err = %v, want ErrPluginNotLoaded", err)
	}
}

// TestUnloadAllUnloadsInReverseLoadOrder holds the ordering a dependency
// declaration implies: a plugin comes off before the thing it depends on.
func TestUnloadAllUnloadsInReverseLoadOrder(t *testing.T) {
	host, _ := newHost(t)
	base := &lifecyclePlugin{id: "tangent.plugin.base"}
	dependent := &lifecyclePlugin{id: "tangent.plugin.dependent", deps: []string{base.id}}
	for _, p := range []*lifecyclePlugin{base, dependent} {
		if err := host.Load(p); err != nil {
			t.Fatalf("Load %s: %v", p.id, err)
		}
	}

	order := host.attemptOrder([]string{dependent.id, base.id})
	if len(order) != 2 || order[0] != base.id || order[1] != dependent.id {
		t.Fatalf("attemptOrder = %v, want load order", order)
	}
	if err := host.UnloadAll(); err != nil {
		t.Fatalf("UnloadAll: %v", err)
	}
	if !base.unloaded || !dependent.unloaded {
		t.Fatalf("UnloadAll left a plugin loaded: base=%v dependent=%v", base.unloaded, dependent.unloaded)
	}
	// Idempotent: a shutdown path that can be reached twice must not close a
	// closed channel.
	if err := host.UnloadAll(); err != nil {
		t.Fatalf("second UnloadAll: %v", err)
	}
}

// TestUnloadAllSurvivesAPanickingPlugin holds the shutdown-path rule: one
// plugin's defect must not stop the next plugin from getting the chance to
// release what it holds.
func TestUnloadAllSurvivesAPanickingPlugin(t *testing.T) {
	host, _ := newHost(t)
	bad := &lifecyclePlugin{id: "tangent.plugin.bad", panicOn: "Unload"}
	good := &lifecyclePlugin{id: "tangent.plugin.good"}
	for _, p := range []*lifecyclePlugin{bad, good} {
		if err := host.Load(p); err != nil {
			t.Fatalf("Load %s: %v", p.id, err)
		}
	}
	err := host.UnloadAll()
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("UnloadAll err = %v, want it to report the panic", err)
	}
	if !good.unloaded {
		t.Error("a panicking plugin stopped the next one from unloading")
	}
}

// TestAPanickingLoadFailsTheBootWithoutCrashingIt. A plugin defect at boot
// should be a line to read, not a stack to decipher — and the boot still fails,
// because LoadShipped has no partial mode.
func TestAPanickingLoadFailsTheBootWithoutCrashingIt(t *testing.T) {
	host, _ := newHost(t)
	err := host.Load(&lifecyclePlugin{id: "tangent.plugin.panics", panicOn: "Load"})
	if err == nil {
		t.Fatal("a panicking Load was reported as success")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("err = %v, want it to name the panic", err)
	}
	inventory := host.Inventory()
	if inventory.Refused != 1 || inventory.Loaded != 0 {
		t.Fatalf("inventory = %+v, want one refusal", inventory)
	}
	if inventory.Plugins[0].Error == "" {
		t.Error("a refused plugin carries no reason; the whole point is that it says why")
	}
}

// TestInventoryReportsWhatLoadedAndWhatRefused is the observability half. It is
// the answer `tangent.health_report` gives, and the reason it cannot be
// inferred from a tool list is right here in the fixture: the loaded plugin
// contributes no tool at all.
func TestInventoryReportsWhatLoadedAndWhatRefused(t *testing.T) {
	host, _ := newHost(t)
	loaded := &lifecyclePlugin{id: "tangent.plugin.loaded"}
	if err := host.Load(loaded); err != nil {
		t.Fatalf("Load: %v", err)
	}
	refused := &lifecyclePlugin{id: "tangent.plugin.refused", deps: []string{"tangent.plugin.absent"}}
	if err := host.Load(refused); err == nil {
		t.Fatal("a plugin with a missing dependency loaded")
	}

	inventory := host.Inventory()
	if inventory.Loaded != 1 || inventory.Refused != 1 {
		t.Fatalf("inventory = %+v, want one loaded and one refused", inventory)
	}
	if len(inventory.Tools) != 0 {
		t.Fatal("fixture drift: this test's value depends on the loaded plugin contributing no tool")
	}
	byID := map[string]PluginRecord{}
	for _, record := range inventory.Plugins {
		byID[record.ID] = record
	}
	if !byID[loaded.id].Loaded || !byID[loaded.id].Enabled {
		t.Errorf("loaded plugin reported %+v", byID[loaded.id])
	}
	if byID[refused.id].Loaded {
		t.Error("a refused plugin reported as loaded")
	}
	if !strings.Contains(byID[refused.id].Error, "tangent.plugin.absent") {
		t.Errorf("refusal reason = %q, want it to name the missing dependency", byID[refused.id].Error)
	}
	if inventory.Attribution == "" {
		t.Error("the inventory does not say its contributed lists are host-wide; an operator " +
			"reading it during an incident should not have to find the source to learn that")
	}

	// After unloading, the record survives and the status does not: which
	// plugins this process loaded is history, whether one is loaded now is not.
	if err := host.Unload(loaded.id); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	after := host.Inventory()
	if after.Loaded != 0 {
		t.Errorf("inventory still reports %d loaded after unloading the only one", after.Loaded)
	}
	if len(after.Plugins) != 2 {
		t.Errorf("unloading dropped a plugin's record; the attempt history is what says it ever loaded")
	}
}

// TestAPanickingToolHandlerIsContainedByTheHost. internal/mcp recovers too, and
// that is deliberate defense in depth — but a boundary worth having at the
// dispatch site is worth having before anything can reach it.
func TestAPanickingToolHandlerIsContainedByTheHost(t *testing.T) {
	host, _ := newHost(t)
	tool := validTool()
	tool.Handler = &stubHandler{
		call: func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
			panic("plugin tool panicked")
		},
	}
	if err := host.RegisterMCPTool(tool); err != nil {
		t.Fatalf("RegisterMCPTool: %v", err)
	}
	_, err := host.MCPTools()[0].Handler.MCPCallTool(
		context.Background(), subprocess.MCPCallRequest{ToolName: tool.Name})
	if err == nil {
		t.Fatal("a panicking tool handler returned no error")
	}
	if !strings.Contains(err.Error(), "panicked") || !strings.Contains(err.Error(), tool.Name) {
		t.Errorf("err = %v, want it to name the panic and the tool", err)
	}
}

// TestAPanickingRouteHandlerIsContainedByTheHost, for the same reason.
func TestAPanickingRouteHandlerIsContainedByTheHost(t *testing.T) {
	host, _ := newHost(t)
	route := validRoute()
	route.Handler = &stubHandler{
		serve: func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
			panic("plugin route panicked")
		},
	}
	if err := host.RegisterHTTPRoute(route); err != nil {
		t.Fatalf("RegisterHTTPRoute: %v", err)
	}
	_, err := host.HTTPRoutes()[0].Handler.HTTPHandle(
		context.Background(), subprocess.HTTPRequest{Method: http.MethodPost, Path: route.Path})
	if err == nil {
		t.Fatal("a panicking route handler returned no error")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("err = %v, want it to name the panic", err)
	}
}

// TestAHangingHandlerIsBoundedByTheDispatchBudget. The budget is shortened for
// the test; production takes dispatchBudget.
func TestAHangingHandlerIsBoundedByTheDispatchBudget(t *testing.T) {
	host, _ := newHost(t)
	host.budget = 30 * time.Millisecond

	stop := make(chan struct{})
	defer close(stop)
	tool := validTool()
	tool.Handler = &stubHandler{
		call: func(ctx context.Context, _ subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
			// Deliberately ignores its context, which is the case the budget
			// exists for: a plugin that honors cancellation needs no guard.
			<-stop
			return subprocess.MCPCallResult{Content: json.RawMessage(`{}`)}, nil
		},
	}
	if err := host.RegisterMCPTool(tool); err != nil {
		t.Fatalf("RegisterMCPTool: %v", err)
	}

	_, err := host.MCPTools()[0].Handler.MCPCallTool(
		context.Background(), subprocess.MCPCallRequest{ToolName: tool.Name})
	if !errors.Is(err, ErrDispatchBudget) {
		t.Fatalf("err = %v, want ErrDispatchBudget", err)
	}
}

// TestShutdownReleasesAnInFlightDispatchWithoutWaitingForIt is the
// "must not block shutdown" clause, and the reason UnloadAll closes the release
// channel before it unloads anything.
//
// CW-20260909-0045 is the prior art: one unbounded stream held graceful
// shutdown past its deadline, and the exiting process still owned the database
// when its successor booted. Waiting out a hung plugin's full dispatch budget
// would be the same failure with a different name.
func TestShutdownReleasesAnInFlightDispatchWithoutWaitingForIt(t *testing.T) {
	host, _ := newHost(t)
	// Long enough that a test finishing quickly proves the release did it, not
	// the budget.
	host.budget = time.Hour

	entered := make(chan struct{})
	stop := make(chan struct{})
	defer close(stop)
	route := validRoute()
	route.Handler = &stubHandler{
		serve: func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
			close(entered)
			<-stop
			return subprocess.HTTPResponse{Status: http.StatusOK}, nil
		},
	}
	if err := host.RegisterHTTPRoute(route); err != nil {
		t.Fatalf("RegisterHTTPRoute: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := host.HTTPRoutes()[0].Handler.HTTPHandle(
			context.Background(), subprocess.HTTPRequest{Method: http.MethodPost, Path: route.Path})
		done <- err
	}()
	<-entered

	if err := host.UnloadAll(); err != nil {
		t.Fatalf("UnloadAll: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrHostShuttingDown) {
			t.Fatalf("err = %v, want ErrHostShuttingDown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited on an in-flight plugin dispatch; this is CW-20260909-0045's " +
			"failure mode with a plugin in place of an SSE stream")
	}
}

// TestACancelledCallerReleasesImmediately. A disconnected browser or an
// abandoned tool call should not hold a dispatch for the full budget.
func TestACancelledCallerReleasesImmediately(t *testing.T) {
	host, _ := newHost(t)
	host.budget = time.Hour

	stop := make(chan struct{})
	defer close(stop)
	route := validRoute()
	route.Handler = &stubHandler{
		serve: func(context.Context, subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
			<-stop
			return subprocess.HTTPResponse{}, nil
		},
	}
	if err := host.RegisterHTTPRoute(route); err != nil {
		t.Fatalf("RegisterHTTPRoute: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := host.HTTPRoutes()[0].Handler.HTTPHandle(
			ctx, subprocess.HTTPRequest{Method: http.MethodPost, Path: route.Path})
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled caller waited on the dispatch budget")
	}
}

// TestAWellBehavedHandlerIsUnaffected. A guard that changed the ordinary answer
// would be a guard nobody could trust with the extraordinary one.
func TestAWellBehavedHandlerIsUnaffected(t *testing.T) {
	host, _ := newHost(t)
	tool := validTool()
	tool.Handler = &stubHandler{
		call: func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
			return subprocess.MCPCallResult{Content: json.RawMessage(`{"ok":true}`)}, nil
		},
	}
	if err := host.RegisterMCPTool(tool); err != nil {
		t.Fatalf("RegisterMCPTool: %v", err)
	}
	result, err := host.MCPTools()[0].Handler.MCPCallTool(
		context.Background(), subprocess.MCPCallRequest{ToolName: tool.Name})
	if err != nil {
		t.Fatalf("MCPCallTool: %v", err)
	}
	if string(result.Content) != `{"ok":true}` {
		t.Fatalf("content = %s, want the handler's own answer verbatim", result.Content)
	}
}

// TestConfigSurfacesStayRefused is the CW-20260910-0036 config ratification as
// a test. The host holds no plugin config, so it can never hold a plugin's
// secret; a build that started answering these would have created the store
// ADR 0005 §3.1's secret boundary depends on not existing.
func TestConfigSurfacesStayRefused(t *testing.T) {
	host, _ := newHost(t)
	if _, err := host.GetConfig("torque_token"); !errors.Is(err, ErrSurfaceNotHonored) {
		t.Errorf("GetConfig err = %v, want ErrSurfaceNotHonored", err)
	}
	if err := host.SetConfig("torque_token", "secret"); !errors.Is(err, ErrSurfaceNotHonored) {
		t.Errorf("SetConfig err = %v, want ErrSurfaceNotHonored", err)
	}
	if err := host.RegisterConfigSchema([]plugin.ConfigFieldDef{{Key: "torque_token"}}); !errors.Is(err, ErrSurfaceNotHonored) {
		t.Errorf("RegisterConfigSchema err = %v, want ErrSurfaceNotHonored", err)
	}
}
