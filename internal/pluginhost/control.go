package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	plugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
)

// ConfigureIntent opens the host-owned enabled-intent store. It holds booleans
// only: no plugin configuration, process environment or credentials.
func (h *Host) ConfigureIntent(path string) error {
	h.ops.Lock()
	defer h.ops.Unlock()
	f, err := os.Open(path)
	var data []byte
	if err == nil {
		data, err = io.ReadAll(io.LimitReader(f, 1<<20+1))
		err = errors.Join(err, f.Close())
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	intent := map[string]bool{}
	if err == nil {
		if len(data) > 1<<20 {
			return errors.New("pluginhost: enabled intent exceeds limit")
		}
		if err = json.Unmarshal(data, &intent); err != nil || intent == nil {
			return errors.New("pluginhost: invalid enabled intent")
		}
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
	enabled, known := h.intent[id]
	return !known || enabled
}

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
	if err := h.saveIntent(id, enabled); err != nil {
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

func (h *Host) saveIntent(id string, enabled bool) error {
	h.mu.Lock()
	intent := make(map[string]bool, len(h.intent)+1)
	for key, value := range h.intent {
		intent[key] = value
	}
	path := h.intentPath
	h.mu.Unlock()
	intent[id] = enabled
	if path != "" {
		data, err := json.Marshal(intent)
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		f, err := os.CreateTemp(dir, ".enabled-*")
		if err != nil {
			return err
		}
		name := f.Name()
		defer os.Remove(name)
		if _, err = f.Write(data); err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			return err
		}
		if err = os.Rename(name, path); err != nil {
			return err
		}
		directory, err := os.Open(dir)
		if err != nil {
			return err
		}
		err = errors.Join(directory.Sync(), directory.Close())
		if err != nil {
			return err
		}
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
