// Package settings reads the app's own "tangent" block from the manifest config. The shell
// modules read theirs; this block holds what only Tangent has.
//
//	tangent:
//	  allowed_hosts: []   # extra Host values (besides loopback) served behind a local TLS proxy
//
// An unknown key or a bad value is refused at start with a message that names the key.
package settings

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// Key is the name of the config block.
const Key = "tangent"

// Settings are the parsed block.
type Settings struct {
	AllowedHosts []string
}

// Parse reads the block (nil for a missing one).
func Parse(raw any) (Settings, error) {
	var s Settings
	if raw == nil {
		return s, nil
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return s, fmt.Errorf("tangent: the config block must be a map")
	}
	var unknown []string
	for k := range block {
		if k != "allowed_hosts" {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return s, fmt.Errorf("tangent: unknown config key(s): %s", strings.Join(unknown, ", "))
	}
	switch hosts := block["allowed_hosts"].(type) {
	case nil:
	case []any:
		for _, h := range hosts {
			t, ok := h.(string)
			if !ok {
				return s, fmt.Errorf("tangent.allowed_hosts must be a list of strings")
			}
			t = strings.ToLower(strings.TrimSpace(t))
			if t == "" || strings.ContainsAny(t, "/@ ") {
				return s, fmt.Errorf("tangent.allowed_hosts: %q is not a host name", t)
			}
			if _, _, err := net.SplitHostPort(t); err != nil && strings.Contains(t, ":") {
				return s, fmt.Errorf("tangent.allowed_hosts: %q is not a host name", t)
			}
			s.AllowedHosts = append(s.AllowedHosts, t)
		}
	default:
		return s, fmt.Errorf("tangent.allowed_hosts must be a list of strings")
	}
	return s, nil
}
