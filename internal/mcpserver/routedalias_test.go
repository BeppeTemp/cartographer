package mcpserver

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// newRoutedAliasHandler is newRoutedTestHandler with kb_aliases applied and the
// token scoped to tokenKBs.
func newRoutedAliasHandler(t *testing.T, aliases []config.KBAlias, tokenKBs []string, names ...string) http.Handler {
	t.Helper()
	multi := NewMultiKBServer("test")
	for _, name := range names {
		k := setupNamedTestKB(t, name)
		setPolicy(k, func(p *kb.Policy) { p.AllowArtifactWrite = true })
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	multi.SetKBAliases(aliases)
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatalf("EnableRoutedMount: %v", err)
	}
	return routedTokenStore(tokenKBs...).Middleware(multi.Handler())
}

func aliasCall(t *testing.T, handler http.Handler, path, tool string, args map[string]any) ToolResult {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args}})
	var resp Response
	rr := routedPost(t, handler, path, string(body))
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; %s", err, rr.Body.String())
	}
	return decodeToolResult(t, resp)
}

// D772 (a): a URL naming only the old name is served with the renamed KB and
// says so; nothing is unknown.
func TestRoutedMount_KBS_AliasOnly(t *testing.T) {
	t.Parallel()
	handler := newRoutedAliasHandler(t, []config.KBAlias{{From: "kb-old", To: "kb-a"}}, []string{"kb-a", "kb-b"}, "kb-a", "kb-b")
	path := RoutedMountPath + "?kbs=kb-old"

	rr := routedPost(t, handler, path, toolsListBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); strings.Contains(body, `"kb-b"`) || strings.Contains(body, `"kb-old"`) {
		t.Errorf("the connection must serve exactly the renamed KB: %.300s", body)
	}
	_, conn := routedKBStatusConnection(t, handler, path, "")
	if want := map[string]any{"kb-old": "kb-a"}; !reflect.DeepEqual(conn["renamed"], want) {
		t.Errorf("renamed = %v, want %v", conn["renamed"], want)
	}
	if conn["unknown"] != nil {
		t.Errorf("unknown = %v, want none", conn["unknown"])
	}
	if want := []any{"kb-a"}; !reflect.DeepEqual(conn["kbs"], want) {
		t.Errorf("kbs = %v, want %v", conn["kbs"], want)
	}
	if h, _ := conn["hint"].(string); !strings.Contains(h, "renamed") || !strings.Contains(h, "cartographer sync") {
		t.Errorf("hint lacks the renamed guidance: %q", h)
	}
	// A clean URL shares the view and must not inherit the pair.
	if _, clean := routedKBStatusConnection(t, handler, RoutedMountPath+"?kbs=kb-a", ""); clean["renamed"] != nil {
		t.Errorf("renamed leaked across requests: %v", clean["renamed"])
	}
}

// D772 (b): the old and the new name together are one KB.
func TestRoutedMount_KBS_AliasAndTargetDeduplicate(t *testing.T) {
	t.Parallel()
	handler := newRoutedAliasHandler(t, []config.KBAlias{{From: "kb-old", To: "kb-a"}}, []string{"kb-a", "kb-b"}, "kb-a", "kb-b")
	_, conn := routedKBStatusConnection(t, handler, RoutedMountPath+"?kbs=kb-old,kb-a", "")
	if want := []any{"kb-a"}; !reflect.DeepEqual(conn["kbs"], want) {
		t.Errorf("kbs = %v, want %v", conn["kbs"], want)
	}
}

// D772 (c): an alias and an unrelated stale name report both fields.
func TestRoutedMount_KBS_AliasWithUnknown(t *testing.T) {
	t.Parallel()
	handler := newRoutedAliasHandler(t, []config.KBAlias{{From: "kb-old", To: "kb-a"}}, []string{"kb-a", "kb-b"}, "kb-a", "kb-b")
	_, conn := routedKBStatusConnection(t, handler, RoutedMountPath+"?kbs=kb-old,kb-b,gone", "kb-a")
	if want := map[string]any{"kb-old": "kb-a"}; !reflect.DeepEqual(conn["renamed"], want) {
		t.Errorf("renamed = %v, want %v", conn["renamed"], want)
	}
	if want := []any{"gone"}; !reflect.DeepEqual(conn["unknown"], want) {
		t.Errorf("unknown = %v, want %v", conn["unknown"], want)
	}
	if want := []any{"kb-a", "kb-b"}; !reflect.DeepEqual(conn["kbs"], want) {
		t.Errorf("kbs = %v, want %v", conn["kbs"], want)
	}
	h, _ := conn["hint"].(string)
	if !strings.Contains(h, routedUnknownHint) || !strings.Contains(h, routedRenamedHint) {
		t.Errorf("hint must carry both notices: %q", h)
	}
}

