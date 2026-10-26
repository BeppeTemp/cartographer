package mcpserver

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

const readMinPath = RoutedMountPath + "?access=read-min"

var updateGolden = flag.Bool("update", false, "rewrite the golden files from the current output")

func readMinNames(t *testing.T, handler http.Handler, path string) []string {
	t.Helper()
	var names []string
	for _, tool := range decodeToolList(t, routedPost(t, handler, path, toolsListBody)) {
		names = append(names, tool.Name)
	}
	return names
}

func TestRoutedReadMin_ListIsExactlyTheProfile(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a", "kb-b")
	got := readMinNames(t, handler, readMinPath)
	if strings.Join(got, ",") != strings.Join(readMinTools, ",") {
		t.Errorf("read-min lists %v, want %v", got, readMinTools)
	}
	if len(readMinTools) != 9 {
		t.Errorf("the profile has %d tools, want 9", len(readMinTools))
	}
	for _, n := range got {
		if !inReadMinProfile(n) {
			t.Errorf("%q listed but not in the profile", n)
		}
	}
}

// The profile can never list a write tool: every name is in readOnlyToolNames.
func TestReadMinToolsAreReadOnly(t *testing.T) {
	t.Parallel()
	for _, n := range readMinTools {
		if !readOnlyToolNames[n] || ToolRequiresWrite(n) {
			t.Errorf("%q is in readMinTools but not read-only (readOnlyToolNames)", n)
		}
	}
}

func TestRoutedReadMin_WriteIsRefusedWithItsOwnReason(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a", "kb-b")
	// The token writes both KBs: the refusal must come from the routed level.
	res := routedCallAt(t, handler, readMinPath, "concept_write", map[string]any{"kb": "kb-a", "id": "manutenzione/new", "content": "x"})
	if !res.IsError {
		t.Fatalf("concept_write on a read-min session succeeded: %+v", res)
	}
	msg := res.Content[0].Text
	if !strings.Contains(msg, "forbidden") || !strings.Contains(msg, denyReadOnlyProfile) {
		t.Errorf("refusal = %q", msg)
	}
	if strings.Contains(msg, denyUnboundFolder) || strings.Contains(msg, "workspace bind") {
		t.Errorf("refusal borrows the unbound-folder wording: %q", msg)
	}
	assertNoCLICommand(t, "read_only_profile refusal", msg)
}

func TestRoutedReadMin_ReadToolsOutsideTheProfileAreCallable(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a")
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"concept_read", map[string]any{"kb": "kb-a", "id": "manutenzione/marker"}},
		{"lint", map[string]any{"kb": "kb-a"}},
		{"graph_path", map[string]any{"kb": "kb-a", "from": "manutenzione/marker", "to": "manutenzione/marker"}},
	} {
		res := routedCallAt(t, handler, readMinPath, tc.tool, tc.args)
		if res.IsError && strings.Contains(res.Content[0].Text, "not found") {
			t.Errorf("%s: not callable on read-min: %+v", tc.tool, res)
		}
		if res.IsError && strings.Contains(res.Content[0].Text, denyReadOnlyProfile) {
			t.Errorf("%s: refused as a write: %+v", tc.tool, res)
		}
	}
	if res := routedCallAt(t, handler, readMinPath, "no_such_tool", map[string]any{}); !res.IsError {
		t.Errorf("unknown tool succeeded on a read-min session")
	}
}

func TestRoutedReadMin_HeaderAndBogusValue(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a")
	if got := routedPost(t, handler, readMinPath, toolsListBody).Header().Get(AccessHeader); got != AccessReadMinValue {
		t.Errorf("%s = %q, want %q", AccessHeader, got, AccessReadMinValue)
	}
	if got := routedPost(t, handler, readOnlyPath, toolsListBody).Header().Get(AccessHeader); got != AccessReadValue {
		t.Errorf("%s = %q, want %q", AccessHeader, got, AccessReadValue)
	}
	for _, v := range []string{"write", "bogus", "read-max"} {
		rr := routedPost(t, handler, RoutedMountPath+"?access="+v, toolsListBody)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("access=%s: status %d, want 400", v, rr.Code)
		}
		if body := rr.Body.String(); !strings.Contains(body, `"read"`) || !strings.Contains(body, `"read-min"`) {
			t.Errorf("the 400 must name both values: %s", body)
		}
	}
	// Empty means absent.
	if rr := routedPost(t, handler, RoutedMountPath+"?access=", toolsListBody); rr.Code != http.StatusOK || rr.Header().Get(AccessHeader) != "" {
		t.Errorf("access= (empty): status %d header %q", rr.Code, rr.Header().Get(AccessHeader))
	}
}

