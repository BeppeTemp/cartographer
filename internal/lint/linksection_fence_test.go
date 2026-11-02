package lint

import (
	"strings"
	"testing"
)

// TestLinksSection_FencedExampleIsNotTheSection pins #1503 WP3.
func TestLinksSection_FencedExampleIsNotTheSection(t *testing.T) {
	t.Parallel()
	// Only a template example in a fence: no section at all.
	only := fencedBody("# T\n\nSee [[a/b]].\n\n'''\n## Links\n\n- [[a/b]]\n'''\n")
	if _, ok := locateLinksSection(only); ok {
		t.Error("a fenced example is not a links section")
	}
	if _, dups, fixable, bare, _ := linksSectionIssues(only, "x/p.md", nil); len(dups) != 0 || len(fixable) != 0 || bare {
		t.Errorf("findings from a fenced example: %v %v %v", dups, fixable, bare)
	}

	// Fences before and after the real section, and a bash comment inside it.
	body := fencedBody(`# T

'''
## Links
- [[x/y]]
'''

## Links

- [[c/d]] - why
'''bash
# a comment, not a heading
echo hi
'''
- [[e/f]] - also

## After

'''
## Links
- [[a/b]]
'''
`)
	x, ok := locateLinksSection(body)
	if !ok {
		t.Fatal("real section not found")
	}
	sec := body[x.headEnd:x.end]
	if !strings.Contains(sec, "- [[c/d]]") || !strings.Contains(sec, "# a comment") || !strings.Contains(sec, "- [[e/f]]") || strings.Contains(sec, "## After") || strings.Contains(sec, "[[x/y]]") {
		t.Errorf("section = %q", sec)
	}
	// Dropping an item that also appears in a fence of the section touches the real line only.
	got := DropLinkItem(fencedBody("## Links\n\n'''\n- [[a/b]]\n'''\n- [[a/b]]\n"), "- [[a/b]]", nil)
	if want := fencedBody("## Links\n\n'''\n- [[a/b]]\n'''\n"); got != want {
		t.Errorf("DropLinkItem touched the fence: %q", got)
	}
}
