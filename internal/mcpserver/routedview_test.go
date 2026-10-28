package mcpserver

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// D1096: the routed view follows the caller's principal. These tests build a
// token store from arbitrary policies (routedTokenStore grants rw on every KB).

const (
	tokRO       = "tok-ro"
	tokAdmin    = "tok-admin"
	tokMixed    = "tok-mixed"
	tokNone     = "tok-none"
	tokMapRule  = "tok-map"
	tokTransfer = "tok-transfer"
)

const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

// viewFixture is a routed multi-KB server behind the token middleware, with
// one token per policy below.
type viewFixture struct {
	multi   *MultiKBServer
	store   *auth.TokenStore
	handler http.Handler
	kbs     map[string]*kb.KB
}

func newViewFixture(t *testing.T, aliases []config.KBAlias, names ...string) *viewFixture {
	t.Helper()
	return newViewFixtureWithBundle(t, nil, aliases, names...)
}

// newViewFixtureWithBundle is newViewFixture on a server that has a bundle of
// skills (D1100: kb_guide serves procedures from it).
func newViewFixtureWithBundle(t *testing.T, bundle fs.FS, aliases []config.KBAlias, names ...string) *viewFixture {
	t.Helper()
	multi := NewMultiKBServer("test")
	byName := map[string]*kb.KB{}
	deps := Deps{BundleFS: bundle, KBByName: func(n string) (*kb.KB, bool) { k, ok := byName[n]; return k, ok }}
	for _, name := range names {
		k := setupNamedTestKB(t, name)
		byName[name] = k
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, deps) })
	}
	multi.SetKBAliases(aliases)
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatalf("EnableRoutedMount: %v", err)
	}
	store := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: tokAdmin, Policy: auth.Policy{Admin: true}},
		{Token: tokRO, Scopes: []auth.KBScope{{KB: "kb-a"}}},
		{Token: tokMixed, Scopes: []auth.KBScope{{KB: "kb-a", Write: true}, {KB: "kb-b"}}},
		{Token: tokNone, Policy: auth.Policy{}},
		{Token: tokMapRule, Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true, Maps: []string{"manutenzione"}}}}},
		{Token: tokTransfer, Scopes: []auth.KBScope{{KB: "kb-a", Write: true}, {KB: "kb-c", Write: true}}},
	})
	return &viewFixture{multi: multi, store: store, handler: store.Middleware(multi.Handler()), kbs: byName}
}

func (f *viewFixture) post(t *testing.T, token, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

func (f *viewFixture) call(t *testing.T, token, path, tool string, args map[string]any) ToolResult {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args}})
	rr := f.post(t, token, path, string(body))
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; %s", err, rr.Body.String())
	}
	if resp.Error != nil {
		return ToolResult{IsError: true, Content: []ContentBlock{{Type: "text", Text: resp.Error.Message}}}
	}
	return decodeToolResult(t, resp)
}

func (f *viewFixture) ctx(t *testing.T, token string) context.Context {
	t.Helper()
	p, ok := f.store.PrincipalOf(token)
	if !ok {
		t.Fatalf("unknown token %q", token)
	}
	return auth.ContextWithPrincipal(context.Background(), p)
}

func (f *viewFixture) view(t *testing.T, token string, kbs []string, bridge bool) *Server {
	t.Helper()
	srv, _, _, err := f.multi.routedFor(f.ctx(t, token), kbs, accessOf(bridge))
	if err != nil {
		t.Fatalf("routedFor: %v", err)
	}
	return srv
}

func listNames(t *testing.T, rr *httptest.ResponseRecorder) (names []string, hasKBProp bool) {
	t.Helper()
	for _, tool := range decodeToolList(t, rr) {
		names = append(names, tool.Name)
		if strings.Contains(string(tool.InputSchema), `"kb"`) {
			hasKBProp = true
		}
	}
	return names, hasKBProp
}

func instructionsOf(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; %s", err, rr.Body.String())
	}
	return resp.Result.Instructions
}

