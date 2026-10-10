package pluginconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"

	sdk "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
)

var keyName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]*$`)
var envName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type Schema struct {
	Fields  map[string]sdk.Field  `json:"fields"`
	Secrets map[string]sdk.Secret `json:"secrets"`
	Digest  string                `json:"digest"`
}

func review(config sdk.Config) (Schema, error) {
	// Decode a detached copy; the installer retains custody of its manifest.
	raw, err := json.Marshal(config)
	if err != nil {
		return Schema{}, ErrRefused
	}
	var cloned sdk.Config
	if err = json.Unmarshal(raw, &cloned); err != nil {
		return Schema{}, ErrRefused
	}
	for key, f := range cloned.Fields {
		if !validID(key) || !keyName.MatchString(key) || (f.Env != "" && !envName.MatchString(f.Env)) {
			return Schema{}, ErrRefused
		}
		if _, exists := cloned.Secrets[key]; exists {
			return Schema{}, ErrRefused
		}
		switch f.Type {
		case "string", "boolean", "integer", "number", "select":
		default:
			return Schema{}, ErrRefused
		}
		if f.Type == "select" && len(f.Options) == 0 || f.Type != "select" && len(f.Options) > 0 {
			return Schema{}, ErrRefused
		}
		seen := map[string]bool{}
		for _, option := range f.Options {
			if option == "" || seen[option] {
				return Schema{}, ErrRefused
			}
			seen[option] = true
		}
		if f.Default != "" {
			if _, err := parseScalar(f, f.Default); err != nil {
				return Schema{}, ErrRefused
			}
		}
	}
	for key, secret := range cloned.Secrets {
		if !validID(key) || !keyName.MatchString(key) || (secret.Env != "" && !envName.MatchString(secret.Env)) {
			return Schema{}, ErrRefused
		}
	}
	sum := sha256.Sum256(raw)
	return Schema{Fields: cloned.Fields, Secrets: cloned.Secrets, Digest: hex.EncodeToString(sum[:])}, nil
}
func parseScalar(f sdk.Field, s string) (any, error) {
	switch f.Type {
	case "string":
		return s, nil
	case "select":
		if slices.Contains(f.Options, s) {
			return s, nil
		}
	case "boolean":
		if s == "true" {
			return true, nil
		}
		if s == "false" {
			return false, nil
		}
	case "integer":
		n, err := strconv.ParseInt(s, 10, 64)
		if err == nil && n >= -9007199254740991 && n <= 9007199254740991 {
			return n, nil
		}
	case "number":
		if s == "" || (s[0] != '-' && (s[0] < '0' || s[0] > '9')) {
			return nil, ErrRefused
		}
		var n json.Number
		if json.Unmarshal([]byte(s), &n) == nil {
			v, err := n.Float64()
			if err == nil && !math.IsInf(v, 0) && !math.IsNaN(v) {
				return v, nil
			}
		}
	}
	return nil, ErrRefused
}
func encodeScalar(f sdk.Field, v any) (string, error) {
	switch f.Type {
	case "string", "select":
		s, ok := v.(string)
		if ok && len(s) <= 65536 {
			_, err := parseScalar(f, s)
			return s, err
		}
	case "boolean":
		b, ok := v.(bool)
		if ok {
			return strconv.FormatBool(b), nil
		}
	case "integer", "number":
		// Requests decode with UseNumber. Do not round large JSON integers through float64.
		var text string
		switch n := v.(type) {
		case json.Number:
			text = n.String()
		case int:
			text = strconv.Itoa(n)
		case int64:
			text = strconv.FormatInt(n, 10)
		case float64:
			if !math.IsNaN(n) && !math.IsInf(n, 0) {
				text = strconv.FormatFloat(n, 'g', -1, 64)
			}
		}
		if text != "" {
			_, err := parseScalar(f, text)
			return text, err
		}
	}
	return "", ErrRefused
}
func fieldError(key, code string) FieldError {
	return FieldError{Path: "/" + key, Code: code, Message: fmt.Sprintf("Setting %s is %s.", key, code)}
}
