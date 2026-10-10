package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"

	sdk "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/tangent/internal/pluginconfig"
)

type configApplication struct {
	id      string
	runtime pluginconfig.Runtime
}

// ConfigureConfig is composition-only and must precede plugin loading. The
// store stays private to the host, never exposed through the global SDK Host.
func (h *Host) ConfigureConfig(store *pluginconfig.Store) error {
	if store == nil {
		return pluginconfig.ErrRefused
	}
	h.ops.Lock()
	defer h.ops.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.configStore != nil || len(h.loaded) > 0 || h.unloaded {
		return pluginconfig.ErrRefused
	}
	h.configStore = store
	return nil
}
func (h *Host) resolveConfig(ctx context.Context, id string) (pluginconfig.Runtime, error) {
	h.mu.Lock()
	store, application := h.configStore, h.configApply
	h.mu.Unlock()
	if store == nil {
		return pluginconfig.Runtime{Values: map[string]string{}}, nil
	}
	if application != nil && application.id == id {
		// Refuse a revision changed while queued or before the actual plan. No new
		// save may silently replace the explicitly requested apply snapshot.
		current, err := store.Read(ctx, id, store.Scopes()[len(store.Scopes())-1])
		if err != nil {
			return pluginconfig.Runtime{}, err
		}
		if current.Revision != application.runtime.Revision {
			return pluginconfig.Runtime{}, pluginconfig.ErrConflict
		}
		return pluginconfig.Runtime{Values: maps.Clone(application.runtime.Values), Secrets: slices.Clone(application.runtime.Secrets), Revision: application.runtime.Revision}, nil
	}
	return store.Runtime(ctx, id, "")
}

// ResolveConfiguration binds a reviewed installed ID to the private store. It
// is used by the composition loader, never exposed through SDK Host.
func (h *Host) ResolveConfiguration(id string) func(context.Context) (pluginconfig.Runtime, error) {
	return func(ctx context.Context) (pluginconfig.Runtime, error) { return h.resolveConfig(ctx, id) }
}

// A scoped handle cannot use the composition resolver to read another plugin.
func (h *ownedHost) ResolveConfiguration(id string) func(context.Context) (pluginconfig.Runtime, error) {
	return func(ctx context.Context) (pluginconfig.Runtime, error) {
		h.mu.Lock()
		current := h.currentOwnerLocked(h.owner) && id == h.owner.id
		runtime := h.owner.config
		h.mu.Unlock()
		if !current || ctx.Err() != nil {
			return pluginconfig.Runtime{}, pluginconfig.ErrRefused
		}
		return pluginconfig.Runtime{Values: maps.Clone(runtime.Values), Secrets: slices.Clone(runtime.Secrets), Revision: runtime.Revision}, nil
	}
}

// ApplyConfig uses the lifecycle operation gate and one exact revision, unlike
// ordinary Reload which intentionally resolves the current configuration.
func (h *Host) ApplyConfig(ctx context.Context, id, revision string) error {
	if err := h.ops.acquire(ctx); err != nil {
		return err
	}
	defer h.ops.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	store, factory := h.configStore, h.factories[id]
	_, loaded := h.loaded[id]
	closing := h.unloaded
	h.mu.Unlock()
	if store == nil || factory == nil || closing || !h.DesiredEnabled(id) {
		return pluginconfig.ErrRefused
	}
	runtime, err := store.Runtime(ctx, id, revision)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.configApply = &configApplication{id, runtime}
	h.mu.Unlock()
	defer func() { h.mu.Lock(); h.configApply = nil; h.mu.Unlock() }()
	if loaded {
		if err = h.unload(id); err != nil {
			return err
		}
	}
	return h.loadFactory(id, factory)
}