func TestRoutedView_ReadOnlyTokenSeesItsOwnKBOnly(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b", "kb-c")

	rr := f.post(t, tokRO, RoutedMountPath, toolsListBody)
	names, hasKB := listNames(t, rr)
	if len(names) == 0 {
		t.Fatal("no tool listed")
	}
	for _, n := range names {
		if ToolRequiresWrite(n) {
			t.Errorf("write tool %q listed to a read-only token", n)
		}
	}
	if hasKB {
		t.Error("a one-KB view must carry no kb property")
	}
	for _, other := range []string{"kb-b", "kb-c"} {
		if strings.Contains(rr.Body.String(), `"`+other+`"`) {
			i := strings.Index(rr.Body.String(), `"`+other+`"`)
			t.Errorf("tools/list names %s to a token that cannot read it: ...%s...", other, rr.Body.String()[max(0, i-80):i+40])
		}
	}
	init := f.post(t, tokRO, RoutedMountPath, initializeBody)
	if got := instructionsOf(t, init); !strings.Contains(got, "read-only") || !strings.Contains(got, `kb: "kb-a"`) || strings.Contains(got, "kb-b") {
		t.Errorf("token view instructions = %q, want the read-only guide of kb-a alone", got)
	}
	if got := instructionsOf(t, f.post(t, tokRO, RoutedMountPath+"?provisioned=1", initializeBody)); got != "" {
		t.Errorf("provisioned token view instructions = %q, want none", got)
	}
	if got := init.Header().Get(AccessHeader); got != "" {
		t.Errorf("token view carries %s = %q", AccessHeader, got)
	}
	res := f.call(t, tokRO, RoutedMountPath, "concept_write", map[string]any{"id": "manutenzione/new", "frontmatter": map[string]any{"title": "x"}, "body": "x"})
	if !res.IsError || !strings.Contains(res.Content[0].Text, denyReadOnlyToken) || strings.Contains(res.Content[0].Text, denyUnboundFolder) {
		t.Errorf("concept_write = %+v, want read_only_token", res)
	}
	if res := f.call(t, tokRO, RoutedMountPath, "concept_read", map[string]any{"id": "manutenzione/marker"}); res.IsError {
		t.Errorf("concept_read failed: %+v", res)
	}
}

func TestRoutedView_ReadOnlyTokenWithKBSAndBridge(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b", "kb-c")

	if f.view(t, tokRO, nil, false) != f.view(t, tokRO, []string{"kb-a,kb-b"}, false) {
		t.Error("?kbs=kb-a,kb-b of a token that reads only kb-a must be the same view as no kbs")
	}
	path := RoutedMountPath + "?kbs=kb-a,kb-b&access=read"
	init := f.post(t, tokRO, path, initializeBody)
	if got := instructionsOf(t, init); !strings.Contains(got, "read-only") || strings.Contains(got, "workspace bind") {
		t.Errorf("unmarked bridge mode instructions = %q, want the guide's read-only wording, not the bridge's", got)
	}
	if got := instructionsOf(t, f.post(t, tokRO, path+"&provisioned=1", initializeBody)); got != readOnlyInstructions {
		t.Errorf("provisioned bridge mode instructions = %q, want the workspace bind sentence", got)
	}
	if init.Header().Get(AccessHeader) != AccessReadValue {
		t.Errorf("bridge mode lacks the %s header", AccessHeader)
	}
	res := f.call(t, tokRO, path, "concept_write", map[string]any{"id": "manutenzione/new", "frontmatter": map[string]any{"title": "x"}, "body": "x"})
	if !res.IsError || !strings.Contains(res.Content[0].Text, denyUnboundFolder) {
		t.Errorf("bridge mode concept_write = %+v, want unbound_folder", res)
	}
}

func TestRoutedView_MixedGrants(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b", "kb-c")

	rr := f.post(t, tokMixed, RoutedMountPath, toolsListBody)
	var wrote bool
	for _, tool := range decodeToolList(t, rr) {
		wrote = wrote || ToolRequiresWrite(tool.Name)
		if tool.Name == "concept_read" {
			if strings.Contains(string(tool.InputSchema), "kb-") {
				t.Errorf("kb schema carries a KB name: %s", tool.InputSchema)
			}
		}
	}
	if !wrote {
		t.Error("a token that writes kb-a must be offered the write tools")
	}
	if res := f.call(t, tokMixed, RoutedMountPath, "concept_write", map[string]any{"kb": "kb-b", "id": "manutenzione/new", "frontmatter": map[string]any{"title": "x"}, "body": "x"}); !res.IsError ||
		!strings.Contains(res.Content[0].Text, denyReadOnlyToken) {
		t.Errorf("write on the read-only KB = %+v, want read_only_token", res)
	}
	if res := f.call(t, tokMixed, RoutedMountPath, "concept_read", map[string]any{"kb": "kb-c", "id": "manutenzione/marker"}); !res.IsError {
		t.Errorf("a KB outside the token's set was readable: %+v", res)
	}
}

func TestRoutedView_RoleRuleNarrowedToAMapReadsAndWrites(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b")
	names, _ := listNames(t, f.post(t, tokMapRule, RoutedMountPath, toolsListBody))
	var wrote bool
	for _, n := range names {
		wrote = wrote || ToolRequiresWrite(n)
	}
	if !wrote {
		t.Error("a write rule narrowed to a map must list the write tools")
	}
	if got := f.view(t, tokMapRule, nil, false).connKBs; strings.Join(got, ",") != "kb-a" {
		t.Errorf("effective set = %v, want [kb-a]", got)
	}
}

