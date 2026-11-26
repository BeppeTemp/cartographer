package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
)

// crossRewriteRig is kb-a (the page moved away from) and kb-b (the KB that
// holds the links and receives the call), both mounted, old-a an alias of kb-a.
type crossRewriteRig struct {
	a, b *kb.KB
	sb   *Server
}

func newCrossRewriteRig(t *testing.T) crossRewriteRig {
	t.Helper()
	r := crossRewriteRig{a: kbtest.New(t), b: kbtest.New(t)}
	r.a.AuthName, r.b.AuthName = "kb-a", "kb-b"
	m := kb.NewMounts(map[string]*kb.KB{"kb-a": r.a, "kb-b": r.b}, []string{"kb-a", "kb-b"}, map[string]string{"old-a": "kb-a"})
	r.a.SetMounts(m)
	r.b.SetMounts(m)
	r.sb = New("test")
	RegisterKBTools(r.sb, r.b, Deps{})
	t.Cleanup(func() { waitReadRefresh(r.b) })
	return r
}

// put writes a page and commits it, so a later commit count sees only the tool's.
func (r crossRewriteRig) put(t *testing.T, k *kb.KB, id, raw string) {
	t.Helper()
	p := filepath.Join(k.DataRoot(), filepath.FromSlash(id)+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, k.Root, "add", "-A")
	gitIn(t, k.Root, "commit", "-q", "-m", "fixture "+id)
}

func rewriteArgs(fromKB, fromID, toKB, toID string) string {
	b, _ := json.Marshal(map[string]any{"retargets": []map[string]string{{"from_kb": fromKB, "from_id": fromID, "to_kb": toKB, "to_id": toID}}})
	return string(b)
}

