// Package pluginconfig owns reviewed plugin settings. A scope or schema is not
// an authority grant; only host composition registers schemas and active scopes.
package pluginconfig

import (
	"context"
	"errors"
	"strings"
	"unicode"
)

var (
	ErrRefused  = errors.New("plugin config refused")
	ErrConflict = errors.New("plugin config revision changed")
	ErrSecret   = errors.New("plugin keychain unavailable")
)

type Scope struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func (s Scope) valid() bool {
	if s.Kind != "client" && s.Kind != "environment" && s.Kind != "project" {
		return false
	}
	return validID(s.ID)
}
func validID(s string) bool {
	return len(s) > 0 && len(s) <= 128 && strings.TrimSpace(s) == s && !strings.ContainsFunc(s, unicode.IsControl)
}

// Secrets is backed by the host OS keychain in production. Accounts are opaque
// revision-specific references; values are never put in SQLite or snapshots.
type Secrets interface {
	Get(context.Context, string) (string, error)
	Set(context.Context, string, string) error
	Delete(context.Context, string) error
}

type Value struct {
	Present       bool   `json:"present"`
	Value         any    `json:"value,omitempty"`
	SecretPresent *bool  `json:"secret_present,omitempty"`
	Editable      bool   `json:"editable"`
	HasOverride   bool   `json:"has_override"`
	Source        string `json:"source,omitempty"`
}
type Snapshot struct {
	PluginID       string           `json:"plugin_id"`
	Scope          Scope            `json:"scope"`
	Revision       string           `json:"revision"`
	SchemaDigest   string           `json:"schema_digest"`
	Values         map[string]Value `json:"values"`
	PendingRestart bool             `json:"pending_restart"`
}
type Changes struct {
	Set   map[string]any `json:"set"`
	Unset []string       `json:"unset"`
}

// FieldError never includes submitted values, provider errors or secret bytes.
type FieldError struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Validation struct {
	Valid  bool         `json:"valid"`
	Errors []FieldError `json:"errors"`
}
