package mcpserver

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

func dashboardIDs(w resolvedWidget) string {
	var ids []string
	for _, e := range w.Entries {
		ids = append(ids, e.ID)
	}
	return strings.Join(ids, ",")
}

// TestResolveDashboardHidesWhatThePrincipalCannotSee: a narrowed principal
// gets no count, entry or field value from a collection it cannot see, on both
// sources, even after the full caller warmed the shared work cache.
func TestResolveDashboardHidesWhatThePrincipalCannotSee(t *testing.T) {
	t.Parallel()
	_, k := workServer(t)
	cc := &conformanceCache{}
	d := kb.Dashboard{Widgets: []kb.DashboardWidget{
		{ID: "w", Type: "list"},
		{ID: "c", Type: "list", Source: "concepts", Filter: &kb.DashboardFilter{Type: []string{"Task"}}},
		{ID: "s", Type: "stat", Filter: &kb.DashboardFilter{Text: "H"}, Source: "concepts"},
	}}
	full := resolveDashboard(adminCtx, k, cc, d)
	if got := dashboardIDs(full.Widgets[0]); !strings.Contains(got, "hidden/h") {
		t.Fatalf("the unrestricted caller must see the hidden map: %s", got)
	}
	if full.Widgets[2].Total != 1 {
		t.Fatalf("the full caller must count the hidden concept: %+v", full.Widgets[2])
	}
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}})
	got := resolveDashboard(ctx, k, cc, d)
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "hidden/") || strings.Contains(string(b), "secret") {
		t.Fatalf("hidden collection leaked: %s", b)
	}
	if got.Widgets[0].Total != 3 || got.Widgets[1].Total != 1 || dashboardIDs(got.Widgets[1]) != "visible/a" || got.Widgets[2].Total != 0 {
		t.Fatalf("counts: %s", b)
	}
}

// TestResolveDashboardBadWidgetIsolated: a widget that cannot resolve carries
// its error and the others still resolve.
func TestResolveDashboardBadWidgetIsolated(t *testing.T) {
	t.Parallel()
	_, k := workServer(t)
	d := kb.Dashboard{Widgets: []kb.DashboardWidget{
		{ID: "bad", Type: "list", Problem: "width must be 1..4"},
		{ID: "src", Type: "list", Source: "nowhere"},
		{ID: "age", Type: "list", Source: "concepts", Sort: "age"},
		{ID: "note", Type: "note", Text: "hello"},
		{ID: "ok", Type: "list", Filter: &kb.DashboardFilter{Map: []string{"visible"}}},
	}}
	got := resolveDashboard(adminCtx, k, &conformanceCache{}, d).Widgets
	for i := 0; i < 3; i++ {
		if got[i].Problem == "" || got[i].Entries != nil {
			t.Fatalf("widget %d: %+v", i, got[i])
		}
	}
	if got[3].Problem != "" || got[3].Text != "hello" {
		t.Fatalf("note: %+v", got[3])
	}
	if got[4].Problem != "" || got[4].Total != 3 {
		t.Fatalf("ok: %+v", got[4])
	}
}

func TestResolveDashboardFilterSortLimit(t *testing.T) {
	t.Parallel()
	_, k := workServer(t)
	yes := true
	cc := &conformanceCache{}
	resolve := func(w kb.DashboardWidget) resolvedWidget {
		w.ID, w.Type = "w", "list"
		return resolveDashboard(adminCtx, k, cc, kb.Dashboard{Widgets: []kb.DashboardWidget{w}}).Widgets[0]
	}
	if w := resolve(kb.DashboardWidget{Filter: &kb.DashboardFilter{Status: []string{"open"}}}); dashboardIDs(w) != "visible/a,hidden/h" && dashboardIDs(w) != "hidden/h,visible/a" {
		t.Fatalf("status: %s", dashboardIDs(w))
	}
	if w := resolve(kb.DashboardWidget{Filter: &kb.DashboardFilter{HasItems: &yes, Map: []string{"visible"}}}); dashboardIDs(w) != "visible/c" || w.Entries[0].ItemCount != 2 {
		t.Fatalf("has_items: %+v", w)
	}
	if w := resolve(kb.DashboardWidget{Filter: &kb.DashboardFilter{Fields: map[string]string{"priority": "p1"}}}); dashboardIDs(w) != "visible/b" || w.Entries[0].Fields["priority"] != "p1" {
		t.Fatalf("fields: %+v", w)
	}
	if w := resolve(kb.DashboardWidget{Filter: &kb.DashboardFilter{Map: []string{"visible"}}, Sort: "field:priority"}); dashboardIDs(w) != "visible/b,visible/a,visible/c" {
		t.Fatalf("sort field: %s", dashboardIDs(w))
	}
	if w := resolve(kb.DashboardWidget{Filter: &kb.DashboardFilter{Map: []string{"visible"}}, Sort: "title", Limit: 2}); dashboardIDs(w) != "visible/a,visible/b" || w.Total != 3 {
		t.Fatalf("limit: %+v", w)
	}
	if w := resolve(kb.DashboardWidget{Filter: &kb.DashboardFilter{Text: "secret"}}); w.Total != 0 {
		t.Fatalf("text matches only title and id: %+v", w)
	}
	over := map[string]string{}
	for i := 0; i <= maxWorkListFields; i++ {
		over[strings.Repeat("k", i+1)] = "v"
	}
	if w := resolve(kb.DashboardWidget{Filter: &kb.DashboardFilter{Fields: over}}); w.Problem != "filter fields: 9 keys: at most 8" {
		t.Fatalf("field filter cap message: %q", w.Problem)
	}
}

// TestResolveDashboardSourceErrorHidesDataDir: a path error from a source
// reaches the widget's error without the server's data directory.
func TestResolveDashboardSourceErrorHidesDataDir(t *testing.T) {
	t.Parallel()
	_, k := workServer(t)
	bad := &fs.PathError{Op: "open", Path: filepath.Join(k.Root, "visible", "a.md"), Err: fs.ErrPermission}
	src := &dashSources{ctx: adminCtx, k: k, workDone: true, workErr: bad}
	w := resolveWidget(src, kb.DashboardWidget{ID: "w", Type: "list"})
	if w.Problem == "" || strings.Contains(w.Problem, k.Root) {
		t.Fatalf("data dir in the widget error: %q", w.Problem)
	}
}
