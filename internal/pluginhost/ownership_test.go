package pluginhost

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	plugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
)

type owningPlugin struct {
	lifecyclePlugin
	handle      *ownedHost
	tool        MCPTool
	route       HTTPRoute
	kind        string
	loadFailure error
	unloads     atomic.Int32
}

func (p *owningPlugin) Load(host plugin.Host) error {
	p.handle = host.(*ownedHost)
	if p.kind != "" {
		if err := host.RegisterUIComponent(plugin.UIComponent{ID: p.kind, Type: plugin.UIComponentTypeEnvelope, Name: "OwnedView"}); err != nil {
			return err
		}
	}
	if err := p.handle.RegisterMCPTool(p.tool); err != nil {
		return err
	}
	if err := p.handle.RegisterHTTPRoute(p.route); err != nil {
		return err
	}
	return p.loadFailure
}
func (p *owningPlugin) Unload() error { p.unloads.Add(1); return nil }

func ownerFixture(id string) *owningPlugin {
	tool := validTool()
	tool.Name = "tangent." + id
	route := validRoute()
	route.Path = "/api/plugins/" + id + "/read"
	return &owningPlugin{lifecyclePlugin: lifecyclePlugin{id: id}, tool: tool, route: route}
}

func TestOwnerUnloadSweepsAndFencesRetainedHandles(t *testing.T) {
	host, svc, kind := newContributingHost(t)
	one := ownerFixture("one")
	one.kind = kind
	two := ownerFixture("two")
	if err := host.Load(one); err != nil {
		t.Fatal(err)
	}
	if err := host.Load(two); err != nil {
		t.Fatal(err)
	}
	oldTool := host.MCPTools()[0]
	if err := host.Unload("one"); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.Lookup(kind); ok {
		t.Fatal("unloaded owned kind is still active")
	}
	if _, ok := host.HTTPRoute(one.route.Method, one.route.Path); ok {
		t.Fatal("unloaded route still resolves")
	}
	if _, ok := host.HTTPRoute(two.route.Method, two.route.Path); !ok {
		t.Fatal("other owner's route removed")
	}
	if _, err := oldTool.Handler.MCPCallTool(context.Background(), subprocess.MCPCallRequest{}); !errors.Is(err, ErrPluginNotLoaded) {
		t.Fatalf("retained handler: %v", err)
	}
	if err := one.handle.RegisterMCPTool(one.tool); !errors.Is(err, ErrPluginNotLoaded) {
		t.Fatalf("retained registrar: %v", err)
	}
	replacement := ownerFixture("one")
	replacement.kind = kind
	if err := host.Load(replacement); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := one.handle.RegisterHTTPRoute(one.route); !errors.Is(err, ErrPluginNotLoaded) {
		t.Fatalf("old owner registered into replacement: %v", err)
	}
	if err := host.UnloadAll(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerFailedLoadRollsBackOnlyItsRegistrations(t *testing.T) {
	host, _ := newHost(t)
	good := ownerFixture("good")
	if err := host.Load(good); err != nil {
		t.Fatal(err)
	}
	bad := ownerFixture("bad")
	bad.loadFailure = errors.New("fixture failure")
	if err := host.Load(bad); !errors.Is(err, bad.loadFailure) {
		t.Fatalf("load: %v", err)
	}
	if _, ok := host.HTTPRoute(bad.route.Method, bad.route.Path); ok {
		t.Fatal("failed load route remains")
	}
	if _, ok := host.HTTPRoute(good.route.Method, good.route.Path); !ok {
		t.Fatal("healthy owner's route removed")
	}
	if bad.unloads.Load() != 1 {
		t.Fatal("failed owner was not torn down once")
	}
	if err := host.Load(ownerFixture("good")); !errors.Is(err, ErrDuplicatePlugin) {
		t.Fatal(err)
	}
	if _, ok := host.GetPlugin("good"); !ok {
		t.Fatal("duplicate load removed active owner")
	}
	_ = host.UnloadAll()
}

func TestEnabledIntentPersistsAndReloadUsesFreshOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enabled.json")
	host, _ := newHost(t)
	if err := host.ConfigureIntent(path); err != nil {
		t.Fatal(err)
	}
	var current *owningPlugin
	factory := func() (plugin.Plugin, error) { current = ownerFixture("toggle"); return current, nil }
	if err := host.RegisterFactory("toggle", factory); err != nil {
		t.Fatal(err)
	}
	if err := host.SetEnabled(context.Background(), "toggle", true); err != nil {
		t.Fatal(err)
	}
	first := current
	if err := host.Reload(context.Background(), "toggle"); err != nil {
		t.Fatal(err)
	}
	if current == first || first.unloads.Load() != 1 {
		t.Fatal("reload reused old instance")
	}
	if err := host.SetEnabled(context.Background(), "toggle", false); err != nil {
		t.Fatal(err)
	}
	fresh, _ := newHost(t)
	if err := fresh.ConfigureIntent(path); err != nil {
		t.Fatal(err)
	}
	if fresh.DesiredEnabled("toggle") {
		t.Fatal("disabled intent did not survive host restart")
	}
	if err := fresh.RegisterFactory("toggle", factory); err != nil {
		t.Fatal(err)
	}
	record := fresh.Inventory(context.Background()).Plugins[0]
	if record.Enabled || record.Loaded || record.State != "disabled" {
		t.Fatalf("disabled inventory: %+v", record)
	}
	if err := fresh.SetEnabled(context.Background(), "toggle", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := fresh.HTTPRoute(current.route.Method, current.route.Path); !ok {
		t.Fatal("enabled route not available")
	}
	_ = fresh.UnloadAll()
	_ = host.UnloadAll()
}

func TestOwnerDispatchBudgetTripsCircuitWithoutStoppingAnotherOwner(t *testing.T) {
	host, _ := newHost(t)
	host.budget = 20 * time.Millisecond
	stuck := ownerFixture("stuck")
	entered := make(chan struct{})
	finish := make(chan struct{})
	defer close(finish)
	stuck.tool.Handler = &stubHandler{call: func(context.Context, subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
		close(entered)
		<-finish
		return subprocess.MCPCallResult{}, nil
	}}
	if err := host.Load(stuck); err != nil {
		t.Fatal(err)
	}
	healthy := ownerFixture("healthy")
	if err := host.Load(healthy); err != nil {
		t.Fatal(err)
	}
	var guardedTool MCPTool
	for _, tool := range host.MCPTools() {
		if tool.Name == stuck.tool.Name {
			guardedTool = tool
		}
	}
	_, err := guardedTool.Handler.MCPCallTool(context.Background(), subprocess.MCPCallRequest{})
	if !errors.Is(err, ErrDispatchBudget) {
		t.Fatalf("budget: %v", err)
	}
	<-entered
	if _, err = guardedTool.Handler.MCPCallTool(context.Background(), subprocess.MCPCallRequest{}); !errors.Is(err, ErrPluginNotLoaded) {
		t.Fatalf("circuit allowed another call: %v", err)
	}
	deadline := time.After(time.Second)
	for stuck.unloads.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("circuit did not tear down owner")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if _, ok := host.GetPlugin(healthy.ID()); !ok {
		t.Fatal("circuit stopped another plugin")
	}
	_ = host.UnloadAll()
}

func TestReloadRecreatesChildWithFreshIncarnation(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)
	var child *ChildPlugin
	factory := func() (plugin.Plugin, error) {
		child = NewChildPlugin(echoSpec(t, binary), nil, nil)
		return child, nil
	}
	id := "tangent.plugin.echo"
	if err := host.RegisterFactory(id, factory); err != nil {
		t.Fatal(err)
	}
	if err := host.SetEnabled(context.Background(), id, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.UnloadAll() })
	old := child
	first := echoedIncarnation(t, old)
	pid := old.pid()
	if err := host.Reload(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	next := echoedIncarnation(t, child)
	if child == old || alive(pid) || next.HostInstance != first.HostInstance || next.OwnerGeneration <= first.OwnerGeneration {
		t.Fatalf("reload failed isolation: old=%+v next=%+v oldPID=%d", first, next, pid)
	}
}

func TestOwnerCallerCancellationDoesNotTripCircuit(t *testing.T) {
	host, _ := newHost(t)
	p := ownerFixture("cancel")
	entered := make(chan struct{})
	p.tool.Handler = &stubHandler{call: func(ctx context.Context, _ subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
		close(entered)
		<-ctx.Done()
		return subprocess.MCPCallResult{}, ctx.Err()
	}}
	if err := host.Load(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.UnloadAll() })
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := host.MCPTools()[0].Handler.MCPCallTool(ctx, subprocess.MCPCallRequest{})
		result <- err
	}()
	<-entered
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !p.handle.owner.available() {
		t.Fatal("caller cancellation quarantined owner")
	}
	if p.unloads.Load() != 0 {
		t.Fatal("caller cancellation stopped plugin")
	}
}

