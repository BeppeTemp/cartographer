package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func revertCall(t *testing.T, s *Server, sha string) ToolResult {
	t.Helper()
	return callTool(t, s, "repair_revert", fmt.Sprintf(`{"sha":%q,"reason":"test"}`, sha))
}

func TestRepairRevertUndoesARepairAsANewCommit(t *testing.T) {
	t.Parallel()
	k, s := autoRepairKB(t)
	run := s.runAutoRepairQuota(t.Context())
	sha := run.Checks[0].Commit
	before := commitCount(t, k)

	res := revertCall(t, s, sha[:8])
	if res.IsError {
		t.Fatalf("revert: %s", res.Content[0].Text)
	}
	if commitCount(t, k) != before+1 {
		t.Fatalf("want one new commit, got %d", commitCount(t, k)-before)
	}
	if got := gitOut(t, k, "log", "-1", "--format=%s"); !strings.HasPrefix(got, "repair_revert: "+sha[:7]) {
		t.Fatalf("subject = %q", got)
	}
	cd, err := k.ReadConcept("ops/n0")
	if err != nil {
		t.Fatal(err)
	}
	fm, _ := okf.ParseFrontmatter(cd.FrontmatterRaw)
	if _, ok := fm.Get("updated"); !ok {
		t.Fatal("the repair was not undone")
	}
	// The revert commit is not itself a repair commit: no revert of a revert.
	if res := revertCall(t, s, gitOut(t, k, "rev-parse", "HEAD")); !res.IsError {
		t.Fatal("a repair_revert commit was accepted as a repair")
	}
}

// The guard is the whole safety of the tool: a write token must not be able to
// roll back history it could not have repaired.
func TestRepairRevertRefusesAnythingButARepairCommit(t *testing.T) {
	t.Parallel()
	k, s := autoRepairKB(t)
	seed := gitOut(t, k, "rev-parse", "HEAD") // "test: seed"
	for name, sha := range map[string]string{
		"an agent write": seed,
		"not hex":        "main; rm -rf",
		"unknown":        "deadbeefdeadbeef",
		"too short":      "abc",
	} {
		before := commitCount(t, k)
		if res := revertCall(t, s, sha); !res.IsError {
			t.Errorf("%s: accepted", name)
		}
		if commitCount(t, k) != before || gitOut(t, k, "status", "--short") != "" {
			t.Errorf("%s: left a trace", name)
		}
	}
}

func TestRepairRevertRefusesWhenALaterChangeTouchedTheSameFiles(t *testing.T) {
	t.Parallel()
	k, s := autoRepairKB(t)
	sha := s.runAutoRepairQuota(t.Context()).Checks[0].Commit
	cd, _ := k.ReadConcept("ops/n0")
	fm, _ := okf.ParseFrontmatter(cd.FrontmatterRaw)
	fm.Set("title", "Changed later")
	if _, err := k.WriteConcept("ops/n0", fm, "# Later\n", cd.ContentHash); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOpAs("test: later edit", "", ""); err != nil {
		t.Fatal(err)
	}
	before := commitCount(t, k)
	res := revertCall(t, s, sha)
	if !res.IsError {
		t.Fatal("a conflicting revert was accepted")
	}
	if commitCount(t, k) != before || gitOut(t, k, "status", "--short") != "" {
		t.Fatalf("a failed revert left the tree dirty: %q", gitOut(t, k, "status", "--short"))
	}
}

func TestRepairRevertIsAdvancedAndWriteOnly(t *testing.T) {
	t.Parallel()
	if !ToolAdvanced("repair_revert") {
		t.Error("repair_revert must stay out of the agent tools/list (D285 budget)")
	}
	if !ToolRequiresWrite("repair_revert") {
		t.Error("repair_revert needs write access")
	}
}

// The Reason trailer is free text from the caller of any write tool, so a
// commit that merely claims to be a repair is not one.
func TestRepairRevertRefusesACommitThatClaimsToBeARepair(t *testing.T) {
	t.Parallel()
	k, s := autoRepairKB(t)
	if res := callTool(t, s, "concept_write", `{"id":"ops/claimed","frontmatter":{"type":"Note","title":"Claimed","updated":"2026-01-02","tags":["a"]},"body":"# Claimed\n\n[[ops/n1]]\n","index_entry":true,"reason":"auto-repair (background)"}`); res.IsError {
		t.Fatalf("concept_write: %s", res.Content[0].Text)
	}
	sha := gitOut(t, k, "rev-parse", "HEAD")
	if got := gitOut(t, k, "log", "-1", "--format=%(trailers:key=Reason,valueonly)"); !strings.HasPrefix(got, "auto-repair") {
		t.Fatalf("fixture: reason trailer = %q", got)
	}
	before := commitCount(t, k)
	if res := revertCall(t, s, sha); !res.IsError {
		t.Fatal("a write with an auto-repair reason was reverted as a repair")
	}
	if commitCount(t, k) != before {
		t.Fatal("the refused revert left a commit")
	}
}

// A git that cannot answer is not a dirty tree: the error names the cause, so
// the agent does not wait for a commit that will never fix it.
func TestRepairRevertNamesAGitFailureNotADirtyTree(t *testing.T) {
	t.Parallel()
	k, s := autoRepairKB(t)
	sha := s.runAutoRepairQuota(t.Context()).Checks[0].Commit
	// A corrupt index breaks `git status` but not the lookups before it.
	if err := os.WriteFile(filepath.Join(k.Root, ".git", "index"), []byte("not an index"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := revertCall(t, s, sha)
	if !res.IsError {
		t.Fatal("the revert ran against a git that cannot report its status")
	}
	if text := res.Content[0].Text; strings.Contains(text, "uncommitted changes") || !strings.Contains(text, "git status") {
		t.Fatalf("error = %q, want the git failure and not the dirty-tree message", text)
	}
}
