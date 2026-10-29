package provisioning_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
)

func TestKBArchiveNames(t *testing.T) {
	root := makeKBWithArchives(t, map[string][]string{"journal": {"a.md"}, "entities": {"b.md"}, "skills": {"c.md"}, ".omc": {"d.md"}})
	got := strings.Join(provisioning.KBArchiveNames(root), ",")
	if got != "entities,journal" {
		t.Errorf("archives = %q, want entities,journal", got)
	}
	if n := provisioning.KBArchiveNames(t.TempDir()); len(n) != 0 {
		t.Errorf("no data/: %v", n)
	}
}

func fiveArchives() provisioning.GuideKB {
	return provisioning.GuideKB{Name: "work-kb", Archives: []string{"backlog", "clients", "incidents", "infra", "notes"}}
}

// The guide is paid by every handshake of every generic client (D1097), like
// the tool descriptions: pin the neutral part, curated bodies are outside it.
func TestRenderGuide_Budget(t *testing.T) {
	const (
		sectionBudget = 2332 // bytes: measured 2120 with the three procedures listed (#1316, D285; was 1782 for 1620), plus at most 10 %
		routingBudget = 338  // bytes: measured 308 (five archives), plus at most 10 %
	)
	section := provisioning.GuideOperational(false, guideProcs())
	t.Logf("operational section: %d bytes", len(section))
	if len(section) > sectionBudget || len(section) > 2400 {
		t.Errorf("the guide's operational section is %d bytes, over its budget of %d: it is paid by every handshake of every connection, trim it or raise the budget deliberately", len(section), sectionBudget)
	}
	full := provisioning.RenderGuide([]provisioning.GuideKB{fiveArchives()}, provisioning.GuideOptions{Routed: true, Procedures: guideProcs()})
	routing := strings.TrimSuffix(strings.TrimSuffix(full, section), "\n\n")
	t.Logf("routing paragraph: %d bytes", len(routing))
	if len(routing) > routingBudget || len(routing) > 512 {
		t.Errorf("a KB's routing paragraph is %d bytes, over its budget of %d: it is paid by every handshake of every connection, trim it or raise the budget deliberately", len(routing), routingBudget)
	}
}

func guideProcs() []provisioning.ProcedureLine {
	var out []provisioning.ProcedureLine
	for _, p := range skillbundle.Procedures() {
		out = append(out, provisioning.ProcedureLine{Short: p.Short, UseWhen: p.UseWhen})
	}
	return out
}

// #1316: the cold-start section, only for a writer on an empty KB whose maps
// are none and none hidden, and that did not opt out; D285 budget for it.
func TestRenderGuide_ColdStart(t *testing.T) {
	const marker = "This KB is empty."
	const coldBudget = 302 // bytes: measured 274, plus at most 10 % (D285)
	empty := provisioning.GuideKB{Name: "kb-a"}
	cases := []struct {
		name string
		kb   provisioning.GuideKB
		opts provisioning.GuideOptions
		want bool
	}{
		{"empty routed", empty, provisioning.GuideOptions{Routed: true}, true},
		{"empty per-KB", empty, provisioning.GuideOptions{}, true},
		{"read-only", empty, provisioning.GuideOptions{ReadOnly: true}, false},
		{"hidden maps", provisioning.GuideKB{Name: "kb-a", ArchivesHidden: true}, provisioning.GuideOptions{}, false},
		{"with maps", provisioning.GuideKB{Name: "kb-a", Archives: []string{"notes"}}, provisioning.GuideOptions{}, false},
		{"preamble none", provisioning.GuideKB{Name: "kb-a", SkipPreamble: true, Curated: "x"}, provisioning.GuideOptions{}, false},
	}
	for _, c := range cases {
		g := provisioning.RenderGuide([]provisioning.GuideKB{c.kb}, c.opts)
		if got := strings.Contains(g, marker); got != c.want {
			t.Errorf("%s: cold start present = %v, want %v:\n%s", c.name, got, c.want, g)
		}
	}
	g := provisioning.RenderGuide([]provisioning.GuideKB{empty}, provisioning.GuideOptions{})
	i := strings.Index(g, marker)
	section := g[i:strings.Index(g, "Operational instructions:")]
	t.Logf("cold-start section: %d bytes", len(section))
	if len(section) > coldBudget || len(section) > 450 {
		t.Errorf("the cold-start section is %d bytes, over its budget of %d (D285)", len(section), coldBudget)
	}
	for _, frag := range []string{"`map_create`", "kebab-case", "journal", "services", "`services/<id>`", "`concept_write`"} {
		if !strings.Contains(section, frag) {
			t.Errorf("the cold-start section lacks %q:\n%s", frag, section)
		}
	}
}

