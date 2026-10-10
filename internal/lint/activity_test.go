package lint

import (
	"strings"
	"testing"
)

func runTemplateMap(t *testing.T, mapFM string) (*testing.T, []Finding, map[string]string) {
	t.Helper()
	k := tempKB(t)
	writeFile(t, k.Root, "templates/server.md", tplServer)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: Infra\ntemplates: [server]\n"+mapFM+"---\n# Infra\n")
	writeFile(t, k.DataRoot(), "infra/n1.md", page("server", "", "## Purpose\n\ntext\n"))
	f, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return t, f, Inactive(k)
}

// D371: a map that declares templates is strict without saying so; the explicit
// require_template: false silences the template_* checks and the check reports
// itself inactive when no map is left to run them.
func TestTemplatesActiveByDefaultAndOptOut(t *testing.T) {
	_, f, inactive := runTemplateMap(t, "")
	if m := tplFinding(f, "infra/n1.md", "template_section_missing"); m == nil || m.Severity != SevWarning {
		t.Errorf("a map with templates is strict by default (section_missing is a warning): %+v", f)
	}
	if r, ok := inactive["template_missing"]; ok {
		t.Errorf("template_missing inactive on a strict map: %s", r)
	}

	_, f, inactive = runTemplateMap(t, "require_template: false\n")
	for _, c := range []string{"template_missing", "template_field_missing", "template_extra_section"} {
		if tplFinding(f, "infra/n1.md", c) != nil {
			t.Errorf("%s fires on a map that opted out: %+v", c, f)
		}
		if !strings.Contains(inactive[c], "require_template: false") {
			t.Errorf("%s inactive reason = %q", c, inactive[c])
		}
	}
	if tplFinding(f, "infra/n1.md", "template_section_missing") != nil {
		t.Errorf("template_section_missing fires on a map that opted out: %+v", f)
	}
	if _, ok := inactive["template_section_missing"]; !ok {
		t.Error("template_section_missing stays active after the opt-out only with template_sections: true")
	}

	_, f, inactive = runTemplateMap(t, "require_template: false\ntemplate_sections: true\n")
	if tplFinding(f, "infra/n1.md", "template_section_missing") == nil {
		t.Errorf("template_sections: true keeps the section check on: %+v", f)
	}
	if _, ok := inactive["template_section_missing"]; ok {
		t.Error("template_section_missing inactive despite template_sections: true")
	}
}

// D371: the checks with nothing to compare against say so, with a reason.
func TestInactiveReasons(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\nkind: map\ntitle: Ops\n---\n# Ops\n")
	writeFile(t, k.DataRoot(), "ops/a.md", "---\ntype: Note\ntitle: A\n---\n# A\n")
	inactive := Inactive(k)
	for _, c := range []string{"template_missing", "template_section_missing", "unknown_type", "stale_open", "forbidden_term", "source_uncited",
		"sops_missing_file", "legacy_path", "unknown_placeholder", "missing_required_field", "invalid_field_value", "forbidden_field", "index_incomplete", "index_stale"} {
		if inactive[c] == "" {
			t.Errorf("%s should be inactive on a bare KB", c)
		}
	}
	for _, c := range []string{"orphan", "broken_link", "closed_with_open_items", "title_quality", "value_case_variant", "sops_format_mismatch"} {
		if r, ok := inactive[c]; ok {
			t.Errorf("%s is always active, got inactive: %s", c, r)
		}
	}

	writeFile(t, k.Root, "glossary.yaml", "terms:\n  - canonical: Widget\n    forbidden: [gizmo]\n")
	writeFile(t, k.Root, "paths.yaml", "repos:\n  r: {}\n")
	writeFile(t, k.Root, "secrets/x.yaml", "a: b\n")
	writeFile(t, k.DataRoot(), "ops/_map.md", "---\ntype: Map\nkind: journal\ntitle: Ops\nrequired_fields: [owner]\nrequire_index_entry: true\n---\n# Ops\n")
	writeFile(t, k.DataRoot(), "ops/s.md", "---\ntype: Source\ntitle: S\ningest_status: pending\n---\n# S\n")
	inactive = Inactive(k)
	for _, c := range []string{"forbidden_term", "sops_missing_file", "stale_open", "missing_required_field", "index_incomplete", "source_uncited"} {
		if r, ok := inactive[c]; ok {
			t.Errorf("%s should be active once its input exists, got: %s", c, r)
		}
	}
}
