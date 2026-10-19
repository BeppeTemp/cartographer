package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// fencedSampleTemplate is guidance prose followed by a fenced markdown sample:
// the shape templateH2 reads its sections from.
const fencedSampleTemplate = "---\ntype: Topic\ntitle: \"{{title}}\"\n---\n" +
	"Fill each section from the sources.\nKeep it short.\n\n" +
	"```markdown\n# {{title}}\n\n## Context\n\n## Decisions\n```\n\n" +
	"Link related pages at the end.\n"

// conceptNewTemplateKB returns a KB whose map notes/ is strict over the given
// templates (slug → file content), served by a registered server.
func conceptNewTemplateKB(t *testing.T, templates map[string]string) (*Server, *kb.KB) {
	t.Helper()
	k := setupTestKB(t)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	slugs := make([]string, 0, len(templates))
	for slug, content := range templates {
		if err := os.WriteFile(filepath.Join(k.Root, "templates", slug+".md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		slugs = append(slugs, slug)
	}
	if err := k.CreateMapWithContract("notes", "Notes", "map", nil, kb.MapContract{Templates: slugs}); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return s, k
}

// conceptNewFindingChecks is the set of finding checks a concept_new response carries.
func conceptNewFindingChecks(t *testing.T, res ToolResult) []string {
	t.Helper()
	var out struct {
		Findings []struct {
			Check string `json:"check"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("decode concept_new response: %v\n%s", err, res.Content[0].Text)
	}
	checks := []string{}
	for _, f := range out.Findings {
		checks = append(checks, f.Check)
	}
	return checks
}

func newPageBody(t *testing.T, k *kb.KB, id string) string {
	t.Helper()
	cd, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		t.Fatal(err)
	}
	return cd.Body
}

func TestConceptNewRendersTheFencedSample(t *testing.T) {
	t.Parallel()
	s, k := conceptNewTemplateKB(t, map[string]string{"topic": fencedSampleTemplate})
	res := callTool(t, s, "concept_new", `{"template":"topic","id":"notes/x","vars":{"title":"Q3 plan"}}`)
	if res.IsError {
		t.Fatalf("concept_new: %+v", res.Content)
	}
	body := newPageBody(t, k, "notes/x")
	if want := "# Q3 plan\n\n## Context\n\n## Decisions"; !strings.Contains(body, want) {
		t.Errorf("body does not carry the sample, got:\n%s", body)
	}
	for _, gone := range []string{"Fill each section", "Link related pages", "```"} {
		if strings.Contains(body, gone) {
			t.Errorf("guidance %q reached the page:\n%s", gone, body)
		}
	}
	if got, want := kb.H2Headings(body), []string{"Context", "Decisions"}; !reflect.DeepEqual(got, want) {
		t.Errorf("page sections = %v, want %v", got, want)
	}
	for _, check := range conceptNewFindingChecks(t, res) {
		if check == "template_section_missing" {
			t.Errorf("a page made from its template reports template_section_missing: %s", res.Content[0].Text)
		}
	}
}

func TestConceptNewFollowsTemplateH2OnWhichFenceIsTheSample(t *testing.T) {
	t.Parallel()
	const (
		prose = "---\ntype: Topic\n---\nGuidance.\n\n"
		// Sections outside the fence: the fence is an example, not the template.
		outside = prose + "## Real\n\n```markdown\n## Example\n```\n"
		// A fence with no section in it does not qualify.
		noSection = prose + "```sh\necho hi\n```\n"
		// Several fences: the first one is the sample, as templateH2 reads it.
		several = prose + "```markdown\n## First\n```\n\n```markdown\n## Second\n```\n"
	)
	s, k := conceptNewTemplateKB(t, map[string]string{"outside": outside, "no-section": noSection, "several": several})
	for id, want := range map[string]string{
		"outside":    "Guidance.\n\n## Real\n\n```markdown\n## Example\n```\n",
		"no-section": "Guidance.\n\n```sh\necho hi\n```\n",
		"several":    "## First\n",
	} {
		if res := callTool(t, s, "concept_new", `{"template":"`+id+`","id":"notes/`+id+`"}`); res.IsError {
			t.Fatalf("concept_new %s: %+v", id, res.Content)
		}
		if got := newPageBody(t, k, "notes/"+id); strings.TrimSpace(got) != strings.TrimSpace(want) {
			t.Errorf("%s: body = %q, want %q", id, got, want)
		}
	}
}

func TestConceptNewKeepsATemplateWithoutFenceVerbatim(t *testing.T) {
	t.Parallel()
	const tpl = "---\ntype: Topic\ntitle: \"{{title}}\"\n---\n# {{title}}\n\nHint: replace this.\n\n## Context\n\n## Decisions\n"
	s, k := conceptNewTemplateKB(t, map[string]string{"topic": tpl})
	if res := callTool(t, s, "concept_new", `{"template":"topic","id":"notes/x","vars":{"title":"Q3 plan"}}`); res.IsError {
		t.Fatalf("concept_new: %+v", res.Content)
	}
	if got, want := strings.TrimSpace(newPageBody(t, k, "notes/x")), "# Q3 plan\n\nHint: replace this.\n\n## Context\n\n## Decisions"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// A placeholder that only the dropped guidance mentions is no variable of the
// page: it is neither required nor accepted, and template_list does not offer it.
func TestConceptNewVarsComeFromTheRenderedSample(t *testing.T) {
	t.Parallel()
	const tpl = "---\ntype: Topic\ntitle: \"{{title}}\"\n---\n" +
		"Write {{hint}} in the sample.\n\n```markdown\n# {{title}}\n\n## Context\n```\n"
	s, k := conceptNewTemplateKB(t, map[string]string{"topic": tpl})
	res := callTool(t, s, "concept_new", `{"template":"topic","id":"notes/x","vars":{"title":"T"}}`)
	if res.IsError {
		t.Fatalf("a placeholder only the guidance mentions was required: %+v", res.Content)
	}
	if got := newPageBody(t, k, "notes/x"); strings.Contains(got, "hint") {
		t.Errorf("guidance reached the page: %q", got)
	}
	res = callTool(t, s, "concept_new", `{"template":"topic","id":"notes/y","vars":{"title":"T","hint":"h"}}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "unexpected template vars: hint") {
		t.Errorf("a guidance-only placeholder is not a var: %+v", res)
	}
	listed := callTool(t, s, "template_list", `{}`)
	var entries []templateListEntry
	if err := json.Unmarshal([]byte(listed.Content[0].Text), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || strings.Join(entries[0].Vars, ",") != "title" {
		t.Errorf("template_list vars = %+v, want [title]", entries)
	}
}
