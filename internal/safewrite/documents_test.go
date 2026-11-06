package safewrite

import (
	"path/filepath"
	"testing"
)

// For any home but the real one, the Documents folder is home-relative on
// every platform: it keeps every test home-relative.
func TestDocumentsDirIsHomeRelativeForAnyOtherHome(t *testing.T) {
	home := t.TempDir()
	if got, want := DocumentsDir(home), filepath.Join(home, "Documents"); got != want {
		t.Fatalf("DocumentsDir = %s, want %s", got, want)
	}
}
