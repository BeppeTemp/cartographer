package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

const testTemplate = "---\ntype: Runbook\ntitle: {{title}}\ntags: [{{tag}}, stable]\n---\n# Procedure\n\nUse {{value}}.\n"

func TestTemplateArtifactLifecycleAndList(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	for name, content := range map[string]string{
		"missing-type": "---\ntitle: x\n---\nbody\n",
		"colon":        "---\ntype: Note\ntitle: {{repo:x}}\n---\nbody\n",
		"braces":       "---\ntype: Note\ntitle: x\n---\n{{broken}\n",
		"key":          "---\n{{title}}: x\ntype: Note\n---\nbody\n",
		"raw":          "---\ntype: Note\nraw {{x}}\n---\nbody\n",
	} {
		tr := decodeToolResult(t, runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_write", map[string]any{"path": "templates/" + name + ".md", "content": content})})[1])
		if !tr.IsError {
			t.Errorf("%s: expected validation error", name)
		}
	}
	if _, err := classifyArtifactPath("templates/Bad.md"); err == nil {
		t.Fatal("invalid template slug accepted")
	}
	tr := decodeToolResult(t, runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "artifact_write", map[string]any{"path": "templates/block-list.md", "content": "---\ntype: Note\ntags:\n- {{tag}}\n---\nbody\n"})})[1])
	if tr.IsError {
		t.Fatalf("valid block-list placeholder rejected: %+v", tr.Content)
	}
}

func TestConceptNewRendersLiterallyAndUpdatesIndex(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestConceptNewRejectsOutOfBandRawFrontmatterPlaceholder(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "raw.md"), []byte("---\ntype: Note\nraw {{x}}\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	tr := decodeToolResult(t, runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "concept_new", map[string]any{"template": "raw", "id": "notes/raw"})})[1])
	if !tr.IsError || !containsText(tr, "unrecognized") {
		t.Fatalf("raw frontmatter validation: %+v", tr.Content)
	}
	if _, err := k.ReadConcept(okf.ConceptID("notes/raw")); !errors.Is(err, okf.ErrNotFound) {
		t.Fatalf("concept_new wrote a rejected raw template: %v", err)
	}
}

func TestConceptNewMissingTemplateListsSortedAndTruncated(t *testing.T) {
	t.Parallel()
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

func TestConceptNewListVars(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	templates := map[string]string{
		"whole":    "---\ntype: Note\ntitle: t\nroles: {{roles}}\n---\nbody\n",
		"flow":     "---\ntype: Note\ntitle: t\nroles: [owner, {{roles}}, last]\n---\nbody\n",
		"block":    "---\ntype: Note\ntitle: t\nroles:\n- owner\n- {{roles}}\n- last\n---\nbody\n",
		"single":   "---\ntype: Note\ntitle: t\nroles: [{{roles}}]\n---\nbody\n",
		"embedded": "---\ntype: Note\ntitle: Runbook {{roles}}\n---\nbody\n",
		"itemtext": "---\ntype: Note\ntitle: t\nroles: [x {{roles}}]\n---\nbody\n",
		"inbody":   "---\ntype: Note\ntitle: t\nroles: [x]\n---\nuse {{roles}}\n",
	}
	for name, content := range templates {
		if err := os.WriteFile(filepath.Join(k.Root, "templates", name+".md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	call := func(args map[string]any) ToolResult {
		return decodeToolResult(t, runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "concept_new", args)})[1])
	}
	roles := func(id string) []string {
		t.Helper()
		data, err := k.ReadConcept(okf.ConceptID(id))
		if err != nil {
			t.Fatal(err)
		}
		fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := fm.Get("roles")
		switch v := got.(type) {
		case []string:
			return v
		case nil:
			return nil
		}
		t.Fatalf("roles = %#v, not a list", got)
		return nil
	}
	ok := func(tpl, id string, vars map[string]any) []string {
		t.Helper()
		if tr := call(map[string]any{"template": tpl, "id": id, "vars": vars}); tr.IsError {
			t.Fatalf("%s: %+v", tpl, tr.Content)
		}
		return roles(id)
	}
	eq := func(got, want []string) {
		t.Helper()
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Fatalf("got %#v want %#v", got, want)
		}
	}
	eq(ok("whole", "n/whole", map[string]any{"roles": []string{"a", "b, c", "{{x}}"}}), []string{"a", "b, c", "{{x}}"})
	eq(ok("flow", "n/flow", map[string]any{"roles": []string{"a", "b"}}), []string{"owner", "a", "b", "last"})
	eq(ok("block", "n/block", map[string]any{"roles": []string{"a", "b"}}), []string{"owner", "a", "b", "last"})
	eq(ok("whole", "n/empty-whole", map[string]any{"roles": []string{}}), []string{})
	eq(ok("flow", "n/empty-flow", map[string]any{"roles": []string{}}), []string{"owner", "last"})
	// Regression: a string stays one scalar item.
	eq(ok("single", "n/string", map[string]any{"roles": "a, b"}), []string{"a, b"})

	for _, c := range []struct{ tpl, where string }{
		{"embedded", `frontmatter "title"`}, {"itemtext", `frontmatter "roles"`}, {"inbody", "body"},
	} {
		id := "n/bad-" + c.tpl
		tr := call(map[string]any{"template": c.tpl, "id": id, "vars": map[string]any{"roles": []string{"a"}}})
		if !tr.IsError || !containsText(tr, `"roles"`) || !containsText(tr, c.where) {
			t.Fatalf("%s: %+v", c.tpl, tr.Content)
		}
		if _, err := k.ReadConcept(okf.ConceptID(id)); !errors.Is(err, okf.ErrNotFound) {
			t.Fatalf("%s: concept written, err=%v", c.tpl, err)
		}
	}
	for _, bad := range []any{1, nil, []any{1}, []any{nil}, map[string]any{}} {
		tr := call(map[string]any{"template": "whole", "id": "n/invalid", "vars": map[string]any{"roles": bad}})
		if !tr.IsError || !containsText(tr, "invalid params") {
			t.Fatalf("%#v: %+v", bad, tr.Content)
		}
	}
	if tr := call(map[string]any{"template": "whole", "id": "n/m", "vars": map[string]any{}}); !tr.IsError || !containsText(tr, "missing") {
		t.Fatalf("missing: %+v", tr.Content)
	}
	if tr := call(map[string]any{"template": "whole", "id": "n/e", "vars": map[string]any{"roles": []string{"a"}, "oops": []string{"x"}}}); !tr.IsError || !containsText(tr, "unexpected") {
		t.Fatalf("extra: %+v", tr.Content)
	}
}
