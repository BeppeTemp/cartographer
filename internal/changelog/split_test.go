package changelog

import (
	"fmt"
	"strings"
	"testing"
)

// notes builds a grouped section: each category has n entries of size runes.
func notes(cats []string, n, size int) string {
	var b strings.Builder
	b.WriteString("## [1.2.3](u) (d)\n\n")
	for _, c := range cats {
		b.WriteString("### " + c + "\n\n")
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "* **x:** %s\n", strings.Repeat("é", size))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func entries(s string) int { return strings.Count(s, "\n* ") }

func TestSplitUnderTheLimitIsOnePart(t *testing.T) {
	in := notes([]string{"Atlas", "General"}, 3, 20)
	parts, err := Split(in, 2000)
	if err != nil || len(parts) != 1 {
		t.Fatalf("parts = %d, %v; want 1", len(parts), err)
	}
	if want := strings.TrimRight(in, "\n") + "\n"; parts[0] != want {
		t.Errorf("part 1 = %q, want the notes unchanged", parts[0])
	}
	if strings.Contains(parts[0], ContinuedNote) {
		t.Error("a single part must not say the notes continue")
	}
}

func TestSplitCutsAtACategoryHeading(t *testing.T) {
	in := notes([]string{"Atlas", "MCP tools", "General"}, 5, 100)
	parts, err := Split(in, 1400)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) < 2 {
		t.Fatalf("parts = %d, want at least 2", len(parts))
	}
	if !strings.HasPrefix(parts[0], "## [1.2.3](u) (d)\n") || !strings.HasSuffix(parts[0], ContinuedNote+"\n") {
		t.Errorf("part 1 must start with the version and end with the note: %q", parts[0])
	}
	for i, p := range parts[1:] {
		if !strings.HasPrefix(p, PartMarker(i+2)+"\n\n### ") {
			t.Errorf("part %d does not start with its marker and a category heading: %.60q", i+2, p)
		}
		if strings.Contains(p, "(continued)") {
			t.Errorf("part %d repeats a heading although no category was cut", i+2)
		}
	}
	if got := entries(strings.Join(parts, "")); got != 15 {
		t.Errorf("entries across parts = %d, want 15", got)
	}
}

func TestSplitCutsAnOversizedCategoryAtAnEntry(t *testing.T) {
	in := notes([]string{"Atlas", "General"}, 40, 100)
	parts, err := Split(in, 1500)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) < 4 {
		t.Fatalf("parts = %d, want the Atlas category over several parts", len(parts))
	}
	all := strings.Join(parts, "")
	plain, cont := strings.Index(all, "### Atlas\n"), strings.Index(all, "### Atlas (continued)\n\n* **x:**")
	if plain < 0 || cont < plain {
		t.Errorf("the first piece keeps the plain heading, the next ones repeat it: plain %d, continued %d", plain, cont)
	}
	for i, p := range parts {
		for _, l := range strings.Split(p, "\n") {
			if strings.HasPrefix(l, "* ") && !strings.HasSuffix(l, strings.Repeat("é", 100)) {
				t.Errorf("part %d: an entry was cut: %.40q", i+1, l)
			}
		}
	}
	if got := entries(strings.Join(parts, "")); got != 80 {
		t.Errorf("entries across parts = %d, want 80", got)
	}
}

func TestSplitNoPartPastTheLimit(t *testing.T) {
	in := notes([]string{"Atlas", "MCP tools", "Client & CLI", "General"}, 30, 77)
	for _, limit := range []int{1200, 2000, 3333, 9000} {
		parts, err := Split(in, limit)
		if err != nil {
			t.Fatal(err)
		}
		for i, p := range parts {
			if n := len([]rune(p)); n > limit {
				t.Errorf("limit %d: part %d has %d characters", limit, i+1, n)
			}
		}
		if got := entries(strings.Join(parts, "")); got != 120 {
			t.Errorf("limit %d: entries = %d, want 120", limit, got)
		}
	}
}

func TestSplitErrorsWhenNoCutHelps(t *testing.T) {
	in := "## [1.2.3](u) (d)\n\n### Atlas\n\n* " + strings.Repeat("a", 3000) + "\n"
	if _, err := Split(in, 1000); err == nil || !strings.Contains(err.Error(), "Atlas") {
		t.Errorf("err = %v, want one naming the category", err)
	}
	if _, err := Split("## [1.2.3](u) (d)\n\n"+strings.Repeat("a", 3000)+"\n\n### Atlas\n\n* a\n", 1000); err == nil {
		t.Error("an oversized preface must fail")
	}
}

// The real CHANGELOG.md splits within a small limit and loses no entry.
func TestSplitRealChangelog(t *testing.T) {
	doc := read(t, "../../CHANGELOG.md")
	rest := doc[strings.Index(doc, "## [")+4:]
	sec, err := Section(doc, rest[:strings.Index(rest, "]")])
	if err != nil {
		t.Fatal(err)
	}
	parts, err := Split(sec, 4000)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range parts {
		if len([]rune(p)) > 4000 {
			t.Errorf("part %d is over the limit", i+1)
		}
	}
	if got, want := entries(strings.Join(parts, "")), entries(sec); got != want {
		t.Errorf("entries = %d, want %d", got, want)
	}
}

func bareDelimiters(s string) (n int) {
	for _, l := range strings.Split(s, "\n") {
		if l == "---" {
			n++
		}
	}
	return n
}

// release-please parses the body on merge: notes between the first and the last
// bare `---`, header before and footer after. Its own test input is this shape.
func TestReleasePRBodyKeepsReleasePleasesFrame(t *testing.T) {
	body := ReleasePRBody("## [1.2.3](u) (d)\n\n### Atlas\n\n* a\n")
	want := ":robot: I have created a release *beep* *boop*\n---\n\n\n## [1.2.3](u) (d)\n\n### Atlas\n\n* a\n\n---\nThis PR was generated with [Release Please](https://github.com/googleapis/release-please). See [documentation](https://github.com/googleapis/release-please#release-please)."
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
	if bareDelimiters(body) != 2 {
		t.Errorf("want exactly two --- lines, got %d", bareDelimiters(body))
	}
	mid := strings.Split(body, "\n---\n")[1]
	if !strings.HasPrefix(strings.TrimSpace(mid), "## [1.2.3]") {
		t.Errorf("the version header must open the notes: %q", mid)
	}
}

func TestSplitBodyFitsTheLimitFramed(t *testing.T) {
	in := notes([]string{"Atlas", "General"}, 40, 100)
	for _, limit := range []int{1500, 4000} {
		parts, err := Split(in, limit)
		if err != nil {
			t.Fatal(err)
		}
		body := ReleasePRBody(parts[0])
		if n := len([]rune(body)); n > limit {
			t.Errorf("limit %d: framed body has %d characters", limit, n)
		}
		if bareDelimiters(body) != 2 {
			t.Errorf("limit %d: framed body must have two --- lines", limit)
		}
		for i, p := range parts {
			if bareDelimiters(p) != 0 {
				t.Errorf("part %d holds a bare ---", i+1)
			}
		}
	}
}

func TestSplitRefusesABareDelimiter(t *testing.T) {
	in := "## [1.2.3](u) (d)\n\n### Atlas\n\n* a\n\n---\n\n* b\n"
	if _, err := Split(in, 1000); err == nil || !strings.Contains(err.Error(), "---") {
		t.Errorf("err = %v, want one naming the --- line", err)
	}
}
