package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	plugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
	capHost "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability/host"
)

type capabilityPlugin struct {
	stubPlugin
	scoped *ownedHost
}

func (p *capabilityPlugin) Load(host plugin.Host) error {
	p.scoped = host.(*ownedHost)
	return nil
}

type capabilityToolFunc func(context.Context, string, any) (ToolResult, error)

func (f capabilityToolFunc) CallTool(ctx context.Context, name string, arguments any) (ToolResult, error) {
	return f(ctx, name, arguments)
}

type toolPolicyFunc func(context.Context, ToolAccess) (ToolAuthorization, error)

func (f toolPolicyFunc) AuthorizeTool(ctx context.Context, access ToolAccess) (ToolAuthorization, error) {
	return f(ctx, access)
}

// This budget enforces one concurrent read across all fixture calls. It is
// explicit host input; the production adapter supplies no no-op fallback.
type testToolBudget struct {
	held     atomic.Bool
	releases atomic.Int64
	hook     func()
}

func (b *testToolBudget) Reserve(ctx context.Context, _ capHost.Authority, _ capHost.Call) (func(), error) {
	if ctx.Err() != nil || !b.held.CompareAndSwap(false, true) {
		return nil, toolRefusal(capability.BudgetExceeded)
	}
	if b.hook != nil {
		b.hook()
	}
	var once sync.Once
	return func() { once.Do(func() { b.held.Store(false); b.releases.Add(1) }) }, nil
}

func reviewedRead(lease context.Context, t *testing.T, access ToolAccess) ToolAuthorization {
	t.Helper()
	scope := capability.Scope{
		Allowlists: map[string][]string{"operations": {toolReadOperation}, "targets": {"tangent.fixture_read"}, "effects": {"read"}},
		Limits:     map[string]int64{"request_bytes": maxToolRequestBytes, "response_bytes": 4096, "deadline_ms": dispatchBudget.Milliseconds(), "concurrency": 1},
	}
	raw, err := json.Marshal(scope)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	grant := capability.Grant{
		GrantID: "fixture-reviewed-read", Name: ToolReadCapability, SchemaVersion: 1, Scope: raw,
		HostInstance: access.Runtime.HostInstance, OwnerID: access.Runtime.OwnerID, OwnerGeneration: access.Runtime.OwnerGeneration,
		Audience: toolAudience, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano), PolicyRevision: "review-1",
	}
	return ToolAuthorization{ResponseBytes: 4096, Authority: capHost.Authority{
		Authenticated: true, Background: true, Actor: capHost.Subject{Kind: capHost.PluginActor, ID: access.Runtime.OwnerID},
		Owner: access.Runtime, Audience: toolAudience, Grant: grant, PolicyRevision: "review-1", Policy: scope,
		TransportScope: &scope, LeaseContext: lease, Active: true, TargetAvailable: true,
	}}
}

func capabilityFixture(t *testing.T, policy toolPolicyFunc, budget *testToolBudget, backend capabilityToolFunc) (*Host, *capabilityPlugin, ToolCaller) {
	t.Helper()
	host, _ := newHost(t)
	if policy != nil {
		if err := host.ConfigureToolCapabilities(ToolCapabilityConfig{Provider: policy, Budget: budget}); err != nil {
			t.Fatal(err)
		}
	}
	if err := host.AttachToolCaller(backend); err != nil {
		t.Fatal(err)
	}
	p := &capabilityPlugin{stubPlugin: stubPlugin{id: "capability-fixture"}}
	if err := host.Load(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.UnloadAll(); err != nil {
			t.Error(err)
		}
	})
	caller, err := p.scoped.Tools()
	if err != nil {
		t.Fatal(err)
	}
	return host, p, caller
}

