package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/secretscan"
)

// dashboardWidgetDoc is the widget field list both read tools carry, so an
// agent that has only seen one of them can still write a dashboard.
const dashboardWidgetDoc = "Widget fields: id (slug), type (kanban|list|stat|agenda|note|table|timeline|links), title, icon, accent, width 1-4, " +
	"source (work, default | concepts), filter {status, map, type, id_prefix, stale, has_items, fields {key: value}, text}, " +
	"group_by (status|map|type|field:<key>), columns (kanban: values of group_by; table: field names), sort (age|title|status|field:<key>), limit, " +
	"text (note), date_field (agenda, timeline) and horizon_days (agenda), show (stat: count|groups), ids (links: concept ids of this KB, no filter)."

// dashboardListEntry is one dashboard of dashboard_list.
type dashboardListEntry struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Widgets int    `json:"widgets,omitempty"`
	Hash    string `json:"hash,omitempty"`
	Error   string `json:"error,omitempty"`
}

// dashboardGetResult is the resolved dashboard plus its name and the hash of
// its file ("" when it does not exist yet).
type dashboardGetResult struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
	resolvedDashboard
}

// narrowDashboard keeps, for a caller that does not read the whole KB, what
// the resolver computed for it plus structural keys: the widget's id, type,
// size and closed-list look, and its resolved entries, groups and counts.
// Every string an author typed and every filter or grouping definition is
// omitted (they can name a map or a value the caller cannot see): the
// title, description, note text, filter, group_by, columns, sort and
// date_field. A widget that failed validation is reduced to a positional id and a fixed sentence.
func narrowDashboard(d resolvedDashboard) resolvedDashboard {
	out := resolvedDashboard{Widgets: make([]resolvedWidget, 0, len(d.Widgets))}
	for i, w := range d.Widgets {
		if w.Problem != "" {
			// A widget that failed validation keeps its raw values (an unknown
			// type, an id that is no slug): none of them reaches this caller,
			// only its position and the fixed sentence.
			var n resolvedWidget
			n.ID, n.Problem = fmt.Sprintf("widget-%d", i+1), "this widget cannot be drawn"
			out.Widgets = append(out.Widgets, n)
			continue
		}
		entries := w.Entries
		if w.Type == "links" {
			// The ids are author-typed: a missing entry keeps none, so a
			// narrowed caller learns no id it could not read.
			entries = append([]dashboardEntry(nil), entries...)
			for j := range entries {
				if entries[j].State == "missing" {
					entries[j].ID = ""
				}
			}
		}
		n := resolvedWidget{Entries: entries, Groups: w.Groups, Total: w.Total}
		n.ID, n.Type, n.Icon, n.Accent, n.Width = w.ID, w.Type, w.Icon, w.Accent, w.Width
		n.Source, n.Limit, n.HorizonDays, n.Show = w.Source, w.Limit, w.HorizonDays, w.Show
		out.Widgets = append(out.Widgets, n)
	}
	return out
}

// toolDashboardList names the dashboards of the KB (dashboards/<name>.yaml).
// It reads files, not concepts, so nothing in it depends on what the caller
// may read of the KB's pages.
func toolDashboardList(k *kb.KB) Tool {
	return Tool{
		Name:        "dashboard_list",
		ReadOnly:    true,
		Description: "Lists the KB's dashboards: name, title, widget count and file hash. A dashboard is a file of widgets that hold queries over the KB; dashboard_get resolves one. " + dashboardWidgetDoc,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			names, err := k.ListDashboards()
			if err != nil {
				return errorResult("dashboard_list: " + relPathErr(err, k.Root)), nil
			}
			out := make([]dashboardListEntry, 0, len(names))
			whole := WholeVisible(ctx, k, false)
			for _, n := range names {
				e := dashboardListEntry{Name: n}
				if !whole {
					// Names only: a title, a count and the hash of the file
					// are the author's text, not the caller's data.
					out = append(out, dashboardListEntry{Name: n})
					continue
				}
				data, err := k.ReadDashboard(n)
				if err != nil {
					e.Error = relPathErr(err, k.Root)
					out = append(out, e)
					continue
				}
				e.Hash = kb.DashboardHash(data)
				if d, perr := kb.ParseDashboard(data); perr != nil {
					e.Error = perr.Error()
				} else {
					e.Title, e.Widgets = d.Title, len(d.Widgets)
				}
				out = append(out, e)
			}
			return jsonResult(map[string]any{"dashboards": out}), nil
		},
	}
}

