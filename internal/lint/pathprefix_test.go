package lint

import (
	"strings"
	"testing"
)

// TestRewritePathPrefixes_OnlyRealPrefixes pins #1503 WP2: of every occurrence
// below, only the first (a real path) is a legacy path; the rewrite touches
// that one and the lint is then clean (it converges).
func TestRewritePathPrefixes_OnlyRealPrefixes(t *testing.T) {
	t.Parallel()
	body := strings.ReplaceAll(`Real: see wiki/ops/x and (wiki/y) and /srv/app/conf.
URL: https://example.com/wiki/Go and mywiki/notes and /srv/application/config and /srv/app-old.
Code: 'cd wiki/' and
'''
cd wiki/
'''
End: /srv/app.
`, "'", "`")
	from := []string{"wiki/", "/srv/app"}
	to := []string{"old/", "/srv/new"}
	got := RewritePathPrefixes(body, from, to)
	want := strings.ReplaceAll(`Real: see old/ops/x and (old/y) and /srv/new/conf.
URL: https://example.com/wiki/Go and mywiki/notes and /srv/application/config and /srv/app-old.
Code: 'cd wiki/' and
'''
cd wiki/
'''
End: /srv/new.
`, "'", "`")
	if got != want {
		t.Fatalf("rewrite:\n%s\nwant:\n%s", got, want)
	}
	if left := FindPathPrefixes(got, from); len(left) != 0 {
		t.Errorf("not converged: %v", left)
	}
	mapping := []legacyPath{{"wiki/", "old/"}, {"/srv/app", "/srv/new"}}
	if f := legacyPathFindings(got, "a.md", mapping); len(f) != 0 {
		t.Errorf("findings after repair: %v", f)
	}
	if f := legacyPathFindings(body, "a.md", mapping); len(f) != 2 {
		t.Errorf("findings before repair = %d, want 2", len(f))
	}
	// Only false matches: no finding at all.
	only := "https://example.com/wiki/Go mywiki/notes /srv/application /srv/app-old\n"
	if f := legacyPathFindings(only, "a.md", mapping); len(f) != 0 {
		t.Errorf("false matches reported: %v", f)
	}
}
