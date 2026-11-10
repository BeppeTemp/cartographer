package provisioning_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func writeRawLock(t *testing.T, dir string, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, provisioning.LockFileName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func managedOf(paths ...string) []map[string]string {
	var out []map[string]string
	for _, p := range paths {
		out = append(out, map[string]string{"kind": "skill", "name": "n", "path": p})
	}
	return out
}

func gotPaths(m []provisioning.ManagedFile) []string {
	var out []string
	for _, mf := range m {
		out = append(out, mf.Path)
	}
	return out
}

// A path that leaves the client directory is ignored on read, in a global and
// a workspace projection, and reported (#1824).
func TestReadLockFile_DropsNonLocalPaths(t *testing.T) {
	bad := []string{"/etc/x", "../x", "a/../../x", "", ".", `\x`}
	all := append(append([]string{}, bad...), "ok/SKILL.md")
	dir := t.TempDir()
	repo := t.TempDir()
	path := writeRawLock(t, dir, map[string]any{
		"providers": map[string]any{"claude": map[string]any{"provider": "claude", "managed": managedOf(all...)}},
		"workspaces": map[string]any{repo: map[string]any{"providers": map[string]any{
			"claude": map[string]any{"provider": "claude", "base_dir": repo, "managed": managedOf(all...)},
		}}},
	})
	lf, err := provisioning.ReadLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string][]provisioning.ManagedFile{
		"global":    lf.Providers["claude"].Managed,
		"workspace": lf.Workspaces[repo].Providers["claude"].Managed,
	} {
		if got := gotPaths(m); len(got) != 1 || got[0] != "ok/SKILL.md" {
			t.Errorf("%s kept %v, want only ok/SKILL.md", name, got)
		}
	}
	if len(lf.Dropped) != 2*len(bad) {
		t.Fatalf("Dropped = %+v, want %d entries", lf.Dropped, 2*len(bad))
	}
	var ws int
	for _, d := range lf.Dropped {
		if d.Provider != "claude" || d.Kind != "skill" || d.Name != "n" {
			t.Errorf("bad dropped entry %+v", d)
		}
		if d.Workspace == repo {
			ws++
		}
	}
	if ws != len(bad) {
		t.Errorf("workspace dropped = %d, want %d", ws, len(bad))
	}
}

func TestReadLockFile_V1DropsNonLocalPaths(t *testing.T) {
	path := writeRawLock(t, t.TempDir(), map[string]any{
		"applied_revision": "r", "provider": "claude",
		"managed": managedOf("/etc/x", "../x", "ok/SKILL.md"),
	})
	lf, err := provisioning.ReadLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := gotPaths(lf.Providers["claude"].Managed); len(got) != 1 || got[0] != "ok/SKILL.md" {
		t.Errorf("kept %v", got)
	}
	if len(lf.Dropped) != 2 {
		t.Errorf("Dropped = %+v", lf.Dropped)
	}
}

// The file a planted entry names survives a prune driven by the read lock.
func TestReadLockFile_PruneLeavesFileOutsideBaseDir(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "client")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(root, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := writeRawLock(t, base, map[string]any{
		"providers": map[string]any{"claude": map[string]any{"provider": "claude",
			"managed": managedOf("../victim.txt", filepath.ToSlash(victim))}},
	})
	lf, err := provisioning.ReadLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{true, false} {
		if _, err := provisioning.PruneManaged(lf.Providers["claude"].Managed, base, dry); err != nil {
			t.Fatalf("PruneManaged(dry=%v): %v", dry, err)
		}
	}
	if b, err := os.ReadFile(victim); err != nil || string(b) != "keep" {
		t.Fatalf("victim file touched: %q, %v", b, err)
	}
}

// A planted base_dir would move legitimate-looking local paths onto another
// directory: a workspace projection whose base_dir is not its key, and a
// global one whose base_dir is relative or unclean, lose every entry (#1824).
func TestReadLockFile_DropsEntriesOfUntrustedBaseDir(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	path := writeRawLock(t, t.TempDir(), map[string]any{
		"providers": map[string]any{
			"claude": map[string]any{"provider": "claude", "base_dir": "relative/dir", "managed": managedOf(".ssh/authorized_keys")},
			"codex":  map[string]any{"provider": "codex", "managed": managedOf("ok/SKILL.md")},
		},
		"workspaces": map[string]any{repo: map[string]any{"providers": map[string]any{
			"claude": map[string]any{"provider": "claude", "base_dir": home, "managed": managedOf(".ssh/authorized_keys")},
			"codex":  map[string]any{"provider": "codex", "base_dir": repo, "managed": managedOf("ok/SKILL.md")},
		}}},
	})
	lf, err := provisioning.ReadLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(lf.Providers["claude"].Managed); n != 0 {
		t.Errorf("global claude kept %d entries", n)
	}
	if n := len(lf.Workspaces[repo].Providers["claude"].Managed); n != 0 {
		t.Errorf("workspace claude kept %d entries", n)
	}
	if len(lf.Providers["codex"].Managed) != 1 || len(lf.Workspaces[repo].Providers["codex"].Managed) != 1 {
		t.Errorf("trusted projections lost entries: %+v", lf)
	}
	if len(lf.Dropped) != 2 {
		t.Errorf("Dropped = %+v, want 2", lf.Dropped)
	}
}

// ReadLock filters like ReadLockFile.
func TestReadLock_DropsNonLocalPaths(t *testing.T) {
	path := writeRawLock(t, t.TempDir(), map[string]any{
		"applied_revision": "r", "provider": "claude",
		"managed": managedOf("/etc/x", "../x", "ok/SKILL.md"),
	})
	lock, err := provisioning.ReadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := gotPaths(lock.Managed); len(got) != 1 || got[0] != "ok/SKILL.md" {
		t.Errorf("kept %v", got)
	}
}
