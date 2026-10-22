package repodocs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// gitTouch marks a test file that creates or drives git repositories: it
// imports the packages that run git, or runs git itself.
var gitTouch = regexp.MustCompile(`"github.com/BeppeTemp/cartographer/internal/(kb|gitx)"|exec\.Command\("git"`)

// TestGitTestPackagesQuietGit: a package whose tests touch git calls
// gitxtest.QuietGit in its TestMain. Without it a detached `git gc --auto`
// races t.TempDir's cleanup and fails a test that did nothing wrong (#1050);
// the fix was made package by package twice before this check existed.
func TestGitTestPackagesQuietGit(t *testing.T) {
	root := repoRoot(t)
	touches := map[string]bool{}
	quiet := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", "node_modules", "web", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dir, _ := filepath.Rel(root, filepath.Dir(path))
		dir = filepath.ToSlash(dir)
		if gitTouch.Match(b) {
			touches[dir] = true
		}
		if strings.Contains(string(b), "gitxtest.QuietGit()") {
			quiet[dir] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for dir := range touches {
		if !quiet[dir] {
			t.Errorf("%s: its tests touch git but no TestMain calls gitxtest.QuietGit(): a detached git gc can break t.TempDir cleanup", dir)
		}
	}
}
