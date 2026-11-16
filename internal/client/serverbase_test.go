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

func TestServerBaseAndAtlasURL(t *testing.T) {
	for _, c := range []struct{ in, base, atlas string }{
		{"https://example.com/mcp", "https://example.com", "https://example.com/ui/"},
		{"https://example.com/mcp/", "https://example.com", "https://example.com/ui/"},
		{"https://example.com", "https://example.com", "https://example.com/ui/"},
		{"https://example.com:8443/prefix/mcp?x=1#f", "https://example.com:8443/prefix", "https://example.com:8443/prefix/ui/"},
		{"https://example.com/mcp/routed", "https://example.com", "https://example.com/ui/"},
		{"https://example.com/prefix/mcp/routed/?kbs=a,b", "https://example.com/prefix", "https://example.com/prefix/ui/"},
		{"https://example.com/mcp/kb-a", "https://example.com", "https://example.com/ui/"},
		{"https://example.com/prefix/mcp/kb-a/", "https://example.com/prefix", "https://example.com/prefix/ui/"},
		{"https://example.com/mcp?kb=kb-a", "https://example.com", "https://example.com/ui/"},
		{"http://127.0.0.1:39273/mcp", "http://127.0.0.1:39273", "http://127.0.0.1:39273/ui/"},
		{"https://u:p@example.com/mcp", "https://u:p@example.com", "https://example.com/ui/"},
	} {
		u, err := client.ServerBase(c.in)
		if err != nil || u.String() != c.base {
			t.Errorf("ServerBase(%q) = %v, %v; want %q", c.in, u, err, c.base)
		}
		if got, err := client.AtlasURL(c.in); err != nil || got != c.atlas {
			t.Errorf("AtlasURL(%q) = %q, %v; want %q", c.in, got, err, c.atlas)
		}
	}
	if _, err := client.ServerBase("http://[::1"); err == nil {
		t.Error("an unparsable URL gave no error")
	}
}

// A routed client adds the KB as a tool argument and hides it from the schemas.
func TestWithRoutedKBAddsTheArgumentAndHidesItFromTheSchema(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "tools/list":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"t","inputSchema":{"type":"object","properties":{"kb":{"type":"string"},"q":{"type":"string"}},"required":["kb","q"]}}]}}`))
		default:
			got, _ = req.Params["arguments"].(map[string]any)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`))
		}
	}))
	defer srv.Close()
	c := client.New(srv.URL+"/mcp/routed", "").WithInsecureHTTP(true).WithRoutedKB("kb-a")
	tools, err := c.ListTools()
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools %v, err %v", tools, err)
	}
	if s := string(tools[0].InputSchema); strings.Contains(s, `"kb"`) || !strings.Contains(s, `"q"`) {
		t.Fatalf("schema = %s", s)
	}
	if _, err := c.CallTool("t", map[string]any{"q": "x"}); err != nil {
		t.Fatal(err)
	}
	if got["kb"] != "kb-a" || got["q"] != "x" {
		t.Fatalf("arguments = %v", got)
	}
	// A model's call arrives as raw JSON (surveyor loop): the kb is added there too.
	got = nil
	if _, err := c.CallTool("t", json.RawMessage(`{"q":"y"}`)); err != nil {
		t.Fatal(err)
	}
	if got["kb"] != "kb-a" || got["q"] != "y" {
		t.Fatalf("raw arguments = %v", got)
	}
	if _, err := c.CallTool("t", json.RawMessage(`[1]`)); err == nil {
		t.Fatal("non-object arguments were accepted")
	}
}

// Every client URL form asks the server's own /health, whatever KB it names
// (#2149): /mcp/kb-a/health is not a route, and its 401 blamed the token.
func TestHealthIsAskedAtTheServerRootForEveryClientURLForm(t *testing.T) {
	for _, form := range []string{"/mcp", "/mcp?kb=kb-a", "/mcp/kb-a", "/mcp/routed"} {
		t.Run(form, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/health" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				_, _ = w.Write([]byte(`{"status":"ok","version":"test"}`))
			}))
			defer srv.Close()
			h, err := client.New(srv.URL+form, "tok").WithInsecureHTTP(true).Health(time.Second)
			if err != nil || h == nil || !h.IsServer() {
				t.Fatalf("Health = %v, %v", h, err)
			}
		})
	}
}

func TestRoutedEndpointDropsTheKBSelectorOfEveryForm(t *testing.T) {
	for _, c := range []struct{ in, routed, want string }{
		{"https://example.com/mcp", "/mcp/routed", "https://example.com/mcp/routed"},
		{"https://example.com/mcp?kb=kb-a", "/mcp/routed", "https://example.com/mcp/routed"},
		{"https://example.com/mcp/kb-a", "/mcp/routed", "https://example.com/mcp/routed"},
		{"https://example.com/mcp/routed", "/mcp/routed", "https://example.com/mcp/routed"},
		{"https://example.com/p/mcp/kb-a", "/mcp/routed", "https://example.com/p/mcp/routed"},
		{"https://example.com/p/mcp", "/mcp/routed", "https://example.com/p/mcp/routed"},
	} {
		if got, err := client.RoutedEndpoint(c.in, c.routed); err != nil || got != c.want {
			t.Errorf("RoutedEndpoint(%q, %q) = %q, %v; want %q", c.in, c.routed, got, err, c.want)
		}
	}
}
