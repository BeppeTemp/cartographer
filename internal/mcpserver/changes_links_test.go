package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// changesLinksKB is a git-backed KB mounted as "docs" with the maps ops,
// other and hidden, and helpers to write, delete, rename and commit.
type changesLinksKB struct {
	t    *testing.T
	k    *kb.KB
	s    *Server
	base time.Time
	n    int
}

func newChangesLinksKB(t *testing.T) *changesLinksKB {
	t.Helper()
	k := setupTestKB(t)
	if !gitx.IsRepo(k.Root) {
		t.Skip("git not in PATH")
	}
	k.AuthName = "docs"
	for _, m := range []string{"ops", "other", "hidden"} {
		if err := k.CreateMap(m, m, "map", nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return &changesLinksKB{t: t, k: k, s: s, base: time.Now().UTC().Truncate(time.Second).Add(5 * time.Minute)}
}

func (c *changesLinksKB) write(id, body string) {
	c.t.Helper()
	p := filepath.Join(c.k.DataRoot(), filepath.FromSlash(id)+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		c.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\ntype: Note\ntitle: "+filepath.Base(id)+"\n---\n"+body), 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func (c *changesLinksKB) remove(id string) {
	c.t.Helper()
	if err := os.Remove(filepath.Join(c.k.DataRoot(), filepath.FromSlash(id)+".md")); err != nil {
		c.t.Fatal(err)
	}
}

func (c *changesLinksKB) rename(from, to string) {
	c.t.Helper()
	if err := os.Rename(filepath.Join(c.k.DataRoot(), filepath.FromSlash(from)+".md"), filepath.Join(c.k.DataRoot(), filepath.FromSlash(to)+".md")); err != nil {
		c.t.Fatal(err)
	}
}

// commit records a commit inside the window when inWindow, otherwise at the
// wall clock, before it.
func (c *changesLinksKB) commit(inWindow bool) {
	c.t.Helper()
	var env []string
	if inWindow {
		c.n++
		at := c.base.Add(time.Duration(c.n) * time.Minute).Format(time.RFC3339)
		env = []string{"GIT_AUTHOR_DATE=" + at, "GIT_COMMITTER_DATE=" + at}
	}
	if err := gitx.Commit(c.k.Root, fmt.Sprintf("test: step %d", c.n), "Tester", "tester@example.test", env...); err != nil {
		c.t.Fatal(err)
	}
}

func (c *changesLinksKB) call(ctx requestContext, args string) changesSinceResult {
	c.t.Helper()
	res := c.s.callTool(ctx, "changes_since", json.RawMessage(args))
	if res.IsError || len(res.Content) == 0 {
		c.t.Fatalf("changes_since %s: %+v", args, res)
	}
	var out changesSinceResult
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		c.t.Fatal(err)
	}
	return out
}

func edgeList(edges []changesLinkEdge) string {
	parts := make([]string, 0, len(edges))
	for _, e := range edges {
		s := e.Source + ">" + e.Target
		if e.TargetMissing {
			s += "!"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

func TestChangesSinceLinks(t *testing.T) {
	c := newChangesLinksKB(t)
	c.write("ops/t", "T.\n")
	c.write("ops/u", "U.\n")
	c.write("ops/p", "P.\n")
	c.write("ops/g", "G.\n")
	c.write("ops/a", "[t](t.md)\n")
	c.write("ops/b", "[g](g.md)\n")
	c.write("ops/lone", "[u](u.md)\n")
	c.write("ops/r", "[u](u.md)\n")
	c.write("hidden/h", "[t](../ops/t.md)\n")
	c.commit(false)

	c.write("ops/a", "[p](p.md)\n") // a→t removed, a→p added
	c.commit(true)
	c.remove("ops/lone") // lone→u removed
	c.remove("ops/g")
	c.write("ops/b", "No more links.\n") // b→g removed, g gone
	c.commit(true)
	// Moved to another map with the same body: from its old place [u](u.md)
	// named ops/u, from the new one it names other/u.
	c.rename("ops/r", "other/r")
	c.commit(true)

	since := fmt.Sprintf(`{"since":%q,"links":true}`, c.base.Format(time.RFC3339))
	got := c.call(adminCtx, since).Links
	if got == nil {
		t.Fatal("links missing")
	}
	if s := edgeList(got.Added); s != "ops/a>ops/p other/r>other/u" {
		t.Errorf("added = %s", s)
	}
	if s := edgeList(got.Removed); s != "ops/a>ops/t ops/b>ops/g! ops/lone>ops/u other/r>ops/u" {
		t.Errorf("removed = %s", s)
	}
	// t keeps the hidden linker for an admin; u lost both.
	if s := strings.Join(got.BecameOrphan, " "); s != "ops/u" {
		t.Errorf("became_orphan = %s", s)
	}
	if s := strings.Join(got.NoLongerOrphan, " "); s != "ops/p" {
		t.Errorf("no_longer_orphan = %s", s)
	}

	// A narrowed token sees neither the other map nor the hidden linker, so
	// for it ops/t became an orphan too.
	narrow := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}}}})
	nr := c.call(narrow, since).Links
	if s := edgeList(nr.Added) + " | " + edgeList(nr.Removed); s != "ops/a>ops/p | ops/a>ops/t ops/b>ops/g! ops/lone>ops/u" {
		t.Errorf("narrowed edges = %s", s)
	}
	if s := strings.Join(nr.BecameOrphan, " "); s != "ops/t ops/u" {
		t.Errorf("narrowed became_orphan = %s", s)
	}

	// Opt-in: the default response carries no links field at all.
	res := c.s.callTool(adminCtx, "changes_since", json.RawMessage(fmt.Sprintf(`{"since":%q}`, c.base.Format(time.RFC3339))))
	if strings.Contains(res.Content[0].Text, `"links`) {
		t.Errorf("default output has links: %s", res.Content[0].Text)
	}

	// A range reaching the root commit has no base: every link is added.
	root := c.call(adminCtx, `{"since":"100000h","links":true}`).Links
	if len(root.Removed) != 0 || !strings.Contains(edgeList(root.Added), "hidden/h>ops/t") {
		t.Errorf("root range: %+v", root)
	}
}

func TestChangesSinceLinksCap(t *testing.T) {
	c := newChangesLinksKB(t)
	c.commit(false)
	var b strings.Builder
	for i := 0; i < maxChangesLinksEdges+1; i++ {
		fmt.Fprintf(&b, "[n](n%03d.md)\n", i)
	}
	c.write("ops/hub", b.String())
	c.commit(true)
	got := c.call(adminCtx, fmt.Sprintf(`{"since":%q,"links":true}`, c.base.Format(time.RFC3339))).Links
	if len(got.Added) != maxChangesLinksEdges || !got.Truncated || got.Added[0].Target != "ops/n000" {
		t.Fatalf("cap: %d edges, truncated %v", len(got.Added), got.Truncated)
	}
}
