package recipe

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type fixture struct {
	t     *testing.T
	dir   string
	clone CloneFunc
	calls int
}

func newFixture(t *testing.T) *fixture {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	f := &fixture{t: t, dir: t.TempDir()}
	git(t, f.dir, "init", "-q")
	f.clone = func(ctx context.Context, url, dest string) error {
		f.calls++
		return gitx.Clone(ctx, f.dir, dest) // the fixture stands in for the https remote
	}
	return f
}

func (f *fixture) write(p, s string) {
	f.t.Helper()
	full := filepath.Join(f.dir, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) commit(msg string) string {
	git(f.t, f.dir, "add", "-A")
	git(f.t, f.dir, "commit", "-qm", msg)
	return git(f.t, f.dir, "rev-parse", "HEAD")
}

func (f *fixture) recipe(hosts ...string) Recipe {
	if len(hosts) == 0 {
		hosts = []string{"git.example.com"}
	}
	r, err := NewRegistry(Options{GitHosts: hosts, Clone: f.clone})
	if err != nil {
		f.t.Fatal(err)
	}
	g, _ := r.Get("git-docs")
	return g
}

const repoURL = "https://git.example.com/org/docs"

func keys(l Listing) map[string]string {
	m := map[string]string{}
	for _, e := range l.Entries {
		m[e.Key] = e.Change
	}
	return m
}

func TestGitDocsListsFromTheCursor(t *testing.T) {
	f := newFixture(t)
	f.write("guide/a.md", "a")
	f.write("guide/sub/b c.md", "b")
	f.write("guide/è.md", "e")
	f.write("guide/skip.txt", "x")
	f.write("README.md", "r")
	first := f.commit("1")
	rc := f.recipe()
	req := Request{Locator: repoURL, Include: []string{"guide/**/*.md"}}

	l, err := rc.List(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := keys(l)
	if len(got) != 3 || got["guide/a.md"] != Added || got["guide/sub/b c.md"] != Added || got["guide/è.md"] != Added {
		t.Fatalf("first listing = %v", got)
	}
	if l.NextCursor != first || l.Entries[0].Locator != repoURL+"#guide/a.md" || l.Entries[0].Revision == "" {
		t.Fatalf("cursor/locator/revision: %+v", l)
	}

	req.Cursor = l.NextCursor
	if l, _ = rc.List(context.Background(), req); len(l.Entries) != 0 || l.NextCursor != first {
		t.Fatalf("no change: %+v", l)
	}

	f.write("guide/a.md", "a2")
	if err := os.Remove(filepath.Join(f.dir, "guide/è.md")); err != nil {
		t.Fatal(err)
	}
	f.write("guide/new.md", "n")
	f.write("other/z.md", "z") // outside the glob
	second := f.commit("2")
	l, err = rc.List(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got = keys(l)
	if len(got) != 3 || got["guide/a.md"] != Modified || got["guide/è.md"] != Removed || got["guide/new.md"] != Added || l.NextCursor != second {
		t.Fatalf("second listing = %v next %s", got, l.NextCursor)
	}

	if !MatchGlob(defaultInclude, "docs"+"/x/a.md") {
		t.Error("the default include misses the docs tree")
	}
	req.Include = []string{"other/*.md", "guide/**/b c.md"}
	req.Cursor = ""
	l, _ = rc.List(context.Background(), req)
	if got = keys(l); len(got) != 2 || got["other/z.md"] != Added || got["guide/sub/b c.md"] != Added {
		t.Fatalf("include = %v", got)
	}
}

func TestGitDocsSplitsAListingWithAnInProgressCursor(t *testing.T) {
	f := newFixture(t)
	f.write("guide/a.md", "1")
	f.write("guide/b.md", "1")
	f.write("guide/c.md", "1")
	head := f.commit("1")
	rc := f.recipe()
	req := Request{Locator: repoURL, MaxEntries: 2, Include: []string{"guide/*.md"}}
	l, err := rc.List(context.Background(), req)
	if err != nil || len(l.Entries) != 2 || l.NextCursor != ".."+head+"@guide/b.md" {
		t.Fatalf("part 1 = %+v, %v", l, err)
	}
	req.Cursor = l.NextCursor
	l, err = rc.List(context.Background(), req)
	if err != nil || len(l.Entries) != 1 || l.Entries[0].Key != "guide/c.md" || l.NextCursor != head {
		t.Fatalf("part 2 = %+v, %v", l, err)
	}

	// A listing in progress keeps its end commit even when the repository moved.
	f.write("guide/d.md", "1")
	f.commit("2")
	req.Cursor = ".." + head + "@guide/b.md"
	if l, _ = rc.List(context.Background(), req); len(l.Entries) != 1 || l.NextCursor != head {
		t.Fatalf("in progress moved: %+v", l)
	}
}

func TestGitDocsRewrittenHistoryIsCursorInvalid(t *testing.T) {
	f := newFixture(t)
	f.write("guide/a.md", "1")
	old := f.commit("1")
	branch := git(t, f.dir, "rev-parse", "--abbrev-ref", "HEAD")
	git(t, f.dir, "checkout", "-q", "--orphan", "fresh")
	f.write("guide/a.md", "2")
	f.commit("rewritten")
	git(t, f.dir, "branch", "-D", branch)
	git(t, f.dir, "reflog", "expire", "--expire=now", "--all")
	git(t, f.dir, "gc", "-q", "--prune=now")
	for _, cur := range []string{old, ".." + old + "@guide/a.md", "garbage", strings.Repeat("a", 40)} {
		_, err := f.recipe().List(context.Background(), Request{Locator: repoURL, Cursor: cur})
		var e *Error
		if !errors.As(err, &e) || e.Code != CodeCursorInvalid || !strings.Contains(e.Detail, "clear feed_cursor") {
			t.Errorf("cursor %q: %v", cur, err)
		}
	}
}

func TestGitDocsRefusesBeforeAnyClone(t *testing.T) {
	f := newFixture(t)
	f.write("guide/a.md", "1")
	f.commit("1")
	for loc, code := range map[string]string{
		"file://" + f.dir:                       CodeBadLocator,
		f.dir:                                   CodeBadLocator,
		"git://git.example.com/org/docs":        CodeBadLocator,
		"ssh://git.example.com/org/docs":        CodeBadLocator,
		"git@git.example.com:org/docs.git":      CodeBadLocator,
		"https://user:pw@git.example.com/org/d": CodeBadLocator,
		"https://other.example.org/org/docs":    CodeHostNotAllowed,
		"https://10.0.0.5/org/docs":             CodeHostNotAllowed,
	} {
		_, err := f.recipe().List(context.Background(), Request{Locator: loc})
		var e *Error
		if !errors.As(err, &e) || e.Code != code {
			t.Errorf("%q: %v, want %s", loc, err, code)
		}
	}
	if f.calls != 0 {
		t.Fatalf("clone called %d times for refused locators", f.calls)
	}
	if _, err := f.recipe("git.example.com").List(context.Background(), Request{Locator: repoURL}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.recipe("nothing.example.org").List(context.Background(), Request{Locator: repoURL}); err == nil || f.calls != 1 {
		t.Errorf("a host not listed was cloned (calls %d, err %v)", f.calls, err)
	}
}

func TestMatchGlob(t *testing.T) {
	for _, c := range []struct {
		p, n string
		ok   bool
	}{
		{"x/**/*.md", "x/a.md", true},
		{"guide/**/*.md", "guide/x/y/a.md", true},
		{"guide/**/*.md", "guide/a.txt", false},
		{"guide/**/*.md", "doc/a.md", false},
		{"**/*.md", "a.md", true},
		{"**", "a/b/c", true},
		{"guide/*.md", "guide/x/a.md", false},
		{"guide/**/b c.md", "guide/q/b c.md", true},
		{"a/**/b/**/c", "a/b/c", true},
		{"[", "a", false},
	} {
		if MatchGlob(c.p, c.n) != c.ok {
			t.Errorf("MatchGlob(%q, %q) != %v", c.p, c.n, c.ok)
		}
	}
}

func TestHostAllowed(t *testing.T) {
	list := []string{"Git.Example.com", "*.corp.example.com", "192.0.2.7"}
	for h, ok := range map[string]bool{
		"git.example.com": true, "GIT.example.com": true, "git.example.com:8443": true,
		"a.corp.example.com": true, "corp.example.com": false, "evil-corp.example.com": false,
		"192.0.2.7": true, "192.0.2.8": false, "": false, "example.com": false,
	} {
		if HostAllowed(h, list) != ok {
			t.Errorf("HostAllowed(%q) != %v", h, ok)
		}
	}
	if HostAllowed("192.0.2.7", []string{"*.0.2.7"}) || HostAllowed("git.example.com", nil) {
		t.Error("an IP matched a wildcard or an empty list allowed a host")
	}
	for p, ok := range map[string]bool{"a.example.com": true, "*.example.com": true, "https://a.example.com": false, "a.example.com:443": false, "": false, "*.": false, "a/b": false} {
		if ValidHostPattern(p) != ok {
			t.Errorf("ValidHostPattern(%q) != %v", p, ok)
		}
	}
}

func TestEveryBuiltinHasItsText(t *testing.T) {
	r, err := NewRegistry(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range Builtins() {
		rc, ok := r.Get(n)
		if !ok || rc.Name() != n || rc.Shape() != ShapeFeed || strings.TrimSpace(rc.Instructions()) == "" {
			t.Errorf("built-in %s incomplete", n)
		}
	}
	if _, ok := r.Get("exec-anything"); ok {
		t.Error("a name outside the built-ins resolved")
	}
	r, _ = NewRegistry(Options{Instructions: map[string]string{"git-docs": "mine"}})
	if rc, _ := r.Get("git-docs"); rc.Instructions() != "mine" {
		t.Error("instructions override not applied")
	}
}

func TestGitReaderReadsAndRefuses(t *testing.T) {
	f := newFixture(t)
	f.write("guide/a b.md", "hello")
	f.write("guide/big.md", strings.Repeat("x", 100))
	f.write("secret.txt", "outside docs")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(f.dir, filepath.Join(f.dir, "guide", "link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/etc/hostname", filepath.Join(f.dir, "guide", "file-link.md")); err != nil {
			t.Fatal(err)
		}
	}
	f.commit("1")
	r := NewGitReader([]string{"git.example.com"}, f.clone)
	defer r.Close()
	ctx := context.Background()

	if s, cut, err := r.Read(ctx, repoURL+"#guide/a b.md", 1<<20); err != nil || s != "hello" || cut {
		t.Fatalf("read = %q %v %v", s, cut, err)
	}
	if s, cut, err := r.Read(ctx, repoURL+"#guide/big.md", 10); err != nil || len(s) != 10 || !cut {
		t.Fatalf("cut = %q %v %v", s, cut, err)
	}
	if f.calls != 1 {
		t.Fatalf("cloned %d times for one repository", f.calls)
	}
	bad := []string{
		repoURL + "#../x", repoURL + "#.git/config", repoURL + "#.GIT/HEAD", repoURL + "#/etc/passwd", repoURL + "#guide/../../x", repoURL + "#", repoURL,
		repoURL + "#docs\\a.md", repoURL + "#nope.md", repoURL + "#docs",
		"https://other.example.org/org/docs#guide/a b.md", "file://" + f.dir + "#guide/big.md",
	}
	if runtime.GOOS != "windows" {
		bad = append(bad, repoURL+"#guide/file-link.md", repoURL+"#guide/link/secret.txt")
	}
	for _, loc := range bad {
		if s, _, err := r.Read(ctx, loc, 100); err == nil {
			t.Errorf("%q was read: %q", loc, s)
		}
	}
}

func TestMain(m *testing.M) {
	gitxtest.QuietGit()
	os.Exit(m.Run())
}

func TestGitDocsSkipsWhatIsNotARegularFileOnEveryPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	f := newFixture(t)
	f.write("guide/a.md", "1")
	f.write("guide/b.md", "1")
	first := f.commit("1")
	if err := os.Remove(filepath.Join(f.dir, "guide/b.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.md", filepath.Join(f.dir, "guide/b.md")); err != nil { // a file became a symlink
		t.Fatal(err)
	}
	if err := os.Symlink("a.md", filepath.Join(f.dir, "guide/c.md")); err != nil { // a new symlink
		t.Fatal(err)
	}
	f.commit("2")
	req := Request{Locator: repoURL, Include: []string{"guide/*.md"}}
	l, err := f.recipe().List(context.Background(), req)
	if err != nil || len(l.Entries) != 1 || l.Entries[0].Key != "guide/a.md" {
		t.Fatalf("first listing = %+v, %v", l, err)
	}
	req.Cursor = first
	l, err = f.recipe().List(context.Background(), req)
	if err != nil || len(l.Entries) != 0 {
		t.Fatalf("incremental listing emitted a symlink: %+v, %v", l, err)
	}
}
