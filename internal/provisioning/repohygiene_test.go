package provisioning

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withTracked(t *testing.T, tracked map[string]bool) {
	t.Helper()
	prev := gitTrackedFn
	gitTrackedFn = func(string) (map[string]bool, error) { return tracked, nil }
	t.Cleanup(func() { gitTrackedFn = prev })
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestEnsureWorkspaceExcluded_OwnsOnlyItsBlock: the user's own exclude lines
// survive verbatim, the block is idempotent, and removing it leaves them.
func TestEnsureWorkspaceExcluded_OwnsOnlyItsBlock(t *testing.T) {
	dir := gitRepo(t)
	path := filepath.Join(dir, ".git", "info", "exclude")
	userLines := "# my own rules\n*.swp\nbuild/\n"
	if err := os.WriteFile(path, []byte(userLines), 0o644); err != nil {
		t.Fatal(err)
	}

	owned := []string{".claude/skills", ".claude/agents", ".mcp.json"}
	if err := EnsureWorkspaceExcluded(dir, owned); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "*.swp") || !strings.Contains(got, "build/") {
		t.Errorf("the user's own exclude lines were lost:\n%s", got)
	}
	for _, p := range owned {
		if !strings.Contains(got, "/"+p) {
			t.Errorf("owned path %q was not excluded:\n%s", p, got)
		}
	}

	// Idempotent: a second run must not stack a second block.
	if err := EnsureWorkspaceExcluded(dir, owned); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(readFile(t, path), excludeMarkerBegin); n != 1 {
		t.Errorf("the block appears %d times after two runs, want 1", n)
	}

	// Removal takes the block and nothing else.
	if err := RemoveWorkspaceExclusions(dir); err != nil {
		t.Fatal(err)
	}
	after := readFile(t, path)
	if strings.Contains(after, excludeMarkerBegin) || strings.Contains(after, ".claude/skills") {
		t.Errorf("the block survived removal:\n%s", after)
	}
	if !strings.Contains(after, "*.swp") || !strings.Contains(after, "build/") {
		t.Errorf("removal took the user's lines with it:\n%s", after)
	}
}

// TestEnsureWorkspaceExcluded_NeverTouchesGitignore is the rule with the most
// consequence: .gitignore is shared with everyone who clones the repository.
func TestEnsureWorkspaceExcluded_NeverTouchesGitignore(t *testing.T) {
	dir := gitRepo(t)
	gitignore := filepath.Join(dir, ".gitignore")
	before := "node_modules/\n"
	if err := os.WriteFile(gitignore, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorkspaceExcluded(dir, []string{".claude/skills"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, gitignore); got != before {
		t.Errorf(".gitignore was modified:\n%s", got)
	}
}

// TestEnsureWorkspaceExcluded_NotARepoIsANoOp: a plain directory is a legal
// workspace; it simply has no hygiene to keep.
func TestEnsureWorkspaceExcluded_NotARepoIsANoOp(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureWorkspaceExcluded(dir, []string{".claude/skills"}); err != nil {
		t.Fatalf("a non-repository workspace errored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Error("a .git directory was created in a plain directory")
	}
}

// TestCheckWorkspaceHygiene_RefusesTrackedPaths: overwriting a versioned file
// destroys work under version control, so the sync refuses instead.
func TestCheckWorkspaceHygiene_RefusesTrackedPaths(t *testing.T) {
	dir := t.TempDir()
	owned := []string{".claude/skills", ".claude/agents", ".mcp.json", "CLAUDE.md"}
	shared := []string{"CLAUDE.md"}

	// Nothing tracked: nothing to refuse.
	withTracked(t, map[string]bool{"README.md": true, "src/main.go": true})
	if err := CheckWorkspaceHygiene("claude", dir, owned, shared); err != nil {
		t.Fatalf("unrelated tracked files caused a refusal: %v", err)
	}

	// A file inside a directory Cartographer owns: pruning would delete it.
	withTracked(t, map[string]bool{".claude/skills/mine/SKILL.md": true})
	err := CheckWorkspaceHygiene("claude", dir, owned, shared)
	if err == nil {
		t.Fatal("a tracked file under an owned directory was accepted")
	}
	var collision *TrackedCollisionError
	if !asTracked(err, &collision) {
		t.Fatalf("error is not a TrackedCollisionError: %T", err)
	}
	if !strings.Contains(err.Error(), ".claude/skills/mine/SKILL.md") {
		t.Errorf("the refusal does not name the file: %v", err)
	}

	// A whole file Cartographer owns.
	withTracked(t, map[string]bool{".mcp.json": true})
	if err := CheckWorkspaceHygiene("claude", dir, owned, shared); err == nil {
		t.Error("a tracked .mcp.json was accepted")
	}

	// A user-owned file Cartographer writes a *block* into is NOT a collision:
	// a repository legitimately has a CLAUDE.md, and the block coexists.
	withTracked(t, map[string]bool{"CLAUDE.md": true})
	if err := CheckWorkspaceHygiene("claude", dir, owned, shared); err != nil {
		t.Errorf("a tracked CLAUDE.md was refused, but Cartographer only writes a block in it: %v", err)
	}
}

func asTracked(err error, target **TrackedCollisionError) bool {
	e, ok := err.(*TrackedCollisionError)
	if ok {
		*target = e
	}
	return ok
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
