package docskb

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

const docs = "docs/" // concatenated below so the path gate does not read test data as a citation

var update = flag.Bool("update", false, "rewrite testdata/golden.txt")

// lowerCaps shrinks the caps so the small fixture exercises every split.
func lowerCaps(t *testing.T) {
	soft, hard := softCap, hardCap
	softCap, hardCap = 400, 800
	t.Cleanup(func() { softCap, hardCap = soft, hard })
}

// fixture copies testdata/src into a MapFS a test can edit.
func fixture(t *testing.T) fstest.MapFS {
	t.Helper()
	m := fstest.MapFS{}
	root := os.DirFS("testdata/src")
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(root, p)
		m[p] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func dump(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "=== %s ===\n%s\n", n, files[n])
	}
	return b.String()
}

func TestGolden(t *testing.T) {
	lowerCaps(t)
	files, err := Generate(fixture(t), "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	got := dump(files)
	golden := filepath.Join("testdata", "golden.txt")
	if *update {
		fmt.Print("@@@GOLDEN@@@\n" + got + "@@@END@@@\n")
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("generated tree differs from testdata/golden.txt (run: go test ./internal/docskb -args -update)")
	}
}

func TestShape(t *testing.T) {
	lowerCaps(t)
	files, err := Generate(fixture(t), "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"data/using-cartographer/readme/index.md", // root page, first of its map
		"data/using-cartographer/flat.md",         // no H2: a flat concept
		"data/using-cartographer/guide/install-v1-usage.md",
		"data/using-cartographer/guide/install-v1-usage-2.md", // same heading twice
		"data/using-cartographer/guide/section-5.md",          // no letters or digits
		"data/using-cartographer/guide/aux-section.md",        // Windows device name
		"data/internals-contributing/docs-map/index.md",
		"data/internals-contributing/contributing.md",
		"data/internals-contributing/brand-index/index.md",
		"data/decisions/d1-first-choice.md",
		"data/decisions/d2-second-choice.md",
		"data/decisions/index.md", "data/index.md", "instructions.md",
	} {
		if _, ok := files[p]; !ok {
			t.Errorf("missing %s", p)
		}
	}
	for p := range files {
		if strings.Contains(p, "d-new-x") || strings.Contains(p, "template") {
			t.Errorf("%s: a pending decision or the template was generated", p)
		}
	}
	idx := string(files["data/using-cartographer/guide/index.md"])
	if strings.Contains(idx, "this is a comment") {
		t.Errorf("a # line inside a fence became a heading:\n%s", idx)
	}
	if got := string(files["data/internals-contributing/docs-map/index.md"]); strings.Contains(got, "| File | Contents |") {
		t.Errorf("docs-map still holds an index table")
	}
	if got := string(files["data/internals-contributing/decisions.md"]) + string(files["data/internals-contributing/decisions/index.md"]); strings.Contains(got, "decisions:index") {
		t.Errorf("the generated decisions block was kept")
	}
}

func TestDeterministic(t *testing.T) {
	lowerCaps(t)
	a, err := Generate(fixture(t), "main")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		b, _ := Generate(fixture(t), "main")
		if dump(a) != dump(b) {
			t.Fatal("two runs on the same input differ")
		}
	}
	dir := t.TempDir()
	if err := WriteTree(a, dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "data", "using-cartographer", "flat.md"))
	if err != nil || !bytes.Equal(got, a["data/using-cartographer/flat.md"]) {
		t.Errorf("WriteTree wrote %q, %v", got, err)
	}
}

