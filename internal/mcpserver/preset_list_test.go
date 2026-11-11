package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func presetListResult(t *testing.T, deps Deps) []PresetEntry {
	t.Helper()
	s := New("test")
	RegisterKBTools(s, setupTestKB(t), deps)
	tr := decodeToolResult(t, runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "preset_list", map[string]any{})})[1])
	if tr.IsError {
		t.Fatalf("preset_list: %+v", tr.Content)
	}
	var out []PresetEntry
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPresetListEmptyWithoutPlugins(t *testing.T) {
	t.Parallel()
	if got := presetListResult(t, Deps{}); len(got) != 0 {
		t.Fatalf("no plugin lists nothing, got %+v", got)
	}
	if ToolRequiresWrite("preset_list") || !ToolAdvanced("preset_list") {
		t.Error("preset_list is read-only and advanced")
	}
	if resourceClassForTool("preset_list") == "" {
		t.Error("preset_list has no resource class")
	}
}

func TestPresetListSortedAndCapped(t *testing.T) {
	t.Parallel()
	create := json.RawMessage(`{"title":"Runbooks","kind":"map","templates":["runbook"]}`)
	big := strings.Repeat("x", presetListCap-1000)
	in := []PresetEntry{
		{ID: "team/zeta", PluginVersion: "1.0.0", Kind: "map", Create: create, Templates: []PresetTemplate{{Slug: "runbook", Path: "templates/runbook.md", Content: big}}},
		{ID: "team/alpha", PluginVersion: "1.0.0", Kind: "map", Create: create, Templates: []PresetTemplate{{Slug: "runbook", Path: "templates/runbook.md", Content: "small"}}},
		{ID: "team/mid", PluginVersion: "1.0.0", Kind: "map", Create: create, Templates: []PresetTemplate{{Slug: "runbook", Path: "templates/runbook.md", Content: big}}},
	}
	got := presetListResult(t, Deps{Presets: in})
	if len(got) != 3 || got[0].ID != "team/alpha" || got[1].ID != "team/mid" || got[2].ID != "team/zeta" {
		t.Fatalf("order: %+v", got)
	}
	if got[0].Truncated || got[0].Templates[0].Content != "small" {
		t.Errorf("a preset within the cap keeps its content: %+v", got[0])
	}
	if got[1].Truncated || got[1].Templates[0].Content != big {
		t.Error("the first preset that fits keeps its content")
	}
	if !got[2].Truncated || got[2].Templates[0].Content != "" || got[2].Templates[0].Path != "templates/runbook.md" {
		t.Errorf("a preset past the cap is listed without content: %+v", got[2])
	}
}
