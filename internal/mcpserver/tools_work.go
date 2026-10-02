package mcpserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Work view limits (D302).
const (
	workListDefaultLimit = 50
	workListMaxLimit     = 200
)

// workQuery is one work_list request, shared by the tool and the UI API.
type workQuery struct {
	Scope     string
	Include   string // "all" (default), "concepts" or "items"
	Where     []conceptListFilter
	StaleOnly bool
	Fields    []string
	OrderBy   []string
	Limit     int
	Offset    int
}

// workEntryOut is a work entry as returned: the collector's fields plus the
// frontmatter values the caller named.
type workEntryOut struct {
	lint.WorkEntry
	Fields map[string]string `json:"fields,omitempty"`
}

type workResponse struct {
	Total      int            `json:"total"`
	ByStatus   map[string]int `json:"by_status"`
	ByMap      map[string]int `json:"by_map"`
	OpenItems  int            `json:"open_items"`
	Entries    []workEntryOut `json:"entries"`
	NextOffset *int           `json:"next_offset,omitempty"`
}

// newWorkQuery validates the raw arguments both surfaces receive.
func newWorkQuery(scope, include string, where []string, stale bool, fields, orderBy []string, limit, offset int) (workQuery, error) {
	q := workQuery{
		Scope:     strings.Trim(strings.ReplaceAll(scope, "\\", "/"), "/"),
		Include:   include,
		StaleOnly: stale,
		Fields:    fields,
		OrderBy:   orderBy,
		Limit:     limit,
		Offset:    offset,
	}
	switch q.Include {
	case "":
		q.Include = "all"
	case "all", "concepts", "items":
	default:
		return workQuery{}, fmt.Errorf("invalid include %q: expected concepts, items or all", include)
	}
	for _, entry := range where {
		f, err := parseConceptListFilter(entry)
		if err != nil {
			return workQuery{}, err
		}
		q.Where = append(q.Where, f)
	}
	if q.Limit <= 0 {
		q.Limit = workListDefaultLimit
	}
	if q.Limit > workListMaxLimit {
		q.Limit = workListMaxLimit
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	return q, nil
}

// visibleWork is the cached whole-KB work view narrowed to what the caller
// may see (D226). The filter runs outside the cache, so a restricted caller
// never sees a hidden concept's entry or counts from a warm cache.
func visibleWork(ctx requestContext, k *kb.KB, cc *conformanceCache) ([]lint.WorkEntry, error) {
	all, err := cc.workEntries(k)
	if err != nil {
		return nil, err
	}
	out := make([]lint.WorkEntry, 0, len(all))
	for _, e := range all {
		if Visible(ctx, k, e.ID) {
			out = append(out, e)
		}
	}
	return out, nil
}

// runWorkQuery filters, counts, orders and pages the caller's work view.
func runWorkQuery(ctx requestContext, k *kb.KB, cc *conformanceCache, q workQuery) (workResponse, error) {
	all, err := visibleWork(ctx, k, cc)
	if err != nil {
		return workResponse{}, err
	}
	var sel []lint.WorkEntry
	for _, e := range all {
		if q.Scope != "" && e.ID != q.Scope && !hasPathPrefix(e.ID, q.Scope) {
			continue
		}
		if (q.Include == "concepts" && !e.OpenPhase) || (q.Include == "items" && len(e.Items) == 0) {
			continue
		}
		if q.StaleOnly && !e.Stale {
			continue
		}
		if len(q.Where) > 0 && (e.Frontmatter == nil || !matchesConceptListFilters(e.Frontmatter, q.Where)) {
			continue
		}
		sel = append(sel, e)
	}
	sortWork(sel, q.OrderBy)

	resp := workResponse{Total: len(sel), ByStatus: map[string]int{}, ByMap: map[string]int{}, Entries: []workEntryOut{}}
	for _, e := range sel {
		status := e.Status
		if status == "" {
			status = "none"
		}
		resp.ByStatus[status]++
		resp.ByMap[e.Map]++
		resp.OpenItems += len(e.Items)
	}
	end := q.Offset + q.Limit
	if end > len(sel) {
		end = len(sel)
	}
	for i := q.Offset; i < end; i++ {
		out := workEntryOut{WorkEntry: sel[i]}
		if len(q.Fields) > 0 && sel[i].Frontmatter != nil {
			out.Fields = map[string]string{}
			for _, key := range q.Fields {
				if v, ok := scalarField(sel[i].Frontmatter, key); ok {
					out.Fields[key] = v
				}
			}
		}
		resp.Entries = append(resp.Entries, out)
	}
	if end < len(sel) {
		resp.NextOffset = &end
	}
	return resp, nil
}

// scalarField is a frontmatter value as a string, when it is a scalar: the
// server returns and orders by a KB's own fields, never interprets them.
func scalarField(fm *okf.Frontmatter, key string) (string, bool) {
	if fm == nil {
		return "", false
	}
	v, ok := fm.Get(key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// sortWork orders entries by the caller's keys (strings, missing last), then
// by the default: map, stale first, oldest timestamp (missing last), ID.
func sortWork(es []lint.WorkEntry, orderBy []string) {
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i], es[j]
		for _, key := range orderBy {
			av, aok := scalarField(a.Frontmatter, key)
			bv, bok := scalarField(b.Frontmatter, key)
			if aok != bok {
				return aok
			}
			if av != bv {
				return av < bv
			}
		}
		if a.Map != b.Map {
			return a.Map < b.Map
		}
		if a.Stale != b.Stale {
			return a.Stale
		}
		if (a.Timestamp == "") != (b.Timestamp == "") {
			return a.Timestamp != ""
		}
		if a.Timestamp != b.Timestamp {
			return a.Timestamp < b.Timestamp
		}
		return a.ID < b.ID
	})
}

