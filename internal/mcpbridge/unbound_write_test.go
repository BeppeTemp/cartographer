package mcpbridge

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
)

func listsWriteTool(t *testing.T, m map[string]any) bool {
	t.Helper()
	res, _ := m["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	for _, raw := range tools {
		if mcpserver.ToolRequiresWrite(raw.(map[string]any)["name"].(string)) {
			return true
		}
	}
	return false
}

func instructionsOf(m map[string]any) string {
	res, _ := m["result"].(map[string]any)
	s, _ := res["instructions"].(string)
	return s
}

// #1356: unbound_access write serves an unbound folder read-write: no
// access=read, write tools listed, a write reaches the server, and the
// handshake says the session is unbound.
func TestBridgeUnboundWrite(t *testing.T) {
	rs := newRoutedServer(t, "kb-a", "kb-b")
	cfg := bridgeConfig(rs)
	cfg.UnboundModes = map[string]string{"default": "write"}
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: t.TempDir(), Token: testToken}
	got := exchange(t, b, initMsg, initedMsg, listMsg, string(mustJSON(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "concept_write", "arguments": map[string]any{
			"kb": "kb-b", "id": "notes/x", "frontmatter": map[string]any{"type": "Note", "title": "X"}, "body": "# X\n"}},
	})))
	for _, u := range rs.seen() {
		if strings.Contains(u, "access") || strings.Contains(u, "kbs=") {
			t.Fatalf("unbound write sent %q", u)
		}
	}
	if !listsWriteTool(t, got["2"]) {
		t.Fatal("unbound write lists no write tool")
	}
	if text, isErr := toolText(t, got["3"]); isErr {
		t.Fatalf("concept_write refused: %s", text)
	}
	if in := instructionsOf(got["1"]); !strings.Contains(in, "has write access") || !strings.Contains(in, "write access") {
		t.Errorf("initialize instructions = %q, want the unbound sentence", in)
	}
}

// With a provider binding the unbound write session keeps kbs=; a per-provider
// entry only affects its provider.
func TestBridgeUnboundWriteProviderBindingAndOverride(t *testing.T) {
	rs := newRoutedServer(t, "kb-a", "kb-b")
	cfg := bridgeConfig(rs)
	cfg.Clients = map[string]clientconfig.ClientBinding{"claude": {KBs: []string{"kb-b"}, KBsSet: true}}
	cfg.UnboundModes = map[string]string{"claude": "write"}
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: t.TempDir(), Token: testToken}
	exchange(t, b, initMsg, initedMsg, listMsg)
	for _, u := range rs.seen() {
		if !strings.HasSuffix(u, "?kbs=kb-b&provisioned=1") {
			t.Fatalf("unbound write with provider binding sent %q", u)
		}
	}

	rs = newRoutedServer(t, "kb-a", "kb-b")
	cfg.ServerURL = rs.URL + "/mcp"
	b = &Bridge{Cfg: cfg, Provider: "codex", Dir: t.TempDir(), Token: testToken}
	got := exchange(t, b, initMsg, initedMsg, listMsg)
	for _, u := range rs.seen() {
		if !strings.HasSuffix(u, "?access=read&provisioned=1") {
			t.Fatalf("another provider sent %q, want access=read", u)
		}
	}
	if listsWriteTool(t, got["2"]) {
		t.Error("another provider lists a write tool")
	}
	if strings.Contains(instructionsOf(got["1"]), "has write access") {
		t.Error("read-only unbound session carries the unbound sentence")
	}
}

// A bound folder is identical with the setting on: no unbound sentence.
func TestBridgeBoundFolderIgnoresUnboundAccess(t *testing.T) {
	rs := newRoutedServer(t, "kb-a", "kb-b")
	cfg := bridgeConfig(rs)
	cfg.UnboundModes = map[string]string{"default": "write"}
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws, "kb-a")
	got := exchange(t, &Bridge{Cfg: cfg, Provider: "claude", Dir: ws, Token: testToken}, initMsg, initedMsg, listMsg)
	if s := Resolve(cfg, "claude", ws); s.ReadOnly || s.Unbound {
		t.Fatalf("bound scope = %+v", s)
	}
	if strings.Contains(instructionsOf(got["1"]), "has write access") {
		t.Error("a bound folder carries the unbound sentence")
	}
}

func TestWithUnboundNote(t *testing.T) {
	out := withUnboundNote([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"x","instructions":"server text"}}`))
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	in := instructionsOf(m)
	if !strings.HasPrefix(in, "server text") || !strings.Contains(in, "has write access") {
		t.Errorf("instructions = %q", in)
	}
	if res := m["result"].(map[string]any); res["protocolVersion"] != "x" {
		t.Error("other result fields were lost")
	}
	for _, untouched := range []string{
		`{"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"no"}}`,
		`not json`,
		`{"jsonrpc":"2.0","method":"notifications/x"}`,
	} {
		if got := string(withUnboundNote([]byte(untouched))); got != untouched {
			t.Errorf("%q was rewritten to %q", untouched, got)
		}
	}
}

// The handshake rewrite also covers an answer relayed as SSE events.
func TestBridgeUnboundWriteHandshakeOverSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2025-06-18\",\"instructions\":\"\"}}\n\n")
	}))
	defer srv.Close()
	cfg := &clientconfig.Config{
		ServerName: "cartographer", ServerURL: srv.URL + "/mcp", ServerRoutedPath: mcpserver.RoutedMountPath,
		Auth: true, Token: "${CARTOGRAPHER_TOKEN}", KnownKBs: []string{"kb-a"},
		UnboundModes: map[string]string{"default": "write"},
	}
	got := exchange(t, &Bridge{Cfg: cfg, Provider: "claude", Dir: t.TempDir(), Token: testToken}, initMsg)
	if in := instructionsOf(got["1"]); !strings.Contains(in, "has write access") {
		t.Errorf("SSE initialize instructions = %q", in)
	}
}

func TestScopeUnboundAccessMatrix(t *testing.T) {
	cfg := baseConfig()
	dir := mkdir(t, t.TempDir())
	if s := Resolve(cfg, "claude", dir); !s.ReadOnly || !s.Unbound || s.AccessOrigin != "default" {
		t.Errorf("default = %+v", s)
	}
	cfg.UnboundModes = map[string]string{"default": "write", "kiro": "read"}
	if s := Resolve(cfg, "claude", dir); s.ReadOnly || !s.Unbound || s.AccessOrigin != "unbound_access" || s.Query() != "" {
		t.Errorf("write = %+v query %q", s, s.Query())
	}
	if s := Resolve(cfg, "kiro", dir); !s.ReadOnly || s.AccessOrigin != "unbound_access" {
		t.Errorf("kiro override = %+v", s)
	}
	if got := Resolve(cfg, "claude", dir).Describe(); got != "every KB of the provider (provider default, unbound)" {
		t.Errorf("describe = %q", got)
	}
	cfg.UnboundModes = map[string]string{"default": "yes"}
	if s := Resolve(cfg, "claude", dir); !s.ReadOnly {
		t.Error("an invalid value opened write access")
	}
}
