package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

var dashboardToolNames = []string{"dashboard_list", "dashboard_get", "dashboard_patch", "dashboard_delete"}

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
	for _, n := range dashboardToolNames {
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
		tool, ok := s.Tools()[n]
		if !ok || tool.ReadOnly || client.ReadOnlyToolNames[n] || resourceClassForTool(n) != resourceWhole || !tool.GitWrapped {
			t.Errorf("%s: registered=%v readonly=%v gitwrapped=%v", n, ok, tool.ReadOnly, tool.GitWrapped)
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

const patchAddOps = `[{"op":"set_title","title":"Week"},{"op":"add","widget":{"id":"open","type":"list","title":"Open"}}]`

func patchArgs(name, ifMatch, ops string) string {
	return `{"name":"` + name + `","if_match":"` + ifMatch + `","ops":` + ops + `}`
}

func gitCount(t *testing.T, k *kb.KB) string {
	t.Helper()
	out, err := exec.Command("git", "-C", k.Root, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func dashboardHashOf(t *testing.T, s *Server, name string) string {
	t.Helper()
	var out struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal([]byte(callTool(t, s, "dashboard_get", `{"name":"`+name+`"}`).Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	return out.Hash
}

// TestDashboardPatchStaleWrite: a stale if_match, and "" on an existing name,
// answer stale_write and write nothing; a hash on a missing name too.
func TestDashboardPatchStaleWrite(t *testing.T) {
	t.Parallel()
	s, k := dashboardServer(t)
	path := filepath.Join(k.Root, "dashboards", "week.yaml")
	before, _ := os.ReadFile(path)
	for _, im := range []string{"", "deadbeef"} {
		res := callTool(t, s, "dashboard_patch", patchArgs("week", im, `[{"op":"set_title","title":"X"}]`))
		if !res.IsError || !strings.Contains(res.Content[0].Text, "stale_write") {
			t.Fatalf("if_match %q: %+v", im, res)
		}
	}
	if res := callTool(t, s, "dashboard_patch", patchArgs("fresh", "abc", patchAddOps)); !res.IsError || !strings.Contains(res.Content[0].Text, "stale_write") {
		t.Fatalf("hash on a missing name: %+v", res)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("the file changed")
	}
	if _, err := os.Stat(filepath.Join(k.Root, "dashboards", "fresh.yaml")); err == nil {
		t.Fatal("a missing name was created")
	}
}

// TestDashboardPatchOneCommit: create, edit and delete are one commit each
// through the server's commit path, the result is the resolved dashboard with
// the new hash, and artifact_read still refuses dashboards/.
func TestDashboardPatchOneCommit(t *testing.T) {
	t.Parallel()
	s, k := workServer(t)
	// The fixture's uncommitted pages are swept into a commit of their own by
	// the first write (commitExternalChanges): take the baseline after it.
	if res := callTool(t, s, "dashboard_patch", patchArgs("seed", "", patchAddOps)); res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	c0 := gitCount(t, k)
	res := callTool(t, s, "dashboard_patch", patchArgs("week", "", patchAddOps))
	if res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	var out struct {
		Name    string `json:"name"`
		Hash    string `json:"hash"`
		Title   string `json:"title"`
		Widgets []struct {
			ID    string `json:"id"`
			Total int    `json:"total"`
		} `json:"widgets"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(k.Root, "dashboards", "week.yaml"))
	if err != nil || out.Name != "week" || out.Title != "Week" || len(out.Widgets) != 1 || out.Widgets[0].ID != "open" || out.Hash != kb.DashboardHash(data) {
		t.Fatalf("result %+v err %v: %s", out, err, res.Content[0].Text)
	}
	if n, _ := strconv.Atoi(gitCount(t, k)); strconv.Itoa(n-1) != c0 {
		t.Fatalf("commits %s -> %s, want one more", c0, gitCount(t, k))
	}
	if log, _ := exec.Command("git", "-C", k.Root, "log", "-1", "--format=%s").Output(); !strings.Contains(string(log), "dashboard_patch: week") {
		t.Fatalf("subject %q", log)
	}
	if res := callTool(t, s, "artifact_read", `{"path":"dashboards/week.yaml"}`); !res.IsError {
		t.Fatalf("artifact_read reads dashboards/: %s", res.Content[0].Text)
	}
	if res := callTool(t, s, "artifact_write", `{"path":"dashboards/x.yaml","content":"widgets: []\n"}`); !res.IsError {
		t.Fatal("artifact_write writes dashboards/")
	}

	// A patch whose third op fails is all-or-nothing: file and history unchanged.
	c1 := gitCount(t, k)
	bad := callTool(t, s, "dashboard_patch", patchArgs("week", out.Hash, `[{"op":"set_title","title":"B"},{"op":"remove","id":"open"},{"op":"remove","id":"gone"}]`))
	if !bad.IsError || !strings.Contains(bad.Content[0].Text, "op 3") {
		t.Fatalf("%+v", bad)
	}
	if after, _ := os.ReadFile(filepath.Join(k.Root, "dashboards", "week.yaml")); string(after) != string(data) || gitCount(t, k) != c1 {
		t.Fatal("a failed patch changed the file or the history")
	}

	// Delete: stale refused, then done in one commit, folder gone with it.
	if res := callTool(t, s, "dashboard_delete", `{"name":"week","if_match":"nope"}`); !res.IsError || !strings.Contains(res.Content[0].Text, "stale_write") {
		t.Fatalf("%+v", res)
	}
	if res := callTool(t, s, "dashboard_delete", `{"name":"week","if_match":""}`); !res.IsError {
		t.Fatal("empty if_match accepted")
	}
	if res := callTool(t, s, "dashboard_delete", `{"name":"week","if_match":"`+out.Hash+`"}`); res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "dashboards", "week.yaml")); err == nil {
		t.Fatal("the file is still there")
	}
	// The folder goes with its last dashboard.
	if res := callTool(t, s, "dashboard_delete", `{"name":"seed","if_match":"`+dashboardHashOf(t, s, "seed")+`"}`); res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "dashboards")); err == nil {
		t.Fatal("empty dashboards/ left behind")
	}
	if res := callTool(t, s, "dashboard_delete", `{"name":"week","if_match":"`+out.Hash+`"}`); !res.IsError || !strings.Contains(res.Content[0].Text, "not_found") {
		t.Fatalf("%+v", res)
	}
	if n, _ := strconv.Atoi(gitCount(t, k)); strconv.Itoa(n-3) != c0 {
		t.Fatalf("commits %s -> %s, want create + delete + delete", c0, gitCount(t, k))
	}
}

// TestDashboardNoteSecretScan: a note holding a credential-shaped string is
// refused with the finding (never the value) and nothing is written.
func TestDashboardNoteSecretScan(t *testing.T) {
	t.Parallel()
	s, k := workServer(t)
	secret := "ghp_" + strings.Repeat("a1B2", 9)
	ops := `[{"op":"add","widget":{"id":"memo","type":"note","text":"token ` + secret + `"}}]`
	res := callTool(t, s, "dashboard_patch", patchArgs("notes", "", ops))
	text := res.Content[0].Text
	if !res.IsError || !strings.Contains(text, `widget "memo"`) || strings.Contains(text, secret) {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "dashboards", "notes.yaml")); err == nil {
		t.Fatal("written")
	}
}

// TestDashboardWritersNeedWholeKBWrite pins who may write: a whole-KB writer
// (and an admin); a narrowed writer, a whole-KB reader and a token with
// artifacts read-only are refused, and the refusal echoes nothing of the file.
func TestDashboardWritersNeedWholeKBWrite(t *testing.T) {
	t.Parallel()
	s, k := workServer(t)
	writeDashboardFile(t, k, "plan", narrowTestDashboard)
	hash := dashboardHashOf(t, s, "plan")
	cases := map[string]auth.Policy{
		"narrowed writer": {Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}, Write: true}}},
		"whole reader":    {Permissions: []auth.Permission{{KB: "docs"}}},
		"artifacts ro":    {Permissions: []auth.Permission{{KB: "docs", Write: true, ReadOnlyArtifacts: true}}},
	}
	for name, pol := range cases {
		ctx := restrictedContext(pol)
		for tool, args := range map[string]string{
			"dashboard_patch":  patchArgs("plan", hash, `[{"op":"set_title","title":"X"}]`),
			"dashboard_delete": `{"name":"plan","if_match":"` + hash + `"}`,
		} {
			res := s.callTool(ctx, tool, json.RawMessage(args))
			text := res.Content[0].Text
			if !res.IsError || !strings.HasPrefix(text, "forbidden") {
				t.Fatalf("%s %s: %+v", name, tool, res)
			}
			for _, h := range []string{"Quarter plan", "hush-team", "secretword", "private words", hash} {
				if strings.Contains(text, h) {
					t.Fatalf("%s %s: refusal echoes %q: %s", name, tool, h, text)
				}
			}
		}
	}
	if _, err := os.Stat(filepath.Join(k.Root, "dashboards", "plan.yaml")); err != nil {
		t.Fatal("the file is gone")
	}
	writer := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true}}})
	if res := s.callTool(writer, "dashboard_patch", json.RawMessage(patchArgs("plan", hash, `[{"op":"set_title","title":"X"}]`))); res.IsError {
		t.Fatalf("a whole-KB writer is refused: %s", res.Content[0].Text)
	}
}

// TestDashboardSecretScanCoversEveryAuthorString: a credential in a widget
// title, the dashboard title or a filter field value is refused, unquoted.
func TestDashboardSecretScanCoversEveryAuthorString(t *testing.T) {
	t.Parallel()
	s, k := workServer(t)
	secret := "ghp_" + strings.Repeat("a1B2", 9)
	for name, ops := range map[string]string{
		"widget title":    `[{"op":"add","widget":{"id":"w","type":"list","title":"` + secret + `"}}]`,
		"dashboard title": `[{"op":"set_title","title":"` + secret + `"}]`,
		"link id":         `[{"op":"add","widget":{"id":"w","type":"links","ids":["` + secret + `"]}}]`,
		"filter field":    `[{"op":"add","widget":{"id":"w","type":"list","filter":{"fields":{"owner":"` + secret + `"}}}}]`,
	} {
		res := callTool(t, s, "dashboard_patch", patchArgs("scan", "", ops))
		if !res.IsError || strings.Contains(res.Content[0].Text, secret) || !strings.Contains(res.Content[0].Text, "credential") {
			t.Fatalf("%s: %+v", name, res)
		}
	}
	if _, err := os.Stat(filepath.Join(k.Root, "dashboards", "scan.yaml")); err == nil {
		t.Fatal("written")
	}
}

// TestDashboardCapCountsEveryFile: unparsable and badly named files count
// toward the cap of 32, though dashboard_list does not name them.
func TestDashboardCapCountsEveryFile(t *testing.T) {
	t.Parallel()
	s, k := workServer(t)
	for i := 0; i < kb.MaxDashboards-1; i++ {
		writeDashboardFile(t, k, fmt.Sprintf("d%02d", i), ": not [yaml")
	}
	writeDashboardFile(t, k, "Bad_Name", "x")
	res := callTool(t, s, "dashboard_patch", patchArgs("one-more", "", patchAddOps))
	if !res.IsError || !strings.Contains(res.Content[0].Text, "cap is 32") {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "dashboards", "one-more.yaml")); err == nil {
		t.Fatal("written past the cap")
	}
}
