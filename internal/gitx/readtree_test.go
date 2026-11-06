package gitx

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func treeRepo(t *testing.T, files map[string]string) (remote, work string) {
	t.Helper()
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	work = t.TempDir()
	mustGit(t, work, "init", "-q")
	mustGit(t, work, "config", "user.email", "test@example.com")
	mustGit(t, work, "config", "user.name", "Test")
	for p, c := range files {
		full := filepath.Join(work, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, work, "add", "-A")
	mustGit(t, work, "update-index", "--chmod=+x", "run.sh")
	mustGit(t, work, "commit", "-q", "-m", "one")
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(work)}).String(), work
}

var lim = TreeLimits{MaxEntries: 10, MaxFileSize: 1000, Skip: func(p string) bool { return strings.HasPrefix(p, "README") }}

func fetch(t *testing.T, remote, ref string) ([]TreeEntry, string, error) {
	t.Helper()
	return FetchTree(context.Background(), remote, ref, filepath.Join(t.TempDir(), "tmp"), lim)
}

func TestFetchTreeRefs(t *testing.T) {
	remote, work := treeRepo(t, map[string]string{"a.txt": "one\n", "run.sh": "#!/bin/sh\n", "README.md": "skipped", "d/b.txt": "b"})
	first := mustGit(t, work, "rev-parse", "HEAD")
	mustGit(t, work, "tag", "v1")
	mustGit(t, work, "branch", "-M", "trunk")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, work, "add", "-A")
	mustGit(t, work, "update-index", "--chmod=+x", "run.sh") // core.filemode is off on Windows
	mustGit(t, work, "commit", "-q", "-m", "two")
	head := mustGit(t, work, "rev-parse", "HEAD")

	for _, c := range []struct{ ref, sha, a string }{
		{"", head, "two\n"}, {"trunk", head, "two\n"}, {"v1", first, "one\n"}, {first, first, "one\n"},
	} {
		entries, sha, err := fetch(t, remote, c.ref)
		if err != nil {
			t.Fatalf("ref %q: %v", c.ref, err)
		}
		if sha != c.sha {
			t.Errorf("ref %q: sha %s, want %s", c.ref, sha, c.sha)
		}
		got := map[string]TreeEntry{}
		for _, e := range entries {
			got[e.Path] = e
		}
		if len(got) != 3 || string(got["a.txt"].Content) != c.a || got["d/b.txt"].Path == "" {
			t.Errorf("ref %q: entries %v", c.ref, got)
		}
		if _, ok := got["README.md"]; ok {
			t.Errorf("ref %q: README.md is skipped", c.ref)
		}
		if !got["run.sh"].Executable || got["a.txt"].Executable {
			t.Errorf("ref %q: executable bits wrong", c.ref)
		}
	}
	if _, _, err := fetch(t, remote, "nope"); err == nil {
		t.Error("a missing ref must fail")
	}
}

func TestFetchTreeKeepsCommittedBytes(t *testing.T) {
	remote, _ := treeRepo(t, map[string]string{".gitattributes": "* text eol=crlf\n", "a.txt": "x\ny\n", "run.sh": "z"})
	entries, _, err := fetch(t, remote, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Path == "a.txt" && string(e.Content) != "x\ny\n" {
			t.Errorf("a.txt = %q, want the committed LF bytes", e.Content)
		}
	}
}

func TestFetchTreeRefusals(t *testing.T) {
	add := func(work, mode, oid, path string) {
		mustGit(t, work, "update-index", "--add", "--cacheinfo", mode+","+oid+","+path)
		mustGit(t, work, "commit", "-q", "-m", path)
	}
	for _, c := range []struct{ name, mode, content, want string }{
		{"link", "120000", "target", "link: a symlink"},
		{"gitlink", "160000", "", "gitlink: a submodule"},
	} {
		remote, work := treeRepo(t, map[string]string{"run.sh": "x"})
		oid := strings.Repeat("1", 40)
		if c.content != "" {
			f := filepath.Join(t.TempDir(), "blob")
			if err := os.WriteFile(f, []byte(c.content), 0o644); err != nil {
				t.Fatal(err)
			}
			oid = mustGit(t, work, "hash-object", "-w", f)
		}
		add(work, c.mode, oid, c.name)
		if _, _, err := fetch(t, remote, ""); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}

	remote, _ := treeRepo(t, map[string]string{"run.sh": "x", "big.bin": strings.Repeat("x", 1001)})
	if _, _, err := fetch(t, remote, ""); err == nil || !strings.Contains(err.Error(), "big.bin: 1001 bytes") {
		t.Errorf("oversized blob: %v", err)
	}
	many := map[string]string{"run.sh": "x"}
	for i := 0; i < 11; i++ {
		many[string(rune('a'+i))+".txt"] = "x"
	}
	remote, _ = treeRepo(t, many)
	if _, _, err := fetch(t, remote, ""); err == nil || !strings.Contains(err.Error(), "more than 10 files") {
		t.Errorf("too many entries: %v", err)
	}
}

func TestFetchTreeInputs(t *testing.T) {
	remote, _ := treeRepo(t, map[string]string{"run.sh": "x"})
	for _, ref := range []string{"--upload-pack=x"} {
		if _, _, err := fetch(t, remote, ref); err == nil {
			t.Errorf("ref %q must be refused", ref)
		}
	}
	if _, _, err := fetch(t, "ext::sh -c x", ""); err == nil {
		t.Error("a transport helper must be refused")
	}
	t.Setenv("PATH", "")
	if _, _, err := fetch(t, remote, ""); !errors.Is(err, ErrGitMissing) {
		t.Errorf("without git on PATH: %v", err)
	}
}

func TestFetchTreeTotalCap(t *testing.T) {
	remote, _ := treeRepo(t, map[string]string{"run.sh": "x", "a.bin": strings.Repeat("a", 600), "b.bin": strings.Repeat("b", 600)})
	l := lim
	l.MaxTotal = 1000
	_, _, err := FetchTree(context.Background(), remote, "", filepath.Join(t.TempDir(), "tmp"), l)
	if err == nil || !strings.Contains(err.Error(), "more than 1000 bytes in all") {
		t.Errorf("over the total cap: %v", err)
	}
	l.MaxTotal = 1201
	if _, _, err := FetchTree(context.Background(), remote, "", filepath.Join(t.TempDir(), "tmp"), l); err != nil {
		t.Errorf("at the total cap: %v", err)
	}
}

func TestFetchTreeDefaultDeadline(t *testing.T) {
	old := FetchTimeout
	FetchTimeout = 300 * time.Millisecond
	defer func() { FetchTimeout = old }()
	tmp := removeWhenFree(t)
	start := time.Now()
	_, _, err := FetchTree(context.Background(), silentRemote(t), "", tmp, lim)
	if err == nil || !errors.Is(err, ErrCloneTimeout) || time.Since(start) > 10*time.Second {
		t.Errorf("silent remote: %v after %v", err, time.Since(start))
	}
}
