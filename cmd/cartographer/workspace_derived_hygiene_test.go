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

// A lock that already records an MCP entry in a file git now tracks: the
// derived sync neither prunes it from the file nor drops it from the lock.
func TestDerivedWorkspace_TrackedMCPFileKeepsItsLockEntries(t *testing.T) {
	const original = "{\n  \"mcpServers\": {\"tools\": {\"url\": \"https://tools.example.com/mcp\"}}\n}\n"
	ws := trackedRepo(t, map[string]string{".kiro/settings/mcp.json": original})
	p := kiroProjection(ws, true)

	dir := t.TempDir()
	var lf provisioning.LockFile
	lf.SetProjection(p.Key(), provisioning.Lock{Provider: "kiro", BaseDir: ws, ProjectScope: true, Managed: []provisioning.ManagedFile{
		{Kind: "mcp", Name: "tools", Path: ".kiro/settings/mcp.json", ContentHash: "h-mcp"},
		{Kind: "mcp", Name: "bridge", Path: ".kiro/settings/mcp.json", Source: provisioning.SourceClient},
	}}, "")
	if err := provisioning.WriteLockFile(lockFilePath(dir), lf); err != nil {
		t.Fatal(err)
	}
	m := skillAndMCPManifest()
	m.Artifacts = m.Artifacts[:1]
	if _, err := materializeForProviders(map[string]provisioning.Manifest{projectionKey(p): m}, []syncProjection{p}, dir, "", false, false, portabilityOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(ws, ".kiro", "settings", "mcp.json")); string(got) != original {
		t.Errorf("the tracked file changed:\n%s", got)
	}
	after, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, mf := range after.ForProjection(p.Key()).Managed {
		if mf.Kind == "mcp" {
			names[mf.Name] = true
		}
	}
	if !names["tools"] || !names["bridge"] {
		t.Errorf("lock entries lost: %v", names)
	}
}

// An artifact name that cannot be projected skips a derived projection with a
// warning; an explicit binding still fails.
func TestDerivedWorkspace_InvalidNameSkipsOnlyDerived(t *testing.T) {
	ws := trackedRepo(t, map[string]string{"README.md": "x\n"})
	bad := func(p syncProjection) error {
		m := provisioning.Manifest{Revision: "r", Artifacts: []provisioning.Artifact{{Kind: "skill", Name: "x\n!/src", Source: "bundle", BuiltIn: true, ContentHash: "h"}}}
		_, err := materializeForProviders(map[string]provisioning.Manifest{projectionKey(p): m}, []syncProjection{p}, t.TempDir(), "", false, false, portabilityOptions{}, nil)
		return err
	}
	if err := bad(kiroProjection(ws, true)); err != nil {
		t.Errorf("derived = %v, want skipped", err)
	}
	var badName *provisioning.InvalidArtifactNameError
	if err := bad(kiroProjection(ws, false)); !errors.As(err, &badName) {
		t.Errorf("explicit = %v, want the refusal", err)
	}
}
