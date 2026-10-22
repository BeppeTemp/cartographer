package mcpbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
)

const testToken = "bridge-tok"

// routedServer is a real routed Cartographer server over httptest: the bridge is
// tested against the server that enforces the set, not against a stand-in.
type routedServer struct {
	*httptest.Server
	mu    sync.Mutex
	urls  []string
	count atomic.Int32
	// authors are the Cartographer-Author values of each request, by JSON-RPC
	// method and id ("tools/call 3").
	authors map[string][]string
}

func newRoutedServer(t *testing.T, names ...string) *routedServer {
	t.Helper()
	multi := mcpserver.NewMultiKBServer("test")
	scopes := make([]auth.KBScope, 0, len(names))
	for _, n := range names {
		k := kbtest.New(t)
		multi.MountKB(n, func(s *mcpserver.Server) { mcpserver.RegisterKBTools(s, k, mcpserver.Deps{}) })
		scopes = append(scopes, auth.KBScope{KB: n, Write: true})
	}
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatal(err)
	}
	store := auth.NewScopedTokenStore([]auth.ScopedToken{{Token: testToken, Scopes: scopes}})
	h := store.Middleware(multi.Handler())
	rs := &routedServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.count.Add(1)
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var m message
		_ = json.Unmarshal(body, &m)
		rs.mu.Lock()
		rs.urls = append(rs.urls, r.URL.RequestURI())
		if rs.authors == nil {
			rs.authors = map[string][]string{}
		}
		rs.authors[m.Method+" "+string(m.ID)] = r.Header.Values(client.AuthorHeader)
		rs.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *routedServer) seen() []string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]string(nil), rs.urls...)
}

func bridgeConfig(rs *routedServer) *clientconfig.Config {
	return &clientconfig.Config{
		ServerName: "cartographer", ServerURL: rs.URL + "/mcp", ServerRoutedPath: mcpserver.RoutedMountPath,
		Auth: true, Token: "${CARTOGRAPHER_TOKEN}",
		KnownKBs: []string{"kb-a", "kb-b", "kb-c"},
	}
}

// exchange runs a scripted stdio session to EOF and returns the responses by id.
func exchange(t *testing.T, b *Bridge, script ...string) map[string]map[string]any {
	t.Helper()
	b.In = strings.NewReader(strings.Join(script, "\r\n") + "\r\n") // \r\n is tolerated
	var out bytes.Buffer
	b.Out = &out
	var logs bytes.Buffer
	b.Log = &logs
	if err := b.Run(context.Background()); err != nil {
		t.Fatalf("run: %v\nlogs: %s", err, logs.String())
	}
	got := map[string]map[string]any{}
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("stdout line is not JSON (stdout is the protocol): %q", sc.Text())
		}
		got[string(mustJSON(m["id"]))] = m
	}
	return got
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

const (
	initMsg   = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	initedMsg = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	listMsg   = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
)

func callMsg(id int, tool, kb string) string {
	args := map[string]any{}
	if kb != "" {
		args["kb"] = kb
	}
	return string(mustJSON(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args}}))
}