func wantCapabilityCode(t *testing.T, err error, code capability.Code) {
	t.Helper()
	var failure *capability.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func TestToolCapabilityDefaultRefusalAndCompositionOwnership(t *testing.T) {
	var calls atomic.Int64
	host, p, caller := capabilityFixture(t, nil, nil, func(context.Context, string, any) (ToolResult, error) {
		calls.Add(1)
		return ToolResult{Content: json.RawMessage(`"private"`)}, nil
	})
	result, err := caller.CallTool(context.Background(), "tangent.fixture_read", map[string]any{"claimed_grant": "unsafe", "caller": "operator"})
	wantCapabilityCode(t, err, capability.CapabilityDenied)
	if calls.Load() != 0 || len(result.Content) != 0 {
		t.Fatal("default refusal reached backend or disclosed content")
	}
	wantCapabilityCode(t, p.scoped.ConfigureToolCapabilities(ToolCapabilityConfig{}), capability.CapabilityDenied)
	wantCapabilityCode(t, p.scoped.AttachToolCaller(capabilityToolFunc(nil)), capability.CapabilityDenied)
	if host.CapabilityDenials()[capHost.DenialKey{Code: capability.CapabilityDenied}] != 1 {
		t.Fatal("default denial was not counted")
	}
	// The composition root remains able to use its own caller. This is not a
	// plugin capability and is deliberately a separate handle.
	rootCaller, rootErr := host.Tools()
	if rootErr != nil {
		t.Fatal(rootErr)
	}
	if _, rootErr = rootCaller.CallTool(context.Background(), "tangent.fixture_read", nil); rootErr != nil {
		t.Fatal(rootErr)
	}
}

func TestToolCapabilityReviewedReadAndReloadFence(t *testing.T) {
	budget := &testToolBudget{}
	var observed []capability.RuntimeIdentity
	var reads int
	host, p, caller := capabilityFixture(t, func(_ context.Context, access ToolAccess) (ToolAuthorization, error) {
		observed = append(observed, access.Runtime)
		return reviewedRead(context.Background(), t, access), nil
	}, budget, func(_ context.Context, name string, args any) (ToolResult, error) {
		reads++
		if name != "tangent.fixture_read" || string(args.(json.RawMessage)) != `{"resource":"one"}` {
			t.Fatal("actual read arguments changed")
		}
		return ToolResult{Content: json.RawMessage(`{"answer":"one"}`)}, nil
	})
	result, err := caller.CallTool(context.Background(), "tangent.fixture_read", map[string]any{"resource": "one"})
	if err != nil || string(result.Content) != `{"answer":"one"}` {
		t.Fatalf("read = %s, %v", result.Content, err)
	}
	old := p.scoped.owner.runtime
	if old.Validate() != nil || old.HostInstance == host.hostInstance || len(observed) < 2 || budget.releases.Load() != 1 {
		t.Fatal("real load identity/recheck/reservation was not reached")
	}
	if err = host.Unload(p.ID()); err != nil {
		t.Fatal(err)
	}
	replacement := &capabilityPlugin{stubPlugin: stubPlugin{id: p.ID()}}
	if err = host.Load(replacement); err != nil {
		t.Fatal(err)
	}
	if replacement.scoped.owner.runtime.OwnerGeneration <= old.OwnerGeneration {
		t.Fatal("replacement reused native generation")
	}
	_, err = caller.CallTool(context.Background(), "tangent.fixture_read", nil)
	wantCapabilityCode(t, err, capability.TargetUnavailable)
	if reads != 1 {
		t.Fatal("old handle borrowed replacement authority")
	}
}

func TestToolCapabilityAdmissionConformance(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*ToolAuthorization)
		code   capability.Code
	}{
		{"expired", func(a *ToolAuthorization) {
			a.Authority.Grant.ExpiresAt = time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
			a.Authority.Grant.IssuedAt = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
		}, capability.CapabilityDenied},
		{"wrong-generation", func(a *ToolAuthorization) { a.Authority.Grant.OwnerGeneration++ }, capability.TargetUnavailable},
		{"claimed-other-actor", func(a *ToolAuthorization) { a.Authority.Actor.ID = "another-plugin" }, capability.Unauthenticated},
		{"unauthenticated", func(a *ToolAuthorization) { a.Authority.Authenticated = false }, capability.Unauthenticated},
		{"missing-caller", func(a *ToolAuthorization) { a.Authority.Background = false }, capability.Unauthenticated},
		{"wrong-policy-revision", func(a *ToolAuthorization) { a.Authority.PolicyRevision = "review-2" }, capability.CapabilityDenied},
		{"wrong-audience", func(a *ToolAuthorization) { a.Authority.Audience = "another-host" }, capability.Unauthenticated},
		{"unsupported-capability", func(a *ToolAuthorization) { a.Authority.Grant.Name = capability.MCPReach }, capability.CapabilityDenied},
		{"write-not-supported", func(a *ToolAuthorization) { a.Authority.Policy.Allowlists["effects"] = []string{"write"} }, capability.ScopeDenied},
		{"target-outside-policy", func(a *ToolAuthorization) { a.Authority.Policy.Allowlists["targets"] = []string{"tangent.another"} }, capability.ScopeDenied},
		{"missing-transport-scope", func(a *ToolAuthorization) { a.Authority.TransportScope = nil }, capability.CapabilityDenied},
		{"unknown-dimension", func(a *ToolAuthorization) { a.Authority.Policy.Allowlists["wildcard"] = []string{"all"} }, capability.ScopeDenied},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var reached atomic.Bool
			_, _, caller := capabilityFixture(t, func(_ context.Context, access ToolAccess) (ToolAuthorization, error) {
				out := reviewedRead(context.Background(), t, access)
				test.modify(&out)
				return out, nil
			}, &testToolBudget{}, func(context.Context, string, any) (ToolResult, error) { reached.Store(true); return ToolResult{}, nil })
			_, err := caller.CallTool(context.Background(), "tangent.fixture_read", nil)
			wantCapabilityCode(t, err, test.code)
			if reached.Load() {
				t.Fatal("refusal reached read backend")
			}
		})
	}
}

