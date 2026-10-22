package mcpserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// headHolds reports which of rels the HEAD tree of k lists.
func headHolds(t *testing.T, k *kb.KB, rels ...string) (held, absent []string) {
	t.Helper()
	paths, err := gitx.TreePaths(k.Root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range rels {
		if slices.Contains(paths, rel) {
			held = append(held, rel)
		} else {
			absent = append(absent, rel)
		}
	}
	return held, absent
}

// assertNothingLeftIn fails when k's working tree still holds anything at rels,
// ignored files included: `git status` alone does not show those.
func assertNothingLeftIn(t *testing.T, k *kb.KB, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if _, err := os.Lstat(filepath.Join(k.Root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s was left in the target working tree (err %v)", rel, err)
		}
	}
	out, err := exec.Command("git", "-C", k.Root, "status", "--porcelain", "--ignored").CombinedOutput()
	var left []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		// The process lock file is ignored and always there while a KB is open.
		if l = strings.TrimSpace(l); l != "" && !strings.HasSuffix(l, ".cartographer.lock") {
			left = append(left, l)
		}
	}
	if err != nil || len(left) > 0 {
		t.Errorf("target working tree of %s not clean, ignored files included (%v):\n%s", k.AuthName, err, strings.Join(left, "\n"))
	}
}