func toolText(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	res, _ := m["result"].(map[string]any)
	if res == nil {
		t.Fatalf("no result in %v", m)
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content in %v", res)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

// Acceptance of WP1: from a bound folder the session lists only the bound KBs,
// the URL carries kbs=, and a call to a KB outside the set is the server's own
// refusal.
func TestBridgeProxiesToTheBoundSet(t *testing.T) {
	rs := newRoutedServer(t, "kb-a", "kb-b", "kb-c")
	cfg := bridgeConfig(rs)
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws, "kb-b", "kb-a")

	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: ws, Token: testToken}
	got := exchange(t, b, initMsg, initedMsg, listMsg,
		callMsg(3, "kb_status", "kb-a"), callMsg(4, "kb_status", "kb-c"))

	if _, ok := got["1"]["result"]; !ok {
		t.Fatalf("initialize: %v", got["1"])
	}
	tools, _ := got["2"]["result"].(map[string]any)["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("tools/list is empty: %v", got["2"])
	}
	text, isErr := toolText(t, got["3"])
	if isErr || !strings.Contains(text, "kb-a") {
		t.Fatalf("kb_status kb-a: %q err=%v", text, isErr)
	}
	// The server names what the connection can reach: exactly the bound set.
	var status struct {
		Connection struct {
			KBs []string `json:"kbs"`
		} `json:"connection"`
	}
	if err := json.Unmarshal([]byte(text), &status); err != nil || strings.Join(status.Connection.KBs, ",") != "kb-a,kb-b" {
		t.Fatalf("connection kbs = %v (%v)", status.Connection.KBs, err)
	}
	text, isErr = toolText(t, got["4"])
	if !isErr || !strings.Contains(text, "not bound to this connection") || !strings.Contains(text, "cartographer workspace resolve") {
		t.Fatalf("a KB outside the set must be the server's refusal, got %q err=%v", text, isErr)
	}
	for _, u := range rs.seen() {
		if !strings.HasSuffix(u, "/mcp/routed?kbs=kb-a,kb-b") {
			t.Fatalf("request URL %q does not carry the bound set", u)
		}
	}
}

// An unbound folder reaches the whole routed endpoint, read-only: no kbs=, and
// access=read, which the server enforces.
func TestBridgeUnboundFolderSendsNoKbs(t *testing.T) {
	rs := newRoutedServer(t, "kb-a", "kb-b")
	cfg := bridgeConfig(rs)
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: t.TempDir(), Token: testToken}
	got := exchange(t, b, initMsg, initedMsg, listMsg, callMsg(3, "kb_status", "kb-b"))
	if text, isErr := toolText(t, got["3"]); isErr {
		t.Fatalf("unbound folder refused kb-b: %s", text)
	}
	for _, u := range rs.seen() {
		if strings.Contains(u, "kbs=") || !strings.HasSuffix(u, "?access=read") {
			t.Fatalf("unbound folder sent %q, want the bare routed path with access=read", u)
		}
	}
	res, _ := got["2"]["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("an unbound folder lists no tool")
	}
	for _, raw := range tools {
		name, _ := raw.(map[string]any)["name"].(string)
		if mcpserver.ToolRequiresWrite(name) {
			t.Errorf("unbound folder lists the write tool %q", name)
		}
	}
}

// An unbound folder with an explicit provider binding (D169) is read-only too;
// a bound folder is not.
func TestBridgeReadOnlyOnlyWhenUnbound(t *testing.T) {
	rs := newRoutedServer(t, "kb-a", "kb-b")
	cfg := bridgeConfig(rs)
	cfg.Clients = map[string]clientconfig.ClientBinding{"claude": {KBs: []string{"kb-b"}, KBsSet: true}}
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws, "kb-a")

	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: t.TempDir(), Token: testToken}
	exchange(t, b, initMsg, initedMsg, listMsg)
	for _, u := range rs.seen() {
		if !strings.HasSuffix(u, "?kbs=kb-b&access=read") {
			t.Fatalf("unbound folder with a provider binding sent %q", u)
		}
	}

	rs = newRoutedServer(t, "kb-a", "kb-b")
	cfg.ServerURL = rs.URL + "/mcp"
	b = &Bridge{Cfg: cfg, Provider: "claude", Dir: ws, Token: testToken}
	got := exchange(t, b, initMsg, initedMsg, listMsg)
	for _, u := range rs.seen() {
		if strings.Contains(u, "access") {
			t.Fatalf("a bound folder asked for read-only: %q", u)
		}
	}
	res, _ := got["2"]["result"].(map[string]any)
	hasWrite := false
	for _, raw := range res["tools"].([]any) {
		if mcpserver.ToolRequiresWrite(raw.(map[string]any)["name"].(string)) {
			hasWrite = true
		}
	}
	if !hasWrite {
		t.Error("a bound folder must list write tools")
	}
}

