package lint

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// D315: title_h1_mismatch and title_quality.

// nameFindings lints one concept of map arch, whose _map.md carries mapFM.
func nameFindings(t *testing.T, mapFM, file, fm, body string) []Finding {
	t.Helper()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md", "---\ntype: Map\ntitle: Arch\n"+mapFM+"---\n")
	writeFile(t, k.DataRoot(), "arch/"+file+".md", "---\ntype: Note\n"+fm+"---\n"+body+"\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return findings
}

func TestTitleH1Mismatch(t *testing.T) {
	t.Parallel()
	got := findingsOf(nameFindings(t, "", "a", "title: Alpha\n", "# Beta\ntext"), "title_h1_mismatch")
	if len(got) != 1 || got[0].Fix == nil || got[0].Fix.Kind != FixSyncH1 || got[0].Fix.To != "Alpha" {
		t.Fatalf("mismatch: %+v", got)
	}
	if got := findingsOf(nameFindings(t, "", "a", "title: Alpha\n", "# Alpha\ntext"), "title_h1_mismatch"); len(got) != 0 {
		t.Errorf("matching title flagged: %+v", got)
	}
	if got := findingsOf(nameFindings(t, "", "a", "title: Alpha\n", "no heading"), "title_h1_mismatch"); len(got) != 0 {
		t.Errorf("no H1 flagged: %+v", got)
	}
}

func TestTitleQuality(t *testing.T) {
	t.Parallel()
	long := func(n int) string { return strings.Repeat("a", n) }
	// The one case with a fix (D366): the title the repair sets.
	fixTo := map[string]string{"emoji": "Launch day", "heart": "Launch"}
	for name, tc := range map[string]struct {
		mapFM, file, fm string
		want            string // "" = no finding
	}{
		"emoji":              {"", "a", "title: \"Launch ⭐ day\"\n", "decorative characters"},
		"heart":              {"", "a", "title: \"Launch ❤️\"\n", "decorative characters"},
		"plain":              {"", "a", "title: Launch day\n", ""},
		"over default":       {"", "a", "title: " + long(101) + "\n", "101 characters (limit 100"},
		"at default":         {"", "a", "title: " + long(100) + "\n", ""},
		"status word":        {"", "a", "title: Router — active\nstatus: active\n", `status word "active"`},
		"no status field":    {"", "a", "title: Router — active\n", ""},
		"word inside a word": {"", "a", "title: Proactive routers\nstatus: active\n", ""},
		"forbidden term":     {"forbidden_title_terms: [nickname]\n", "a", "title: The NickName story\n", `forbidden term "nickname"`},
		"date slug":          {"", "2026-01-02-x", "title: Router\n", "date-prefixed ID"},
		"date slug journal":  {"kind: journal\n", "2026-01-02-x", "title: Router\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			got := findingsOf(nameFindings(t, tc.mapFM, tc.file, tc.fm, "# x"), "title_quality")
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("unexpected: %+v", got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0].Message, tc.want) {
				t.Fatalf("finding: %+v", got)
			}
			if fixTo[name] == "" && got[0].Fix != nil {
				t.Fatalf("unexpected fix: %+v", got[0].Fix)
			}
			if fixTo[name] != "" && (got[0].Fix == nil || got[0].Fix.Kind != FixSetValue || got[0].Fix.Field != "title" || got[0].Fix.To != fixTo[name]) {
				t.Fatalf("fix = %+v, want set_value title %q", got[0].Fix, fixTo[name])
			}
		})
	}
}

// The write-time path (CheckConcept) reports both checks too.
func TestTitleChecks_WriteTime(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md", "---\ntype: Map\ntitle: Arch\nforbidden_title_terms: [nickname]\n---\n")
	got := CheckConcept(k, okf.ConceptID("arch/a"), "---\ntype: Note\ntitle: Nickname\n---\n# Other\n")
	if len(findingsOf(got, "title_h1_mismatch")) != 1 || len(findingsOf(got, "title_quality")) != 1 {
		t.Fatalf("CheckConcept: %+v", got)
	}
}

func TestMapContract_TitleKeys(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/_map.md", "---\ntype: Map\ntitle: Arch\nforbidden_title_terms: [a, b]\n---\n")
	c, err := k.ReadMapContract("arch")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.ForbiddenTitleTerms) != 2 || len(c.Malformed) != 0 || len(c.LegacyKeys) != 0 {
		t.Fatalf("contract: %+v", c)
	}
}
