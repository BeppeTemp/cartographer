package provisioning

import (
	"os"
	"path/filepath"
	"testing"
)

// A global hooks.json reached through a symlinked .gemini (a dotfiles
// checkout) is someone else's: emptying it never deletes it or its directory.
func TestRemoveAntigravityHook_KeepsASymlinkedHooksFile(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	cfgDir := filepath.Join(outside, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(cfgDir, "hooks.json")
	if err := os.WriteFile(hooks, []byte(`{"`+antigravityHookKey("h")+`": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, ".gemini")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	_ = removeAntigravityHook(base, "h", "")
	if _, err := os.Stat(hooks); err != nil {
		t.Fatalf("the symlinked hooks.json was deleted: %v", err)
	}
	if fi, err := os.Lstat(filepath.Join(base, ".gemini")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the .gemini link was removed: %v", err)
	}
}
