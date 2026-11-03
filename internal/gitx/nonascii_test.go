package gitx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Four listings printed a path like città.png as "citt\303\240.png": the
// changes timeline missed the file's birth and a conflict on it was registered
// under a name no later git command accepted. They use -z now.
func TestListings_NonASCIIAndOddNamesComeBackIntact(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	names := []string{"città.png", "日本.md"}
	if runtime.GOOS != "windows" {
		names = append(names, "tab\tname.md")
	}

	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	first := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Commit(dir, "add", "A", "a@example.test", "GIT_AUTHOR_DATE="+first.Format(time.RFC3339), "GIT_COMMITTER_DATE="+first.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "città.png"), filepath.Join(dir, "città2.png")); err != nil {
		t.Fatal(err)
	}
	second := first.Add(time.Hour)
	if err := Commit(dir, "rename", "A", "a@example.test", "GIT_AUTHOR_DATE="+second.Format(time.RFC3339), "GIT_COMMITTER_DATE="+second.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	commits, err := LogNameStatus(dir, first.Add(-time.Second))
	if err != nil || len(commits) != 2 {
		t.Fatalf("LogNameStatus: %v, %d commits", err, len(commits))
	}
	if f := commits[0].Files; len(f) != 1 || f[0].Status != "R" || f[0].OldPath != "città.png" || f[0].Path != "città2.png" {
		t.Errorf("rename = %#v", f)
	}
	got := map[string]bool{}
	for _, f := range commits[1].Files {
		got[f.Path] = f.Status == "A"
	}
	for _, n := range names {
		if !got[n] {
			t.Errorf("birth of %q missing from %#v", n, commits[1].Files)
		}
	}
}

func TestUnmergedAndIgnoredFiles_NonASCII(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	a, b, branch := setupClones(t)
	writeFileT(t, a, "città.md", "fromA\n")
	runGit(a, "add", "-A")
	runGit(a, "commit", "-m", "a")
	if _, err := runGit(a, "push", "origin", branch); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, b, "città.md", "fromB\n")
	runGit(b, "add", "-A")
	runGit(b, "commit", "-m", "b")
	runGit(b, "fetch", "origin")
	runGit(b, "merge", "origin/"+branch) // conflicts: leaves città.md unmerged

	files, err := UnmergedFiles(b)
	if err != nil || len(files) != 1 || files[0] != "città.md" {
		t.Fatalf("UnmergedFiles = %q, %v", files, err)
	}
	if err := AddPath(b, files[0]); err != nil {
		t.Fatalf("AddPath on the unmerged path: %v", err)
	}

	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, dir, ".gitignore", "*.log\n")
	writeFileT(t, dir, "città.log", "x")
	ignored, err := IgnoredFiles(dir, dir)
	if err != nil || len(ignored) != 1 || ignored[0] != "città.log" {
		t.Fatalf("IgnoredFiles = %q, %v", ignored, err)
	}
}

// With -z a commit with no changed file ends in a lone NUL: it must not leak
// into the commit's Reason (or its files).
func TestLogNameStatus_EmptyCommitHasCleanFields(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	cfgIdentity(t, dir)
	writeFileT(t, dir, "a.md", "x")
	runGit(dir, "add", "-A")
	runGit(dir, "commit", "-m", "first")
	runGit(dir, "commit", "--allow-empty", "-m", "empty with reason\n\nReason: because")
	runGit(dir, "commit", "--allow-empty", "-m", "empty no reason")
	commits, err := LogNameStatus(dir, time.Now().Add(-time.Hour))
	if err != nil || len(commits) != 3 {
		t.Fatalf("LogNameStatus: %v, %d commits", err, len(commits))
	}
	if c := commits[0]; c.Reason != "" || len(c.Files) != 0 {
		t.Errorf("empty commit without reason = %q, files %v", c.Reason, c.Files)
	}
	if c := commits[1]; c.Reason != "because" || len(c.Files) != 0 {
		t.Errorf("empty commit with reason = %q, files %v", c.Reason, c.Files)
	}
	if c := commits[2]; len(c.Files) != 1 || c.Files[0].Path != "a.md" || c.Reason != "" {
		t.Errorf("first commit = %#v", c)
	}
}
