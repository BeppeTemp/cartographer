package mcpserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/execbit"
)

// noExecFS makes a KB behave as on NTFS: git ignores the filesystem mode
// (core.fileMode=false), so a new file is staged as 100644 and a tracked one
// keeps its index mode. strip names tracked files that are executable: each is
// recorded as 100755 in a commit (on NTFS the fixture's own commit wrote 100644
// whatever the file mode), then loses the bit on disk, as a filesystem that
// cannot carry it reads every file.
func noExecFS(t *testing.T, root string, strip ...string) {
	t.Helper()
	if len(strip) > 0 {
		args := append([]string{"-C", root, "update-index", "--chmod=+x", "--"}, strip...)
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git update-index: %v: %s", err, out)
		}
		if out, err := exec.Command("git", "-C", root, "-c", "user.name=fixture", "-c", "user.email=fixture@example.com", "commit", "-q", "--allow-empty", "-m", "exec bits").CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v: %s", err, out)
		}
	}
	if out, err := exec.Command("git", "-C", root, "config", "core.fileMode", "false").CombinedOutput(); err != nil {
		t.Fatalf("git config: %v: %s", err, out)
	}
	for _, rel := range strip {
		if err := os.Chmod(filepath.Join(root, filepath.FromSlash(rel)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// gitMode is the mode the HEAD tree of root records for rel.
func gitMode(t *testing.T, root, rel string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "ls-tree", "HEAD", "--", rel).Output()
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		t.Fatalf("%s not in HEAD of %s", rel, root)
	}
	return f[0]
}

// The execute bit of a moved file comes from the source's index, not from its
// filesystem, and reaches the target's commit as 100755 on a filesystem that
// cannot carry it (#1712).
func TestArtifactMove_KeepsTrackedExecModeWithoutFilesystemBit(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	amWriteBytes(t, f.a, "skills/query-rete/SKILL.md", []byte(amSkillMD), 0o644)
	amWriteBytes(t, f.a, "skills/query-rete/scripts/run.sh", []byte("#!/bin/sh\n"), 0o755)
	amWriteBytes(t, f.a, "hooks/guard/run.sh", []byte("#!/bin/sh\n"), 0o755)
	amWriteBytes(t, f.a, "hooks/guard/hook.json", []byte(`{"event":"PreToolUse","command":"run.sh"}`), 0o644)
	xferCommit(t, f.a)
	noExecFS(t, f.a.Root, "skills/query-rete/scripts/run.sh", "hooks/guard/run.sh")
	noExecFS(t, f.b.Root)

	res := f.move(t, `{"target_kb":"kb-b","paths":["skills/query-rete","hooks/guard"]}`)
	if res.IsError {
		t.Fatalf("artifact_move: %s", xferText(res))
	}
	for rel, want := range map[string]string{
		"skills/query-rete/scripts/run.sh": "100755",
		"skills/query-rete/SKILL.md":       "100644",
		"hooks/guard/run.sh":               "100755",
		"hooks/guard/hook.json":            "100644",
	} {
		if got := gitMode(t, f.b.Root, rel); got != want {
			t.Errorf("%s committed as %s in the target, want %s", rel, got, want)
		}
	}
}

func TestArtifactMove_SingleFileKeepsTrackedExecMode(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	amWriteBytes(t, f.a, "templates/job.md", []byte("---\ntype: Note\n---\nx\n"), 0o755)
	xferCommit(t, f.a)
	noExecFS(t, f.a.Root, "templates/job.md")
	noExecFS(t, f.b.Root)
	res := f.move(t, `{"target_kb":"kb-b","paths":["templates/job.md"]}`)
	if res.IsError {
		t.Fatalf("artifact_move: %s", xferText(res))
	}
	if got := gitMode(t, f.b.Root, "templates/job.md"); got != "100755" {
		t.Errorf("committed as %s, want 100755", got)
	}
}

func TestConceptTransfer_KeepsTrackedExecModeWithoutFilesystemBit(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/dossier/index.md", "---\ntype: Note\ntitle: Dossier\n---\nbody\n")
	for name, mode := range map[string]os.FileMode{"check.sh": 0o755, "data.txt": 0o644} {
		p := filepath.Join(f.a.DataRoot(), "notes", "dossier", name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	xferCommit(t, f.a)
	rel := func(k string) string {
		r, err := filepath.Rel(f.a.Root, filepath.Join(f.a.DataRoot(), k))
		if err != nil {
			t.Fatal(err)
		}
		return filepath.ToSlash(r)
	}
	noExecFS(t, f.a.Root, rel("notes/dossier/check.sh"))
	noExecFS(t, f.b.Root)

	res := f.transfer(t, `{"target_kb":"kb-b","moves":[{"source_id":"notes/dossier","target_id":"wiki/dossier"}]}`)
	if res.IsError {
		t.Fatalf("concept_transfer: %s", xferText(res))
	}
	dst := func(k string) string {
		r, err := filepath.Rel(f.b.Root, filepath.Join(f.b.DataRoot(), k))
		if err != nil {
			t.Fatal(err)
		}
		return filepath.ToSlash(r)
	}
	if got := gitMode(t, f.b.Root, dst("wiki/dossier/check.sh")); got != "100755" {
		t.Errorf("check.sh committed as %s, want 100755", got)
	}
	if got := gitMode(t, f.b.Root, dst("wiki/dossier/data.txt")); got != "100644" {
		t.Errorf("data.txt committed as %s, want 100644", got)
	}
}

// Where the filesystem carries the bit, a chmod +x the index does not hold yet
// is not lost on the move: executable = index 100755 OR filesystem bit.
func TestArtifactMove_FilesystemExecBitSurvivesWhenIndexSaysNo(t *testing.T) {
	t.Parallel()
	if !execbit.Supported {
		t.Skip("the filesystem has no execute bit")
	}
	f := newTransferFixture(t)
	amWriteBytes(t, f.a, "skills/query-rete/SKILL.md", []byte(amSkillMD), 0o644)
	amWriteBytes(t, f.a, "skills/query-rete/scripts/run.sh", []byte("#!/bin/sh\n"), 0o644)
	xferCommit(t, f.a)
	// fileMode=false keeps the later chmod out of the pre-move commit: the
	// index stays 100644 while the disk says executable.
	noExecFS(t, f.a.Root)
	noExecFS(t, f.b.Root)
	if err := os.Chmod(filepath.Join(f.a.Root, "skills/query-rete/scripts/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := f.move(t, `{"target_kb":"kb-b","paths":["skills/query-rete"]}`)
	if res.IsError {
		t.Fatalf("artifact_move: %s", xferText(res))
	}
	if got := gitMode(t, f.b.Root, "skills/query-rete/scripts/run.sh"); got != "100755" {
		t.Errorf("committed as %s, want 100755", got)
	}
}

// A file git does not track keeps the filesystem's answer.
func TestExecFromIndex_UntrackedKeepsFilesystemAnswer(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	amWriteBytes(t, k, "skills/s/a.sh", []byte("x"), 0o755)
	amWriteBytes(t, k, "skills/s/b.txt", []byte("x"), 0o644)
	files := []transferFile{{rel: "a.sh", exec: execbit.IsExecutable(0o755)}, {rel: "b.txt"}}
	if err := execFromIndex(k, filepath.Join(k.Root, "skills/s"), files); err != nil {
		t.Fatal(err)
	}
	if files[0].exec != execbit.Supported || files[1].exec {
		t.Errorf("untracked files changed: %+v", files)
	}
}
