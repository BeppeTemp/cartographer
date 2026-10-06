package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// routedTestToken is the bearer token the fixtures below grant rw on every
// mounted KB: metadata and every tool are fail-closed without a principal
// (installPolicy), so a routed-mount test that skips auth would be asserting
// the denial path rather than the routing.
const routedTestToken = "routed-tok"

func routedTokenStore(names ...string) *auth.TokenStore {
	scopes := make([]auth.KBScope, 0, len(names))
	for _, name := range names {
		scopes = append(scopes, auth.KBScope{KB: name, Write: true})
	}
	return auth.NewScopedTokenStore([]auth.ScopedToken{{Token: routedTestToken, Scopes: scopes}})
}

// setupNamedTestKB is setupTestKB with a concept whose body names the KB, so a
// routed call can be shown to have reached the KB it named rather than a
// plausible sibling — the failure D187's "never infer the KB" rule exists to
// prevent.
func setupNamedTestKB(t *testing.T, kbName string) *kb.KB {
	t.Helper()
	k := setupTestKB(t)
	body := "---\ntype: Runbook\ntitle: Marker " + kbName + "\n---\n# Marker\nbelongs-to-" + kbName + "\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "manutenzione", "marker.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	return k
}

// newRoutedTestHandler mounts the named KBs, enables the routed mount and
// wraps the result in the token middleware.
func newRoutedTestHandler(t *testing.T, names ...string) http.Handler {
	t.Helper()
	multi := NewMultiKBServer("test")
	for _, name := range names {
		k := setupNamedTestKB(t, name)
		k.AllowArtifactWrite = true
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatalf("EnableRoutedMount: %v", err)
	}
	return routedTokenStore(names...).Middleware(multi.Handler())
}

func routedPost(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+routedTestToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

type listedTool struct {
	Name        string          `json:"name"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func decodeToolList(t *testing.T, rr *httptest.ResponseRecorder) []listedTool {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("tools/list: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Result struct {
			Tools []listedTool `json:"tools"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rr.Body.String())
	}
	if resp.Error != nil {
		t.Fatalf("tools/list error: %s", resp.Error.Message)
	}
	return resp.Result.Tools
}

// TestRoutedMount_ToolsList_OneCopyWithRequiredKB is the whole point of D187:
// three KBs, one copy of each tool, and the KB carried as a required argument.
func TestRoutedMount_ToolsList_OneCopyWithRequiredKB(t *testing.T) {
	handler := newRoutedTestHandler(t, "kb-one", "kb-two", "kb-three")

	tools := decodeToolList(t, routedPost(t, handler, RoutedMountPath, toolsListBody))
	if len(tools) == 0 {
		t.Fatal("routed tools/list returned no tools")
	}
	seen := map[string]int{}
	for _, tool := range tools {
		seen[tool.Name]++
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("%s: decode schema: %v", tool.Name, err)
		}
		if _, ok := schema.Properties["kb"]; !ok {
			t.Errorf("%s: schema has no kb property", tool.Name)
		}
		required := false
		for _, r := range schema.Required {
			if r == "kb" {
				required = true
			}
		}
		if !required {
			t.Errorf("%s: kb is not required with 3 KBs routed", tool.Name)
		}
	}
	for name, n := range seen {
		if n != 1 {
			t.Errorf("tool %q advertised %d times, want exactly 1", name, n)
		}
	}

	// The per-KB endpoint advertises the same tools without the kb argument:
	// the routed payload is one copy where three mounts would be three.
	perKBBody := routedPost(t, handler, "/mcp/kb-one", toolsListBody).Body.Len()
	perKB := decodeToolList(t, routedPost(t, handler, "/mcp/kb-one", toolsListBody))
	if len(perKB) != len(tools) {
		t.Errorf("per-KB advertises %d tools, routed %d: the union should match a homogeneous mount", len(perKB), len(tools))
	}

	// D187's acceptance criterion: the payload a three-KB client carries drops
	// from three copies to roughly one. The kb property makes each schema
	// slightly larger, so the bar is "well under two copies", not "exactly a
	// third" — a regression that reintroduces duplication fails it either way.
	routedBody := routedPost(t, handler, RoutedMountPath, toolsListBody).Body.Len()
	if routedBody >= 2*perKBBody {
		t.Errorf("routed tools/list is %d bytes against %d per KB (x3 = %d): the duplication is not gone",
			routedBody, perKBBody, 3*perKBBody)
	}
	t.Logf("tools/list: routed %d bytes vs %d for three per-KB mounts", routedBody, 3*perKBBody)
}