func (h *Host) prepareOwnerConfig(owner *registrationOwner) error {
	h.mu.Lock()
	store := h.configStore
	h.mu.Unlock()
	if store == nil {
		return nil
	}
	if _, err := store.Schema(owner.ctx, owner.id); errors.Is(err, pluginconfig.ErrRefused) {
		return nil
	} else if err != nil {
		return err
	}
	runtime, err := h.resolveConfig(owner.ctx, owner.id)
	if err != nil {
		return err
	}
	owner.config = runtime
	return nil
}
func (h *Host) markOwnerConfig(p sdk.Plugin, owner *registrationOwner) {
	if child, ok := p.(*ChildPlugin); ok {
		child.configurationLoaded(owner.ctx)
		return
	}
	h.mu.Lock()
	store := h.configStore
	h.mu.Unlock()
	if store != nil && owner.config.Revision != "" {
		// A later save keeps pending=true. Activation cannot claim a newer revision.
		if err := store.MarkApplied(owner.ctx, owner.id, owner.config.Revision); err != nil && !errors.Is(err, pluginconfig.ErrConflict) {
			h.logger.Warn("plugin config activation remains pending", "plugin", owner.id)
		}
	}
}
func (h *ownedHost) GetConfig(key string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.currentOwnerLocked(h.owner) {
		return "", pluginconfig.ErrRefused
	}
	value, ok := h.owner.config.Values[key]
	if !ok {
		return "", pluginconfig.ErrRefused
	}
	return value, nil
}
func (h *ownedHost) SetConfig(key, value string) error {
	h.mu.Lock()
	store, current := h.configStore, h.currentOwnerLocked(h.owner)
	h.mu.Unlock()
	if !current || store == nil {
		return pluginconfig.ErrRefused
	}
	return store.ChangeString(h.owner.ctx, h.owner.id, key, value)
}
func (h *ownedHost) RegisterConfigSchema(fields []sdk.ConfigFieldDef) error {
	h.mu.Lock()
	store, current := h.configStore, h.currentOwnerLocked(h.owner)
	h.mu.Unlock()
	if !current || store == nil {
		return pluginconfig.ErrRefused
	}
	schema, err := store.Schema(h.owner.ctx, h.owner.id)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, field := range fields {
		if seen[field.Key] || field.Component != "" {
			return pluginconfig.ErrRefused
		}
		seen[field.Key] = true
		expected := sdk.ConfigFieldDef{Key: field.Key}
		if declared, ok := schema.Fields[field.Key]; ok {
			kind := declared.Type
			if kind == "boolean" {
				kind = "bool"
			}
			if kind == "integer" {
				kind = "int"
			}
			expected.Type, expected.Label, expected.Description, expected.Required, expected.Options = kind, declared.Label, declared.Description, declared.Required, declared.Options
			if declared.Default != "" {
				expected.Default = ownerDefault(declared.Type, declared.Default)
			}
		} else if declared, ok := schema.Secrets[field.Key]; ok {
			expected.Type, expected.Label, expected.Description, expected.Required = "secret", declared.Label, declared.Description, declared.Required
		} else {
			return pluginconfig.ErrRefused
		}
		actualRaw, e := json.Marshal(field)
		if e != nil {
			return pluginconfig.ErrRefused
		}
		expectedRaw, e := json.Marshal(expected)
		if e != nil || string(actualRaw) != string(expectedRaw) {
			return pluginconfig.ErrRefused
		}
	}
	return nil
}

func scrubConfigText(text string, secrets []string) string {
	for _, value := range secrets {
		if value != "" {
			text = strings.ReplaceAll(text, value, "[redacted]")
		}
	}
	return redactPluginDiagnostic(text)
}

func ownerDefault(kind, text string) any {
	switch kind {
	case "boolean":
		return text == "true"
	case "integer":
		value, _ := strconv.ParseInt(text, 10, 64)
		return value
	case "number":
		value, _ := strconv.ParseFloat(text, 64)
		return value
	default:
		return text
	}
}

func (p *ChildPlugin) configurationLoaded(ctx context.Context) {
	p.mu.Lock()
	p.configLoaded = true
	apply := p.configApply
	p.mu.Unlock()
	p.persistConfiguration(ctx, apply)
}
func (p *ChildPlugin) configurationActivated(ctx context.Context) {
	p.mu.Lock()
	loaded, apply := p.configLoaded, p.configApply
	p.mu.Unlock()
	if loaded {
		p.persistConfiguration(ctx, apply)
	}
}
func (p *ChildPlugin) persistConfiguration(ctx context.Context, apply func(context.Context) error) {
	if apply == nil {
		return
	}
	if err := apply(ctx); err != nil && !errors.Is(err, pluginconfig.ErrConflict) {
		p.mu.Lock()
		logger := p.logger
		p.mu.Unlock()
		if logger != nil {
			logger.Warn("plugin config activation remains pending", "plugin", p.ID())
		}
	}
}
func (p *ChildPlugin) redactFailure(text string) string {
	p.mu.Lock()
	secrets := slices.Clone(p.configSecrets)
	p.mu.Unlock()
	return scrubConfigText(text, secrets)
}
