package lint

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

const tplServer = "---\ntype: Host\ntitle: \"{{title}}\"\n" +
	"x-template.required_fields: [owner]\n" +
	"x-template.field_values.state: [up, down]\n" +
	"x-template.optional_sections: [Notes]\n" +
	"x-template.section_aliases.Purpose: [Objective, Scopo]\n" +
	"---\n# {{title}}\n\n## Purpose\n\n## Operations\n\n## Notes\n"

const tplServiceB = "---\ntype: Host\ntitle: \"{{title}}\"\nx-template.open_sections: true\n---\n# {{title}}\n\n## Role\n"

func runStrict(t *testing.T, pages map[string]string) []Finding {
	t.Helper()
	k := tempKB(t)
	writeFile(t, k.Root, "templates/server.md", tplServer)
	writeFile(t, k.Root, "templates/appliance.md", tplServiceB)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\ntemplates: [server, appliance]\nrequire_template: true\n---\n# Infra\n")
	for id, c := range pages {
		writeFile(t, k.DataRoot(), id+".md", c)
	}
	f, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func page(shape, extra, body string) string {
	s := "---\ntype: Host\ntitle: H\nowner: me\n"
	if shape != "" {
		s += "shape: " + shape + "\n"
	}
	return s + extra + "---\n# H\n\n" + body
}

func tplFinding(f []Finding, path, check string) *Finding {
	for i := range f {
		if f[i].Path == path && f[i].Check == check {
			return &f[i]
		}
	}
	return nil
}

func TestShapeIsNotAToolParam(t *testing.T) {
	if IsToolParamField("shape") {
		t.Fatal("shape must be a page field, not a tool parameter")
	}
	if !IsToolParamField("template") {
		t.Fatal("template is the tool parameter D352 avoids")
	}
}

func TestTemplateChecks(t *testing.T) {
	ok := "## Purpose\n\ntext\n\n## Operations\n\ntext\n"
	f := runStrict(t, map[string]string{
		"infra/good":      page("server", "state: up\n", ok),
		"infra/missing":   page("", "", ok), // type Host: templates/host.md does not exist, two candidates
		"infra/unknown":   page("nope", "", ok),
		"infra/badslug":   page("../x", "", ok),
		"infra/mismatch":  strings.Replace(page("server", "", ok), "type: Host", "type: Service", 1),
		"infra/nofield":   strings.Replace(page("server", "", ok), "owner: me\n", "", 1),
		"infra/badvalue":  page("server", "state: UP\n", ok),
		"infra/extra":     page("server", "", ok+"\n## Random section\n\nx\n"),
		"infra/alias":     page("server", "", "## Objective\n\ntext\n\n## Operations\n\ntext\n"),
		"infra/order":     page("server", "", "## Operations\n\nops\n\n## Random\n\nr\n\n## Purpose\n\nwhy\n"),
		"infra/lack":      page("server", "", "## Purpose\n\ntext\n"),
		"infra/open":      page("appliance", "", "## Role\n\ntext\n\n## Whatever I like\n\nfree\n"),
		"infra/closed":    page("server", "status: done\n", "## Random\n\nx\n"),
		"infra/optional":  page("server", "", ok), // Notes is optional: not missing
		"infra/fenced":    page("server", "", "```\n## Not a section\n```\n\n"+ok),
		"infra/typedflow": page("server", "", ok),
	})
	cases := []struct {
		id, check string
		want      bool
	}{
		{"good", "template_missing", false}, {"good", "template_extra_section", false}, {"good", "template_section_missing", false},
		{"good", "template_field_missing", false}, {"good", "template_field_value", false}, {"good", "template_section_order", false},
		{"missing", "template_missing", true},
		{"unknown", "template_unknown", true}, {"badslug", "template_unknown", true},
		{"mismatch", "template_type_mismatch", true},
		{"nofield", "template_field_missing", true},
		{"badvalue", "template_field_value", true},
		{"extra", "template_extra_section", true},
		{"alias", "template_section_alias", true}, {"alias", "template_section_missing", false},
		{"order", "template_section_order", true}, {"order", "template_extra_section", true},
		{"lack", "template_section_missing", true},
		{"open", "template_extra_section", false}, {"open", "template_section_missing", false},
		{"closed", "template_extra_section", false}, {"closed", "template_section_missing", false}, {"closed", "template_field_missing", false},
		{"optional", "template_section_missing", false},
		{"fenced", "template_extra_section", false},
	}
	for _, c := range cases {
		if got := tplFinding(f, "infra/"+c.id+".md", c.check) != nil; got != c.want {
			t.Errorf("%s %s: fired=%v want %v", c.id, c.check, got, c.want)
		}
	}
	// Two Host templates in one map are checked independently: the same extra
	// section is an error under one and free under the other.
	if tplFinding(f, "infra/open.md", "template_not_allowed") != nil {
		t.Error("appliance is in the map's templates")
	}
	// Severities and fixes.
	if m := tplFinding(f, "infra/lack.md", "template_section_missing"); m == nil || m.Severity != SevWarning {
		t.Errorf("section_missing under require_template is a warning: %+v", m)
	}
	if m := tplFinding(f, "infra/order.md", "template_section_order"); m == nil || m.Severity != SevInfo || m.Fix == nil || m.Fix.Kind != FixReorderSections {
		t.Errorf("order: %+v", m)
	} else if want := "Purpose\nOperations\nRandom"; m.Fix.To != want {
		t.Errorf("order fix lists %q, want %q (extras after the last template section)", m.Fix.To, want)
	}
	if m := tplFinding(f, "infra/alias.md", "template_section_alias"); m == nil || m.Fix == nil || m.Fix.Kind != FixRenameHeading || m.Fix.Field != "Objective" || m.Fix.To != "Purpose" {
		t.Errorf("alias: %+v", m)
	}
	if m := tplFinding(f, "infra/mismatch.md", "template_type_mismatch"); m == nil || m.Fix == nil || m.Fix.Field != "type" || m.Fix.To != "Host" {
		t.Errorf("mismatch: %+v", m)
	}
	if m := tplFinding(f, "infra/badvalue.md", "template_field_value"); m == nil || m.Fix == nil || m.Fix.To != "up" {
		t.Errorf("field value: %+v", m)
	}
	if m := tplFinding(f, "infra/missing.md", "template_missing"); m == nil || m.Fix != nil {
		t.Errorf("two candidates must not be fixed: %+v", m)
	}
	for _, x := range f {
		if strings.HasPrefix(x.Check, "template_") && x.Severity == SevError {
			t.Errorf("%s is an error: template checks never turn a gate red (D289)", x.Check)
		}
	}
}

func TestTemplateMissingFixWhenOneCandidate(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "templates/server.md", tplServer)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\ntemplates: [server]\nrequire_template: true\n---\n")
	writeFile(t, k.DataRoot(), "infra/a.md", page("", "", "## Purpose\n\nx\n\n## Operations\n\ny\n"))
	f, _ := Run(k, "", false)
	m := tplFinding(f, "infra/a.md", "template_missing")
	if m == nil || m.Fix == nil || m.Fix.Kind != FixSetValue || m.Fix.Field != "shape" || m.Fix.To != "server" {
		t.Fatalf("one candidate must be fixed: %+v", m)
	}
}

