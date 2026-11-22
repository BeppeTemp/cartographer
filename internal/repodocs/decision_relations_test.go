package repodocs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Split so the cited-path and cited-decision gates do not take the fixtures for
// pointers; every fixture number is a record of the real tree (D11 is a gap).
const relDir = "docs/" + "decisions/"

// relRecord is one fixture record: `D<n>-<slug>.md` or `D-new-<slug>.md`, with
// the given frontmatter lines after `topic:`.
func relRecord(file string, keys ...string) (string, string) {
	head := strings.TrimSuffix(file, ".md")
	if !strings.HasPrefix(head, "D-new-") && !strings.HasPrefix(head, "AD") {
		head = head[:strings.Index(head, "-")]
	}
	fm := "---\ntopic: project-governance\n"
	for _, k := range keys {
		fm += k + "\n"
	}
	return file, fm + "---\n\n# " + head + " — A record\n"
}

func writeRelTree(t *testing.T, files map[string]string) (string, []Decision) {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	decisions, err := LoadDecisions(root)
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}
	return root, decisions
}

func relTree(records ...[2]string) map[string]string {
	files := map[string]string{}
	for _, r := range records {
		files[relDir+r[0]] = r[1]
	}
	return files
}

func rec(file string, keys ...string) [2]string {
	f, body := relRecord(file, keys...)
	return [2]string{f, body}
}

