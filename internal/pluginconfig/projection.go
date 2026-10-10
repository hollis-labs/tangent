package pluginconfig

import (
	"context"
	"slices"
)

// Group projects only reviewed flat scalar declarations to kit-settings. It is
// host presentation metadata, not a plugin-issued capability or keychain value.
type Group struct {
	ID           string          `json:"id"`
	Label        string          `json:"label"`
	Schema       map[string]any  `json:"schema"`
	Fields       map[string]any  `json:"fields"`
	Capabilities map[string]bool `json:"capabilities"`
}

func (s *Store) Group(ctx context.Context, id string) (Group, error) {
	schema, err := s.Schema(ctx, id)
	if err != nil {
		return Group{}, err
	}
	properties := map[string]any{}
	fields := map[string]any{}
	required := []string{}
	field := func(key string, secret bool) {
		fields[key] = map[string]any{"editable": true, "secret": secret, "restart_required": true, "apply_target": id}
	}
	for key, f := range schema.Fields {
		kind := f.Type
		if kind == "select" {
			kind = "string"
		}
		p := map[string]any{"type": kind, "title": f.Label}
		if f.Label == "" {
			p["title"] = key
		}
		if f.Description != "" {
			p["description"] = f.Description
		}
		if f.Default != "" {
			p["default"], err = parseScalar(f, f.Default)
			if err != nil {
				return Group{}, err
			}
		}
		if f.Type == "select" {
			p["enum"] = f.Options
		}
		properties[key] = p
		field(key, false)
		if f.Required {
			required = append(required, key)
		}
	}
	for key, f := range schema.Secrets {
		label := f.Label
		if label == "" {
			label = key
		}
		p := map[string]any{"type": "string", "title": label, "writeOnly": true}
		if f.Description != "" {
			p["description"] = f.Description
		}
		properties[key] = p
		field(key, true)
		if f.Required {
			required = append(required, key)
		}
	}
	slices.Sort(required)
	return Group{ID: id, Label: id, Schema: map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}, Fields: fields, Capabilities: map[string]bool{"can_read": true, "can_validate": true, "can_update": len(fields) > 0, "can_reset": len(fields) > 0}}, nil
}
