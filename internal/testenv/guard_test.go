package testenv

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoUnisolatedBinaryRuns fails when a test file starts a process without going through
// testenv.Command. A binary started with the caller's own environment would resolve the real
// home and XDG directories, which hold the live instance's data.
//
// Starting a build tool or a system utility is fine: they do not read Tangent's data. Two kinds
// of file are exempt until the code they test is ported or deleted in this change: the smoke and
// plugin-host tests of the pre-Substrate tree, which start processes named by a variable.
// scripts/test runs every test process with a temporary HOME and XDG tree regardless.
func TestNoUnisolatedBinaryRuns(t *testing.T) {
	tools := map[string]bool{"go": true, "node": true, "npm": true, "git": true, "pgrep": true, "plutil": true}
	legacy := []string{
		"internal/smoke/", "internal/pluginhost/", "internal/plugins/", "internal/launchagent/",
		"internal/mcp/tether_", "internal/server/", "internal/db/ownership_test.go",
	}
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "testenv":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		slash := filepath.ToSlash(path)
		for _, l := range legacy {
			if strings.Contains(slash, l) {
				return nil
			}
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "exec" && (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext") {
					if lit, ok := firstArg(call, sel.Sel.Name == "CommandContext").(*ast.BasicLit); ok && tools[strings.Trim(lit.Value, `"`)] {
						return true
					}
					t.Errorf("%s: %s starts a process with exec.%s; use testenv.Command", path, fset.Position(call.Pos()), sel.Sel.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func firstArg(call *ast.CallExpr, ctx bool) ast.Expr {
	i := 0
	if ctx {
		i = 1
	}
	if len(call.Args) > i {
		return call.Args[i]
	}
	return nil
}

// Env must point HOME and every XDG variable inside the root and nowhere else.
func TestEnvIsolates(t *testing.T) {
	root := t.TempDir()
	env := Env(root, "X=1")
	seen := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		seen[k] = v
	}
	for _, k := range Vars {
		if !strings.HasPrefix(seen[k], root) {
			t.Errorf("%s=%q is outside %s", k, seen[k], root)
		}
	}
	if seen["X"] != "1" {
		t.Error("extra variables were dropped")
	}
	for k := range seen {
		if strings.HasPrefix(k, "TANGENT_") || k == "SUBSTRATE_CONFIG" {
			t.Errorf("%s leaked into the child environment", k)
		}
	}
}

// Check refuses a process whose home is the real one.
func TestCheckRefusesRealHome(t *testing.T) {
	t.Setenv("HOME", "/nonexistent-home")
	if err := Check(); err == nil {
		t.Fatal("a real home was accepted")
	}
	_ = os.Getenv
}
