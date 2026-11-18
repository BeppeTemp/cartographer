package mcpserver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// failingWrite is a write tool that touches the disk (a tracked file and a new
// one) and then fails, the shape of concept_move after applied moves.
func failingWrite(k *kb.KB, goErr bool) Tool {
	return gitWrap(k, Tool{Name: "test_failing_write", Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		_ = k.WriteFileAtomic("data/half-done.md", []byte("half\n"))
		_ = os.WriteFile(filepath.Join(k.Root, "README.md"), []byte("changed\n"), 0o644)
		if goErr {
			return ToolResult{}, errors.New("boom")
		}
		return errorResult("test_failing_write: late failure; " + recoveryHint), nil
	}})
}

func TestGitWrap_FailedHandlerLeavesTheTreeAtHEAD(t *testing.T) {
	t.Parallel()
	for _, goErr := range []bool{false, true} {
		k, _ := setupGitKB(t)
		head := gitOut(t, k, "rev-parse", "HEAD")
		res, err := failingWrite(k, goErr).Handler(authLocalContext(), json.RawMessage(`{}`))
		if goErr {
			if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "discarded") {
				t.Fatalf("err = %v, want the handler error plus the discarded note", err)
			}
		} else {
			if !res.IsError || strings.Contains(res.Content[0].Text, "concept_history") || !strings.Contains(res.Content[0].Text, "discarded") {
				t.Fatalf("result = %+v, want the error to say the changes were discarded", res)
			}
		}
		if st := gitOut(t, k, "status", "--porcelain"); st != "" {
			t.Fatalf("goErr=%v: tree not clean after a failed write:\n%s", goErr, st)
		}
		if got := gitOut(t, k, "rev-parse", "HEAD"); got != head {
			t.Fatalf("goErr=%v: HEAD moved to %s", goErr, got)
		}
		// The next write, a plain one, commits no "external changes".
		writeWrappedTool(t, k, "log_append", nil)
		if strings.Contains(gitOut(t, k, "log", "--format=%s"), externalChangesSubject) {
			t.Fatalf("goErr=%v: the residue of the failed write was committed as external changes", goErr)
		}
	}
}

func TestGitWrap_FailedHandlerDiscardsNothingWhenTheTreeWasNotClean(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	// An open conflict makes commitExternalChanges leave the dirty tree alone,
	// so the tree is not provably clean: nothing may be discarded.
	editOutOfBand(t, k, "notes/mine.md", "---\ntype: Note\ntitle: Mine\n---\n# Mine\n")
	if err := k.RegisterConflict(kb.Conflict{ConceptID: "foo", Path: "data/foo.md"}); err != nil {
		t.Fatal(err)
	}
	ran := false
	tool := gitWrap(k, Tool{Name: "test_failing_write", Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		ran = true
		return ToolResult{}, errors.New("boom")
	}})
	tool.Handler(authLocalContext(), json.RawMessage(`{}`)) //nolint:errcheck
	if !ran {
		t.Skip("the open conflict refuses the write before the handler: nothing to assert")
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "notes/mine.md")); err != nil {
		t.Fatalf("unsaved work was discarded: %v", err)
	}
}

// #2190: a commit refused by an index.lock that appeared during the call fails
// the call, names the lock, leaves nothing of the write on disk, and the next
// write after the lock is gone commits.
func TestGitWrap_CommitFailureFailsTheCallAndRestoresTheTree(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	head := gitOut(t, k, "rev-parse", "HEAD")
	lock := filepath.Join(k.Root, ".git", "index.lock")
	res := writeWrappedTool(t, k, "test_write", func() {
		if err := os.WriteFile(lock, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	})
	text := ""
	if len(res.Content) > 0 {
		text = res.Content[0].Text
	}
	if !res.IsError || res.CommitSHA != "" || strings.Contains(text, `"ok"`) {
		t.Fatalf("a failed commit answered %+v, want an error", res)
	}
	if !strings.Contains(text, "index.lock") || !strings.Contains(text, "remove that file") {
		t.Fatalf("error %q must name the lock file and the remedy", text)
	}
	// #2262: the caller never sees the server's path of the clone.
	roots := []string{k.Root, filepath.ToSlash(k.Root)}
	if r, err := filepath.EvalSymlinks(k.Root); err == nil {
		roots = append(roots, r)
	}
	for _, root := range roots {
		if strings.Contains(text, root) {
			t.Fatalf("error %q names the KB root %q", text, root)
		}
	}
	if !strings.Contains(text, "<kb>") {
		t.Fatalf("error %q should show the root as <kb>", text)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "data", "wrapped.md")); !os.IsNotExist(err) {
		t.Fatalf("the written file is still on disk (stat err %v)", err)
	}
	if got := gitOut(t, k, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved to %s", got)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if res := writeWrappedTool(t, k, "test_write", nil); res.IsError || res.CommitSHA == "" {
		t.Fatalf("the write after the lock was removed = %+v, want a commit", res)
	}
	if strings.Contains(gitOut(t, k, "log", "--format=%s"), externalChangesSubject) {
		t.Fatal("the residue of the failed write was committed as external changes")
	}
}

// #2265: a foreign HEAD.lock makes the discard report an error although the
// write's files are already gone; the answer must say nothing is left, not that
// the changes stay uncommitted.
func TestGitWrap_HeadLockDiscardErrorOnCleanTreeSaysNothingIsLeft(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	lock := filepath.Join(k.Root, ".git", "HEAD.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	res := writeWrappedTool(t, k, "test_write", nil)
	text := ""
	if len(res.Content) > 0 {
		text = res.Content[0].Text
	}
	if !res.IsError || res.CommitSHA != "" {
		t.Fatalf("answered %+v, want an error", res)
	}
	if strings.Contains(text, "left uncommitted") || !strings.Contains(text, discardedNote) {
		t.Fatalf("error %q must say nothing is left", text)
	}
	if !k.TreeClean() {
		t.Fatal("tree is not clean: the premise of the test is gone")
	}
	if k.LeftoversPending() {
		t.Fatal("a clean tree still has leftovers recorded")
	}
}
