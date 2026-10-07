package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/peers"
)

// peerTestHandler mounts kb-one and kb-two behind the routed mount with the
// peer hub on. Tokens: "alice" and "bob" write both KBs, "reader" reads
// kb-one only, "other" has kb-two only.
func peerTestHandler(t *testing.T) http.Handler {
	t.Helper()
	hub := peers.New(0)
	multi := NewMultiKBServer("test")
	for _, name := range []string{"kb-one", "kb-two"} {
		k := setupNamedTestKB(t, name)
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{Peers: hub}) })
	}
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatal(err)
	}
	multi.EnablePeers(hub)
	both := []auth.KBScope{{KB: "kb-one", Write: true}, {KB: "kb-two", Write: true}}
	return auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "alice", Scopes: both},
		{Token: "bob", Scopes: both},
		{Token: "reader", Scopes: []auth.KBScope{{KB: "kb-one"}}},
		{Token: "other", Scopes: []auth.KBScope{{KB: "kb-two", Write: true}}},
	}).Middleware(multi.Handler())
}

func peerPost(t *testing.T, h http.Handler, token, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, PeerAPIPrefix+path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func peerToolCall(t *testing.T, h http.Handler, token, tool string, args map[string]any) ToolResult {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	req := httptest.NewRequest(http.MethodPost, RoutedMountPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s: status=%d body=%s", tool, rr.Code, rr.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s: decode: %v", tool, err)
	}
	if resp.Error != nil {
		t.Fatalf("%s: rpc error: %s", tool, resp.Error.Message)
	}
	return decodeToolResult(t, resp)
}

func registerPeer(t *testing.T, h http.Handler, token, id, provider string, kbs ...string) {
	t.Helper()
	rr := peerPost(t, h, token, "/register", map[string]any{"id": id, "provider": provider, "kbs": kbs})
	if rr.Code != http.StatusOK {
		t.Fatalf("register %s: %d %s", id, rr.Code, rr.Body.String())
	}
}

func TestPeers_SendThroughToolTakeThroughAPI(t *testing.T) {
	h := peerTestHandler(t)
	registerPeer(t, h, "alice", "codex-1", "codex", "kb-one")
	registerPeer(t, h, "bob", "kiro-1", "kiro", "kb-one", "kb-two")

	list := peerToolCall(t, h, "alice", "peer_list", map[string]any{"kb": "kb-one"})
	if list.IsError || !strings.Contains(list.Content[0].Text, `"kiro-1"`) || !strings.Contains(list.Content[0].Text, `"codex-1"`) {
		t.Fatalf("peer_list: %+v", list)
	}
	sent := peerToolCall(t, h, "alice", "peer_send", map[string]any{"kb": "kb-one", "from": "codex-1", "to": "kiro-1", "text": "are you editing runbook/deploy?"})
	if sent.IsError {
		t.Fatalf("peer_send: %s", sent.Content[0].Text)
	}

	rr := peerPost(t, h, "bob", "/take", map[string]any{"ids": []string{"kiro-1"}, "touch": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("take: %d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Messages map[string][]peers.Message `json:"messages"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	m := got.Messages["kiro-1"]
	if len(m) != 1 || m[0].From != "codex-1" || m[0].KB != "kb-one" || m[0].Text != "are you editing runbook/deploy?" {
		t.Fatalf("take: %+v", got)
	}
}

func TestPeers_WaitToolReturnsPendingMail(t *testing.T) {
	h := peerTestHandler(t)
	registerPeer(t, h, "alice", "codex-1", "codex", "kb-one")
	registerPeer(t, h, "bob", "kiro-1", "kiro", "kb-one")
	peerToolCall(t, h, "bob", "peer_send", map[string]any{"kb": "kb-one", "from": "kiro-1", "to": "codex-1", "text": "ping"})
	res := peerToolCall(t, h, "alice", "peer_wait", map[string]any{"kb": "kb-one", "session": "codex-1", "timeout_seconds": 1})
	if res.IsError || !strings.Contains(res.Content[0].Text, `"ping"`) {
		t.Fatalf("peer_wait: %+v", res)
	}
	empty := peerToolCall(t, h, "alice", "peer_wait", map[string]any{"kb": "kb-one", "session": "codex-1", "timeout_seconds": 1})
	if empty.IsError || !strings.Contains(empty.Content[0].Text, `"messages":[]`) {
		t.Fatalf("second peer_wait: %+v", empty)
	}
}

func TestPeers_TokensStayInTheirLane(t *testing.T) {
	h := peerTestHandler(t)
	registerPeer(t, h, "alice", "codex-1", "codex", "kb-one")
	registerPeer(t, h, "bob", "kiro-1", "kiro", "kb-one")

	// Joining a KB the token cannot read is refused like an unknown KB.
	if rr := peerPost(t, h, "other", "/register", map[string]any{"id": "x", "provider": "codex", "kbs": []string{"kb-one"}}); rr.Code != http.StatusNotFound {
		t.Fatalf("register on an unreadable KB: %d", rr.Code)
	}
	// Another token can neither read a session's mail nor speak as it.
	if rr := peerPost(t, h, "bob", "/take", map[string]any{"ids": []string{"codex-1"}}); rr.Code != http.StatusForbidden {
		t.Fatalf("take someone else's mail: %d", rr.Code)
	}
	spoof := peerToolCall(t, h, "bob", "peer_send", map[string]any{"kb": "kb-one", "from": "codex-1", "to": "kiro-1", "text": "x"})
	if !spoof.IsError {
		t.Fatal("peer_send as another token's session succeeded")
	}
	// A read-only token may list and wait, not send.
	registerPeer(t, h, "reader", "claude-1", "claude", "kb-one")
	if res := peerToolCall(t, h, "reader", "peer_list", map[string]any{"kb": "kb-one"}); res.IsError {
		t.Fatalf("peer_list with a read token: %+v", res)
	}
	if res := peerToolCall(t, h, "reader", "peer_send", map[string]any{"kb": "kb-one", "from": "claude-1", "to": "kiro-1", "text": "x"}); !res.IsError {
		t.Fatal("peer_send with a read-only token succeeded")
	}
}

func TestPeers_APIAbsentWhenDisabled(t *testing.T) {
	multi := NewMultiKBServer("test")
	k := setupNamedTestKB(t, "kb-one")
	multi.MountKB("kb-one", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	h := auth.NewTokenStore(nil).Middleware(multi.Handler())
	if rr := peerPost(t, h, "", "/register", map[string]any{}); rr.Code != http.StatusNotFound {
		t.Fatalf("peer API with the hub off: %d", rr.Code)
	}
	if _, ok := multi.servers["kb-one"].Tools()["peer_send"]; ok {
		t.Fatal("peer_send registered with the hub off")
	}
}
