package provisioning

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1502: a workspace that is a subdirectory of a repository is checked against
// what git tracks there, and its projection is excluded with root-anchored
// patterns. Both used to read `sub/.git` missing as "not a repository".
func TestHygiene_WorkspaceInRepositorySubdirectory(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	for _, f := range []string{".mcp.json", "sub/.mcp.json", "sub2/readme.md"} {
		p := filepath.Join(repo, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")

	sub, sub2 := filepath.Join(repo, "sub"), filepath.Join(repo, "sub2")

	err := CheckWorkspaceHygiene("claude", sub, []string{".mcp.json"}, nil)
	var coll *TrackedCollisionError
	if !errors.As(err, &coll) || len(coll.Paths) != 1 || coll.Paths[0] != ".mcp.json" {
		t.Fatalf("sub: err = %v, want a TrackedCollisionError on .mcp.json", err)
	}
	if err := CheckWorkspaceHygiene("claude", sub2, []string{".mcp.json"}, nil); err != nil {
		t.Errorf("sub2 tracks no .mcp.json: %v", err)
	}

	// Two workspaces of one repository each keep their own block.
	for _, ws := range []string{sub, sub2} {
		if err := EnsureWorkspaceExcluded(ws, []string{".claude/skills/x"}); err != nil {
			t.Fatalf("EnsureWorkspaceExcluded(%s): %v", ws, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/sub/.claude/skills/x", "/sub2/.claude/skills/x"} {
		if !strings.Contains(string(data), want+"\n") {
			t.Errorf("exclude lacks %s:\n%s", want, data)
		}
	}
	if !gitIgnores(t, repo, "sub/.claude/skills/x/SKILL.md") || gitIgnores(t, repo, ".claude/skills/x/SKILL.md") {
		t.Errorf("the patterns must cover the subdirectory only:\n%s", data)
	}

	// Removing one leaves the other.
	if err := RemoveWorkspaceExclusions(sub); err != nil {
		t.Fatal(err)
	}
	if gitIgnores(t, repo, "sub/.claude/skills/x/SKILL.md") || !gitIgnores(t, repo, "sub2/.claude/skills/x/SKILL.md") {
		data, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
		t.Errorf("unbinding sub must keep sub2:\n%s", data)
	}
}
