package mcpserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

// Budget for what an agent-profile client pays on every round-trip (D285).
// A new tool or a longer description that breaks it must be shortened (move
// the detail to docs/control-plane.md §MCP API), not the budget raised
// without a decision.
const (
	// 23 KiB since D302: work_list added 700 bytes (22490 → 23190) after
	// D301 had already trimmed the descriptions that list growing sets.
	//
	// 25 KiB since D318: concept_batch and asset_delete joined the agent
	// profile (25322 bytes, +1.8 KiB over D302's 23190) after concept_batch's
	// description and schema were cut from 3 KiB to 1.4 KiB; the remaining
	// descriptions were already at their floor.
	//
	// 25.5 KiB since D315: map_update gained title_max_length and
	// forbidden_title_terms (+0.1 KiB of schema, no description grew).
	//
	// 26 KiB since D715: concept_new's vars schema accepts a string or a list
	// of strings (+17 bytes over the budget; no description grew).
	//
	// 29 KiB since D906: the measured registry counts artifact_write and
	// artifact_delete (+2.2 KiB; artifact_write's description was cut to the
	// per-tool limit). A KB with default settings already listed them through
	// allow_template_write (D352); the zero policy measured here did not.
	toolsListBudgetBytes   = 29 * 1024
	toolDescriptionMaxChar = 600
)

// TestServer_ToolDescriptionBudget guards the size of the agent-profile
// tools/list of one unprefixed mount: total serialized bytes and the length of
// every tool description.
func TestServer_ToolDescriptionBudget(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	bundleFS := fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide KB creation\nversion: \"1.0\"\n---\nBody here.\n"),
		},
	}
	RegisterKBTools(s, k, Deps{BundleFS: bundleFS})
	s.SetToolsProfile("agent")

	resps := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	})
	if len(resps) != 2 || resps[1].Error != nil {
		t.Fatalf("tools/list: unexpected responses: %+v", resps)
	}
	raw, err := json.Marshal(resps[1].Result)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	type sized struct {
		name  string
		bytes int
		desc  int
	}
	var tools []sized
	for _, rt := range result.Tools {
		var d struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(rt, &d); err != nil {
			t.Fatal(err)
		}
		tools = append(tools, sized{d.Name, len(rt), len([]rune(d.Description))})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].bytes > tools[j].bytes })
	heaviest := func() string {
		var b strings.Builder
		for i := 0; i < 5 && i < len(tools); i++ {
			fmt.Fprintf(&b, "\n  %s: %d bytes (description %d chars)", tools[i].name, tools[i].bytes, tools[i].desc)
		}
		return b.String()
	}

	t.Logf("agent-profile tools/list: %d bytes (budget %d)", len(raw), toolsListBudgetBytes)
	if total := len(raw); total > toolsListBudgetBytes {
		t.Errorf("agent-profile tools/list is %d bytes, budget %d (D285): shorten descriptions, detail goes to docs/control-plane.md. Heaviest:%s",
			total, toolsListBudgetBytes, heaviest())
	}
	for _, tl := range tools {
		if tl.desc > toolDescriptionMaxChar {
			t.Errorf("tool %q description is %d chars, budget %d (D285): shorten it, detail goes to docs/control-plane.md. Heaviest:%s",
				tl.name, tl.desc, toolDescriptionMaxChar, heaviest())
		}
	}
}

// toolsListReadMinBudgetBytes bounds the tools/list of a single-KB routed mount
// in ?access=read-min (D1184), as served (the response body). Raise it only with
// a decision (D285); the golden testdata/tools_list_read_min.json pins the content.
const toolsListReadMinBudgetBytes = 5100 // measured 4871

// routedKBOverheadBytes bounds what the `kb` argument adds to a routed
// tools/list over the same mount bound to one KB (D1183): a fixed per-tool
// cost (the property, its short description and `required`), measured as
// 6768 bytes over 64 tools (about 105 per tool, indented JSON: #1185 compacts it). Raise it only with a decision. #1206 owns the final
// routed budgets and generalises this test.
const routedKBOverheadBytes = 7000

// routedListSize serializes the tools/list of a routed mount over n KBs.
func routedListSize(t *testing.T, n int) (size, tools int) {
	t.Helper()
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("kb-%d", i+1)
	}
	handler := newRoutedTestHandler(t, names...)
	rr := routedPost(t, handler, RoutedMountPath, toolsListBody)
	return len(rr.Body.Bytes()), len(decodeToolList(t, rr))
}

// TestRoutedToolsListDoesNotGrowWithKBs: the routed list is measured on the
// full tool set (the helper cannot put the routed view in the agent profile),
// so the absolute 1-KB size is not compared with toolsListBudgetBytes. Its size must not depend on the number of KBs (D1183).
func TestRoutedToolsListDoesNotGrowWithKBs(t *testing.T) {
	t.Parallel()
	one, _ := routedListSize(t, 1)
	two, tools := routedListSize(t, 2)
	nine, _ := routedListSize(t, 9)
	t.Logf("routed tools/list: 1 KB %d bytes, 2 KBs %d, 9 KBs %d (%d tools, %d bytes over 1 KB, %d per tool)",
		one, two, nine, tools, two-one, (two-one)/max(tools, 1))
	if nine != two {
		t.Errorf("routed tools/list grows with the KBs: 2 KBs %d bytes, 9 KBs %d (D1183): the kb property must not name them", two, nine)
	}
	if over := two - one; over > routedKBOverheadBytes {
		t.Errorf("the kb property adds %d bytes over the single-KB list, budget %d (D1183)", over, routedKBOverheadBytes)
	}
}
