package client_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// fakeMCPServer mimics the minimal JSON-RPC surface used by MCPClient.Call:
// tools/call → {"result": {"content": [{"type":"text","text":"<json>"}]}}.
func fakeMCPServer(t *testing.T, wantToken string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantToken != "" {
			if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
				t.Errorf("Authorization header = %q, want Bearer %s", got, wantToken)
			}
		}

		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Method != "tools/call" {
			t.Fatalf("method = %q, want tools/call", req.Method)
		}

		kb := r.URL.Query().Get("kb")

		var payload string
		switch req.Params.Name {
		case "ok_tool":
			payload = `{"answer":42,"kb":"` + kb + `"}`
		case "err_tool":
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "boom"}},
					"isError": true,
				},
			}
			json.NewEncoder(w).Encode(resp)
			return
		case "multi_tool":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"content": []map[string]any{{"type": "text", "text": "first"}, {"type": "text", "text": "second"}}}})
			return
		default:
			http.Error(w, "unknown tool", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": payload}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestCall_PreservesMultipleTextBlocks(t *testing.T) {
	srv := fakeMCPServer(t, "")
	defer srv.Close()
	raw, err := client.New(srv.URL, "").Call("multi_tool", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(raw, &got); err != nil || len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("blocks = %s, %v", raw, err)
	}
}

func TestCall_Success(t *testing.T) {
	srv := fakeMCPServer(t, "")
	defer srv.Close()

	c := client.New(srv.URL, "")
	raw, err := c.Call("ok_tool", map[string]any{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var result struct {
		Answer int `json:"answer"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.Answer != 42 {
		t.Errorf("Answer = %d, want 42", result.Answer)
	}
}

func TestCall_BearerToken(t *testing.T) {
	srv := fakeMCPServer(t, "secret-token")
	defer srv.Close()

	c := client.New(srv.URL, "secret-token")
	if _, err := c.Call("ok_tool", map[string]any{}); err != nil {
		t.Fatalf("Call: %v", err)
	}
}

func TestCall_ToolError(t *testing.T) {
	srv := fakeMCPServer(t, "")
	defer srv.Close()

	c := client.New(srv.URL, "")
	if _, err := c.Call("err_tool", map[string]any{}); err == nil {
		t.Fatal("expected error from err_tool, got nil")
	}
}

// TestCall_ToolError_ClassifiesAsRemoteFailed is the exact D120 regression
// scenario: the server is reachable (it returns a valid JSON-RPC response)
// but the requested tool doesn't exist there (e.g. a stale/unqualified tool
// name) — this must classify as RemoteFailed/mcp_failed, never as
// RemoteUnavailable (which would render as "server unreachable").
func TestCall_ToolError_ClassifiesAsRemoteFailed(t *testing.T) {
	srv := fakeMCPServer(t, "")
	defer srv.Close()

	c := client.New(srv.URL, "")
	_, err := c.Call("err_tool", map[string]any{})
	var re *client.RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want a *client.RemoteError", err)
	}
	if re.State != client.RemoteFailed {
		t.Errorf("State = %q, want %q", re.State, client.RemoteFailed)
	}
	if re.Code != client.CodeMCPFailed {
		t.Errorf("Code = %q, want %q", re.Code, client.CodeMCPFailed)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to preserve the tool's error text", err)
	}
}

func TestCall_WithKB(t *testing.T) {
	srv := fakeMCPServer(t, "")
	defer srv.Close()

	c := client.New(srv.URL, "").WithKB("homelab")
	raw, err := c.Call("ok_tool", map[string]any{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var result struct {
		KB string `json:"kb"`
	}
	json.Unmarshal(raw, &result)
	if result.KB != "homelab" {
		t.Errorf("kb query param = %q, want homelab", result.KB)
	}
}

func TestCall_Unreachable(t *testing.T) {
	c := client.New("http://127.0.0.1:1", "")
	if _, err := c.Call("ok_tool", map[string]any{}); err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}

// TestCall_Unreachable_ClassifiesAsRemoteUnavailable is the dial-time
// counterpart of TestCall_ToolError_ClassifiesAsRemoteFailed: a connection
// that never reaches an HTTP server classifies as RemoteUnavailable, code
// unreachable (not dns_failed: 127.0.0.1 resolves fine, the connection is
// refused).
func TestCall_Unreachable_ClassifiesAsRemoteUnavailable(t *testing.T) {
	c := client.New("http://127.0.0.1:1", "")
	_, err := c.Call("ok_tool", map[string]any{})
	var re *client.RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want a *client.RemoteError", err)
	}
	if re.State != client.RemoteUnavailable {
		t.Errorf("State = %q, want %q", re.State, client.RemoteUnavailable)
	}
	if re.Code != client.CodeUnreachable {
		t.Errorf("Code = %q, want %q", re.Code, client.CodeUnreachable)
	}
}

// TestCall_Unauthorized_ClassifiesAsRemoteUnavailable confirms 401 responses
// keep classifying as RemoteUnavailable/unauthorized, and that
// errors.Is(err, client.ErrUnauthorized) still works through the
// RemoteError wrapper (pre-D120 callers rely on this).
func TestCall_Unauthorized_ClassifiesAsRemoteUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := client.New(srv.URL, "wrong-token").Call("ok_tool", map[string]any{})
	var re *client.RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want a *client.RemoteError", err)
	}
	if re.State != client.RemoteUnavailable || re.Code != client.CodeUnauthorized {
		t.Errorf("State/Code = %q/%q, want %q/%q", re.State, re.Code, client.RemoteUnavailable, client.CodeUnauthorized)
	}
	if !errors.Is(err, client.ErrUnauthorized) {
		t.Fatalf("expected errors.Is(err, client.ErrUnauthorized) through the RemoteError wrapper, got %v", err)
	}
}

// TestCall_MalformedJSONRPCResponse_ClassifiesAsRemoteFailed: a reachable
// server (HTTP 200) that returns a body that isn't valid JSON-RPC classifies
// as RemoteFailed/mcp_failed, alongside the tool-level isError:true case.
func TestCall_MalformedJSONRPCResponse_ClassifiesAsRemoteFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := client.New(srv.URL, "").Call("ok_tool", map[string]any{})
	var re *client.RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want a *client.RemoteError", err)
	}
	if re.State != client.RemoteFailed || re.Code != client.CodeMCPFailed {
		t.Errorf("State/Code = %q/%q, want %q/%q", re.State, re.Code, client.RemoteFailed, client.CodeMCPFailed)
	}
}

// TestCall_HTTPStatusFailure_ClassifiesAsRemoteUnavailable: a non-401 HTTP
// failure status (e.g. 500) classifies as RemoteUnavailable/http_failed.
func TestCall_HTTPStatusFailure_ClassifiesAsRemoteUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := client.New(srv.URL, "").Call("ok_tool", map[string]any{})
	var re *client.RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want a *client.RemoteError", err)
	}
	if re.State != client.RemoteUnavailable || re.Code != client.CodeHTTPFailed {
		t.Errorf("State/Code = %q/%q, want %q/%q", re.State, re.Code, client.RemoteUnavailable, client.CodeHTTPFailed)
	}
}

func TestPing_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Method != "ping" {
			t.Errorf("method = %q, want ping", req.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
	}))
	defer srv.Close()

	if err := client.New(srv.URL, "").Ping(2 * time.Second); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPing_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="cartographer"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	err := client.New(srv.URL, "wrong-token").Ping(2 * time.Second)
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if !errors.Is(err, client.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestPing_Timeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // hang until the client times out
	}))
	defer func() {
		close(block)
		srv.Close()
	}()

	start := time.Now()
	err := client.New(srv.URL, "").Ping(200 * time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if errors.Is(err, client.ErrUnauthorized) {
		t.Fatalf("timeout must not be classified as unauthorized: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Ping ignored its dedicated timeout: took %v", elapsed)
	}
}

func TestPing_Unreachable(t *testing.T) {
	err := client.New("http://127.0.0.1:1", "").Ping(1 * time.Second)
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
	if errors.Is(err, client.ErrUnauthorized) {
		t.Fatalf("network error must not be classified as unauthorized: %v", err)
	}
}

func TestPing_DoesNotMutateClientTimeout(t *testing.T) {
	c := client.New("http://127.0.0.1:1", "")
	before := c.HTTP.Timeout
	_ = c.Ping(100 * time.Millisecond)
	if c.HTTP.Timeout != before {
		t.Fatalf("Ping mutated the shared HTTP client timeout: %v -> %v", before, c.HTTP.Timeout)
	}
}

func TestHealth_StripsMCPPathAndParsesVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/health" {
			t.Errorf("path = %q, want /health", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","version":"v1.2.3","kbs":[]}`))
	}))
	defer srv.Close()

	health, err := client.New(srv.URL+"/mcp", "").Health(time.Second)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.Status != "ok" || health.Version != "v1.2.3" || health.Ready != nil || health.KBs == nil || len(*health.KBs) != 0 {
		t.Errorf("Health = %+v, want status=ok version=v1.2.3 ready=nil kbs=empty-present", health)
	}
}

