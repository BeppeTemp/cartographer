package skillbundle

import (
	"io/fs"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

var notServed = []string{"cartographer-feedback", "cartographer-kb-create", "cartographer-kb-doctor", "cartographer-ops"}

func TestProcedures_ServedEntriesMatchTheBundle(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, p := range Procedures() {
		data, err := fs.ReadFile(FS, "bundled/"+p.Skill+"/SKILL.md")
		if err != nil {
			t.Fatalf("%s: %v", p.Skill, err)
		}
		fm, _, has := okf.SplitFrontmatter(string(data))
		if !has || !strings.Contains(fm, "name: "+p.Skill) {
			t.Errorf("%s: frontmatter name does not equal the entry's skill", p.Skill)
		}
		if seen[p.Short] || p.Short != strings.TrimPrefix(p.Skill, "cartographer-kb-") {
			t.Errorf("short name %q is duplicated or not the skill name without its prefix", p.Short)
		}
		seen[p.Short] = true
	}
}

// A seventh bundled skill fails here until someone decides whether it is served.
func TestProcedures_EveryBundledSkillIsClassified(t *testing.T) {
	t.Parallel()
	entries, err := fs.ReadDir(FS, "bundled")
	if err != nil {
		t.Fatal(err)
	}
	servedSet := map[string]bool{}
	for _, p := range Procedures() {
		servedSet[p.Skill] = true
	}
	notSet := map[string]bool{}
	for _, n := range notServed {
		notSet[n] = true
		if servedSet[n] {
			t.Errorf("%s is both served and deliberately not served", n)
		}
	}
	for _, e := range entries {
		if e.IsDir() && !servedSet[e.Name()] && !notSet[e.Name()] {
			t.Errorf("bundled skill %s is neither served by kb_guide nor in the not-served list: decide (D1100)", e.Name())
		}
	}
}

// A CLI step added to a served skill later must be dealt with in its note.
func TestProcedures_CLIStepNeedsANote(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile("`cartographer [a-z]")
	for _, p := range Procedures() {
		body, err := p.Body(FS)
		if err != nil {
			t.Fatal(err)
		}
		if re.MatchString(body) && p.Note == "" {
			t.Errorf("%s runs a cartographer command but has no note for a client without a shell", p.Short)
		}
	}
}

func TestFindProcedure(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"ingest", "cartographer-kb-ingest", " INGEST ", "Cartographer-KB-Conflict-Resolve", "import"} {
		if _, ok := FindProcedure(in); !ok {
			t.Errorf("%q should resolve", in)
		}
	}
	for _, in := range []string{"kb-ingest", "cartographer-kb-doctor", "doctor", "cartographer-kb-create", ""} {
		if _, ok := FindProcedure(in); ok {
			t.Errorf("%q must not resolve", in)
		}
	}
}

func TestProcedure_Reference(t *testing.T) {
	t.Parallel()
	ingest, _ := FindProcedure("ingest")
	conflict, _ := FindProcedure("conflict-resolve")
	want, err := fs.ReadFile(FS, "bundled/cartographer-kb-ingest/references/source-kinds.md")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := ingest.Reference(FS, "references/source-kinds.md")
	if !ok || got != string(want) {
		t.Fatalf("reference not returned verbatim (ok=%v)", ok)
	}
	bad := []string{"", "/references/source-kinds.md", "../x", "./references/source-kinds.md", "references/../SKILL.md",
		"SKILL.md", "references", "references/", "references/missing.md", `references\source-kinds.md`, "references/secrets.md", "references/source-kinds.md\x00"}
	for _, f := range bad {
		if _, ok := ingest.Reference(FS, f); ok && f != "references/source-kinds.md" {
			t.Errorf("ingest accepted %q", f)
		}
		if _, ok := conflict.Reference(FS, f); ok {
			t.Errorf("conflict-resolve accepted %q", f)
		}
	}
	if got := ingest.ReferenceFiles(FS); !reflect.DeepEqual(got, []string{"references/source-kinds.md"}) {
		t.Errorf("ingest files = %v", got)
	}
	if got := conflict.ReferenceFiles(FS); len(got) != 0 {
		t.Errorf("conflict-resolve files = %v", got)
	}
}

func TestProcedure_Body(t *testing.T) {
	t.Parallel()
	for _, p := range Procedures() {
		body, err := p.Body(FS)
		if err != nil {
			t.Fatal(err)
		}
		head := body
		if len(head) > 400 {
			head = head[:400]
		}
		if strings.HasPrefix(body, "---") || strings.HasPrefix(body, "name:") {
			t.Errorf("%s: body starts with frontmatter:\n%s", p.Short, head)
		}
	}
	p, _ := FindProcedure("import")
	m := fstest.MapFS{"bundled/cartographer-kb-import/SKILL.md": {Data: []byte("# plain\nno frontmatter\n")}}
	if body, err := p.Body(m); err != nil || body != "# plain\nno frontmatter\n" {
		t.Errorf("no-frontmatter body = %q, %v", body, err)
	}
	if _, err := p.Body(fstest.MapFS{}); err == nil || !strings.Contains(err.Error(), "import") {
		t.Errorf("missing SKILL.md error = %v", err)
	}
}
