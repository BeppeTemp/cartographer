package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// trackedRepo is a git repository tracking the given files (path -> content).
func trackedRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ws := t.TempDir()
	if out, err := exec.Command("git", "-C", ws, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	for rel, content := range files {
		abs := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", ws, "add", rel).CombinedOutput(); err != nil {
			t.Skipf("git add: %v %s", err, out)
		}
	}
	return ws
}

func kiroProjection(ws string, derived bool) syncProjection {
	p := syncProjection{Provider: "kiro", Workspace: ws, BaseDir: ws, Scope: provisioning.ScopeProject, Derived: derived}
	if derived {
		p.SharedSkipped, p.SkipKinds = derivedSharedSkips(p.Provider, ws)
	}
	return p
}

func skillAndMCPManifest() provisioning.Manifest {
	m := provisioning.Manifest{Artifacts: []provisioning.Artifact{
		{Kind: "skill", Name: "example", Source: "bundle", BuiltIn: true, ContentHash: "h-skill",
			Files: []provisioning.ArtifactFile{{Path: "SKILL.md", Content: []byte("# example\n")}}},
		{Kind: "mcp", Name: "tools", Source: "bundle", BuiltIn: true, ContentHash: "h-mcp",
			Files: []provisioning.ArtifactFile{{Path: "tools.json", Content: []byte(`{"type":"http","url":"https://tools.example.com/mcp"}`)}}},
	}}
	m.Revision = "r"
	return m
}

func sync1181(t *testing.T, projections []syncProjection) error {
	t.Helper()
	manifests := map[string]provisioning.Manifest{}
	for _, p := range projections {
		manifests[projectionKey(p)] = skillAndMCPManifest()
	}
	dir := t.TempDir()
	_, err := materializeForProviders(manifests, projections, dir, "", false, false, portabilityOptions{}, nil)
	return err
}

// #1181: a derived workspace that tracks the MCP file Kiro's projection writes
// whole syncs, and the file stays byte-identical.
func TestDerivedWorkspace_TrackedMCPFileIsLeftAlone(t *testing.T) {
	const original = "{\"mcpServers\": {}}\n"
	ws := trackedRepo(t, map[string]string{".kiro/settings/mcp.json": original})
	p := kiroProjection(ws, true)
	if len(p.SharedSkipped) != 1 || p.SharedSkipped[0] != ".kiro/settings/mcp.json" {
		t.Fatalf("SharedSkipped = %v, want the tracked mcp file", p.SharedSkipped)
	}
	if err := sync1181(t, []syncProjection{p}); err != nil {
		t.Fatalf("sync = %v, want no error", err)
	}
	got, _ := os.ReadFile(filepath.Join(ws, ".kiro", "settings", "mcp.json"))
	if !bytes.Equal(got, []byte(original)) {
		t.Errorf("the tracked file changed:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(ws, ".kiro", "skills", "example", "SKILL.md")); err != nil {
		t.Errorf("the skill was not projected: %v", err)
	}
}

// #1181: a real name clash in one derived workspace skips that projection with
// a warning and the others in the run are projected; an explicit binding with
// the same clash is still refused.
func TestDerivedWorkspace_ClashSkipsOnlyThatProjection(t *testing.T) {
	clash := trackedRepo(t, map[string]string{".kiro/skills/example/SKILL.md": "mine\n"})
	clean := trackedRepo(t, map[string]string{"README.md": "x\n"})

	err := sync1181(t, []syncProjection{kiroProjection(clash, true), kiroProjection(clean, true)})
	if err != nil {
		t.Fatalf("sync = %v, want the clash skipped", err)
	}
	if b, _ := os.ReadFile(filepath.Join(clash, ".kiro", "skills", "example", "SKILL.md")); string(b) != "mine\n" {
		t.Errorf("the tracked skill changed: %q", b)
	}
	if _, err := os.Stat(filepath.Join(clean, ".kiro", "skills", "example", "SKILL.md")); err != nil {
		t.Errorf("the other workspace was not projected: %v", err)
	}

	err = sync1181(t, []syncProjection{kiroProjection(clash, false)})
	var collision *provisioning.TrackedCollisionError
	if !errors.As(err, &collision) {
		t.Errorf("explicit binding = %v, want the refusal", err)
	}
}
