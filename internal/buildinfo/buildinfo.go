// Package buildinfo holds source provenance stamped by make build-go.
package buildinfo

// Commit and Dirty are linker inputs. Empty means the build was not stamped;
// the release version and Go's own build identity are independent of them.
var (
	Commit string
	Dirty  string
)

// Info describes the source used for this binary, without filesystem paths.
type Info struct {
	Commit string `json:"commit"`
	Dirty  *bool  `json:"dirty,omitempty"`
}

// Current preserves unknown provenance rather than claiming an unstamped
// binary came from a clean checkout.
func Current() Info {
	info := Info{Commit: Commit}
	if Commit != "" && (Dirty == "true" || Dirty == "false") {
		dirty := Dirty == "true"
		info.Dirty = &dirty
	}
	return info
}
