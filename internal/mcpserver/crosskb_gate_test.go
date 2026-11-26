package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/feedback"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
)

// gateRig is a git KB kb-a (maps arch and ops) mounted with kb-b, which holds
// ops/x and is readable whole or not by the caller.
func gateRig(t *testing.T, mountB bool) *kb.KB {
	t.Helper()
	a, b := gateKB(t), kbtest.New(t)
	a.AuthName, b.AuthName = "kb-a", "kb-b"
	writeBPage := filepath.Join(b.DataRoot(), "ops", "x.md")
	if err := os.MkdirAll(filepath.Dir(writeBPage), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(writeBPage, []byte("---\ntype: Note\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	byName, order := map[string]*kb.KB{"kb-a": a}, []string{"kb-a"}
	if mountB {
		byName["kb-b"], order = b, append(order, "kb-b")
	}
	m := kb.NewMounts(byName, order, nil)
	a.SetMounts(m)
	b.SetMounts(m)
	return a
}

// gateVerdict writes arch/p with body and judges it as ctx would.
func gateVerdict(t *testing.T, k *kb.KB, who string, body string) string {
	t.Helper()
	p := filepath.Join(k.DataRoot(), "arch", "p.md")
	if err := os.WriteFile(p, []byte("---\ntype: Note\ntitle: P\nstatus: active\n---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(p) })
	res := judgeWrite(k, "concept_write", newCallerView(crossCallers()[who], k))
	if res == nil {
		return ""
	}
	return res.Content[0].Text
}

// AC-cross-kb-links-12
func TestWriteGateCrossKB(t *testing.T) {
	missing := "See [[kb:kb-b/ops/gone]].\n"
	for who, want := range map[string]bool{"reads-a-b": true, "admin": true, "a-only": false, "b-scoped": false} {
		k := gateRig(t, true)
		if got := strings.Contains(gateVerdict(t, k, who, missing), "broken_cross_kb_link"); got != want {
			t.Errorf("%s: refused for the missing target = %v, want %v", who, got, want)
		}
	}
	// A page that is there is no finding; a KB that is not mounted is none either.
	if got := gateVerdict(t, gateRig(t, true), "reads-a-b", "See [[kb:kb-b/ops/x]].\n"); strings.Contains(got, "broken_cross_kb_link") {
		t.Errorf("a present target was refused: %s", got)
	}
	if got := gateVerdict(t, gateRig(t, false), "admin", missing); strings.Contains(got, "broken_cross_kb_link") {
		t.Errorf("an unmounted KB was refused: %s", got)
	}
	// AC-cross-kb-links-13: the self-named link is refused for every caller who sees the page.
	for _, who := range []string{"reads-a-b", "a-only", "admin"} {
		got := gateVerdict(t, gateRig(t, true), who, "See [[kb:kb-a/ops/x]].\n")
		if !strings.Contains(got, "cross-KB link names this KB: write [[ops/x]]") {
			t.Errorf("%s: the self-named link was not refused: %q", who, got)
		}
	}
}

// leakWorld is kb-a with a page linking kb-b/ops/x, in one of three worlds
// the caller cannot tell apart: kb-b not mounted, kb-b mounted with the page,
// kb-b mounted without it.
func leakWorld(t *testing.T, world string) *kb.KB {
	t.Helper()
	a, b := kbtest.New(t), kbtest.New(t)
	a.AuthName, b.AuthName = "kb-a", "kb-b"
	put := func(k *kb.KB, id, body string) {
		p := filepath.Join(k.DataRoot(), filepath.FromSlash(id)+".md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("---\ntype: Note\n---\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(a, "p", "See [[kb:kb-b/ops/x]].\n")
	byName, order := map[string]*kb.KB{"kb-a": a}, []string{"kb-a"}
	if world != "absent" {
		byName["kb-b"], order = b, append(order, "kb-b")
	}
	if world == "present" {
		put(b, "ops/x", "x\n")
	}
	m := kb.NewMounts(byName, order, nil)
	a.SetMounts(m)
	b.SetMounts(m)
	return a
}

// AC-cross-kb-links-14: what a caller who cannot read kb-b is told is byte for
// byte the same whether kb-b is absent, present with the page, or present
// without it (leak surfaces 1, 2, 4: lint with its totals, gate_check, the
// conformance counts).
func TestCrossKBLeakSurfaces(t *testing.T) {
	hidden := crossCallers()["a-only"]
	call := func(ctx context.Context, tool Tool) string {
		t.Helper()
		res, err := tool.Handler(ctx, json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return res.Content[0].Text
	}
	surfaces := map[string]func(k *kb.KB) string{
		"lint":       func(k *kb.KB) string { return call(hidden, toolLint(k)) },
		"gate_check": func(k *kb.KB) string { return call(hidden, toolGateCheck(k)) },
		"ui_findings": func(k *kb.KB) string {
			cc := &conformanceCache{}
			fs, err := cc.lintFindings(k)
			if err != nil {
				t.Fatal(err)
			}
			vis, err := uiVisibleFindingsFrom(hidden, k, "", fs)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(vis)
			return string(b)
		},
	}
	for name, surface := range surfaces {
		want := surface(leakWorld(t, "absent"))
		for _, world := range []string{"present", "missing"} {
			if got := surface(leakWorld(t, world)); got != want {
				t.Errorf("%s: world %q differs from the absent KB:\n%s\n%s", name, world, got, want)
			}
		}
	}
	// A caller who reads kb-b whole does learn the target is missing.
	reader := crossCallers()["reads-a-b"]
	if got := call(reader, toolLint(leakWorld(t, "missing"))); !strings.Contains(got, "broken_cross_kb_link") {
		t.Errorf("a reader of kb-b is not told: %s", got)
	}
}

// orphanWorld is kb-a with the map arch (two pages, so neither is alone), whose
// page arch/p links kb-b/x and whose page arch/q is linked from kb-b/from when
// present: kb-b has the page x, and the page from, or neither.
func orphanWorld(t *testing.T, present bool) *kb.KB {
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
	put(a, "arch/p.md", "---\ntype: Note\n---\n[[kb:kb-b/x]]\n")
	put(a, "arch/q.md", "---\ntype: Note\n---\nq\n")
	if present {
		put(b, "x.md", "---\ntype: Note\n---\nx\n")
		put(b, "from.md", "---\ntype: Note\n---\n[[kb:kb-a/arch/q]]\n")
	}
	m := kb.NewMounts(map[string]*kb.KB{"kb-a": a, "kb-b": b}, []string{"kb-a", "kb-b"}, nil)
	a.SetMounts(m)
	b.SetMounts(m)
	return a
}

// A caller who cannot read kb-b whole sees the orphan verdict of a page whose
// only edge crosses to kb-b whether or not that edge resolves: it is no
// existence oracle. A reader of kb-b has it cleared when the edge resolves.
func TestOrphanVerdictIsNoExistenceOracle(t *testing.T) {
	callers := crossCallers()
	shown := func(who string, present bool, path string) bool {
		a := orphanWorld(t, present)
		fs, err := (&conformanceCache{}).lintFindings(a)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range lintFilteredFor(callers[who], a, fs) {
			if f.Path == path && f.Check == "orphan" {
				return true
			}
		}
		return false
	}
	for _, path := range []string{"arch/p.md", "arch/q.md"} {
		for _, who := range []string{"a-only", "b-scoped"} {
			if !shown(who, true, path) || !shown(who, false, path) {
				t.Errorf("%s / %s: the orphan must show whether or not kb-b has the page", who, path)
			}
		}
		if shown("reads-a-b", true, path) {
			t.Errorf("reads-a-b / %s: a reader of kb-b has it cleared when the edge resolves", path)
		}
		if !shown("reads-a-b", false, path) {
			t.Errorf("reads-a-b / %s: with no edge the orphan stays", path)
		}
	}
}

// kb_guide says how to link another KB exactly when initialize does: the
// connection serves two or more KBs the caller reads and the caller writes.
func TestKBGuideCrossLinkLine(t *testing.T) {
	t.Parallel()
	r := newCrossRig(t)
	const marker = "[[kb:<name>/<id>]]"
	writer := principal(auth.Permission{KB: "kb-a", Write: true}, auth.Permission{KB: "kb-b", Write: true})
	reader := principal(auth.Permission{KB: "kb-a"}, auth.Permission{KB: "kb-b"})
	guide := func(ctx context.Context) string { return renderKBGuide(ctx, r.a, nil, feedback.Mode("")) }
	if got := guide(withRoutedSet(writer, []string{"kb-a", "kb-b"})); !strings.Contains(got, marker) {
		t.Errorf("a writer on a two-KB connection lacks the line:\n%s", got)
	}
	if got := guide(withRoutedSet(writer, []string{"kb-a"})); strings.Contains(got, marker) {
		t.Errorf("a one-KB connection has the line")
	}
	if got := guide(writer); strings.Contains(got, marker) {
		t.Errorf("a per-KB endpoint has the line")
	}
	if got := guide(withRoutedSet(reader, []string{"kb-a", "kb-b"})); strings.Contains(got, marker) {
		t.Errorf("a read-only caller has the line")
	}
	// A KB of the set the caller cannot read does not count.
	if got := guide(withRoutedSet(principal(auth.Permission{KB: "kb-a", Write: true}), []string{"kb-a", "kb-b"})); strings.Contains(got, marker) {
		t.Errorf("an unreadable second KB counted")
	}
}
