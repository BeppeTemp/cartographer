package procattr

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// guardAllowlist names the non-test files that may start a child without Hide,
// each with the reason. Everything else that starts a process on Windows goes
// through Hide (D1518).
var guardAllowlist = map[string]string{
	// WP2 of the tray follow-ups hid the service manager and the taskkill
	// call; the sites below are hidden by the next change and leave the list.
	"cmd/cartographer/workspace.go":            "git, hidden next",
	"internal/clientconfig/workspace.go":       "git, hidden next",
	"internal/gitx/gitx.go":                    "git, hidden next",
	"internal/gitx/ident.go":                   "git, hidden next",
	"internal/gitx/realign.go":                 "git, hidden next",
	"internal/provisioning/kbtracked.go":       "git, hidden next",
	"internal/provisioning/opencodeversion.go": "opencode, hidden next",
	"internal/provisioning/repohygiene.go":     "git, hidden next",
	"internal/sops/create.go":                  "sops, hidden next",
	"internal/sops/sops.go":                    "sops, hidden next",
	"cmd/cartographer/setup.go":                "loginctl: Linux only",
	"internal/tray/actions.go":                 "rundll32 is a GUI binary, open is macOS: no console to hide",
	"internal/gitx/gitxtest/gitxtest.go":       "test helper",
	"internal/testkbs/testkbs.go":              "builds the synthetic KBs of `make dev`, never runs as a service",
	"internal/repodocs/repodocs.go":            "repository checks, run by the gate",
}

// TestEveryChildProcessIsHidden is a parse-only check (it runs on every OS):
// a top-level declaration of a non-test file that calls exec.Command or exec.CommandContext
// must also call Hide, unless its file is on the allowlist.
func TestEveryChildProcessIsHidden(t *testing.T) {
	root := repoRoot(t)
	var bad []string
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", "node_modules", "web", "film", "demo", "test", "scripts":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			name := "var"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				name = fn.Name.Name
			}
			spawns, hides := callsIn(decl) // a package-level var may hold a func literal
			if !spawns || hides {
				continue
			}
			if _, ok := guardAllowlist[rel]; ok {
				seen[rel] = true
				continue
			}
			bad = append(bad, rel+": "+name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("%s starts a process without procattr.Hide (a Windows console window opens after FreeConsole, D1479)", b)
	}
	for rel := range guardAllowlist {
		if !seen[rel] {
			t.Errorf("%s is on the allowlist but has no unhidden exec call: remove the entry", rel)
		}
	}
}

// callsIn reports whether body calls exec.Command/CommandContext and whether
// it calls a Hide function (procattr.Hide, or Hide inside this package).
func callsIn(body ast.Node) (spawns, hides bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if id, ok := fun.X.(*ast.Ident); ok {
				switch {
				case id.Name == "exec" && (fun.Sel.Name == "Command" || fun.Sel.Name == "CommandContext"):
					spawns = true
				case id.Name == "procattr" && fun.Sel.Name == "Hide":
					hides = true
				}
			}
		}
		return true
	})
	return
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
