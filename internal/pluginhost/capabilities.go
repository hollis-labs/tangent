package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
	capHost "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability/host"
)

const (
	// ToolReadCapability describes the native owned-handle adapter only. It is
	// not a substitute for mcp.reach's connection-bound reverse transport.
	ToolReadCapability   = "host.tangent.tools.read"
	toolReadOperation    = "tangent/tools/read"
	toolAudience         = "tangent-owned-tools"
	maxToolRequestBytes  = 32 << 10
	maxToolResponseBytes = 1 << 20
)

var ownedToolName = regexp.MustCompile(`^tangent\.[a-z0-9_]{1,120}$`)

// ToolAccess is observed by the host, not supplied as an identity by the plugin.
// Arguments is a detached JSON snapshot for resource/effect policy validation.
type ToolAccess struct {
	Runtime   capability.RuntimeIdentity
	Tool      string
	Arguments json.RawMessage
}

// ToolAuthorization is current reviewed host policy. The provider must verify
// that this concrete tool/resource operation is read-only, authenticate any
// initiating caller from a trusted host binding, and return a cancellable lease.
// A manifest, MCP label, cookie forwarded by a plugin, or unsafe-install flag is
// not such a binding. Background must be explicitly approved, never inferred.
type ToolAuthorization struct {
	Authority     capHost.Authority
	ResponseBytes int64
}

// ToolCapabilityProvider returns fresh immutable snapshots at admission and
// again before/after the read. It must honor ctx, return promptly and be safe for
// concurrent use. The current production composition has no approved provider.
type ToolCapabilityProvider interface {
	AuthorizeTool(context.Context, ToolAccess) (ToolAuthorization, error)
}

type ToolCapabilityConfig struct {
	Provider ToolCapabilityProvider
	Budget   capHost.Budget
	Audit    capHost.AuditSink
	Clock    capHost.Clock
}

type toolCapabilityBoundary struct {
	config   ToolCapabilityConfig
	catalog  *capability.Catalog
	audit    *capHost.Auditor
	requests atomic.Uint64
}

// ToolCapabilityCatalog closes the adapter to reads and exact tool targets.
// Its discovery descriptor grants nothing; writes lack backend commit coupling
// and are deliberately unsupported.
func ToolCapabilityCatalog() (*capability.Catalog, error) {
	return capability.NewCatalog(nil, []capability.Descriptor{{
		Name: ToolReadCapability, SchemaVersion: 1,
		Description:   "Read approved Tangent tools through an exact native load owner.",
		EffectCeiling: capability.Read, Operations: []string{toolReadOperation},
		ScopeSchema: capability.ScopeSchema{
			Allowlists: []string{"operations", "targets", "effects"},
			Limits:     []string{"request_bytes", "response_bytes", "deadline_ms", "concurrency"},
		},
	}})
}