func TestRoutedView_NoGrantIsTheDeniedView(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b")
	for _, path := range []string{RoutedMountPath, RoutedMountPath + "?kbs=kb-a"} {
		for _, body := range []string{initializeBody, toolsListBody} {
			rr := f.post(t, tokNone, path, body)
			out := rr.Body.String()
			if !strings.Contains(out, "forbidden: no_kb_access") {
				t.Errorf("%s: want forbidden: no_kb_access, got %.300s", path, out)
			}
			for _, name := range []string{"kb-a", "kb-b"} {
				if strings.Contains(out, name) {
					t.Errorf("%s: the denied view names %s: %.300s", path, name, out)
				}
			}
		}
		if res := f.call(t, tokNone, path, "concept_read", map[string]any{"kb": "kb-a", "id": "x"}); !res.IsError ||
			!strings.Contains(strings.ToLower(res.Content[0].Text), "unknown tool") {
			t.Errorf("%s: tools/call = %+v, want unknown tool", path, res)
		}
	}
	// Fail-closed for the zero principal too (a handler used without the middleware).
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, RoutedMountPath, strings.NewReader(toolsListBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	f.multi.Handler().ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), "no_kb_access") {
		t.Errorf("zero principal: %.300s", rr.Body.String())
	}
}

func TestRoutedView_AdminAndAuthOffKeepTodaysSurface(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b")
	if f.view(t, tokAdmin, nil, false) != f.multi.routed {
		t.Error("an admin token must get the pre-built default view")
	}
	off := auth.ContextWithPrincipal(context.Background(), auth.LocalAdminPrincipal())
	if srv, _, _, err := f.multi.routedFor(off, nil, ""); err != nil || srv != f.multi.routed {
		t.Errorf("auth off must get the pre-built default view (err=%v)", err)
	}
}

func TestRoutedView_CacheIsKeyedBySetAndMode(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b", "kb-c")

	ro := f.view(t, tokRO, nil, false)
	if again := f.view(t, tokRO, []string{"kb-a"}, false); again != ro {
		t.Error("two requests with the same set and mode must share a Server")
	}
	// kb-a alone, three modes: token read-only (tok-ro), normal (tok-transfer
	// narrowed to kb-a), bridge.
	rw := f.view(t, tokTransfer, []string{"kb-a"}, false)
	bridge := f.view(t, tokRO, nil, true)
	if ro == rw || ro == bridge || rw == bridge {
		t.Error("read-write, token read-only and bridge views of one set must be three Servers")
	}
	if ro.tokenReadOnly != true || ro.readOnlySession || bridge.tokenReadOnly || !bridge.readOnlySession || rw.tokenReadOnly || rw.readOnlySession {
		t.Errorf("modes swapped: ro=%+v rw=%+v bridge=%+v", ro.tokenReadOnly, rw.tokenReadOnly, bridge.readOnlySession)
	}
	// Many requests from several principals add only distinct (set, mode) pairs.
	for _, tok := range []string{tokAdmin, tokRO, tokMixed, tokNone, tokMapRule, tokTransfer} {
		for i := 0; i < 3; i++ {
			f.post(t, tok, RoutedMountPath, toolsListBody)
			f.post(t, tok, RoutedMountPath+"?kbs=kb-a", toolsListBody)
		}
	}
	f.multi.routedMu.Lock()
	defer f.multi.routedMu.Unlock()
	want := map[string]bool{}
	for _, k := range []string{
		routedViewKey([]string{"kb-a", "kb-b", "kb-c"}, routedNormal), // admin
		routedViewKey([]string{"kb-a"}, routedTokenReadOnly),          // tok-ro
		routedViewKey([]string{"kb-a"}, routedBridgeReadOnly),         // bridge above
		routedViewKey([]string{"kb-a", "kb-b"}, routedNormal),         // tok-mixed
		routedViewKey([]string{"kb-a"}, routedNormal),                 // tok-map, tok-transfer ?kbs=kb-a, tok-mixed ?kbs=kb-a
		routedViewKey([]string{"kb-a", "kb-c"}, routedNormal),         // tok-transfer
		routedViewKey(nil, routedNormal),                              // tok-none
	} {
		want[k] = true
	}
	for k := range f.multi.routedViews {
		if !want[k] {
			t.Errorf("unexpected cached view %q", k)
		}
	}
	if len(f.multi.routedViews) != len(want) {
		t.Errorf("routedViews has %d entries, want %d", len(f.multi.routedViews), len(want))
	}
}

