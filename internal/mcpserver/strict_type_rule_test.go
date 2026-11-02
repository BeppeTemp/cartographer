package mcpserver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// strictRuleKB is a KB whose strict map jr admits only Note and already holds
// a page of an undeclared type (jr/bad, Topic), as a map narrowed after the
// fact does (#1314).
func strictRuleKB(t *testing.T) (*Server, *kb.KB) {
	t.Helper()
	s, k := conceptNewTemplateKB(t, map[string]string{"topic": timestampTemplate})
	if err := k.CreateMapWithContract("jr", "Journal", "journal", []string{"Note"}, kb.MapContract{}); err != nil {
		t.Fatal(err)
	}
	fm := newFM()
	fm.Set("type", "Topic")
	fm.Set("title", "Bad")
	if _, err := k.WriteConcept("jr/bad", fm, "# Bad\n\nold text\n", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	return s, k
}

func refusedForType(t *testing.T, res ToolResult, what string) {
	t.Helper()
	if !res.IsError {
		t.Fatalf("%s: an undeclared type must be refused", what)
	}
	for _, want := range []string{"not allowed in map jr (strict)", "declared types: Note", "kb_repair"} {
		if !strings.Contains(res.Content[0].Text, want) {
			t.Errorf("%s: refusal lacks %q: %s", what, want, res.Content[0].Text)
		}
	}
}

// One rule on every write tool (D1314): introducing an undeclared type is
// refused, a new page or a type change, and nothing is written.
func TestStrictTypeRule_RefusesAnIntroducedTypeOnEveryTool(t *testing.T) {
	t.Parallel()
	s, k := strictRuleKB(t)

	refusedForType(t, callTool(t, s, "concept_write", `{"id":"jr/task","frontmatter":{"type":"Task","title":"T"},"body":"# T\n"}`), "concept_write")
	refusedForType(t, callTool(t, s, "concept_new", `{"template":"topic","id":"jr/viatpl","vars":{"title":"T"}}`), "concept_new")
	patch := fmt.Sprintf(`{"id":"jr/bad","if_match":%q,"frontmatter":{"type":"Task"}}`, hashOf(t, k, "jr/bad"))
	refusedForType(t, callTool(t, s, "concept_patch", patch), "concept_patch type change")
	batchNew := `{"operations":[{"op":"write","id":"jr/task","frontmatter":{"type":"Task","title":"T"},"body":"# T\n"}]}`
	refusedForType(t, callTool(t, s, "concept_batch", batchNew), "concept_batch new page")
	batchPatch := fmt.Sprintf(`{"operations":[{"op":"patch","id":"jr/bad","if_match":%q,"frontmatter":{"type":"Task"}}]}`, hashOf(t, k, "jr/bad"))
	refusedForType(t, callTool(t, s, "concept_batch", batchPatch), "concept_batch type change")

	for _, id := range []string{"jr/task", "jr/viatpl"} {
		if _, err := k.ReadConcept(okf.ConceptID(id)); err == nil {
			t.Errorf("%s must not exist after a refusal", id)
		}
	}
	if d, _ := k.ReadConcept("jr/bad"); !strings.Contains(d.FrontmatterRaw, "Topic") {
		t.Errorf("a refused type change must leave the page: %s", d.FrontmatterRaw)
	}
}

// A write that leaves an existing page's type as it is is never refused for
// it, even if the type is already undeclared (the page is no worse).
func TestStrictTypeRule_NeverRefusesAPageThatKeepsItsType(t *testing.T) {
	t.Parallel()
	s, k := strictRuleKB(t)

	callOK(t, s, "concept_patch", fmt.Sprintf(`{"id":"jr/bad","if_match":%q,"old_string":"old text","new_string":"new text"}`, hashOf(t, k, "jr/bad")))
	callOK(t, s, "concept_batch", fmt.Sprintf(`{"operations":[{"op":"patch","id":"jr/bad","if_match":%q,"old_string":"new text","new_string":"newer text"}]}`, hashOf(t, k, "jr/bad")))
	callOK(t, s, "concept_write", fmt.Sprintf(`{"id":"jr/bad","if_match":%q,"frontmatter":{"type":"Topic","title":"Bad"},"body":"# Bad\n\nrewritten\n"}`, hashOf(t, k, "jr/bad")))
	// Moving the page to a declared type is the way out.
	callOK(t, s, "concept_patch", fmt.Sprintf(`{"id":"jr/bad","if_match":%q,"frontmatter":{"type":"Note"}}`, hashOf(t, k, "jr/bad")))
}
