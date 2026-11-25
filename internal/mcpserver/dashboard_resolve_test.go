package mcpserver

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func groupSummary(w resolvedWidget) string {
	var parts []string
	for _, g := range w.Groups {
		var ids []string
		for _, e := range g.Entries {
			ids = append(ids, e.ID)
		}
		parts = append(parts, g.Key+"="+strings.Join(ids, "+"))
	}
	return strings.Join(parts, " ")
}

// AC-kbdash-7
func TestResolveAgendaBuckets(t *testing.T) {
	t.Parallel()
	_, k := workServer(t)
	for id, date := range map[string]string{"p": "2026-11-20", "t": "2026-11-24", "n": "2026-11-26", "far": "2026-12-30"} {
		writeKBFile(t, k, "visible/"+id+".md", "---\ntype: Task\ntitle: "+id+"\nstatus: open\ndue: "+date+"\n---\n# "+id+"\n")
	}
	writeKBFile(t, k, "visible/nodate.md", "---\ntype: Task\ntitle: nodate\nstatus: open\n---\n# n\n")
	writeKBFile(t, k, "hidden/hd.md", "---\ntype: Task\ntitle: hd\nstatus: open\ndue: 2026-11-25\n---\n# h\n")
	d := kb.Dashboard{Widgets: []kb.DashboardWidget{{ID: "a", Type: "agenda", Source: "concepts", DateField: "due", HorizonDays: 3, Sort: "title"}}}
	now := time.Date(2026, 11, 24, 23, 30, 0, 0, time.UTC) // the server's day, whatever the caller's
	got := resolveDashboardAt(adminCtx, k, &conformanceCache{}, d, now).Widgets[0]
	if s := groupSummary(got); s != "overdue=visible/p 2026-11-24=visible/t 2026-11-25=hidden/hd 2026-11-26=visible/n" || got.Total != 4 {
		t.Fatalf("agenda: %s total %d", s, got.Total)
	}
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}})
	hid := resolveDashboardAt(ctx, k, &conformanceCache{}, d, now).Widgets[0]
	if s := groupSummary(hid); strings.Contains(s, "hidden") || hid.Total != 3 {
		t.Fatalf("hidden entry in the agenda: %s total %d", s, hid.Total)
	}
}

// AC-kbdash-8
func TestResolveKanbanColumnsTotal(t *testing.T) {
	t.Parallel()
	_, k := workServer(t)
	d := kb.Dashboard{Widgets: []kb.DashboardWidget{
		{ID: "k", Type: "kanban", Filter: &kb.DashboardFilter{Map: []string{"visible"}}, Columns: []string{"in-progress", "blocked", "open"}},
		{ID: "all", Type: "kanban", Filter: &kb.DashboardFilter{Map: []string{"visible"}}},
	}}
	got := resolveDashboard(adminCtx, k, &conformanceCache{}, d).Widgets
	if s := groupSummary(got[0]); s != "in-progress=visible/b blocked= open=visible/a" || got[0].Total != 2 {
		t.Fatalf("columns: %s total %d", s, got[0].Total)
	}
	if got[0].Groups[1].Total != 0 {
		t.Fatalf("an empty column stays: %+v", got[0].Groups)
	}
	if got[1].Total != 3 || len(got[1].Groups) != 3 {
		t.Fatalf("default columns are the statuses: %+v", got[1])
	}
}

// AC-kbdash-28
func TestResolveGroupByTypeAndField(t *testing.T) {
	t.Parallel()
	_, k := workServer(t)
	writeKBFile(t, k, "visible/e.md", "---\ntype: Task\ntitle: E\nstatus: open\ncustomer: acme\n---\n# E\n")
	writeKBFile(t, k, "visible/f.md", "---\ntype: Task\ntitle: F\nstatus: open\ncustomer: beta\n---\n# F\n")
	f := &kb.DashboardFilter{Map: []string{"visible"}, Status: []string{"open"}}
	for _, source := range []string{"work", "concepts"} {
		d := kb.Dashboard{Widgets: []kb.DashboardWidget{
			{ID: "t", Type: "list", Source: source, Filter: f, GroupBy: "type", Sort: "title"},
			{ID: "c", Type: "list", Source: source, Filter: f, GroupBy: "field:customer", Sort: "title"},
			{ID: "s", Type: "stat", Source: source, Filter: f, GroupBy: "field:customer", Show: "groups"},
			{ID: "n", Type: "stat", Source: source, Filter: f},
		}}
		got := resolveDashboardAt(adminCtx, k, &conformanceCache{}, d, time.Now()).Widgets
		if s := groupSummary(got[0]); s != "Task=visible/a+visible/e+visible/f" {
			t.Fatalf("%s by type: %s", source, s)
		}
		if s := groupSummary(got[1]); s != "=visible/a acme=visible/e beta=visible/f" {
			t.Fatalf("%s by field: %s", source, s)
		}
		if got[2].Total != 3 || len(got[2].Groups) != 3 || got[2].Groups[1].Entries != nil {
			t.Fatalf("%s stat groups: %+v", source, got[2])
		}
		if got[3].Total != 3 || got[3].Entries != nil || got[3].Groups != nil {
			t.Fatalf("%s stat count: %+v", source, got[3])
		}
	}
	bad := kb.Dashboard{Widgets: []kb.DashboardWidget{{ID: "s", Type: "stat", Show: "groups"}}}
	if w := resolveDashboard(adminCtx, k, &conformanceCache{}, bad).Widgets[0]; w.Problem == "" {
		t.Fatal("show groups without group_by accepted")
	}
}
