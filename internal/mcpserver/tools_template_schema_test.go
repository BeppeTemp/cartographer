package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

const schemaTemplate = "---\ntype: Host\ntitle: \"{{title}}\"\n" +
	"x-template.description: A machine\n" +
	"x-template.required_fields: [owner]\n" +
	"x-template.field_values.state: [up, down]\n" +
	"x-template.optional_sections: [Notes]\n" +
	"x-template.section_aliases.Purpose: [Objective]\n" +
	"---\n# {{title}}\n\n## Purpose\n\n## Notes\n"

func TestTemplateSlugHelperAgreesWithArtifactSlug(t *testing.T) {
	for _, s := range []string{"a", "host", "host-v2", "a--b", "x1", "", "A", "a_b", "a/b", "..", "a-", "-a", "a b", "é", "a.b"} {
		if kb.ValidTemplateSlug(s) != artifactSlugPattern.MatchString(s) {
			t.Errorf("slug %q: kb says %v, artifactSlugPattern says %v", s, kb.ValidTemplateSlug(s), artifactSlugPattern.MatchString(s))
		}
	}
}

func TestValidateTemplateArtifactMetadata(t *testing.T) {
	if _, _, vars, err := validateTemplateArtifact([]byte(schemaTemplate)); err != nil || strings.Join(vars, ",") != "title" {
		t.Fatalf("a schema template is valid and its metadata is no variable: %v %v", err, vars)
	}
	head := "---\ntype: Host\ntitle: x\n"
	body := "---\n# x\n\n## Purpose\n\n## Notes\n"
	for name, meta := range map[string]string{
		"unknown key":          "x-template.bogus: [a]\n",
		"scalar for list":      "x-template.required_fields: owner\n",
		"empty list":           "x-template.optional_fields: []\n",
		"placeholder":          "x-template.description: use {{x}}\n",
		"bad open":             "x-template.open_sections: maybe\n",
		"optional not section": "x-template.optional_sections: [Nowhere]\n",
		"alias of nothing":     "x-template.section_aliases.Nowhere: [A]\n",
		"alias is a section":   "x-template.section_aliases.Purpose: [Notes]\n",
		"alias twice":          "x-template.section_aliases.Purpose: [Aim]\nx-template.section_aliases.Notes: [Aim]\n",
		"dotted field":         "x-template.field_values.a.b: [x]\n",
		"empty description":    "x-template.description: \n",
	} {
		if _, _, _, err := validateTemplateArtifact([]byte(head + meta + body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTemplateListCarriesTheSchema(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	if res := callTool(t, s, "artifact_write", `{"path":"templates/server.md","content":`+jsonString(schemaTemplate)+`}`); res.IsError {
		t.Fatalf("write: %+v", res.Content)
	}
	res := callTool(t, s, "template_list", `{}`)
	var listed []templateListEntry
	if err := json.Unmarshal([]byte(res.Content[0].Text), &listed); err != nil || len(listed) != 1 {
		t.Fatalf("%v %s", err, res.Content[0].Text)
	}
	e := listed[0]
	if e.Description != "A machine" || strings.Join(e.Sections, ",") != "Purpose,Notes" || strings.Join(e.OptionalSections, ",") != "Notes" ||
		strings.Join(e.RequiredFields, ",") != "owner" || strings.Join(e.FieldValues["state"], ",") != "up,down" || strings.Join(e.SectionAliases["Purpose"], ",") != "Objective" {
		t.Fatalf("entry = %+v", e)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestConceptNewStampsShapeAndStripsSchema(t *testing.T) {
	k := setupTestKB(t)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A template carrying its own shape: the caller's slug wins.
	tpl := strings.Replace(schemaTemplate, "type: Host\n", "type: Host\nshape: other\nowner: me\nstate: up\n", 1)
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "server.md"), []byte(tpl+"\n## Operations\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CreateMapWithContract("infra", "Infra", "map", nil, "", kb.MapContract{Templates: []string{"server"}, RequireTemplate: true}), error(nil); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	res := callTool(t, s, "concept_new", `{"template":"server","id":"infra/box","vars":{"title":"Box"}}`)
	if res.IsError {
		t.Fatalf("concept_new: %+v", res.Content)
	}
	cd, err := k.ReadConcept("infra/box")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cd.FrontmatterRaw, "x-template.") {
		t.Fatalf("template metadata reached the page:\n%s", cd.FrontmatterRaw)
	}
	if !strings.Contains(cd.FrontmatterRaw, "shape: server") || strings.Contains(cd.FrontmatterRaw, "shape: other") {
		t.Fatalf("shape not stamped with the template's slug:\n%s", cd.FrontmatterRaw)
	}
	for _, f := range lint.CheckConcept(k, "infra/box", "---\n"+cd.FrontmatterRaw+"\n---\n"+cd.Body) {
		if strings.HasPrefix(f.Check, "template_") && f.Check != "template_section_missing" {
			t.Errorf("a page made from its template has a template finding: %s %s", f.Check, f.Message)
		}
	}
	// A template the map does not list is not refused; the response says so.
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "appliance.md"), []byte("---\ntype: Host\ntitle: \"{{title}}\"\n---\n# {{title}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = callTool(t, s, "concept_new", `{"template":"appliance","id":"infra/other","vars":{"title":"Other"}}`)
	if res.IsError || !strings.Contains(res.Content[0].Text, "template_not_allowed") {
		t.Fatalf("a template outside the map's list is reported, not refused: %+v", res)
	}
}

func TestMapCreateAndUpdateTemplateKeys(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	if res := callTool(t, s, "map_create", `{"name":"infra","title":"Infra","templates":["server","runbook"],"default_template":"server"}`); res.IsError {
		t.Fatalf("map_create: %+v", res.Content)
	}
	c, err := k.ReadMapContract("infra")
	if err != nil || !c.RequireTemplate || c.DefaultTemplate != "server" || strings.Join(c.Templates, ",") != "runbook,server" {
		t.Fatalf("a map created with templates starts strict: %v %+v", err, c)
	}
	if res := callTool(t, s, "map_create", `{"name":"bad","title":"Bad","templates":["a"],"default_template":"b"}`); !res.IsError {
		t.Fatal("a default outside the list must be refused")
	}
	if res := callTool(t, s, "map_create", `{"name":"bad2","title":"Bad","templates":["../x"]}`); !res.IsError {
		t.Fatal("a path is not a template slug")
	}
	res := callTool(t, s, "map_update", `{"map":"infra","require_template":false,"templates":["server"],"default_template":"server"}`)
	if res.IsError {
		t.Fatalf("map_update: %+v", res.Content)
	}
	if !strings.Contains(res.Content[0].Text, `"require_template": false`) || !strings.Contains(res.Content[0].Text, `"default_template": "server"`) {
		t.Fatalf("the contract echo lacks the template keys: %s", res.Content[0].Text)
	}
	if res := callTool(t, s, "map_update", `{"map":"infra","templates":["runbook"]}`); !res.IsError {
		t.Fatal("dropping the default's template from the list must be refused")
	}
	if res := callTool(t, s, "map_update", `{"map":"infra","templates":[]}`); res.IsError == false {
		t.Fatal("removing the list while default_template stays must be refused")
	}
}

func TestTemplateOnlyArtifactWrite(t *testing.T) {
	k := setupTestKB(t)
	k.AllowTemplateWrite = true // allow_artifact_write stays off
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	content := jsonString("---\ntype: Note\ntitle: x\n---\n# x\n")
	if res := callTool(t, s, "artifact_write", `{"path":"templates/x.md","content":`+content+`}`); res.IsError {
		t.Fatalf("a template is writable: %+v", res.Content)
	}
	for _, path := range []string{"skills/s/SKILL.md", "instructions.md", "paths.yaml", "agents/a.md"} {
		res := callTool(t, s, "artifact_write", `{"path":"`+path+`","content":"x"}`)
		if !res.IsError || !strings.Contains(res.Content[0].Text, "only templates/ is writable on this KB") {
			t.Errorf("%s must be refused in template-only mode: %+v", path, res)
		}
	}
	res := callTool(t, s, "artifact_read", `{"path":"templates/x.md"}`)
	var read struct {
		SHA256 string `json:"sha256"`
	}
	_ = json.Unmarshal([]byte(res.Content[0].Text), &read)
	if res := callTool(t, s, "artifact_delete", `{"path":"instructions.md","if_match":"x"}`); !res.IsError || !strings.Contains(res.Content[0].Text, "only templates/") {
		t.Fatalf("artifact_delete outside templates/: %+v", res)
	}
	if res := callTool(t, s, "artifact_delete", `{"path":"templates/x.md","if_match":"`+read.SHA256+`"}`); res.IsError {
		t.Fatalf("a template is deletable: %+v", res.Content)
	}
	// The full right lifts the limit.
	k.AllowArtifactWrite = true
	s2 := New("test")
	RegisterKBTools(s2, k, Deps{})
	if res := callTool(t, s2, "artifact_write", `{"path":"hooks/h/run.sh","content":"echo hi\n"}`); res.IsError {
		t.Fatalf("allow_artifact_write unchanged: %+v", res.Content)
	}
}

func TestTemplateWriteOffRegistersNothing(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	if _, ok := s.Tools()["artifact_write"]; ok {
		t.Fatal("with neither right artifact_write is not registered")
	}
}

// The proposal kb_review builds is a template artifact_write accepts.
func TestTemplateProposalPassesValidation(t *testing.T) {
	k := setupTestKB(t)
	if err := k.CreateMapWithContract("infra", "Infra", "map", nil, "", kb.MapContract{}); err != nil {
		t.Fatal(err)
	}
	for i, n := range []string{"a", "b", "c", "d"} {
		body := "# H\n\n## Purpose\n\np\n\n## Operations: day two\n\no\n"
		if i == 0 {
			body += "\n## Notes\n\nn\n"
		}
		page := "---\ntype: Host\ntitle: " + n + "\nowner: me\nstate: up\n---\n" + body
		if err := os.WriteFile(filepath.Join(k.DataRoot(), "infra", n+".md"), []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	findings, err := lint.Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	items, err := lint.Review(k, findings)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, it := range items {
		if it.Kind != lint.ReviewTemplateProposal {
			continue
		}
		n++
		if _, _, _, err := validateTemplateArtifact([]byte(it.Template)); err != nil {
			t.Errorf("the proposed template is refused by artifact_write: %v\n%s", err, it.Template)
		}
	}
	if n != 1 {
		t.Fatalf("want one proposal, got %d", n)
	}
}

// The default auto_repair list converges a page the template checks fault: it
// is bound, its alias renamed and its sections reordered, in one write, with the
// extra section kept after the template's and no text written (D352).
func TestFixpointRepairsTemplateShape(t *testing.T) {
	k, _ := setupGitKB(t)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := "---\ntype: Host\ntitle: \"{{title}}\"\nx-template.section_aliases.Purpose: [Objective]\n---\n# {{title}}\n\n## Purpose\n\n## Operations\n"
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "server.md"), []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := k.CreateMapWithContract("infra", "Infra", "map", nil, "", kb.MapContract{Templates: []string{"server"}, RequireTemplate: true}); err != nil {
		t.Fatal(err)
	}
	page := "---\ntype: Host\ntitle: Box\n---\n# Box\n\nIntro line.\n\n## Operations\n\nops text\n\n## Odd one\n\nodd text\n\n## Objective\n\nwhy text\n\n```\n## Fenced\n```\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "infra", "box.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	k.AutoCommit = true
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	out, skip := repairConceptFixpoint(k, "infra/box", config.DefaultAutoRepair, nil)
	if skip != nil {
		t.Fatalf("skip = %+v", skip)
	}
	for _, c := range []string{"template_missing", "template_section_order", "template_section_alias"} {
		if out.Changed[c] == 0 {
			t.Errorf("%s was not applied: %+v", c, out.Changed)
		}
	}
	cd, err := k.ReadConcept("infra/box")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cd.FrontmatterRaw, "shape: server") {
		t.Errorf("shape not set:\n%s", cd.FrontmatterRaw)
	}
	wantOrder := []string{"## Purpose", "## Operations", "## Odd one"}
	last := -1
	for _, h := range wantOrder {
		i := strings.Index(cd.Body, "\n"+h+"\n")
		if i <= last {
			t.Errorf("%q out of order in:\n%s", h, cd.Body)
		}
		last = i
	}
	for _, text := range []string{"Intro line.", "ops text", "odd text", "why text", "```\n## Fenced\n```"} {
		if !strings.Contains(cd.Body, text) {
			t.Errorf("repair lost %q:\n%s", text, cd.Body)
		}
	}
	for _, f := range lint.CheckConcept(k, "infra/box", "---\n"+cd.FrontmatterRaw+"\n---\n"+cd.Body) {
		if f.Check == "template_section_alias" || f.Check == "template_section_order" || f.Check == "template_missing" {
			t.Errorf("still reported after the repair: %s", f.Check)
		}
	}
}