// AC-P5-9: a relation declared on one side fails with the line to add on the other.
func TestDecisionRelationProblems(t *testing.T) {
	for _, tc := range []struct {
		name    string
		records [][2]string
		want    string // a substring of one problem; "" means none
	}{
		{"mirrored", [][2]string{rec("D10-a.md", "superseded_by: D12"), rec("D12-b.md", "supersedes: D10"),
			rec("D13-c.md", "amended_by: D14"), rec("D14-d.md", "amends: D13")}, ""},
		{"a pending record passes on the branch", [][2]string{rec("D10-a.md", "superseded_by: D-new-x"), rec("D-new-x.md", "supersedes: D10")}, ""},
		{"a key in the body is prose", [][2]string{{"D10-a.md", "---\ntopic: x\n---\n\n# D10 — A\n\nsupersedes: D12\n"}, rec("D12-b.md")}, ""},
		{"supersedes without the mirror", [][2]string{rec("D10-a.md"), rec("D12-b.md", "supersedes: D10")},
			"D10-a.md: write `superseded_by: D12` in its frontmatter: D12-b.md supersedes it"},
		{"amends without the mirror", [][2]string{rec("D10-a.md", "amended_by: D13"), rec("D12-b.md", "amends: D10"), rec("D13-c.md", "amends: D10")},
			"D10-a.md: write `amended_by: D13, D12` in its frontmatter: D12-b.md amends it"},
		{"superseded_by without the mirror", [][2]string{rec("D10-a.md", "superseded_by: D12"), rec("D12-b.md")},
			"D12-b.md: write `supersedes: D10` in its frontmatter"},
		{"amended_by without the mirror", [][2]string{rec("D10-a.md", "amended_by: D12"), rec("D12-b.md")},
			"D12-b.md: write `amends: D10` in its frontmatter"},
		{"two records supersede one", [][2]string{rec("D10-a.md", "superseded_by: D12"), rec("D12-b.md", "supersedes: D10"), rec("D13-c.md", "supersedes: D10")},
			"but D13-c.md supersedes it too"},
		{"an id with no file", [][2]string{rec("D12-b.md", "amends: D15")}, "names D15, which has no file"},
		{"a record names itself", [][2]string{rec("D12-b.md", "amends: D12")}, "`amends: D12` names the record itself"},
		{"supersedes and amends one record", [][2]string{rec("D10-a.md", "superseded_by: D12", "amended_by: D12"), rec("D12-b.md", "supersedes: D10", "amends: D10")},
			"both supersedes and amends D10"},
		{"a two-record cycle", [][2]string{rec("D10-a.md", "supersedes: D12", "superseded_by: D12"), rec("D12-b.md", "supersedes: D10", "superseded_by: D10")},
			"form a cycle"},
		{"a longer cycle", [][2]string{rec("D10-a.md", "amends: D13", "amended_by: D12"), rec("D12-b.md", "amends: D10", "amended_by: D13"), rec("D13-c.md", "amends: D12", "amended_by: D10")},
			"form a cycle, D10 → D13 → D12 → D10"},
		{"a superseded record amends a later one", [][2]string{rec("D10-a.md", "superseded_by: D13", "amends: D12"), rec("D12-b.md", "amended_by: D10"), rec("D13-c.md", "supersedes: D10")},
			"D10-a.md: is superseded by D13, so it cannot supersede or amend D12"},
		{"the AD file carries a key", [][2]string{rec("AD1-AD13-x.md", "amends: D10"), rec("D10-a.md")}, "the historical AD file declares no relation"},
		{"a bracketed list", [][2]string{rec("D12-b.md", "amends: [D10]")}, "`amends: [D10]` does not parse"},
		{"an empty value", [][2]string{rec("D12-b.md", "supersedes:")}, "`supersedes: ` does not parse"},
		{"a bare number", [][2]string{rec("D12-b.md", "amends: 10")}, "does not parse"},
		{"an id twice", [][2]string{rec("D12-b.md", "amends: D10, D10")}, "none twice"},
		{"two successors", [][2]string{rec("D10-a.md", "superseded_by: D12, D13")}, "`superseded_by:` names one record"},
		{"an indented key", [][2]string{rec("D12-b.md", " supersedes: D10")}, "looks like a relation key but is not one: write `supersedes:`"},
		{"a hyphen for the underscore", [][2]string{rec("D10-a.md", "superseded-by: D12")}, "write `superseded_by:` at the start of the line"},
		{"a capital letter", [][2]string{rec("D12-b.md", "Amended_By: D10")}, "write `amended_by:` at the start of the line, in lower case"},
		{"a space before the colon", [][2]string{rec("D12-b.md", "amends : D10")}, "write `amends:`"},
		{"a key twice", [][2]string{rec("D12-b.md", "amends: D10", "amends: D13")}, "`amends:` appears twice"},
		{"a CRLF frontmatter is read", [][2]string{rec("D10-a.md"), {"D12-b.md", "---\r\ntopic: x\r\namends: D10\r\n---\r\n\r\n# D12 — B\r\n"}},
			"D10-a.md: write `amended_by: D12`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, decisions := writeRelTree(t, relTree(tc.records...))
			got := RelationProblems(decisions)
			if tc.want == "" {
				if len(got) > 0 {
					t.Fatalf("want no problem, got:\n%s", strings.Join(got, "\n"))
				}
				return
			}
			if !strings.Contains(strings.Join(got, "\n"), tc.want) {
				t.Fatalf("want a problem containing %q, got:\n%s", tc.want, strings.Join(got, "\n"))
			}
		})
	}
}

// A current-state file that cites a superseded record names its successor on the
// same line; an amended record, a decision record and the generated index are not concerned.
func TestSupersededCitations(t *testing.T) {
	files := relTree(rec("D10-a.md", "superseded_by: D12"), rec("D12-b.md", "supersedes: D10"),
		rec("D13-c.md", "amended_by: D14"), rec("D14-d.md", "amends: D13"),
		[2]string{"D-new-e.md", "---\ntopic: x\n---\n\n# D-new-e — E\n\nReplaces D10.\n"})
	files["docs/"+"page.md"] = "Holds D10.\nHolds D10, now D12.\nHolds D13.\n"
	files["docs/decisions.md"] = "Intro.\n" + IndexBegin + "\n- D10\n" + IndexEnd + "\nSee D10.\n"
	files["CONTRIBUTING.md"] = "D10 rules.\n"
	files["internal/x/AGENTS.md"] = "Per D10.\n"
	files[".claude/skills/s/SKILL.md"] = "Follow D10.\n"
	files["README.md"] = "D10 is not a current-state file of the rule.\n"
	files["CHANGELOG.md"] = "D10\n"
	root, decisions := writeRelTree(t, files)

	got, err := SupersededCitations(root, decisions)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		".claude/skills/s/SKILL.md:1: cites D10, which D12 supersedes: cite D12 instead, or name it on the same line",
		"CONTRIBUTING.md:1: cites D10",
		"docs/decisions.md:5: cites D10",
		"docs/" + "page.md:1: cites D10",
		"internal/x/AGENTS.md:1: cites D10",
	}
	if len(got) != len(want) {
		t.Fatalf("want %d citations, got %d:\n%s", len(want), len(got), strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("citation %d: want %q, got %q", i, want[i], got[i])
		}
	}
}