// D1096 non-disclosure: a name the token cannot read is "unknown", exactly
// like a name that is not mounted.
func TestRoutedView_UnreadableNameIsIndistinguishableFromUnmounted(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b", "kb-c")

	body := func(name string) string {
		rr := f.post(t, tokRO, RoutedMountPath+"?kbs="+name, toolsListBody)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("?kbs=%s: status %d, want 400", name, rr.Code)
		}
		return strings.ReplaceAll(rr.Body.String(), name, "<name>")
	}
	if a, b := body("kb-b"), body("kb-zzz"); a != b {
		t.Errorf("the 400 differs between an unreadable and an unmounted name:\n%q\n%q", a, b)
	}
	if strings.Contains(body("kb-b"), "kb-c") {
		t.Error("the 400 names a KB the token cannot read")
	}

	// A call naming either, with or without the name in ?kbs=, gets the same text.
	for _, path := range []string{RoutedMountPath, RoutedMountPath + "?kbs=%s,kb-a"} {
		text := func(name string) string {
			p := strings.ReplaceAll(path, "%s", name)
			res := f.call(t, tokMixed, p, "concept_read", map[string]any{"kb": name, "id": "x"})
			if !res.IsError {
				t.Fatalf("%s: a call naming %s succeeded", p, name)
			}
			return strings.ReplaceAll(res.Content[0].Text, name, "<name>")
		}
		if a, b := text("kb-c"), text("kb-zzz"); a != b {
			t.Errorf("%s: unreadable and unmounted names answer differently:\n%q\n%q", path, a, b)
		}
	}
}

func TestRoutedView_KBStatusConnectionNamesOnlyReadableKBs(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b", "kb-c")
	res := f.call(t, tokRO, RoutedMountPath+"?kbs=kb-a,kb-b", "kb_status", nil)
	if res.IsError {
		t.Fatalf("kb_status: %+v", res)
	}
	text := res.Content[0].Text
	var obj map[string]any
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		t.Fatal(err)
	}
	conn, _ := obj["connection"].(map[string]any)
	asList := func(v any) string {
		var out []string
		for _, x := range v.([]any) {
			out = append(out, x.(string))
		}
		return strings.Join(out, ",")
	}
	if asList(conn["kbs"]) != "kb-a" || asList(conn["mounted"]) != "kb-a" || asList(conn["unknown"]) != "kb-b" {
		t.Errorf("connection = %v, want kbs [kb-a], mounted [kb-a], unknown [kb-b]", conn)
	}
	if conn["unbound"] != nil {
		t.Errorf("unbound = %v, want none", conn["unbound"])
	}
	if strings.Contains(text, "kb-c") {
		t.Errorf("kb_status names kb-c to a token that cannot read it: %s", text)
	}
}

func TestRoutedView_AliasToAnUnreadableTargetDiscloses_Nothing(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, []config.KBAlias{{From: "kb-old", To: "kb-b"}, {From: "kb-older", To: "kb-a"}}, "kb-a", "kb-b")
	res := f.call(t, tokRO, RoutedMountPath+"?kbs=kb-old,kb-a", "kb_status", nil)
	if res.IsError {
		t.Fatalf("kb_status: %+v", res)
	}
	text := res.Content[0].Text
	var obj map[string]any
	_ = json.Unmarshal([]byte(text), &obj)
	conn, _ := obj["connection"].(map[string]any)
	if conn["renamed"] != nil || conn["unknown"] == nil {
		t.Errorf("connection = %v, want unknown [kb-old] and no renamed", conn)
	}
	if strings.Contains(text, "kb-b") {
		t.Errorf("an alias disclosed its unreadable target: %s", text)
	}
	// The same alias to a readable target still works.
	rr := f.post(t, tokRO, RoutedMountPath+"?kbs=kb-older", toolsListBody)
	if rr.Code != http.StatusOK {
		t.Errorf("alias to a readable target: status %d", rr.Code)
	}
}

func TestRoutedView_TransferTargetUnreadableAnswersLikeUnmounted(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "kb-b", "kb-c")
	call := func(tool, target string, path string) string {
		args := map[string]any{"kb": "kb-a", "target_kb": target}
		if tool == "concept_transfer" {
			args["moves"] = []map[string]any{{"source_id": "manutenzione/marker", "target_id": "manutenzione/marker"}}
		} else {
			args["paths"] = []string{"skills/x"}
		}
		res := f.call(t, tokTransfer, path, tool, args)
		if !res.IsError {
			t.Fatalf("%s to %s succeeded: %+v", tool, target, res)
		}
		return strings.ReplaceAll(res.Content[0].Text, target, "<t>")
	}
	for _, tool := range []string{"concept_transfer", "artifact_move"} {
		if a, b := call(tool, "kb-b", RoutedMountPath), call(tool, "kb-zzz", RoutedMountPath); a != b {
			t.Errorf("%s: unreadable and unmounted targets answer differently:\n%q\n%q", tool, a, b)
		}
		if got := call(tool, "kb-c", RoutedMountPath+"?kbs=kb-a"); !strings.Contains(got, "not bound to this connection") {
			t.Errorf("%s: a readable KB outside the set = %q, want not bound", tool, got)
		}
	}
}
