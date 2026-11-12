package star

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStarlarkIsConfined: go.starlark.net is imported by this package, by the
// runtime it builds on (internal/plugin/starcore) and by nothing else, so
// `go list -deps` shows who links it (the confinement is a contract, like
// purego in internal/tray). Every other package reaches scripts through Load,
// Program and Check.
func TestStarlarkIsConfined(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skip("not inside the module tree")
	}
	own := map[string]bool{
		filepath.Join(root, "internal", "plugin", "star"):     true,
		filepath.Join(root, "internal", "plugin", "starcore"): true,
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", "node_modules":
				return filepath.SkipDir
			}
			if own[path] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return nil // a file that does not parse is another test's problem
		}
		for _, imp := range f.Imports {
			if p := strings.Trim(imp.Path.Value, `"`); p == "go.starlark.net" || strings.HasPrefix(p, "go.starlark.net/") {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s imports %s: only internal/plugin/star and starcore may", rel, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
