package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// Protocol conformance after D168, which moved the wire format to the official
// SDK. This file replaces the D128-era suite, which pinned the hand-written
// era machinery byte for byte. Those assertions were the acceptance bar for
// code that no longer exists: re-asserting the SDK's framing here would
// recreate, as a test, exactly the maintenance burden the migration removes —
// every spec revision would land as a diff in this file rather than as a
// dependency bump.
//
// What stays is what Cartographer still owns: that both eras reach a mounted
// KB, that the advertised capabilities name only what is implemented, and that
// the transport shape the deployment documents (POST-only, JSON, no session)
// is the one actually served.

func newEraServer(t *testing.T) *Server {
	t.Helper()
	k := setupTestKB(t)
	s := New("1.2.3")
	RegisterKBTools(s, k, Deps{})
	return s
}

// resultMap decodes a response's result into a generic map.
func resultMap(t *testing.T, resp Response) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return m
}

// openHandler mounts the server behind an auth-disabled TokenStore, the way
// `serve` does when no tokens are configured: the middleware is what installs
// the local-admin principal, and without it every call is denied fail-closed.
func openHandler(s *Server) http.Handler {
	return auth.NewTokenStore(nil).Middleware(s.HTTPHandler())
}

// postMCP sends one JSON-RPC message over HTTP with the headers the Streamable
// HTTP transport requires, and returns the recorder.
func postMCP(t *testing.T, h http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestHandshakeEraStillReachesTheKB is the assertion that made the SDK worth
// adopting: a client on the 2024-11-05 handshake — which three of the four
// supported providers were still speaking when issue #118 was last assessed —
// initializes and calls a tool with no special handling on our side.
func TestHandshakeEraStillReachesTheKB(t *testing.T) {
	s := newEraServer(t)
	resps := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"legacy","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	})
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("legacy initialize: %v", resps[0].Error)
	}
	init := resultMap(t, resps[0])
	if init["protocolVersion"] != "2024-11-05" {
		t.Errorf("legacy initialize answered protocolVersion %v, want 2024-11-05", init["protocolVersion"])
	}
	list := resultMap(t, resps[1])
	tools, _ := list["tools"].([]interface{})
	if len(tools) == 0 {
		t.Error("legacy tools/list returned no tools")
	}
}

// TestNewEraReachesTheKB is the same journey on 2026-07-28, which carries its
// protocol version in _meta rather than in a handshake.
func TestNewEraReachesTheKB(t *testing.T) {
	s := newEraServer(t)
	h := openHandler(s)
	rr := postMCP(t, h,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"new","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`,
		map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/list"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rr.Body.String())
	}
	if resp.Error != nil {
		t.Fatalf("new-era tools/list: %v", resp.Error)
	}
	m := resultMap(t, resp)
	if _, ok := m["tools"]; !ok {
		t.Errorf("new-era tools/list has no tools field: %v", m)
	}
}

// TestCapabilitiesNameOnlyWhatIsBacked keeps D151's rule enforced across the
// migration: the SDK advertises {"logging":{}} by default "for historical
// reasons", and Cartographer has never served logging. An advertised
// capability nothing implements is how a client is led to call a method that
// does not exist.
func TestCapabilitiesNameOnlyWhatIsBacked(t *testing.T) {
	s := newEraServer(t)
	resps := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`,
	})
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	caps, _ := resultMap(t, resps[0])["capabilities"].(map[string]interface{})
	if caps == nil {
		t.Fatal("initialize returned no capabilities")
	}
	if _, ok := caps["tools"]; !ok {
		t.Error("tools capability missing, but tools are served")
	}
	for _, unbacked := range []string{"logging", "prompts", "resources", "completions"} {
		if _, ok := caps[unbacked]; ok {
			t.Errorf("advertised %q capability, which this server does not implement", unbacked)
		}
	}
}

// TestTransportShape pins the three transport properties the deployment docs
// promise: POST is the whole transport, the reply is JSON rather than an event
// stream, and no session id is issued.
func TestTransportShape(t *testing.T) {
	s := newEraServer(t)
	h := openHandler(s)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(method, "/mcp", nil)
		req.Header.Set("Accept", "application/json, text/event-stream")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /mcp = %d, want 405", method, rr.Code)
		}
	}

	rr := postMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, nil)
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if id := rr.Header().Get("Mcp-Session-Id"); id != "" {
		t.Errorf("stateless transport issued a session id %q", id)
	}
}
