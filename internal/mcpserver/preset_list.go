package mcpserver

import (
	"encoding/json"
	"sort"
)

// PresetTemplate is a template of a preset as preset_list returns it.
type PresetTemplate struct {
	Slug    string `json:"slug"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
}

// PresetDashboard is a dashboard of a preset as preset_list returns it: Ops is
// the exact ops array of one dashboard_patch {name, if_match: "", ops} call.
type PresetDashboard struct {
	Name        string          `json:"name"`
	Path        string          `json:"path"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Ops         json.RawMessage `json:"ops,omitempty"`
}

// PresetEntry is one preset of a pinned plugin as preset_list returns it.
type PresetEntry struct {
	ID            string            `json:"id"` // <plugin>/<name>
	PluginVersion string            `json:"plugin_version"`
	Description   string            `json:"description"`
	Kind          string            `json:"kind,omitempty"`   // absent for a dashboard-only preset
	Create        json.RawMessage   `json:"create,omitempty"` // absent for a dashboard-only preset
	Templates     []PresetTemplate  `json:"templates"`
	Dashboards    []PresetDashboard `json:"dashboards"`
	Truncated     bool              `json:"truncated,omitempty"`
}

// presetListCap bounds the whole preset_list result; a preset past it is
// listed without its template contents and its dashboard ops.
const presetListCap = 256 << 10

func toolPresetList(entries []PresetEntry) Tool {
	sorted := append([]PresetEntry{}, entries...)
	for i := range sorted {
		if sorted[i].Dashboards == nil {
			sorted[i].Dashboards = []PresetDashboard{}
		}
		if sorted[i].Templates == nil {
			sorted[i].Templates = []PresetTemplate{}
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	used := 0
	for i := range sorted {
		size := len(sorted[i].Create)
		for _, t := range sorted[i].Templates {
			size += len(t.Content)
		}
		for _, d := range sorted[i].Dashboards {
			size += len(d.Ops)
		}
		if used+size > presetListCap {
			bare := make([]PresetTemplate, len(sorted[i].Templates))
			for j, t := range sorted[i].Templates {
				bare[j] = PresetTemplate{Slug: t.Slug, Path: t.Path}
			}
			sorted[i].Templates, sorted[i].Truncated = bare, true
			if len(sorted[i].Dashboards) > 0 {
				ds := make([]PresetDashboard, len(sorted[i].Dashboards))
				for j, d := range sorted[i].Dashboards {
					d.Ops = nil
					ds[j] = d
				}
				sorted[i].Dashboards = ds
			}
			continue
		}
		used += size
	}
	return Tool{
		Name: "preset_list", ReadOnly: true,
		Description: "Lists the presets the server's pinned plugins supply: a map contract (create: the map_create arguments, minus name) and its templates (write each at its path with artifact_write, then map_create). dashboards: for each, call dashboard_patch {name, if_match: \"\", ops} with the ops given (create only: an existing name is refused, pick another); a preset without create has no map to create. Server data, the same for every caller.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			return jsonResult(sorted), nil
		},
	}
}
