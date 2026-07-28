package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

const testTemplate = "---\ntype: Runbook\ntitle: {{title}}\ntags: [{{tag}}, stable]\n---\n# Procedure\n\nUse {{value}}.\n"

func TestTemplateArtifactLifecycleAndList(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	call := func(name string, args map[string]any) ToolResult {
		responses := runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, name, args)})
		return decodeToolResult(t, responses[1])
	}
	if tr := call("artifact_write", map[string]any{"path": "templates/runbook.md", "content": testTemplate}); tr.IsError {
		t.Fatalf("template write: %+v", tr.Content)
	}
	if tr := call("artifact_write", map[string]any{"path": "templates/alpha.md", "content": "---\ntype: Note\ntitle: Alpha\n---\n# Alpha\n"}); tr.IsError {
		t.Fatalf("second template write: %+v", tr.Content)
	}
	if tr := call("artifact_read", map[string]any{"path": "templates/runbook.md"}); tr.IsError || !containsText(tr, "Procedure") {
		t.Fatalf("template read: %+v", tr.Content)
	}
	tr := call("template_list", map[string]any{})
	if tr.IsError {
		t.Fatalf("template_list: %+v", tr.Content)
	}
	var listed []templateListEntry
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].Slug != "alpha" || listed[1].Slug != "runbook" || listed[1].Type != "Runbook" || strings.Join(listed[1].Vars, ",") != "tag,title,value" {
		t.Fatalf("template_list = %+v", listed)
	}
	tr = call("artifact_list", map[string]any{})
	if tr.IsError || !containsText(tr, `"kind": "template"`) {
		t.Fatalf("artifact_list: %+v", tr.Content)
	}
	var read struct {
		SHA256 string `json:"sha256"`
	}
	tr = call("artifact_read", map[string]any{"path": "templates/runbook.md"})
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &read); err != nil {
		t.Fatal(err)
	}
	if tr = call("artifact_delete", map[string]any{"path": "templates/runbook.md", "if_match": read.SHA256}); tr.IsError {
		t.Fatalf("template delete: %+v", tr.Content)
	}
}

func TestTemplateArtifactRejectsInvalidSyntax(t *testing.T) {
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	for name, content := range map[string]string{
		"missing-type": "---\ntitle: x\n---\nbody\n",
		"colon":        "---\ntype: Note\ntitle: {{repo:x}}\n---\nbody\n",
		"braces":       "---\ntype: Note\ntitle: x\n---\n{{broken}\n",
		"key":          "---\n{{title}}: x\ntype: Note\n---\nbody\n",
	} {
		tr := decodeToolResult(t, runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_write", map[string]any{"path": "templates/" + name + ".md", "content": content})})[1])
		if !tr.IsError {
			t.Errorf("%s: expected validation error", name)
		}
	}
	if _, err := classifyArtifactPath("templates/Bad.md"); err == nil {
		t.Fatal("invalid template slug accepted")
	}
}

func TestConceptNewRendersLiterallyAndUpdatesIndex(t *testing.T) {
	k := setupTestKB(t)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "runbook.md"), []byte(testTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	call := func(name string, args map[string]any) ToolResult {
		return decodeToolResult(t, runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, name, args)})[1])
	}
	value := "cash $5 {{not_reparsed}}: yes\nnew_key: no"
	tr := call("concept_new", map[string]any{"template": "runbook", "id": "notes/rendered", "vars": map[string]string{"title": "A: title\nnot_a_key", "tag": "a,b", "value": value}})
	if tr.IsError {
		t.Fatalf("concept_new: %+v", tr.Content)
	}
	data, err := k.ReadConcept(okf.ConceptID("notes/rendered"))
	if err != nil {
		t.Fatal(err)
	}
	fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := fm.Get("title"); got != "A: title\nnot_a_key" {
		t.Fatalf("safe title = %#v", got)
	}
	if _, exists := fm.Get("not_a_key"); exists {
		t.Fatal("newline injected a frontmatter key")
	}
	if got, _ := fm.Get("tags"); strings.Join(got.([]string), ",") != "a,b,stable" {
		t.Fatalf("safe list value = %#v", got)
	}
	if !strings.Contains(data.Body, value) {
		t.Fatalf("literal body value not preserved: %q", data.Body)
	}
	if tr = call("search", map[string]any{"query": "cash"}); tr.IsError || !containsText(tr, "notes/rendered") {
		t.Fatalf("immediate search: %+v", tr.Content)
	}
	if tr = call("concept_read", map[string]any{"id": "notes/rendered", "section": "Procedure"}); tr.IsError || !containsText(tr, "cash") {
		t.Fatalf("section read: %+v", tr.Content)
	}
	if tr = call("concept_new", map[string]any{"template": "runbook", "id": "notes/rendered", "vars": map[string]string{"title": "x", "tag": "x", "value": "x"}}); !tr.IsError || !containsText(tr, "concept_write") {
		t.Fatalf("existing target: %+v", tr.Content)
	}
	if tr = call("concept_new", map[string]any{"template": "runbook", "id": "notes/missing", "vars": map[string]string{"title": "x"}}); !tr.IsError || !containsText(tr, "missing") {
		t.Fatalf("missing vars: %+v", tr.Content)
	}
	if tr = call("concept_new", map[string]any{"template": "runbook", "id": "notes/extra", "vars": map[string]string{"title": "x", "tag": "x", "value": "x", "oops": "x"}}); !tr.IsError || !containsText(tr, "unexpected") {
		t.Fatalf("extra vars: %+v", tr.Content)
	}
	if tr = call("concept_new", map[string]any{"template": "runbook", "id": "notes/Bad", "vars": map[string]string{}}); !tr.IsError || !containsText(tr, "invalid") {
		t.Fatalf("invalid id: %+v", tr.Content)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "empty-vars.md"), []byte("---\ntype: Note\ntitle: Static\n---\n# Static\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tr = call("concept_new", map[string]any{"template": "empty-vars", "id": "notes/static"}); tr.IsError {
		t.Fatalf("no-vars template: %+v", tr.Content)
	}
}

func TestConceptNewRevalidatesOutOfBandTemplate(t *testing.T) {
	k := setupTestKB(t)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "bad.md"), []byte("---\ntype: Note\ntitle: {{repo:x}}\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	tr := decodeToolResult(t, runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "concept_new", map[string]any{"template": "bad", "id": "notes/x"})})[1])
	if !tr.IsError || !containsText(tr, "invalid") {
		t.Fatalf("out-of-band validation: %+v", tr.Content)
	}
}

func TestConceptNewMissingTemplateListsSortedAndTruncated(t *testing.T) {
	k := setupTestKB(t)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 21; i >= 0; i-- {
		name := fmt.Sprintf("template-%02d.md", i)
		if err := os.WriteFile(filepath.Join(k.Root, "templates", name), []byte("---\ntype: Note\ntitle: Static\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	tr := decodeToolResult(t, runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "concept_new", map[string]any{"template": "missing", "id": "notes/new"})})[1])
	if !tr.IsError || !containsText(tr, "template-00") || !containsText(tr, "template-19") || containsText(tr, "template-20") || !containsText(tr, "truncated:true") {
		t.Fatalf("missing template response: %+v", tr.Content)
	}
}
