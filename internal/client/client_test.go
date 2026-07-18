package client_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