// toolDashboardGet returns one dashboard with its widgets resolved for the
// caller. A name with no file answers an empty dashboard with hash "", the
// starting point of a first patch.
func toolDashboardGet(k *kb.KB, cc *conformanceCache) Tool {
	return Tool{
		Name:        "dashboard_get",
		ReadOnly:    true,
		Description: "Returns dashboard <name> resolved for you: each widget with its entries (or groups) and total, or its own error; a name with no file is an empty dashboard with hash \"\". Entries are only what you may read. See dashboard_list. " + dashboardWidgetDoc,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var p struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if !kb.ValidDashboardName(p.Name) {
				return errorResult(fmt.Sprintf("dashboard_get: invalid name %q: want a lowercase slug of at most 48 characters", p.Name)), nil
			}
			data, err := k.ReadDashboard(p.Name)
			switch {
			case errors.Is(err, os.ErrNotExist):
				return jsonResult(dashboardGetResult{Name: p.Name, resolvedDashboard: resolvedDashboard{Widgets: []resolvedWidget{}}}), nil
			case err != nil:
				return errorResult("dashboard_get: " + relPathErr(err, k.Root)), nil
			}
			d, err := kb.ParseDashboard(data)
			if err != nil {
				return errorResult("dashboard_get: " + p.Name + ": " + err.Error()), nil
			}
			res := dashboardGetResult{Name: p.Name, Hash: kb.DashboardHash(data), resolvedDashboard: resolveDashboard(ctx, k, cc, d)}
			if !WholeVisible(ctx, k, false) {
				res.Hash = ""
				res.resolvedDashboard = narrowDashboard(res.resolvedDashboard)
			}
			return jsonResult(res), nil
		},
	}
}

// dashboardOpsDoc is the ops encoding both write tools point at.
const dashboardOpsDoc = "ops is a JSON array applied in order, all or nothing: " +
	`{"op":"add","widget":{...},"index":n?}, {"op":"update","id":"w","widget":{keys to merge, null removes a key}}, ` +
	`{"op":"remove","id":"w"}, {"op":"move","id":"w","index":n}, {"op":"set_title","title":"t"}, {"op":"set_description","description":"d"}.`

// dashboardSecretFindings names every credential-shaped string in an author
// string of the dashboard (title, description, widget titles, note text,
// link ids, filter text and field values), by place and kind, never quoting the value.
func dashboardSecretFindings(d kb.Dashboard) []string {
	var out []string
	scan := func(place, text string) {
		for _, m := range secretscan.Find(text) {
			out = append(out, fmt.Sprintf("%s, line %d: %s", place, m.Line, m.Kind))
		}
	}
	scan("dashboard title", d.Title)
	scan("dashboard description", d.Description)
	for _, w := range d.Widgets {
		scan(fmt.Sprintf("widget %q title", w.ID), w.Title)
		scan(fmt.Sprintf("widget %q text", w.ID), w.Text)
		for i, id := range w.IDs {
			scan(fmt.Sprintf("widget %q link id %d", w.ID, i+1), id)
		}
		if w.Filter != nil {
			scan(fmt.Sprintf("widget %q filter text", w.ID), w.Filter.Text)
			keys := make([]string, 0, len(w.Filter.Fields))
			for k := range w.Filter.Fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				scan(fmt.Sprintf("widget %q filter field %q", w.ID, k), w.Filter.Fields[k])
			}
		}
	}
	return out
}

