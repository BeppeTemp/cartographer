package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #2254: with files of an earlier failed write held by a foreign index.lock for
// the whole call, a write is refused before its handler runs and HEAD stays put.
func TestGitWrap_RefusesAWriteWhileALeftoverIsHeld(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	head := gitOut(t, k, "rev-parse", "HEAD")
	if err := k.WriteFileAtomic("data/left.md", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	gitOut(t, k, "add", "-A") // staged: only a reset settles it
	lock := filepath.Join(k.Root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := k.DiscardUncommitted(); err == nil || !k.LeftoversPending() {
		t.Fatalf("setup: discard err %v, pending %v", err, k.LeftoversPending())
	}
	ran := false
	tool := gitWrap(k, Tool{Name: "test_write", Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		ran = true
		return textResult(`{"ok":true}`), nil
	}})
	res, err := tool.Handler(authLocalContext(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if ran || !res.IsError || res.CommitSHA != "" {
		t.Fatalf("handler ran %v, result %+v: want a refusal", ran, res)
	}
	if !strings.Contains(res.Content[0].Text, "index.lock") {
		t.Fatalf("refusal %q must name the lock", res.Content[0].Text)
	}
	if got := gitOut(t, k, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved to %s", got)
	}
	if out := gitOut(t, k, "ls-tree", "-r", "--name-only", "HEAD"); strings.Contains(out, "left.md") {
		t.Fatal("the leftover was committed")
	}
	// The lock goes: the next write discards the leftover and commits only itself.
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if res := writeWrappedTool(t, k, "test_write", nil); res.IsError || res.CommitSHA == "" {
		t.Fatalf("write after the lock = %+v", res)
	}
	if out := gitOut(t, k, "ls-tree", "-r", "--name-only", "HEAD"); strings.Contains(out, "left.md") {
		t.Fatal("the leftover reached HEAD")
	}
}

// #2254: concept_transfer skips gitWrap's pre-check, so it refuses itself while
// a leftover is held, with the same answer and nothing moved.
func TestConceptTransfer_RefusesWhileALeftoverIsHeld(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/a.md", "---\ntype: Note\ntitle: A\n---\n# A\nbody\n")
	xferCommit(t, f.a)
	headA, headB := xferHead(t, f.a), xferHead(t, f.b)
	if err := f.a.WriteFileAtomic("data/left.md", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	gitOut(t, f.a, "add", "-A")
	lock := filepath.Join(f.a.Root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.a.DiscardUncommitted(); err == nil || !f.a.LeftoversPending() {
		t.Fatalf("setup: discard err %v, pending %v", err, f.a.LeftoversPending())
	}
	res := f.transfer(t, flatMoves)
	if !res.IsError || !strings.Contains(xferText(res), "index.lock") || strings.Contains(xferText(res), "open conflicts") {
		t.Fatalf("answer %q, want the lock refusal", xferText(res))
	}
	if xferHead(t, f.a) != headA || xferHead(t, f.b) != headB {
		t.Fatal("a HEAD moved")
	}
	if _, err := os.Stat(filepath.Join(f.a.DataRoot(), "notes", "a.md")); err != nil {
		t.Fatalf("the source page was touched: %v", err)
	}
}
