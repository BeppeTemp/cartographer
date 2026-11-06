package provisioning

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// A link listed in the allow set is the user's wiring: it is followed for
// writes and never removed, so a cleanup that would delete the file rewrites it
// through the link instead.

func listLinks(t *testing.T, links ...string) {
	t.Helper()
	skipWithoutSymlinks(t)
	safewrite.SetAllowedLinks(links)
	t.Cleanup(safewrite.ResetAllowedLinks)
}

func TestRemoveInstructionsBlock_RewritesAListedLeafLinkInsteadOfRemovingIt(t *testing.T) {
	base, real := t.TempDir(), t.TempDir()
	content := instructionsBlockBegin + "\nours\n" + instructionsBlockEnd + "\n"
	target := filepath.Join(real, "CLAUDE.md")
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "CLAUDE.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	listLinks(t, link)

	if err := removeInstructionsBlock(base, link); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was removed or replaced: %v", err)
	}
	if b, err := os.ReadFile(target); err != nil || strings.Contains(string(b), "ours") {
		t.Errorf("target = %q, %v: the block should be gone, the file kept", b, err)
	}
}

func TestRemoveAntigravityHook_RewritesAListedHooksLinkThroughIt(t *testing.T) {
	base, real := t.TempDir(), t.TempDir()
	target := filepath.Join(real, "hooks.json")
	if err := os.WriteFile(target, []byte(`{"`+antigravityHookKey("h")+`": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	hookRel := ".agents/hooks/h/hook.json"
	link := antigravityHooksPath(base, hookRel)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	listLinks(t, link)

	if err := removeAntigravityHook(base, "h", hookRel); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was removed or replaced: %v", err)
	}
	if b, err := os.ReadFile(target); err != nil || strings.Contains(string(b), antigravityHookKey("h")) {
		t.Errorf("target = %q, %v", b, err)
	}
}

// The trap: pruneEmptyDirs must never unlink a listed directory link once
// everything below it was pruned (os.Remove on the link would succeed).
func TestPruneEmptyDirsNeverUnlinksAListedDirectoryLink(t *testing.T) {
	base, real := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "custom")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	listLinks(t, link)

	pruneEmptyDirs(base, filepath.Join("custom", "x", "gone.md"))
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the listed directory link was unlinked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(real, "x")); !os.IsNotExist(err) {
		t.Errorf("the empty directory below the link should be pruned: %v", err)
	}
	if _, err := os.Stat(real); err != nil {
		t.Errorf("the target directory is gone: %v", err)
	}
}

// removeEmptyDestDir undoes the directory a refused delivery created; when that
// directory is a listed link it must stay.
func TestRemoveEmptyDestDirNeverUnlinksAListedDirectoryLink(t *testing.T) {
	base, real := t.TempDir(), t.TempDir()
	link := filepath.Join(base, "dest")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	listLinks(t, link)
	removeEmptyDestDir(base, link)
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the listed directory link was unlinked: %v", err)
	}
}
