package safewrite

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// listed registers links as the allow set for the test and resets it after.
func listed(t *testing.T, links ...string) {
	t.Helper()
	skipOnWindows(t)
	SetAllowedLinks(links)
	t.Cleanup(ResetAllowedLinks)
}

func mustWrite(t *testing.T, p, s string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), perm); err != nil {
		t.Fatal(err)
	}
}

func mustLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func isLink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

func TestListedDirectoryLinkCoversWhatIsBelowIt(t *testing.T) {
	base, real := t.TempDir(), t.TempDir()
	link := filepath.Join(base, "cfg")
	mustLink(t, real, link)
	listed(t, link)

	f := filepath.Join(link, "sub", "f.txt")
	if err := MkdirAllNoFollow(base, filepath.Dir(f), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := WriteFile(base, f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(real, "sub", "f.txt")); string(b) != "x" {
		t.Errorf("the write did not land in the target: %q", b)
	}
	if err := Remove(base, f); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !isLink(link) {
		t.Error("the link was replaced")
	}
	// The listed directory link itself is never removed.
	if err := Remove(base, link); !errors.Is(err, ErrLinkedLeaf) || !isLink(link) {
		t.Errorf("Remove(link) = %v, link intact %v", err, isLink(link))
	}
}

func TestUnlistedAndNestedLinksAreRefused(t *testing.T) {
	base, real, other := t.TempDir(), t.TempDir(), t.TempDir()
	link := filepath.Join(base, "cfg")
	mustLink(t, real, link)
	mustLink(t, other, filepath.Join(real, "nested"))
	mustLink(t, other, filepath.Join(base, "sibling"))
	listed(t, link)

	for _, p := range []string{filepath.Join(link, "nested", "f"), filepath.Join(base, "sibling", "f")} {
		err := WriteFile(base, p, []byte("x"), 0o644)
		if !errors.Is(err, ErrSymlinkDestination) {
			t.Errorf("WriteFile(%s) = %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(other, "f")); err == nil {
		t.Error("a refused write landed")
	}

	// Listing the nested link follows it.
	listed(t, link, filepath.Join(link, "nested"))
	if err := WriteFile(base, filepath.Join(link, "nested", "f"), []byte("x"), 0o644); err != nil {
		t.Errorf("nested listed link: %v", err)
	}
}

func TestStrictCheckIgnoresTheAllowSet(t *testing.T) {
	base, real := t.TempDir(), t.TempDir()
	link := filepath.Join(base, "cfg")
	mustLink(t, real, link)
	listed(t, link)
	p := filepath.Join(link, "f")
	if err := CheckPath(base, p); err != nil {
		t.Errorf("CheckPath: %v", err)
	}
	if err := CheckPathStrict(base, p); !errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("CheckPathStrict = %v", err)
	}
}

func TestAtomicWriteThroughAListedLeafLinkKeepsTheLink(t *testing.T) {
	base, real := t.TempDir(), t.TempDir()
	target := filepath.Join(real, "t.json")
	mustWrite(t, target, "old", 0o600)
	link := filepath.Join(base, "c.json")
	mustLink(t, target, link)
	listed(t, link)

	changed, err := WriteFileAtomicNoFollowChanged(base, link, []byte("new"), 0o644)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	// The trap: a rename over the path would have replaced the link.
	if !isLink(link) {
		t.Fatal("the atomic write replaced the link with a regular file")
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Errorf("target = %q", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o600 {
			t.Errorf("target mode = %v, want the target's own 0600", fi.Mode().Perm())
		}
	}
	changed, err = WriteFileAtomicNoFollowChanged(base, link, []byte("new"), 0o644)
	if err != nil || changed {
		t.Errorf("identical bytes: changed=%v err=%v", changed, err)
	}
	if ents, _ := os.ReadDir(real); len(ents) != 1 {
		t.Errorf("temp file left in the target directory: %v", ents)
	}
}

func TestAtomicWriteFollowsRelativeAndChainedLinks(t *testing.T) {
	base := t.TempDir()
	mustWrite(t, filepath.Join(base, "store", "t"), "old", 0o644)
	mustLink(t, "t", filepath.Join(base, "store", "hop"))
	link := filepath.Join(base, "dot", "c")
	mustLink(t, filepath.Join("..", "store", "hop"), link)
	listed(t, link, filepath.Join(base, "store", "hop"))
	if err := WriteFileAtomicNoFollow(base, link, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "store", "t")); string(b) != "new" {
		t.Errorf("target = %q", b)
	}
	if !isLink(link) || !isLink(filepath.Join(base, "store", "hop")) {
		t.Error("a link of the chain was replaced")
	}
}

func TestAtomicWriteThroughADanglingListedLink(t *testing.T) {
	base, real := t.TempDir(), t.TempDir()
	link := filepath.Join(base, "c")
	mustLink(t, filepath.Join(real, "new.json"), link)
	listed(t, link)
	if err := WriteFileAtomicNoFollow(base, link, []byte("x"), 0o644); err != nil {
		t.Fatalf("existing parent: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(real, "new.json")); string(b) != "x" || !isLink(link) {
		t.Errorf("target = %q, link %v", b, isLink(link))
	}

	gone := filepath.Join(base, "d")
	mustLink(t, filepath.Join(real, "missing", "x.json"), gone)
	listed(t, link, gone)
	err := WriteFileAtomicNoFollow(base, gone, []byte("x"), 0o644)
	if !errors.Is(err, ErrSymlinkDestination) || !strings.Contains(err.Error(), gone) || !strings.Contains(err.Error(), "not writable") {
		t.Errorf("no parent: %v", err)
	}
}

func TestWriteIntoAReadOnlyTargetIsARefusalNamingLinkAndTarget(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a non-root POSIX user")
	}
	base, real := t.TempDir(), t.TempDir()
	target := filepath.Join(real, "t")
	mustWrite(t, target, "old", 0o644)
	link := filepath.Join(base, "c")
	mustLink(t, target, link)
	dirLink := filepath.Join(base, "d")
	mustLink(t, real, dirLink)
	listed(t, link, dirLink)
	if err := os.Chmod(real, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(real, 0o755) })

	for _, err := range []error{
		WriteFileAtomicNoFollow(base, link, []byte("new"), 0o644),
		WriteFile(base, filepath.Join(dirLink, "n"), []byte("new"), 0o644),
	} {
		if !errors.Is(err, ErrSymlinkDestination) || !strings.Contains(err.Error(), "not writable") {
			t.Errorf("err = %v", err)
		}
	}
	if err := WriteFileAtomicNoFollow(base, link, []byte("new"), 0o644); !strings.Contains(err.Error(), link) || !strings.Contains(err.Error(), target) {
		t.Errorf("message names neither link nor target: %v", err)
	}
	// A write through no followed link keeps its raw error.
	if err := WriteFile(real, filepath.Join(real, "n"), []byte("x"), 0o644); errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("raw error wrapped: %v", err)
	}
}

