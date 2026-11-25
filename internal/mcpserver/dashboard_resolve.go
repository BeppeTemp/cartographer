package mcpserver

import (
	"fmt"
	"sort"
	"strings"

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
	Total   int              `json:"total"`
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
	res := resolvedDashboard{Title: d.Title, Description: d.Description, Widgets: make([]resolvedWidget, 0, len(d.Widgets))}
	src := &dashSources{ctx: ctx, k: k, cc: cc, conceptKeys: conceptFieldKeys(d)}
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
	out.Total = len(picked)
	limit := w.Limit
	if limit <= 0 {
		limit = dashboardDefaultLimit
	}
	if limit > dashboardMaxLimit {
		limit = dashboardMaxLimit
	}
	if len(picked) > limit {
		picked = picked[:limit]
	}
	readKeys := append(widgetFieldKeys(w), "waiting_on", "review_after")
	out.Entries = make([]dashboardEntry, 0, len(picked))
	for _, r := range picked {
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
		out.Entries = append(out.Entries, e)
	}
	return out
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