// Fail closed across versions: a server that does not confirm read-only mode
// (it ignores ?access=) is not proxied; the session has no tools.
func TestBridgeServerWithoutConfirmationServesNoTools(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"concept_write","inputSchema":{"type":"object"}}]}}`)
	}))
	defer srv.Close()
	cfg := &clientconfig.Config{
		ServerName: "cartographer", ServerURL: srv.URL + "/mcp", ServerRoutedPath: mcpserver.RoutedMountPath,
		KnownKBs: []string{"kb-a"},
	}
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: t.TempDir()}
	got := exchange(t, b, initMsg, initedMsg, listMsg, callMsg(3, "concept_write", "kb-a"))

	if n := hits.Load(); n != 1 {
		t.Fatalf("the bridge kept proxying to an unconfirmed server (%d requests)", n)
	}
	// The first response is replaced by the local answer, not relayed.
	res, _ := got["1"]["result"].(map[string]any)
	if instr, _ := res["instructions"].(string); !strings.Contains(instr, "upgraded") {
		t.Fatalf("initialize instructions = %v", res["instructions"])
	}
	if tools, _ := got["2"]["result"].(map[string]any)["tools"].([]any); len(tools) != 0 {
		t.Fatalf("tools = %v, want none", tools)
	}
	if got["3"]["error"] == nil {
		t.Fatalf("a tool call must be refused locally: %v", got["3"])
	}
}

// TRAP: an empty set must never reach the server as kbs= (empty means every KB
// there). The bridge answers the handshake and an empty tool list itself.
func TestBridgeNoKBNeverContactsTheServer(t *testing.T) {
	rs := newRoutedServer(t, "kb-a", "kb-b")
	cfg := bridgeConfig(rs)
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws) // --no-kb

	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: ws, Token: testToken}
	got := exchange(t, b, initMsg, initedMsg, listMsg, callMsg(3, "kb_status", "kb-a"))

	if n := rs.count.Load(); n != 0 {
		t.Fatalf("server contacted %d times: %v", n, rs.seen())
	}
	res, _ := got["1"]["result"].(map[string]any)
	if instr, _ := res["instructions"].(string); !strings.Contains(instr, "bound to no KB") {
		t.Fatalf("initialize instructions = %v", res["instructions"])
	}
	if res["protocolVersion"] != "2025-06-18" {
		t.Fatalf("protocolVersion = %v", res["protocolVersion"])
	}
	if tools, _ := got["2"]["result"].(map[string]any)["tools"].([]any); len(tools) != 0 {
		t.Fatalf("tools = %v", tools)
	}
	if got["3"]["error"] == nil {
		t.Fatalf("a tool call must be refused locally: %v", got["3"])
	}
}

func TestBridgeUnreachableServerSaysSo(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	cfg := bridgeConfig(rs)
	rs.Close()
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: t.TempDir(), Token: testToken}
	got := exchange(t, b, initMsg)
	msg, _ := got["1"]["error"].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "could not reach") {
		t.Fatalf("error = %v", got["1"])
	}
}

func TestBridgeBadTokenNamesTheVariable(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	cfg := bridgeConfig(rs)
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: t.TempDir(), Token: "wrong"}
	got := exchange(t, b, initMsg)
	msg, _ := got["1"]["error"].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "$CARTOGRAPHER_TOKEN") {
		t.Fatalf("error = %v", got["1"])
	}
}

// A client that declares roots while the working directory is bound to nothing
// is scoped by its roots (an IDE host that spawns outside the workspace).
func TestBridgeRootsFallback(t *testing.T) {
	rs := newRoutedServer(t, "kb-a", "kb-b")
	cfg := bridgeConfig(rs)
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws, "kb-b")

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: t.TempDir(), Token: testToken, In: inR, Out: outW, Log: io.Discard}
	done := make(chan error, 1)
	go func() { done <- b.Run(context.Background()); outW.Close() }()

	send := func(s string) { _, _ = io.WriteString(inW, s+"\n") }
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	read := func() map[string]any {
		if !sc.Scan() {
			t.Fatalf("stdout closed early: %v", sc.Err())
		}
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"roots":{}},"clientInfo":{"name":"t","version":"1"}}}`)
	initRes, ok := read()["result"].(map[string]any)
	if !ok {
		t.Fatal("no initialize result")
	}
	// The handshake must not name KBs of a set the roots may still replace.
	if instr, _ := initRes["instructions"].(string); instr != rootsPendingInstructions || strings.Contains(instr, "kb-") {
		t.Fatalf("initialize instructions = %q", instr)
	}
	send(initedMsg)
	req := read()
	if req["method"] != "roots/list" {
		t.Fatalf("expected roots/list, got %v", req)
	}
	send(string(mustJSON(map[string]any{"jsonrpc": "2.0", "id": req["id"],
		"result": map[string]any{"roots": []any{map[string]any{"uri": fileURI(ws)}}}})))
	send(callMsg(2, "kb_status", "kb-a"))
	text, isErr := toolText(t, read())
	if !isErr || !strings.Contains(text, "not bound to this connection") {
		t.Fatalf("kb-a must be outside the root-derived set, got %q err=%v", text, isErr)
	}
	inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bridge did not stop at EOF")
	}
}