// TestRoutedMount_PerKBEndpointsUnchanged pins the invariant that makes this
// opt-in safe: enabling the routed mount must not alter a single byte of what
// the per-KB endpoints advertise.
func TestRoutedMount_PerKBEndpointsUnchanged(t *testing.T) {
	plain := NewMultiKBServer("test")
	routed := NewMultiKBServer("test")
	for _, name := range []string{"kb-one", "kb-two"} {
		kPlain := setupNamedTestKB(t, name)
		plain.MountKB(name, func(s *Server) { RegisterKBTools(s, kPlain, Deps{}) })
		kRouted := setupNamedTestKB(t, name)
		routed.MountKB(name, func(s *Server) { RegisterKBTools(s, kRouted, Deps{}) })
	}
	if err := routed.EnableRoutedMount("test", nil); err != nil {
		t.Fatalf("EnableRoutedMount: %v", err)
	}

	for _, name := range []string{"kb-one", "kb-two"} {
		store := routedTokenStore("kb-one", "kb-two")
		want := routedPost(t, store.Middleware(plain.Handler()), "/mcp/"+name, toolsListBody).Body.String()
		got := routedPost(t, store.Middleware(routed.Handler()), "/mcp/"+name, toolsListBody).Body.String()
		if got != want {
			t.Errorf("/mcp/%s tools/list changed when the routed mount was enabled:\n got: %s\nwant: %s", name, got, want)
		}
	}
}

