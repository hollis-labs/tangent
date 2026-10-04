package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	driver "github.com/hollis-labs/plugin-host"
	plugin "github.com/hollis-labs/plugin-sdk"
	"github.com/hollis-labs/plugin-sdk/capability"
	"github.com/hollis-labs/plugin-sdk/subprocess"
	tangentplugin "github.com/hollis-labs/tangent/pkg/plugin"
)

const hostVersionForChildren = tangentplugin.ContractVersion
const (
	maxChildRestarts      = 3
	restartInitialBackoff = time.Second
	restartMaxBackoff     = 30 * time.Second
	healthProbeCacheTTL   = time.Second
)

// ChildSpec is Tangent's manifest-resolved artifact and per-plugin roots.
// Env explicitly supplements the inherited environment, preserving the existing
// plugin-owned environment configuration. It conveys no capability grants.
type ChildSpec struct {
	ID       string
	Version  string
	Command  string
	Args     []string
	WorkDir  string
	Env      []string
	DataDir  string
	CacheDir string
	Verify   func() error
	Cleanup  func() error
}

var ErrChildGone = driver.ErrGone
var ErrPluginUnhealthy = driver.ErrUnhealthy

type pluginHealth struct {
	ok        bool
	message   string
	checked   time.Time
	reachable bool
}

type ChildPluginOption func(*ChildPlugin)

func WithHealthGate(enabled bool) ChildPluginOption {
	return func(p *ChildPlugin) { p.healthGate = enabled }
}

// ChildPlugin adapts the shared lifecycle to Tangent's registration surfaces.
// The host manifest remains authoritative: no runtime list-tools call is made.
// Transport, process groups, shutdown and recovery belong to plugin-host.
type ChildPlugin struct {
	spec            ChildSpec
	tools           []MCPTool
	route           []HTTPRoute
	healthGate      bool
	mu              sync.Mutex
	lifecycle       *driver.Lifecycle
	gate            *driver.HealthGate
	owner           driver.Owner
	active          context.Context
	cancel          context.CancelFunc
	status          plugin.PluginStatus
	identity        subprocess.InitResult
	process         *driver.Process
	logger          *slog.Logger
	reportedFailure string
}

func NewChildPlugin(spec ChildSpec, tools []MCPTool, routes []HTTPRoute, opts ...ChildPluginOption) *ChildPlugin {
	p := &ChildPlugin{spec: spec, tools: tools, route: routes, healthGate: true}
	for _, opt := range opts {
		opt(p)
	}
	return p
}
func (p *ChildPlugin) ID() string { return p.spec.ID }
func (p *ChildPlugin) Name() string {
	if info := p.info(); info.Name != "" {
		return info.Name
	}
	return p.spec.ID
}
func (p *ChildPlugin) Version() string {
	if info := p.info(); info.Version != "" {
		return info.Version
	}
	return "unknown"
}
func (p *ChildPlugin) Description() string {
	if info := p.info(); info.Description != "" {
		return info.Description
	}
	return "subprocess plugin " + p.spec.ID
}
func (p *ChildPlugin) Dependencies() []string { return nil }

