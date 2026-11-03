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

func TestOpen_SweepsStaleTempFilesOnly(t *testing.T) {
	t.Parallel()
	k := mustInitKB(t)
	stale := filepath.Join(k.Root, "data", ".tmp-wiki-old")
	fresh := filepath.Join(k.Root, "data", ".tmp-wiki-new")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * processLockTimeout)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(k.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale temp file kept (err=%v)", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a fresh temp file, possibly a write in progress, was removed: %v", err)
	}
}
