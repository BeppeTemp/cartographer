package lint

import (
	"reflect"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// crossPair is kb-a and kb-b mounted together; with mounted false they stay
// unmounted siblings. Pages are written through the KB so each one's graph
// view is invalidated at once, not after the cross max age.
func crossPair(t *testing.T) (a, b *kb.KB) {
	t.Helper()
	a, b = tempKB(t), tempKB(t)
	m := kb.NewMounts(map[string]*kb.KB{"kb-a": a, "kb-b": b}, []string{"kb-a", "kb-b"}, nil)
	a.SetMounts(m)
	b.SetMounts(m)
	return a, b
}

func putPage(t *testing.T, k *kb.KB, id, body string) {
	t.Helper()
	fm, err := okf.ParseFrontmatter("type: Note\ntitle: T")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.WriteConcept(okf.ConceptID(id), fm, body, ""); err != nil {
		t.Fatal(err)
	}
}

func messageOf(fs []Finding, path, check string) string {
	for _, f := range fs {
		if f.Path == path && f.Check == check {
			return f.Message
		}
	}
	return ""
}

// AC-cross-kb-links-12
func TestBrokenCrossKBLink(t *testing.T) {
	t.Parallel()
	a, b := crossPair(t)
	putPage(t, b, "ops/x", "x\n")
	putPage(t, a, "p", "See [[kb:kb-b/ops/x]] and [[kb:kb-b/ops/gone]].\n")
	putPage(t, a, "q", "See [[kb:kb-b/ops/x]] and [[kb:kb-z/ops/x]] and [[kb:kb-b@host/ops/gone]].\n")
	putPage(t, a, "bad", "See [[kb:kb b/ops/x]].\n")
	for name, run := range map[string]func() []Finding{
		"Run":         func() []Finding { f, _ := Run(a, "", false); return f },
		"ScopedCheck": func() []Finding { return ScopedCheck(a, ids("p", "q", "bad")) },
	} {
		got := run()
		if m := messageOf(got, "p.md", "broken_cross_kb_link"); m != "broken cross-KB link to kb-b:ops/gone" {
			t.Errorf("%s: p = %q", name, m)
		}
		if hasCheck(got, "q.md", "broken_cross_kb_link") {
			t.Errorf("%s: a present target, an unmounted KB and a server qualifier are no finding", name)
		}
		if m := messageOf(got, "bad.md", "broken_cross_kb_link"); m != "malformed cross-KB link" {
			t.Errorf("%s: bad = %q", name, m)
		}
		for _, f := range got {
			if f.Check != "broken_cross_kb_link" {
				continue
			}
			wantKBs := f.Path == "p.md"
			if wantKBs != (len(f.OtherKBs) == 1 && f.OtherKBs[0] == "kb-b") {
				t.Errorf("%s: %s OtherKBs = %v", name, f.Path, f.OtherKBs)
			}
		}
		if hasCheck(got, "p.md", "broken_link") {
			t.Errorf("%s: a cross link is never a broken_link", name)
		}
	}
	// No mounts: nothing resolves, no finding.
	solo := tempKB(t)
	putPage(t, solo, "p", "[[kb:kb-b/ops/gone]]\n")
	if f, _ := Run(solo, "", false); hasCheck(f, "p.md", "broken_cross_kb_link") {
		t.Error("a KB with no mounts reports no cross-KB finding")
	}
}

// AC-cross-kb-links-13
func TestSelfNamedCrossKBLink(t *testing.T) {
	t.Parallel()
	a, _ := crossPair(t)
	putPage(t, a, "x", "x\n")
	putPage(t, a, "p", "See [[kb:kb-a/x]].\n")
	for name, got := range map[string][]Finding{
		"Run":         func() []Finding { f, _ := Run(a, "", false); return f }(),
		"ScopedCheck": ScopedCheck(a, ids("p")),
	} {
		if m := messageOf(got, "p.md", "broken_cross_kb_link"); m != "cross-KB link names this KB: write [[x]]" {
			t.Errorf("%s: %q", name, m)
		}
		for _, f := range got {
			if f.Check == "broken_cross_kb_link" && len(f.OtherKBs) != 0 {
				t.Errorf("%s: the self finding names no other KB: %v", name, f.OtherKBs)
			}
		}
	}
	if refs, err := a.CrossIncoming("x"); err != nil || len(refs) != 0 {
		t.Errorf("a self-named link is never an edge: %v %v", refs, err)
	}
	if got := a.CrossTarget(kb.CrossLink{KB: "kb-a", ID: "x"}); got.Mounted {
		t.Errorf("CrossTarget of the own name = %+v", got)
	}
	// It clears no orphan: p links ops/x with it only.
	if f, _ := Run(a, "", false); !hasCheck(f, "x.md", "orphan") {
		t.Error("a self-named link must not clear orphan")
	}
	// No mounts: the own name is unknown, no finding.
	solo := tempKB(t)
	putPage(t, solo, "p", "[[kb:kb-a/ops/x]]\n")
	if f, _ := Run(solo, "", false); hasCheck(f, "p.md", "broken_cross_kb_link") {
		t.Error("no mounts, no own name: no finding")
	}
}

// AC-cross-kb-links-15: the shared lint always reports the orphan; a resolved
// cross edge only names the KBs that would clear it (Finding.ClearedBy), and
// the caller's rights decide after the cache.
func TestOrphanCrossKBEdges(t *testing.T) {
	t.Parallel()
	a, b := crossPair(t)
	writeFile(t, a.DataRoot(), "arch/_map.md", "---\ntype: Map\ntitle: Arch\nkind: map\n---\n")
	putPage(t, a, "arch/other", "other\n")
	putPage(t, a, "target", "target\n")
	putPage(t, a, "arch/top", "[[kb:kb-b/ops/x]]\n")
	putPage(t, a, "arch/top2", "[[kb:kb-b/ops/x2]]\n")
	putPage(t, b, "ops/x", "x\n")
	putPage(t, b, "from", "[[kb:kb-a/target]]\n")
	clearedBy := func(findings func() []Finding) map[string][]string {
		out := map[string][]string{}
		for _, x := range findings() {
			if x.Check == "orphan" {
				out[x.Path] = x.ClearedBy
			}
		}
		return out
	}
	run := func() []Finding {
		f, err := Run(a, "", false)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	want := []string{"kb-b"}
	got := clearedBy(run)
	if !reflect.DeepEqual(got["target.md"], want) || !reflect.DeepEqual(got["arch/top.md"], want) {
		t.Errorf("a cross edge must name its KB in ClearedBy: %v", got)
	}
	if _, ok := got["arch/top2.md"]; !ok || got["arch/top2.md"] != nil {
		t.Errorf("an unresolved outgoing link clears nothing, and the finding stays: %v", got)
	}
	sc := clearedBy(func() []Finding { return ScopedCheck(a, ids("target", "arch/top")) })
	if !reflect.DeepEqual(sc["target.md"], want) || !reflect.DeepEqual(sc["arch/top.md"], want) {
		t.Errorf("ScopedCheck agrees with Run: %v", sc)
	}
	// The B pages go: the findings stay, with nothing to clear them.
	for _, id := range []okf.ConceptID{"from", "ops/x"} {
		if err := b.WithGitLock(func() error { return b.DeleteConcept(id) }); err != nil {
			t.Fatal(err)
		}
	}
	got = clearedBy(run)
	if _, ok := got["target.md"]; !ok || got["target.md"] != nil || got["arch/top.md"] != nil {
		t.Errorf("without the B pages nothing clears the orphans: %v", got)
	}
}
