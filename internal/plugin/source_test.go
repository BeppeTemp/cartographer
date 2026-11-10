package plugin

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func TestParseSourceSpellings(t *testing.T) {
	dir := t.TempDir()
	abs, _ := filepath.EvalSymlinks(dir)
	for _, c := range []struct {
		in          string
		git         bool
		remote, ref string
		bad         bool
	}{
		{in: "https://example.com/team/x.git", git: true, remote: "https://example.com/team/x.git"},
		{in: "https://example.com/team/x.git@v1.2", git: true, remote: "https://example.com/team/x.git", ref: "v1.2"},
		{in: "https://user@example.com/team/x.git", git: true, remote: "https://user@example.com/team/x.git"},
		{in: "https://user@example.com/team/x.git@main", git: true, remote: "https://user@example.com/team/x.git", ref: "main"},
		{in: "https://user@example.com", git: true, remote: "https://user@example.com"},
		{in: "https://user@example.com:8443/x.git", git: true, remote: "https://user@example.com:8443/x.git"},
		{in: "gitlab.example.com:team/forks.git@v1", git: true, remote: "gitlab.example.com:team/forks.git", ref: "v1"},
		{in: "git@gitlab.example.com:team/forks.git", git: true, remote: "git@gitlab.example.com:team/forks.git"},
		{in: "git@gitlab.example.com:team/forks.git@v1.2", git: true, remote: "git@gitlab.example.com:team/forks.git", ref: "v1.2"},
		{in: "ssh://git@example.com/x.git@feature", git: true, remote: "ssh://git@example.com/x.git", ref: "feature"},
		{in: "file:///srv/x.git", git: true, remote: "file:///srv/x.git"},
		{in: "https://example.com/x.git@", bad: true},
		{in: "-x", bad: true},
		{in: "ext::sh -c x", bad: true},
		{in: "ftp://example.com/x.git", bad: true},
		{in: "no-such-folder", bad: true},
		{in: "", bad: true},
	} {
		s, err := ParseSource(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("%q: want an error, got %+v", c.in, s)
			}
			continue
		}
		if err != nil || s.Git != c.git || s.Remote != c.remote || s.Ref != c.ref {
			t.Errorf("%q: %+v, %v; want remote %q ref %q", c.in, s, err, c.remote, c.ref)
		}
	}
	// An existing directory wins, even one named like a remote with a ref.
	s, err := ParseSource(dir)
	if err != nil || s.Git || s.Path != abs || s.String() != abs {
		t.Errorf("folder: %+v, %v", s, err)
	}
	odd := filepath.Join(dir, "x@v1")
	if err := os.Mkdir(odd, 0o755); err != nil {
		t.Fatal(err)
	}
	if s, err := ParseSource(odd); err != nil || s.Git {
		t.Errorf("folder with @: %+v, %v", s, err)
	}
	if got := (Source{Git: true, Remote: "https://example.com/x.git", Ref: "v1"}).String(); got != "https://example.com/x.git@v1" {
		t.Errorf("String = %q", got)
	}
}

func TestFolderSnapshot(t *testing.T) {
	dir := t.TempDir()
	put := func(p, c string, mode os.FileMode) {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), mode); err != nil {
			t.Fatal(err)
		}
	}
	put(ManifestFile, validManifest, 0o644)
	put("skills/helper/SKILL.md", skillMD, 0o644)
	put("README.md", "ignored", 0o644)
	put("docs/page.txt", "ignored", 0o644)
	put(".git/config", "ignored", 0o644)
	put(".gitignore", "ignored", 0o644)
	src, err := ParseSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, sha, err := Fetch(context.Background(), src, t.TempDir())
	if err != nil || sha != "" {
		t.Fatalf("Fetch: %v, sha %q", err, sha)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	if got := strings.Join(paths, ","); !strings.Contains(got, ManifestFile) || !strings.Contains(got, "skills/helper/SKILL.md") || strings.Contains(got, "README") || strings.Contains(got, "docs/") || strings.Contains(got, ".git") {
		t.Errorf("paths = %s", got)
	}
	if _, probs := ReadTree(files, Options{}); len(probs) != 0 {
		t.Errorf("snapshot does not validate: %v", probs)
	}

	if err := os.Symlink("SKILL.md", filepath.Join(dir, "skills/helper/link.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	// A symlink is a finding of the tree list, like every other (`"path": reason`).
	files, _, err = Fetch(context.Background(), src, t.TempDir())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	_, probs := ReadTree(files, Options{})
	if want := `"skills/helper/link.md": a symlink or non-regular file`; !slices.Contains(probs, want) {
		t.Errorf("problems = %q, want %q", probs, want)
	}
}

func TestFetchGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	work := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", work}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for p, c := range map[string]string{ManifestFile: validManifest, "skills/helper/SKILL.md": skillMD, "README.md": "x", "docs/page.txt": "x"} {
		full := filepath.Join(work, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	run("add", "-A")
	run("-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-q", "-m", "one")
	remote := (&url.URL{Scheme: "file", Path: filepath.ToSlash(work)}).String()

	src, err := ParseSource(remote)
	if err != nil {
		t.Fatal(err)
	}
	files, sha, err := Fetch(context.Background(), src, filepath.Join(t.TempDir(), "plugins"))
	if err != nil || len(sha) != 40 {
		t.Fatalf("Fetch: %v, sha %q", err, sha)
	}
	if _, probs := ReadTree(files, Options{}); len(probs) != 0 {
		t.Errorf("fetched tree does not validate: %v", probs)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, "docs/") {
			t.Errorf("git fetch kept %s", f.Path)
		}
	}

	t.Setenv("PATH", "")
	if _, _, err := Fetch(context.Background(), src, t.TempDir()); err != gitx.ErrGitMissing {
		t.Errorf("without git: %v", err)
	}
	// A folder needs no git.
	folder, _ := ParseSource(work)
	if _, _, err := Fetch(context.Background(), folder, t.TempDir()); err != nil {
		t.Errorf("folder without git: %v", err)
	}
}

func TestFolderTotalCap(t *testing.T) {
	dir := t.TempDir()
	chunk := strings.Repeat("x", contract.ArtifactMaxFileSize)
	for i := 0; i < MaxBytes/contract.ArtifactMaxFileSize+1; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.bin", i)), []byte(chunk), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src, err := ParseSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Fetch(context.Background(), src, t.TempDir()); err == nil || !strings.Contains(err.Error(), "bytes in all") {
		t.Errorf("folder over the total cap: %v", err)
	}
}
