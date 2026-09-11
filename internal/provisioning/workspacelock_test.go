package provisioning

import (
	"path/filepath"
	"testing"
)

func lockWith(paths ...string) Lock {
	managed := make([]ManagedFile, 0, len(paths))
	for _, p := range paths {
		managed = append(managed, ManagedFile{Kind: "skill", Name: filepath.Base(p), Path: p, Source: "kb:demo"})
	}
	return Lock{AppliedRevision: "r1", Managed: managed}
}

// TestProjections_GlobalAndWorkspacesAreSeparate is WP3's whole point: a
// workspace lookup can never return the global projection's entries, and a
// global lookup can never return a workspace's, because they are different
// maps. Prune deletes what its lock names, so the wrong key here deletes
// someone else's files.
func TestProjections_GlobalAndWorkspacesAreSeparate(t *testing.T) {
	var lf LockFile
	lf.SetProvider("claude", lockWith("global-skill/SKILL.md"))
	lf.SetProjection(Projection{Provider: "claude", Workspace: "/w/dante"}, lockWith("dante-skill/SKILL.md"), "git.example.com/team/dante")
	lf.SetProjection(Projection{Provider: "claude", Workspace: "/w/homelab"}, lockWith("homelab-skill/SKILL.md"), "")

	global := lf.ForProjection(Projection{Provider: "claude"})
	if len(global.Managed) != 1 || global.Managed[0].Path != "global-skill/SKILL.md" {
		t.Fatalf("global projection = %+v", global.Managed)
	}
	dante := lf.ForProjection(Projection{Provider: "claude", Workspace: "/w/dante"})
	if len(dante.Managed) != 1 || dante.Managed[0].Path != "dante-skill/SKILL.md" {
		t.Fatalf("dante projection = %+v", dante.Managed)
	}
	// The base dir is the workspace, not the lockfile's directory: pruning
	// against the wrong base dir is how the wrong files get deleted.
	if dante.BaseDir != "/w/dante" {
		t.Errorf("workspace lock BaseDir = %q, want the workspace path", dante.BaseDir)
	}
	if LockBaseDir(dante, "/home/user") != "/w/dante" {
		t.Error("LockBaseDir did not honour the workspace base dir")
	}
	// A workspace that was never written resolves empty, not to the global one.
	if got := lf.ForProjection(Projection{Provider: "claude", Workspace: "/w/nope"}); len(got.Managed) != 0 {
		t.Errorf("an unknown workspace resolved to %+v", got.Managed)
	}
	if got := lf.WorkspaceRemote("/w/dante"); got != "git.example.com/team/dante" {
		t.Errorf("workspace remote = %q", got)
	}
}

// TestRemoveProjection_LeavesSiblingsAlone: unbinding one workspace must not
// touch another's applied state, nor the global one.
func TestRemoveProjection_LeavesSiblingsAlone(t *testing.T) {
	var lf LockFile
	lf.SetProvider("claude", lockWith("global/SKILL.md"))
	lf.SetProjection(Projection{Provider: "claude", Workspace: "/w/a"}, lockWith("a/SKILL.md"), "")
	lf.SetProjection(Projection{Provider: "codex", Workspace: "/w/a"}, lockWith("a-codex/SKILL.md"), "")
	lf.SetProjection(Projection{Provider: "claude", Workspace: "/w/b"}, lockWith("b/SKILL.md"), "")

	lf.RemoveProjection(Projection{Provider: "claude", Workspace: "/w/a"})

	if got := lf.ForProjection(Projection{Provider: "claude", Workspace: "/w/b"}); len(got.Managed) != 1 {
		t.Error("removing one workspace's projection emptied another's")
	}
	if got := lf.ForProjection(Projection{Provider: "codex", Workspace: "/w/a"}); len(got.Managed) != 1 {
		t.Error("removing one provider's projection emptied a sibling provider's in the same workspace")
	}
	if got := lf.ForProjection(Projection{Provider: "claude"}); len(got.Managed) != 1 {
		t.Error("removing a workspace projection touched the global one")
	}

	// The last provider takes the workspace entry with it: no residue to match.
	lf.RemoveProjection(Projection{Provider: "codex", Workspace: "/w/a"})
	if _, present := lf.Workspaces["/w/a"]; present {
		t.Error("the workspace entry survived its last provider")
	}
}

// TestProjections_Deterministic: status, doctor and prune iterate this, so the
// order must not depend on map iteration.
func TestProjections_Deterministic(t *testing.T) {
	var lf LockFile
	lf.SetProvider("codex", lockWith("x"))
	lf.SetProvider("claude", lockWith("y"))
	lf.SetProjection(Projection{Provider: "codex", Workspace: "/w/b"}, lockWith("z"), "")
	lf.SetProjection(Projection{Provider: "claude", Workspace: "/w/a"}, lockWith("w"), "")

	want := []string{"claude (global)", "codex (global)", "claude in /w/a", "codex in /w/b"}
	for i := 0; i < 20; i++ {
		got := lf.Projections()
		if len(got) != len(want) {
			t.Fatalf("projections = %d, want %d", len(got), len(want))
		}
		for j, p := range got {
			if p.String() != want[j] {
				t.Fatalf("projection[%d] = %q, want %q", j, p.String(), want[j])
			}
		}
	}

	only := lf.WorkspaceProjections("/w/a")
	if len(only) != 1 || only[0].Provider != "claude" {
		t.Errorf("WorkspaceProjections(/w/a) = %+v, want just claude", only)
	}
	// A sync of one workspace may touch exactly its own projections.
	if len(lf.WorkspaceProjections("")) != 2 {
		t.Error("the global projections are not addressable as a workspace-scoped set")
	}
}

// TestLockFileRoundTrip_WorkspacesSurvive, and a pre-D193 lockfile still reads
// with no workspace projection at all.
func TestLockFileRoundTrip_WorkspacesSurvive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.json")

	var lf LockFile
	lf.SetProvider("claude", lockWith("global/SKILL.md"))
	lf.SetProjection(Projection{Provider: "claude", Workspace: "/w/dante"}, lockWith("dante/SKILL.md"), "git.example.com/team/dante")
	if err := WriteLockFile(path, lf); err != nil {
		t.Fatal(err)
	}
	back, err := ReadLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.ForProjection(Projection{Provider: "claude", Workspace: "/w/dante"}); len(got.Managed) != 1 || got.BaseDir != "/w/dante" {
		t.Errorf("workspace projection did not survive a round trip: %+v", got)
	}
	if got := back.ForProjection(Projection{Provider: "claude"}); len(got.Managed) != 1 {
		t.Errorf("global projection did not survive a round trip: %+v", got)
	}
	if back.WorkspaceRemote("/w/dante") != "git.example.com/team/dante" {
		t.Error("the workspace remote guard did not survive a round trip")
	}

	// A lockfile written before D193 has no workspaces key at all, and must
	// read as "global projection only" rather than as anything ambiguous.
	legacy := filepath.Join(dir, "legacy.json")
	if err := WriteLockFile(legacy, LockFile{Providers: map[string]Lock{"claude": lockWith("old/SKILL.md")}}); err != nil {
		t.Fatal(err)
	}
	old, err := ReadLockFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(old.Workspaces) != 0 {
		t.Errorf("a pre-D193 lockfile produced workspace projections: %+v", old.Workspaces)
	}
	if len(old.Projections()) != 1 || !old.Projections()[0].Global() {
		t.Errorf("a pre-D193 lockfile did not read as global-only: %+v", old.Projections())
	}
}
