package plugin

import (
	"fmt"
	"regexp"
)

// UIContribution names an export in the reviewed common UI bundle. This is a
// declaration, not registration or admission; runtime policy still belongs to
// Tangent. Priority zero remains explicit rather than defaulting through truthiness.
type UIContribution struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Export   string `json:"export"`
	Region   string `json:"region"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
}

var uiLocalKey = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var uiExport = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// ValidateUIContributions validates the reviewed host projection without
// interpreting its isolation preference as permission to execute a bundle.
func ValidateUIContributions(declarations []UIContribution) error {
	if len(declarations) == 0 || len(declarations) > 32 {
		return fmt.Errorf("plugin: invalid UI contribution inventory")
	}
	seen := map[string]bool{}
	for _, d := range declarations {
		if !uiLocalKey.MatchString(d.Key) || len(d.Key) > 128 || !uiExport.MatchString(d.Export) || len(d.Export) > 128 || len(d.Title) > 128 || seen[d.Key] {
			return fmt.Errorf("plugin: invalid or duplicate UI declaration %q", d.Key)
		}
		if (d.Kind != "panel" && d.Kind != "widget") || d.Region != "right" {
			return fmt.Errorf("plugin: unsupported UI contribution kind or region")
		}
		seen[d.Key] = true
	}
	return nil
}