func TestTemplateNotAllowedAndDefault(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "templates/server.md", tplServer)
	writeFile(t, k.Root, "templates/appliance.md", tplServiceB)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\ntemplates: [server]\ndefault_template: server\nrequire_template: true\n---\n")
	ok := "## Purpose\n\nx\n\n## Operations\n\ny\n"
	writeFile(t, k.DataRoot(), "infra/a.md", page("appliance", "", "## Role\n\nr\n"))
	writeFile(t, k.DataRoot(), "infra/b.md", page("", "", ok)) // default_template resolves it by type
	f, _ := Run(k, "", false)
	if tplFinding(f, "infra/a.md", "template_not_allowed") == nil {
		t.Error("appliance is not one of the map's templates")
	}
	if tplFinding(f, "infra/b.md", "template_missing") != nil {
		t.Error("the default template binds a page of its type")
	}
}

func TestNoTemplateKeysNoTemplateFindings(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "templates/host.md", tplServer)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\n---\n")
	writeFile(t, k.DataRoot(), "infra/a.md", page("nope", "", "## Random\n"))
	f, _ := Run(k, "", false)
	for _, x := range f {
		if strings.HasPrefix(x.Check, "template_") {
			t.Errorf("a map with no template key reported %s", x.Check)
		}
	}
}

func TestTemplateSectionsKeepsItsMeaning(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "templates/host.md", tplServer)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\ntemplate_sections: true\n---\n")
	writeFile(t, k.DataRoot(), "infra/a.md", page("", "", "## Purpose\n\nx\n"))
	f, _ := Run(k, "", false)
	m := tplFinding(f, "infra/a.md", "template_section_missing")
	if m == nil || m.Severity != SevInfo || !strings.Contains(m.Message, "Operations") || strings.Contains(m.Message, "Notes") {
		t.Fatalf("template_sections: true gives an info naming the required sections only: %+v", m)
	}
	for _, x := range f {
		if x.Check == "template_missing" || x.Check == "template_extra_section" {
			t.Errorf("template_sections alone must not enforce the schema: %s", x.Check)
		}
	}
}

