package provisioning_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
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
		sectionBudget = 1640 // bytes: measured 1491, plus at most 10 %
		routingBudget = 338  // bytes: measured 308 (five archives), plus at most 10 %
	)
	section := provisioning.GuideOperational(false)
	t.Logf("operational section: %d bytes", len(section))
	if len(section) > sectionBudget || len(section) > 2048 {
		t.Errorf("the guide's operational section is %d bytes, over its budget of %d: it is paid by every handshake of every connection, trim it or raise the budget deliberately", len(section), sectionBudget)
	}
	full := provisioning.RenderGuide([]provisioning.GuideKB{fiveArchives()}, provisioning.GuideOptions{Routed: true})
	routing := strings.TrimSuffix(strings.TrimSuffix(full, section), "\n\n")
	t.Logf("routing paragraph: %d bytes", len(routing))
	if len(routing) > routingBudget || len(routing) > 512 {
		t.Errorf("a KB's routing paragraph is %d bytes, over its budget of %d: it is paid by every handshake of every connection, trim it or raise the budget deliberately", len(routing), routingBudget)
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
	if !strings.Contains(g, "No archives detected yet.") {
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
	if !strings.Contains(g, "No archives are visible to this connection.") {
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
