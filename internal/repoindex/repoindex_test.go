package repoindex

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestNormalizeRemote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want RemoteKey
	}{
		{"scp-like", "git@github.com:owner/name.git", "github.com/owner/name"},
		{"scp-like no user", "github.com:owner/name.git", "github.com/owner/name"},
		{"ssh scheme", "ssh://git@github.com/owner/name.git", "github.com/owner/name"},
		{"ssh scheme with port", "ssh://git@github.com:2222/owner/name.git", "github.com/owner/name"},
		{"https", "https://github.com/owner/name.git", "github.com/owner/name"},
		{"https no .git suffix", "https://github.com/owner/name", "github.com/owner/name"},
		{"http", "http://github.com/owner/name.git", "github.com/owner/name"},
		{"git protocol", "git://github.com/owner/name.git", "github.com/owner/name"},
		{"uppercase host", "https://GitHub.com/owner/name.git", "github.com/owner/name"},
		{"nested group", "https://gitlab.com/group/subgroup/name.git", "gitlab.com/group/subgroup/name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeRemote(c.in)
			if err != nil {
				t.Fatalf("NormalizeRemote(%q) error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("NormalizeRemote(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeRemoteErrors(t *testing.T) {
	for _, in := range []string{"", "not-a-remote-at-all", "https:///owner/name"} {
		if _, err := NormalizeRemote(in); err == nil {
			t.Errorf("NormalizeRemote(%q): expected error, got nil", in)
		}
	}
}

// writeGitRepo creates dir/.git/config with an origin remote of remoteURL
// (or no origin section at all if remoteURL is "").
func writeGitRepo(t *testing.T, dir, remoteURL string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "[core]\n\trepositoryformatversion = 0\n"
	if remoteURL != "" {
		content += "[remote \"origin\"]\n\turl = " + remoteURL + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanFindsRepos(t *testing.T) {
	root := t.TempDir()
	repoA := filepath.Join(root, "projects", "repo-a")
	repoB := filepath.Join(root, "projects", "nested", "repo-b")
	noRemote := filepath.Join(root, "projects", "no-remote")
	os.MkdirAll(repoA, 0o755)
	os.MkdirAll(repoB, 0o755)
	os.MkdirAll(noRemote, 0o755)
	writeGitRepo(t, repoA, "git@github.com:acme/repo-a.git")
	writeGitRepo(t, repoB, "https://github.com/acme/repo-b.git")
	writeGitRepo(t, noRemote, "")

	// Directory that should be skipped entirely.
	skipped := filepath.Join(root, "projects", "node_modules", "some-pkg")
	os.MkdirAll(skipped, 0o755)
	writeGitRepo(t, skipped, "git@github.com:acme/should-not-be-found.git")

	idx, err := Scan([]string{root}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := idx.Repos["github.com/acme/repo-a"]; len(got) != 1 || got[0] != repoA {
		t.Errorf("repo-a: got %v", got)
	}
	if got := idx.Repos["github.com/acme/repo-b"]; len(got) != 1 || got[0] != repoB {
		t.Errorf("repo-b: got %v", got)
	}
	if _, ok := idx.Repos["github.com/acme/should-not-be-found"]; ok {
		t.Errorf("node_modules should have been skipped")
	}
}

func TestScanDepthCap(t *testing.T) {
	root := t.TempDir()
	// depthCap is 4: build a repo 6 levels deep, which must not be found.
	deep := root
	for i := 0; i < 6; i++ {
		deep = filepath.Join(deep, "d")
	}
	os.MkdirAll(deep, 0o755)
	writeGitRepo(t, deep, "git@github.com:acme/too-deep.git")

	idx, err := Scan([]string{root}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Repos["github.com/acme/too-deep"]; ok {
		t.Errorf("repo beyond depthCap should not have been found")
	}
}

func TestLookupIndexAmbiguousShortName(t *testing.T) {
	idx := &Index{Repos: map[RemoteKey][]string{
		"github.com/acme/tools":  {"/a"},
		"gitlab.com/other/tools": {"/b"},
	}}
	_, _, err := lookupIndex(idx, "tools")
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
}

func TestLookupIndexMultipleClonesWarns(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	os.MkdirAll(first, 0o755)
	os.MkdirAll(second, 0o755)
	writeGitRepo(t, first, "git@github.com:acme/tools.git")
	writeGitRepo(t, second, "git@github.com:acme/tools.git")

	idx := &Index{Repos: map[RemoteKey][]string{
		"github.com/acme/tools": {first, second},
	}}
	path, warnings, err := lookupIndex(idx, "tools")
	if err != nil {
		t.Fatal(err)
	}
	if path != first {
		t.Errorf("path = %q, want %q", path, first)
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning, got %v", warnings)
	}
}

func TestLookupIndexFullKey(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	os.MkdirAll(a, 0o755)
	os.MkdirAll(b, 0o755)
	writeGitRepo(t, a, "git@github.com:acme/tools.git")
	writeGitRepo(t, b, "git@gitlab.com:other/tools.git")

	idx := &Index{Repos: map[RemoteKey][]string{
		"github.com/acme/tools":  {a},
		"gitlab.com/other/tools": {b},
	}}
	path, _, err := lookupIndex(idx, "gitlab.com/other/tools")
	if err != nil {
		t.Fatal(err)
	}
	if path != b {
		t.Errorf("path = %q, want %q", path, b)
	}
}

func TestLookupIndexNotFound(t *testing.T) {
	idx := &Index{Repos: map[RemoteKey][]string{}}
	_, _, err := lookupIndex(idx, "missing")
	if err != errNotIndexed {
		t.Errorf("err = %v, want errNotIndexed", err)
	}
}

func TestResolveManualPathsOverride(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = os.UserHomeDir }()

	path, warnings, err := Resolve("mine", map[string]string{"mine": dir}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if path != dir {
		t.Errorf("path = %q, want %q", path, dir)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestResolveRescanOnMiss(t *testing.T) {
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = os.UserHomeDir }()

	root := t.TempDir()
	repo := filepath.Join(root, "myrepo")
	os.MkdirAll(repo, 0o755)
	writeGitRepo(t, repo, "git@github.com:acme/myrepo.git")

	// No cache exists yet: Resolve must fall through to Scan and find it.
	path, _, err := Resolve("myrepo", nil, []string{root}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if path != repo {
		t.Errorf("path = %q, want %q", path, repo)
	}

	// The cache should now be populated: resolving again with the same roots
	// (D181: matching roots is what makes the cache eligible at all) must hit
	// the cache rather than rescan, and still return repo.
	path2, _, err := Resolve("myrepo", nil, []string{root}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if path2 != repo {
		t.Errorf("path2 = %q, want %q", path2, repo)
	}
}

func TestResolveNotFound(t *testing.T) {
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = os.UserHomeDir }()

	root := t.TempDir()
	_, _, err := Resolve("nope", nil, []string{root}, 0)
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

// TestResolveStaleHitRescans covers D181: a cache entry whose path no longer
// holds a clone must behave as a miss, so a moved repo resolves to its new
// location and the cache is rewritten to match.
func TestResolveStaleHitRescans(t *testing.T) {
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = os.UserHomeDir }()

	root := t.TempDir()
	oldPath := filepath.Join(root, "myrepo")
	os.MkdirAll(oldPath, 0o755)
	writeGitRepo(t, oldPath, "git@github.com:acme/myrepo.git")

	// Populate the cache at oldPath.
	if _, _, err := Resolve("myrepo", nil, []string{root}, 0); err != nil {
		t.Fatal(err)
	}

	// Move the clone within the same roots: the cache still points at oldPath.
	newPath := filepath.Join(root, "moved", "myrepo")
	os.MkdirAll(filepath.Join(root, "moved"), 0o755)
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}

	path, _, err := Resolve("myrepo", nil, []string{root}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if path != newPath {
		t.Errorf("path = %q, want %q (new location)", path, newPath)
	}

	got, err := LoadCache()
	if err != nil {
		t.Fatal(err)
	}
	if paths := got.Repos["github.com/acme/myrepo"]; len(paths) != 1 || paths[0] != newPath {
		t.Errorf("cache not rewritten to new location: %v", paths)
	}
}

// TestLookupIndexFirstCloneGoneSecondAliveNoWarning covers D181: when the
// first clone in root order is gone, the surviving one resolves cleanly with
// no multiple-clones warning — there is, in fact, only one clone left.
func TestLookupIndexFirstCloneGoneSecondAliveNoWarning(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(root, "gone")
	alive := filepath.Join(root, "alive")
	os.MkdirAll(alive, 0o755)
	writeGitRepo(t, alive, "git@github.com:acme/tools.git")
	// gone is never created: it never existed at this path, or was removed.

	idx := &Index{Repos: map[RemoteKey][]string{
		"github.com/acme/tools": {gone, alive},
	}}
	path, warnings, err := lookupIndex(idx, "tools")
	if err != nil {
		t.Fatal(err)
	}
	if path != alive {
		t.Errorf("path = %q, want %q", path, alive)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no multiple-clones warning, got %v", warnings)
	}
}

// TestLookupIndexAmbiguousOnlyOnPaper covers D181: two remotes share a short
// name in the cache, but only one still has a live clone — resolution
// succeeds instead of raising the ambiguity error.
func TestLookupIndexAmbiguousOnlyOnPaper(t *testing.T) {
	root := t.TempDir()
	alive := filepath.Join(root, "alive")
	os.MkdirAll(alive, 0o755)
	writeGitRepo(t, alive, "git@github.com:acme/tools.git")
	missing := filepath.Join(root, "missing")
	// missing is never created.

	idx := &Index{Repos: map[RemoteKey][]string{
		"github.com/acme/tools":  {alive},
		"gitlab.com/other/tools": {missing},
	}}
	path, _, err := lookupIndex(idx, "tools")
	if err != nil {
		t.Fatalf("expected successful resolution, got error: %v", err)
	}
	if path != alive {
		t.Errorf("path = %q, want %q", path, alive)
	}
}

// TestResolveEveryCloneGoneNotFound covers D181: a cache entry whose every
// path is dead falls through to a rescan same as an ordinary miss, and — when
// the rescan finds nothing either — the not-found error keeps its
// search_depth guidance.
func TestResolveEveryCloneGoneNotFound(t *testing.T) {
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = os.UserHomeDir }()

	root := t.TempDir()
	idx := &Index{Roots: []string{root}, Repos: map[RemoteKey][]string{
		"github.com/acme/myrepo": {filepath.Join(root, "myrepo")}, // never created
	}}
	if err := SaveCache(idx); err != nil {
		t.Fatal(err)
	}

	_, _, err := Resolve("myrepo", nil, []string{root}, 0)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "search_depth") {
		t.Errorf("error missing search_depth guidance: %v", err)
	}
}

// TestLookupIndexLivePathNotARepo covers D181: a cached path that still
// exists as a directory but no longer contains a .git entry is treated as
// dead, the same outcome as a path that vanished entirely.
func TestLookupIndexLivePathNotARepo(t *testing.T) {
	root := t.TempDir()
	emptied := filepath.Join(root, "emptied")
	os.MkdirAll(emptied, 0o755) // directory present, but no .git subdirectory

	idx := &Index{Repos: map[RemoteKey][]string{
		"github.com/acme/myrepo": {emptied},
	}}
	_, _, err := lookupIndex(idx, "myrepo")
	if err != errNotIndexed {
		t.Errorf("err = %v, want errNotIndexed", err)
	}
}

// TestResolveRootsChangedInvalidatesCache covers D181: a change of
// search_roots invalidates the cache even for a key present under the old
// roots at a path that is still perfectly live — root order decides the
// winner among clones, so a reorder or replacement of roots is a semantic
// change, not a cosmetic one.
func TestResolveRootsChangedInvalidatesCache(t *testing.T) {
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = os.UserHomeDir }()

	rootA := t.TempDir()
	pathA := filepath.Join(rootA, "myrepo")
	os.MkdirAll(pathA, 0o755)
	writeGitRepo(t, pathA, "git@github.com:acme/myrepo.git")

	// Populate the cache scanning rootA: Roots == [rootA], key -> pathA.
	if _, _, err := Resolve("myrepo", nil, []string{rootA}, 0); err != nil {
		t.Fatal(err)
	}

	// A second, distinct root also carries a clone of the same remote, at a
	// different path. pathA is untouched and still perfectly live.
	rootB := t.TempDir()
	pathB := filepath.Join(rootB, "myrepo")
	os.MkdirAll(pathB, 0o755)
	writeGitRepo(t, pathB, "git@github.com:acme/myrepo.git")

	// search_roots now points only at rootB: the cached entry (Roots=[rootA],
	// path=pathA) must not be trusted even though pathA is still live.
	path, _, err := Resolve("myrepo", nil, []string{rootB}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if path != pathB {
		t.Errorf("path = %q, want %q (roots change must force a rescan)", path, pathB)
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = os.UserHomeDir }()

	if got := expandHome("~"); got != home {
		t.Errorf("expandHome(~) = %q, want %q", got, home)
	}
	want := filepath.Join(home, "Documents")
	if got := expandHome("~/Documents"); got != want {
		t.Errorf("expandHome(~/Documents) = %q, want %q", got, want)
	}
	if got := expandHome("/etc/foo"); got != "/etc/foo" {
		t.Errorf("expandHome(/etc/foo) = %q, want unchanged", got)
	}
}

func TestShortName(t *testing.T) {
	if got := RemoteKey("github.com/owner/name").ShortName(); got != "name" {
		t.Errorf("ShortName() = %q, want name", got)
	}
}

func TestCacheRoundTrip(t *testing.T) {
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = os.UserHomeDir }()

	idx := &Index{Roots: []string{"/a", "/b"}, Repos: map[RemoteKey][]string{
		"github.com/acme/repo": {"/home/x/repo"},
	}}
	if err := SaveCache(idx); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCache()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got.Roots)
	if len(got.Repos["github.com/acme/repo"]) != 1 {
		t.Errorf("round-tripped cache missing repo: %+v", got)
	}
}

func TestLoadCacheNotExist(t *testing.T) {
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	defer func() { userHomeDir = os.UserHomeDir }()

	if _, err := LoadCache(); !os.IsNotExist(err) {
		t.Errorf("LoadCache() err = %v, want os.ErrNotExist", err)
	}
}
