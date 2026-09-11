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

// TestRoutedMount_SingleKB_KBOptional: with exactly one KB routed there is no
// ambiguity to resolve, so requiring the argument would be ceremony.
func TestRoutedMount_SingleKB_KBOptional(t *testing.T) {
	handler := newRoutedTestHandler(t, "only")

	res := routedCall(t, handler, "concept_read", map[string]any{"id": "manutenzione/marker"})
	if res.IsError {
		t.Fatalf("single-KB routed call without kb failed: %+v", res)
	}

	for _, tool := range decodeToolList(t, routedPost(t, handler, RoutedMountPath, toolsListBody)) {
		var schema struct {
			Required []string `json:"required"`
		}
		json.Unmarshal(tool.InputSchema, &schema)
		for _, r := range schema.Required {
			if r == "kb" {
				t.Fatalf("%s: kb is required with a single KB routed", tool.Name)
			}
		}
	}
}

func TestRoutedMount_UnknownKB(t *testing.T) {
	handler := newRoutedTestHandler(t, "kb-one", "kb-two")
	res := routedCall(t, handler, "atlas_overview", map[string]any{"kb": "nope"})
	if !res.IsError || !strings.Contains(res.Content[0].Text, `unknown kb "nope"`) {
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

// TestRoutedMount_RefusesToolPrefix: routing and prefixing are alternative
// answers to the same problem, so asking for both is a config error rather
// than a silently ignored setting.
func TestRoutedMount_RefusesToolPrefix(t *testing.T) {
	multi := NewMultiKBServer("test")
	k := setupNamedTestKB(t, "kb-one")
	if err := multi.MountKBWithPrefix("kb-one", "one", func(s *Server) { RegisterKBTools(s, k, Deps{}) }); err != nil {
		t.Fatalf("MountKBWithPrefix: %v", err)
	}
	err := multi.EnableRoutedMount("test", nil)
	if err == nil {
		t.Fatal("routed mount accepted a prefixed KB")
	}
	if !strings.Contains(err.Error(), "kb-one") || !strings.Contains(err.Error(), "tool_prefix") {
		t.Errorf("error names neither the KB nor the key: %v", err)
	}
}

// TestRoutedMount_RefusesKBNamedRouted: the routed mount's own path cannot be
// claimed by a KB, and the collision is refused at startup rather than
// shadowing one of the two at request time.
func TestRoutedMount_RefusesKBNamedRouted(t *testing.T) {
	multi := NewMultiKBServer("test")
	k := setupNamedTestKB(t, "routed")
	multi.MountKB("routed", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	if err := multi.EnableRoutedMount("test", nil); err == nil {
		t.Fatal("routed mount accepted a KB named \"routed\"")
	}
}

// TestWithKBProperty_PreservesExistingSchema: the injection edits the decoded
// schema, so a tool's own required list and properties survive.
func TestWithKBProperty_PreservesExistingSchema(t *testing.T) {
	in := json.RawMessage(`{"type":"object","required":["id"],"properties":{"id":{"type":"string"}}}`)
	out := withKBProperty(in, true)
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
