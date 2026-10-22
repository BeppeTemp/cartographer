package lint

import (
	"strings"
	"testing"
)

type loneFile struct{ rel, content string }

const loneNote = "---\ntype: Note\ntitle: T\n---\nBody.\n"

func loneVerdict(t *testing.T, files []loneFile, id, path string) (run, scoped bool) {
	t.Helper()
	k := tempKB(t)
	for _, f := range files {
		root := k.DataRoot()
		if strings.HasPrefix(f.rel, "services/") {
			root = k.Root // services/ is a sibling of data/, not an archive
		}
		writeFile(t, root, f.rel, f.content)
	}
	got, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return hasCheck(got, path, "orphan"), hasCheck(ScopedCheck(k, ids(id)), path, "orphan")
}

// The only live concept of an archive or of the KB is not an orphan: the
// write that creates it cannot link it to anything (D1099).
func TestOrphan_LoneLiveConcept(t *testing.T) {
	t.Parallel()
	armap := loneFile{"arch/_map.md", "---\ntype: Map\ntitle: Arch\nkind: map\n---\n"}
	cases := []struct {
		name  string
		files []loneFile
		id    string
		want  bool
	}{
		{"alone", []loneFile{armap, {"arch/lonely.md", loneNote}}, "arch/lonely", false},
		{"alone in its archive, another archive holds two linked pages", []loneFile{armap, {"arch/lonely.md", loneNote},
			{"ops/_map.md", "---\ntype: Map\ntitle: Ops\nkind: map\n---\n"},
			{"ops/a.md", "---\ntype: Note\ntitle: A\n---\n[b](b.md)\n"}, {"ops/b.md", "---\ntype: Note\ntitle: B\n---\n[a](a.md)\n"}}, "arch/lonely", false},
		{"second page links nothing", []loneFile{armap, {"arch/lonely.md", loneNote}, {"arch/other.md", loneNote}}, "arch/lonely", true},
		{"second page links the first", []loneFile{armap, {"arch/lonely.md", loneNote}, {"arch/other.md", "---\ntype: Note\ntitle: O\n---\n[l](lonely.md)\n"}}, "arch/lonely", false},
		{"one live and one retired", []loneFile{armap, {"arch/lonely.md", loneNote}, {"arch/old.md", "---\ntype: Note\ntitle: O\nstatus: archived\n---\nOld.\n"}}, "arch/lonely", false},
		{"two live and one retired", []loneFile{armap, {"arch/lonely.md", loneNote}, {"arch/other.md", loneNote}, {"arch/old.md", "---\ntype: Note\ntitle: O\nstatus: deprecated\n---\nOld.\n"}}, "arch/lonely", true},
		{"root alone", []loneFile{{"solo.md", loneNote}}, "solo", false},
		{"second root page", []loneFile{{"solo.md", loneNote}, {"duo.md", loneNote}}, "solo", true},
		{"services alone", []loneFile{{"services/x.md", loneNote}}, "services/x", false},
		{"services with another concept elsewhere", []loneFile{{"services/x.md", loneNote}, {"arch/_map.md", "---\ntype: Map\ntitle: A\nkind: map\n---\n"}, {"arch/a.md", loneNote}}, "services/x", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path := c.id + ".md"
			run, scoped := loneVerdict(t, c.files, c.id, path)
			if run != c.want || scoped != c.want {
				t.Fatalf("orphan on %s: Run=%v ScopedCheck=%v, want %v", c.id, run, scoped, c.want)
			}
		})
	}
}

func TestOrphan_LoneLive_RetiredStatuses(t *testing.T) {
	t.Parallel()
	for _, status := range retiredStatuses {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			files := []loneFile{
				{"arch/_map.md", "---\ntype: Map\ntitle: Arch\nkind: map\n---\n"},
				{"arch/lonely.md", loneNote},
				{"arch/old.md", "---\ntype: Note\ntitle: O\nstatus: " + status + "\n---\nOld.\n"},
			}
			run, scoped := loneVerdict(t, files, "arch/lonely", "arch/lonely.md")
			if run || scoped {
				t.Fatalf("one live page beside a %s one is exempt: Run=%v ScopedCheck=%v", status, run, scoped)
			}
		})
	}
}