func TestRoutedReadMin_CombinesWithKBS(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a", "kb-b", "kb-c")
	rr := routedPost(t, handler, RoutedMountPath+"?kbs=kb-a,kb-b&access=read-min", toolsListBody)
	tools := decodeToolList(t, rr)
	if len(tools) != len(readMinTools) {
		t.Errorf("lists %d tools, want %d", len(tools), len(readMinTools))
	}
	for _, tool := range tools {
		if strings.Contains(string(tool.InputSchema), "kb-c") {
			t.Errorf("%s: schema names kb-c, outside kbs", tool.Name)
		}
	}
}

func TestRoutedReadMin_TokenWithoutWriteGetsTheSameList(t *testing.T) {
	t.Parallel()
	multi := NewMultiKBServer("test")
	k := setupNamedTestKB(t, "kb-a")
	multi.MountKB("kb-a", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatal(err)
	}
	store := auth.NewScopedTokenStore([]auth.ScopedToken{{Token: routedTestToken, Scopes: []auth.KBScope{{KB: "kb-a"}}}})
	handler := store.Middleware(multi.Handler())
	if got := readMinNames(t, handler, readMinPath); strings.Join(got, ",") != strings.Join(readMinTools, ",") {
		t.Errorf("read-only token lists %v", got)
	}
}

func TestRoutedReadMin_DoesNotShareACachedServer(t *testing.T) {
	t.Parallel()
	multi := NewMultiKBServer("test")
	for _, name := range []string{"kb-a", "kb-b"} {
		k := setupNamedTestKB(t, name)
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatal(err)
	}
	admin := auth.ContextWithPrincipal(t.Context(), auth.LocalAdminPrincipal())
	for _, kbs := range [][]string{nil, {"kb-a,kb-b"}, {"kb-a"}} {
		view := func(access string) *Server {
			s, _, _, err := multi.routedFor(admin, kbs, access)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
		rw, ro, rm := view(""), view(AccessReadValue), view(AccessReadMinValue)
		if rw == rm || ro == rm || rw == ro {
			t.Errorf("kbs=%v: modes share a Server", kbs)
		}
		if !rm.readMinSession || rm.readOnlySession || ro.readMinSession || rw.readMinSession {
			t.Errorf("kbs=%v: modes swapped", kbs)
		}
		if view(AccessReadMinValue) != rm {
			t.Errorf("kbs=%v: the read-min Server is not cached", kbs)
		}
	}
}

func TestRoutedReadMin_InstructionsAreGeneric(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a")
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	instr := func(path string) string {
		var resp struct {
			Result struct {
				Instructions string `json:"instructions"`
			} `json:"result"`
		}
		if err := json.Unmarshal(routedPost(t, handler, path, init).Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Result.Instructions
	}
	if got := instr(readMinPath + "&provisioned=1"); got != readMinInstructions {
		t.Errorf("provisioned instructions = %q", got)
	}
	assertNoCLICommand(t, "read-min instructions", readMinInstructions)
	// Not provisioned: the guide, read-only wording, no write bullets, no CLI.
	got := instr(readMinPath)
	assertNoCLICommand(t, "read-min guide", got)
	if strings.Contains(got, "concept_write") {
		t.Errorf("read-min guide carries write bullets: %s", got)
	}
}

// TestRoutedReadMin_Golden pins the whole tools/list of a single-KB routed mount
// in read-min (schemas and descriptions), and its size budget (D285).
func TestRoutedReadMin_Golden(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a")
	rr := routedPost(t, handler, readMinPath, toolsListBody)
	var resp struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, resp.Result, "", "  "); err != nil {
		t.Fatal(err)
	}
	buf.WriteByte('\n')
	const golden = "testdata/tools_list_read_min.json"
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v; run: go test ./internal/mcpserver -run TestRoutedReadMin_Golden -args -update", err)
	}
	if !bytes.Equal(want, buf.Bytes()) {
		t.Errorf("read-min tools/list differs from %s; after a deliberate change run: go test ./internal/mcpserver -run TestRoutedReadMin_Golden -args -update", golden)
	}
	compact := new(bytes.Buffer)
	if err := json.Compact(compact, resp.Result); err != nil {
		t.Fatal(err)
	}
	t.Logf("read-min tools/list: %d bytes compact, %d bytes on the wire (budget %d)", compact.Len(), len(rr.Body.Bytes()), toolsListReadMinBudgetBytes)
	if n := len(rr.Body.Bytes()); n > toolsListReadMinBudgetBytes {
		t.Errorf("read-min tools/list is %d bytes, budget %d (D285): shorten descriptions, detail goes to docs/control-plane.md", n, toolsListReadMinBudgetBytes)
	}
	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &list); err != nil {
		t.Fatal(err)
	}
	for _, tl := range list.Tools {
		if n := len([]rune(tl.Description)); n > toolDescriptionMaxChar {
			t.Errorf("tool %q description is %d chars, budget %d (D285)", tl.Name, n, toolDescriptionMaxChar)
		}
	}
}
