package provisioning

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A file the process may not remove (#2178) is a per-file refusal, not a fatal
// error: the others are pruned and the caller keeps the refused one in its lock.
func TestPruneManaged_PermissionDeniedIsRefusal(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX directory permissions and a non-root user")
	}
	base := t.TempDir()
	for _, d := range []string{"ro", "rw"} {
		if err := os.MkdirAll(filepath.Join(base, ".claude", d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, ".claude", d, "a.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ro := filepath.Join(base, ".claude", "ro")
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })

	managed := []ManagedFile{
		{Path: ".claude/ro/a.md", Kind: "agent", Name: "ro"},
		{Path: ".claude/rw/a.md", Kind: "agent", Name: "rw"},
	}
	pruned, err := PruneManaged(managed, base, false)
	var refused *PruneRefusedError
	if !errors.As(err, &refused) || len(refused.Refusals) != 1 || refused.Refusals[0].File.Name != "ro" || !IsRefusal(err) {
		t.Fatalf("err = %v, want one refusal for ro", err)
	}
	if len(pruned) != 1 || pruned[0].Name != "rw" {
		t.Errorf("pruned = %v, want rw only", pruned)
	}
}

// A permission error stripping a hook registration or an instructions block is
// the same per-file refusal as a denied file removal, not a fatal error that
// aborts the whole sync (#2218).
func TestPruneManaged_PermissionDeniedOnSharedFilesIsARefusal(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not deny the owner on Windows or as root")
	}
	baseDir := t.TempDir()
	claude := filepath.Join(baseDir, ".claude")
	if err := os.MkdirAll(filepath.Join(claude, "hooks", "notify"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"hooks":{"PostToolUse":[{"matcher":"x","hooks":[{"type":"command","command":` +
		`"` + filepath.ToSlash(filepath.Join(claude, "hooks", "notify", "notify.sh")) + `"}]}]}}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "hooks", "notify", "hook.json"), []byte(`{"event":"PostToolUse"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	block := "<!-- cartographer:instructions:begin — block managed by Cartographer, do not edit by hand -->\nRule.\n<!-- cartographer:instructions:end -->\n"
	if err := os.WriteFile(filepath.Join(claude, "CLAUDE.md"), []byte("# mine\n\n"+block), 0o644); err != nil {
		t.Fatal(err)
	}
	// Neither settings.json nor CLAUDE.md can be rewritten (atomic write
	// needs the directory), and the hook files stay readable.
	if err := os.Chmod(claude, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(claude, 0o755) })

	managed := []ManagedFile{
		{Kind: "hook", Name: "notify", Path: ".claude/hooks/notify/hook.json", ContentHash: "h"},
		{Kind: "instructions", Name: "kb-a", Path: ".claude/CLAUDE.md", ContentHash: "h"},
	}
	pruned, err := PruneManaged(managed, baseDir, false)
	if err == nil || !IsRefusal(err) || !errors.Is(err, ErrPruneDenied) {
		t.Fatalf("err = %v, want a ErrPruneDenied refusal", err)
	}
	if len(pruned) != 0 {
		t.Errorf("pruned = %+v, want nothing", pruned)
	}
	if _, serr := os.Stat(filepath.Join(claude, "hooks", "notify", "hook.json")); serr != nil {
		t.Errorf("the hook file left while its registration stays: %v", serr)
	}
}

// Any removal error, not only a permission one (a Windows sharing violation
// is not fs.ErrPermission), is a per-file refusal: here a non-empty directory
// stands in for a file the OS refuses to remove, on every platform (#2218).
func TestPruneManaged_AnyRemovalErrorIsARefusal(t *testing.T) {
	baseDir := t.TempDir()
	dir := filepath.Join(baseDir, ".claude", "skills", "a", "stuck")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	managed := []ManagedFile{{Kind: "skill", Name: "a", Path: ".claude/skills/a/stuck", ContentHash: "h"}}
	pruned, err := PruneManaged(managed, baseDir, false)
	if err == nil || !IsRefusal(err) || !errors.Is(err, ErrPruneDenied) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if len(pruned) != 0 {
		t.Errorf("pruned = %+v", pruned)
	}
}
