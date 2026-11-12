package pluginhost

import "strings"

// expandFixture substitutes placeholders in redaction-corpus templates with
// credential-shaped fragments assembled at run time, so no file in the tree
// holds a whole credential-shaped literal.
func expandFixture(s string) string {
	return strings.NewReplacer(
		"{{URLSEP}}", ":"+"//",
		"{{SLACKB}}", "xo"+"xb-",
		"{{SLACKP}}", "xo"+"xp-",
		"{{JWTHEAD}}", "ey"+"J",
		"{{PEMBEGIN}}", "-----"+"BEGIN ",
		"{{KEYSUFFIX}}", "_"+"KEY",
	).Replace(s)
}