func TestQueuedControlCancellationNeverRunsLater(t *testing.T) {
	host, _ := newHost(t)
	if err := host.RegisterFactory("queued", func() (plugin.Plugin, error) {
		t.Error("canceled queued factory ran")
		return ownerFixture("queued"), nil
	}); err != nil {
		t.Fatal(err)
	}
	host.ops.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := make(chan error, 1)
	go func() { result <- host.SetEnabled(ctx, "queued", true) }()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled control waited for operation gate")
	}
	host.ops.Unlock()
}

func TestInventoryDuplicateRefusalPreservesCurrentOwner(t *testing.T) {
	host, _ := newHost(t)
	p := ownerFixture("current")
	if err := host.Load(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.UnloadAll() })
	if err := host.Load(ownerFixture("current")); !errors.Is(err, ErrDuplicatePlugin) {
		t.Fatal(err)
	}
	inventory := host.Inventory(context.Background())
	if len(inventory.Plugins) != 1 {
		t.Fatalf("unexpected inventory: %+v", inventory)
	}
	current := inventory.Plugins[0]
	if !current.Loaded || current.State != "running" || current.Error != "" {
		t.Fatalf("duplicate refusal hid active owner: %+v", current)
	}
}

type failedRuntimePlugin struct{ *owningPlugin }

func (*failedRuntimePlugin) RuntimeFailure() string { return "fixture terminal runtime failure" }

func TestInventoryTerminalRuntimeFailureIsNotRunning(t *testing.T) {
	host, _ := newHost(t)
	p := &failedRuntimePlugin{ownerFixture("failed")}
	if err := host.Load(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.UnloadAll() })
	inventory := host.Inventory(context.Background())
	current := inventory.Plugins[0]
	if current.State != "failed" || !current.FailedAfterLoad || current.RuntimeError == "" {
		t.Fatalf("terminal runtime failure reported running: %+v", current)
	}
}