func TestRemoveOfAListedLeafLinkIsARefusalAndKeepsBoth(t *testing.T) {
	base, real := t.TempDir(), t.TempDir()
	target := filepath.Join(real, "t")
	mustWrite(t, target, "keep", 0o644)
	link := filepath.Join(base, "c")
	mustLink(t, target, link)
	listed(t, link)
	err := Remove(base, link)
	if !errors.Is(err, ErrSymlinkDestination) || !errors.Is(err, ErrLinkedLeaf) {
		t.Fatalf("err = %v", err)
	}
	if !isLink(link) {
		t.Error("link removed")
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Error("target changed")
	}
}

func TestAListedLinkToANonPlainTargetIsStillRefused(t *testing.T) {
	skipOnWindows(t)
	base := t.TempDir()
	link := filepath.Join(base, "c")
	mustLink(t, "/dev/null", link)
	listed(t, link)
	if err := WriteFileAtomicNoFollow(base, link, []byte("x"), 0o644); !errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("err = %v", err)
	}
	if !IsUnsafe(link) {
		t.Error("a link to a device is not unsafe")
	}
}

func TestFollowedLinksListsTheListedOnesInWalkOrder(t *testing.T) {
	base, real := t.TempDir(), t.TempDir()
	a := filepath.Join(base, "a")
	mustLink(t, real, a)
	mustLink(t, real, filepath.Join(real, "b"))
	listed(t, a, filepath.Join(a, "b"), filepath.Join(base, "unrelated"))
	got := FollowedLinks(base, filepath.Join(a, "b", "f"))
	if len(got) != 2 || got[0].Path != a || got[0].Target != real {
		t.Errorf("links = %+v", got)
	}
}

