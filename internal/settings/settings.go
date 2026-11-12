// Package settings reads the app's own "tangent" block from the manifest config. The shell
// modules read theirs; this block holds what only Tangent has.
//
//	tangent:
//	  allowed_hosts: []        # extra Host values (besides loopback) served behind a local TLS proxy
//	  gateway_producers: []    # API key names that are gateway producers (tether-gateway, a machine's gateway)
//	  trusted_forwarders: []   # the producers among them trusted to attest the session they forward for
//
// An unknown key or a bad value is refused at start with a message that names the key.
package settings

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
)

// Key is the name of the config block.
const Key = "tangent"

// Settings are the parsed block.
type Settings struct {
	AllowedHosts      []string
	GatewayProducers  []string
	TrustedForwarders []string
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
		if k != "allowed_hosts" && k != "gateway_producers" && k != "trusted_forwarders" {
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
	var err error
	if s.GatewayProducers, err = names(block, "gateway_producers"); err != nil {
		return s, err
	}
	if s.TrustedForwarders, err = names(block, "trusted_forwarders"); err != nil {
		return s, err
	}
	return s, nil
}

// names reads a list of lowercase key names.
func names(block map[string]any, key string) ([]string, error) {
	var out []string
	switch list := block[key].(type) {
	case nil:
	case []any:
		for _, v := range list {
			n, ok := v.(string)
			if !ok || !keyName.MatchString(n) {
				return nil, fmt.Errorf("tangent.%s: every entry must be a key name (lowercase letters, digits, - and _)", key)
			}
			out = append(out, n)
		}
	default:
		return nil, fmt.Errorf("tangent.%s must be a list of strings", key)
	}
	return out, nil
}

var keyName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