func TestToolCapabilityActualCallerPolicyAndConcurrentBudget(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	budget := &testToolBudget{}
	var refused atomic.Bool
	_, _, caller := capabilityFixture(t, func(_ context.Context, access ToolAccess) (ToolAuthorization, error) {
		out := reviewedRead(context.Background(), t, access)
		out.Authority.Background = false
		out.Authority.InitiatingCaller = &capHost.Subject{Kind: capHost.SessionClient, ID: "host-verified-fixture-session"}
		policy := out.Authority.Policy
		if refused.Load() {
			policy.Allowlists = map[string][]string{"operations": {toolReadOperation}, "targets": {}, "effects": {"read"}}
		}
		out.Authority.CallerPolicy = &policy
		return out, nil
	}, budget, func(ctx context.Context, _ string, _ any) (ToolResult, error) {
		close(entered)
		select {
		case <-finish:
			return ToolResult{Content: json.RawMessage(`{}`)}, nil
		case <-ctx.Done():
			return ToolResult{}, ctx.Err()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := caller.CallTool(ctx, "tangent.fixture_read", nil); done <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("reviewed caller did not reach read")
	}
	_, err := caller.CallTool(ctx, "tangent.fixture_read", nil)
	wantCapabilityCode(t, err, capability.BudgetExceeded)
	close(finish)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	refused.Store(true)
	_, err = caller.CallTool(ctx, "tangent.fixture_read", nil)
	wantCapabilityCode(t, err, capability.ScopeDenied)
	if budget.held.Load() || budget.releases.Load() != 1 {
		t.Fatal("concurrent reservation leaked")
	}
}

type failingCapabilityAudit struct{ events []capHost.AuditEvent }

func (s *failingCapabilityAudit) Record(_ context.Context, event capHost.AuditEvent) error {
	s.events = append(s.events, event)
	return errors.New("synthetic-secret-sink-error")
}

func TestToolCapabilityPolicyFailureTelemetryDoesNotExposePayload(t *testing.T) {
	host, _ := newHost(t)
	sink := &failingCapabilityAudit{}
	if err := host.ConfigureToolCapabilities(ToolCapabilityConfig{
		Provider: toolPolicyFunc(func(context.Context, ToolAccess) (ToolAuthorization, error) {
			return ToolAuthorization{}, errors.New("synthetic-secret-policy-error")
		}),
		Budget: &testToolBudget{}, Audit: sink,
	}); err != nil {
		t.Fatal(err)
	}
	if err := host.AttachToolCaller(capabilityToolFunc(func(context.Context, string, any) (ToolResult, error) {
		t.Fatal("denial reached backend")
		return ToolResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}
	p := &capabilityPlugin{stubPlugin: stubPlugin{id: "fixture"}}
	if err := host.Load(p); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := host.UnloadAll(); err != nil {
			t.Error(err)
		}
	}()
	caller, err := p.scoped.Tools()
	if err != nil {
		t.Fatal(err)
	}
	_, err = caller.CallTool(context.Background(), "tangent.fixture_read", map[string]any{"token": "synthetic-secret-payload"})
	wantCapabilityCode(t, err, capability.CapabilityDenied)
	if err.Error() != "capability_denied: host.tangent.tools.read" {
		t.Fatalf("raw error leaked: %v", err)
	}
	if len(sink.events) != 1 || sink.events[0].Target != "" || sink.events[0].Actor.ID != "" || sink.events[0].Outcome != capability.CapabilityDenied {
		t.Fatal("early denial logged unverified labels")
	}
	if host.toolCapabilities.audit.Failures() != 1 || host.CapabilityDenials()[capHost.DenialKey{Code: capability.CapabilityDenied}] != 1 {
		t.Fatal("sink failure lost denial counter")
	}
}

func TestToolCapabilityMissingBudgetAndImmutableComposition(t *testing.T) {
	host, _ := newHost(t)
	provider := toolPolicyFunc(func(_ context.Context, access ToolAccess) (ToolAuthorization, error) {
		return reviewedRead(context.Background(), t, access), nil
	})
	wantCapabilityCode(t, host.ConfigureToolCapabilities(ToolCapabilityConfig{Provider: provider}), capability.CapabilityDenied)
	if err := host.ConfigureToolCapabilities(ToolCapabilityConfig{Provider: provider, Budget: &testToolBudget{}}); err != nil {
		t.Fatal(err)
	}
	wantCapabilityCode(t, host.ConfigureToolCapabilities(ToolCapabilityConfig{Provider: provider, Budget: &testToolBudget{}}), capability.Conflict)
}

func TestToolCapabilityRechecksBeforeDisclosure(t *testing.T) {
	for _, withdrawal := range []string{"lease", "policy", "owner", "request", "response-size"} {
		t.Run(withdrawal, func(t *testing.T) {
			lease, revoke := context.WithCancel(context.Background())
			defer revoke()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var withdrawn atomic.Bool
			var host *Host
			var p *capabilityPlugin
			budget := &testToolBudget{}
			host, p, caller := capabilityFixture(t, func(_ context.Context, access ToolAccess) (ToolAuthorization, error) {
				out := reviewedRead(lease, t, access)
				if withdrawal == "policy" && withdrawn.Load() {
					out.Authority.PolicyRevision = "review-2"
				}
				return out, nil
			}, budget, func(context.Context, string, any) (ToolResult, error) {
				withdrawn.Store(true)
				switch withdrawal {
				case "lease":
					revoke()
				case "owner":
					if err := host.Unload(p.ID()); err != nil {
						t.Fatal(err)
					}
				case "request":
					cancel()
				case "response-size":
					return ToolResult{Content: make(json.RawMessage, 4097)}, nil
				}
				return ToolResult{Content: json.RawMessage(`"private-result"`)}, nil
			})
			result, err := caller.CallTool(ctx, "tangent.fixture_read", nil)
			if err == nil || len(result.Content) != 0 || !withdrawn.Load() || budget.releases.Load() != 1 {
				t.Fatalf("withdrawn read disclosed/failed release: %+v %v", result, err)
			}
		})
	}
}

func TestToolCapabilityBudgetAndCancellationBeforeBackend(t *testing.T) {
	lease, revoke := context.WithCancel(context.Background())
	defer revoke()
	budget := &testToolBudget{hook: revoke}
	var reached atomic.Bool
	_, _, caller := capabilityFixture(t, func(_ context.Context, access ToolAccess) (ToolAuthorization, error) {
		return reviewedRead(lease, t, access), nil
	}, budget,
		func(context.Context, string, any) (ToolResult, error) { reached.Store(true); return ToolResult{}, nil })
	_, err := caller.CallTool(context.Background(), "tangent.fixture_read", nil)
	if err == nil || reached.Load() || budget.releases.Load() != 1 {
		t.Fatal("withdrawal during reservation reached backend or leaked reservation")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = caller.CallTool(canceled, "tangent.fixture_read", nil)
	wantCapabilityCode(t, err, capability.Cancelled)
}
