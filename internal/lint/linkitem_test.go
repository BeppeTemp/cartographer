package lint

import (
	"strings"
	"testing"
)

// #1503 WP12: one list-item regex, and a separator is not a word.
func TestLinksSection_BoldProseLineIsNotAnItem(t *testing.T) {
	t.Parallel()
	body := "# T\n\nThe owner is [a](../a/a.md).\n\n## Links\n\n- [parent](../p/p.md)\n**Why both**: [a](../a/a.md) is the owner\n---\n- [a](../a/a.md)\n"
	_, dups, fixable, _, _ := linksSectionIssues(body, "x/t.md", nil)
	if len(dups) == 0 {
		t.Fatalf("the duplicate is still reported: %v", dups)
	}
	for id, fix := range fixable {
		// Only the real list item may be dropped.
		if fix.Field != "- [a](../a/a.md)" {
			t.Errorf("%s: fix would edit %q", id, fix.Field)
		}
	}
	if len(fixable) != 1 {
		t.Fatalf("fixable = %v", fixable)
	}
	for _, fix := range fixable {
		out := ReplaceLinkItem(body, fix.Field, "", func(int, string) bool { return false })
		if !strings.Contains(out, "**Why both**: [a](../a/a.md) is the owner\n---\n") {
			t.Errorf("the bold line or the rule was removed:\n%s", out)
		}
	}
	if listItemRe.MatchString("**bold** text") || listItemRe.MatchString("---") || !listItemRe.MatchString("  - x") || !listItemRe.MatchString("1) x") {
		t.Error("listItemRe")
	}
	if got := listMarker("  -   [[a]]"); got != "  -   " {
		t.Errorf("listMarker = %q", got)
	}
}

func TestLinksSection_SeparatorsAreNotWords(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"middle dot": "# T\n\n## Links\n\n- [[a/b]] · [[c/d]]\n",
		"arrow":      "# T\n\n## Links\n\n- [[a/b]] → [[c/d]]\n",
		"em dash":    "# T\n\n## Links\n\n- [[a/b]] — [[c/d]]\n",
	} {
		if _, _, _, bare, n := linksSectionIssues(body, "x/t.md", nil); !bare || n != 2 {
			t.Errorf("%s: bare=%v count=%d, want a bare list of 2", name, bare, n)
		}
	}
	if _, _, _, bare, _ := linksSectionIssues("# T\n\n## Links\n\n- [[a/b]] perché sì\n", "x/t.md", nil); bare {
		t.Error("a non-ASCII word is a word")
	}
}
