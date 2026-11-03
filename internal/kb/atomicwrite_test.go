package kb

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// writeFileAtomic must flush the data before the rename and the directory
// after it; a crash can otherwise leave an empty file that the next write
// commits. The order is asserted through the seams, since a crash cannot be
// reproduced on CI.
func TestWriteFileAtomic_SyncsDataBeforeRenameAndDirectoryAfter(t *testing.T) {
	var seq []string
	oldSync, oldRename, oldDir := syncTempFile, renameTemp, syncParent
	t.Cleanup(func() { syncTempFile, renameTemp, syncParent = oldSync, oldRename, oldDir })
	syncTempFile = func(f *os.File) error { seq = append(seq, "sync-file"); return f.Sync() }
	renameTemp = func(a, b string) error { seq = append(seq, "rename"); return renameReplacing(a, b) }
	syncParent = func(d string) error { seq = append(seq, "sync-dir"); return syncDir(d) }

	dest := filepath.Join(t.TempDir(), "f.md")
	if err := writeFileAtomic(dest, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if want := []string{"sync-file", "rename", "sync-dir"}; !reflect.DeepEqual(seq, want) {
		t.Fatalf("sequence = %v, want %v", seq, want)
	}
}

// A temp file orphaned by a kill is junk: never committed, whatever KB it is in.
func TestTempFileIsJunkAndNeverShowsInGitStatus(t *testing.T) {
	t.Parallel()
	if !IsJunkPath("data/notes/.tmp-wiki-123456") || !IsJunkPath(".tmp-wiki-1") {
		t.Fatal("a .tmp-wiki-* file is not junk")
	}
	k := mustInitKB(t)
	for _, rel := range []string{"data/.tmp-wiki-111", ".tmp-wiki-222", "skills/.tmp-wiki-333"} {
		abs := filepath.Join(k.Root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Open re-applies the exclude list, as it does for a KB created before it.
	if _, err := Open(k.Root); err != nil {
		t.Fatal(err)
	}
	out, err := gitStatusPorcelain(k.Root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, ".tmp-wiki-") {
		t.Fatalf("orphaned temp files show in git status:\n%s", out)
	}
}

func TestSweepStaleTempFiles_RemovesOnlyOurStaleOrphans(t *testing.T) {
	t.Parallel()
	k := mustInitKB(t)
	old := time.Now().Add(-2 * processLockTimeout)
	mk := func(rel string, age bool) string {
		p := filepath.Join(k.Root, rel)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if age {
			if err := os.Chtimes(p, old, old); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	stale := mk("data/.tmp-wiki-123456", true)
	fresh := mk("data/.tmp-wiki-654321", false)
	notOurs := mk("data/.tmp-wiki-notes.md", true) // prefix, but not CreateTemp's name
	tracked := mk("data/.tmp-wiki-777", true)
	gitIn(t, k.Root, "add", "-f", "data/.tmp-wiki-777")
	gitIn(t, k.Root, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "tracked")

	k.SweepStaleTempFiles()

	for p, wantGone := range map[string]bool{stale: true, fresh: false, notOurs: false, tracked: false} {
		_, err := os.Stat(p)
		if gone := os.IsNotExist(err); gone != wantGone {
			t.Errorf("%s: removed=%v, want %v", filepath.Base(p), gone, wantGone)
		}
	}
}

func TestOpen_DoesNotSweepOrCreateTheLockFile(t *testing.T) {
	t.Parallel()
	k := mustInitKB(t)
	stale := filepath.Join(k.Root, "data", ".tmp-wiki-1")
	if err := os.WriteFile(stale, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * processLockTimeout)
	_ = os.Chtimes(stale, old, old)
	_ = os.Remove(filepath.Join(k.Root, LockFileName))
	if _, err := Open(k.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("Open swept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.Root, LockFileName)); err == nil {
		t.Error("Open created the lock file")
	}
}