func TestMapWithoutTemplatesAndSubsetCheck(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "bare/_map.md", "---\ntype: Map\nkind: map\ntitle: Bare\n---\n")
	for _, n := range []string{"a", "b", "c"} {
		writeFile(t, k.DataRoot(), "bare/"+n+".md", "---\ntype: Note\ntitle: "+n+"\n---\n# "+n+"\n")
	}
	writeFile(t, k.DataRoot(), "small/_map.md", "---\ntype: Map\nkind: map\ntitle: Small\n---\n")
	writeFile(t, k.DataRoot(), "small/a.md", "---\ntype: Note\ntitle: a\n---\n# a\n")
	// A template whose values leave the map's.
	writeFile(t, k.Root, "templates/server.md", tplServer)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\ntemplates: [server]\nfield_values.state: [up]\n---\n")
	f, _ := Run(k, "", false)
	if tplFinding(f, "bare/_map.md", "map_without_templates") == nil {
		t.Error("a map of 3 pages with no templates is reported")
	}
	if tplFinding(f, "small/_map.md", "map_without_templates") != nil {
		t.Error("a map of 1 page is not")
	}
	if tplFinding(f, "infra/_map.md", "contract_malformed") == nil {
		t.Error("a template allowing a value the map does not is contract_malformed")
	}
}

func TestReorderSections(t *testing.T) {
	body := "Intro text.\n\n## B\n\nbeta\n\n```\n## C fenced\n```\n\n## Extra\n\nx\n\n## A\n\nalpha\n"
	got, ok := ReorderSections(body, []string{"A", "B", "Extra"})
	want := "Intro text.\n\n## A\n\nalpha\n\n## B\n\nbeta\n\n```\n## C fenced\n```\n\n## Extra\n\nx\n"
	if !ok || got != want {
		t.Fatalf("got %q ok=%v\nwant %q", got, ok, want)
	}
	if again, ok := ReorderSections(got, []string{"A", "B", "Extra"}); ok || again != got {
		t.Fatal("a second pass must be a no-op")
	}
	if _, ok := ReorderSections(body, []string{"A", "Renamed", "Extra"}); ok {
		t.Fatal("headings that are no longer the listed ones must leave the page alone")
	}
	renamed, ok := RenameHeading("## Objective\n\nx\n\n```\n## Objective\n```\n", "Objective", "Purpose")
	if !ok || renamed != "## Purpose\n\nx\n\n```\n## Objective\n```\n" {
		t.Fatalf("rename touched a fenced heading or missed: %q", renamed)
	}
	if _, ok := RenameHeading(renamed, "Objective", "Purpose"); ok {
		t.Fatal("rename must be idempotent")
	}
}

func TestTemplateChecksOnWriteAgreeWithRun(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "templates/server.md", tplServer)
	writeFile(t, k.Root, "templates/appliance.md", tplServiceB)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\ntemplates: [server, appliance]\nrequire_template: true\n---\n")
	pages := map[string]string{
		"infra/a": page("", "", "## Purpose\n\nx\n"),
		"infra/b": page("server", "state: UP\n", "## Operations\n\nx\n\n## Objective\n\ny\n\n## Odd\n"),
		"infra/c": page("nope", "", "## Role\n"),
	}
	for id, c := range pages {
		writeFile(t, k.DataRoot(), id+".md", c)
	}
	f, _ := Run(k, "", false)
	seen := map[string]bool{}
	for id, c := range pages {
		var want, got []string
		for _, x := range f {
			if s, ok := Spec(x.Check); ok && x.Path == okf.IDToPath(okf.ConceptID(id)) && s.Level == LevelConcept && s.OnWrite && strings.HasPrefix(x.Check, "template_") {
				want = append(want, x.Check+"|"+x.Message)
			}
		}
		for _, x := range CheckConcept(k, okf.ConceptID(id), c) {
			if strings.HasPrefix(x.Check, "template_") {
				got = append(got, x.Check+"|"+x.Message)
				seen[x.Check] = true
			}
		}
		sort.Strings(want)
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: CheckConcept %q != Run %q", id, got, want)
		}
	}
	for _, c := range []string{"template_missing", "template_unknown", "template_field_value", "template_section_alias", "template_section_order", "template_extra_section"} {
		if !seen[c] {
			t.Errorf("the fixture never produced %s on the write path", c)
		}
	}
}
