package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
)

const presetDashboardFile = "title: Week\ndescription: What moves.\nwidgets:\n  - id: open\n    type: list\n  - id: memo\n    type: note\n    text: hello\n"

// presetDashboardEntry builds the entry a loaded plugin yields, with the same
// conversion the server uses (kb.DashboardFileOps).
func presetDashboardEntry(t *testing.T, id, create string) PresetEntry {
	t.Helper()
	title, desc, ops, err := kb.DashboardFileOps([]byte(presetDashboardFile))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	e := PresetEntry{ID: id, PluginVersion: "1.0.0", Templates: []PresetTemplate{}, Dashboards: []PresetDashboard{{Name: "week", Path: "dashboards/week.yaml", Title: title, Description: desc, Ops: raw}}}
	if create != "" {
		e.Kind, e.Create = "map", json.RawMessage(create)
	}
	return e
}

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

func TestPresetListDashboardOnlyEntry(t *testing.T) {
	t.Parallel()
	got := presetListResult(t, Deps{Presets: []PresetEntry{presetDashboardEntry(t, "team/planner", "")}})
	if len(got) != 1 || got[0].Kind != "" || got[0].Create != nil || len(got[0].Dashboards) != 1 || len(got[0].Dashboards[0].Ops) == 0 {
		t.Fatalf("dashboard-only entry: %+v", got)
	}
	s := New("test")
	RegisterKBTools(s, setupTestKB(t), Deps{Presets: []PresetEntry{presetDashboardEntry(t, "team/planner", "")}})
	text := callTool(t, s, "preset_list", `{}`).Content[0].Text
	if strings.Contains(text, `"kind"`) || strings.Contains(text, `"create"`) || !strings.Contains(text, `"templates":[]`) {
		t.Errorf("kind and create are absent, templates is an empty array: %s", text)
	}
}

// AC-plugdash-7: the result is server data, the same for every principal that
// may call it (a map-narrowed one is refused, whole-KB resource class), and the
// tool stays read-only.
func TestPresetListSamePerPrincipal(t *testing.T) {
	t.Parallel()
	s := New("test")
	k := kbtest.New(t)
	k.AuthName = "docs"
	RegisterKBTools(s, k, Deps{Presets: []PresetEntry{presetDashboardEntry(t, "team/planner", `{"title":"Planner","kind":"map"}`)}})
	whole := callTool(t, s, "preset_list", `{}`)
	if whole.IsError || !strings.Contains(whole.Content[0].Text, `"dashboards":[{"name":"week"`) {
		t.Fatalf("whole reader: %+v", whole)
	}
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}})
	narrow := s.callTool(ctx, "preset_list", json.RawMessage(`{}`))
	if !narrow.IsError || !strings.Contains(narrow.Content[0].Text, "needs_whole_kb") || strings.Contains(narrow.Content[0].Text, "dashboards") {
		t.Errorf("a map-narrowed principal gets no preset content: %+v", narrow)
	}
	again := callTool(t, s, "preset_list", `{}`)
	if again.Content[0].Text != whole.Content[0].Text {
		t.Error("the result differs between two whole-KB callers")
	}
	if ToolRequiresWrite("preset_list") {
		t.Error("preset_list must stay read-only")
	}
}

// AC-plugdash-8: past the cap a preset lists its dashboards without ops.
func TestPresetListCapDropsDashboardOps(t *testing.T) {
	t.Parallel()
	fit := presetDashboardEntry(t, "team/a", `{"title":"A","kind":"map"}`)
	over := presetDashboardEntry(t, "team/b", `{"title":"B","kind":"map"}`)
	over.Dashboards[0].Ops = json.RawMessage(`"` + strings.Repeat("x", presetListCap) + `"`)
	got := presetListResult(t, Deps{Presets: []PresetEntry{over, fit}})
	if got[0].Truncated || len(got[0].Dashboards[0].Ops) == 0 {
		t.Errorf("a preset within the cap keeps its ops: %+v", got[0])
	}
	d := got[1].Dashboards[0]
	if !got[1].Truncated || len(d.Ops) != 0 || d.Name != "week" || d.Path != "dashboards/week.yaml" || d.Title != "Week" {
		t.Errorf("a preset past the cap lists its dashboards without ops: %+v", got[1])
	}
}

// the ops preset_list returns are what dashboard_patch accepts to create the
// dashboard; a second apply of the name is refused and writes nothing.
func TestPresetListOpsApplyWithDashboardPatch(t *testing.T) {
	t.Parallel()
	s, k := workServer(t)
	_ = k
	e := presetDashboardEntry(t, "team/planner", `{"title":"Planner","kind":"map"}`)
	got := presetListResult(t, Deps{Presets: []PresetEntry{e}})
	ops := string(got[0].Dashboards[0].Ops)
	if res := callTool(t, s, "dashboard_patch", patchArgs("week", "", ops)); res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	text := callTool(t, s, "dashboard_get", `{"name":"week"}`).Content[0].Text
	for _, want := range []string{`"title":"Week"`, `"id":"open"`, `"id":"memo"`, `hello`} {
		if !strings.Contains(text, want) {
			t.Errorf("dashboard_get lacks %s: %s", want, text)
		}
	}
	if res := callTool(t, s, "dashboard_patch", patchArgs("week", "", ops)); !res.IsError || !strings.Contains(res.Content[0].Text, "stale_write") {
		t.Errorf("a second apply must be refused: %+v", res)
	}
}