// AC-P5-10: the index lists a superseded record only under Superseded, and marks an
// amended one in its topic.
func TestRenderIndexListsSupersededRecordsLast(t *testing.T) {
	_, decisions := writeRelTree(t, relTree(
		rec("D10-a.md", "superseded_by: D-new-x"),
		rec("D12-b.md", "amended_by: D-new-x, D13"),
		rec("D13-c.md", "amends: D12"),
		rec("D-new-x.md", "supersedes: D10", "amends: D12"),
	))
	if p := RelationProblems(decisions); len(p) > 0 {
		t.Fatalf("the fixture is inconsistent:\n%s", strings.Join(p, "\n"))
	}
	got := RenderIndex(decisions)
	if !strings.Contains(got, "4 records, one file each, grouped by the topic they belong to; 1 superseded, listed last.\n") {
		t.Errorf("the count line says how many are superseded:\n%s", got)
	}
	if n := strings.Count(got, "(decisions/D10-a.md)"); n != 1 {
		t.Errorf("the superseded record is listed once, got %d:\n%s", n, got)
	}
	sup := strings.Index(got, "\n### Superseded\n")
	if sup < 0 || !strings.HasSuffix(got, "\n### Superseded\n\n- [D10 — A record](decisions/D10-a.md) → [D-new-x](decisions/D-new-x.md)\n") {
		t.Fatalf("Superseded is the last section, each record pointing at its successor:\n%s", got)
	}
	if !strings.Contains(got[:sup], "- [D12 — A record](decisions/D12-b.md) (amended by [D13](decisions/D13-c.md), [D-new-x](decisions/D-new-x.md))\n") {
		t.Errorf("an amended record stays in its topic, followed by the records that amend it, oldest first:\n%s", got)
	}

	// Without a relation the index is the flat list per topic.
	_, plain := writeRelTree(t, relTree(rec("D10-a.md"), rec("D12-b.md")))
	if got := RenderIndex(plain); strings.Contains(got, "Superseded") || strings.Contains(got, "amended by") || !strings.Contains(got, "2 records, one file each, grouped by the topic they belong to.\n") {
		t.Errorf("an index with no relation keeps its shape:\n%s", got)
	}
}

// AC-P5-11: a pending decision that supersedes a numbered one lands with both sides
// numbered: the lander rewrites the placeholder in the frontmatter of the older record too.
func TestLanderNumbersBothSidesOfARelation(t *testing.T) {
	e := newLandEnv(t)
	e.queue("rel", "feat(client): rel\n", "2026-01-01T10:00:00Z", map[string]string{
		relDir + "D-new-rel.md": "---\ntopic: x\nsupersedes: D10\n---\n\n# D-new-rel — Rel\n",
		relDir + "D10-old.md":   "---\ntopic: x\nsuperseded_by: D-new-rel\n---\n\n# D10 — Old\n",
	})
	if out, ok := e.land(); !ok {
		t.Fatalf("land failed\n%s", out)
	}
	if got := e.upGit("show", "main:"+relDir+"D10-old.md"); !strings.Contains(got, "\nsuperseded_by: D11\n") {
		t.Errorf("the older record names its successor by its number, got\n%s", got)
	}
	if got := e.upGit("show", "main:"+relDir+"D11-rel.md"); !strings.Contains(got, "\nsupersedes: D10\n") || !strings.Contains(got, "# D11 — Rel") {
		t.Errorf("the pending record lands numbered, its relation intact, got\n%s", got)
	}
}
