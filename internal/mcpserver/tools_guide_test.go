package mcpserver

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
)

// D1100: kb_guide serves the operating guide and the bundled procedures.

const kbSentence = "pass `kb:"

func guideServer(t *testing.T, bundle fs.FS) (*Server, *kb.KB) {
	t.Helper()
	k := setupTestKB(t)
	k.AuthName = "docs"
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: bundle})
	return s, k
}

func guideText(t *testing.T, res ToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("empty result")
	}
	return res.Content[0].Text
}

func TestKBGuide_GuideAndProcedures(t *testing.T) {
	t.Parallel()
	s, k := guideServer(t, skillbundle.FS)
	if err := os.WriteFile(filepath.Join(k.Root, "instructions.md"), []byte("Rules of docs.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := callTool(t, s, "kb_guide", `{}`)
	if res.IsError {
		t.Fatalf("kb_guide: %s", guideText(t, res))
	}
	text := guideText(t, res)
	for _, want := range []string{"Operational instructions:", "Rules of docs.", "Archives: manutenzione/", "## Procedures", "`ingest`", "`conflict-resolve`", "`import`"} {
		if !strings.Contains(text, want) {
			t.Errorf("guide lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, kbSentence) {
		t.Errorf("a per-KB endpoint must not carry the kb-argument sentence:\n%s", text)
	}
	if !strings.Contains(text, "`kb_guide`") {
		t.Errorf("the guide must name kb_guide:\n%s", text)
	}
	// An empty procedure with no file is the guide too.
	if got := guideText(t, callTool(t, s, "kb_guide", `{"procedure":""}`)); got != text {
		t.Errorf("empty procedure differs from the guide")
	}
	if got := guideText(t, callTool(t, s, "kb_guide", ``)); got != text {
		t.Errorf("no arguments differ from the guide")
	}
}

func TestKBGuide_Procedures(t *testing.T) {
	t.Parallel()
	s, _ := guideServer(t, skillbundle.FS)
	for _, tc := range []struct {
		short, full string
		note, refs  bool
	}{
		{"ingest", "cartographer-kb-ingest", true, true},
		{"conflict-resolve", "cartographer-kb-conflict-resolve", false, false},
		{"import", "cartographer-kb-import", true, false},
	} {
		p, _ := skillbundle.FindProcedure(tc.short)
		body, err := p.Body(skillbundle.FS)
		if err != nil {
			t.Fatal(err)
		}
		line := ""
		for _, l := range strings.Split(body, "\n") {
			if strings.HasPrefix(l, "## ") {
				line = l
				break
			}
		}
		for _, name := range []string{tc.short, tc.full} {
			args, _ := json.Marshal(map[string]string{"procedure": name})
			res := callTool(t, s, "kb_guide", string(args))
			if res.IsError {
				t.Fatalf("%s: %s", name, guideText(t, res))
			}
			text := guideText(t, res)
			if !strings.HasPrefix(text, procedurePreface) {
				t.Errorf("%s: no preface", name)
			}
			if line == "" || !strings.Contains(text, line) {
				t.Errorf("%s: body line %q missing", name, line)
			}
			if strings.HasPrefix(strings.TrimPrefix(text, procedurePreface), "---") || strings.Contains(text, "\nname: "+tc.full) {
				t.Errorf("%s: frontmatter served", name)
			}
			if got := strings.Contains(text, p.Note) && p.Note != ""; got != tc.note {
				t.Errorf("%s: note present = %v, want %v", name, got, tc.note)
			}
			if got := strings.HasSuffix(strings.TrimSpace(text), "and `file`."); got != tc.refs {
				t.Errorf("%s: reference line present = %v, want %v", name, got, tc.refs)
			}
		}
	}
}

func TestKBGuide_FilesAndRefusals(t *testing.T) {
	t.Parallel()
	s, _ := guideServer(t, skillbundle.FS)
	want, err := fs.ReadFile(skillbundle.FS, "bundled/cartographer-kb-ingest/references/source-kinds.md")
	if err != nil {
		t.Fatal(err)
	}
	res := callTool(t, s, "kb_guide", `{"procedure":"ingest","file":"references/source-kinds.md"}`)
	if res.IsError || guideText(t, res) != string(want) {
		t.Fatalf("reference not served verbatim: %+v", res.IsError)
	}
	for _, args := range []string{
		`{"procedure":"ingest","file":"../SKILL.md"}`,
		`{"procedure":"ingest","file":"SKILL.md"}`,
		`{"procedure":"ingest","file":"references/secrets.md"}`,
	} {
		res := callTool(t, s, "kb_guide", args)
		if !res.IsError || !strings.Contains(guideText(t, res), "references/source-kinds.md") {
			t.Errorf("%s: want isError listing the readable files, got %+v", args, res)
		}
	}
	if res := callTool(t, s, "kb_guide", `{"procedure":"conflict-resolve","file":"references/x.md"}`); !res.IsError || !strings.Contains(guideText(t, res), "no reference files") {
		t.Errorf("conflict-resolve file: %+v", res)
	}
	if res := callTool(t, s, "kb_guide", `{"file":"references/source-kinds.md"}`); !res.IsError || !strings.Contains(guideText(t, res), "procedure") {
		t.Errorf("file alone: %+v", res)
	}
	for _, name := range []string{"cartographer-kb-doctor", "nope", "kb-ingest"} {
		res := callTool(t, s, "kb_guide", `{"procedure":"`+name+`"}`)
		text := guideText(t, res)
		if !res.IsError || !strings.Contains(text, "ingest, conflict-resolve, import") {
			t.Errorf("%s: want unknown with the available names, got %q", name, text)
		}
	}
	if res := callTool(t, s, "kb_guide", `{"procedure":3}`); !res.IsError || !strings.Contains(guideText(t, res), "invalid params") {
		t.Errorf("non-string procedure: %+v", res)
	}
}

func TestKBGuide_NoBundle(t *testing.T) {
	t.Parallel()
	s, _ := guideServer(t, nil)
	text := guideText(t, callTool(t, s, "kb_guide", `{}`))
	if !strings.Contains(text, "Operational instructions:") || strings.Contains(text, "## Procedures") {
		t.Errorf("guide without a bundle:\n%s", text)
	}
	if res := callTool(t, s, "kb_guide", `{"procedure":"ingest"}`); !res.IsError || !strings.Contains(guideText(t, res), "no bundled procedures") {
		t.Errorf("procedure without a bundle: %+v", res)
	}
}

func TestKBGuide_MissingServedSkillIsLeftOut(t *testing.T) {
	t.Parallel()
	sub, err := fs.Sub(skillbundle.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := guideServer(t, hideFS{sub, "bundled/cartographer-kb-import/SKILL.md"})
	text := guideText(t, callTool(t, s, "kb_guide", `{}`))
	if strings.Contains(text, "`import`") || !strings.Contains(text, "`ingest`") {
		t.Errorf("procedures list:\n%s", text)
	}
	if res := callTool(t, s, "kb_guide", `{"procedure":"import"}`); !res.IsError {
		t.Errorf("a missing skill must be an error")
	}
}

// hideFS hides one file of the wrapped FS.
type hideFS struct {
	fs.FS
	hidden string
}

func (h hideFS) Open(name string) (fs.File, error) {
	if name == h.hidden {
		return nil, fs.ErrNotExist
	}
	return h.FS.Open(name)
}

func TestKBGuide_RoutedConnectionsScopeTheGuide(t *testing.T) {
	t.Parallel()
	f := newViewFixtureWithBundle(t, skillbundle.FS, nil, "kb-a", "work-kb")
	writeInstructions(t, f, "kb-a", "Rules of kb-a.\n")
	writeInstructions(t, f, "work-kb", "Secret rules of work-kb.\n")

	res := f.call(t, tokAdmin, RoutedMountPath, "kb_guide", map[string]any{"kb": "kb-a"})
	text := guideText(t, res)
	if res.IsError || !strings.Contains(text, "Rules of kb-a.") || strings.Contains(text, "work-kb") {
		t.Fatalf("two-KB connection must serve kb-a only:\n%s", text)
	}
	if !strings.Contains(text, kbSentence+" \"kb-a\"`") {
		t.Errorf("two-KB connection lacks the kb-argument sentence:\n%s", text)
	}

	// A one-KB connection: no kb argument, no sentence.
	text = guideText(t, f.call(t, tokAdmin, RoutedMountPath+"?kbs=kb-a", "kb_guide", map[string]any{}))
	if strings.Contains(text, kbSentence) || !strings.Contains(text, "Rules of kb-a.") {
		t.Errorf("one-KB connection:\n%s", text)
	}
}

func TestKBGuide_Authorization(t *testing.T) {
	t.Parallel()
	f := newViewFixtureWithBundle(t, skillbundle.FS, nil, "kb-a", "kb-b")
	root := f.kbs["kb-a"].Root
	if err := os.MkdirAll(filepath.Join(root, "skills", "my-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "my-skill", "SKILL.md"), []byte("---\nname: my-skill\ndescription: Use when testing.\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A read-only whole-KB token and the admin get the skills section.
	for _, tok := range []string{tokRO, tokAdmin} {
		res := f.call(t, tok, RoutedMountPath, "kb_guide", map[string]any{"kb": "kb-a"})
		if res.IsError || !strings.Contains(guideText(t, res), "## This KB's skills") || !strings.Contains(guideText(t, res), "`my-skill`") {
			t.Errorf("%s: %+v", tok, res)
		}
	}
	// A rule on another KB only: forbidden.
	if res := f.call(t, tokRO, RoutedMountPath+"?kbs=kb-a,kb-b", "kb_guide", map[string]any{"kb": "kb-b"}); !res.IsError {
		t.Errorf("a token with no grant on kb-b read it: %+v", res)
	}
	// A map-scoped principal: the guide and the procedures, no word of skills.
	res := f.call(t, tokMapRule, RoutedMountPath, "kb_guide", map[string]any{"kb": "kb-a"})
	text := guideText(t, res)
	if res.IsError || !strings.Contains(text, "## Procedures") || strings.Contains(text, "skills") || strings.Contains(text, "my-skill") {
		t.Errorf("map-scoped principal:\n%s", text)
	}
	// The read-only routed view lists it and serves it.
	rr := f.post(t, tokAdmin, RoutedMountPath+"?access=read", toolsListBody)
	names, _ := listNames(t, rr)
	found := false
	for _, n := range names {
		found = found || n == "kb_guide"
	}
	if !found {
		t.Errorf("read-only view does not list kb_guide: %v", names)
	}
	if res := f.call(t, tokAdmin, RoutedMountPath+"?access=read", "kb_guide", map[string]any{"kb": "kb-a"}); res.IsError {
		t.Errorf("read-only view refused kb_guide: %+v", res)
	}
}

func TestKBGuide_NoGrantIsForbidden(t *testing.T) {
	t.Parallel()
	s, _ := guideServer(t, skillbundle.FS)
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "other"}}})
	res := s.callTool(ctx, "kb_guide", json.RawMessage(`{}`))
	if !res.IsError || !strings.Contains(guideText(t, res), denyNoKBAccess) {
		t.Errorf("want %s: %+v", denyNoKBAccess, res)
	}
}

func TestKBGuide_KBSkillsAreFilteredCutAndSorted(t *testing.T) {
	t.Parallel()
	s, k := guideServer(t, skillbundle.FS)
	skills := filepath.Join(k.Root, "skills")
	write := func(dir, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(skills, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if content != "" {
			if err := os.WriteFile(filepath.Join(skills, dir, "SKILL.md"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	valid := func(name, desc string) string {
		return "---\nname: " + name + "\ndescription: " + desc + "\n---\nbody\n"
	}
	write("zeta", valid("zeta", "Use when z."))
	write("alpha", valid("alpha", strings.Repeat("é", 600)))
	write("broken", "") // no SKILL.md
	write("Bad_Name", valid("Bad_Name", "outside the slug pattern"))
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte(valid("linked", "from outside")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(skills, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	text := guideText(t, callTool(t, s, "kb_guide", `{}`))
	i := strings.Index(text, "## This KB's skills")
	if i < 0 {
		t.Fatalf("no skills section:\n%s", text)
	}
	section := text[i:]
	for _, bad := range []string{"broken", "Bad_Name", "linked", "outside"} {
		if strings.Contains(section, bad) {
			t.Errorf("section lists %q:\n%s", bad, section)
		}
	}
	a, z := strings.Index(section, "`alpha`"), strings.Index(section, "`zeta`")
	if a < 0 || z < a {
		t.Errorf("skills missing or unsorted:\n%s", section)
	}
	if n := strings.Count(section, "é"); n != guideSkillDescMax {
		t.Errorf("description cut to %d runes, want %d", n, guideSkillDescMax)
	}
}

func TestKBGuide_PointersAndDescription(t *testing.T) {
	t.Parallel()
	s, _ := guideServer(t, skillbundle.FS)
	for _, name := range []string{"atlas_overview", "kb_status", "kb_guide"} {
		tool, ok := s.Tools()[name]
		if !ok || !strings.Contains(tool.Description, "kb_guide") && name != "kb_guide" {
			t.Errorf("%s does not point to kb_guide", name)
		}
	}
	if d := s.Tools()["kb_guide"].Description; len([]rune(d)) > 600 {
		t.Errorf("kb_guide description is %d chars", len([]rune(d)))
	}
}

func TestKBGuide_RefusesOldAndNotServedNames(t *testing.T) {
	t.Parallel()
	s, _ := guideServer(t, skillbundle.FS)
	for _, name := range []string{"kb-ingest", "kb-conflict-resolve", "kb-import", "cartographer-kb-create", "cartographer-kb-doctor", "cartographer-ops", "create", "doctor"} {
		res := callTool(t, s, "kb_guide", `{"procedure":"`+name+`"}`)
		if !res.IsError || !strings.Contains(guideText(t, res), "ingest, conflict-resolve, import") {
			t.Errorf("%s: want refusal listing the served names, got %+v", name, res)
		}
	}
}
