package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// The fixed per-call cost (#1206): what an agent pays on every model call,
// whatever the question, is the managed instructions block its client holds
// plus the tools/list of its connection. TestFixedCallCostBudget measures both
// on a routed mount with 1 and 9 KBs, per tool profile, and fails past a budget
// per component and cell, saying which component grew and by how much.
//
// The `initialize` guide a client without the block receives is a different
// payload, pinned where its pieces are visible, by TestRenderGuide_Budget
// (internal/provisioning); the block's shared and brief sections keep their own
// pins in TestBlock_Budget. Do not fold either into this test.
//
// Budgets are bytes, as every budget in the repository. Token reading, for
// orientation only and never asserted: ≈ 2.5 bytes per token (order of
// magnitude, from #1183's table: the 1-KB default tools/list measured 11.7k
// tokens on v0.26.0, where it was 28997 bytes), JSON schemas denser than prose;
// the block's prose is closer to the 4 bytes per token of provisioning's
// ApproxTokens.
//
// A budget is the value measured when the v0.27.0 token work landed plus at most
// 10 %, rounded up. The "measured" figures below include #1207 (PR #1228),
// which lengthens the search description and the shared block section; before
// it each cell measured 83 to 195 bytes less (block 3575 and 8583; tools/list
// agent 31235 and 36414, access=read 12982 and 15812, access=read-min 4944 and
// 5918). Re-measure with `go test -v -run TestFixedCallCostBudget` once it lands. It is raised only with a decision (D285), never to make the
// test pass: move a tool's detail to docs/control-plane.md §MCP API, a KB's
// detail to `kb_guide`.
const fixedCostBytesPerToken = 2.5

// fixedCostProfiles are the three listings an agent connection can ask for:
// the default agent profile, `?access=read` (D1144) and `?access=read-min`, the
// 9-tool read profile (D1184). Ordered from largest to smallest.
var fixedCostProfiles = []struct{ name, path string }{
	{"agent", RoutedMountPath},
	{"access=read", readOnlyPath},
	{"access=read-min", readMinPath},
}

// fixedCostBlockBudget is the instructions file `sync` writes for Claude Code,
// per KB count, with the fixture of fixedCostKBs (5 archives, 12 skills and a
// 3 KB curated instructions.md per KB, all brief). It folds the 1-KB and
// 9-KB cells of TestBlock_Budget (D1182: 3377 and 6801 bytes), which rendered
// the block for a non-routed connection; the routed rendering `sync` writes
// adds the `kb` routing clause to every KB's sentence (105 bytes per KB since
// #1246/D1246, 183 before). The 9-KB cell is capped at D1182's 8192 bytes.
var fixedCostBlockBudget = map[int]int{
	// Measured 3585 bytes after #1246 (3663 on v0.27.0, #1206, after #1182
	// and #1207); measured plus 10 % rounded up.
	1: 3944,
	// Measured 7969 bytes after #1246 (8671 on v0.27.0, #1206, over D1182's
	// cap; 7024 for the same KBs rendered non-routed). Measured plus 10 % is
	// 8766, so the hard cap of D1182 applies.
	9: 8192,
}

// fixedCostBlockPerKBBudget bounds what one more KB adds to the block,
// (block9 - block1) / 8: the routing sentence, the archives and the skills
// line, never the curated text (#1182). Measured 548 bytes after #1246 (626
// on v0.27.0, #1206); measured plus 10 % rounded up.
const fixedCostBlockPerKBBudget = 603

