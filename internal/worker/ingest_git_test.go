package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
)

func TestIngestGitSourceNeedsAnAllowedHost(t *testing.T) {
	k := newIKB()
	k.src("s/ok", map[string]any{"connector": "git", "locator": "https://git.example.com/org/docs#guide/a.md"})
	k.src("s/other", map[string]any{"connector": "git", "locator": "https://other.example.org/org/docs#guide/a.md"})
	k.src("s/ssh", map[string]any{"connector": "git", "locator": "ssh://git@git.example.com/org/docs#guide/a.md"})
	k.src("s/up", map[string]any{"connector": "git", "locator": "https://git.example.com/org/docs#../x"})
	for _, u := range []string{"u/1-other", "u/2-ssh", "u/3-up", "u/4-ok"} {
		k.unit(u, "s/"+strings.SplitN(u, "-", 2)[1], "open", "any", nil)
	}
	in := newInst(t, TierSmall)
	if it := next(t, in, k); it != nil {
		t.Fatalf("no git.allow_hosts, yet took %+v", it)
	}
	in.gitHosts = []string{"git.example.com"}
	in.skipped = map[string]time.Time{}
	it := next(t, in, k)
	if it == nil || it.Key != "u/4-ok" || it.Data.(ingestCand).connector != "git" {
		t.Fatalf("took %+v, want only the unit on the allowed host", it)
	}
}

func TestIngestGitFetcherReadsAndRefuses(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.MkdirAll(filepath.Join(dir, "guide"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "guide", "a.md"), []byte("the page"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "1")
	clone := func(ctx context.Context, url, dest string) error { return gitx.Clone(ctx, dir, dest) }

	const repo = "https://git.example.com/org/docs"
	r := recipe.NewGitReader([]string{"git.example.com"}, clone)
	defer r.Close()
	out := (&gitFetcher{locator: repo + "#guide/a.md", reader: r}).fetch(context.Background())
	if out.IsError || !strings.HasPrefix(out.Text, fetchBanner) || !strings.HasSuffix(out.Text, "the page") {
		t.Fatalf("read = %+v", out)
	}
	for _, loc := range []string{repo + "#../x", repo + "#/etc/passwd", "file://" + dir + "#guide/a.md"} {
		if out := (&gitFetcher{locator: loc, reader: r}).fetch(context.Background()); !out.IsError {
			t.Errorf("%q was read", loc)
		}
	}
}