func routedCall(t *testing.T, handler http.Handler, tool string, args map[string]any) ToolResult {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	rr := routedPost(t, handler, RoutedMountPath, string(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("%s: status=%d body=%s", tool, rr.Code, rr.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s: decode: %v; body=%s", tool, err, rr.Body.String())
	}
	if resp.Error != nil {
		t.Fatalf("%s: rpc error: %s", tool, resp.Error.Message)
	}
	return decodeToolResult(t, resp)
}

// TestRoutedMount_DispatchesToTheNamedKB: each of three KBs is reachable, and
// the content that comes back is that KB's.
func TestRoutedMount_DispatchesToTheNamedKB(t *testing.T) {
	handler := newRoutedTestHandler(t, "kb-one", "kb-two", "kb-three")

	for _, name := range []string{"kb-one", "kb-two", "kb-three"} {
		res := routedCall(t, handler, "concept_read", map[string]any{"kb": name, "id": "manutenzione/marker"})
		if res.IsError {
			t.Fatalf("%s: concept_read failed: %+v", name, res)
		}
		text := res.Content[0].Text
		if !strings.Contains(text, "belongs-to-"+name) {
			t.Errorf("kb=%s returned another KB's content: %s", name, text)
		}
	}
}

// TestRoutedMount_MissingKB_NamesThem: with more than one KB routed the
// argument is required, and the refusal has to say which KBs exist — an error
// the caller can act on without a second round-trip.
func TestRoutedMount_MissingKB_NamesThem(t *testing.T) {
	handler := newRoutedTestHandler(t, "kb-one", "kb-two", "kb-three")
	res := routedCall(t, handler, "atlas_overview", map[string]any{})
	if !res.IsError {
		t.Fatal("a call with no kb succeeded; the KB must never be inferred")
	}
	text := res.Content[0].Text
	for _, name := range []string{"kb-one", "kb-two", "kb-three"} {
		if !strings.Contains(text, name) {
			t.Errorf("missing-kb error does not name %q: %s", name, text)
		}
	}
}

// TestRoutedMount_SingleKB_KBOptional: with exactly one KB mounted there is no
// ambiguity to resolve, so the schemas carry no kb at all (D288).
func TestRoutedMount_SingleKB_KBOptional(t *testing.T) {
	handler := newRoutedTestHandler(t, "only")

	res := routedCall(t, handler, "concept_read", map[string]any{"id": "manutenzione/marker"})
	if res.IsError {
		t.Fatalf("single-KB routed call without kb failed: %+v", res)
	}

	for _, tool := range decodeToolList(t, routedPost(t, handler, RoutedMountPath, toolsListBody)) {
		props, _ := routedSchema(t, tool)
		if _, ok := props["kb"]; ok {
			t.Fatalf("%s: schema carries kb with a single KB routed", tool.Name)
		}
	}
}

func TestRoutedMount_UnknownKB(t *testing.T) {
	handler := newRoutedTestHandler(t, "kb-one", "kb-two")
	res := routedCall(t, handler, "atlas_overview", map[string]any{"kb": "nope"})
	if !res.IsError || !strings.Contains(res.Content[0].Text, `kb "nope" is not available`) {
		t.Fatalf("want unknown-kb error, got %+v", res)
	}
	if !strings.Contains(res.Content[0].Text, "kb-one") {
		t.Errorf("unknown-kb error does not list the mounted KBs: %s", res.Content[0].Text)
	}
}

// TestRoutedMount_GatedTool_UnionThenAttributedRefusal: the exposed set is the
// union, so artifact_write is advertised because one KB allows it; the KB that
// does not gets an error naming itself and the setting, rather than the tool
// silently vanishing for everyone.
func TestRoutedMount_GatedTool_UnionThenAttributedRefusal(t *testing.T) {
	multi := NewMultiKBServer("test")
	allowed := setupNamedTestKB(t, "open-kb")
	allowed.AllowArtifactWrite = true
	multi.MountKB("open-kb", func(s *Server) { RegisterKBTools(s, allowed, Deps{}) })
	denied := setupNamedTestKB(t, "closed-kb") // AllowArtifactWrite stays false
	multi.MountKB("closed-kb", func(s *Server) { RegisterKBTools(s, denied, Deps{}) })
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatalf("EnableRoutedMount: %v", err)
	}
	handler := routedTokenStore("open-kb", "closed-kb").Middleware(multi.Handler())

	found := false
	for _, tool := range decodeToolList(t, routedPost(t, handler, RoutedMountPath, toolsListBody)) {
		if tool.Name == "artifact_write" {
			found = true
		}
	}
	if !found {
		t.Fatal("artifact_write is absent from the routed union although one KB allows it")
	}

	res := routedCall(t, handler, "artifact_write", map[string]any{
		"kb": "closed-kb", "path": "skills/x/SKILL.md", "content": "x",
	})
	if !res.IsError {
		t.Fatal("artifact_write on a KB with the gate closed succeeded")
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "closed-kb") || !strings.Contains(text, "allow_artifact_write") {
		t.Errorf("refusal names neither the KB nor the setting: %s", text)
	}

	res = routedCall(t, handler, "artifact_write", map[string]any{
		"kb": "open-kb", "path": "skills/demo/SKILL.md",
		"content": "---\nname: demo\ndescription: A demo skill for the routed mount test.\n---\nBody.\n",
	})
	if res.IsError {
		t.Fatalf("artifact_write on the KB that allows it failed: %+v", res)
	}
}

// TestRoutedMount_RejectsQueryKBSelector: two channels for the same choice is
// how they get to disagree.
func TestRoutedMount_RejectsQueryKBSelector(t *testing.T) {
	handler := newRoutedTestHandler(t, "kb-one", "kb-two")
	rr := routedPost(t, handler, RoutedMountPath+"?kb=kb-one", toolsListBody)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// TestRoutedMount_NotEnabled_PathFallsThrough: with no routed mount, the path
// is an ordinary KB name, so a KB called "routed" keeps its own endpoint.
func TestRoutedMount_NotEnabled_PathFallsThrough(t *testing.T) {
	multi := newMultiKBTestHandler(t, "routed", "other")
	rr := routedPost(t, routedTokenStore("routed", "other").Middleware(multi.Handler()), RoutedMountPath, toolsListBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("/mcp/routed for a KB named \"routed\": status=%d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// routedSchema decodes one tool's listed schema.
func routedSchema(t *testing.T, tool listedTool) (props map[string]map[string]any, required []string) {
	t.Helper()
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("%s: decode schema: %v", tool.Name, err)
	}
	return schema.Properties, schema.Required
}

// TestRoutedMount_KBS_AbsentIsAllMounted: no ?kbs= is the default binding, every
// mounted KB, as a required enum of all of them (D288).
func TestRoutedMount_KBS_AbsentIsAllMounted(t *testing.T) {
	handler := newRoutedTestHandler(t, "kb-a", "kb-b", "kb-c")
	for _, tool := range decodeToolList(t, routedPost(t, handler, RoutedMountPath, toolsListBody)) {
		props, _ := routedSchema(t, tool)
		enum, _ := props["kb"]["enum"].([]any)
		if len(enum) != 3 {
			t.Fatalf("%s: kb enum = %v, want the 3 mounted KBs", tool.Name, enum)
		}
	}
}

// TestRoutedMount_KBS_NarrowsToTwo: ?kbs= of two KBs advertises an enum of
// exactly those two, dispatches to either and refuses the third, naming the
// allowed ones. The binding only narrows: the token would reach the third.
func TestRoutedMount_KBS_NarrowsToTwo(t *testing.T) {
	handler := newRoutedTestHandler(t, "kb-a", "kb-b", "kb-c")
	path := RoutedMountPath + "?kbs=kb-b,kb-a,kb-a"

	for _, tool := range decodeToolList(t, routedPost(t, handler, path, toolsListBody)) {
		props, required := routedSchema(t, tool)
		enum, _ := props["kb"]["enum"].([]any)
		if len(enum) != 2 || enum[0] != "kb-a" || enum[1] != "kb-b" {
			t.Fatalf("%s: kb enum = %v, want [kb-a kb-b]", tool.Name, enum)
		}
		if !containsString(required, "kb") {
			t.Errorf("%s: kb not required with 2 KBs", tool.Name)
		}
	}

	call := func(args map[string]any) ToolResult {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "concept_read", "arguments": args}})
		var resp Response
		rr := routedPost(t, handler, path, string(body))
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v; %s", err, rr.Body.String())
		}
		return decodeToolResult(t, resp)
	}
	res := call(map[string]any{"kb": "kb-b", "id": "manutenzione/marker"})
	if res.IsError || !strings.Contains(res.Content[0].Text, "belongs-to-kb-b") {
		t.Fatalf("kb-b inside the binding: %+v", res)
	}
	res = call(map[string]any{"kb": "kb-c", "id": "manutenzione/marker"})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "kb-a") || strings.Contains(res.Content[0].Text, "belongs-to") {
		t.Fatalf("kb-c outside the binding must be refused naming the allowed KBs: %+v", res)
	}
	res = call(map[string]any{"id": "manutenzione/marker"})
	if !res.IsError {
		t.Fatalf("no kb with 2 KBs bound must be refused: %+v", res)
	}
}

