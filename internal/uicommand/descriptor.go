// Package uicommand implements the bounded, opt-in host channel of ADR 0014.
// It does not establish caller identity or grant agent access.
package uicommand

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	MaxDescriptorBytes = 32 << 10
	MaxArgumentsBytes  = 8 << 10
	MaxCommands        = 16
	MaxRows            = 32
	MaxSelected        = 64
)

type Scope string

const (
	Ephemeral Scope = "ephemeral"
	URLBacked Scope = "url-backed"
)

type Declaration struct {
	Name   string          `json:"name"`
	Scope  Scope           `json:"scope"`
	Schema json.RawMessage `json:"input_schema"`
}

type Filter struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type Row struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

type Descriptor struct {
	Version  int           `json:"version"`
	Route    string        `json:"route"`
	Filters  []Filter      `json:"active_filters,omitempty"`
	Search   string        `json:"search,omitempty"`
	Selected []string      `json:"selected_ids,omitempty"`
	Rows     []Row         `json:"visible_rows,omitempty"`
	Commands []Declaration `json:"available_commands,omitempty"`
}

var commandName = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)?$`)

// CoreCommands returns host-owned schemas and scopes. Views opt in by declaring
// their names; they cannot replace the schemas or change URL semantics.
func CoreCommands() []Declaration {
	return []Declaration{
		{"navigate", URLBacked, json.RawMessage(`{"type":"object","properties":{"route":{"type":"string","maxLength":2048}},"required":["route"],"additionalProperties":false}`)},
		core("open_modal"), core("close_modal"),
		core("open_drawer"), core("close_drawer"), core("focus_item"),
	}
}
func core(name string) Declaration {
	const key = "id"
	return Declaration{name, Ephemeral, json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"%s":{"type":"string","minLength":1,"maxLength":256}},"required":["%s"],"additionalProperties":false}`, key, key))}
}

func localRoute(route string) bool {
	return len(route) > 0 && len(route) <= 256 && strings.HasPrefix(route, "/") && !strings.HasPrefix(route, "//") && !strings.ContainsAny(route, "\\?#") && identifier(route, 256)
}

