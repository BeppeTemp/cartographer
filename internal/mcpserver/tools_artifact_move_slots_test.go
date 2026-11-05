package mcpserver

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The log AppendLog writes is data/log.md: a line of an earlier move names the
// skill, and is history, not a reference that a move leaves dangling.
func TestArtifactMove_ReferencesIgnoreTheLogOfTheDataLayout(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	amWriteBytes(t, f.a, "skills/s/SKILL.md", []byte("---\nname: s\ndescription: A skill\n---\nbody\n"), 0o644)
	if err := f.a.AppendLog("artifact_move (1 artifact(s) from kb-b):\n- kb-b:skills/s → skills/s", time.Now()); err != nil {
		t.Fatal(err)
	}
	amWriteBytes(t, f.a, "data/notes/n.md", []byte("---\ntype: Note\ntitle: N\n---\nSee skills/s.\n"), 0o644)
	xferCommit(t, f.a)
	if !amExists(f.a, "data/log.md") {
		t.Fatal("the fixture has no data/log.md: the test proves nothing")
	}
	refs, _ := refsOf(t, f.move(t, `{"target_kb":"kb-b","paths":["skills/s"]}`))
	if len(refs) != 1 || refs[0] != "data/notes/n.md → skills/s" {
		t.Errorf("references_in_source = %v: want the note only, not the log", refs)
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

// Two names that differ only by case cannot both be checked out on a
// case-insensitive filesystem, so a free slot is not free when its twin exists,
// whatever the filesystem the server runs on.
func TestArtifactMove_RefusesATargetNameThatDiffersOnlyByCase(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	hook := []byte(`{"event":"SessionStart","command":"./run.sh"}`)
	amWriteBytes(t, f.a, "hooks/guard/hook.json", hook, 0o644)
	amWriteBytes(t, f.a, "hooks/guard/run.sh", []byte("#!/bin/sh\n"), 0o755)
	amWriteBytes(t, f.b, "hooks/Guard/hook.json", hook, 0o644)
	xferCommit(t, f.a)
	xferCommit(t, f.b)
	headA, headB := xferHead(t, f.a), xferHead(t, f.b)

	res := f.move(t, `{"target_kb":"kb-b","paths":["hooks/guard"]}`)
	if !res.IsError || !strings.Contains(xferText(res), "hooks/Guard") || !strings.Contains(xferText(res), "case") {
		t.Fatalf("want a refusal naming hooks/Guard and the case, got %s", xferText(res))
	}
	if !amExists(f.a, "hooks/guard/hook.json") || xferHead(t, f.a) != headA || xferHead(t, f.b) != headB {
		t.Error("a refused move changed a KB")
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

// A single-file artifact is the other shape of the same slot.
func TestArtifactMove_RefusesAFileNameThatDiffersOnlyByCase(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	body := []byte("---\nname: helper\ndescription: Helps\n---\nbody\n")
	amWriteBytes(t, f.a, "agents/helper.md", body, 0o644)
	amWriteBytes(t, f.b, "agents/Helper.md", []byte("---\nname: Helper\ndescription: Helps\n---\nbody\n"), 0o644)
	xferCommit(t, f.a)
	xferCommit(t, f.b)
	res := f.move(t, `{"target_kb":"kb-b","paths":["agents/helper.md"]}`)
	if !res.IsError || !strings.Contains(xferText(res), "agents/Helper.md") {
		t.Fatalf("want a refusal naming agents/Helper.md, got %s", xferText(res))
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

// A parent that does not exist yet has no twin: the usual first move into a
// KB with no hooks/ still works.
func TestArtifactMove_NoTwinWhenTheParentIsMissing(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	if err := os.RemoveAll(filepath.Join(f.b.Root, "hooks")); err != nil {
		t.Fatal(err)
	}
	amWriteBytes(t, f.a, "hooks/guard/hook.json", []byte(`{"event":"SessionStart","command":"./run.sh"}`), 0o644)
	amWriteBytes(t, f.a, "hooks/guard/run.sh", []byte("#!/bin/sh\n"), 0o755)
	xferCommit(t, f.a)
	if res := f.move(t, `{"target_kb":"kb-b","paths":["hooks/guard"]}`); res.IsError {
		t.Fatalf("artifact_move: %s", xferText(res))
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

// A file the scan cannot read makes the reference list incomplete, and
// the plan says so (#1500).
func TestArtifactMove_ReferencesReportAnUnreadableFileAsTruncated(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions are not enforced here")
	}
	f := newTransferFixture(t)
	amWriteBytes(t, f.a, "skills/s/SKILL.md", []byte("---\nname: s\ndescription: A skill\n---\nbody\n"), 0o644)
	amWriteBytes(t, f.a, "data/notes/n.md", []byte("---\ntype: Note\ntitle: N\n---\nSee skills/s.\n"), 0o644)
	// Git-ignored, so the commit that precedes a move does not try to read it.
	ex := filepath.Join(f.a.Root, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(ex), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ex, []byte("data/notes/unread.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.a.Root, "data", "notes", "unread.md")
	amWriteBytes(t, f.a, "data/notes/unread.md", []byte("x"), 0o000)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o644) })
	xferCommit(t, f.a)
	_, truncated := refsOf(t, f.move(t, `{"target_kb":"kb-b","paths":["skills/s"]}`))
	if !truncated {
		t.Error("an unreadable file was scanned as complete")
	}
}