func (p *ChildPlugin) Load(host plugin.Host) error {
	h, ok := host.(*Host)
	if !ok || h == nil {
		return fmt.Errorf("pluginhost: %s: Tangent host required; got %T", p.ID(), host)
	}
	p.mu.Lock()
	p.logger = h.logger
	if p.lifecycle != nil {
		p.mu.Unlock()
		return fmt.Errorf("pluginhost: %s already loaded", p.ID())
	}
	resolved, err := absoluteChildSpec(p.spec)
	if err != nil {
		p.mu.Unlock()
		return err
	}
	p.spec = resolved
	l, err := driver.NewLifecycle(p.ID(), driver.LifecycleOptions{
		HostInstance: h.hostInstance, Generations: &h.generations,
		Retry: driver.RetryPolicy{MaxAttempts: maxChildRestarts + 1, Backoff: restartInitialBackoff},
		// Preserve Tangent's crash recovery under the explicit classifier model.
		// Lifecycle calls this only for an unexpected exit of an activated child;
		// protocol/identity/version/init failures and intentional stops never reach it.
		ClassifyExit: func(info driver.ExitInfo) error {
			p.mu.Lock()
			proc, controller := p.process, p.lifecycle
			p.mu.Unlock()
			tail := ""
			if proc != nil {
				tail = sanitizePluginDiagnostic(proc.Diagnostics())
			}
			h.logger.Warn("pluginhost: child crashed", "plugin", p.ID(), "exit_code", info.Code, "signal", info.Signal, "stderr", tail)
			if controller.Status().RetryAttempts >= maxChildRestarts {
				h.logger.Warn("pluginhost: restart attempts exhausted", "plugin", p.ID(), "exit_code", info.Code, "signal", info.Signal, "stderr", tail)
			}
			return &driver.TransientError{Code: "unexpected_exit", Cause: info.Err}
		},
		CallbackTimeout: 15 * time.Second,
		Callbacks: driver.LifecycleCallbacks{
			Plan:     p.plan,
			Activate: p.activate,
			Revoke:   p.revoke,
		},
	})
	if err == nil {
		p.lifecycle = l
	}
	p.mu.Unlock()
	if err != nil {
		return err
	}
	if err = l.Enable(h.Context()); err != nil {
		err = &pluginDiagnosticError{cause: err, text: redactPluginDiagnostic(err.Error())}
		p.fail(err)
		return err
	}
	for _, tool := range p.tools {
		tool.Handler = p
		if err = h.RegisterMCPTool(tool); err != nil {
			return p.failAndStop(err)
		}
	}
	for _, route := range p.route {
		route.Handler = p
		if err = h.RegisterHTTPRoute(route); err != nil {
			return p.failAndStop(err)
		}
	}
	return nil
}

func (p *ChildPlugin) plan(ctx context.Context) (driver.Plan, error) {
	p.mu.Lock()
	l := p.lifecycle
	p.mu.Unlock()
	// Lifecycle owns the retry loop and its initial one-second wait. Add only
	// the remaining host-policy delay to preserve the existing 1/2/4s schedule.
	attempt := l.Status().RetryAttempts
	if attempt > 1 {
		timer := time.NewTimer(restartBackoff(attempt-1) - restartInitialBackoff)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return driver.Plan{}, ctx.Err()
		case <-timer.C:
		}
	}
	if p.spec.Verify != nil {
		if err := p.spec.Verify(); err != nil {
			return driver.Plan{}, fmt.Errorf("pluginhost: verify reviewed bundle: %w", err)
		}
	}
	dir := p.spec.WorkDir
	if dir == "" {
		dir = filepath.Dir(p.spec.Command)
	}
	return driver.Plan{Spec: driver.Spec{
		ID: p.ID(), ExpectedID: p.ID(), ExpectedVersion: p.spec.Version,
		Command: p.spec.Command, Args: p.spec.Args, Dir: p.spec.WorkDir,
		Env:         driver.InheritEnv(append(append([]string(nil), p.spec.Env...), "PWD="+dir)...),
		Redact:      redactRawDiagnostic,
		StderrBytes: pluginStderrBytes,
		Init: subprocess.InitParams{
			PluginDir: dir, DataDir: p.spec.DataDir, CacheDir: p.spec.CacheDir,
			// Do not plumb host-held configuration through Init: plugins read
			// their own environment. This keeps configuration and its secrets
			// outside Tangent's stores, inventory and reports.
			Config: map[string]string{}, Grants: capability.GrantSet{},
			CapabilityContract: capability.ContractVersion,
			HostInfo:           subprocess.HostInfo{Version: hostVersionForChildren, Protocol: subprocess.ProtocolVersion},
		},
	}}, nil
}