// The kernel resolves ".." against the physical directory behind a listed
// directory link, so the write must land where the kernel would send it.
func TestRelativeTargetUnderAListedDirectoryLinkResolvesPhysically(t *testing.T) {
	base, root := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(root, "real", "keep"), "k", 0o644)
	mustWrite(t, filepath.Join(root, "shared", "f"), "old", 0o644)
	// base/shared/f must NOT be the one written: a lexical join would pick it.
	mustWrite(t, filepath.Join(base, "shared", "f"), "decoy", 0o644)
	dir := filepath.Join(base, "dir")
	mustLink(t, filepath.Join(root, "real"), dir)
	link := filepath.Join(dir, "f")
	mustLink(t, filepath.Join("..", "shared", "f"), link)
	listed(t, dir, link)
	if err := WriteFileAtomicNoFollow(base, link, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "shared", "f")); string(b) != "new" {
		t.Errorf("physical target = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "shared", "f")); string(b) != "decoy" {
		t.Errorf("the write landed on the lexical path: %q", b)
	}
	if !isLink(link) {
		t.Error("the link was replaced")
	}
}

func TestAnUnlistedHopOfTheChainStopsTheWrite(t *testing.T) {
	base, real := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(real, "t"), "keep", 0o644)
	hop := filepath.Join(real, "hop")
	mustLink(t, filepath.Join(real, "t"), hop)
	link := filepath.Join(base, "c")
	mustLink(t, hop, link)
	listed(t, link)
	err := WriteFileAtomicNoFollow(base, link, []byte("new"), 0o644)
	if !errors.Is(err, ErrSymlinkDestination) || !strings.Contains(err.Error(), hop) {
		t.Fatalf("err = %v, want a refusal naming the unlisted hop", err)
	}
	if b, _ := os.ReadFile(filepath.Join(real, "t")); string(b) != "keep" {
		t.Errorf("target changed: %q", b)
	}
	listed(t, link, hop)
	if err := WriteFileAtomicNoFollow(base, link, []byte("new"), 0o644); err != nil {
		t.Errorf("all hops listed: %v", err)
	}
}

func TestRemoveEmptyDirNeverUnlinksALink(t *testing.T) {
	base, real := t.TempDir(), t.TempDir()
	link := filepath.Join(base, "d")
	mustLink(t, real, link)
	listed(t, link)
	if err := RemoveEmptyDir(link); err != nil || !isLink(link) {
		t.Fatalf("err=%v link intact=%v", err, isLink(link))
	}
	if _, err := os.Stat(real); err != nil {
		t.Error("the target was removed")
	}
	plain := filepath.Join(base, "p")
	if err := os.Mkdir(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveEmptyDir(plain); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plain); err == nil {
		t.Error("an empty plain directory should be removed")
	}
}

// A hop is matched in its physical spelling: listing its lexical twin (the same
// logical path under a listed directory link, but another file) is not listing it.
func TestAHopWhoseLexicalTwinIsListedIsStillRefused(t *testing.T) {
	base, root := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(root, "real", "keep"), "k", 0o644)
	mustWrite(t, filepath.Join(root, "t"), "keep", 0o644)
	mustLink(t, filepath.Join(root, "t"), filepath.Join(root, "hop")) // physical hop, unlisted
	mustLink(t, filepath.Join(root, "t"), filepath.Join(base, "hop")) // its lexical twin, listed
	dir := filepath.Join(base, "dir")
	mustLink(t, filepath.Join(root, "real"), dir)
	link := filepath.Join(dir, "c")
	mustLink(t, filepath.Join("..", "hop"), link) // kernel: root/hop; lexical: base/hop
	listed(t, dir, link, filepath.Join(base, "hop"))
	err := WriteFileAtomicNoFollow(base, link, []byte("new"), 0o644)
	if !errors.Is(err, ErrSymlinkDestination) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "t")); string(b) != "keep" {
		t.Errorf("target changed: %q", b)
	}
}

// The user lists the logical path they see; a hop reached through a listed
// directory link is the same file as that path.
func TestAHopListedByItsLogicalPathIsFollowed(t *testing.T) {
	base, root := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(root, "real", "t"), "old", 0o644)
	mustLink(t, "t", filepath.Join(root, "real", "hop"))
	dir := filepath.Join(base, "dir")
	mustLink(t, filepath.Join(root, "real"), dir)
	link := filepath.Join(base, "c")
	mustLink(t, filepath.Join("dir", "hop"), link)
	listed(t, dir, link, filepath.Join(dir, "hop"))
	if err := WriteFileAtomicNoFollow(base, link, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "real", "t")); string(b) != "new" {
		t.Errorf("target = %q", b)
	}
}
