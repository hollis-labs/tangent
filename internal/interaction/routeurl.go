package interaction

import (
	"fmt"
	"regexp"
)

// pluginRoutePath is the only shape of same-origin path an envelope may name for the page to call back
// into: the plugin route prefix, a plugin id, and at least one more segment. Every segment starts with a
// letter or digit, so a "." or ".." segment, an empty segment ("//"), a percent escape, a backslash,
// whitespace, a query and a fragment cannot be spelled. The browser resolves dot segments before it sends
// a request, so without this rule a hostile envelope could aim the owner's own session at any same-origin
// route (the page's own check is the same rule; the server does not rely on it).
var pluginRoutePath = regexp.MustCompile(`^/api/plugins/[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)+$`)

const maxPluginRoutePath = 256

// routeURLFields names, per kind, the request fields the page treats as callable paths.
var routeURLFields = map[string][][]string{
	"tangent.external-review": {{"action_url"}, {"state_url"}},
	"tangent.app-board":       {{"sync", "endpoint"}},
}

// refuseUnsafeRouteURLs applies the rule to a new request for a kind that carries callable paths. It runs
// when an interaction is created only: records already stored are left as they are (the page refuses to
// call one that fails the rule).
func refuseUnsafeRouteURLs(kind string, request any) error {
	for _, path := range routeURLFields[kind] {
		value, present := dig(request, path)
		if !present {
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue // the schema owns the type
		}
		if !ValidPluginRoutePath(text) {
			return fmt.Errorf("%w: %s %s must be a clean path under /api/plugins/<plugin>/ (letters, digits, '.', '_', '-' in each segment; no dot segment, empty segment, escape, query or fragment)",
				ErrDefinitionValidation, kind, joinPath(path))
		}
	}
	return nil
}

// ValidPluginRoutePath reports whether p is an acceptable callable path (see pluginRoutePath).
func ValidPluginRoutePath(p string) bool {
	return len(p) <= maxPluginRoutePath && pluginRoutePath.MatchString(p)
}

func dig(value any, path []string) (any, bool) {
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		if value, ok = object[key]; !ok {
			return nil, false
		}
	}
	return value, true
}

func joinPath(path []string) string {
	out := path[0]
	for _, p := range path[1:] {
		out += "." + p
	}
	return out
}
