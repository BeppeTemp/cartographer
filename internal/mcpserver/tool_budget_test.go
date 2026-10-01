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
// the detail to docs/control-plane.md §API MCP), not the budget raised
// without a decision.
const (
	toolsListBudgetBytes   = 22 * 1024
	toolDescriptionMaxChar = 600
)

// TestServer_ToolDescriptionBudget guards the size of the agent-profile
// tools/list of one unprefixed mount: total serialized bytes and the length of
// every tool description.
func TestServer_ToolDescriptionBudget(t *testing.T) {
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
