package client_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// rpcServer answers every request with the JSON-RPC envelope body the handler
// returns (status 200).
func rpcServer(t *testing.T, handler func(method string, params map[string]any) map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		body := handler(req.Method, req.Params)
		body["jsonrpc"] = "2.0"
		body["id"] = 1
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func status401(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
}

func TestListTools_TwoPagesSkipsNameless(t *testing.T) {
	srv := rpcServer(t, func(method string, params map[string]any) map[string]any {
		if method != "tools/list" {
			t.Errorf("method = %q", method)
		}
		if params["cursor"] == "p2" {
			return map[string]any{"result": map[string]any{"tools": []map[string]any{{"name": "c", "description": "C", "inputSchema": map[string]any{"type": "object"}}}}}
		}
		return map[string]any{"result": map[string]any{
			"tools":      []map[string]any{{"name": "a", "description": "A", "inputSchema": map[string]any{"type": "object", "x": 1}}, {"description": "no name"}},
			"nextCursor": "p2",
		}}
	})
	defer srv.Close()
	tools, err := client.New(srv.URL, "").ListTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "a" || tools[1].Name != "c" {
		t.Fatalf("tools = %+v", tools)
	}
	if string(tools[0].InputSchema) != `{"type":"object","x":1}` {
		t.Errorf("schema = %s", tools[0].InputSchema)
	}
}

func TestListTools_PageCap(t *testing.T) {
	srv := rpcServer(t, func(string, map[string]any) map[string]any {
		return map[string]any{"result": map[string]any{"tools": []any{}, "nextCursor": "again"}}
	})
	defer srv.Close()
	_, err := client.New(srv.URL, "").ListTools()
	if err == nil || !strings.Contains(err.Error(), "20 pages") {
		t.Fatalf("err = %v", err)
	}
}

func TestListTools_Unauthorized(t *testing.T) {
	srv := status401(t)
	defer srv.Close()
	_, err := client.New(srv.URL, "x").ListTools()
	var re *client.RemoteError
	if !errors.As(err, &re) || re.Code != client.CodeUnauthorized {
		t.Fatalf("err = %v", err)
	}
}

func TestCallTool(t *testing.T) {
	srv := rpcServer(t, func(_ string, params map[string]any) map[string]any {
		switch params["name"] {
		case "ok":
			return map[string]any{"result": map[string]any{"content": []map[string]any{{"type": "text", "text": "one"}, {"type": "text", "text": "two"}}}}
		case "bad":
			return map[string]any{"result": map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "stale write"}}}}
		case "empty":
			return map[string]any{"result": map[string]any{"content": []any{}}}
		default:
			return map[string]any{"error": map[string]any{"code": -32602, "message": "invalid params"}}
		}
	})
	defer srv.Close()
	c := client.New(srv.URL, "")

	out, err := c.CallTool("ok", map[string]any{})
	if err != nil || out.IsError || out.Text != "one\ntwo" {
		t.Fatalf("ok: %+v %v", out, err)
	}
	out, err = c.CallTool("bad", map[string]any{})
	if err != nil || !out.IsError || out.Text != "stale write" {
		t.Fatalf("bad: %+v %v", out, err)
	}
	out, err = c.CallTool("empty", map[string]any{})
	if err != nil || !out.IsError || out.Text != "tool returned no content" {
		t.Fatalf("empty: %+v %v", out, err)
	}
	_, err = c.CallTool("other", map[string]any{})
	var re *client.RemoteError
	if !errors.As(err, &re) || re.RPCCode != -32602 {
		t.Fatalf("rpc error: %v", err)
	}
}

func TestCallTool_Unauthorized(t *testing.T) {
	srv := status401(t)
	defer srv.Close()
	_, err := client.New(srv.URL, "x").CallTool("t", nil)
	var re *client.RemoteError
	if !errors.As(err, &re) || re.Code != client.CodeUnauthorized {
		t.Fatalf("err = %v", err)
	}
}
