package mcpserver

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// Dashboard resolver: turns the queries of a dashboard's widgets into entries,
// for the caller who asks. Every row comes from a source already narrowed to
// what that caller may read (D226), and nothing resolved here is cached across
// callers.

const (
	dashboardDefaultLimit = workListDefaultLimit
	dashboardMaxLimit     = workListMaxLimit
	dashboardItemPreview  = 3 // items shown per entry; the count says the rest
)

// dashboardEntry is a work_list entry, trimmed: what a widget draws.
type dashboardEntry struct {
	ID        string            `json:"id"`
	Title     string            `json:"title,omitempty"`
	Type      string            `json:"type,omitempty"`
	Map       string            `json:"map,omitempty"`
	Status    string            `json:"status,omitempty"`
	State     string            `json:"state,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	AgeDays   *int              `json:"age_days,omitempty"`
	Stale     bool              `json:"stale,omitempty"`
	Items     []lint.WorkItem   `json:"items,omitempty"` // the first dashboardItemPreview
	ItemCount int               `json:"item_count,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// resolvedWidget is a widget definition plus what it resolved to. A tolerant
// read's Problem and a resolution failure both travel in the definition's
// `error`: they fail that widget, never the dashboard.
type resolvedWidget struct {
	kb.DashboardWidget
	Entries []dashboardEntry `json:"entries,omitempty"`
	// Groups holds the grouped shape: the columns of a kanban, the buckets of
	// an agenda, the counts of a stat that shows groups. Entries is then empty.
	Groups []dashboardGroup `json:"groups,omitempty"`
	Total  int              `json:"total"`
}

// dashboardGroup is one group of a widget: its key (a value of the group-by
// field, or "overdue" / a YYYY-MM-DD day for an agenda; "" collects the rows
// without the key), the rows of the group and the entries shown of them.
type dashboardGroup struct {
	Key     string           `json:"key"`
	Total   int              `json:"total"`
	Entries []dashboardEntry `json:"entries,omitempty"`
}

type resolvedDashboard struct {
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Widgets     []resolvedWidget `json:"widgets"`
}

// dashRow is one candidate entry of a source, with the accessors a filter, a
// sort and a group need.
type dashRow struct {
	out      dashboardEntry
	age      int // -1 when unknown
	hasItems bool
	get      func(key string) (any, bool)
}

// resolveDashboard resolves every widget of d for ctx's caller. Each source
// is read once and shared by the widgets that use it.
func resolveDashboard(ctx requestContext, k *kb.KB, cc *conformanceCache, d kb.Dashboard) resolvedDashboard {
	return resolveDashboardAt(ctx, k, cc, d, lint.Now().Local())
}

// resolveDashboardAt is resolveDashboard with the server clock given: an
// agenda compares dates to now's day in now's own zone, never to the caller's.
func resolveDashboardAt(ctx requestContext, k *kb.KB, cc *conformanceCache, d kb.Dashboard, now time.Time) resolvedDashboard {
	res := resolvedDashboard{Title: d.Title, Description: d.Description, Widgets: make([]resolvedWidget, 0, len(d.Widgets))}
	src := &dashSources{ctx: ctx, k: k, cc: cc, conceptKeys: conceptFieldKeys(d), now: now}
	for _, w := range d.Widgets {
		res.Widgets = append(res.Widgets, resolveWidget(src, w))
	}
	return res
}

// dashSources loads each source at most once per dashboard.
type dashSources struct {
	ctx         requestContext
	k           *kb.KB
	cc          *conformanceCache
	conceptKeys []string
	now         time.Time

	work, concepts         []dashRow
	workErr, conceptErr    error
	workDone, conceptsDone bool
}

func (s *dashSources) rows(source string) ([]dashRow, error) {
	if source == "concepts" {
		if !s.conceptsDone {
			s.concepts, s.conceptErr = loadConceptRows(s.ctx, s.k, s.conceptKeys)
			s.conceptsDone = true
		}
		return s.concepts, s.conceptErr
	}
	if !s.workDone {
		s.work, s.workErr = loadWorkRows(s.ctx, s.k, s.cc)
		s.workDone = true
	}
	return s.work, s.workErr
}

func loadWorkRows(ctx requestContext, k *kb.KB, cc *conformanceCache) ([]dashRow, error) {
	entries, err := visibleWork(ctx, k, cc)
	if err != nil {
		return nil, err
	}
	sortWork(entries, nil)
	rows := make([]dashRow, 0, len(entries))
	for _, e := range entries {
		e := e
		out := dashboardEntry{ID: e.ID, Title: e.Title, Type: e.Type, Map: e.Map, Status: e.Status, State: e.State,
			Reason: e.Reason, AgeDays: e.AgeDays, Stale: e.Stale, ItemCount: len(e.Items)}
		if n := len(e.Items); n > 0 {
			if n > dashboardItemPreview {
				n = dashboardItemPreview
			}
			out.Items = e.Items[:n]
		}
		age := -1
		if e.AgeDays != nil {
			age = *e.AgeDays
		}
		rows = append(rows, dashRow{out: out, age: age, hasItems: len(e.Items) > 0, get: func(key string) (any, bool) {
			if e.Frontmatter == nil {
				return nil, false
			}
			return e.Frontmatter.Get(key)
		}})
	}
	return rows, nil
}

func loadConceptRows(ctx requestContext, k *kb.KB, keys []string) ([]dashRow, error) {
	res, err := queryConcepts(k, ConceptQuery{Fields: keys, Include: visiblePredicate(ctx, k)})
	if err != nil {
		return nil, err
	}
	rows := make([]dashRow, 0, len(res.Entries))
	for _, e := range res.Entries {
		e := e
		out := dashboardEntry{ID: e.ID, Title: e.Title, Type: e.Type, Map: e.Collection, Status: e.Status}
		rows = append(rows, dashRow{out: out, age: -1, get: func(key string) (any, bool) {
			v, ok := e.Fields[key]
			return v, ok
		}})
	}
	return rows, nil
}

// widgetFieldKeys are the frontmatter keys one widget reads: its field
// filter, its sort and group keys and its date field.
func widgetFieldKeys(w kb.DashboardWidget) []string {
	var keys []string
	if w.Filter != nil {
		for key := range w.Filter.Fields {
			keys = append(keys, key)
		}
	}
	for _, spec := range []string{w.Sort, w.GroupBy} {
		if key, ok := strings.CutPrefix(spec, "field:"); ok {
			keys = append(keys, key)
		}
	}
	if w.DateField != "" {
		keys = append(keys, w.DateField)
	}
	sort.Strings(keys)
	return keys
}

// conceptFieldKeys is the union of the keys the widgets of the concepts source
// read, plus the two every entry carries.
func conceptFieldKeys(d kb.Dashboard) []string {
	seen := map[string]bool{"waiting_on": true, "review_after": true}
	keys := []string{"waiting_on", "review_after"}
	for _, w := range d.Widgets {
		if w.Source != "concepts" {
			continue
		}
		for _, key := range widgetFieldKeys(w) {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	return keys
}

func resolveWidget(src *dashSources, w kb.DashboardWidget) resolvedWidget {
	out := resolvedWidget{DashboardWidget: w}
	fail := func(format string, a ...any) resolvedWidget {
		out.Problem = fmt.Sprintf(format, a...)
		out.Entries, out.Total = nil, 0
		return out
	}
	if w.Problem != "" {
		return fail("%s", w.Problem)
	}
	switch w.Type {
	case "note":
		return out // text only: nothing to resolve
	case "kanban", "list", "stat", "agenda":
	default:
		return fail("widget type %q is not resolved", w.Type)
	}
	source := w.Source
	if source == "" {
		source = "work"
	}
	if source != "work" && source != "concepts" {
		return fail("unknown source %q", source)
	}
	if source == "concepts" {
		if w.Filter != nil && (w.Filter.Stale != nil || w.Filter.HasItems != nil) {
			return fail("filter stale and has_items need source work")
		}
		if w.Sort == "age" {
			return fail("sort age needs source work")
		}
	}
	match, err := newDashFilter(w.Filter)
	if err != nil {
		return fail("%v", err)
	}
	rows, err := src.rows(source)
	if err != nil {
		return fail("%s source: %s", source, relPathErr(err, src.k.Root))
	}
	picked := make([]dashRow, 0, len(rows))
	for _, r := range rows {
		if match(r) {
			picked = append(picked, r)
		}
	}
	sortDashRows(picked, w.Sort)
	limit := w.Limit
	if limit <= 0 {
		limit = dashboardDefaultLimit
	}
	if limit > dashboardMaxLimit {
		limit = dashboardMaxLimit
	}
	readKeys := append(widgetFieldKeys(w), "waiting_on", "review_after")
	shape := func(rows []dashRow) []dashboardEntry {
		if len(rows) > limit {
			rows = rows[:limit]
		}
		entries := make([]dashboardEntry, 0, len(rows))
		for _, r := range rows {
			e := r.out
			for _, key := range readKeys {
				if v, ok := r.get(key); ok {
					if s, ok := fieldText(v); ok {
						if e.Fields == nil {
							e.Fields = map[string]string{}
						}
						e.Fields[key] = s
					}
				}
			}
			entries = append(entries, e)
		}
		return entries
	}
	groupBy := w.GroupBy
	if groupBy == "" && w.Type == "kanban" {
		groupBy = "status" // a kanban is columns; by default the status ones
	}
	switch {
	case w.Type == "agenda":
		for _, g := range agendaGroups(picked, w, src.now) {
			out.Total += len(g.rows)
			out.Groups = append(out.Groups, dashboardGroup{Key: g.key, Total: len(g.rows), Entries: shape(g.rows)})
		}
	case groupBy != "" && (w.Type == "kanban" || w.Type == "list" || (w.Type == "stat" && w.Show == "groups")):
		for _, g := range groupDashRows(picked, groupBy, w.Columns) {
			out.Total += len(g.rows)
			dg := dashboardGroup{Key: g.key, Total: len(g.rows)}
			if w.Type != "stat" {
				dg.Entries = shape(g.rows)
			}
			out.Groups = append(out.Groups, dg)
		}
	case w.Type == "stat" && w.Show == "groups":
		return fail("show groups needs group_by")
	default:
		out.Total = len(picked)
		if w.Type != "stat" {
			out.Entries = shape(picked)
		}
	}
	return out
}

type dashRowGroup struct {
	key  string
	rows []dashRow
}

// groupKey is the value of a row for a group_by spec ("" when it has none).
func groupKey(r dashRow, spec string) string {
	switch {
	case spec == "status":
		return r.out.Status
	case spec == "map":
		return r.out.Map
	case spec == "type":
		return r.out.Type
	}
	if key, ok := strings.CutPrefix(spec, "field:"); ok {
		if v, ok := r.get(key); ok {
			s, _ := fieldText(v)
			return s
		}
	}
	return ""
}

// groupDashRows groups rows by spec in order of first appearance, so the
// widget's sort orders the groups. With columns, only those groups are kept,
// in that order and all present even when empty: a row outside them is left
// out of every count (the total of a kanban is what it shows).
func groupDashRows(rows []dashRow, spec string, columns []string) []dashRowGroup {
	var groups []dashRowGroup
	idx := map[string]int{}
	for _, c := range columns {
		if _, dup := idx[c]; !dup {
			idx[c] = len(groups)
			groups = append(groups, dashRowGroup{key: c})
		}
	}
	for _, r := range rows {
		key := groupKey(r, spec)
		i, ok := idx[key]
		if !ok {
			if len(columns) > 0 {
				continue
			}
			i = len(groups)
			idx[key] = i
			groups = append(groups, dashRowGroup{key: key})
		}
		groups[i].rows = append(groups[i].rows, r)
	}
	return groups
}

const (
	agendaDefaultHorizon = 14
	agendaOverdue        = "overdue"
)

// agendaGroups buckets rows by the day of their date field: "overdue" first,
// then each day up to the horizon, ascending. A row without a readable date,
// or past the horizon, is left out. Days are calendar days of now's zone.
func agendaGroups(rows []dashRow, w kb.DashboardWidget, now time.Time) []dashRowGroup {
	field := w.DateField
	if field == "" {
		field = "review_after"
	}
	horizon := w.HorizonDays
	if horizon <= 0 {
		horizon = agendaDefaultHorizon
	}
	const layout = "2006-01-02"
	today := now.Format(layout)
	last := time.Date(now.Year(), now.Month(), now.Day()+horizon, 0, 0, 0, 0, time.UTC).Format(layout)
	byDay := map[string][]dashRow{}
	var days []string
	var overdue []dashRow
	for _, r := range rows {
		v, ok := r.get(field)
		if !ok {
			continue
		}
		text, ok := fieldText(v)
		if !ok || len(text) < len(layout) {
			continue
		}
		day := text[:len(layout)]
		if _, err := time.Parse(layout, day); err != nil {
			continue
		}
		switch {
		case day < today:
			overdue = append(overdue, r)
		case day <= last:
			if _, seen := byDay[day]; !seen {
				days = append(days, day)
			}
			byDay[day] = append(byDay[day], r)
		}
	}
	sort.Strings(days)
	var groups []dashRowGroup
	if len(overdue) > 0 {
		groups = append(groups, dashRowGroup{key: agendaOverdue, rows: overdue})
	}
	for _, d := range days {
		groups = append(groups, dashRowGroup{key: d, rows: byDay[d]})
	}
	return groups
}

// fieldText renders a scalar or list frontmatter value as text; other shapes
// are left out.
func fieldText(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case []string:
		return strings.Join(x, ", "), true
	}
	return "", false
}

// newDashFilter turns a widget filter into a row predicate. The field filter
// is capped at maxWorkListFields keys.
func newDashFilter(f *kb.DashboardFilter) (func(dashRow) bool, error) {
	if f == nil {
		return func(dashRow) bool { return true }, nil
	}
	if n := len(f.Fields); n > maxWorkListFields {
		return nil, fmt.Errorf("filter fields: %d keys: at most %d", n, maxWorkListFields)
	}
	text := strings.ToLower(f.Text)
	return func(r dashRow) bool {
		e := r.out
		if len(f.Status) > 0 && !containsStr(f.Status, e.Status) && !(e.State != "" && containsStr(f.Status, e.State)) {
			return false
		}
		if len(f.Map) > 0 && !containsStr(f.Map, e.Map) {
			return false
		}
		if len(f.Type) > 0 && !containsStr(f.Type, e.Type) {
			return false
		}
		if f.IDPrefix != "" && !strings.HasPrefix(e.ID, f.IDPrefix) {
			return false
		}
		if f.Stale != nil && e.Stale != *f.Stale {
			return false
		}
		if f.HasItems != nil && r.hasItems != *f.HasItems {
			return false
		}
		for key, want := range f.Fields {
			v, ok := r.get(key)
			if !ok || !frontmatterValueMatches(v, want) {
				return false
			}
		}
		if text != "" && !strings.Contains(strings.ToLower(e.Title+" "+e.ID), text) {
			return false
		}
		return true
	}, nil
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// sortDashRows orders rows by the widget's sort key, stably over the source's
// own order; a row without the key goes last. "" keeps the source order.
func sortDashRows(rows []dashRow, spec string) {
	if spec == "" {
		return
	}
	text := func(r dashRow) (string, bool) {
		switch {
		case spec == "title":
			return strings.ToLower(r.out.Title), r.out.Title != ""
		case spec == "status":
			return r.out.Status, r.out.Status != ""
		case strings.HasPrefix(spec, "field:"):
			v, ok := r.get(strings.TrimPrefix(spec, "field:"))
			if !ok {
				return "", false
			}
			return fieldText(v)
		}
		return "", false
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if spec == "age" { // oldest first
			if (a.age < 0) != (b.age < 0) {
				return a.age >= 0
			}
			return a.age > b.age
		}
		av, aok := text(a)
		bv, bok := text(b)
		if aok != bok {
			return aok
		}
		return av < bv
	})
}