// ConfigureToolCapabilities is a composition-root port, fixed before any load.
// No plugin-scoped handle may install a policy or replace the tool backend.
func (h *Host) ConfigureToolCapabilities(config ToolCapabilityConfig) error {
	if config.Provider == nil || config.Budget == nil {
		return toolRefusal(capability.CapabilityDenied)
	}
	catalog, err := ToolCapabilityCatalog()
	if err != nil {
		return err
	}
	if config.Audit == nil {
		config.Audit = capabilityLogSink{host: h}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.unloaded || len(h.attempts) != 0 || len(h.owners) != 0 || h.toolCapabilities != nil {
		return toolRefusal(capability.Conflict)
	}
	h.toolCapabilities = &toolCapabilityBoundary{config: config, catalog: catalog, audit: capHost.NewAuditor(config.Audit)}
	return nil
}

func (*ownedHost) ConfigureToolCapabilities(ToolCapabilityConfig) error {
	return toolRefusal(capability.CapabilityDenied)
}
func (*ownedHost) AttachToolCaller(ToolCaller) error {
	return toolRefusal(capability.CapabilityDenied)
}

func (h *Host) prepareToolCapabilityOwner(owner *registrationOwner) error {
	generation, err := h.capabilityGenerations.Next(owner.ctx, h.toolHostInstance, owner.id)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.currentOwnerLocked(owner) {
		return ErrPluginNotLoaded
	}
	owner.runtime = capability.RuntimeIdentity{HostInstance: h.toolHostInstance, OwnerID: owner.id, OwnerGeneration: generation}
	// Always retain bounded denial counters, even without a provider or sink.
	if h.toolCapabilities == nil {
		catalog, catalogErr := ToolCapabilityCatalog()
		if catalogErr != nil {
			return catalogErr
		}
		h.toolCapabilities = &toolCapabilityBoundary{catalog: catalog, audit: capHost.NewAuditor(capabilityLogSink{host: h})}
	}
	return nil
}

type capabilityLogSink struct{ host *Host }

func (s capabilityLogSink) Record(ctx context.Context, event capHost.AuditEvent) error {
	if event.Outcome != "" {
		// Fixed vocabulary only. Even valid-looking labels may contain secrets;
		// do not log target/caller/grant identifiers or raw provider failures.
		s.host.logger.WarnContext(ctx, "pluginhost: tool capability refused", "capability", ToolReadCapability, "code", event.Outcome, "reason", event.Reason)
	}
	return nil
}

// CapabilityDenials exposes only closed error-code/reason counters, never
// arguments, result bodies, raw provider errors, tokens or caller labels.
func (h *Host) CapabilityDenials() map[capHost.DenialKey]uint64 {
	h.mu.Lock()
	boundary := h.toolCapabilities
	h.mu.Unlock()
	if boundary == nil {
		return map[capHost.DenialKey]uint64{}
	}
	return boundary.audit.Denials()
}

func (h *ownedHost) Tools() (ToolCaller, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.currentOwnerLocked(h.owner) {
		return nil, ErrPluginNotLoaded
	}
	if h.toolCaller == nil {
		return nil, ErrToolCallerUnavailable
	}
	return &ownedToolCaller{host: h.Host, owner: h.owner}, nil
}

type ownedToolCaller struct {
	host  *Host
	owner *registrationOwner
}

func toolRefusal(code capability.Code) *capability.Error {
	return &capability.Error{Code: code, Capability: ToolReadCapability, EffectState: capability.NotStarted}
}

func (c *ownedToolCaller) CallTool(ctx context.Context, name string, arguments any) (result ToolResult, err error) {
	h := c.host
	h.mu.Lock()
	boundary, backend := h.toolCapabilities, h.toolCaller
	current := h.currentOwnerLocked(c.owner) && c.owner.ready
	runtime := c.owner.runtime
	h.mu.Unlock()
	// Audit early failures through the same closed vocabulary as the enforcer.
	enforced := false
	defer func() {
		if recover() != nil {
			err = toolRefusal(capability.InternalError)
		}
		if err != nil {
			result = ToolResult{}
			if !enforced && boundary != nil {
				var failure *capability.Error
				if !errors.As(err, &failure) {
					failure = toolRefusal(capability.InternalError)
					err = failure
				}
				boundary.audit.Record(context.Background(), capHost.AuditEvent{Capability: ToolReadCapability, Outcome: failure.Code, EffectState: capability.NotStarted})
			}
		}
	}()
	if ctx == nil {
		return result, toolRefusal(capability.InvalidRequest)
	}
	if ctx.Err() != nil {
		return result, toolRefusal(capability.Cancelled)
	}
	if !current || backend == nil {
		return result, toolRefusal(capability.TargetUnavailable)
	}
	if boundary == nil || boundary.config.Provider == nil || boundary.config.Budget == nil {
		return result, toolRefusal(capability.CapabilityDenied)
	}
	if !ownedToolName.MatchString(name) {
		return result, toolRefusal(capability.InvalidRequest)
	}
	raw, marshalErr := json.Marshal(arguments)
	if marshalErr != nil || len(raw) > maxToolRequestBytes {
		return result, toolRefusal(capability.InvalidRequest)
	}
	callCtx, cancel := context.WithTimeout(ctx, dispatchBudget)
	defer cancel()
	stopOwner := context.AfterFunc(c.owner.ctx, cancel)
	defer stopOwner()
	request := ToolAccess{Runtime: runtime, Tool: name, Arguments: raw}
	resolver := &toolAuthorityResolver{caller: c, boundary: boundary, request: request}
	authorization, authErr := resolver.authorize(callCtx)
	if authErr != nil {
		return result, authErr
	}
	requestID := boundary.requests.Add(1)
	if requestID > capability.MaxSafeInteger {
		return result, toolRefusal(capability.InvalidRequest)
	}
	call := capHost.Call{
		Capability: ToolReadCapability, GrantID: authorization.Authority.Grant.GrantID,
		Operation: toolReadOperation, Target: name, Effect: capability.Read, RequestID: capability.RequestID(requestID),
		Usage: map[string]int64{"request_bytes": int64(len(raw)), "response_bytes": authorization.ResponseBytes, "deadline_ms": dispatchBudget.Milliseconds(), "concurrency": 1},
	}
	resolver.grantID, resolver.responseBytes = call.GrantID, authorization.ResponseBytes
	if deadline, ok := callCtx.Deadline(); ok {
		call.Usage["deadline_ms"] = max(1, (time.Until(deadline) + time.Millisecond - 1).Milliseconds())
	}
	enforcer, makeErr := capHost.NewEnforcer(capHost.EnforcerConfig{
		HostInstance: h.toolHostInstance, Audience: toolAudience, Catalog: boundary.catalog,
		Resolver: resolver, Budget: boundary.config.Budget, Audit: boundary.audit, Clock: boundary.config.Clock,
	})
	if makeErr != nil {
		return result, makeErr
	}
	enforced = true
	err = enforcer.Run(callCtx, call, func(readCtx context.Context, permit *capHost.Permit) error {
		var readErr error
		result, readErr = backend.CallTool(readCtx, name, json.RawMessage(append([]byte(nil), raw...)))
		if readErr != nil {
			return readErr
		}
		// Reads must still be authorized when their contents are disclosed. A
		// read callback cannot safely serve writes lacking atomic CheckCommit.
		if checkErr := permit.Recheck(); checkErr != nil {
			return completedReadRefusal(checkErr)
		}
		if int64(len(result.Content)) > authorization.ResponseBytes {
			return completedReadRefusal(toolRefusal(capability.BudgetExceeded))
		}
		result.Content = append(json.RawMessage(nil), result.Content...)
		return nil
	})
	return result, err
}

func completedReadRefusal(err error) error {
	var failure *capability.Error
	if !errors.As(err, &failure) {
		failure = toolRefusal(capability.InternalError)
	}
	classified := *failure
	// The backend has already run; refusal prevents disclosure but must not
	// claim that no callback was performed.
	classified.EffectState = capability.Unknown
	return &classified
}

type toolAuthorityResolver struct {
	caller        *ownedToolCaller
	boundary      *toolCapabilityBoundary
	request       ToolAccess
	grantID       string
	responseBytes int64
}

func (r *toolAuthorityResolver) authorize(ctx context.Context) (out ToolAuthorization, err error) {
	defer func() {
		if recover() != nil {
			out = ToolAuthorization{}
			err = toolRefusal(capability.InternalError)
		}
	}()
	if ctx.Err() != nil {
		return out, toolRefusal(capability.Cancelled)
	}
	if !r.caller.owner.available() {
		return out, toolRefusal(capability.TargetUnavailable)
	}
	request := r.request
	request.Arguments = append(json.RawMessage(nil), request.Arguments...)
	out, err = r.boundary.config.Provider.AuthorizeTool(ctx, request)
	if err != nil {
		return ToolAuthorization{}, toolRefusal(capability.CapabilityDenied)
	}
	if ctx.Err() != nil {
		return ToolAuthorization{}, toolRefusal(capability.Cancelled)
	}
	if !r.caller.owner.available() {
		return ToolAuthorization{}, toolRefusal(capability.TargetUnavailable)
	}
	if out.Authority.Owner != request.Runtime || out.Authority.Actor != (capHost.Subject{Kind: capHost.PluginActor, ID: request.Runtime.OwnerID}) {
		return ToolAuthorization{}, toolRefusal(capability.Unauthenticated)
	}
	if out.ResponseBytes < 1 || out.ResponseBytes > maxToolResponseBytes {
		return ToolAuthorization{}, toolRefusal(capability.InvalidRequest)
	}
	return out, nil
}

func (r *toolAuthorityResolver) Resolve(ctx context.Context, _ capHost.Call) (capHost.Authority, error) {
	out, err := r.authorize(ctx)
	if err != nil {
		return capHost.Authority{}, err
	}
	if out.Authority.Grant.GrantID != r.grantID || out.ResponseBytes != r.responseBytes {
		return capHost.Authority{}, toolRefusal(capability.Conflict)
	}
	return out.Authority, nil
}

// Assert the provider's budget dependency rather than supplying a permissive
// fallback. The shared helper owns cancellation, expiry and reservation release.
var _ capHost.AuthorityResolver = (*toolAuthorityResolver)(nil)
var _ ToolCaller = (*ownedToolCaller)(nil)