func (p *ChildPlugin) activate(ctx context.Context, owner driver.Owner, proc *driver.Process) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	p.owner = owner
	p.process = proc
	p.active, p.cancel = context.WithCancel(context.Background())
	p.gate = driver.NewHealthGate(proc.Client().Health, healthProbeCacheTTL)
	p.identity = proc.Info()
	p.status = plugin.PluginStatus{Loaded: true, Enabled: true, LoadedAt: time.Now().UTC()}
	return nil
}
func (p *ChildPlugin) revoke(_ context.Context, owner driver.Owner) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owner == owner && p.cancel != nil {
		p.cancel()
		p.active = nil
		p.cancel = nil
	}
	return nil
}

func (p *ChildPlugin) Unload() error {
	p.mu.Lock()
	l := p.lifecycle
	p.mu.Unlock()
	if l == nil {
		if p.spec.Cleanup != nil {
			return p.spec.Cleanup()
		}
		return nil
	}
	err := l.Disable(context.Background())
	p.mu.Lock()
	p.status = plugin.PluginStatus{}
	p.mu.Unlock()
	if err == nil && p.spec.Cleanup != nil {
		return p.spec.Cleanup()
	}
	return err
}
func (p *ChildPlugin) Status() plugin.PluginStatus {
	p.mu.Lock()
	s, l := p.status, p.lifecycle
	p.mu.Unlock()
	if l == nil {
		return s
	}
	state := l.Status()
	runtimeError := p.RuntimeFailure()
	s.Loaded = state.State == driver.StateRunning
	s.Enabled = state.DesiredEnabled
	if runtimeError != "" {
		s.LastError = runtimeError
	} else if state.LastFailure != nil {
		s.LastError = state.LastFailure.Error()
	}
	if state.Exhausted && !strings.Contains(s.LastError, "restart attempts exhausted") {
		s.LastError += "; restart attempts exhausted"
	}
	return s
}
func (p *ChildPlugin) Restarts() int {
	p.mu.Lock()
	l := p.lifecycle
	p.mu.Unlock()
	if l == nil {
		return 0
	}
	return l.Status().RetryAttempts
}

// Exhausted reports the controller's actual bounded-recovery outcome.
func (p *ChildPlugin) Exhausted() bool {
	p.mu.Lock()
	l := p.lifecycle
	p.mu.Unlock()
	return l != nil && l.Status().Exhausted
}

// dispatch captures one incarnation and cancels admitted work when it is
// revoked. A replacement never receives an old generation's queued call.
func (p *ChildPlugin) dispatch(ctx context.Context, method string, params any) (json.RawMessage, error) {
	p.mu.Lock()
	l, owner, active, gate := p.lifecycle, p.owner, p.active, p.gate
	p.mu.Unlock()
	if l == nil || active == nil || !l.IsCurrent(owner) {
		return nil, fmt.Errorf("pluginhost: %s not loaded: %w", p.ID(), ErrChildGone)
	}
	proc := l.Current()
	if proc == nil || !l.IsCurrent(owner) {
		return nil, ErrChildGone
	}
	if p.healthGate && method != subprocess.MethodHealth {
		if err := gate.Check(); err != nil {
			return nil, err
		}
	}
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(active, cancel)
	defer stop()
	defer cancel()
	if active.Err() != nil {
		return nil, ErrChildGone
	}
	raw, err := proc.Client().Conn().Call(callCtx, method, params)
	if active.Err() != nil && ctx.Err() == nil {
		return nil, errors.Join(ErrChildGone, err)
	}
	return raw, err
}
func (p *ChildPlugin) MCPCallTool(ctx context.Context, request subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
	raw, err := p.dispatch(ctx, subprocess.MethodMCPCallTool, request)
	if err != nil {
		return subprocess.MCPCallResult{}, err
	}
	var result subprocess.MCPCallResult
	err = json.Unmarshal(raw, &result)
	return result, err
}
func (p *ChildPlugin) HTTPHandle(ctx context.Context, request subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
	raw, err := p.dispatch(ctx, subprocess.MethodHTTPHandle, request)
	if err != nil {
		return subprocess.HTTPResponse{}, err
	}
	var result subprocess.HTTPResponse
	err = json.Unmarshal(raw, &result)
	return result, err
}
func (p *ChildPlugin) probeHealth(ctx context.Context) pluginHealth {
	p.mu.Lock()
	l, owner, gate, active := p.lifecycle, p.owner, p.gate, p.active
	p.mu.Unlock()
	if l == nil || gate == nil || active == nil || !l.IsCurrent(owner) {
		message := ErrChildGone.Error()
		if l != nil && l.Status().Exhausted {
			message += "; restart attempts exhausted"
		}
		return pluginHealth{message: message, checked: time.Now().UTC()}
	}
	probeCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(active, cancel)
	defer stop()
	defer cancel()
	v := gate.Probe(probeCtx)
	if !l.IsCurrent(owner) {
		message := ErrChildGone.Error()
		if l.Status().Exhausted {
			message += "; restart attempts exhausted"
		}
		return pluginHealth{message: message, checked: time.Now().UTC()}
	}
	return pluginHealth{ok: v.OK, message: v.Message, checked: v.Checked, reachable: v.Reachable}
}
func (p *ChildPlugin) info() subprocess.InitResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.identity
}
func (p *ChildPlugin) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status.LastError = err.Error()
}
func (p *ChildPlugin) failAndStop(err error) error {
	stopErr := p.Unload()
	p.fail(err)
	return errors.Join(err, stopErr)
}
func restartBackoff(attempt int) time.Duration {
	delay := restartInitialBackoff
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay >= restartMaxBackoff {
			return restartMaxBackoff
		}
	}
	return delay
}