func TestRenderGuide_ProceduresList(t *testing.T) {
	g := provisioning.RenderGuide([]provisioning.GuideKB{fiveArchives()}, provisioning.GuideOptions{Procedures: guideProcs()})
	for _, frag := range []string{"`ingest`", "`conflict-resolve`", "`import`", "`procedure: <name>`", "the whole guide"} {
		if !strings.Contains(g, frag) {
			t.Errorf("guide lacks %q:\n%s", frag, g)
		}
	}
	none := provisioning.RenderGuide([]provisioning.GuideKB{fiveArchives()}, provisioning.GuideOptions{})
	if strings.Contains(none, "procedure: <name>") || !strings.Contains(none, "the whole guide") {
		t.Errorf("no procedures: no list, but still the whole-guide line:\n%s", none)
	}
	ro := provisioning.RenderGuide([]provisioning.GuideKB{fiveArchives()}, provisioning.GuideOptions{ReadOnly: true, Procedures: guideProcs()})
	if !strings.Contains(ro, "`ingest`") {
		t.Errorf("a read-only guide keeps the list:\n%s", ro)
	}
}

func TestRenderGuide_ClientOnlyLinesAreLeftOut(t *testing.T) {
	g := provisioning.RenderGuide([]provisioning.GuideKB{fiveArchives()}, provisioning.GuideOptions{Routed: true})
	for _, frag := range []string{"kb reauthor", "update check", "latest_version", "cartographer-ops", "never run git"} {
		if strings.Contains(g, frag) {
			t.Errorf("the guide carries %q, which needs a Cartographer client", frag)
		}
	}
	if m := regexp.MustCompile("cartographer [a-z]+").FindString(strings.ReplaceAll(g, `"cartographer" server`, "")); m != "" {
		t.Errorf("the guide names a cartographer subcommand: %q", m)
	}
}

func TestRenderGuide_TwoKBsInGivenOrder(t *testing.T) {
	g := provisioning.RenderGuide([]provisioning.GuideKB{
		{Name: "kb-a", Archives: []string{"x"}, Curated: "Rules of A."},
		{Name: "work-kb", Curated: "Rules of B."},
	}, provisioning.GuideOptions{Routed: true})
	a, b := strings.Index(g, "pass `kb: \"kb-a\"`"), strings.Index(g, "pass `kb: \"work-kb\"`")
	if a < 0 || b < a {
		t.Fatalf("routing paragraphs missing or out of order:\n%s", g)
	}
	if strings.Count(g, "Operational instructions:") != 1 {
		t.Errorf("the bullets must appear once:\n%s", g)
	}
	ops := strings.Index(g, "Operational instructions:")
	if !(ops > b && strings.Index(g, "Rules of A.") > ops && strings.Index(g, "Rules of B.") > strings.Index(g, "Rules of A.")) {
		t.Errorf("order must be routing, bullets, curated A, curated B:\n%s", g)
	}
	if !strings.Contains(g, "No maps yet.") {
		t.Errorf("a KB with no archive says so:\n%s", g)
	}
}

func TestRenderGuide_NotRoutedHasNoKBSentence(t *testing.T) {
	g := provisioning.RenderGuide([]provisioning.GuideKB{fiveArchives()}, provisioning.GuideOptions{})
	if strings.Contains(g, "`kb` argument") {
		t.Errorf("a per-KB connection has no kb argument:\n%s", g)
	}
}

func TestRenderGuide_HiddenArchives(t *testing.T) {
	g := provisioning.RenderGuide([]provisioning.GuideKB{{Name: "kb-a", ArchivesHidden: true}}, provisioning.GuideOptions{})
	if !strings.Contains(g, "No maps are visible to this connection.") {
		t.Errorf("hidden archives:\n%s", g)
	}
}

