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

// PresetEntry is one preset of a pinned plugin as preset_list returns it.
type PresetEntry struct {
	ID            string           `json:"id"` // <plugin>/<name>
	PluginVersion string           `json:"plugin_version"`
	Description   string           `json:"description"`
	Kind          string           `json:"kind"`
	Create        json.RawMessage  `json:"create"`
	Templates     []PresetTemplate `json:"templates"`
	Truncated     bool             `json:"truncated,omitempty"`
}

// presetListCap bounds the whole preset_list result; a preset past it is
// listed without its template contents.
const presetListCap = 256 << 10

func toolPresetList(entries []PresetEntry) Tool {
	sorted := append([]PresetEntry{}, entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	used := 0
	for i := range sorted {
		size := len(sorted[i].Create)
		for _, t := range sorted[i].Templates {
			size += len(t.Content)
		}
		if used+size > presetListCap {
			bare := make([]PresetTemplate, len(sorted[i].Templates))
			for j, t := range sorted[i].Templates {
				bare[j] = PresetTemplate{Slug: t.Slug, Path: t.Path}
			}
			sorted[i].Templates, sorted[i].Truncated = bare, true
			continue
		}
		used += size
	}
	return Tool{
		Name: "preset_list", ReadOnly: true,
		Description: "Lists the presets the server's pinned plugins supply: a map contract (create: the map_create arguments, minus name) and its templates (write each at its path with artifact_write, then map_create). Server data, the same for every caller.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			return jsonResult(sorted), nil
		},
	}
}
