package mcpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func echoTool(plugin, name string, writes bool) PluginTool {
	return PluginTool{
		Plugin: plugin, Name: name, Description: "does " + name, Writes: writes,
		Params: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		Run: func(_ context.Context, c PluginCall) (string, error) {
			return "ran " + plugin + "/" + name + " " + string(c.Params), nil
		},
	}
}

func listed(t *testing.T, m map[string]any) map[string]map[string]any {
	t.Helper()
	res, _ := m["result"].(map[string]any)
	if res == nil {
		t.Fatalf("no result: %v", m)
	}
	out := map[string]map[string]any{}
	for _, raw := range res["tools"].([]any) {
		tool := raw.(map[string]any)
		out[tool["name"].(string)] = tool
	}
	return out
}

// pluginBridge is a bridge bound to kb-a in a fresh folder with the given
// plugin tools; its log is a buffer the test reads back.
func pluginBridge(t *testing.T, rs *routedServer, tools ...PluginTool) *Bridge {
	t.Helper()
	cfg := bridgeConfig(rs)
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws, "kb-a")
	return &Bridge{Cfg: cfg, Provider: "claude", Dir: ws, Token: testToken, Plugins: tools}
}

func TestPluginToolsAreListedAfterTheServersWithTheirSchemaAndHint(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	b := pluginBridge(t, rs, echoTool("team-forks", "find-all", false), echoTool("team-forks", "push", true))
	got := exchange(t, b, initMsg, initedMsg, listMsg)
	tools := listed(t, got["2"])
	if _, ok := tools["kb_status"]; !ok {
		t.Fatalf("the server's own tools are gone: %v", tools)
	}
	ro, w := tools["team_forks_find_all"], tools["team_forks_push"]
	if ro == nil || w == nil {
		t.Fatalf("plugin tools missing (hyphens become underscores): %v", tools)
	}
	if d, _ := ro["description"].(string); d != "[plugin team-forks] does find-all" {
		t.Errorf("description %q", d)
	}
	if _, ok := ro["inputSchema"].(map[string]any)["properties"]; !ok {
		t.Errorf("inputSchema is not the registered params: %v", ro["inputSchema"])
	}
	if h, _ := ro["annotations"].(map[string]any)["readOnlyHint"].(bool); !h {
		t.Errorf("a read tool is not annotated read-only: %v", ro["annotations"])
	}
	if h, _ := w["annotations"].(map[string]any)["readOnlyHint"].(bool); h {
		t.Errorf("a write tool is annotated read-only")
	}
}

func TestPluginToolNamesLongerThan64AreDroppedAndLogged(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	long := strings.Repeat("x", 48) // plugin + "_" + 48 > 64
	b := pluginBridge(t, rs, echoTool("team-forks-plugin-name", long, false), echoTool("p", "ok", false))
	got := exchange(t, b, initMsg, initedMsg, listMsg)
	tools := listed(t, got["2"])
	for n := range tools {
		if len(n) > 64 {
			t.Errorf("tool %q is over 64 characters", n)
		}
		if strings.HasPrefix(n, "team_forks_plugin_name_x") {
			t.Errorf("the long tool was truncated, not dropped: %q", n)
		}
	}
	if tools["p_ok"] == nil {
		t.Errorf("a short tool next to it was lost")
	}
	if l := b.Log.(*bytes.Buffer).String(); !strings.Contains(l, "over 64 characters") {
		t.Errorf("the drop was not logged: %q", l)
	}
}