func TestRenderGuide_PreambleNone(t *testing.T) {
	one := provisioning.GuideKB{Name: "kb-a", Curated: "Only A.", SkipPreamble: true}
	two := provisioning.GuideKB{Name: "work-kb", Curated: "B."}
	if g := provisioning.RenderGuide([]provisioning.GuideKB{one, two}, provisioning.GuideOptions{}); !strings.Contains(g, "Operational instructions:") {
		t.Errorf("one KB opting out must keep the bullets for the other:\n%s", g)
	}
	two.SkipPreamble = true
	g := provisioning.RenderGuide([]provisioning.GuideKB{one, two}, provisioning.GuideOptions{})
	if strings.Contains(g, "Operational instructions:") {
		t.Errorf("every KB opting out drops the bullets:\n%s", g)
	}
	if !strings.Contains(g, "The following directives are the \"kb-a\" KB's own") {
		t.Errorf("the scope sentence stays (D182):\n%s", g)
	}
}

func TestRenderGuide_PlaceholdersStayVerbatim(t *testing.T) {
	g := provisioning.RenderGuide([]provisioning.GuideKB{{Name: "kb-a", Curated: "See {{repo:kb-a}} and {{path:docs}}."}}, provisioning.GuideOptions{})
	if !strings.Contains(g, "See {{repo:kb-a}} and {{path:docs}}.") {
		t.Errorf("a placeholder was rewritten:\n%s", g)
	}
}

func TestRenderGuide_ReadOnlyNamesNoWriteTool(t *testing.T) {
	g := provisioning.RenderGuide([]provisioning.GuideKB{fiveArchives()}, provisioning.GuideOptions{Routed: true, ReadOnly: true})
	for _, tool := range []string{"concept_write", "concept_batch", "concept_patch", "log_append", "git_conflict_resolve", "conflicts_list"} {
		if strings.Contains(g, tool) {
			t.Errorf("a read-only guide names %s:\n%s", tool, g)
		}
	}
	if !strings.Contains(g, "read-only") || !strings.Contains(g, "`search`") {
		t.Errorf("a read-only guide says so and keeps orientation:\n%s", g)
	}
	if strings.Contains(g, "workspace bind") {
		t.Errorf("the bridge sentence is not part of the guide:\n%s", g)
	}
}

func TestKBCuratedInstructions_PreambleNone(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "instructions.md"), []byte("<!-- cartographer: preamble: none -->\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, skip := provisioning.KBCuratedInstructions(root)
	if body != "Body." || !skip {
		t.Errorf("body=%q skip=%v", body, skip)
	}
}

func writeInstr(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "instructions.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestKBCuratedInstructions_CutAtLineBoundaryWithMarker(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	content := strings.Repeat(line, 1000) // 100000 bytes, over the cap
	body, _ := provisioning.KBCuratedInstructions(writeInstr(t, content))
	kept := (provisioning.MaxInstructionsBytes / 100) * 100
	if !strings.HasPrefix(body, strings.TrimSpace(content[:kept])) {
		t.Fatalf("body is not the text cut at the last line boundary")
	}
	marker := body[len(strings.TrimSpace(content[:kept])):]
	want := fmt.Sprintf("%d bytes left out", len(content)-kept)
	if !strings.Contains(marker, "was cut") || !strings.Contains(marker, want) {
		t.Errorf("marker %q lacks %q", marker, want)
	}
}

func TestKBCuratedInstructions_ExactlyAtCapIsNotCut(t *testing.T) {
	line := strings.Repeat("x", 63) + "\n"
	content := strings.Repeat(line, provisioning.MaxInstructionsBytes/64)
	if len(content) != provisioning.MaxInstructionsBytes {
		t.Fatal("fixture is not at the cap")
	}
	body, _ := provisioning.KBCuratedInstructions(writeInstr(t, content))
	if body != strings.TrimSpace(content) {
		t.Errorf("a file exactly at the cap must be returned whole (len %d)", len(body))
	}
}

func TestKBCuratedInstructions_CutNeverSplitsARune(t *testing.T) {
	content := strings.Repeat("é", provisioning.MaxInstructionsBytes) // 2 bytes each, no newline
	body, _ := provisioning.KBCuratedInstructions(writeInstr(t, content))
	if !utf8.ValidString(body) || !strings.Contains(body, "was cut") {
		t.Errorf("cut text must stay valid UTF-8 with a marker")
	}
}

