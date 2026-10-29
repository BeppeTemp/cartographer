package mcpserver

import (
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestExternalChangesNeverCommitJunk (#1377): a Finder .DS_Store at the KB
// root and one under data/ are not swept into the external-changes commit.
func TestExternalChangesNeverCommitJunk(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 1)
	for _, p := range []string{".DS_Store", "data/ops/.DS_Store", "data/ops/__pycache__/x.pyc"} {
		abs := filepath.Join(k.Root, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(abs), 0o755)
		if err := os.WriteFile(abs, []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cd, err := k.ReadConcept("ops/n0")
	if err != nil {
		t.Fatal(err)
	}
	res := callTool(t, s, "concept_patch", `{"id":"ops/n0","if_match":"`+cd.ContentHash+`","frontmatter":{"tags":["b"]}}`)
	if res.IsError {
		t.Fatalf("concept_patch: %s", res.Content[0].Text)
	}
	if tracked := gitOut(t, k, "ls-files"); strings.Contains(tracked, "DS_Store") || strings.Contains(tracked, "pyc") {
		t.Errorf("junk tracked at HEAD:\n%s", tracked)
	}
	if c := gitOut(t, k, "log", "-1", "--stat", "--format=%s"); !strings.Contains(c, "n0") {
		t.Errorf("last commit does not touch the edited page: %s", c)
	}
}

// TestRepair_JunkFileUntracks (#1377): a junk file already tracked is closed
// by kb_repair junk_file: one commit, the file untracked and kept on disk, the
// finding gone.
func TestRepair_JunkFileUntracks(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 1)
	for _, p := range []string{".DS_Store", "data/ops/.DS_Store"} {
		if err := os.WriteFile(filepath.Join(k.Root, filepath.FromSlash(p)), []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitOut(t, k, "add", "-f", p)
	}
	gitOut(t, k, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "seed junk")
	plan := repairCall(t, s, `{"check":"junk_file"}`)
	if plan["planned_total"].(float64) != 2 {
		t.Fatalf("plan = %v", plan)
	}
	before := commitCount(t, k)
	out := repairCall(t, s, `{"check":"junk_file","dry_run":false}`)
	if out["applied"].(float64) != 2 {
		t.Fatalf("apply = %v", out)
	}
	if commitCount(t, k) != before+1 {
		t.Errorf("want one commit for the repair")
	}
	if tracked := gitOut(t, k, "ls-files"); strings.Contains(tracked, "DS_Store") {
		t.Errorf("still tracked:\n%s", tracked)
	}
	if _, err := os.Stat(filepath.Join(k.Root, ".DS_Store")); err != nil {
		t.Errorf("working-tree file must stay: %v", err)
	}
	if st := gitOut(t, k, "status", "--porcelain"); strings.TrimSpace(st) != "" {
		t.Errorf("tree not clean after the repair: %q", st)
	}
	again := repairCall(t, s, `{"check":"junk_file"}`)
	if again["found_total"].(float64) != 0 {
		t.Errorf("finding remains: %v", again)
	}
}

// A tracked file whose name is a glob is untracked alone: the names go to git
// as literal paths, never as pathspecs.
func TestRepair_JunkFileUntrackIsLiteral(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("a file name with * cannot exist on Windows")
	}
	k, _ := repairKB(t, 1)
	_ = os.MkdirAll(filepath.Join(k.Root, "x"), 0o755)
	for _, p := range []string{"x/*.pyc", "x/keep.pyc"} {
		if err := os.WriteFile(filepath.Join(k.Root, filepath.FromSlash(p)), []byte("j"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitOut(t, k, "add", "-f", p)
	}
	tracked, err := gitx.TrackedPaths(k.Root, "x/*.pyc")
	if err != nil || len(tracked) != 1 || !tracked["x/*.pyc"] {
		t.Fatalf("TrackedPaths = %v, %v: want only the glob-named file", tracked, err)
	}
	if err := gitx.Untrack(k.Root, "x/*.pyc"); err != nil {
		t.Fatal(err)
	}
	if tr := gitOut(t, k, "ls-files", "x"); !strings.Contains(tr, "keep.pyc") || strings.Contains(tr, "*.pyc") {
		t.Errorf("tracked after untrack: %q", tr)
	}
}