// fixedCostToolsListBudget is the serialized tools/list result of the routed
// mount, per profile and KB count.
var fixedCostToolsListBudget = map[string]map[int]int{
	// History of the single-mount agent budget this row replaces
	// (the total TestServer_ToolDescriptionBudget asserted until #1206):
	// 23 KiB since D302: work_list added 700 bytes (22490 → 23190) after
	// D301 had already trimmed the descriptions that list growing sets.
	// 25 KiB since D318: concept_batch and asset_delete joined the agent
	// profile (25322 bytes) after concept_batch's description and schema were
	// cut from 3 KiB to 1.4 KiB. 25.5 KiB since D315: map_update gained
	// title_max_length and forbidden_title_terms. 26 KiB since D715:
	// concept_new's vars schema accepts a string or a list of strings.
	// 29 KiB since D906: the measured registry counts artifact_write and
	// artifact_delete. 31 KiB since D1191: secret_set, secret_resolve and
	// service_get joined the agent profile (31138 bytes).
	//
	// Measured on v0.27.0 (#1206), after #1183, #1184, #1185 and #1207: 1 KB
	// 31430 bytes, byte for byte the unprefixed single mount the old budget
	// measured (one KB carries no `kb` property, D288), so its 31 KiB stays;
	// 9 KBs 36609.
	"agent": {1: 31 * 1024, 9: 40000},
	// Measured on v0.27.0 (#1206): 1 KB 13065 bytes, 9 KBs 15895.
	"access=read": {1: 14300, 9: 17400},
	// Measured on v0.27.0 (#1206): 1 KB 5027 bytes, 9 KBs 6001. The 1-KB cell
	// keeps the read-min budget of TestRoutedReadMin_Golden (D1184, 5100 on the wire, measured
	// 4871); testdata/tools_list_read_min.json still pins the content.
	"access=read-min": {1: 5100, 9: 6600},
}

// fixedCostToolsListGrowthBudget bounds the tools/list growth from 1 to 9 KBs
// per profile: the `kb` argument every tool gains from two KBs up, a fixed
// per-tool cost that does not grow with the KB count (D1183; its "few hundred
// bytes" target is not reachable while `kb` stays required). Measured on
// v0.27.0 (#1206): agent 5179, access=read 2830, access=read-min 974. It
// replaces the routed overhead budget of D1183 (7000, measured 6768 on the full
// registry before #1185).
var fixedCostToolsListGrowthBudget = map[string]int{
	"agent":           5650,
	"access=read":     3100,
	"access=read-min": 1070,
}

var fixedCostKBNames = []string{"kb-a", "kb-b", "kb-c", "kb-d", "kb-e", "kb-f", "kb-g", "kb-h", "kb-i"}

// fixedCostCurated is a deliberately large synthetic instructions.md (about
// 3 KB). The brief block must not carry it: a regression that inlines the
// curated text again fails the block budget.
var fixedCostCurated = "# Curated instructions\n\n" + strings.Repeat("Synthetic curated guidance that the brief block leaves to kb_guide.\n", 45)

// fixedCostKBs builds the first n fixture KBs: setupNamedTestKB plus five
// archives, twelve skills and the curated instructions.md. It returns the KB
// roots and a fresh MultiKBServer that mounts them as serve.go does.
func fixedCostKBs(t *testing.T, n int) (map[string]string, *MultiKBServer) {
	t.Helper()
	bundleFS := fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide KB creation\nversion: \"1.0\"\n---\nBody here.\n"),
		},
	}
	roots := map[string]string{}
	multi := NewMultiKBServer("test")
	for _, name := range fixedCostKBNames[:n] {
		k := setupNamedTestKB(t, name)
		for _, archive := range []string{"backlog", "clients", "incidents", "infra", "notes"} {
			page := "---\ntype: Note\ntitle: A\n---\n# A\n"
			writeFixedCostFile(t, filepath.Join(k.DataRoot(), archive, "a.md"), page)
		}
		for s := 0; s < 12; s++ {
			skill := fmt.Sprintf("%s-procedure-%02d", name, s)
			writeFixedCostFile(t, filepath.Join(k.Root, "skills", skill, "SKILL.md"),
				"---\nname: "+skill+"\ndescription: Use when testing "+skill+".\n---\nBody.\n")
		}
		writeFixedCostFile(t, filepath.Join(k.Root, "instructions.md"), fixedCostCurated)
		roots[name] = k.Root
		// What cmd/cartographer/serve.go does for every mounted KB.
		multi.MountKB(name, func(s *Server) {
			RegisterKBTools(s, k, Deps{BundleFS: bundleFS, RoutedMount: true})
			s.SetToolsProfile("agent")
		})
	}
	// The routed view carries the server's profile too (serve.go): without it
	// the routed list is the full registry, advanced tools included.
	if err := multi.EnableRoutedMount("test", func(s *Server) { s.SetToolsProfile("agent") }); err != nil {
		t.Fatalf("EnableRoutedMount: %v", err)
	}
	return roots, multi
}

func writeFixedCostFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sizedTool is one tool of a measured tools/list.
type sizedTool struct {
	name   string
	bytes  int // the serialized tool
	desc   int // description length in characters
	kbProp int // bytes of the routed `kb` property, 0 without one
}

// toolSizes splits a serialized tools/list result into its tools, heaviest first.
func toolSizes(t *testing.T, raw []byte) []sizedTool {
	t.Helper()
	var result struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	tools := make([]sizedTool, 0, len(result.Tools))
	for _, rt := range result.Tools {
		var d struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"inputSchema"`
		}
		if err := json.Unmarshal(rt, &d); err != nil {
			t.Fatal(err)
		}
		tools = append(tools, sizedTool{d.Name, len(rt), len([]rune(d.Description)), len(d.InputSchema.Properties["kb"])})
	}
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].bytes != tools[j].bytes {
			return tools[i].bytes > tools[j].bytes
		}
		return tools[i].name < tools[j].name
	})
	return tools
}

// heaviestTools prints the n heaviest tools of a toolSizes list.
func heaviestTools(tools []sizedTool, n int) string {
	var b strings.Builder
	for i := 0; i < n && i < len(tools); i++ {
		fmt.Fprintf(&b, "\n  %s: %d bytes (description %d chars)", tools[i].name, tools[i].bytes, tools[i].desc)
	}
	return b.String()
}

func toolNames(tools []sizedTool) []string {
	names := make([]string, len(tools))
	for i, tl := range tools {
		names[i] = tl.name
	}
	sort.Strings(names)
	return names
}

// measuredToolsList is the routed tools/list of one profile.
type measuredToolsList struct {
	bytes int // json.Marshal of the result, as tool_budget_test.go measures
	tools []sizedTool
}

func (m measuredToolsList) kbPropBytes() int {
	n := 0
	for _, tl := range m.tools {
		n += tl.kbProp
	}
	return n
}

// measureRoutedToolsList requests tools/list at path. An error, or an empty
// list, fails the test: an empty list measures 0 bytes and would pass every
// budget.
func measureRoutedToolsList(t *testing.T, multi *MultiKBServer, names []string, path string) measuredToolsList {
	t.Helper()
	handler := routedTokenStore(names...).Middleware(multi.Handler())
	rr := routedPost(t, handler, path, toolsListBody)
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("tools/list at %s: status %d, undecodable body: %v; %s", path, rr.Code, err, rr.Body.String())
	}
	if resp.Error != nil {
		t.Fatalf("tools/list at %s: %s", path, resp.Error.Message)
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}
	m := measuredToolsList{bytes: len(raw), tools: toolSizes(t, raw)}
	if len(m.tools) == 0 {
		t.Fatalf("tools/list at %s lists no tool: refusing to measure an empty list as 0 bytes", path)
	}
	return m
}

// measuredBlock is the instructions file Apply wrote for Claude Code.
type measuredBlock struct {
	content string
}

func (b measuredBlock) bytes() int { return len(b.content) }

// heaviestLines prints the n longest lines of the block.
func (b measuredBlock) heaviestLines(n int) string {
	lines := strings.Split(b.content, "\n")
	sort.SliceStable(lines, func(i, j int) bool { return len(lines[i]) > len(lines[j]) })
	var s strings.Builder
	for i := 0; i < n && i < len(lines); i++ {
		line := lines[i]
		if len(line) > 100 {
			line = line[:100] + "…"
		}
		fmt.Fprintf(&s, "\n  %d bytes: %s", len(lines[i]), line)
	}
	return s.String()
}

// measureBlock renders the block in process through the path `sync` takes:
// BuildManifest for a routed mount, then Apply for Claude Code into a fresh
// base directory. Only the instructions kind is applied (skills would need the
// KB roots to materialise and are not part of the block).
func measureBlock(t *testing.T, roots map[string]string) measuredBlock {
	t.Helper()
	m, err := provisioning.BuildManifest(nil, roots, provisioning.BuildOptions{RoutedMount: true})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	var instr []provisioning.Artifact
	for _, a := range m.Artifacts {
		if a.Kind == "instructions" {
			// The trust shortcut provisioning's own tests take: Apply needs no
			// real signer to render the block.
			a.Signed = true
			instr = append(instr, a)
		}
	}
	order := make([]string, 0, len(roots))
	for name := range roots {
		order = append(order, name)
	}
	sort.Strings(order)
	base := t.TempDir()
	if _, err := provisioning.Apply(provisioning.MergeArtifacts(instr), provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: base, SkipLockWrite: true,
		ExpandPlaceholders: true, KBOrder: order,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(base, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("instructions file: %v", err)
	}
	content := string(data)
	// The bytes must not depend on where the test runs.
	for _, p := range append([]string{base}, order...) {
		if root, ok := roots[p]; ok {
			p = root
		}
		if strings.Contains(content, p) {
			t.Fatalf("the block carries the temporary path %s: its size would change from run to run", p)
		}
	}
	return measuredBlock{content: content}
}

// fixedCost is every measurement for one KB count.
type fixedCost struct {
	block measuredBlock
	lists map[string]measuredToolsList
}

func measureFixedCost(t *testing.T, n int) fixedCost {
	t.Helper()
	roots, multi := fixedCostKBs(t, n)
	names := fixedCostKBNames[:n]
	fc := fixedCost{block: measureBlock(t, roots), lists: map[string]measuredToolsList{}}
	for _, p := range fixedCostProfiles {
		fc.lists[p.name] = measureRoutedToolsList(t, multi, names, p.path)
	}
	return fc
}

// agentProfileNames is the tool set of the agent profile on one unprefixed
// mount, the golden the routed listings are compared with.
func agentProfileNames(t *testing.T) ([]string, int) {
	t.Helper()
	s := New("test")
	RegisterKBTools(s, setupTestKB(t), Deps{BundleFS: fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide KB creation\nversion: \"1.0\"\n---\nBody here.\n"),
		},
	}})
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
	return toolNames(toolSizes(t, raw)), len(raw)
}

// TestFixedCallCostBudget fails when a component of the fixed per-call cost
// outgrows its budget in any cell: instructions block × {1, 9 KBs}, tools/list
// × {agent, access=read, access=read-min} × {1, 9 KBs}, plus the growth with
// the KB count of each.
func TestFixedCallCostBudget(t *testing.T) {
	t.Parallel()
	one, nine := measureFixedCost(t, 1), measureFixedCost(t, 9)
	byKBs := map[int]fixedCost{1: one, 9: nine}

	// The cells are comparable only if they list what the profile promises.
	agentNames, unprefixed := agentProfileNames(t)
	var readNames []string
	for _, n := range agentNames {
		if !ToolRequiresWrite(n) {
			readNames = append(readNames, n)
		}
	}
	readMin := append([]string(nil), readMinTools...)
	sort.Strings(readMin)
	golden := map[string][]string{"agent": agentNames, "access=read": readNames, "access=read-min": readMin}
	for _, kbs := range []int{1, 9} {
		for _, p := range fixedCostProfiles {
			got := toolNames(byKBs[kbs].lists[p.name].tools)
			if strings.Join(got, ",") != strings.Join(golden[p.name], ",") {
				t.Fatalf("%s with %d KBs lists %v, want the profile's %v: the cell would not measure the profile", p.name, kbs, got, golden[p.name])
			}
		}
	}
	for _, name := range fixedCostKBNames {
		if c := strings.Count(nine.block.content, "<!-- cartographer:kb:"+name+":begin -->"); c != 1 {
			t.Errorf("the 9-KB block has %d regions for %s, want 1", c, name)
		}
	}

	// The whole table, with the fixed per-call cost each connection pays:
	// visible in `go test -v`, not asserted (the components are).
	var table strings.Builder
	fmt.Fprintf(&table, "fixed per-call cost (bytes; ≈ tokens at %.1f bytes/token):", fixedCostBytesPerToken)
	for _, kbs := range []int{1, 9} {
		fc := byKBs[kbs]
		for _, p := range fixedCostProfiles {
			l := fc.lists[p.name]
			sum := fc.block.bytes() + l.bytes
			fmt.Fprintf(&table, "\n  %-15s %d KB(s): block %5d + tools/list %5d (%2d tools, kb property %4d) = %5d ≈ %.1fk tokens",
				p.name, kbs, fc.block.bytes(), l.bytes, len(l.tools), l.kbPropBytes(), sum, float64(sum)/fixedCostBytesPerToken/1000)
		}
	}
	fmt.Fprintf(&table, "\n  unprefixed single mount, agent profile: tools/list %d", unprefixed)
	t.Log(table.String())

	over := func(got, budget int) string {
		return fmt.Sprintf("%d bytes, budget %d: over by %d bytes (%.1f %%)", got, budget, got-budget, 100*float64(got-budget)/float64(budget))
	}
	const hint = "\nThe budget is raised only with a decision (D285): tool detail goes to docs/control-plane.md §MCP API, KB detail to kb_guide."

	perKB := (nine.block.bytes() - one.block.bytes()) / 8
	for _, kbs := range []int{1, 9} {
		t.Run(fmt.Sprintf("instructions block/%d KBs", kbs), func(t *testing.T) {
			b := byKBs[kbs].block
			if budget := fixedCostBlockBudget[kbs]; b.bytes() > budget {
				t.Errorf("instructions block, %d KB(s): %s; %+d bytes against the 1-KB block, %d bytes per added KB. Heaviest lines:%s%s",
					kbs, over(b.bytes(), budget), b.bytes()-one.block.bytes(), perKB, b.heaviestLines(3), hint)
			}
		})
	}
	t.Run("instructions block/per added KB", func(t *testing.T) {
		if perKB > fixedCostBlockPerKBBudget {
			t.Errorf("instructions block grows %s per added KB (1 KB %d, 9 KBs %d): the curated text or a per-KB line came back. Heaviest lines of the 9-KB block:%s%s",
				over(perKB, fixedCostBlockPerKBBudget), one.block.bytes(), nine.block.bytes(), nine.block.heaviestLines(3), hint)
		}
	})
	for _, kbs := range []int{1, 9} {
		for i, p := range fixedCostProfiles {
			t.Run(fmt.Sprintf("tools/list/%s/%d KBs", p.name, kbs), func(t *testing.T) {
				l := byKBs[kbs].lists[p.name]
				budget := fixedCostToolsListBudget[p.name][kbs]
				if l.bytes <= budget {
					return
				}
				prev := "none, the largest profile"
				if i > 0 {
					pp := fixedCostProfiles[i-1].name
					prev = fmt.Sprintf("%d bytes saved against %s", byKBs[kbs].lists[pp].bytes-l.bytes, pp)
				}
				t.Errorf("tools/list, profile %s, %d KB(s): %s; %+d bytes against %s with 1 KB (kb property %d bytes); %s. Heaviest:%s%s",
					p.name, kbs, over(l.bytes, budget), l.bytes-one.lists[p.name].bytes, p.name, l.kbPropBytes(), prev, heaviestTools(l.tools, 5), hint)
			})
		}
	}
	for _, p := range fixedCostProfiles {
		t.Run("tools/list/"+p.name+"/growth 1 to 9 KBs", func(t *testing.T) {
			growth := nine.lists[p.name].bytes - one.lists[p.name].bytes
			if budget := fixedCostToolsListGrowthBudget[p.name]; growth > budget {
				t.Errorf("tools/list, profile %s, grows %s from 1 to 9 KBs (kb property %d bytes over %d tools): the routed kb argument grew (D1183).%s",
					p.name, over(growth, budget), nine.lists[p.name].kbPropBytes(), len(nine.lists[p.name].tools), hint)
			}
		})
	}
}

// The measurements are stable: two runs give the same bytes, so a budget
// failure is a growth, never noise from timestamps or temporary paths.
func TestFixedCallCostIsStable(t *testing.T) {
	t.Parallel()
	a, b := measureFixedCost(t, 2), measureFixedCost(t, 2)
	if a.block.content != b.block.content {
		t.Errorf("two renderings of the same block differ:\n%s\n---\n%s", a.block.content, b.block.content)
	}
	for _, p := range fixedCostProfiles {
		if a.lists[p.name].bytes != b.lists[p.name].bytes {
			t.Errorf("%s: two tools/list measure %d and %d bytes", p.name, a.lists[p.name].bytes, b.lists[p.name].bytes)
		}
	}
}
