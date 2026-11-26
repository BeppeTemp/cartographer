package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
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

// TestResolveDashboardCapsEntriesAcrossGroups: the limit is per group, so a
// grouped widget is also capped as a whole; group totals stay true.
func TestResolveDashboardCapsEntriesAcrossGroups(t *testing.T) {
	t.Parallel()
	_, k := workServer(t)
	for i := 0; i < 250; i++ {
		writeKBFile(t, k, fmt.Sprintf("visible/bulk%03d.md", i),
			fmt.Sprintf("---\ntype: Bulk\ntitle: Bulk %d\nteam: t%d\n---\n# Bulk\n", i, i%5))
	}
	d := kb.Dashboard{Widgets: []kb.DashboardWidget{
		{ID: "g", Type: "list", Source: "concepts", GroupBy: "field:team", Filter: &kb.DashboardFilter{Type: []string{"Bulk"}}},
	}}
	w := resolveDashboard(adminCtx, k, &conformanceCache{}, d).Widgets[0]
	shown, total := 0, 0
	for _, g := range w.Groups {
		shown += len(g.Entries)
		total += g.Total
	}
	if shown != dashboardMaxLimit || total != 250 || w.Total != 250 {
		t.Fatalf("shown %d total %d widget total %d", shown, total, w.Total)
	}
}

// TestAgendaDayReadsStrictly: only a string that is exactly a day, or a full
// RFC 3339 timestamp (taken in the server zone), is a date.
func TestAgendaDayReadsStrictly(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("plus2", 2*3600)
	for in, want := range map[any]string{
		"2026-11-24":                "2026-11-24",
		"2026-11-24T23:30:00Z":      "2026-11-25",
		"2026-11-24T10:00:00+02:00": "2026-11-24",
	} {
		if got, ok := agendaDay(in, zone); !ok || got != want {
			t.Errorf("%v: got %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []any{"2026-11-24garbage", "2026-11-24 10:00", "2026-11-24T10:00:00", "2026-13-40", "", []string{"2026-11-24"}, 20261124} {
		if got, ok := agendaDay(in, zone); ok {
			t.Errorf("%v accepted as %q", in, got)
		}
	}
}

// TestAgendaHorizonIsClamped: a horizon past a year reads as a year.
func TestAgendaHorizonIsClamped(t *testing.T) {
	t.Parallel()
	_, k := workServer(t)
	writeKBFile(t, k, "visible/in.md", "---\ntype: Task\ntitle: in\nstatus: open\ndue: 2027-06-01\n---\n# in\n")
	writeKBFile(t, k, "visible/out.md", "---\ntype: Task\ntitle: out\nstatus: open\ndue: 2028-01-01\n---\n# out\n")
	d := kb.Dashboard{Widgets: []kb.DashboardWidget{{ID: "a", Type: "agenda", Source: "concepts", DateField: "due", HorizonDays: 100000}}}
	now := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)
	got := resolveDashboardAt(adminCtx, k, &conformanceCache{}, d, now).Widgets[0]
	if s := groupSummary(got); s != "2027-06-01=visible/in" {
		t.Fatalf("agenda: %s", s)
	}
}

// TestGroupsAreBounded: past dashboardMaxGroups distinct keys the rest fall
// into one "(other)" group with a true total, and a key is cut to 200 bytes.
func TestGroupsAreBounded(t *testing.T) {
	t.Parallel()
	_, k := workServer(t)
	for i := 0; i < 60; i++ {
		writeKBFile(t, k, fmt.Sprintf("visible/g%02d.md", i), fmt.Sprintf("---\ntype: Bulk\ntitle: G%d\nteam: team%02d\n---\n# G\n", i, i))
	}
	long := strings.Repeat("x", 300)
	writeKBFile(t, k, "visible/long.md", "---\ntype: Bulk\ntitle: Long\nteam: "+long+"\n---\n# L\n")
	d := kb.Dashboard{Widgets: []kb.DashboardWidget{{ID: "g", Type: "stat", Show: "groups", Source: "concepts", GroupBy: "field:team", Filter: &kb.DashboardFilter{Type: []string{"Bulk"}}, Sort: "title"}}}
	w := resolveDashboard(adminCtx, k, &conformanceCache{}, d).Widgets[0]
	if len(w.Groups) != dashboardMaxGroups+1 || w.Total != 61 {
		t.Fatalf("groups %d total %d", len(w.Groups), w.Total)
	}
	other := w.Groups[len(w.Groups)-1]
	if other.Key != dashboardOtherGroup || other.Total != 61-dashboardMaxGroups {
		t.Fatalf("other group: %+v", other)
	}
	for _, g := range w.Groups {
		if len(g.Key) > dashboardKeyMaxBytes {
			t.Fatalf("key of %d bytes", len(g.Key))
		}
	}
}

// AC-kbdash-25
func TestResolveTableAndTimeline(t *testing.T) {
	t.Parallel()
	_, k := workServer(t)
	for id, date := range map[string]string{"b": "2026-11-26", "a": "2026-11-20", "c": "2026-12-30"} {
		writeKBFile(t, k, "visible/"+id+".md", "---\ntype: Task\ntitle: "+id+"\nstatus: open\ndue: "+date+"\ncustomer: acme\n---\n# "+id+"\n")
	}
	writeKBFile(t, k, "visible/nodate.md", "---\ntype: Task\ntitle: nodate\nstatus: open\ncustomer: acme\n---\n# n\n")
	writeKBFile(t, k, "hidden/hd.md", "---\ntype: Task\ntitle: hd\nstatus: open\ndue: 2026-11-21\ncustomer: acme\n---\n# h\n")
	d := kb.Dashboard{Widgets: []kb.DashboardWidget{
		{ID: "t", Type: "table", Source: "concepts", Filter: &kb.DashboardFilter{Fields: map[string]string{"customer": "acme"}}, Columns: []string{"customer", "due"}, Sort: "title"},
		{ID: "tl", Type: "timeline", Source: "concepts", Filter: &kb.DashboardFilter{Fields: map[string]string{"customer": "acme"}}, DateField: "due"},
	}}
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	got := resolveDashboardAt(adminCtx, k, &conformanceCache{}, d, now)
	tbl := got.Widgets[0]
	if tbl.Problem != "" || tbl.Total != 5 || tbl.Entries[0].Fields["customer"] != "acme" || tbl.Entries[0].Fields["due"] == "" {
		t.Fatalf("table: %+v", tbl)
	}
	tl := got.Widgets[1]
	if s := dashboardIDs(tl); tl.Problem != "" || tl.Total != 4 || len(tl.Groups) != 0 || s != "visible/a,hidden/hd,visible/b,visible/c" {
		t.Fatalf("timeline: %q total %d err %q", s, tl.Total, tl.Problem)
	}
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}})
	hid := resolveDashboardAt(ctx, k, &conformanceCache{}, d, now)
	if s := dashboardIDs(hid.Widgets[1]); strings.Contains(s, "hidden") || hid.Widgets[1].Total != 3 || hid.Widgets[0].Total != 4 {
		t.Fatalf("hidden entry leaks: %q %+v", s, hid.Widgets[0].Total)
	}
	for _, e := range hid.Widgets[0].Entries {
		if strings.HasPrefix(e.ID, "hidden/") {
			t.Fatalf("table leaks %s", e.ID)
		}
	}
}