// TestSizeCaps generates a page the real caps must cut and checks that no
// section concept is above the hard cap except the named unsplittable block.
func TestSizeCaps(t *testing.T) {
	para := strings.Repeat("Words fill a paragraph until it takes room. ", 12) + "\n\n"
	var b strings.Builder
	b.WriteString("# Big\n\nLead.\n\n## Cut at blank lines\n\n")
	b.WriteString(strings.Repeat(para, 60))
	b.WriteString("## Cut at sub-headings\n\n")
	for _, h := range []string{"One", "Two", "Three", "Four"} {
		b.WriteString("### " + h + "\n\n" + strings.Repeat(para, 12))
	}
	b.WriteString("## Block\n\n" + strings.Repeat("A single paragraph with no blank line. ", 600) + "\n")
	m := fixture(t)
	m[docs+"guide.md"] = &fstest.MapFile{Data: []byte(b.String())}
	m[docs+"flat.md"] = &fstest.MapFile{Data: []byte("# A flat page\n\nNo links into the page above.\n")} // its fixture links name headings guide.md no longer has
	files, err := Generate(m, "main")
	if err != nil {
		t.Fatal(err)
	}
	unsplittable := map[string]bool{"data/using-cartographer/guide/block.md": true}
	sections := 0
	for p, c := range files {
		if !strings.HasPrefix(p, "data/using-cartographer/guide/") || strings.HasSuffix(p, "/index.md") {
			continue
		}
		sections++
		if len(c) > hardCap && !unsplittable[p] {
			t.Errorf("%s is %d bytes, above the %d cap, and is not a named unsplittable block", p, len(c), hardCap)
		}
	}
	if sections < 8 {
		t.Errorf("only %d section concepts for a 60 KiB page", sections)
	}
	if len(files["data/using-cartographer/guide/block.md"]) <= hardCap {
		t.Errorf("the unsplittable block is no longer above the cap: drop it from the list")
	}
}

func TestErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(fstest.MapFS)
		want string
	}{
		"page in no table": {func(m fstest.MapFS) { m[docs+"orphan.md"] = &fstest.MapFile{Data: []byte("# O\n")} }, docs + "orphan.md: a docs page in no table"},
		"bad row": {func(m fstest.MapFS) {
			m[docs+"index.md"].Data = bytes.Replace(m[docs+"index.md"].Data, []byte("[flat.md](flat.md)"), []byte("[flat](flat.md)"), 1)
		}, docs + "index.md:"},
		"listed, missing": {func(m fstest.MapFS) { delete(m, docs+"flat.md") }, docs + "flat.md: listed but unreadable"},
		"unknown topic": {func(m fstest.MapFS) {
			m[docs+"decisions/D1-first-choice.md"].Data = bytes.Replace(m[docs+"decisions/D1-first-choice.md"].Data, []byte("architecture"), []byte("nonsense"), 1)
		}, docs + "decisions/D1-first-choice.md: missing or unknown `topic:`"},
		"broken anchor": {func(m fstest.MapFS) {
			m[docs+"flat.md"].Data = append(m[docs+"flat.md"].Data, []byte("\nSee [nowhere](guide.md#no-such-heading).\n")...)
		}, docs + "flat.md:"},
		"bad decision name": {func(m fstest.MapFS) { m[docs+"decisions/notes.md"] = &fstest.MapFile{Data: []byte("x")} }, docs + "decisions/notes.md: name is neither"},
	} {
		t.Run(name, func(t *testing.T) {
			m := fixture(t)
			tc.edit(m)
			_, err := Generate(m, "main")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestSlugAndAnchor(t *testing.T) {
	for in, want := range map[string]string{
		"Workspace scope (D193)": "workspace-scope-d193",
		"  A & B  ":              "a-b",
		"???":                    "",
		"Café":                   "caf",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"Workspace scope (D193)": "workspace-scope-d193",
		"A & B":                  "a--b",
		"Café":                   "café",
	} {
		if got := githubAnchor(in); got != want {
			t.Errorf("githubAnchor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRef(t *testing.T) {
	for in, want := range map[string]string{
		"v0.29.0":            "v0.29.0",
		"v0.29.0-rc.1":       "v0.29.0-rc.1",
		"v0.28.0-5-gabc1234": "main",
		"0.29.0":             "main",
		"dev":                "main",
		"":                   "main",
	} {
		if got := Ref(in); got != want {
			t.Errorf("Ref(%q) = %q, want %q", in, got, want)
		}
	}
}
