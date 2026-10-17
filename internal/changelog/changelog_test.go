package changelog

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current output")

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The two fixtures are real: the 0.24.0 section of CHANGELOG.md (with the
// Migration note above it and the 0.23.0 section below), and the body of the
// 0.25.0 release PR, header and footer included. The golden files were read by
// a person; -update rewrites them after a deliberate change of the output.
func TestRegroupGolden(t *testing.T) {
	for _, name := range []string{"changelog", "release-pr-body"} {
		t.Run(name, func(t *testing.T) {
			in := read(t, filepath.Join("testdata", name+".md"))
			got := Regroup(in)
			golden := filepath.Join("testdata", name+".golden.md")
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if want := read(t, golden); got != want {
				t.Errorf("Regroup(%s) differs from %s; run: go test ./internal/changelog -run TestRegroupGolden -args -update", name, golden)
			}
			if got == in {
				t.Error("the fixture came back unchanged: nothing was regrouped")
			}
		})
	}
}

// The grouped text goes through Regroup again on the next push to main (the
// job runs after every one of them): it must not move.
func TestRegroupIsIdempotent(t *testing.T) {
	for _, name := range []string{"changelog", "release-pr-body"} {
		once := Regroup(read(t, filepath.Join("testdata", name+".md")))
		if twice := Regroup(once); twice != once {
			t.Errorf("%s: a second pass changed the output:\n%s", name, twice)
		}
	}
}

func TestRegroupSortsByAreaAndDropsDevelopment(t *testing.T) {
	got := Regroup(read(t, filepath.Join("testdata", "changelog.md")))

	section := got[strings.Index(got, "## [0.24.0]"):strings.Index(got, "## [0.23.0]")]
	order := []string{"### Atlas", "### MCP tools", "### Lint & repair", "### Client & CLI", "### General"}
	last := -1
	for _, h := range order {
		i := strings.Index(section, "\n"+h+"\n")
		if i < 0 {
			t.Fatalf("0.24.0 has no %q heading:\n%s", h, section)
		}
		if i < last {
			t.Errorf("%q is out of order", h)
		}
		last = i
	}

	// Each entry under the area its scope names, the scope prefix gone.
	for area, want := range map[string]string{
		"### Atlas":         "* a Secrets panel lists the KB's `secrets/*.sops.yaml` files",
		"### MCP tools":     "* record the calling agent client as an Agent commit trailer",
		"### Lint & repair": "* unknown_type is info outside maps that adopted types",
		"### Client & CLI":  "* workspace bind writes a Cartographer MCP entry",
	} {
		start := strings.Index(section, "\n"+area+"\n")
		rest := section[start+1:]
		if end := strings.Index(rest[len(area):], "\n### "); end >= 0 {
			rest = rest[:len(area)+end]
		}
		if !strings.Contains(rest, want) {
			t.Errorf("%q is not under %s", want, area)
		}
	}
	// 0.24.0 has no server-side entry (kb_status is MCP): the area is omitted.
	if strings.Contains(section, "### Server & KB") {
		t.Error("an empty area was written")
	}
	// kb_status is a synonym of the MCP area; webui and web are Atlas.
	if strings.Contains(section, "**kb_status:**") || strings.Contains(section, "**webui:**") {
		t.Error("a mapped scope prefix survived")
	}
	// Unscoped entries are General.
	general := section[strings.Index(section, "### General"):]
	if !strings.Contains(general, "* a write whose push failed (not a conflict) reports committed,") {
		t.Errorf("an unscoped entry is not under General:\n%s", general)
	}
	// Development entries are not in the changelog.
	if strings.Contains(section, "live sessions with make dev") {
		t.Error("a dev entry survived")
	}
}

// Everything the regrouper does not regroup is copied byte for byte: the text
// above the version section (the Migration note) and every later section.
func TestRegroupLeavesTheRestAlone(t *testing.T) {
	in := read(t, filepath.Join("testdata", "changelog.md"))
	got := Regroup(in)

	cut := func(s, from string) string { return s[strings.Index(s, from):] }
	if before := in[:strings.Index(in, "## [0.24.0]")]; !strings.HasPrefix(got, before) {
		t.Error("the Migration note changed")
	}
	if cut(in, "## [0.23.0]") != cut(got, "## [0.23.0]") {
		t.Error("the section below the first version changed")
	}

	body := read(t, filepath.Join("testdata", "release-pr-body.md"))
	grouped := Regroup(body)
	header := body[:strings.Index(body, "## [0.25.0]")]
	footer := body[strings.LastIndex(body, "---\n"):]
	if !strings.HasPrefix(grouped, header) || !strings.HasSuffix(grouped, footer) {
		t.Errorf("the release PR header or footer changed:\n%s", grouped)
	}
}

func TestRegroupKeepsOtherSectionsInPlace(t *testing.T) {
	in := strings.Join([]string{
		"## [1.0.0](https://example.com/compare/v0.9.0...v1.0.0) (2026-01-01)",
		"",
		"",
		"### ⚠ BREAKING CHANGES",
		"",
		"* **mcp:** the `old` tool is gone",
		"",
		"",
		"### Features",
		"",
		"* **mcp:** add a tool ([#1](https://example.com/issues/1))",
		"* **lint:** add a check ([#2](https://example.com/issues/2))",
		"",
		"",
		"### Performance Improvements",
		"",
		"* **kb:** faster reads ([#3](https://example.com/issues/3))",
		"",
	}, "\n")
	got := Regroup(in)
	want := strings.Join([]string{
		"## [1.0.0](https://example.com/compare/v0.9.0...v1.0.0) (2026-01-01)",
		"",
		"",
		"### ⚠ BREAKING CHANGES",
		"",
		"* **mcp:** the `old` tool is gone",
		"",
		"",
		"### MCP tools",
		"",
		"#### Features",
		"",
		"* add a tool ([#1](https://example.com/issues/1))",
		"",
		"",
		"### Lint & repair",
		"",
		"#### Features",
		"",
		"* add a check ([#2](https://example.com/issues/2))",
		"",
		"",
		"### Performance Improvements",
		"",
		"* **kb:** faster reads ([#3](https://example.com/issues/3))",
		"",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRegroupOnlyTheFirstSectionAndOnlyWhatItCanParse(t *testing.T) {
	section := func(v, entry string) string {
		return "## [" + v + "](https://example.com) (2026-01-01)\n\n\n### Bug Fixes\n\n" + entry + "\n\n"
	}
	in := section("0.2.0", "* **web:** a") + section("0.1.0", "* **web:** b")
	got := Regroup(in)
	if !strings.Contains(got, "### Atlas") || !strings.Contains(got, "* **web:** b") {
		t.Errorf("the first section must be regrouped and the second left alone:\n%s", got)
	}

	// Unchanged: no version section, no Features/Bug Fixes, text it would lose.
	for name, in := range map[string]string{
		"no version":    "# Changelog\n\n### Features\n\n* **web:** a\n",
		"no entries":    "## [0.1.0](https://example.com) (2026-01-01)\n\n\n### Reverts\n\n* **web:** a\n",
		"stray text":    "## [0.1.0](https://example.com) (2026-01-01)\n\n\n### Features\n\nsome prose\n\n* **web:** a\n",
		"empty section": "## [0.1.0](https://example.com) (2026-01-01)\n",
		"":              "",
	} {
		if got := Regroup(in); got != in {
			t.Errorf("%s: changed:\n%s", name, got)
		}
	}
}

func TestRegroupGeneralKeepsAnUnknownScope(t *testing.T) {
	in := "## [0.1.0](https://example.com) (2026-01-01)\n\n\n### Features\n\n* **newthing:** a\n* b\n* **web:** c\n\n"
	got := Regroup(in)
	general := got[strings.Index(got, "### General"):]
	for _, want := range []string{"* **newthing:** a", "* b"} {
		if !strings.Contains(general, want) {
			t.Errorf("General lacks %q:\n%s", want, general)
		}
	}
	if strings.Contains(got, "### Lint & repair") || strings.Contains(got, "#### Bug Fixes") {
		t.Errorf("an empty area or group was written:\n%s", got)
	}
}

func TestRegroupKeepsMultiLineEntries(t *testing.T) {
	in := "## [0.1.0](https://example.com) (2026-01-01)\n\n\n### Features\n\n* **web:** a\n  more of a\n* **web:** b\n\n"
	got := Regroup(in)
	if !strings.Contains(got, "* a\n  more of a\n* b\n") {
		t.Errorf("a continuation line was lost:\n%s", got)
	}
}

func TestEveryScopeHasOneArea(t *testing.T) {
	seen := map[string]string{}
	for _, a := range areas {
		for _, s := range a.Scopes {
			if prev, dup := seen[s]; dup {
				t.Errorf("scope %q is in both %s and %s", s, prev, a.Name)
			}
			seen[s] = a.Name
		}
	}
}

// The pr-title job and the regrouper must agree on the scopes: a scope the job
// accepts that no area knows would land in General, and one the job rejects
// would be a scope nobody can use.
func TestCanonicalScopesMatchTheWorkflow(t *testing.T) {
	ci := read(t, filepath.Join("..", "..", ".github", "workflows", "pr-title.yml"))
	lines := strings.Split(ci, "\n")
	var got []string
	for i, l := range lines {
		if strings.TrimSpace(l) != "scopes: |" {
			continue
		}
		indent := len(l) - len(strings.TrimLeft(l, " "))
		for _, s := range lines[i+1:] {
			if strings.TrimSpace(s) == "" || len(s)-len(strings.TrimLeft(s, " ")) <= indent {
				break
			}
			got = append(got, strings.TrimSpace(s))
		}
	}
	want := CanonicalScopes()
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("pr-title.yml scopes = %v, CanonicalScopes() = %v", got, want)
	}

	contributing := read(t, filepath.Join("..", "..", "CONTRIBUTING.md"))
	for _, s := range want {
		if !strings.Contains(contributing, "`"+s+"`") {
			t.Errorf("CONTRIBUTING.md does not list the canonical scope %q", s)
		}
	}
}

func TestWrappedLines(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []int
	}{
		{"one line", "feat(mcp): a tool\n\nprose", nil},
		{"wrapped", "intro\n\nfix(atlas): switching KB draws the graph without waiting for the\nfirst frame\n", []int{3}},
		{"unscoped and breaking", "feat!: a thing that is long and\nwraps", []int{1}},
		{"scope with underscore", "fix(kb_status): a thing that\nwraps", []int{1}},
		{"two headers in a row", "feat(mcp): a\nfix(lint): b\n\nrest", nil},
		{"bullet below", "feat(mcp): a\n* detail\n- more\n1. numbered", nil},
		{"heading below", "feat(mcp): a\n## Notes", nil},
		{"issue reference continues the line", "feat(mcp): a thing that closes\n#123 and wraps", []int{1}},
		{"last line", "feat(mcp): a", nil},
		{"crlf", "feat(mcp): a\r\n\r\nfix(lint): b\r\ncontinued\r\n", []int{3}},
		{"prose that starts like a type", "features: not a header\nsecond line", nil},
		{"every wrapped one", "feat: a\nb\n\nfix(web): c\nd\n", []int{1, 4}},
	}
	for _, c := range cases {
		got := WrappedLines(c.body)
		if len(got) != len(c.want) {
			t.Errorf("%s: WrappedLines = %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: WrappedLines = %v, want %v", c.name, got, c.want)
			}
		}
	}
}