func decode(raw []byte, value any) error {
	if err := checkJSON(raw); err != nil {
		return err
	}
	if err := exactFields(raw, reflect.TypeOf(value)); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return errors.New("invalid JSON shape or field")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

// ValidateDescriptor detaches input and compiles a bounded local schema dialect.
// References, regular expressions, combinators and network schema loading are
// excluded so an untrusted declaration cannot start I/O or recursive validation.
func ValidateDescriptor(raw []byte) (Descriptor, map[string]*jsonschema.Schema, error) {
	var d Descriptor
	if len(raw) > MaxDescriptorBytes {
		return d, nil, errors.New("descriptor too large")
	}
	if err := decode(raw, &d); err != nil {
		return d, nil, err
	}
	if d.Version != 1 || !localRoute(d.Route) || len(d.Search) > 1024 || len(d.Filters) > 16 || len(d.Selected) > MaxSelected || len(d.Rows) > MaxRows || len(d.Commands) > MaxCommands {
		return d, nil, errors.New("invalid descriptor bounds or version")
	}
	names := map[string]bool{}
	for _, filter := range d.Filters {
		if !identifier(filter.Name, 64) || names[filter.Name] || len(filter.Values) == 0 || len(filter.Values) > 16 {
			return d, nil, errors.New("invalid active_filters")
		}
		names[filter.Name] = true
		for _, value := range filter.Values {
			if len(value) > 256 || !displayText(value) {
				return d, nil, errors.New("invalid filter value")
			}
		}
	}
	ids := map[string]bool{}
	for _, id := range d.Selected {
		if !identifier(id, 128) || ids[id] {
			return d, nil, errors.New("invalid selected_ids")
		}
		ids[id] = true
	}
	ids = map[string]bool{}
	for _, row := range d.Rows {
		if !identifier(row.ID, 128) || ids[row.ID] || len(row.Summary) > 512 {
			return d, nil, errors.New("invalid visible_rows")
		}
		ids[row.ID] = true
	}
	normalized, err := json.Marshal(d)
	if err != nil || len(normalized) > MaxDescriptorBytes {
		return d, nil, errors.New("normalized descriptor too large")
	}
	compiled := map[string]*jsonschema.Schema{}
	cores := map[string]Declaration{}
	for _, c := range CoreCommands() {
		cores[c.Name] = c
	}
	for i, c := range d.Commands {
		if len(c.Name) > 128 || !commandName.MatchString(c.Name) || (c.Scope != Ephemeral && c.Scope != URLBacked) {
			return d, nil, errors.New("invalid command declaration")
		}
		if _, exists := compiled[c.Name]; exists {
			return d, nil, errors.New("duplicate command")
		}
		if owned, ok := cores[c.Name]; ok {
			if c.Scope != owned.Scope {
				return d, nil, errors.New("core scope cannot be changed")
			}
			var supplied, expected any
			if err := json.Unmarshal(c.Schema, &supplied); err != nil {
				return d, nil, err
			}
			_ = json.Unmarshal(owned.Schema, &expected)
			a, _ := json.Marshal(supplied)
			b, _ := json.Marshal(expected)
			if !bytes.Equal(a, b) {
				return d, nil, errors.New("core schema cannot be changed")
			}
			c = owned
			d.Commands[i] = owned
		}
		schema, err := compileSchema(c.Schema)
		if err != nil {
			return d, nil, errors.New("invalid available_commands input_schema")
		}
		compiled[c.Name] = schema
	}
	return d, compiled, nil
}

func compileSchema(raw []byte) (*jsonschema.Schema, error) {
	if len(raw) == 0 {
		return nil, errors.New("invalid schema size")
	}
	var doc map[string]any
	if err := decode(raw, &doc); err != nil {
		return nil, err
	}
	normalized, err := json.Marshal(doc)
	if err != nil || len(normalized) > 2048 {
		return nil, errors.New("normalized schema too large")
	}
	if doc["type"] != "object" {
		return nil, errors.New("arguments schema must be an object")
	}
	if err := checkSchema(doc, 0); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	const uri = "urn:tangent:ui-command"
	if err := c.AddResource(uri, doc); err != nil {
		return nil, err
	}
	return c.Compile(uri)
}

func checkSchema(doc map[string]any, depth int) error {
	if depth > 6 || len(doc) > 12 {
		return errors.New("schema complexity limit")
	}
	for key, value := range doc {
		switch key {
		case "type":
			if value != "object" && value != "array" && value != "string" && value != "boolean" && value != "integer" && value != "number" {
				return errors.New("unsupported schema type")
			}
		case "properties":
			props, ok := value.(map[string]any)
			if !ok || len(props) > 32 {
				return errors.New("invalid schema properties")
			}
			for name, v := range props {
				child, ok := v.(map[string]any)
				if !ok || len(name) > 128 {
					return errors.New("invalid property schema")
				}
				if err := checkSchema(child, depth+1); err != nil {
					return err
				}
			}
		case "items":
			child, ok := value.(map[string]any)
			if !ok {
				return errors.New("invalid items schema")
			}
			if err := checkSchema(child, depth+1); err != nil {
				return err
			}
		case "additionalProperties":
			if value != false {
				return errors.New("additionalProperties must be false")
			}
		case "required", "enum", "minLength", "maxLength", "minItems", "maxItems", "minimum", "maximum":
			// The compiler validates keyword values; the outer byte bound caps enums.
		default:
			return fmt.Errorf("unsupported schema keyword %s", key)
		}
	}
	if doc["type"] == "object" && doc["additionalProperties"] != false {
		return errors.New("object schema must be closed")
	}
	return nil
}

func commandRoute(route string) bool {
	return len(route) > 0 && len(route) <= 2048 && strings.HasPrefix(route, "/") && !strings.HasPrefix(route, "//") && !strings.ContainsAny(route, "\\\r\n\x00") && identifier(route, 2048)
}
