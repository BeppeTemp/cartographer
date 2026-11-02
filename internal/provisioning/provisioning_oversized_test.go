package provisioning

import (
	"os"
	"path/filepath"
	"testing"
)

// A file over the artifact cap is never read: ReadArtifactFiles fails (a client
// must not materialize half a skill), the Capped variant reports it, and a
// symlink beside it is not followed.
func TestReadDirFilesCapsAndSkipsSymlinks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills", "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("small"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "big.bin"), make([]byte, ArtifactMaxFileSize+1), 0o644))
	if err := os.Symlink(filepath.Join(dir, "SKILL.md"), filepath.Join(dir, "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	files, err := readDirFiles(dir, nil)
	must(err)
	got := map[string]ArtifactFile{}
	for _, f := range files {
		got[f.Path] = f
	}
	if len(got) != 2 || got["big.bin"].Oversized != ArtifactMaxFileSize+1 || got["big.bin"].Content != nil || string(got["SKILL.md"].Content) != "small" {
		t.Fatalf("files = %+v", got)
	}
	hash, err := contentHashDirTracked(dir, "skill", nil)
	must(err)
	if hash == "" {
		t.Fatal("no hash")
	}
}
