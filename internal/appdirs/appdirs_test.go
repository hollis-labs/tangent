package appdirs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveOverrideAndPrepare(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d")
	d, err := Resolve(func(k string) string {
		if k == DataDirEnv {
			return dir
		}
		return ""
	})
	if err != nil || d.Data != dir {
		t.Fatalf("%v %v", d, err)
	}
	if err = d.Prepare(); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{d.Data: 0o700, d.Database(): 0o600} {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: %v %v", p, fi, err)
		}
	}
}

func TestResolveFollowsXDG(t *testing.T) {
	x := t.TempDir()
	t.Setenv("XDG_DATA_HOME", x)
	d, err := Resolve(func(string) string { return "" })
	if err != nil || d.Data != filepath.Join(x, "tangent") {
		t.Fatalf("%v %v", d, err)
	}
}