// AC-kbdash-24
func TestResolveLinksHiddenEqualsMissing(t *testing.T) {
	t.Parallel()
	_, k := workServer(t)
	writeKBFile(t, k, "visible/a.md", "---\ntype: Task\ntitle: Alpha\nstatus: open\n---\n# a\n")
	writeKBFile(t, k, "hidden/hd.md", "---\ntype: Task\ntitle: Secret title\nstatus: open\n---\n# h\n")
	mk := func(id string) kb.Dashboard {
		return kb.Dashboard{Widgets: []kb.DashboardWidget{{ID: "l", Type: "links", IDs: []string{"visible/a", id}}}}
	}
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}})
	// The same id string in both runs: hidden for the restricted caller,
	// absent for the whole-KB one is a different caller, so compare a hidden
	// id with a nonexistent id of the same spelling length.
	hid := resolveDashboardAt(ctx, k, &conformanceCache{}, mk("hidden/hd"), now)
	non := resolveDashboardAt(ctx, k, &conformanceCache{}, mk("hidden/zz"), now)
	a, b := hid.Widgets[0].Entries, non.Widgets[0].Entries
	if len(a) != 2 || a[0].Title != "Alpha" || a[1].State != "missing" || a[1].Title != "" {
		t.Fatalf("restricted: %+v", a)
	}
	if a[1].State != b[1].State || a[1].Title != b[1].Title || a[1].Type != b[1].Type || a[1].Fields != nil {
		t.Fatalf("hidden differs from missing: %+v vs %+v", a[1], b[1])
	}
	if strings.Contains(fmt.Sprint(hid), "Secret") {
		t.Fatalf("title leaks: %+v", hid)
	}
	// Narrowed output drops the id typed by the author for a missing entry.
	nar := narrowDashboard(hid).Widgets[0]
	if nar.Entries[1].ID != "" || nar.IDs != nil || nar.Entries[0].ID != "visible/a" {
		t.Fatalf("narrowed: %+v", nar)
	}
	full := resolveDashboardAt(adminCtx, k, &conformanceCache{}, mk("hidden/hd"), now)
	if full.Widgets[0].Entries[1].Title != "Secret title" {
		t.Fatalf("whole-KB caller: %+v", full.Widgets[0].Entries)
	}
}

