package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// A plugin command reaches the routed endpoint from a client URL of any form,
// with no ?kb= on it (#2149).
func TestCLIKBAddressesTheRoutedEndpointForEveryClientURLForm(t *testing.T) {
	for _, form := range []string{"/mcp", "/mcp?kb=kb-a", "/mcp/kb-a", "/mcp/routed"} {
		t.Run(form, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/health":
					_, _ = w.Write([]byte(`{"status":"ok","version":"test"}`))
				case r.URL.Path != "/mcp/routed" || r.URL.Query().Has("kb"):
					w.WriteHeader(http.StatusBadRequest)
				default:
					var req map[string]any
					_ = json.NewDecoder(r.Body).Decode(&req)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req["id"],
						"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}})
				}
			}))
			defer srv.Close()
			cfg := clientconfig.Default()
			cfg.ServerURL = srv.URL + form
			cfg.ServerRoutedPath = "/mcp/routed"
			cfg.KnownKBs = []string{"kb-a"}
			out, err := cliKB(cfg).Call(context.Background(), "concept_read", map[string]any{"kb": "kb-a"})
			if err != nil || out != "ok" {
				t.Fatalf("Call = %q, %v", out, err)
			}
		})
	}
}