var (
	_ plugin.Plugin          = (*ChildPlugin)(nil)
	_ subprocess.MCPHandler  = (*ChildPlugin)(nil)
	_ subprocess.HTTPHandler = (*ChildPlugin)(nil)
)

// RuntimeFailure describes unavailability after a successful activation. The
// library owns recovery; status sampling emits each terminal diagnostic once.
func (p *ChildPlugin) RuntimeFailure() string {
	p.mu.Lock()
	l, loadedAt := p.lifecycle, p.status.LoadedAt
	p.mu.Unlock()
	if l == nil || loadedAt.IsZero() {
		return ""
	}
	state := l.Status()
	if (state.State != driver.StateFailed && state.State != driver.StateQuarantined) || state.LastFailure == nil {
		return ""
	}
	if state.State == driver.StateFailed && state.LastFailure.Retryable && !state.Exhausted {
		return ""
	}
	text := state.LastFailure.Error()
	if state.LastFailure.Cause != nil {
		text += ": " + state.LastFailure.Cause.Error()
	}
	if state.Exhausted {
		text += "; restart attempts exhausted"
	}
	text = redactPluginDiagnostic(text)
	if len(text) > 2048 {
		text = strings.ToValidUTF8(text[:2048], "")
	}
	if text == "" {
		text = "pluginhost: failed after load"
	}
	p.mu.Lock()
	fresh := p.reportedFailure != text
	p.reportedFailure = text
	logger := p.logger
	p.mu.Unlock()
	if fresh && logger != nil {
		logger.Warn("pluginhost: plugin failed after load", "plugin", p.ID(), "reason", text)
	}
	return text
}

func absoluteChildSpec(spec ChildSpec) (ChildSpec, error) {
	command, err := filepath.Abs(spec.Command)
	if err != nil || strings.TrimSpace(spec.Command) == "" {
		return spec, fmt.Errorf("pluginhost: invalid plugin executable directory")
	}
	spec.Command = command
	dir := spec.WorkDir
	if dir == "" {
		dir = filepath.Dir(command)
	}
	spec.WorkDir, err = filepath.Abs(dir)
	if err != nil {
		return spec, fmt.Errorf("pluginhost: resolve plugin directory: %w", err)
	}
	return spec, nil
}

// Retain the typed library cause while exposing only sanitized display text.
type pluginDiagnosticError struct {
	cause error
	text  string
}

func (e *pluginDiagnosticError) Error() string { return e.text }
func (e *pluginDiagnosticError) Unwrap() error { return e.cause }
