package provisioning

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// The single-file hash is a second line behind the manifest's own check: a
// descriptor that became a link between the listing and the hash is refused,
// not read.
func TestContentHashFile_RefusesALink(t *testing.T) {
	skipWithoutSymlinks(t)
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "x.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	var linkErr *contract.KBSymlinkError
	if _, err := contract.ContentHashFile(link); !errors.As(err, &linkErr) {
		t.Fatalf("contentHashFile(link) = %v, want a *contract.KBSymlinkError", err)
	}
	real := filepath.Join(dir, "y.json")
	if err := os.WriteFile(real, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := contract.ContentHashFile(real); err != nil {
		t.Fatalf("contentHashFile(regular file): %v", err)
	}
}
