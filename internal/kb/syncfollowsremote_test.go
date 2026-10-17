package kb

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// D903: whether a KB syncs is not a setting, it follows the remote. A KB without
// one never contacts anything; with one it fetches before a write and pushes
// after it.
func TestSyncFollowsTheRemote(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	t.Run("no remote", func(t *testing.T) {
		k, _ := initGitKB(t)
		if k.SyncInDue() {
			t.Error("SyncInDue on a KB with no remote")
		}
		if fetched, err := k.SyncIn(); fetched || err != nil {
			t.Errorf("SyncIn = (%v, %v), want no fetch and no error", fetched, err)
		}
		if err := k.SyncOut(); err != nil {
			t.Errorf("SyncOut = %v", err)
		}
		if s := k.GitStatusSnapshot(); s.State != "no_remote" || s.LastSyncInAt != nil {
			t.Errorf("status = %+v, want no_remote and no sync ever", s)
		}
		// Not a repository: nothing to sync either, whatever else is set.
		plain := &KB{Root: t.TempDir()}
		if plain.SyncInDue() {
			t.Error("SyncInDue on a folder that is not a repository")
		}
	})

	t.Run("with a remote", func(t *testing.T) {
		k, bare := setupKBWithRemote(t)
		if !k.SyncInDue() {
			t.Fatal("SyncInDue is false on a KB with a remote")
		}
		if err := k.SyncOut(); err != nil {
			t.Fatal(err)
		}
		branch, _ := gitx.Branch(k.Root)
		if n := gitHere(t, bare, "rev-list", "--count", branch); n == "" || n == "0" {
			t.Errorf("SyncOut pushed nothing: %q", n)
		}
		if fetched, err := k.SyncIn(); !fetched || err != nil {
			t.Errorf("SyncIn = (%v, %v), want a fetch", fetched, err)
		}
		if s := k.GitStatusSnapshot(); s.LastSyncInAt == nil || s.State != "clean" {
			t.Errorf("status = %+v", s)
		}
	})
}