// TestHealth_DecodesToolPrefix is D120: the client must decode each KB's
// advertised tool_prefix, and keep tolerating the older shapes (string-only
// KB list, or no tool_prefix field at all) as an empty (unprefixed) value.
func TestHealth_DecodesToolPrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","kbs":[{"name":"alpha"},{"name":"beta","tool_prefix":"custom_name"}]}`))
	}))
	defer srv.Close()

	health, err := client.New(srv.URL, "").Health(time.Second)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.KBs == nil || len(*health.KBs) != 2 {
		t.Fatalf("KBs = %+v, want 2 entries", health.KBs)
	}
	kbs := *health.KBs
	if kbs[0].Name != "alpha" || kbs[0].ToolPrefix != "" {
		t.Errorf("kbs[0] = %+v, want Name=alpha ToolPrefix=\"\"", kbs[0])
	}
	if kbs[1].Name != "beta" || kbs[1].ToolPrefix != "custom_name" {
		t.Errorf("kbs[1] = %+v, want Name=beta ToolPrefix=custom_name", kbs[1])
	}
}

// TestHealth_ToolPrefixAbsentOnLegacyShapes confirms both older shapes an
// existing client already tolerates — the bare string-array KB list, and an
// object KB entry that predates tool_prefix entirely — decode to an empty
// (unprefixed) ToolPrefix rather than failing.
func TestHealth_ToolPrefixAbsentOnLegacyShapes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","kbs":["alpha","beta"]}`))
	}))
	defer srv.Close()

	health, err := client.New(srv.URL, "").Health(time.Second)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.KBs == nil || len(*health.KBs) != 2 {
		t.Fatalf("KBs = %+v, want 2 entries", health.KBs)
	}
	for _, kb := range *health.KBs {
		if kb.ToolPrefix != "" {
			t.Errorf("kb %+v: ToolPrefix = %q, want empty for the legacy string-array shape", kb, kb.ToolPrefix)
		}
	}
}

func TestHealth_PreservesAbsentVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	health, err := client.New(srv.URL, "").Health(time.Second)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.Version != "" {
		t.Errorf("Health.Version = %q, want empty for an older server", health.Version)
	}
}
