package provisioning_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// #1502: a sync that changes nothing rewrote the client's settings/hook
// registration files at every session start (truncate, write): a watcher woke
// up and a reader could see an empty file. The second run must leave every file
// the user shares with us, its inode and its mtime, alone.
func TestSecondSyncLeavesSharedFilesAlone(t *testing.T) {
	for _, p := range []configurator.Provider{
		configurator.ProviderClaudeCode, configurator.ProviderCodex, configurator.ProviderKiro, configurator.ProviderCrush,
	} {
		t.Run(string(p), func(t *testing.T) {
			if !provisioning.SupportsSessionHook(p) {
				t.Skip("no session hook")
			}
			base := t.TempDir()
			lock, err := provisioning.EnsureBootstrapHook(base, p, provisioning.Lock{}, false)
			if err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour)
			shared := map[string]fs.FileInfo{}
			_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
				// The hook's own files are Cartographer's; the registrations are not.
				if err != nil || d.IsDir() || strings.Contains(filepath.ToSlash(path), "/cartographer-bootstrap/") || strings.Contains(filepath.ToSlash(path), "/cartographer-write-findings/") {
					return nil
				}
				_ = os.Chtimes(path, old, old)
				info, _ := os.Stat(path)
				shared[path] = info
				return nil
			})
			if len(shared) == 0 {
				t.Fatal("nothing was written")
			}
			if _, err := provisioning.EnsureBootstrapHook(base, p, lock, false); err != nil {
				t.Fatal(err)
			}
			for path, before := range shared {
				if !strings.HasSuffix(path, ".json") && !strings.HasSuffix(path, ".toml") {
					continue
				}
				after, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if !os.SameFile(before, after) || !after.ModTime().Equal(old) {
					t.Errorf("%s was rewritten by a sync that changed nothing", path)
				}
			}
		})
	}
}