// A client that declares roots but is already inside a binding is not asked.
func TestBridgeBoundFolderIgnoresRoots(t *testing.T) {
	rs := newRoutedServer(t, "kb-a", "kb-b")
	cfg := bridgeConfig(rs)
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws, "kb-a")
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: ws, Token: testToken}
	got := exchange(t, b,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"roots":{}},"clientInfo":{"name":"t","version":"1"}}}`,
		initedMsg, callMsg(2, "kb_status", "kb-a"))
	if _, isErr := toolText(t, got["2"]); isErr {
		t.Fatal("kb-a refused")
	}
	if _, asked := got[`"cartographer-roots-1"`]; asked {
		t.Fatal("bridge asked for roots inside a binding")
	}
}

// The scope at initialize can be "no KB" (the provider's own binding is empty)
// and the roots can still bind a non-empty set: later requests must reach the
// server, which is stateless and needs no session opened by initialize.
func TestBridgeRootsLiftAnEmptyProviderScope(t *testing.T) {
	rs := newRoutedServer(t, "kb-a", "kb-b")
	cfg := bridgeConfig(rs)
	cfg.Clients = map[string]clientconfig.ClientBinding{"claude": {KBsSet: true}} // bound to no KB
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws, "kb-b")

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: t.TempDir(), Token: testToken, In: inR, Out: outW, Log: io.Discard}
	go func() { _ = b.Run(context.Background()); outW.Close() }()
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	read := func() map[string]any {
		if !sc.Scan() {
			t.Fatalf("stdout closed early: %v", sc.Err())
		}
		var m map[string]any
		_ = json.Unmarshal(sc.Bytes(), &m)
		return m
	}
	send := func(s string) { _, _ = io.WriteString(inW, s+"\n") }

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"roots":{}},"clientInfo":{"name":"t","version":"1"}}}`)
	read()
	send(initedMsg)
	req := read()
	send(string(mustJSON(map[string]any{"jsonrpc": "2.0", "id": req["id"],
		"result": map[string]any{"roots": []any{map[string]any{"uri": fileURI(ws)}}}})))
	send(listMsg)
	res, _ := read()["result"].(map[string]any)
	if tools, _ := res["tools"].([]any); len(tools) == 0 {
		t.Fatalf("tools/list after roots is empty: %v", res)
	}
	inW.Close()
}