func TestPluginToolsCollideWithServerToolsAndBetweenPlugins(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	// kb_status is a server tool; a + b_c and a_b + c both want "a_b_c".
	b := pluginBridge(t, rs, echoTool("kb", "status", false), echoTool("a_b", "c", false), echoTool("a", "b_c", false))
	got := exchange(t, b, initMsg, initedMsg, listMsg, callMsg(3, "kb_status", "kb-a"), callMsg(4, "a_b_c", ""))
	tools := listed(t, got["2"])
	if d, _ := tools["kb_status"]["description"].(string); strings.Contains(d, "[plugin") {
		t.Errorf("a plugin tool shadowed the server's kb_status")
	}
	if d, _ := tools["a_b_c"]["description"].(string); !strings.HasPrefix(d, "[plugin a] ") {
		t.Errorf("a_b_c belongs to the earlier plugin in name order, got %q", d)
	}
	if text, isErr := toolText(t, got["3"]); isErr || !strings.Contains(text, "kb-a") {
		t.Errorf("kb_status must still reach the server: %q %v", text, isErr)
	}
	if text, _ := toolText(t, got["4"]); !strings.Contains(text, "ran a/b_c") {
		t.Errorf("a_b_c ran %q", text)
	}
	l := b.Log.(*bytes.Buffer).String()
	if !strings.Contains(l, "kb_status is not served") || !strings.Contains(l, "a_b_c is not served") {
		t.Errorf("collisions not logged: %q", l)
	}
}

