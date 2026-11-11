package mcpserver

import "testing"

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
