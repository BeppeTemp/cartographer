package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// orphanCleared says whether the shared lint reports path as an orphan that
// the resolved edges of another KB would clear.
func orphanCleared(fs []lint.Finding, path string) bool {
	for _, f := range fs {
		if f.Path == path && f.Check == "orphan" && len(f.ClearedBy) > 0 {
			return true
		}
	}
	return false
}

func stampHas(fs []lint.Finding, path, check string) bool {
	for _, f := range fs {
		if f.Path == path && f.Check == check {
			return true
		}
	}
	return false
}

// putThroughServer writes (or, with body "", removes) a page the way a write
// through the server does: under the git lock, which forgets the last
// validation of the graph.
func putThroughServer(t *testing.T, k *kb.KB, id, body string) {
	t.Helper()
	p := filepath.Join(k.DataRoot(), filepath.FromSlash(id)+".md")
	err := k.WithGitLock(func() error {
		if body == "" {
			return os.Remove(p)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte("---\ntype: Note\n---\n"+body), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// AC-cross-kb-links-16
func TestCrossStampThroughTheCache(t *testing.T) {
	t.Parallel()
	a, b, plain := kbtest.New(t), kbtest.New(t), kbtest.New(t)
	a.AuthName, b.AuthName = "kb-a", "kb-b"
	m := kb.NewMounts(map[string]*kb.KB{"kb-a": a, "kb-b": b}, []string{"kb-a", "kb-b"}, nil)
	a.SetMounts(m)
	b.SetMounts(m)
	putThroughServer(t, a, "p", "See [[kb:kb-b/ops/x]].\n")
	putThroughServer(t, a, "t", "target\n")
	cc := &conformanceCache{}
	lintOf := func() []lint.Finding {
		t.Helper()
		fs, err := cc.lintFindings(a)
		if err != nil {
			t.Fatal(err)
		}
		return fs
	}
	if !stampHas(lintOf(), "p.md", "broken_cross_kb_link") {
		t.Fatal("a missing target is a finding")
	}
	calls := cc.LintCalls()
	lintOf()
	if cc.LintCalls() != calls {
		t.Fatal("nothing changed: the cached lint must be reused")
	}
	// B gains the target page, with no change in A.
	putThroughServer(t, b, "ops/x", "x\n")
	if got := lintOf(); stampHas(got, "p.md", "broken_cross_kb_link") || cc.LintCalls() == calls {
		t.Errorf("B added the target: the finding must go (recomputed %v)", cc.LintCalls() != calls)
	}
	// And loses it.
	calls = cc.LintCalls()
	putThroughServer(t, b, "ops/x", "")
	if !stampHas(lintOf(), "p.md", "broken_cross_kb_link") || cc.LintCalls() == calls {
		t.Error("B removed the target: the finding must come back")
	}
	// B adds, then removes, a link to A's page t: its orphan goes and returns.
	if !stampHas(lintOf(), "t.md", "orphan") || orphanCleared(lintOf(), "t.md") {
		t.Fatal("t is an orphan nothing clears before B links it")
	}
	putThroughServer(t, b, "from", "[[kb:kb-a/t]]\n")
	if !orphanCleared(lintOf(), "t.md") {
		t.Error("B links t: the shared lint must name kb-b as clearing it, with no change in A")
	}
	putThroughServer(t, b, "from", "")
	if orphanCleared(lintOf(), "t.md") {
		t.Error("B no longer links t: nothing clears it")
	}
	// A KB with no cross link keeps a stamp with no cross part.
	if s := crossStamp(plain); s != "" {
		t.Errorf("a KB with no cross link has stamp %q", s)
	}
	if lintInputsStamp(plain) != lintInputsStamp(plain) {
		t.Error("unstable stamp")
	}
}

// AC-cross-kb-links-16
func TestCrossStampIsCheap(t *testing.T) {
	// Not parallel: the build counter is global.
	a, b := kbtest.New(t), kbtest.New(t)
	a.AuthName, b.AuthName = "kb-a", "kb-b"
	m := kb.NewMounts(map[string]*kb.KB{"kb-a": a, "kb-b": b}, []string{"kb-a", "kb-b"}, nil)
	a.SetMounts(m)
	b.SetMounts(m)
	putThroughServer(t, a, "p", "[[kb:kb-b/ops/x]]\n")
	putThroughServer(t, b, "q", "[[kb:kb-a/p]]\n")
	_ = lintInputsStamp(a)
	before := kb.CrossDigestBuilds()
	for i := 0; i < 1000; i++ {
		_ = lintInputsStamp(a)
	}
	if got := kb.CrossDigestBuilds() - before; got != 0 {
		t.Errorf("1000 stamps with nothing changed rebuilt the inbound digest %d times", got)
	}
}

// A KB that only receives a cross link (B links A, A links nothing out) keeps a
// whole-KB cached body per set of rights on B: the orphan count of A depends on
// whether the caller reads B. Both warm orders give each caller its own answer.
func TestOverviewCacheKeyAccountsForInboundCrossRights(t *testing.T) {
	callers := crossCallers()
	var orphansOf = func(ctx context.Context, a *kb.KB) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/overview", nil).WithContext(ctx)
		serveUICached(rec, req, a, "overview", true, uiWholeStamp, func(w http.ResponseWriter, r *http.Request) {
			fs, err := (&conformanceCache{}).lintFindings(a)
			if err != nil {
				t.Fatal(err)
			}
			n := len(lintFilteredFor(r.Context(), a, fs))
			_, _ = w.Write([]byte(strconv.Itoa(n)))
		})
		n, err := strconv.Atoi(strings.TrimSpace(rec.Body.String()))
		if err != nil {
			t.Fatalf("body %q: %v", rec.Body.String(), err)
		}
		return n
	}
	for _, order := range [][]string{{"reads-a-b", "a-only"}, {"a-only", "reads-a-b"}} {
		a := inboundWorld(t)
		counts := map[string]int{}
		for _, who := range order {
			counts[who] = orphansOf(callers[who], a)
		}
		if counts["reads-a-b"] >= counts["a-only"] {
			t.Errorf("order %v: the reader of kb-b must see fewer orphans than the caller who cannot, got %v", order, counts)
		}
	}
	if fp := crossFingerprint(callers["admin"], inboundWorld(t)); fp == "" {
		t.Error("a KB with only an inbound cross link must not keep the plain key")
	}
}

// inboundWorld is kb-a with two archive pages and no link out, and kb-b with a
// page linking arch/q.
func inboundWorld(t *testing.T) *kb.KB {
	t.Helper()
	a, b := kbtest.New(t), kbtest.New(t)
	a.AuthName, b.AuthName = "kb-a", "kb-b"
	put := func(k *kb.KB, rel, body string) {
		p := filepath.Join(k.DataRoot(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(a, "arch/_map.md", "---\ntype: Map\ntitle: Arch\nkind: map\n---\n")
	put(a, "arch/q.md", "---\ntype: Note\n---\nq\n")
	put(a, "arch/r.md", "---\ntype: Note\n---\nr\n")
	put(b, "from.md", "---\ntype: Note\n---\n[[kb:kb-a/arch/q]]\n")
	m := kb.NewMounts(map[string]*kb.KB{"kb-a": a, "kb-b": b}, []string{"kb-a", "kb-b"}, nil)
	a.SetMounts(m)
	b.SetMounts(m)
	return a
}
