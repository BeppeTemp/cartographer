package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

const toolTestDashboard = "title: Week\nwidgets:\n  - id: open\n    type: list\n  - id: memo\n    type: note\n    text: private words\n"

func writeDashboardFile(t *testing.T, k *kb.KB, name, content string) {
	t.Helper()
	dir := filepath.Join(k.Root, "dashboards")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dashboardServer(t *testing.T) (*Server, *kb.KB) {
	t.Helper()
	s, k := workServer(t)
	writeDashboardFile(t, k, "week", toolTestDashboard)
	return s, k
}

// TestDashboardToolsAreAdvanced: the two read tools are advanced, read-only,
// registered and of the collection class; the write names are not read-only.
func TestDashboardToolsAreAdvanced(t *testing.T) {
	t.Parallel()
	// The write names join advancedToolNames with their tools: the registry
	// test refuses a name no tool carries.
	for _, n := range []string{"dashboard_list", "dashboard_get"} {
		if !ToolAdvanced(n) {
			t.Errorf("%s must be advanced", n)
		}
	}
	s, _ := workServer(t)
	for _, n := range []string{"dashboard_list", "dashboard_get"} {
		tool, ok := s.Tools()[n]
		if !ok || !tool.ReadOnly || !client.ReadOnlyToolNames[n] || resourceClassForTool(n) != resourceCollection {
			t.Errorf("%s: registered=%v readonly=%v", n, ok, tool.ReadOnly)
		}
	}
	for _, n := range []string{"dashboard_patch", "dashboard_delete"} {
		if client.ReadOnlyToolNames[n] {
			t.Errorf("%s must not be read-only", n)
		}
	}
}

// TestDashboardGetMissingIsEmpty: an unknown name is an empty dashboard with
// hash ""; a KB the caller cannot read is refused by the policy gate.
func TestDashboardGetMissingIsEmpty(t *testing.T) {
	t.Parallel()
	s, _ := dashboardServer(t)
	res := callTool(t, s, "dashboard_get", `{"name":"nothing-here"}`)
	if res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	var out struct {
		Name    string            `json:"name"`
		Hash    string            `json:"hash"`
		Widgets []json.RawMessage `json:"widgets"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "nothing-here" || out.Hash != "" || out.Widgets == nil || len(out.Widgets) != 0 {
		t.Fatalf("not empty: %s", res.Content[0].Text)
	}
	if res := callTool(t, s, "dashboard_get", `{"name":"../x"}`); !res.IsError {
		t.Fatal("a bad name was accepted")
	}
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "other"}}})
	if res := s.callTool(ctx, "dashboard_get", json.RawMessage(`{"name":"week"}`)); !res.IsError || strings.Contains(res.Content[0].Text, "private") {
		t.Fatalf("a caller with no access to the KB read: %+v", res)
	}
}

func TestDashboardListAndGet(t *testing.T) {
	t.Parallel()
	s, k := dashboardServer(t)
	writeDashboardFile(t, k, "bad", "title: [unclosed\n")
	res := callTool(t, s, "dashboard_list", `{}`)
	var list struct {
		Dashboards []dashboardListEntry `json:"dashboards"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Dashboards) != 2 || list.Dashboards[0].Name != "bad" || list.Dashboards[0].Error == "" {
		t.Fatalf("list: %s", res.Content[0].Text)
	}
	w := list.Dashboards[1]
	if w.Name != "week" || w.Title != "Week" || w.Widgets != 2 || w.Hash != kb.DashboardHash([]byte(toolTestDashboard)) {
		t.Fatalf("week: %+v", w)
	}
	var got dashboardGetResult
	res = callTool(t, s, "dashboard_get", `{"name":"week"}`)
	if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
		t.Fatal(err)
	}
	if got.Hash != w.Hash || len(got.Widgets) != 2 || got.Widgets[0].Total != 4 || got.Widgets[1].Text != "private words" {
		t.Fatalf("get: %s", res.Content[0].Text)
	}
	if res := callTool(t, s, "dashboard_get", `{"name":"bad"}`); !res.IsError {
		t.Fatal("a file that is not YAML must be an error")
	}
}

