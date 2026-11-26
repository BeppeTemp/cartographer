package kb

import (
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func fixedNames(m map[string][]string) func(string) []string {
	return func(kb string) []string { return m[kb] }
}

// AC-cross-kb-rewrite-1
func TestRewriteCrossLinks(t *testing.T) {
	t.Parallel()
	moves := []CrossRetarget{{FromKB: "kb-a", FromID: "ops/x", ToKB: "kb-b", ToID: "ops/y"}}
	names := fixedNames(map[string][]string{"kb-a": {"kb-a", "old-a"}})
	own := []string{"kb-c"}
	for _, c := range []struct {
		name, in, want string
		n              int
	}{
		{"plain", "[[kb:kb-a/ops/x]]", "[[kb:kb-b/ops/y]]", 1},
		{"section and label kept", "see [[kb:kb-a/ops/x#s|l]] now", "see [[kb:kb-b/ops/y#s|l]] now", 1},
		{"md suffix", "[[kb:kb-a/ops/x.md]]", "[[kb:kb-b/ops/y]]", 1},
		{"alias name", "[[kb:old-a/ops/x]]", "[[kb:kb-b/ops/y]]", 1},
		{"twice", "[[kb:kb-a/ops/x]] [[kb:kb-a/ops/x#z]]", "[[kb:kb-b/ops/y]] [[kb:kb-b/ops/y#z]]", 2},
		{"other id", "[[kb:kb-a/ops/z]]", "[[kb:kb-a/ops/z]]", 0},
		{"other kb", "[[kb:kb-d/ops/x]]", "[[kb:kb-d/ops/x]]", 0},
		{"server qualified", "[[kb:kb-a@host/ops/x]]", "[[kb:kb-a@host/ops/x]]", 0},
		{"malformed", "[[kb:kb-a/ops/x y]] [[kb:kb a/ops/x]] [[kb:kb-a]]", "[[kb:kb-a/ops/x y]] [[kb:kb a/ops/x]] [[kb:kb-a]]", 0},
		{"fence", "```\n[[kb:kb-a/ops/x]]\n```\n", "```\n[[kb:kb-a/ops/x]]\n```\n", 0},
		{"inline code", "`[[kb:kb-a/ops/x]]` [[kb:kb-a/ops/x]]", "`[[kb:kb-a/ops/x]]` [[kb:kb-b/ops/y]]", 1},
		{"markdown href", "[x](kb:kb-a/ops/x)", "[x](kb:kb-a/ops/x)", 0},
		{"internal link", "[[ops/x]]", "[[ops/x]]", 0},
	} {
		got, n := RewriteCrossLinks(c.in, own, moves, names)
		if got != c.want || n != c.n {
			t.Errorf("%s: got %q, %d; want %q, %d", c.name, got, n, c.want, c.n)
		}
	}
	// Duplicate FromKB/FromID pairs keep the first; nil names matches the name itself.
	dup := []CrossRetarget{{"kb-a", "x", "kb-b", "first"}, {"kb-a", "x", "kb-b", "second"}}
	if got, _ := RewriteCrossLinks("[[kb:kb-a/x]]", own, dup, nil); got != "[[kb:kb-b/first]]" {
		t.Errorf("duplicates: %q", got)
	}
	if got, n := RewriteCrossLinks("[[kb:kb-a/x]]", own, nil, nil); n != 0 || got != "[[kb:kb-a/x]]" {
		t.Errorf("no moves: %q %d", got, n)
	}
}

// AC-cross-kb-rewrite-2
func TestRewriteCrossLinksNeverSelfNamed(t *testing.T) {
	t.Parallel()
	own := []string{"kb-b", "old-b"}
	moves := []CrossRetarget{{FromKB: "kb-a", FromID: "ops/x", ToKB: "kb-b", ToID: "ops/y"}}
	got, n := RewriteCrossLinks("[[kb:kb-a/ops/x#s|l]] [[kb:kb-b/other]] [[kb:old-b/o2]]", own, moves, nil)
	if want := "[[ops/y#s|l]] [[kb:kb-b/other]] [[kb:old-b/o2]]"; got != want || n != 1 {
		t.Errorf("RewriteCrossLinks = %q, %d; want %q, 1", got, n, want)
	}
	// The own-named links are findings, untouched here and internalized there.
	got, n = InternalizeCrossLinks("[[kb:kb-b/other#s|l]] [[kb:old-b/o2]] [[kb:kb-a/z]] `[[kb:kb-b/c]]` [[kb:kb-b@host/q]] [[kb:kb-b]]", own)
	if want := "[[other#s|l]] [[o2]] [[kb:kb-a/z]] `[[kb:kb-b/c]]` [[kb:kb-b@host/q]] [[kb:kb-b]]"; got != want || n != 2 {
		t.Errorf("InternalizeCrossLinks = %q, %d; want %q, 2", got, n, want)
	}
	for _, out := range []string{got} {
		for _, o := range own {
			if strings.Contains(strings.ReplaceAll(out, "`[[kb:kb-b/c]]`", ""), "[[kb:"+o+"/") {
				t.Errorf("a self-named link survives: %q", out)
			}
		}
	}
}

func TestCrossLinksTo(t *testing.T) {
	a, b := mountedPair(t, map[string]string{"old-b": "kb-b"})
	writeConcept(t, a, "p1", "[[kb:kb-b/ops/x]]\n")
	writeConcept(t, a, "p2", "[[kb:old-b/ops/x#s]] [[kb:kb-b/ops/other]]\n")
	writeConcept(t, a, "p3", "[[kb:kb-b/ops/other]] [[kb:kb-b@host/ops/x]] [[kb:kb-a/ops/x]]\n")
	writeConcept(t, a, "p4", "`[[kb:kb-b/ops/x]]`\n")
	writeConcept(t, b, "q", "[[kb:kb-a/p1]]\n")
	got, err := a.CrossLinksTo(b.Mounts().NamesFor("kb-b"), map[string]bool{"ops/x": true})
	if err != nil || !reflect.DeepEqual(got, []okf.ConceptID{"p1", "p2"}) {
		t.Errorf("CrossLinksTo = %v, %v", got, err)
	}
	if got, _ := a.CrossLinksTo([]string{"kb-b"}, map[string]bool{"ops/x": true}); !reflect.DeepEqual(got, []okf.ConceptID{"p1"}) {
		t.Errorf("by current name only: %v", got)
	}
	if got, err := mustInitKB(t).CrossLinksTo([]string{"kb-b"}, map[string]bool{"ops/x": true}); err != nil || got != nil {
		t.Errorf("no mounts: %v, %v", got, err)
	}
}

// The cross-link rewrite is the only code that edits a cross link: RewriteLinks
// and UnlinkTargets leave it byte-identical (invariant of cross-kb-rewrite).
func TestInternalRewritesLeaveCrossLinksAlone(t *testing.T) {
	t.Parallel()
	body := "[[kb:kb-b/m/a#s|l]] [[m/a]] [x](kb:kb-b/m/a) [[kb:kb-b@host/m/a]]\n"
	rw, n := RewriteLinks(body, "m/x.md", map[string]string{"m/a": "m/b"})
	if want := strings.Replace(body, "[[m/a]]", "[[m/b]]", 1); rw != want || n != 1 {
		t.Errorf("RewriteLinks = %q, %d", rw, n)
	}
	ul, n := UnlinkTargets(body, "m/x.md", map[string]bool{"m/a": true})
	if want := strings.Replace(body, "[[m/a]]", "m/a", 1); ul != want || n != 1 {
		t.Errorf("UnlinkTargets = %q, %d", ul, n)
	}
}
