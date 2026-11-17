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
