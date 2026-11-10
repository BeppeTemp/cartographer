package provisioning

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// A file over the artifact cap is never read: ReadArtifactFiles fails (a client
// must not materialize half a skill), the Capped variant reports it, and a
// symlink beside it is not followed.
func TestReadDirFilesCapsAndRefusesSymlinks(t *testing.T) {
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
	must(os.WriteFile(filepath.Join(dir, "big.bin"), make([]byte, contract.ArtifactMaxFileSize+1), 0o644))
	if err := os.Symlink(filepath.Join(dir, "SKILL.md"), filepath.Join(dir, "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// A link among the files refuses the directory (contract/kblinks.go); with it gone
	// the rest reads as before.
	var linkErr *contract.KBSymlinkError
	if _, err := contract.ReadDirFiles(dir, nil); !errors.As(err, &linkErr) {
		t.Fatalf("readDirFiles with a link = %v, want a *contract.KBSymlinkError", err)
	}
	must(os.Remove(filepath.Join(dir, "link.md")))
	files, err := contract.ReadDirFiles(dir, nil)
	must(err)
	got := map[string]contract.ArtifactFile{}
	for _, f := range files {
		got[f.Path] = f
	}
	if len(got) != 2 || got["big.bin"].Oversized != contract.ArtifactMaxFileSize+1 || got["big.bin"].Content != nil || string(got["SKILL.md"].Content) != "small" {
		t.Fatalf("files = %+v", got)
	}
	hash, err := contract.ContentHashDirTracked(dir, "skill", nil)
	must(err)
	if hash == "" {
		t.Fatal("no hash")
	}
}

// The directory budget: past ArtifactMaxFiles the file that crosses it comes
// back oversized and the walk stops; the hash agrees (stable, no read).
func TestReadDirFilesBudget(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < contract.ArtifactMaxFiles+3; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := contract.ReadDirFiles(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != contract.ArtifactMaxFiles+1 || files[len(files)-1].Oversized == 0 {
		t.Fatalf("got %d files, last %+v", len(files), files[len(files)-1])
	}
	h1, err := contract.ContentHashDirTracked(dir, "skill", nil)
	if err != nil {
		t.Fatal(err)
	}
	if h2, _ := contract.ContentHashDirTracked(dir, "skill", nil); h1 != h2 {
		t.Fatal("hash not stable")
	}
}

func TestReadFileCappedRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "l")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	if _, _, err := contract.ReadFileCapped(link); !errors.Is(err, contract.ErrNotRegular) {
		t.Fatalf("err = %v", err)
	}
}

// An artifact that leaves the manifest (excluded as oversized) is pruned from
// a client that had it installed, like one deleted from the KB; the client
// recomputes the revision from what it receives, so nothing loops.
func TestApply_ArtifactDroppedFromManifestIsPruned(t *testing.T) {
	kbRoot := t.TempDir()
	skillDir := filepath.Join(kbRoot, "skills", "gone")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: gone\ndescription: gone\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "x"}} {
		cmd := exec.Command("git", append([]string{"-C", kbRoot}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git: %v %s", err, out)
		}
	}
	roots := map[string]string{"kb-a": kbRoot}
	m1, err := contract.BuildManifest(nil, roots, contract.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	res, err := Apply(m1, ApplyOptions{KBRoots: roots, Provider: configurator.ProviderClaudeCode, BaseDir: base})
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(base, ".claude", "skills", "gone")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("setup: %v", err)
	}
	var empty contract.Manifest
	empty.Revision = contract.ComputeRevision(nil)
	if _, err := Apply(empty, ApplyOptions{KBRoots: roots, Provider: configurator.ProviderClaudeCode, BaseDir: base, Lock: res.NewLock}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatalf("the installed copy survived: %v", err)
	}
}