// #845: a .gitignore of the target that matches a dossier asset made `git add -A`
// skip it. The target commit was still non-empty, the move counted as done, and
// the source removal deleted the only committed copy.
func TestTwoKBMove_ConceptTransferTargetIgnoreRuleLosesNoFile(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/dossier/index.md", "---\ntype: Note\ntitle: Dossier\n---\n![d](diagram.png)\n")
	if err := os.WriteFile(filepath.Join(f.a.DataRoot(), "notes", "dossier", "diagram.png"), []byte{0, 1, 2, 0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	xferCommit(t, f.a)
	// The rule travels with the dossier: committed after the asset, it leaves
	// the source's tracked copy alone, and the pre-check on the target's rules
	// (#873) cannot see it, so only the target commit check catches it.
	ignore := filepath.Join(f.a.DataRoot(), "notes", "dossier", ".gitignore")
	if err := os.WriteFile(ignore, []byte("*.png\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	xferCommit(t, f.a)
	headA := xferHead(t, f.a)
	const call = `{"target_kb":"kb-b","moves":[{"source_id":"notes/dossier","target_id":"wiki/dossier"}]}`
	srcFiles := []string{"data/notes/dossier/index.md", "data/notes/dossier/diagram.png"}
	dstFiles := []string{"data/wiki/dossier/index.md", "data/wiki/dossier/diagram.png"}

	res := f.transfer(t, call)
	text := xferText(res)
	if !res.IsError || !strings.Contains(text, "target step failed") || !strings.Contains(text, "data/wiki/dossier/diagram.png") || !strings.Contains(text, "ignore rule") {
		t.Fatalf("want a target-step failure naming the ignored asset, got: %s", text)
	}
	if xferHead(t, f.a) != headA {
		t.Error("the source must not be committed to")
	}
	if _, absent := headHolds(t, f.a, srcFiles...); len(absent) > 0 {
		t.Errorf("the source HEAD lost %v", absent)
	}
	if held, _ := headHolds(t, f.b, dstFiles...); len(held) > 0 {
		t.Errorf("the target HEAD still holds %v after the undo", held)
	}
	assertNothingLeftIn(t, f.b, "data/wiki/dossier")
	xferClean(t, f.a)

	if err := os.Remove(ignore); err != nil {
		t.Fatal(err)
	}
	xferCommit(t, f.a)
	if res := f.transfer(t, call); res.IsError {
		t.Fatalf("the retry after removing the ignore rule: %s", xferText(res))
	}
	if _, absent := headHolds(t, f.b, dstFiles...); len(absent) > 0 {
		t.Errorf("the retry left %v out of the target HEAD", absent)
	}
	if held, _ := headHolds(t, f.a, srcFiles...); len(held) > 0 {
		t.Errorf("the retry left %v in the source HEAD", held)
	}
}

// The same hole through an ignore file the moved skill brings along: the
// up-front IgnoredPaths runs before the copy, when that file is not in the
// target yet, so it cannot see it.
func TestTwoKBMove_ArtifactMoveIgnoreFileInTheUnitLosesNoFile(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	amWriteBytes(t, f.a, "skills/s/SKILL.md", []byte("---\nname: s\ndescription: S\n---\n# S\n"), 0o644)
	amWriteBytes(t, f.a, "skills/s/model.bin", []byte{0, 1, 2, 0xff}, 0o644)
	xferCommit(t, f.a)
	// Added after model.bin was committed: the file stays tracked in the source,
	// so the source-side ignored-files refusal does not see it.
	ignore := filepath.Join(f.a.Root, "skills", "s", ".gitignore")
	amWriteBytes(t, f.a, "skills/s/.gitignore", []byte("*.bin\n"), 0o644)
	xferCommit(t, f.a)
	headA := xferHead(t, f.a)
	const call = `{"target_kb":"kb-b","paths":["skills/s"]}`
	files := []string{"skills/s/SKILL.md", "skills/s/model.bin", "skills/s/.gitignore"}

	res := f.move(t, call)
	text := xferText(res)
	if !res.IsError || !strings.Contains(text, "artifact_move: target step failed") || !strings.Contains(text, "skills/s/model.bin") {
		t.Fatalf("want a target-step failure naming model.bin, got: %s", text)
	}
	if xferHead(t, f.a) != headA {
		t.Error("the source must not be committed to")
	}
	if _, absent := headHolds(t, f.a, files...); len(absent) > 0 {
		t.Errorf("the source HEAD lost %v", absent)
	}
	if held, _ := headHolds(t, f.b, files...); len(held) > 0 {
		t.Errorf("the target HEAD still holds %v after the undo", held)
	}
	assertNothingLeftIn(t, f.b, "skills")
	xferClean(t, f.a)

	if err := os.Remove(ignore); err != nil {
		t.Fatal(err)
	}
	xferCommit(t, f.a)
	if res := f.move(t, call); res.IsError {
		t.Fatalf("the retry after removing the ignore file: %s", xferText(res))
	}
	if _, absent := headHolds(t, f.b, "skills/s/SKILL.md", "skills/s/model.bin"); len(absent) > 0 {
		t.Errorf("the retry left %v out of the target HEAD", absent)
	}
}

// The failure path before the commit had the same leak: DiscardChanges keeps
// ignored files, so a copy the target's rules hide stayed in its working tree
// and the retry failed on "already exists".
func TestTwoKBMove_TargetCommitFailureRemovesIgnoredCopies(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the failing commit is a shell pre-commit hook")
	}
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/dossier/index.md", "---\ntype: Note\ntitle: Dossier\n---\nd\n")
	if err := os.WriteFile(filepath.Join(f.a.DataRoot(), "notes", "dossier", "diagram.png"), []byte{0, 1}, 0o644); err != nil {
		t.Fatal(err)
	}
	xferCommit(t, f.a)
	// The rule travels with the dossier: committed after the asset, it leaves
	// the source's tracked copy alone, and the pre-check on the target's rules
	// (#873) cannot see it, so only the target commit check catches it.
	ignore := filepath.Join(f.a.DataRoot(), "notes", "dossier", ".gitignore")
	if err := os.WriteFile(ignore, []byte("*.png\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	xferCommit(t, f.a)
	hook := filepath.Join(f.b.Root, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	headA, headB := xferHead(t, f.a), xferHead(t, f.b)
	const call = `{"target_kb":"kb-b","moves":[{"source_id":"notes/dossier","target_id":"wiki/dossier"}]}`

	res := f.transfer(t, call)
	if !res.IsError || !strings.Contains(xferText(res), "target step failed") {
		t.Fatalf("want a target-step failure, got: %s", xferText(res))
	}
	if xferHead(t, f.a) != headA || xferHead(t, f.b) != headB {
		t.Error("a target commit that never landed must leave both HEADs as they were")
	}
	assertNothingLeftIn(t, f.b, "data/wiki/dossier")

	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ignore); err != nil {
		t.Fatal(err)
	}
	xferCommit(t, f.a)
	if res := f.transfer(t, call); res.IsError {
		t.Fatalf("the retry after the cause is gone: %s", xferText(res))
	}
}
