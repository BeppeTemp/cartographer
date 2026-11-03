package provisioning

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stripExcludeBlockT(t *testing.T, content, key string) string {
	t.Helper()
	got, err := stripExcludeBlock(content, key)
	if err != nil {
		t.Fatalf("stripExcludeBlock: %v", err)
	}
	return got
}

// #1502: an exclude block whose end line is missing used to swallow every later
// line of the user's file. Now the file is left byte for byte and the sync gets
// a warning-class error.
func TestStripExcludeUnterminated(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	path := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := "user-a\n" + excludeMarkerBegin + "\n/pattern\nuser-b\nuser-c\n"
	for name, content := range map[string]string{
		"orphan begin": orig,
		"orphan end":   "user-a\n" + excludeMarkerEnd + "\nuser-b\n",
		"two begins":   excludeMarkerBegin + "\n/x\n" + excludeMarkerBegin + "\n/y\n" + excludeMarkerEnd + "\nuser-b\n",
		"end before":   excludeMarkerEnd + "\nuser-b\n" + excludeMarkerBegin + "\n",
		"stale orphan": "user-a\n# >>> cartographer (managed: gone) >>>\n/p\nuser-b\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			err := EnsureWorkspaceExcluded(repo, []string{".claude/skills/x"})
			if name == "stale orphan" {
				// A keyed block of a removed worktree with no end is kept, not dropped.
				if err != nil {
					t.Fatal(err)
				}
				if got, _ := os.ReadFile(path); !strings.Contains(string(got), "user-b\n") {
					t.Errorf("user lines lost:\n%s", got)
				}
				return
			}
			if !errors.Is(err, ErrExcludeBlockCorrupted) || !ExcludeWarning(err) {
				t.Fatalf("err = %v, want a warning-class ErrExcludeBlockCorrupted", err)
			}
			if err := RemoveWorkspaceExclusions(repo); !errors.Is(err, ErrExcludeBlockCorrupted) {
				t.Fatalf("remove: %v", err)
			}
			if got, _ := os.ReadFile(path); string(got) != content {
				t.Errorf("file changed:\n%q\nwant\n%q", got, content)
			}
		})
	}
}

// #1502: a CRLF exclude file stays CRLF, byte for byte outside the block, and
// the block follows its endings.
func TestExcludeKeepsCRLF(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	path := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	user := "# mine\r\nbuild/\r\n"
	if err := os.WriteFile(path, []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorkspaceExcluded(repo, []string{".claude/skills/x"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), user) || strings.Contains(strings.ReplaceAll(string(got), "\r\n", ""), "\n") {
		t.Errorf("the file must stay CRLF throughout:\n%q", got)
	}
	if err := RemoveWorkspaceExclusions(repo); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != user {
		t.Errorf("after the round trip: %q, want %q", got, user)
	}
}

// A CRLF file whose last line has no terminator gets CRLF before the block too.
func TestExcludeKeepsCRLFWithoutFinalTerminator(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	path := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# mine\r\nbuild/"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorkspaceExcluded(repo, []string{".claude/skills/x"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(strings.ReplaceAll(string(got), "\r\n", ""), "\n") {
		t.Errorf("mixed line endings:\n%q", got)
	}
}
