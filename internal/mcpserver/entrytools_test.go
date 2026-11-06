package mcpserver

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// The entry tools (D-new-entry-tools) are one list in configurator; the server
// flags exactly those tools, and tools/list carries the vendor pins for them
// and for no other tool.

func TestEntryTools_FlagMatchesTheList(t *testing.T) {
	t.Parallel()
	s := New("test")
	RegisterKBTools(s, setupNamedTestKB(t, "kb-a"), Deps{})
	var flagged []string
	for _, name := range s.toolsOrd {
		if s.tools[name].AlwaysLoad {
			flagged = append(flagged, name)
		}
	}
	want := configurator.EntryTools()
	slices.Sort(flagged)
	slices.Sort(want)
	if !slices.Equal(flagged, want) {
		t.Errorf("tools flagged AlwaysLoad = %v, want configurator.EntryTools() = %v", flagged, want)
	}
}

func TestEntryTools_ToolsListCarriesTheAlwaysLoadMeta(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a", "kb-b")
	for _, path := range []string{RoutedMountPath, readOnlyPath, readMinPath} {
		var resp struct {
			Result struct {
				Tools []struct {
					Name string         `json:"name"`
					Meta map[string]any `json:"_meta"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal(routedPost(t, handler, path, toolsListBody).Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, tl := range resp.Result.Tools {
			if !configurator.IsEntryTool(tl.Name) {
				if len(tl.Meta) != 0 {
					t.Errorf("%s: %s carries _meta %v, only entry tools are pinned", path, tl.Name, tl.Meta)
				}
				continue
			}
			seen++
			if tl.Meta["anthropic/alwaysLoad"] != true || tl.Meta["antigravity/alwaysLoad"] != true {
				t.Errorf("%s: %s _meta = %v, want both alwaysLoad keys", path, tl.Name, tl.Meta)
			}
		}
		if seen != len(configurator.EntryTools()) {
			t.Errorf("%s lists %d entry tools, want %d", path, seen, len(configurator.EntryTools()))
		}
	}
}

// The entry paragraph is paid by every handshake of a provisioned client
// (D285): names only tools, no call syntax, no CLI command, a self-contained
// first line.
func TestEntryInstructions_Budget(t *testing.T) {
	t.Parallel()
	const budget = 400 // bytes, the issue's cap
	t.Logf("entry paragraph: %d bytes", len(entryInstructions))
	if len(entryInstructions) > budget {
		t.Errorf("entryInstructions is %d bytes, over its budget of %d: it is paid by every handshake of every provisioned connection", len(entryInstructions), budget)
	}
	if strings.Contains(entryInstructions, "\n") {
		t.Error("the entry paragraph must be one line: Pi shows the first line as the server summary")
	}
	for _, name := range configurator.EntryTools() {
		if !strings.Contains(entryInstructions, "`"+name+"`") {
			t.Errorf("entryInstructions does not name `%s`", name)
		}
	}
	for _, syntax := range []string{"execute", "tools[", "mcp__", "cartographer_"} {
		if strings.Contains(entryInstructions, syntax) {
			t.Errorf("entryInstructions carries the client syntax %q", syntax)
		}
	}
	assertNoCLICommand(t, "entry paragraph", entryInstructions)
}

// Under a budget that picks tools by size (OpenCode's inline catalog, Pi's
// codemode.inlineBudget) the entry tools must stay short. Bytes of the compact
// input schema as listed on a one-KB read-min mount; the cap is the measured
// value plus at most 10 %, raised only with a decision.
func TestEntryTools_SchemaSizeCap(t *testing.T) {
	t.Parallel()
	caps := map[string]int{"atlas_overview": 70, "search": 256, "concept_read": 526, "graph_context": 163}
	handler := newRoutedTestHandler(t, "kb-a")
	var resp struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(routedPost(t, handler, readMinPath, toolsListBody).Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, tl := range resp.Result.Tools {
		limit, ok := caps[tl.Name]
		if !ok {
			continue
		}
		seen++
		var compact strings.Builder
		var v any
		if err := json.Unmarshal(tl.InputSchema, &v); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		compact.Write(b)
		t.Logf("%s: input schema %d bytes (cap %d)", tl.Name, compact.Len(), limit)
		if compact.Len() > limit {
			t.Errorf("%s input schema is %d bytes, over its cap of %d: trim a property description (the detail belongs to the tool description and kb_guide)", tl.Name, compact.Len(), limit)
		}
	}
	if seen != len(caps) {
		t.Errorf("found %d of the %d entry tools", seen, len(caps))
	}
}
