package changelog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCompactGolden(t *testing.T) {
	// notes-0.29.md is the 0.29.0 section of CHANGELOG.md as the release PR
	// carried it: 76670 characters, over the body limit.
	in := read(t, filepath.Join("testdata", "notes-0.29.md"))
	got := Compact(in)
	golden := filepath.Join("testdata", "notes-0.29.compact.golden.md")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if want := read(t, golden); got != want {
		t.Errorf("Compact differs from %s; run: go test ./internal/changelog -run TestCompactGolden -args -update", golden)
	}
	if n := len([]rune(got)); n > NotesLimit-1000 {
		t.Errorf("the 0.29 notes compacted are %d characters: they must fit the release PR body without a split", n)
	}
	// And the property the issue asks for: a body that needs no split.
	parts, err := Split(got, NotesLimit)
	if err != nil || len(parts) != 1 {
		t.Errorf("Split: %d parts, %v; want 1", len(parts), err)
	}
}

func TestCompactOneBulletPerCommit(t *testing.T) {
	in := read(t, filepath.Join("testdata", "notes-0.29.md"))
	got := Compact(in)
	sha := regexp.MustCompile(`\(([^)]*\b)?([0-9a-f]{7})(,|\))`)
	seen := map[string]string{}
	for _, l := range strings.Split(got, "\n") {
		if !strings.HasPrefix(l, "* ") {
			continue
		}
		m := sha.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if prev, dup := seen[m[2]]; dup {
			t.Errorf("commit %s listed twice:\n%s\n%s", m[2], prev, l)
		}
		seen[m[2]] = l
	}
	if strings.Contains(got, "](http") && !strings.Contains(strings.SplitN(got, "\n", 2)[0], "](http") {
		t.Error("a link survived outside the version heading")
	}
	for _, c := range []string{"3a6f4ca", "e40088a"} {
		if strings.Count(got, c) != 1 {
			t.Errorf("%s appears %d times, want 1", c, strings.Count(got, c))
		}
	}
	// Full text order: breaking changes first, then areas.
	if i, j := strings.Index(got, "### ⚠ BREAKING CHANGES"), strings.Index(got, "### Atlas"); i < 0 || j < i {
		t.Errorf("breaking changes at %d, Atlas at %d", i, j)
	}
}

func TestCompactReferences(t *testing.T) {
	in := `## [1.0.0](https://example.com/compare/v0.9.0...v1.0.0) (2026-01-01)

### ⚠ BREAKING CHANGES

* **mcp:** the tool is gone
* **mcp:** the tool is gone

### Atlas

#### Features

* a view ([#12](https://example.com/issues/12)) ([abc1234](https://example.com/commit/abc1234def5678abc1234def5678abc1234def56))
* the header ([abc1234](https://example.com/commit/abc1234def5678abc1234def5678abc1234def56)), closes [#7](https://example.com/issues/7), [#8](https://example.com/issues/8)
* only on the header ([1111111](https://example.com/commit/1111111def5678abc1234def5678abc1234def56)), closes [#7](https://example.com/issues/7)
* only on the header ([#13](https://example.com/issues/13)) ([2222222](https://example.com/commit/2222222def5678abc1234def5678abc1234def56))

#### Bug Fixes

* the same commit as the feature ([1111111](https://example.com/commit/1111111def5678abc1234def5678abc1234def56))
`
	want := `## [1.0.0](https://example.com/compare/v0.9.0...v1.0.0) (2026-01-01)

### ⚠ BREAKING CHANGES

* **mcp:** the tool is gone

### Atlas

#### Features

* a view (#12, abc1234)
* only on the header (1111111, closes #7)
* only on the header (#13, 2222222)
`
	if got := Compact(in); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCompactDropsEmptiedHeadings(t *testing.T) {
	in := `## [1.0.0](https://example.com/c) (2026-01-01)

### Atlas

#### Features

* one ([abc1234](https://example.com/commit/abc1234def5678abc1234def5678abc1234def56))

### MCP tools

#### Bug Fixes

* one again ([abc1234](https://example.com/commit/abc1234def5678abc1234def5678abc1234def56))
`
	got := Compact(in)
	if strings.Contains(got, "### MCP tools") || strings.Contains(got, "Bug Fixes") {
		t.Errorf("an emptied heading stayed:\n%s", got)
	}
}
