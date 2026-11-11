package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A route has one definition with a tier and a summary, and no two routes
// share a pattern.
func TestAPIRoutes_TableIsComplete(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, rt := range apiRoutes {
		if rt.Pattern == "" || rt.Handler == nil {
			t.Errorf("route %+v has no pattern or handler", rt)
		}
		switch rt.Tier {
		case tierStable, tierPreview, tierInternal:
		default:
			t.Errorf("%s: no tier", rt.Pattern)
		}
		if rt.Summary == "" {
			t.Errorf("%s: no summary", rt.Pattern)
		}
		if seen[rt.Pattern] {
			t.Errorf("%s: defined twice", rt.Pattern)
		}
		seen[rt.Pattern] = true
		if rt.kbScoped() != (len(rt.segments) > 1 && rt.segments[0] == "kbs") {
			t.Errorf("%s: the KB variable must be the second segment of every /kbs/{kb}/... route", rt.Pattern)
		}
	}
}

// The tiers decided for the public API; moving a route is a deliberate edit here.
func TestAPIRoutes_Tiers(t *testing.T) {
	t.Parallel()
	want := map[string]apiTier{
		"/kbs": tierStable, "/whoami": tierStable, "/kbs/{kb}/overview": tierStable,
		"/kbs/{kb}/concept": tierStable, "/kbs/{kb}/search": tierStable, "/kbs/{kb}/changes": tierStable,
		"/settings": tierInternal, "/releases": tierInternal, "/kbs/{kb}/artifacts": tierInternal,
		"/kbs/{kb}/artifact": tierInternal, "/kbs/{kb}/secrets": tierInternal,
		"/kbs/{kb}/placeholders": tierInternal, "/kbs/{kb}/audit": tierInternal,
		"/kbs/{kb}/maintenance/summary": tierInternal, "/kbs/{kb}/components": tierInternal,
		"/kbs/{kb}/maintenance/review": tierPreview,
	}
	for _, rt := range apiRoutes {
		if w, ok := want[rt.Pattern]; ok && rt.Tier != w {
			t.Errorf("%s: tier %s, want %s", rt.Pattern, rt.Tier, w)
		}
		delete(want, rt.Pattern)
	}
	for p := range want {
		t.Errorf("%s: not in the table", p)
	}
}

// Every per-KB path demo/export.mjs lists is a route of the table: a renamed
// route breaks this test, not only the docs workflow that builds the demo.
func TestAPIRoutes_DemoExportPathsExist(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(filepath.Join("..", "..", "demo", "export.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	list := regexp.MustCompile(`for \(const p of \[([^\]]*)\]\)`).FindSubmatch(src)
	if list == nil {
		t.Fatal("demo/export.mjs: the per-KB path list (`for (const p of [...])`) is gone; update this test")
	}
	paths := regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(list[1], -1)
	if len(paths) == 0 {
		t.Fatal("demo/export.mjs: the per-KB path list is empty")
	}
	for _, m := range paths {
		p, _, _ := strings.Cut(string(m[1]), "?")
		segs := append([]string{"kbs", "kb-a"}, strings.Split(p, "/")...)
		found := false
		for _, rt := range apiRoutes {
			if rt.match(segs) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("demo/export.mjs exports /kbs/{kb}/%s, which is not a UI API route", p)
		}
	}
}
