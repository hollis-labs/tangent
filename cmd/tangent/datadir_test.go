package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/cli"

	"github.com/hollis-labs/tangent/internal/appdirs"
	"github.com/hollis-labs/tangent/internal/testenv"
)

func TestPlan(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		all     bool
		prepare bool
	}{
		{nil, true, true},
		{[]string{"serve"}, true, true},
		{[]string{"version"}, false, false},
		{[]string{"config"}, false, false},
		{[]string{"health"}, false, false},
		{[]string{"--help"}, false, false},
		{[]string{"help", "serve"}, false, false},
	} {
		mods, prepare := plan(tc.args)
		if prepare != tc.prepare || (len(mods) == len(substrateModules)) != tc.all {
			t.Errorf("%v: modules %v prepare %v", tc.args, mods, prepare)
		}
		if !tc.all {
			for _, m := range []string{"database", "identity", "secrets", "httphost"} {
				if slices.Contains(mods, m) {
					t.Errorf("%v starts %s, which opens a file", tc.args, m)
				}
			}
		}
	}
}

// plan reads every non-dash argument as a command word, which is right only while the root has
// no flag that takes a value. This fails when one is added.
func TestRootHasNoValueFlags(t *testing.T) {
	cfg := map[string]any{}
	if err := json.Unmarshal([]byte(substrateConfigJSON), &cfg); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)
	mods := pick(readOnly)
	run, err := boot.StartWith(context.Background(), substrateApp, mods, string(raw), bootOptions(os.Getenv, false, mods))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	root := cli.Root(run.App)
	check := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) {
			if f.Value.Type() != "bool" {
				t.Errorf("root flag --%s takes a value (%s): teach plan about it", f.Name, f.Value.Type())
			}
		})
	}
	check(root.Flags())
	check(root.PersistentFlags())
}

// Read-only commands start no module that opens a file and create no directory: with an empty
// HOME and no TANGENT_DATA_DIR, nothing appears. `serve` is the positive control (binary_test.go
// asserts what it creates), so the emptiness check is known to be able to see a write. This is
// also the proof that no default resolves to the pre-Substrate ~/.tangent: whatever the binary
// creates for serve is under the XDG data home, and nothing under HOME is named .tangent.
func TestReadOnlyCommandsCreateNothing(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "tangent")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil { //nolint:gosec // builds this package
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, args := range [][]string{{"version"}, {"config"}, {"health"}, {"--help"}, {"help"}} {
		cmd := testenv.Command(t, bin, args...)
		before := entries(t, cmd.Dir)
		out, err := cmd.CombinedOutput()
		if err != nil && args[0] != "health" && args[0] != "config" {
			t.Errorf("%v failed: %v\n%s", args, err, out)
		}
		if got := entries(t, cmd.Dir); !slices.Equal(got, before) {
			t.Errorf("%v created %v", args, slices.DeleteFunc(got, func(p string) bool { return slices.Contains(before, p) }))
		}
	}
}

func TestDefaultsNeverResolveToTheOldDirectory(t *testing.T) {
	d, err := appdirs.Resolve(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{d.Data, d.Database(), d.SigningKey(), d.Secrets(), d.Artifacts()} {
		if strings.Contains(filepath.ToSlash(p)+"/", "/.tangent/") {
			t.Errorf("%s is under the pre-Substrate ~/.tangent", p)
		}
	}
}

func entries(t *testing.T, root string) []string {
	t.Helper()
	var got []string
	_ = filepath.WalkDir(root, func(p string, _ fs.DirEntry, _ error) error {
		if p != root {
			got = append(got, p)
		}
		return nil
	})
	slices.Sort(got)
	return got
}

// A path in the operator's override is theirs: absolute stays, relative is placed in the data
// directory (it is never silently replaced by the default).
func TestOverridePathsAreHonoured(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	abs := filepath.Join(t.TempDir(), "elsewhere.db")
	for name, tc := range map[string]struct{ override, want string }{
		"relative": {"database:\n  sqlite:\n    path: other.db\n", filepath.Join(dir, "other.db")},
		"absolute": {"database:\n  sqlite:\n    path: " + abs + "\n", abs},
		"default":  {"identity:\n  bcrypt_cost: 4\n", filepath.Join(dir, "tangent.db")},
	} {
		file := filepath.Join(t.TempDir(), "o.yaml")
		if err := os.WriteFile(file, []byte(tc.override), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SUBSTRATE_CONFIG", file)
		run, err := bootWith(context.Background(), func(k string) string {
			if k == "TANGENT_DATA_DIR" {
				return dir
			}
			return ""
		}, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		db, _ := run.App.Config["database"].(map[string]any)
		sq, _ := db["sqlite"].(map[string]any)
		if got, _ := sq["path"].(string); got != tc.want {
			t.Errorf("%s: database path %q, want %q", name, got, tc.want)
		}
		if _, err := os.Stat(tc.want); err != nil {
			t.Errorf("%s: the database was not created at %s: %v", name, tc.want, err)
		}
		_ = run.Stop(context.Background())
	}
}

// The offline owner and key commands open the database and the signing key and serve nothing: no
// listener, no tool server.
func TestPlanForTheIdentityCommands(t *testing.T) {
	for _, args := range [][]string{{"identity", "setup", "--username", "owner"}, {"identity", "key", "create", "--stdout"}} {
		mods, prepare := plan(args)
		if !prepare {
			t.Errorf("%v: the data directory must be created for the owner to live in it", args)
		}
		for _, m := range []string{"database", "identity", "secrets", "auth", "cli"} {
			if !slices.Contains(mods, m) {
				t.Errorf("%v does not start %s", args, m)
			}
		}
		for _, m := range []string{"httphost", "mcp"} {
			if slices.Contains(mods, m) {
				t.Errorf("%v starts %s, which serves", args, m)
			}
		}
	}
}
