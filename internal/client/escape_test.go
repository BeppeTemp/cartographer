package client_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

const evilText = "boom\x1b]0;t\x07\nforged"

func assertNoRawControl(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: want an error", what)
	}
	if strings.ContainsAny(err.Error(), "\x1b\x07\n") {
		t.Errorf("%s: raw control character in %q", what, err.Error())
	}
}

func TestRemoteErrorTextFromTheServerIsEscaped(t *testing.T) {
	respond := func(handler http.HandlerFunc) *client.MCPClient {
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		return client.New(srv.URL+"/mcp", "")
	}
	rpcError := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32000, "message": evilText}})
	}
	toolError := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
			"content": []map[string]string{{"type": "text", "text": evilText}}, "isError": true}})
	}
	status := func(code int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code); _, _ = w.Write([]byte(evilText)) }
	}

	_, err := respond(rpcError).Call("t", nil)
	assertNoRawControl(t, "JSON-RPC error", err)
	_, err = respond(toolError).Call("t", nil)
	assertNoRawControl(t, "tool error", err)
	_, err = respond(status(500)).Call("t", nil)
	assertNoRawControl(t, "HTTP body", err)
	_, err = respond(status(500)).Health(5 * time.Second)
	assertNoRawControl(t, "health body", err)
}
