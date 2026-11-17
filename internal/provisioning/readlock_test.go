package provisioning_test

import (
	"encoding/json"
	"errors"
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

// A global projection records no base_dir (or the lockfile's own directory): a
// recorded base_dir naming another directory, "/" included, loses every entry.
func TestReadLockFile_GlobalBaseDirMustBeLockfileDir(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	path := writeRawLock(t, dir, map[string]any{
		"providers": map[string]any{
			"claude": map[string]any{"provider": "claude", "base_dir": string(filepath.Separator), "managed": managedOf("etc/x")},
			"codex":  map[string]any{"provider": "codex", "base_dir": other, "managed": managedOf("ok/SKILL.md")},
			"pi":     map[string]any{"provider": "pi", "base_dir": dir, "managed": managedOf("ok/SKILL.md")},
			"kiro":   map[string]any{"provider": "kiro", "managed": managedOf("ok/SKILL.md")},
		},
	})
	lf, err := provisioning.ReadLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int{"claude": 0, "codex": 0, "pi": 1, "kiro": 1} {
		if n := len(lf.Providers[name].Managed); n != want {
			t.Errorf("%s kept %d entries, want %d", name, n, want)
		}
	}
}

// Windows spellings are not local on any OS, and a lock built in memory never
// reaches the disk through PruneManaged: the entry is refused, the rest pruned.
func TestPruneManaged_RefusesNonLocalEntries(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "client")
	victim := filepath.Join(root, "victim.txt")
	for _, d := range []string{filepath.Join(base, "ok")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(base, "ok", "SKILL.md")
	if err := os.WriteFile(good, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var managed []provisioning.ManagedFile
	for _, p := range []string{"../victim.txt", filepath.ToSlash(victim), `..\victim.txt`, `a\..\..\victim.txt`, `C:\victim.txt`, `C:victim.txt`, "ok/SKILL.md"} {
		managed = append(managed, provisioning.ManagedFile{Kind: "skill", Name: "n", Path: p})
	}
	for _, kind := range []string{"instructions", "mcp", "hook"} {
		managed = append(managed, provisioning.ManagedFile{Kind: kind, Name: "n", Path: "../victim.txt"})
	}
	for _, dry := range []bool{true, false} {
		pruned, err := provisioning.PruneManaged(managed, base, dry)
		var refused *provisioning.PruneRefusedError
		if !errors.As(err, &refused) || len(refused.Refusals) != len(managed)-1 {
			t.Fatalf("dry=%v: err = %v, want %d refusals", dry, err, len(managed)-1)
		}
		if !errors.Is(err, provisioning.ErrUnsafeManagedPath) || !provisioning.IsRefusal(err) {
			t.Errorf("dry=%v: refusal is not ErrUnsafeManagedPath: %v", dry, err)
		}
		if len(pruned) != 1 || pruned[0].Path != "ok/SKILL.md" {
			t.Errorf("dry=%v: pruned %v", dry, gotPaths(pruned))
		}
	}
	if b, err := os.ReadFile(victim); err != nil || string(b) != "keep" {
		t.Fatalf("victim file touched: %q, %v", b, err)
	}
	if _, err := os.Stat(good); !os.IsNotExist(err) {
		t.Errorf("the local entry was not pruned: %v", err)
	}
}

// Backslash and drive spellings are dropped on read on every OS.
func TestReadLockFile_DropsWindowsSpellingsOnEveryOS(t *testing.T) {
	path := writeRawLock(t, t.TempDir(), map[string]any{
		"providers": map[string]any{"claude": map[string]any{"provider": "claude",
			"managed": managedOf(`..\x`, `a\..\..\x`, `a/..\x`, `C:\x`, `c:x`, "ok/SKILL.md", "dots..ok/SKILL.md")}},
	})
	lf, err := provisioning.ReadLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := gotPaths(lf.Providers["claude"].Managed); len(got) != 2 {
		t.Errorf("kept %v", got)
	}
}
