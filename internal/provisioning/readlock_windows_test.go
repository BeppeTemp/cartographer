//go:build windows

package provisioning_test

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// Drive-relative and rooted spellings are not local on Windows (#1824).
func TestReadLockFile_WindowsDropsDriveAndRootedPaths(t *testing.T) {
	path := writeRawLock(t, t.TempDir(), map[string]any{
		"providers": map[string]any{"claude": map[string]any{"provider": "claude",
			"managed": managedOf(`C:\x`, `\x`, `C:x`, "NUL", "ok/SKILL.md")}},
	})
	lf, err := provisioning.ReadLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := gotPaths(lf.Providers["claude"].Managed); len(got) != 1 || got[0] != "ok/SKILL.md" {
		t.Errorf("kept %v", got)
	}
	if len(lf.Dropped) != 4 {
		t.Errorf("Dropped = %+v", lf.Dropped)
	}
}
