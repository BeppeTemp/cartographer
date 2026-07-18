package repoindex

import (
	"os"
	"path/filepath"
	"sort"
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

	idx, err := Scan([]string{root})
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

	idx, err := Scan([]string{root})
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
	idx := &Index{Repos: map[RemoteKey][]string{
		"github.com/acme/tools": {"/first", "/second"},
	}}
	path, warnings, err := lookupIndex(idx, "tools")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/first" {
		t.Errorf("path = %q, want /first", path)
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning, got %v", warnings)
	}
}

func TestLookupIndexFullKey(t *testing.T) {
	idx := &Index{Repos: map[RemoteKey][]string{
		"github.com/acme/tools":  {"/a"},
		"gitlab.com/other/tools": {"/b"},
	}}
	path, _, err := lookupIndex(idx, "gitlab.com/other/tools")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/b" {
		t.Errorf("path = %q, want /b", path)
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

	path, warnings, err := Resolve("mine", map[string]string{"mine": dir}, nil)
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
	path, _, err := Resolve("myrepo", nil, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if path != repo {
		t.Errorf("path = %q, want %q", path, repo)
	}

	// The cache should now be populated, resolving from cache without roots.
	path2, _, err := Resolve("myrepo", nil, nil)
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
	_, _, err := Resolve("nope", nil, []string{root})
	if err == nil {
		t.Fatal("expected not-found error")
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