const narrowTestDashboard = "title: Quarter plan\ndescription: for hush-team secretword\nwidgets:\n" +
	"  - id: open\n    type: list\n    title: Visible only\n    width: 2\n    filter:\n      map: [visible]\n    group_by: field:owner\n" +
	"  - id: memo\n    type: note\n    title: Memo title\n    text: private words\n"

// TestDashboardNarrowedCallerGetsOnlyResolvedData: a caller that does not read
// the whole KB gets structural keys and what the resolver computed for it,
// and none of the strings an author typed or any filter definition; a whole
// reader gets all of them.
func TestDashboardNarrowedCallerGetsOnlyResolvedData(t *testing.T) {
	t.Parallel()
	s, k := workServer(t)
	writeDashboardFile(t, k, "plan", narrowTestDashboard)
	hidden := []string{"Quarter plan", "hush-team", "Visible only", "secretword", "Memo title", "private words", "field:owner", `"filter"`, `"map":["visible"]`}

	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}})
	res := s.callTool(ctx, "dashboard_get", json.RawMessage(`{"name":"plan"}`))
	if res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	text := res.Content[0].Text
	for _, h := range append(hidden, "hidden/") {
		if strings.Contains(text, h) {
			t.Fatalf("narrowed caller sees %q: %s", h, text)
		}
	}
	for _, want := range []string{`"id":"open"`, `"width":2`, `"visible/a"`, `"id":"memo"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("narrowed caller lacks %s: %s", want, text)
		}
	}
	list := s.callTool(ctx, "dashboard_list", json.RawMessage(`{}`)).Content[0].Text
	if strings.Contains(list, "Quarter") || strings.Contains(list, "hash") || !strings.Contains(list, `"name":"plan"`) {
		t.Fatalf("narrowed list: %s", list)
	}

	whole := callTool(t, s, "dashboard_get", `{"name":"plan"}`).Content[0].Text
	for _, h := range hidden[:7] {
		if !strings.Contains(whole, h) {
			t.Fatalf("whole reader lacks %q: %s", h, whole)
		}
	}
	if !strings.Contains(callTool(t, s, "dashboard_list", `{}`).Content[0].Text, "Quarter plan") {
		t.Fatal("whole reader's list lacks the title")
	}
}

// TestDashboardNarrowedCallerDoesNotSeeRawInvalidWidget: a widget that failed
// validation keeps its raw values in the tolerant read; a narrowed caller gets
// a positional id and a fixed sentence, nothing it typed.
func TestDashboardNarrowedCallerDoesNotSeeRawInvalidWidget(t *testing.T) {
	t.Parallel()
	s, k := workServer(t)
	writeDashboardFile(t, k, "odd", "widgets:\n  - id: Not A Slug\n    type: hush-type\n    icon: hush-icon\n    accent: hush-accent\n    source: hush-source\n    show: hush-show\n  - id: fine\n    type: list\n")
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}})
	res := s.callTool(ctx, "dashboard_get", json.RawMessage(`{"name":"odd"}`))
	if res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	text := res.Content[0].Text
	if strings.Contains(text, "hush") || strings.Contains(text, "Not A Slug") {
		t.Fatalf("raw invalid widget reached a narrowed caller: %s", text)
	}
	if !strings.Contains(text, `"id":"widget-1"`) || !strings.Contains(text, "cannot be drawn") || !strings.Contains(text, `"id":"fine"`) {
		t.Fatalf("placeholder missing: %s", text)
	}
	if whole := callTool(t, s, "dashboard_get", `{"name":"odd"}`).Content[0].Text; !strings.Contains(whole, "hush-type") {
		t.Fatalf("whole reader must see the raw widget: %s", whole)
	}
}
