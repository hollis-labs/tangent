package pluginhost

import (
	"context"
	"errors"
	"fmt"

	plugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/tangent/internal/pluginintent"
)

// ConfigureIntent opens the host-owned enabled-intent store. It holds booleans
// only: no plugin configuration, process environment or credentials.
func (h *Host) ConfigureIntent(path string) error {
	h.ops.Lock()
	defer h.ops.Unlock()
	intent, err := pluginintent.Read(path)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.intentPath != "" || len(h.loaded) > 0 {
		return errors.New("pluginhost: intent must be configured before loading")
	}
	h.intentPath = path
	h.intent = intent
	return nil
}

// RegisterFactory records an installed artifact's loader. Each enable/reload
// must obtain a fresh snapshot; it cannot reuse a removed executable snapshot.
func (h *Host) RegisterFactory(id string, factory func() (plugin.Plugin, error)) error {
	if id == "" || factory == nil {
		return errors.New("pluginhost: invalid plugin factory")
	}
	h.ops.Lock()
	defer h.ops.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.factories[id]; exists {
		return ErrDuplicatePlugin
	}
	h.factories[id] = factory
	return nil
}

func (h *Host) DesiredEnabled(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.intent[id]
}

// Lifecycle management is an operator/composition action, never authority
// acquired by asserting an interface on an embedded scoped SDK handle.
func (*ownedHost) SetEnabled(context.Context, string, bool) error { return ErrSurfaceNotHonored }
func (*ownedHost) Reload(context.Context, string) error           { return ErrSurfaceNotHonored }

// SetEnabled serializes durable operator intent and actual lifecycle work.
// Failed startup remains enabled-but-failed in Inventory, never reported ready.
func (h *Host) SetEnabled(ctx context.Context, id string, enabled bool) error {
	if err := h.ops.acquire(ctx); err != nil {
		return err
	}
	defer h.ops.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	factory, known := h.factories[id]
	_, loaded := h.loaded[id]
	closing := h.unloaded
	h.mu.Unlock()
	if !known {
		return fmt.Errorf("pluginhost: unknown installed plugin %q", id)
	}
	if closing {
		return ErrHostShuttingDown
	}
	if err := h.saveIntent(ctx, id, enabled); err != nil {
		return err
	}
	if !enabled {
		if loaded {
			return h.unload(id)
		}
		return nil
	}
	if loaded {
		return nil
	}
	return h.loadFactory(id, factory)
}

// Reload stops and sweeps the old owner before resolving a new bundle. A
// failed replacement stays failed; no old process or stale route is revived.
func (h *Host) Reload(ctx context.Context, id string) error {
	if err := h.ops.acquire(ctx); err != nil {
		return err
	}
	defer h.ops.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	factory, known := h.factories[id]
	_, loaded := h.loaded[id]
	closing := h.unloaded
	h.mu.Unlock()
	if !known {
		return fmt.Errorf("pluginhost: unknown installed plugin %q", id)
	}
	if closing {
		return ErrHostShuttingDown
	}
	if !h.DesiredEnabled(id) {
		return fmt.Errorf("pluginhost: %s is disabled", id)
	}
	if loaded {
		if err := h.unload(id); err != nil {
			return err
		}
	}
	return h.loadFactory(id, factory)
}

func (h *Host) loadFactory(id string, factory func() (plugin.Plugin, error)) error {
	p, err := factory()
	if err == nil {
		if p == nil || p.ID() != id {
			err = errors.New("pluginhost: factory identity mismatch")
		} else {
			err = h.load(p)
		}
	}
	if err != nil {
		h.mu.Lock()
		h.faults[id] = redactPluginDiagnostic(err.Error())
		h.mu.Unlock()
		return err
	}
	return nil
}

func (h *Host) saveIntent(ctx context.Context, id string, enabled bool) error {
	h.mu.Lock()
	path := h.intentPath
	if path == "" {
		h.intent[id] = enabled
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()
	intent, err := pluginintent.Set(ctx, path, id, enabled)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.intent = intent
	h.mu.Unlock()
	return nil
}

// operationGate permits callers to abandon queued management work before it
// acquires lifecycle ownership. An abandoned operation never runs later.
type operationGate chan struct{}

func (g operationGate) Lock()   { g <- struct{}{} }
func (g operationGate) Unlock() { <-g }
func (g operationGate) acquire(ctx context.Context) error {
	select {
	case g <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