func TestKBCuratedInstructions_CutInsideFrontmatterKeepsOnlyMarker(t *testing.T) {
	content := "---\nperimeter: " + strings.Repeat("k", provisioning.MaxInstructionsBytes) + "\n---\nBody.\n"
	body, _ := provisioning.KBCuratedInstructions(writeInstr(t, content))
	if strings.Contains(body, "perimeter") || !strings.HasPrefix(body, "[instructions.md was cut") {
		t.Errorf("frontmatter must not pass as body: %.80q", body)
	}
}

func TestKBCuratedInstructions_SymlinkRefused(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("OUTSIDE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "instructions.md")); err != nil {
		t.Skipf("no symlinks: %v", err)
	}
	if body, _ := provisioning.KBCuratedInstructions(root); body != "" {
		t.Errorf("a symlinked instructions.md must yield nothing, got %q", body)
	}
}

// D1100: the writer's guide names kb_guide; the managed block, byte-pinned,
// does not.
func TestRenderGuide_NamesKBGuideAndTheBlockDoesNot(t *testing.T) {
	g := provisioning.RenderGuide([]provisioning.GuideKB{fiveArchives()}, provisioning.GuideOptions{Routed: true})
	if !strings.Contains(g, "`kb_guide`") {
		t.Errorf("the guide does not name kb_guide:\n%s", g)
	}
	a := goldenBlock(t, func(string) {})
	if strings.Contains(string(a.Files[0].Content), "kb_guide") {
		t.Errorf("the managed block names kb_guide:\n%s", a.Files[0].Content)
	}
}

// D1100: kb_guide is read-only, so a read-only guide names it too.
func TestRenderGuide_ReadOnlyNamesKBGuide(t *testing.T) {
	g := provisioning.RenderGuide([]provisioning.GuideKB{fiveArchives()}, provisioning.GuideOptions{Routed: true, ReadOnly: true})
	if !strings.Contains(g, "`kb_guide`") {
		t.Errorf("the read-only guide does not name kb_guide:\n%s", g)
	}
	t.Logf("read-only operational section: %d bytes", len(provisioning.GuideOperational(true, nil)))
}

// What the server generates for an agent never tells it to read by sections as
// a habit, nor to follow a search hit to a section (D1185, D1354): a page is
// read whole unless it is genuinely large, and a runbook, a procedure or an
// incident always is, because ten small reads cost more than two whole ones.
// A hit names no section, so a text that says "the hit's section" is stale.
func TestGuidanceDoesNotPushSectionReads(t *testing.T) {
	texts := map[string]string{
		"guide(routed)":      provisioning.RenderGuide([]provisioning.GuideKB{fiveArchives()}, provisioning.GuideOptions{Routed: true}),
		"guide(read-only)":   provisioning.RenderGuide([]provisioning.GuideKB{fiveArchives()}, provisioning.GuideOptions{ReadOnly: true}),
		"operational":        provisioning.GuideOperational(false, nil),
		"shared operational": provisioning.SharedOperationalSection(),
	}
	err := fs.WalkDir(skillbundle.FS, "bundled", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		data, rerr := skillbundle.FS.ReadFile(path)
		texts[path] = string(data)
		return rerr
	})
	if err != nil {
		t.Fatal(err)
	}
	sectionRead := regexp.MustCompile("(?i)(`outline|`section|outline: true|section:)")
	wholeKinds := regexp.MustCompile("(?i)\\b(runbook|procedure|incident)s?\\b")
	hitSection := regexp.MustCompile("(?i)\\bhit('s)?\\b[^\\n]{0,40}\\bsection\\b")
	for name, text := range texts {
		for i, line := range strings.Split(text, "\n") {
			if hitSection.MatchString(line) {
				t.Errorf("%s:%d sends an agent from a search hit to a section, which a hit no longer names (D1354): %q", name, i+1, line)
			}
			if sectionRead.MatchString(line) && wholeKinds.MatchString(line) && !strings.Contains(strings.ToLower(line), "whole") && !strings.Contains(strings.ToLower(line), "never") {
				t.Errorf("%s:%d recommends reading by section where runbooks, procedures and incidents are read whole: %q", name, i+1, line)
			}
		}
	}
}
