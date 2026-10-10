package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"sync"

	plugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
)

// ToolRegistry is the live MCP surface. Hooks run under the registration lock;
// implementations must not call back into Host.
type ToolRegistry interface {
	AddPluginTool(MCPTool) error
	RemovePluginTools(...string)
}

// AttachToolRegistry installs existing declarations and subsequent mutations.
func (h *Host) AttachToolRegistry(registry ToolRegistry) error {
	if registry == nil {
		return errors.New("pluginhost: nil tool registry")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.toolRegistry != nil {
		return errors.New("pluginhost: tool registry already attached")
	}
	var added []string
	for _, tool := range h.tools {
		if err := registry.AddPluginTool(tool); err != nil {
			registry.RemovePluginTools(added...)
			return err
		}
		added = append(added, tool.Name)
	}
	h.toolRegistry = registry
	return nil
}

type registrationOwner struct {
	host   *Host
	id     string
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
	ready  bool
}

func newRegistrationOwner(h *Host, id string) *registrationOwner {
	ctx, cancel := context.WithCancel(h.ctx)
	return &registrationOwner{host: h, id: id, ctx: ctx, cancel: cancel}
}
func (h *Host) currentOwnerLocked(owner *registrationOwner) bool {
	return h.owners[owner.id] == owner && owner.ctx.Err() == nil && !h.unloaded
}

// trip closes admission immediately; teardown is serialized with reload and
// checks the exact owner again, so a late trip cannot stop a replacement.
func (o *registrationOwner) trip(err error) {
	o.once.Do(func() {
		h := o.host
		h.mu.Lock()
		if !h.currentOwnerLocked(o) {
			h.mu.Unlock()
			return
		}
		o.cancel()
		h.faults[o.id] = redactPluginDiagnostic(err.Error())
		h.mu.Unlock()
		go func() {
			h.ops.Lock()
			defer h.ops.Unlock()
			h.mu.Lock()
			same := h.owners[o.id] == o
			h.mu.Unlock()
			if same {
				if stopErr := h.unload(o.id); stopErr != nil {
					h.logger.Warn("pluginhost: circuit teardown failed", "plugin", o.id, "error", stopErr)
				}
			}
		}()
	})
}

// ownedHost captures the caller at Load, without inferring identity from a
// component/tool name or exposing identity minting to the plugin.
type ownedHost struct {
	*Host
	owner *registrationOwner
}

func (h *ownedHost) Context() context.Context { return h.owner.ctx }
func (h *ownedHost) RegisterUIComponent(c plugin.UIComponent) error {
	return h.registerUIComponent(c, h.owner)
}
func (h *ownedHost) RegisterMCPTool(t MCPTool) error     { return h.registerMCPTool(t, h.owner) }
func (h *ownedHost) RegisterHTTPRoute(r HTTPRoute) error { return h.registerHTTPRoute(r, h.owner) }
func registerChildTool(host plugin.Host, tool MCPTool) error {
	registrar, ok := host.(interface{ RegisterMCPTool(MCPTool) error })
	if !ok {
		return fmt.Errorf("pluginhost: missing tool registrar")
	}
	return registrar.RegisterMCPTool(tool)
}
func registerChildRoute(host plugin.Host, route HTTPRoute) error {
	registrar, ok := host.(interface{ RegisterHTTPRoute(HTTPRoute) error })
	if !ok {
		return fmt.Errorf("pluginhost: missing route registrar")
	}
	return registrar.RegisterHTTPRoute(route)
}

// HTTPRoute resolves the current registration, never a stale mux snapshot.
func (h *Host) HTTPRoute(method, path string) (HTTPRoute, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.routes[method+" "+path]
	return r, ok
}

func (o *registrationOwner) available() bool {
	h := o.host
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.currentOwnerLocked(o) && o.ready
}