// workSummary is kb_status.work for the caller.
func workSummary(entries []lint.WorkEntry) map[string]int {
	concepts, items, stale := 0, 0, 0
	for _, e := range entries {
		if e.OpenPhase {
			concepts++
		}
		items += len(e.Items)
		if e.Stale {
			stale++
		}
	}
	return map[string]int{"open_concepts": concepts, "open_items": items, "stale": stale}
}

// toolWorkList is the generic work view (D302): open-phase concepts of any
// type and unchecked items of any concept. Read-only: work changes through
// the ordinary write tools, under their gates.
func toolWorkList(k *kb.KB, cc *conformanceCache) Tool {
	return Tool{
		Name:        "work_list",
		ReadOnly:    true,
		Description: "Open work: concepts in an open status for their map, and unchecked - [ ] items in any concept, with section and line. where as in concept_list; fields and order_by name frontmatter keys. Read-only: change work with concept_patch.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"scope": {"type": "string"},
				"include": {"type": "string", "enum": ["all", "concepts", "items"]},
				"where": {"type": "array", "items": {"type": "string"}},
				"stale": {"type": "boolean"},
				"fields": {"type": "array", "items": {"type": "string"}},
				"order_by": {"type": "array", "items": {"type": "string"}},
				"limit": {"type": "integer"},
				"offset": {"type": "integer"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var p struct {
				Scope   string   `json:"scope"`
				Include string   `json:"include"`
				Where   []string `json:"where"`
				Stale   bool     `json:"stale"`
				Fields  []string `json:"fields"`
				OrderBy []string `json:"order_by"`
				Limit   int      `json:"limit"`
				Offset  int      `json:"offset"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			q, err := newWorkQuery(p.Scope, p.Include, p.Where, p.Stale, p.Fields, p.OrderBy, p.Limit, p.Offset)
			if err != nil {
				return errorResult(err.Error()), nil
			}
			resp, err := runWorkQuery(ctx, k, cc, q)
			if err != nil {
				return errorResult(fmt.Sprintf("work_list: %v", err)), nil
			}
			out, _ := json.MarshalIndent(resp, "", "  ")
			return textResult(string(out)), nil
		},
	}
}
