package mcpserver

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

func writeJunk(t *testing.T, k *kb.KB, rel string) {
	t.Helper()
	abs := filepath.Join(k.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// trackJunk commits a junk file the way a KB did before #1377: forced past
// .git/info/exclude.
func trackJunk(t *testing.T, k *kb.KB, rel string) {
	t.Helper()
	writeJunk(t, k, rel)
	if out, err := exec.Command("git", "-C", k.Root, "add", "-f", "--", rel).CombinedOutput(); err != nil {
		t.Fatalf("git add -f %s: %v: %s", rel, err, out)
	}
	if _, err := k.CommitOp("test: track " + rel); err != nil {
		t.Fatal(err)
	}
}

func tracked(t *testing.T, k *kb.KB, rel string) bool {
	t.Helper()
	return gitOut(t, k, "ls-files", "--", rel) != ""
}

// A write finds junk at the KB root and under data/ left by Finder: neither the
// external-changes commit nor the write's own tracks it (#1377).
func TestWrite_DoesNotTrackJunk(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	junk := []string{".DS_Store", "templates/.DS_Store", "data/ops/.DS_Store", "notes.md~"}
	for _, rel := range junk {
		writeJunk(t, k, rel)
	}
	args, _ := json.Marshal(map[string]any{"id": "ops/a", "frontmatter": map[string]any{"type": "Note", "title": "A"}, "body": "# A\n", "index_entry": true})
	if res := callTool(t, s, "concept_write", string(args)); res.IsError {
		t.Fatalf("concept_write: %s", res.Content[0].Text)
	}
	if !tracked(t, k, "data/ops/a.md") {
		t.Fatal("the write was not committed")
	}
	for _, rel := range junk {
		if tracked(t, k, rel) {
			t.Errorf("%s is tracked after a write", rel)
		}
	}
	if n := countFindings(t, k, "junk_file"); n != 0 {
		t.Errorf("junk_file findings = %d, want 0", n)
	}
}

// A junk file a KB already tracks is untracked by kb_repair in one commit; the
// file stays on disk, and a second run has nothing to do.
func TestRepairJunkFile_UntracksInOneCommit(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	trackJunk(t, k, ".DS_Store")
	if n := countFindings(t, k, "junk_file"); n != 1 {
		t.Fatalf("want one junk_file finding, got %d", n)
	}

	before := commitCount(t, k)
	out := repairApply(t, s, "junk_file")
	if out["applied"].(float64) != 1 {
		t.Fatalf("applied = %v, want 1: %v", out["applied"], out)
	}
	if got := commitCount(t, k); got != before+1 {
		t.Fatalf("commits = %d, want %d", got, before+1)
	}
	if tracked(t, k, ".DS_Store") {
		t.Fatal(".DS_Store is still tracked")
	}
	if _, err := os.Stat(filepath.Join(k.Root, ".DS_Store")); err != nil {
		t.Fatalf("the file on disk must be left alone: %v", err)
	}
	if n := countFindings(t, k, "junk_file"); n != 0 {
		t.Fatalf("junk_file still reported after the repair: %d", n)
	}

	again := repairApply(t, s, "junk_file")
	if again["applied"].(float64) != 0 || commitCount(t, k) != before+1 {
		t.Fatalf("a second run must change nothing: %v", again)
	}
}

// With no ignore rule covering the file, the commit would track it again: the
// repair skips it rather than report it applied.
func TestRepairJunkFile_SkipsAFileNoRuleIgnores(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	trackJunk(t, k, ".DS_Store")
	// Only the .DS_Store line goes: .cartographer/ uncovered would make the
	// external-changes commit run first.
	excludePath := filepath.Join(k.Root, ".git", "info", "exclude")
	raw, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l != ".DS_Store" {
			kept = append(kept, l)
		}
	}
	if err := os.WriteFile(excludePath, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	before := commitCount(t, k)
	out := repairApply(t, s, "junk_file")
	if out["applied"].(float64) != 0 || commitCount(t, k) != before {
		t.Fatalf("nothing may be applied: %v", out)
	}
	skipped, _ := json.Marshal(out["skipped"])
	if !strings.Contains(string(skipped), "no ignore rule covers .DS_Store") {
		t.Fatalf("skipped = %s", skipped)
	}
	if !tracked(t, k, ".DS_Store") {
		t.Fatal("a skipped file must stay tracked")
	}
}
