package mcpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postWithHost sends an MCP POST to a real loopback listener (the SDK's
// localhost guard reads the connection's local address, which a recorder does
// not have) with a Host header the way a same-host reverse proxy keeps it.
func postWithHost(t *testing.T, srvURL, path, host, token, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srvURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// A same-host reverse proxy (Caddy, nginx keeping Host, a sidecar) reaches a
// loopback listener with the public Host. With a token the request is
// authenticated: the SDK's own localhost guard must not answer 403 on top
// (OriginGuard and LoopbackHostGuard own the rule).
func TestReverseProxy_ForeignHostWithTokenIsServed(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(newRoutedTestHandler(t, "kb-one", "kb-two"))
	defer ts.Close()
	resp := postWithHost(t, ts.URL, RoutedMountPath, "kb.example.com", routedTestToken, initializeBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200 (the SDK localhost guard answered 403)", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(b), `"protocolVersion"`) {
		t.Fatalf("not a JSON-RPC initialize result: %v %s", err, b)
	}
}

// Regression pin: with auth off the loopback listener still refuses a foreign
// Host, now through LoopbackHostGuard.
func TestReverseProxy_AuthOffForeignHostStillRefused(t *testing.T) {
	t.Parallel()
	multi := newMultiKBTestHandler(t, "kb-one")
	ts := httptest.NewServer(LoopbackHostGuard(multi.Handler()))
	defer ts.Close()
	resp := postWithHost(t, ts.URL, "/mcp/kb-one", "kb.example.com", "", initializeBody)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d, want 403 from LoopbackHostGuard", resp.StatusCode)
	}
	resp = postWithHost(t, ts.URL, "/mcp/kb-one", "127.0.0.1:39273", "", initializeBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("loopback Host: status=%d, want 200", resp.StatusCode)
	}
}
