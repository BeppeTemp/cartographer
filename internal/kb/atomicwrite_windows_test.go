//go:build windows

package kb

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A reader that has the destination open without FILE_SHARE_DELETE makes the
// rename fail with "Access is denied" until it closes; the writer retries.
func TestWriteFileAtomic_SurvivesAConcurrentReader(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "f.md")
	if err := os.WriteFile(dest, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := os.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		r.Close()
	}()
	if err := writeFileAtomic(dest, []byte("new")); err != nil {
		t.Fatalf("writeFileAtomic with a reader holding the file: %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "new" {
		t.Fatalf("content = %q", b)
	}
}
