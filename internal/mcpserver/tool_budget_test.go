package mcpserver

import (
	"encoding/json"
	"fmt"
	"testing"
	"testing/fstest"
)

// toolDescriptionMaxChar bounds one tool description (D285). The total the
// tools/list costs is TestFixedCallCostBudget's, per profile and KB count.
const toolDescriptionMaxChar = 600

// TestServer_ToolDescriptionBudget guards the length of every description in
// the agent-profile tools/list of one unprefixed mount.
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
	tools := toolSizes(t, raw)
	for _, tl := range tools {
		if tl.desc > toolDescriptionMaxChar {
			t.Errorf("tool %q description is %d chars, budget %d (D285): shorten it, detail goes to docs/control-plane.md. Heaviest:%s",
				tl.name, tl.desc, toolDescriptionMaxChar, heaviestTools(tools, 5))
		}
	}
}

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

// TestRoutedToolsListDoesNotGrowWithKBs: from two KBs up the routed list is
// the same bytes whatever the number of KBs (D1183). What the `kb` property
// costs over one KB is budgeted by TestFixedCallCostBudget.
func TestRoutedToolsListDoesNotGrowWithKBs(t *testing.T) {
	t.Parallel()
	two, _ := routedListSize(t, 2)
	nine, _ := routedListSize(t, 9)
	if nine != two {
		t.Errorf("routed tools/list grows with the KBs: 2 KBs %d bytes, 9 KBs %d (D1183): the kb property must not name them", two, nine)
	}
}
