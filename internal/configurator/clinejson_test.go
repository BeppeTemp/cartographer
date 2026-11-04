package configurator_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func clineEntry(t *testing.T, spec configurator.ServerSpec, name string) (map[string]any, *configurator.EmitResult) {
	t.Helper()
	r, err := configurator.EmitServer(name, spec, configurator.ProviderCline)
	if err != nil {
		t.Fatalf("EmitServer: %v", err)
	}
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(r.Content, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.MCPServers[name], r
}

func TestClineStdioBridgeSpec(t *testing.T) {
	e, r := clineEntry(t, configurator.ServerSpec{Type: "stdio", Command: "cartographer", Args: []string{"mcp", "--provider", "cline"}}, "cartographer")
	if r.FilePath != ".cline/data/settings/cline_mcp_settings.json" {
		t.Errorf("FilePath = %q", r.FilePath)
	}
	if e["type"] != "stdio" || e["command"] != "cartographer" {
		t.Errorf("entry = %v", e)
	}
	// cwd would pin one folder for a global entry; the rest is the user's policy.
	for _, k := range []string{"cwd", "disabled", "autoApprove", "timeout", "env"} {
		if _, has := e[k]; has {
			t.Errorf("%q must not be written: %v", k, e)
		}
	}
}

func TestClineHTTPIsStreamableHTTP(t *testing.T) {
	e, _ := clineEntry(t, configurator.ServerSpec{Type: "http", URL: "https://example.com/mcp", Headers: map[string]string{"Authorization": "Bearer x"}}, "kb-server")
	if e["type"] != "streamableHttp" || e["url"] != "https://example.com/mcp" {
		t.Errorf("entry = %v", e)
	}
	if h, _ := e["headers"].(map[string]any); h["Authorization"] != "Bearer x" {
		t.Errorf("headers = %v", e["headers"])
	}
}

func TestClineUnknownTransportIsAnError(t *testing.T) {
	_, err := configurator.EmitServer("x", configurator.ServerSpec{Type: "websocket"}, configurator.ProviderCline)
	if err == nil || !strings.Contains(err.Error(), "unsupported transport") {
		t.Fatalf("err = %v", err)
	}
}

// The file is Cline's own: it recreates it and keeps a .bak beside it, so it is
// never deleted, and ${VAR} expansion in headers is unverified.
func TestClineDescriptorKeepsItsFile(t *testing.T) {
	d, ok := configurator.Lookup(configurator.ProviderCline)
	if !ok {
		t.Fatal("no descriptor")
	}
	if d.DeletableWhenEmpty || d.SupportsMCPHeaders || d.FlatToolNamespace {
		t.Errorf("descriptor = %+v", d)
	}
}