// D772 (d): a mounted name beats an alias, so a KB created later under an old
// name is reached by its own name.
func TestRoutedMount_KBS_MountedNameBeatsAlias(t *testing.T) {
	t.Parallel()
	handler := newRoutedAliasHandler(t, []config.KBAlias{{From: "kb-b", To: "kb-a"}}, []string{"kb-a", "kb-b"}, "kb-a", "kb-b")
	path := RoutedMountPath + "?kbs=kb-b"
	_, conn := routedKBStatusConnection(t, handler, path, "")
	if want := []any{"kb-b"}; !reflect.DeepEqual(conn["kbs"], want) {
		t.Errorf("kbs = %v, want %v: the alias redirected a mounted KB", conn["kbs"], want)
	}
	if conn["renamed"] != nil {
		t.Errorf("renamed = %v, want none", conn["renamed"])
	}
	res := aliasCall(t, handler, path, "concept_read", map[string]any{"id": "manutenzione/marker"})
	if res.IsError || !strings.Contains(res.Content[0].Text, "belongs-to-kb-b") {
		t.Errorf("want kb-b content, got %+v", res)
	}
}

// D772 (e): an alias whose target is not mounted stays unknown, and a URL with
// nothing resolvable is still a 400.
func TestRoutedMount_KBS_AliasToUnmountedStaysUnknown(t *testing.T) {
	t.Parallel()
	handler := newRoutedAliasHandler(t, []config.KBAlias{{From: "kb-old", To: "kb-gone"}}, []string{"kb-a"}, "kb-a")
	_, conn := routedKBStatusConnection(t, handler, RoutedMountPath+"?kbs=kb-old,kb-a", "")
	if want := []any{"kb-old"}; !reflect.DeepEqual(conn["unknown"], want) {
		t.Errorf("unknown = %v, want %v", conn["unknown"], want)
	}
	if conn["renamed"] != nil {
		t.Errorf("renamed = %v, want none", conn["renamed"])
	}
	rr := routedPost(t, handler, RoutedMountPath+"?kbs=kb-old", toolsListBody)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"kb-old"`) {
		t.Errorf("status=%d body=%s, want a 400 naming kb-old", rr.Code, rr.Body.String())
	}
}

// D772 (f)+(g): a call naming the old KB reaches the new one (through its own
// callTool, hence its policy and audit), and the schema advertises only the new
// name.
func TestRoutedMount_KBS_CallByOldNameReachesTarget(t *testing.T) {
	t.Parallel()
	handler := newRoutedAliasHandler(t, []config.KBAlias{{From: "kb-old", To: "kb-a"}}, []string{"kb-a", "kb-b"}, "kb-a", "kb-b")
	path := RoutedMountPath + "?kbs=kb-old,kb-b"

	if body := routedPost(t, handler, path, toolsListBody).Body.String(); !strings.Contains(body, `"enum":["kb-a","kb-b"]`) || strings.Contains(body, `"kb-old"`) {
		t.Errorf("the kb enum must list the new names only: %.400s", body)
	}
	res := aliasCall(t, handler, path, "concept_read", map[string]any{"kb": "kb-old", "id": "manutenzione/marker"})
	if res.IsError || !strings.Contains(res.Content[0].Text, "belongs-to-kb-a") {
		t.Fatalf("a call by the old name must reach kb-a: %+v", res)
	}
	// Without the URL naming it, the old name is not an alias of the request.
	res = aliasCall(t, handler, RoutedMountPath+"?kbs=kb-b", "concept_read", map[string]any{"kb": "kb-old", "id": "manutenzione/marker"})
	if !res.IsError {
		t.Errorf("an alias must apply only to a connection that named it: %+v", res)
	}
}

// D772: an alias selects a KB and never widens a token: a token scoped to the
// old name does not reach the renamed KB (scopes are not migrated, D177).
func TestRoutedMount_KBS_AliasDoesNotWidenAuthorization(t *testing.T) {
	t.Parallel()
	args := map[string]any{"kb": "kb-old", "id": "manutenzione/marker"}
	path := RoutedMountPath + "?kbs=kb-old"

	// Control: the same call succeeds for a token scoped to the new name, so
	// the refusal below is the scope and not a missing concept.
	allowed := newRoutedAliasHandler(t, []config.KBAlias{{From: "kb-old", To: "kb-a"}}, []string{"kb-a"}, "kb-a", "kb-b")
	if res := aliasCall(t, allowed, path, "concept_read", args); res.IsError || !strings.Contains(res.Content[0].Text, "belongs-to-kb-a") {
		t.Fatalf("control: a token scoped to the new name must read the renamed KB: %+v", res)
	}

	handler := newRoutedAliasHandler(t, []config.KBAlias{{From: "kb-old", To: "kb-a"}}, []string{"kb-old"}, "kb-a", "kb-b")
	res := aliasCall(t, handler, path, "concept_read", args)
	if !res.IsError {
		t.Fatalf("a token scoped to the old name reached the renamed KB: %+v", res)
	}
	if strings.Contains(res.Content[0].Text, "belongs-to-kb-a") {
		t.Errorf("the refusal leaks the renamed KB's content: %q", res.Content[0].Text)
	}
}

// D772: an item whose from is a mounted name is dropped at SetKBAliases.
func TestSetKBAliases_DropsMountedFrom(t *testing.T) {
	t.Parallel()
	multi := NewMultiKBServer("test")
	for _, name := range []string{"kb-a", "kb-b"} {
		k := setupNamedTestKB(t, name)
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	multi.SetKBAliases([]config.KBAlias{{From: "kb-b", To: "kb-a"}, {From: "kb-old", To: "kb-a"}})
	if want := map[string]string{"kb-old": "kb-a"}; !reflect.DeepEqual(multi.routedAliases, want) {
		t.Errorf("routedAliases = %v, want %v", multi.routedAliases, want)
	}
}
