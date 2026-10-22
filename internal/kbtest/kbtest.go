// Package kbtest copies a pre-built KB into a test directory. It is test
// support, like net/http/httptest: imported by _test.go files only (a test in
// internal/repodocs enforces that).
//
// A fixture is a copy of a template, not a new kb.Init: Init spawns about ten git
// processes, and a test binary with hundreds of fixtures spent most of its
// time and CPU creating them. The template is built once per test binary,
// held in memory, and its directory is removed right after the capture.
package kbtest

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/testtree"
)

// DirName is the basename of every fixture directory, hence the title Init
// derives for data/index.md.
const DirName = "kb"

var (
	templateOnce sync.Once
	template     testtree.Tree
	templateErr  error
)

func buildTemplate() (testtree.Tree, error) {
	tmp, err := os.MkdirTemp("", "kbtest-template-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	root := filepath.Join(tmp, DirName)
	if _, err := kb.Init(root); err != nil {
		return nil, fmt.Errorf("kb.Init: %w", err)
	}
	return testtree.Capture(root)
}

// tempDir is t.TempDir without its cleanup check: the fixtures this package
// replaces removed their directory with a best-effort os.RemoveAll, and a
// background write of the code under test (a read refresh, a repair run) may
// still be landing in the tree when the test ends. t.TempDir turns that race
// into a failure of the test; here the removal is retried and then left alone.
func tempDir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kbtest-*")
	if err != nil {
		t.Fatalf("kbtest: %v", err)
	}
	t.Cleanup(func() {
		var err error
		for i := 0; i < 20; i++ {
			if err = os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Logf("kbtest: leaving %s behind: %v", dir, err)
	})
	return dir
}

// New returns a fresh KB: a directory named "kb" in a temporary directory, a git work
// tree with exactly the initial commit, on gitx.DefaultBranch. It spawns no git
// process.
//
// Use it for a test that needs a KB and does not assert on what kb.Init
// itself does. A test about Init — the layout it creates, the identity
// (InitWithIdentity), the branch, settings.yaml, the KB name in data/index.md,
// Init on an existing or unborn repository — keeps calling kb.Init: the
// fixture would hide what it is supposed to verify. When in doubt, keep
// kb.Init: a wrong switch weakens a test silently. A test that needs the KB at
// a path of its own (a command's --kb flag) passes k.Root.
func New(t testing.TB) *kb.KB {
	t.Helper()
	templateOnce.Do(func() { template, templateErr = buildTemplate() })
	if templateErr != nil {
		t.Fatalf("kbtest: building the KB template: %v", templateErr)
	}
	root := filepath.Join(tempDir(t), DirName)
	if err := template.Restore(root); err != nil {
		t.Fatalf("kbtest: restoring the KB template: %v", err)
	}
	k, err := kb.Open(root)
	if err != nil {
		t.Fatalf("kbtest: kb.Open: %v", err)
	}
	return k
}
