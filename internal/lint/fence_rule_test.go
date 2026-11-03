package lint

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// fencedBody swaps ' for ` so the fixture can live in a raw string.
func fencedBody(s string) string { return strings.ReplaceAll(s, "'", "`") }

// TestFenceRuleIsOne pins WP1 of #1503: a four-backtick fence holding a
// three-backtick line, a tilde fence and an unclosed fence are the same lines
// of code for every consumer of the package.
func TestFenceRuleIsOne(t *testing.T) {
	body := fencedBody(`intro
''''
'''
see 'repo:k' between double braces
[inner](inner.md)
''''
[outer](outer.md)
~~~
[tilde](tilde.md)
~~~
'''
[open](open.md)
`)
	kinds := kb.FenceKinds(body)
	want := []kb.LineKind{
		kb.LineText, kb.LineFenceOpen, kb.LineFenceBody, kb.LineFenceBody, kb.LineFenceBody,
		kb.LineFenceClose, kb.LineText, kb.LineFenceOpen, kb.LineFenceBody, kb.LineFenceClose,
		kb.LineFenceOpen, kb.LineFenceBody, kb.LineFenceBody,
	}
	for i, w := range want {
		if kinds[i] != w {
			t.Fatalf("line %d: kind %d, want %d", i, kinds[i], w)
		}
	}

	// MaskCodeSpans blanks exactly the non-text lines.
	masked := strings.Split(kb.MaskCodeSpans(body), "\n")
	for i, l := range strings.Split(body, "\n") {
		if (kinds[i] != kb.LineText) != (strings.TrimSpace(masked[i]) == "" && strings.TrimSpace(l) != "") && strings.TrimSpace(l) != "" {
			t.Errorf("line %d: MaskCodeSpans disagrees with FenceKinds", i)
		}
	}

	// The facts scanner sees only the outer link.
	var hrefs []string
	for _, para := range paragraphLinks(body) {
		for _, o := range para {
			hrefs = append(hrefs, o.href)
		}
	}
	if strings.Join(hrefs, ",") != "outer.md" {
		t.Errorf("paragraphLinks hrefs = %v, want only outer.md", hrefs)
	}

	// The placeholder detector does not look inside the four-backtick fence.
	if _, ok := detectMangledPlaceholders("a.md", body); ok {
		t.Error("detectMangledPlaceholders flagged a line inside a fence")
	}

	// codeSpans: the four-backtick block is one span holding the three-backtick line.
	spans := codeSpans(body)
	if len(spans) < 1 || !strings.Contains(spans[0], "```") || !strings.Contains(spans[0], "inner.md") {
		t.Errorf("codeSpans = %q", spans)
	}

	// An ssh prompt before a four-backtick fence holding a three-backtick line
	// is one remote block.
	remote := fencedBody("ssh host-a\n''''\n'''\nls\n''''\nafter\n")
	spansR := remoteFenceSpans(remote)
	if len(spansR) != 1 || !strings.Contains(remote[spansR[0][0]:spansR[0][1]], "ls") {
		t.Errorf("remoteFenceSpans = %v", spansR)
	}
}