// AC-kbdash-31
func TestResolveCrossKBLinksNonDisclosure(t *testing.T) {
	t.Parallel()
	r := newCrossRig(t)
	if err := os.WriteFile(filepath.Join(r.b.DataRoot(), "ops", "x.md"), []byte("---\ntype: Note\ntitle: Target page\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	entries := func(ctx context.Context, id string) []dashboardEntry {
		d := kb.Dashboard{Widgets: []kb.DashboardWidget{{ID: "l", Type: "links", IDs: []string{id}}}}
		return resolveDashboardAt(ctx, r.a, &conformanceCache{}, d, now).Widgets[0].Entries
	}
	callers := crossCallers()
	// A caller who cannot read kb-b: an absent KB, an unreadable one, an
	// @server-qualified one and a page that does not exist give the same bytes.
	var want string
	for _, id := range []string{"kb:nope/ops/x", "kb:kb-b/ops/x", "kb:kb-b@other/ops/x", "kb:kb-b/ops/gone", "kb:old-b/ops/x"} {
		raw, _ := json.Marshal(entries(callers["a-only"], id))
		if want == "" {
			want = string(raw)
		}
		if string(raw) != want || strings.Contains(string(raw), "Target") || strings.Contains(string(raw), "kb-b") {
			t.Errorf("%s: %s, want %s", id, raw, want)
		}
	}
	if want != `[{"id":"","state":"unresolved"}]` {
		t.Errorf("neutral entry = %s", want)
	}
	// A reader of the target sees resolved or missing, and the KB name.
	if got := entries(callers["reads-a-b"], "kb:old-b/ops/x"); len(got) != 1 || got[0].KB != "kb-b" || got[0].ID != "ops/x" || got[0].Title != "Target page" || got[0].State != "" {
		t.Errorf("resolved: %+v", got)
	}
	if got := entries(callers["reads-a-b"], "kb:kb-b/ops/gone"); len(got) != 1 || got[0].State != "missing" || got[0].Title != "" || got[0].KB != "" {
		t.Errorf("missing: %+v", got)
	}
	// A connection that does not reach kb-b says so, with nothing of the page.
	scoped := withRoutedSet(callers["reads-a-b"], []string{"kb-a"})
	if got := entries(scoped, "kb:kb-b/ops/x"); len(got) != 1 || got[0].State != "out_of_scope" || got[0].Title != "" || got[0].ID != "" {
		t.Errorf("out_of_scope: %+v", got)
	}
}

// A widget of cross-KB ids reads no local source (a local source failure does
// not fail it), and a caller who does not read this KB whole gets no
// author-typed kb: target in the narrowed output unless the page resolved.
func TestResolveCrossLinksLazyAndNarrowed(t *testing.T) {
	t.Parallel()
	r := newCrossRig(t)
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	d := kb.Dashboard{Widgets: []kb.DashboardWidget{{ID: "l", Type: "links", IDs: []string{"kb:kb-b/ops/x", "kb:secret-kb/hidden/page", "kb:kb-b/ops/gone"}}}}
	src := &dashSources{ctx: crossCallers()["reads-a-b"], k: r.a, cc: &conformanceCache{}, now: now, conceptsDone: true, conceptErr: fmt.Errorf("boom")}
	if w := resolveWidget(src, d.Widgets[0]); w.Problem != "" || len(w.Entries) != 3 || w.Entries[0].KB != "kb-b" {
		t.Fatalf("cross-only widget touched the local source: %+v", w)
	}
	// Partial reader of kb-a (one map), no grant on kb-b: every cross entry is neutral.
	partial := principal(auth.Permission{KB: "kb-a", Maps: []string{"visible"}})
	nar := narrowDashboard(resolveDashboardAt(partial, r.a, &conformanceCache{}, d, now)).Widgets[0]
	raw, _ := json.Marshal(nar)
	for _, leak := range []string{"secret-kb", "hidden", "ops/x", "ops/gone", "kb-b"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("%q leaks: %s", leak, raw)
		}
	}
	// Partial reader of kb-a who reads kb-b: the resolved page appears, nothing typed for the others.
	both := principal(auth.Permission{KB: "kb-a", Maps: []string{"visible"}}, auth.Permission{KB: "kb-b"})
	raw, _ = json.Marshal(narrowDashboard(resolveDashboardAt(both, r.a, &conformanceCache{}, d, now)).Widgets[0])
	if strings.Contains(string(raw), "secret-kb") || strings.Contains(string(raw), "ops/gone") {
		t.Errorf("typed target leaks: %s", raw)
	}
}
