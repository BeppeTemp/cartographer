package mcpserver

import (
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// pinToday fixes lint.Now, the injectable calendar day. The tests of this file
// therefore do not run in parallel: a parallel test reading lint.Now would race
// with the write.
func pinToday(t *testing.T, now time.Time) {
	t.Helper()
	prev := lint.Now
	lint.Now = func() time.Time { return now }
	t.Cleanup(func() { lint.Now = prev })
}

const timestampTemplate = "---\ntype: Topic\ntitle: \"{{title}}\"\ntimestamp: \"{{timestamp}}\"\n---\n# {{title}}\n\nCreated {{timestamp}}.\n"

func TestConceptNewTimestampDefaultsToTodayUTC(t *testing.T) {
	// 23:30 at UTC-5 is already the next day in UTC.
	pinToday(t, time.Date(2031, 2, 3, 23, 30, 0, 0, time.FixedZone("UTC-5", -5*3600)))
	s, k := conceptNewTemplateKB(t, map[string]string{"topic": timestampTemplate})
	res := callTool(t, s, "concept_new", `{"template":"topic","id":"notes/x","vars":{"title":"T"}}`)
	if res.IsError {
		t.Fatalf("concept_new without timestamp: %+v", res.Content)
	}
	cd, err := k.ReadConcept("notes/x")
	if err != nil {
		t.Fatal(err)
	}
	fm, err := okf.ParseFrontmatter(cd.FrontmatterRaw)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := fm.Get("timestamp"); got != "2031-02-04" {
		t.Errorf("frontmatter timestamp = %#v, want 2031-02-04", got)
	}
	if !strings.Contains(cd.Body, "Created 2031-02-04.") {
		t.Errorf("body does not carry the default timestamp: %q", cd.Body)
	}
}

func TestConceptNewExplicitTimestampWins(t *testing.T) {
	pinToday(t, time.Date(2031, 2, 3, 12, 0, 0, 0, time.UTC))
	s, k := conceptNewTemplateKB(t, map[string]string{"topic": timestampTemplate})
	res := callTool(t, s, "concept_new", `{"template":"topic","id":"notes/x","vars":{"title":"T","timestamp":"2020-01-02"}}`)
	if res.IsError {
		t.Fatalf("concept_new: %+v", res.Content)
	}
	cd, err := k.ReadConcept("notes/x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cd.FrontmatterRaw, "2020-01-02") || strings.Contains(cd.FrontmatterRaw, "2031") || !strings.Contains(cd.Body, "Created 2020-01-02.") {
		t.Errorf("the passed timestamp did not win:\n%s\n%s", cd.FrontmatterRaw, cd.Body)
	}
}

func TestConceptNewTimestampDefaultTouchesNothingElse(t *testing.T) {
	pinToday(t, time.Date(2031, 2, 3, 12, 0, 0, 0, time.UTC))
	s, k := conceptNewTemplateKB(t, map[string]string{
		"topic": timestampTemplate,
		"plain": "---\ntype: Topic\ntitle: \"{{title}}\"\n---\n# {{title}}\n",
	})
	// Only timestamp has a default: any other missing variable still fails.
	res := callTool(t, s, "concept_new", `{"template":"topic","id":"notes/a"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "missing template vars: title") || strings.Contains(res.Content[0].Text, "timestamp") {
		t.Errorf("missing title: %+v", res)
	}
	// A template without {{timestamp}} does not gain the variable.
	res = callTool(t, s, "concept_new", `{"template":"plain","id":"notes/b","vars":{"title":"B"}}`)
	if res.IsError {
		t.Fatalf("template without timestamp: %+v", res.Content)
	}
	if body := newPageBody(t, k, "notes/b"); strings.Contains(body, "2031") {
		t.Errorf("a timestamp leaked into a template that has none: %q", body)
	}
	res = callTool(t, s, "concept_new", `{"template":"plain","id":"notes/c","vars":{"title":"C","timestamp":"2020-01-02"}}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "unexpected template vars: timestamp") {
		t.Errorf("an unused timestamp stays unexpected: %+v", res)
	}
}
