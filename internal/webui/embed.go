// Package webui embeds the built SPA (ui/ builds into dist/).
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// Assets returns the built SPA rooted at dist/, for httphost.Options{Assets: ...}.
// An unbuilt tree holds only dist/.gitkeep, which still compiles and serves nothing.
func Assets() (fs.FS, error) {
	return fs.Sub(embedded, "dist")
}