func pageBody(t *testing.T, k *kb.KB, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(k.DataRoot(), filepath.FromSlash(id)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const notePage = "---\ntype: Note\n---\n"

// AC-cross-kb-rewrite-3
func TestCrossLinkRewrite(t *testing.T) {
	t.Parallel()
	r := newCrossRewriteRig(t)
	r.put(t, r.a, "new", notePage+"moved here\n")
	r.put(t, r.b, "p", notePage+"see [[kb:kb-a/old#s|label]] and [[kb:old-a/old]] and [[kb:kb-a/other]] and `[[kb:kb-a/old]]`\n")
	r.put(t, r.b, "q", notePage+"nothing here\n")
	before := commitCount(t, r.b)

	res := callTool(t, r.sb, "cross_link_rewrite", rewriteArgs("kb-a", "old", "kb-a", "new"))
	if res.IsError {
		t.Fatalf("cross_link_rewrite: %s", resultText(res))
	}
	if got := commitCount(t, r.b) - before; got != 1 {
		t.Fatalf("%d commits in kb-b, want 1", got)
	}
	if want := "see [[kb:kb-a/new#s|label]] and [[kb:kb-a/new]] and [[kb:kb-a/other]] and `[[kb:kb-a/old]]`\n"; !strings.HasSuffix(pageBody(t, r.b, "p"), "---\n"+want) {
		t.Errorf("page p = %q", pageBody(t, r.b, "p"))
	}
	var out struct {
		Rewritten struct{ Pages, Links int } `json:"rewritten"`
	}
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil || out.Rewritten.Pages != 1 || out.Rewritten.Links != 2 {
		t.Errorf("result %s (%v)", resultText(res), err)
	}
	// Idempotent: nothing left to retarget, nothing committed.
	res = callTool(t, r.sb, "cross_link_rewrite", rewriteArgs("kb-a", "old", "kb-a", "new"))
	if res.IsError || commitCount(t, r.b) != before+1 {
		t.Errorf("second call: error %v, commits %d (want %d)", res.IsError, commitCount(t, r.b), before+1)
	}
}

func TestCrossLinkRewriteInternalForm(t *testing.T) {
	t.Parallel()
	r := newCrossRewriteRig(t)
	r.put(t, r.b, "p", notePage+"[[kb:kb-a/old#s|l]]\n")
	r.put(t, r.b, "ops/y", notePage+"landed\n")
	if res := callTool(t, r.sb, "cross_link_rewrite", rewriteArgs("kb-a", "old", "kb-b", "ops/y")); res.IsError {
		t.Fatal(resultText(res))
	}
	if got, want := pageBody(t, r.b, "p"), "---\n[[ops/y#s|l]]\n"; !strings.HasSuffix(got, want) {
		t.Errorf("page = %q, want the internal form %q", got, want)
	}
}

func TestCrossLinkRewriteBadPageWritesNothing(t *testing.T) {
	t.Parallel()
	r := newCrossRewriteRig(t)
	r.put(t, r.b, "good", notePage+"[[kb:kb-a/old]]\n")
	r.put(t, r.b, "zbad", "---\ntype: [unclosed\n---\n[[kb:kb-a/old]]\n")
	before := commitCount(t, r.b)
	res := callTool(t, r.sb, "cross_link_rewrite", rewriteArgs("kb-a", "old", "kb-a", "new"))
	if !res.IsError || !strings.Contains(resultText(res), "zbad") {
		t.Fatalf("want an error naming the page: %s", resultText(res))
	}
	if pageBody(t, r.b, "good") != notePage+"[[kb:kb-a/old]]\n" || commitCount(t, r.b) != before {
		t.Error("a failed call must leave the KB untouched")
	}
}

func TestCrossLinkRewriteValidation(t *testing.T) {
	t.Parallel()
	r := newCrossRewriteRig(t)
	for _, args := range []string{
		`{"retargets":[]}`,
		rewriteArgs("kb a", "x", "kb-a", "y"),
		rewriteArgs("kb-a", "../x", "kb-a", "y"),
		rewriteArgs("kb-a", "x", "kb-a", ""),
	} {
		if res := callTool(t, r.sb, "cross_link_rewrite", args); !res.IsError {
			t.Errorf("%s: want an error", args)
		}
	}
	s := New("test")
	k := setupTestKB(t)
	RegisterKBTools(s, k, Deps{})
	if res := callTool(t, s, "cross_link_rewrite", rewriteArgs("kb-a", "x", "kb-a", "y")); !res.IsError {
		t.Error("a KB with no mounts must refuse")
	}
}

// AC-cross-kb-rewrite-4
func TestCrossLinkRewriteAuthorization(t *testing.T) {
	t.Parallel()
	r := newCrossRewriteRig(t)
	args := json.RawMessage(rewriteArgs("kb-a", "old", "kb-a", "new"))
	scoped := auth.Policy{Permissions: []auth.Permission{{KB: "kb-b", Write: true, Maps: []string{"ops"}}}}
	if err := authorizeTool(scoped, r.b, "kb-b", "cross_link_rewrite", args); err == nil || !strings.Contains(err.Error(), denyNeedsWholeKB) {
		t.Errorf("scoped writer: %v, want %s", err, denyNeedsWholeKB)
	}
	readOnly := auth.Policy{Permissions: []auth.Permission{{KB: "kb-b"}}}
	if err := authorizeTool(readOnly, r.b, "kb-b", "cross_link_rewrite", args); err == nil {
		t.Error("a read-only grant must be refused")
	}
	whole := auth.Policy{Permissions: []auth.Permission{{KB: "kb-b", Write: true}}}
	if err := authorizeTool(whole, r.b, "kb-b", "cross_link_rewrite", args); err != nil {
		t.Errorf("whole-KB writer refused: %v", err)
	}
	if !listToolNames(t, r.sb)["cross_link_rewrite"] {
		t.Error("the full listing must carry cross_link_rewrite")
	}
	r.sb.SetToolsProfile("agent")
	if listToolNames(t, r.sb)["cross_link_rewrite"] {
		t.Error("the agent profile must not list cross_link_rewrite")
	}
}

// A relation of the page's KB that pins the old name to another remote leaves
// that alias link unresolved (only kb-a, which is not written, has a remote): it is not rewritten, the link by the current name is.
func TestCrossLinkRewriteHonoursAliasGuard(t *testing.T) {
	t.Parallel()
	r := newCrossRewriteRig(t)
	gitIn(t, r.a.Root, "remote", "add", "origin", "https://git.example.com/org/a.git")
	if err := os.WriteFile(filepath.Join(r.b.Root, kb.SettingsFile), []byte("depends_on:\n  - {name: old-a, remote: git.example.com/org/other}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.b.Root, "add", kb.SettingsFile)
	gitIn(t, r.b.Root, "commit", "-q", "-m", "deps")
	r.put(t, r.b, "p", notePage+"[[kb:old-a/old]] [[kb:kb-a/old]]\n")
	if res := callTool(t, r.sb, "cross_link_rewrite", rewriteArgs("kb-a", "old", "kb-a", "new")); res.IsError {
		t.Fatal(resultText(res))
	}
	if got := pageBody(t, r.b, "p"); !strings.HasSuffix(got, "---\n[[kb:old-a/old]] [[kb:kb-a/new]]\n") {
		t.Errorf("page = %q", got)
	}
}