// toolDashboardPatch creates or edits one dashboard with a list of ops. The
// whole-KB write class (policy.go) and the handler's own checks mean the
// caller reads every page, so its result may carry the resolved dashboard in
// full. The handler never locks or commits: gitWrap does (one commit per call).
func toolDashboardPatch(k *kb.KB, cc *conformanceCache) Tool {
	return Tool{
		Name: "dashboard_patch",
		Description: "Creates or edits dashboard <name> with ops, guarded by if_match: the hash dashboard_get returned, \"\" to create (stale_write on a mismatch, also when \"\" meets an existing file). " +
			"Returns the dashboard resolved as dashboard_get does. The error lists every problem and names the failing op; nothing is written then. Every author string (titles, description, note text, filter text and field values) is scanned for credentials and refused with the finding. " +
			dashboardOpsDoc + " " + dashboardWidgetDoc,
		InputSchema: json.RawMessage(`{"type":"object","required":["name","if_match","ops"],"properties":{"name":{"type":"string"},"if_match":{"type":"string","description":"Hash of the file as read, or \"\" to create"},"ops":{"type":"array","items":{"type":"object"}}}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var p struct {
				Name    string          `json:"name"`
				IfMatch *string         `json:"if_match"`
				Ops     json.RawMessage `json:"ops"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if !kb.ValidDashboardName(p.Name) {
				return errorResult(fmt.Sprintf("dashboard_patch: invalid name %q: want a lowercase slug of at most 48 characters", p.Name)), nil
			}
			if p.IfMatch == nil {
				return errorResult("dashboard_patch: if_match is required: the hash from dashboard_get, or \"\" to create"), nil
			}
			if len(p.Ops) == 0 {
				return errorResult("dashboard_patch: ops is required"), nil
			}
			ops, err := kb.DecodeDashboardOps(p.Ops)
			if err != nil {
				return errorResult("dashboard_patch: " + err.Error()), nil
			}
			cur, err := k.ReadDashboard(p.Name)
			exists := err == nil
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return errorResult("dashboard_patch: " + relPathErr(err, k.Root)), nil
			}
			if curHash := kb.DashboardHash(cur); *p.IfMatch != curHash {
				if !exists {
					return errorResult(fmt.Sprintf("stale_write: dashboard %q not found; pass if_match \"\" to create it", p.Name)), nil
				}
				return errorResult(fmt.Sprintf("stale_write: dashboard %q hash mismatch; current hash %s", p.Name, curHash)), nil
			}
			if !exists {
				n, err := k.CountDashboardFiles()
				if err != nil {
					return errorResult("dashboard_patch: " + relPathErr(err, k.Root)), nil
				}
				if n >= kb.MaxDashboards {
					return errorResult(fmt.Sprintf("dashboard_patch: the KB holds %d dashboard files, the cap is %d", n, kb.MaxDashboards)), nil
				}
			}
			out, err := kb.ApplyDashboardOps(cur, ops)
			if err != nil {
				return errorResult("dashboard_patch: " + err.Error()), nil
			}
			d, err := kb.ParseDashboard(out)
			if err != nil {
				return errorResult("dashboard_patch: " + err.Error()), nil
			}
			if f := dashboardSecretFindings(d); len(f) > 0 {
				return errorResult("dashboard_patch: refused, the dashboard holds what looks like a credential: " + strings.Join(f, "; ")), nil
			}
			if err := k.WriteDashboard(p.Name, out); err != nil {
				return errorResult("dashboard_patch: " + relPathErr(err, k.Root)), nil
			}
			return jsonResult(dashboardGetResult{Name: p.Name, Hash: kb.DashboardHash(out), resolvedDashboard: resolveDashboard(ctx, k, cc, d)}), nil
		},
	}
}

// toolDashboardDelete removes one dashboard file, guarded by its hash.
func toolDashboardDelete(k *kb.KB) Tool {
	return Tool{
		Name:        "dashboard_delete",
		Description: "Deletes dashboard <name>; if_match is the hash dashboard_get returned (stale_write on a mismatch, not_found when there is no such dashboard). See dashboard_patch.",
		InputSchema: json.RawMessage(`{"type":"object","required":["name","if_match"],"properties":{"name":{"type":"string"},"if_match":{"type":"string","description":"Hash of the file as read"}}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var p struct {
				Name    string `json:"name"`
				IfMatch string `json:"if_match"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if !kb.ValidDashboardName(p.Name) {
				return errorResult(fmt.Sprintf("dashboard_delete: invalid name %q: want a lowercase slug of at most 48 characters", p.Name)), nil
			}
			if p.IfMatch == "" {
				return errorResult("dashboard_delete: if_match is required: the hash from dashboard_get"), nil
			}
			cur, err := k.ReadDashboard(p.Name)
			switch {
			case errors.Is(err, os.ErrNotExist):
				return errorResult(fmt.Sprintf("not_found: dashboard %q", p.Name)), nil
			case err != nil:
				return errorResult("dashboard_delete: " + relPathErr(err, k.Root)), nil
			}
			if h := kb.DashboardHash(cur); p.IfMatch != h {
				return errorResult(fmt.Sprintf("stale_write: dashboard %q hash mismatch; current hash %s", p.Name, h)), nil
			}
			if err := k.RemoveDashboard(p.Name); err != nil {
				return errorResult("dashboard_delete: " + relPathErr(err, k.Root)), nil
			}
			return jsonResult(map[string]string{"name": p.Name, "status": "deleted"}), nil
		},
	}
}
