package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/testtree"
)

// A fixture is a copy of a template, not a new Init: Init spawns eleven git
// processes (D812). The template is built once per test binary and held in
// memory; kbtest does the same for the packages that import kb, which this one
// cannot.
var (
	templateOnce sync.Once
	template     testtree.Tree
	templateErr  error
)

func buildTemplate() (testtree.Tree, error) {
	tmp, err := os.MkdirTemp("", "kb-template-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	root := filepath.Join(tmp, "kb")
	if _, err := Init(root); err != nil {
		return nil, fmt.Errorf("Init: %w", err)
	}
	return testtree.Capture(root)
}

// newTestKB returns a fresh KB: a directory named "kb" under t.TempDir(), a git
// work tree with exactly the initial commit. It spawns no git process.
//
// Use it for a test that needs a KB and does not assert on what Init does
// (layout, identity, branch, settings.yaml, the KB name in data/index.md, Init
// on an existing or unborn repository): those tests call Init themselves.
// When in doubt, call Init: a wrong switch weakens a test silently.
func newTestKB(t testing.TB) *KB {
	t.Helper()
	templateOnce.Do(func() { template, templateErr = buildTemplate() })
	if templateErr != nil {
		t.Fatalf("building the KB template: %v", templateErr)
	}
	root := filepath.Join(t.TempDir(), "kb")
	if err := template.Restore(root); err != nil {
		t.Fatalf("restoring the KB template: %v", err)
	}
	k, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return k
}