// TestRoutedMount_KBS_SingleKB: ?kbs= of one KB removes `kb` from every schema,
// dispatches a call without it to that KB, accepts the same value and refuses
// any other.
func TestRoutedMount_KBS_SingleKB(t *testing.T) {
	handler := newRoutedTestHandler(t, "kb-a", "kb-b", "kb-c")
	path := RoutedMountPath + "?kbs=kb-b"

	for _, tool := range decodeToolList(t, routedPost(t, handler, path, toolsListBody)) {
		props, required := routedSchema(t, tool)
		if _, ok := props["kb"]; ok {
			t.Fatalf("%s: schema still carries kb with a single-KB binding", tool.Name)
		}
		if containsString(required, "kb") {
			t.Errorf("%s: kb required with a single-KB binding", tool.Name)
		}
	}

	call := func(args map[string]any) ToolResult {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "concept_read", "arguments": args}})
		var resp Response
		rr := routedPost(t, handler, path, string(body))
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v; %s", err, rr.Body.String())
		}
		return decodeToolResult(t, resp)
	}
	for _, args := range []map[string]any{
		{"id": "manutenzione/marker"},
		{"kb": "kb-b", "id": "manutenzione/marker"},
	} {
		res := call(args)
		if res.IsError || !strings.Contains(res.Content[0].Text, "belongs-to-kb-b") {
			t.Fatalf("args %v: want kb-b content, got %+v", args, res)
		}
	}
	if res := call(map[string]any{"kb": "kb-a", "id": "manutenzione/marker"}); !res.IsError {
		t.Fatalf("a kb other than the bound one must be refused: %+v", res)
	}
}

