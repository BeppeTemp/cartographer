package lint

import (
	"reflect"
	"strings"
	"testing"
)

func TestStripDecoration(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"🚀 Deploy notes ✨": "Deploy notes",
		"Launch ❤️":        "Launch",
		"Plain":            "Plain",
		"🚀":                "",
	} {
		got := StripDecoration(in)
		if got != want || StripDecoration(got) != got {
			t.Errorf("StripDecoration(%q) = %q, want %q (idempotent)", in, got, want)
		}
	}
}

// D366: only the decorative case of title_quality has a fix.
func TestTitleQuality_OnlyDecorationHasAFix(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		fm  string
		fix bool
	}{
		"decorated":       {"title: \"🚀 Deploy notes\"\n", true},
		"only decoration": {"title: \"🚀\"\n", false},
		"status word":     {"title: Router — active\nstatus: active\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			got := findingsOf(nameFindings(t, "", "a", tc.fm, "# x"), "title_quality")
			if len(got) != 1 || (got[0].Fix != nil) != tc.fix {
				t.Fatalf("findings: %+v", got)
			}
		})
	}
}

// D366: link_to_retired carries the retarget only for a live successor, and
// names the linkers it edits.
func TestLinkToRetired_FixNeedsALiveSuccessor(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\ntitle: Ops\nkind: map\n---\n")
	page := func(id, extra string) {
		writeFile(t, k.DataRoot(), "ops/"+id+".md", "---\ntype: Note\ntitle: "+id+"\n"+extra+"---\n# "+id+"\n")
	}
	page("old", "status: deprecated\nsuperseded_by: ops/new\n")
	page("new", "")
	page("a", "status: deprecated\nsuperseded_by: ops/b\n")
	page("b", "status: deprecated\nsuperseded_by: ops/new\n")
	page("none", "status: deprecated\n")
	page("dangling", "status: deprecated\nsuperseded_by: ops/missing\n")
	for _, id := range []string{"old", "a", "b", "none", "dangling"} {
		writeFile(t, k.DataRoot(), "ops/l-"+id+".md", "---\ntype: Note\ntitle: L "+id+"\n---\n# L\n\n[x]("+id+".md)\n")
	}
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]*Fix{
		"ops/old.md":      {Kind: FixRetargetLinks, Field: "ops/old", To: "ops/new", Targets: []string{"ops/l-old"}},
		"ops/b.md":        {Kind: FixRetargetLinks, Field: "ops/b", To: "ops/new", Targets: []string{"ops/l-b"}},
		"ops/a.md":        nil, // its successor is retired itself
		"ops/none.md":     nil,
		"ops/dangling.md": nil,
	}
	for path, fix := range want {
		f := findingFor(findings, path, "link_to_retired")
		if f == nil {
			t.Errorf("%s: no link_to_retired finding", path)
			continue
		}
		if !reflect.DeepEqual(f.Fix, fix) {
			t.Errorf("%s: fix = %+v, want %+v", path, f.Fix, fix)
		}
	}
}

// D366: index_lists_retired carries the drop only for a concept with a line
// of its own.
func TestIndexListsRetired_FixNeedsALineOfItsOwn(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\ntitle: Ops\nkind: map\n---\n")
	writeFile(t, k.DataRoot(), "ops/index.md", "---\ntype: Index\ntitle: Ops\n---\n- [Old](old.md)\n- [Old2](old2.md) and [Live](live.md)\n")
	for _, id := range []string{"live", "live2", "live3"} {
		writeFile(t, k.DataRoot(), "ops/"+id+".md", "---\ntype: Note\ntitle: "+id+"\n---\n# "+id+"\n")
	}
	for _, id := range []string{"old", "old2"} {
		writeFile(t, k.DataRoot(), "ops/"+id+".md", "---\ntype: Note\ntitle: "+id+"\nstatus: deprecated\n---\n# "+id+"\n")
	}
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var fixes []*Fix
	for _, f := range findings {
		if f.Check == "index_lists_retired" {
			fixes = append(fixes, f.Fix)
		}
	}
	if len(fixes) != 2 || fixes[0] == nil || fixes[0].Kind != FixDropIndexEntry || fixes[0].Field != "ops/old" || fixes[1] != nil {
		t.Fatalf("fixes = %+v", fixes)
	}
}

// TestStripDecoration_KeepsMeaningfulSymbols pins #1503 WP4: a symbol that is
// part of the title is not decoration, and no title_quality finding is raised.
func TestStripDecoration_KeepsMeaningfulSymbols(t *testing.T) {
	t.Parallel()
	for _, title := range []string{
		"Room kept at 20°C",
		"x^2 scaling",
		"Run `make gate` first",
		"Acme® gateway",
		"Acme™ and © notes",
	} {
		if got := StripDecoration(title); got != title {
			t.Errorf("StripDecoration(%q) = %q", title, got)
		}
		got := findingsOf(nameFindings(t, "", "a", "title: \""+title+"\"\n", "# "+title+"\ntext"), "title_quality")
		for _, f := range got {
			if strings.HasPrefix(f.Message, decorativeTitleMsg) {
				t.Errorf("%q flagged as decorative: %+v", title, f)
			}
		}
	}
	if got := StripDecoration("🚀 Launch ✔ day 🇮🇹"); got != "Launch day" {
		t.Errorf("emoji not stripped: %q", got)
	}
}