func TestPluginToolCallRunsLocallyAndNeverReachesTheServer(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	b := pluginBridge(t, rs, echoTool("p", "t", false))
	// A call before any tools/list settles the names by itself.
	got := exchange(t, b, initMsg, initedMsg, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"p_t","arguments":{"q":"x"}}}`)
	text, isErr := toolText(t, got["5"])
	if isErr || text != `ran p/t {"q":"x"}` {
		t.Fatalf("got %q err=%v", text, isErr)
	}
	for _, u := range rs.seen() {
		if strings.Contains(u, "p_t") {
			t.Errorf("the plugin call reached the server: %s", u)
		}
	}
}

func TestPluginToolErrorIsAnErrorResultNotARPCError(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	bad := echoTool("p", "t", false)
	bad.Run = func(context.Context, PluginCall) (string, error) { return "", errors.New("boom") }
	b := pluginBridge(t, rs, bad)
	got := exchange(t, b, initMsg, initedMsg, listMsg, callMsg(3, "p_t", ""))
	if text, isErr := toolText(t, got["3"]); !isErr || text != "boom" {
		t.Errorf("got %q err=%v", text, isErr)
	}
	if len(listed(t, got["2"])) == 0 {
		t.Error("tools/list broke")
	}
}

func TestPluginWriteToolIsHiddenAndRefusedInAReadOnlySession(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	b := pluginBridge(t, rs, echoTool("p", "read", false), echoTool("p", "write", true))
	b.Cfg = bridgeConfig(rs) // a binding elsewhere, none here: the unbound default is read-only
	b.Dir = mkdir(t, t.TempDir())
	got := exchange(t, b, initMsg, initedMsg, listMsg, callMsg(3, "p_write", ""), callMsg(4, "p_read", ""))
	if !b.currentScope().ReadOnly {
		t.Fatal("the test scope is not read-only")
	}
	tools := listed(t, got["2"])
	if tools["p_write"] != nil || tools["p_read"] == nil {
		t.Errorf("read-only list: %v", tools)
	}
	if text, isErr := toolText(t, got["3"]); !isErr || !strings.Contains(text, "read-only") {
		t.Errorf("write call: %q err=%v", text, isErr)
	}
	if text, isErr := toolText(t, got["4"]); isErr || !strings.Contains(text, "ran p/read") {
		t.Errorf("read call: %q err=%v", text, isErr)
	}
}

func TestPluginKBCallCarriesTheSessionScopeAuthorAndSetOnly(t *testing.T) {
	rs := newRoutedServer(t, "kb-a", "kb-b")
	var inside, outside string
	tool := echoTool("p", "t", false)
	tool.Run = func(ctx context.Context, c PluginCall) (string, error) {
		raw, err := c.KB.CallContext(ctx, "kb_status", map[string]any{"kb": "kb-a"})
		if err != nil {
			return "", err
		}
		inside = string(raw)
		_, err = c.KB.CallContext(ctx, "kb_status", map[string]any{"kb": "kb-b"})
		outside = err.Error()
		return "done", nil
	}
	b := pluginBridge(t, rs, tool)
	b.Cfg.Identities = map[string]string{"kb-a": "User <user@example.com>"}
	got := exchange(t, b, initMsg, initedMsg, listMsg, callMsg(3, "p_t", ""))
	if text, isErr := toolText(t, got["3"]); isErr || text != "done" {
		t.Fatalf("%q %v", text, isErr)
	}
	if !strings.Contains(inside, "kb-a") {
		t.Errorf("kb.call result %q", inside)
	}
	if !strings.Contains(outside, "not one of the KBs this session serves") {
		t.Errorf("a KB outside the session set was reachable: %q", outside)
	}
	found := false
	for _, u := range rs.seen() {
		if strings.Contains(u, "kbs=kb-a") {
			found = true
		}
	}
	if !found {
		t.Errorf("the kb.call did not carry the session scope: %v", rs.seen())
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var author bool
	for k, v := range rs.authors {
		if strings.HasPrefix(k, "tools/call \"cartographer-plugin-") && len(v) == 1 {
			author = true
		}
	}
	if !author {
		t.Errorf("the bridge's own tools/call carried no Cartographer-Author: %v", rs.authors)
	}
}

// pagedServer answers tools/list in two pages, in JSON or SSE.
func pagedServer(t *testing.T, sse bool) *routedServer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m message
		_ = json.Unmarshal(raw, &m)
		var p struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(m.Params, &p)
		res := map[string]any{"tools": []any{map[string]any{"name": "srv_one"}}, "nextCursor": "2"}
		if p.Cursor != "" {
			res = map[string]any{"tools": []any{map[string]any{"name": "p_t"}}}
		}
		out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": res})
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write(append(append([]byte("data: "), out...), '\n', '\n'))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return &routedServer{Server: srv}
}

func TestPluginToolsAreMergedOnTheLastPageOnlyJSONAndSSE(t *testing.T) {
	for _, sse := range []bool{false, true} {
		b := pluginBridge(t, pagedServer(t, sse), echoTool("p", "t", false), echoTool("p", "u", false))
		got := exchange(t, b, initMsg, initedMsg, listMsg,
			`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"cursor":"2"}}`)
		first, last := listed(t, got["2"]), listed(t, got["3"])
		if len(first) != 1 || first["srv_one"] == nil {
			t.Errorf("sse=%v: the first page was touched: %v", sse, first)
		}
		// p_t is a server tool of page two: the plugin's p_t is dropped, p_u kept.
		if d, _ := last["p_t"]["description"].(string); strings.Contains(d, "[plugin") {
			t.Errorf("sse=%v: the plugin shadowed the server's p_t", sse)
		}
		if last["p_u"] == nil {
			t.Errorf("sse=%v: the last page lacks the plugin tool: %v", sse, last)
		}
	}
}

func TestPluginStdoutStaysProtocolOnly(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	tool := echoTool("p", "t", false)
	tool.Run = func(_ context.Context, c PluginCall) (string, error) {
		_, _ = io.WriteString(c.Log, "plugin p: printed from the script\n")
		return "ok", nil
	}
	b := pluginBridge(t, rs, tool)
	got := exchange(t, b, initMsg, initedMsg, callMsg(3, "p_t", "")) // exchange fails on a non-JSON stdout line
	if text, _ := toolText(t, got["3"]); text != "ok" {
		t.Errorf("%q", text)
	}
	if !strings.Contains(b.Log.(*bytes.Buffer).String(), "printed from the script") {
		t.Error("the script's output did not reach the bridge log")
	}
}

func TestPluginCallsRunConcurrentlyAndCancelWithTheirNotification(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	var running atomic.Int32
	cancelled := make(chan struct{})
	slow := echoTool("p", "slow", false)
	slow.Run = func(ctx context.Context, _ PluginCall) (string, error) {
		running.Add(1)
		<-ctx.Done()
		close(cancelled)
		return "", ctx.Err()
	}
	b := pluginBridge(t, rs, slow, echoTool("p", "fast", false))
	pr, pw := io.Pipe()
	var out bytes.Buffer
	var mu sync.Mutex
	b.In, b.Out, b.Log = pr, lockedWriter{&mu, &out}, io.Discard
	done := make(chan error, 1)
	go func() { done <- b.Run(context.Background()) }()
	var wmu sync.Mutex
	send := func(s string) {
		wmu.Lock()
		defer wmu.Unlock()
		_, _ = io.WriteString(pw, s+"\n")
	}
	send(initMsg)
	send(initedMsg)
	send(callMsg(3, "p_slow", ""))
	for running.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	var many sync.WaitGroup
	for i := 10; i < 30; i++ {
		many.Add(1)
		go func(i int) { defer many.Done(); send(callMsg(i, "p_fast", "")) }(i)
	}
	many.Wait()
	send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	select {
	case <-cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("the notification did not cancel the running plugin call")
	}
	_ = pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if n := strings.Count(out.String(), "ran p/fast"); n != 20 {
		t.Errorf("%d fast answers, want 20", n)
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// A read-only listing omits the server's write tools, so a plugin tool named
// like one is free there; once the scope widens the server's tool must win.
func TestPluginToolNameFreeInAReadOnlyScopeYieldsAfterTheScopeWidens(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	b := pluginBridge(t, rs, echoTool("concept", "write", false))
	b.HTTP = &http.Client{}
	b.Log = io.Discard
	ro := Scope{KBs: []string{"kb-a"}, ReadOnly: true, Source: "test"}
	b.scope = &ro
	set, err := b.settle(context.Background(), ro)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set.byName["concept_write"]; !ok {
		t.Fatalf("the name should be free in a read-only listing: %v", set.order)
	}
	rw := Scope{KBs: []string{"kb-a"}, Source: "test"}
	b.scope = &rw
	if set, err = b.settle(context.Background(), rw); err != nil {
		t.Fatal(err)
	}
	if _, ok := set.byName["concept_write"]; ok {
		t.Error("the plugin tool still shadows the server's concept_write after the scope widened")
	}
	// The client was told concept_write is a read-only plugin tool: a call that
	// now names the server's write tool is refused, not forwarded.
	var out bytes.Buffer
	b.Out = &out
	m := message{ID: json.RawMessage("9"), Method: "tools/call", Params: json.RawMessage(`{"name":"concept_write","arguments":{"kb":"kb-a"}}`)}
	before := len(rs.seen())
	b.runPluginCall(context.Background(), rw, mustJSON(m), m, func() {})
	if !strings.Contains(out.String(), "list the tools again") || !strings.Contains(out.String(), `"isError":true`) {
		t.Errorf("answer %q", out.String())
	}
	if n := len(rs.seen()); n != before {
		t.Errorf("the refused call reached the server (%d requests)", n-before)
	}
	// The client lists the tools again, as the error says: the server's tool
	// is reachable once more.
	out.Reset()
	lm := message{ID: json.RawMessage("10"), Method: "tools/list"}
	b.forward(context.Background(), rw, []byte(`{"jsonrpc":"2.0","id":10,"method":"tools/list"}`), lm, func() {})
	out.Reset()
	m2 := message{ID: json.RawMessage("11"), Method: "tools/call", Params: m.Params}
	b.runPluginCall(context.Background(), rw, []byte(`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"concept_write","arguments":{"kb":"kb-a"}}}`), m2, func() {})
	if strings.Contains(out.String(), "list the tools again") || !strings.Contains(out.String(), `"id":11`) {
		t.Errorf("after a re-list the call is still refused: %q", out.String())
	}
}

func TestPluginKBClientRefusesArgumentsThatAreNotAnObject(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	b := pluginBridge(t, rs)
	b.HTTP = &http.Client{}
	sc := Scope{KBs: []string{"kb-a"}, Source: "test"}
	b.scope = &sc
	for _, args := range []any{nil, "kb-a", []any{"kb-a"}} {
		if _, err := b.sessionKB().CallContext(context.Background(), "kb_status", args); err == nil {
			t.Errorf("args %v were not refused", args)
		}
	}
}