// TestRoutedMount_KBS_UnknownName: a name that is not mounted is a 400 naming
// it and the mounted KBs, never a silently smaller set.
func TestRoutedMount_KBS_UnknownName(t *testing.T) {
	handler := newRoutedTestHandler(t, "kb-a", "kb-b")
	rr := routedPost(t, handler, RoutedMountPath+"?kbs=kb-a,nope", toolsListBody)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"nope"`) || !strings.Contains(body, "kb-a") || !strings.Contains(body, "kb-b") {
		t.Errorf("400 names neither the typo nor the mounted KBs: %s", body)
	}
}

// TestRoutedMount_KBS_MetadataGateUsesTheSet: a principal scoped to kb-a only
// is refused the metadata of a connection bound to kb-b (it can reach none of
// that connection's KBs) and admitted on one bound to kb-a.
func TestRoutedMount_KBS_MetadataGateUsesTheSet(t *testing.T) {
	multi := NewMultiKBServer("test")
	for _, name := range []string{"kb-a", "kb-b"} {
		k := setupNamedTestKB(t, name)
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatalf("EnableRoutedMount: %v", err)
	}
	handler := routedTokenStore("kb-a").Middleware(multi.Handler())

	hasRPCError := func(rr *httptest.ResponseRecorder) bool {
		var resp struct {
			Error *struct{} `json:"error"`
		}
		return rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &resp) != nil || resp.Error != nil
	}
	if rr := routedPost(t, handler, RoutedMountPath+"?kbs=kb-a", toolsListBody); hasRPCError(rr) {
		t.Fatalf("bound to the reachable KB: status=%d body=%.300s", rr.Code, rr.Body.String())
	}
	if rr := routedPost(t, handler, RoutedMountPath+"?kbs=kb-b", toolsListBody); !hasRPCError(rr) {
		t.Fatalf("a connection bound only to an unreachable KB listed tools: %.300s", rr.Body.String())
	}
}

// TestWithKBProperty_PreservesExistingSchema: the injection edits the decoded
// schema, so a tool's own required list and properties survive.
func TestWithKBProperty_PreservesExistingSchema(t *testing.T) {
	in := json.RawMessage(`{"type":"object","required":["id"],"properties":{"id":{"type":"string"}}}`)
	out := withKBProperty(in, []string{"kb-a", "kb-b"})
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(out, &schema); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := schema.Properties["id"]; !ok {
		t.Error("existing property dropped")
	}
	if _, ok := schema.Properties["kb"]; !ok {
		t.Error("kb property not injected")
	}
	if len(schema.Required) != 2 {
		t.Errorf("required = %v, want both id and kb", schema.Required)
	}
}

// TestSplitKBArgument_StripsKB: the per-KB handler must receive exactly the
// arguments it would have received on its own endpoint.
func TestSplitKBArgument_StripsKB(t *testing.T) {
	name, rest, err := splitKBArgument(json.RawMessage(`{"kb":"kb-one","id":"a/b"}`))
	if err != nil {
		t.Fatal(err)
	}
	if name != "kb-one" {
		t.Errorf("name = %q", name)
	}
	var got map[string]any
	json.Unmarshal(rest, &got)
	if _, ok := got["kb"]; ok {
		t.Error("kb was not stripped from the arguments")
	}
	if got["id"] != "a/b" {
		t.Errorf("rest = %v", got)
	}
}

// D320: a stale session calling a pre-D288 prefixed name learns what replaced
// it, and the prefixed name is never advertised.
func TestLegacyPrefixedToolReturnsExplicitError(t *testing.T) {
	handler := newRoutedTestHandler(t, "kb-a", "kb-b")

	res := routedCall(t, handler, "kb_a__search", map[string]any{"query": "marker"})
	if !res.IsError {
		t.Fatalf("prefixed name must fail: %+v", res)
	}
	msg := res.Content[0].Text
	for _, want := range []string{"D288", "`search`", `kb: "kb-a"`, "Restart your agent session"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
	// A prefix no served KB derives still names the replacement, not the KB.
	if msg := routedCall(t, handler, "other__search", nil).Content[0].Text; !strings.Contains(msg, "`kb` argument") {
		t.Errorf("unmapped prefix: %s", msg)
	}
	// A genuinely unknown name keeps the plain message.
	if msg := routedCall(t, handler, "kb_a__nonsense", nil).Content[0].Text; msg != "tool not found: kb_a__nonsense" {
		t.Errorf("unknown bare tool: %s", msg)
	}
	for _, tool := range decodeToolList(t, routedPost(t, handler, RoutedMountPath, toolsListBody)) {
		if strings.Contains(tool.Name, "__") {
			t.Errorf("tools/list advertises prefixed name %q", tool.Name)
		}
	}
	// The per-KB endpoint has no kb argument, so the message names none.
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kb_a__search","arguments":{"query":"x"}}}`
	var resp Response
	if err := json.Unmarshal(routedPost(t, handler, "/mcp/kb-a", body).Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if msg := decodeToolResult(t, resp).Content[0].Text; !strings.Contains(msg, "call `search` instead") {
		t.Errorf("per-KB endpoint: %s", msg)
	}
}
