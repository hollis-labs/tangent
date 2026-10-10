package uicommand

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

// checkJSON rejects ambiguous/lossy JSON before typed decoding: duplicate keys,
// invalid UTF-8/surrogates, and excessive value count/nesting. Numbers stay
// json.Number throughout this pass; descriptive schema literals are not rounded.
func checkJSON(raw []byte) error {
	if !utf8.Valid(raw) || !validEscapes(raw) {
		return errors.New("invalid JSON Unicode")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	nodes := 0
	if err := walkJSON(d, 0, &nodes, false); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
func walkJSON(d *json.Decoder, depth int, nodes *int, schema bool) error {
	*nodes++
	if depth > 16 || *nodes > 2048 {
		return errors.New("JSON depth or node limit")
	}
	token, err := d.Token()
	if err != nil {
		return errors.New("invalid JSON")
	}
	if token == nil && !schema {
		return errors.New("null observation field")
	}
	if text, ok := token.(string); ok && !displayText(text) {
		return errors.New("non-printing JSON text")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			keyToken, err := d.Token()
			if err != nil {
				return errors.New("invalid JSON object")
			}
			key, ok := keyToken.(string)
			if !ok || keys[key] || !displayText(key) {
				return errors.New("invalid or duplicate JSON key")
			}
			keys[key] = true
			if err := walkJSON(d, depth+1, nodes, schema || key == "input_schema"); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := walkJSON(d, depth+1, nodes, schema); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	if _, err := d.Token(); err != nil {
		return errors.New("invalid JSON closing delimiter")
	}
	return nil
}
func validEscapes(raw []byte) bool {
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, ok := hexQuad(raw[i+1 : i+5])
		if !ok {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, ok := hexQuad(raw[i+3 : i+7])
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return !inString
}
func hexQuad(raw []byte) (uint16, bool) {
	var n uint16
	for _, c := range raw {
		n <<= 4
		switch {
		case c >= '0' && c <= '9':
			n += uint16(c - '0')
		case c >= 'a' && c <= 'f':
			n += uint16(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			n += uint16(c - 'A' + 10)
		default:
			return 0, false
		}
	}
	return n, true
}
func displayText(s string) bool {
	for _, r := range s {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}
func identifier(s string, bound int) bool {
	if len(s) == 0 || len(s) > bound || !displayText(s) {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsSpace)
}

// DecodeFrame validates a bounded UI wire frame before strict typed decoding.
func DecodeFrame(raw []byte, value any) error {
	if len(raw) > MaxDescriptorBytes+1024 {
		return errors.New("UI frame too large")
	}
	return decode(raw, value)
}

// exactFields closes encoding/json's case-insensitive struct-field matching.
// Wire contracts use exact snake_case keys, including nested rows/commands.
func exactFields(raw []byte, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return errors.New("invalid JSON object")
		}
		known := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			key := strings.Split(field.Tag.Get("json"), ",")[0]
			if key != "" && key != "-" {
				known[key] = field.Type
			}
		}
		for key, value := range fields {
			fieldType, ok := known[key]
			if !ok {
				return errors.New("unknown JSON field")
			}
			if err := exactFields(value, fieldType); err != nil {
				return err
			}
		}
		if t == reflect.TypeFor[Row]() {
			if fields["summary"] == nil {
				return errors.New("visible_rows summary is required")
			}
		}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return errors.New("invalid JSON array")
		}
		for _, value := range values {
			if err := exactFields(value, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
