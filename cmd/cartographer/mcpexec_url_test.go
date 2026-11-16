package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// A client URL of any form reaches the routed endpoint, with no ?kb= on it and
// the health probe at the server's own /health (#2149).
func TestMCPExecDepsAddressTheRoutedEndpointForEveryClientURLForm(t *testing.T) {
	for _, form := range []string{"/mcp", "/mcp?kb=kb-a", "/mcp/kb-a", "/mcp/routed"} {
		t.Run(form, func(t *testing.T) {
			var seen []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = append(seen, r.Method+" "+r.URL.RequestURI())
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/health":
					_, _ = w.Write([]byte(`{"status":"ok","version":"test"}`))
				case r.Method == http.MethodGet:
					// What the real server does with a health probe under /mcp/...
					w.WriteHeader(http.StatusUnauthorized)
				case r.URL.Path != "/mcp/routed":
					w.WriteHeader(http.StatusBadRequest)
				case r.URL.Query().Has("kb"):
					http.Error(w, "conflicting kb selection", http.StatusBadRequest)
				default:
					var req map[string]any
					_ = json.NewDecoder(r.Body).Decode(&req)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req["id"],
						"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}})
				}
			}))
			defer srv.Close()
			home := t.TempDir()
			setHome(t, home)
			cfg := clientconfig.Default()
			cfg.ServerURL = srv.URL + form
			cfg.ServerRoutedPath = "/mcp/routed"
			cfg.Auth = true
			cfg.Token = "tok-123"
			cfg.KnownKBs = []string{"kb-a"}
			if err := clientconfig.Save(home, cfg); err != nil {
				t.Fatal(err)
			}
			deps, err := productionMCPExecDeps()
			if err != nil {
				t.Fatal(err)
			}
			out, err := deps.Call(context.Background(), "artifact_read", map[string]any{"kb": "kb-a"})
			if err != nil || out != "ok" {
				t.Fatalf("Call = %q, %v; requests %s", out, err, strings.Join(seen, ", "))
			}
		})
	}
}
