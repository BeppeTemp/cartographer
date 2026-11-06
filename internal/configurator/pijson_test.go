package configurator_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func piEntry(t *testing.T, spec configurator.ServerSpec, name string) (map[string]any, *configurator.EmitResult) {
	t.Helper()
	r, err := configurator.EmitServer(name, spec, configurator.ProviderPi)
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

func TestPiThirdPartyServerPinsDirectExposure(t *testing.T) {
	e, r := piEntry(t, configurator.ServerSpec{Type: "http", URL: "https://example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${CARTOGRAPHER_TOKENS}"}}, "kb-server")
	if r.FilePath != ".pi/agent/mcp.json" {
		t.Errorf("FilePath = %q", r.FilePath)
	}
	// A KB-distributed server keeps the entry-wide pin: the default exposure
	// ("codemode") would not declare its tools to the model.
	if e["exposure"] != "direct" {
		t.Errorf("exposure = %v, want direct", e["exposure"])
	}
	if _, has := e["toolExposure"]; has {
		t.Error("a third-party server carries no toolExposure: its tool names are not ours")
	}
	h := e["headers"].(map[string]any)
	if h["Authorization"] != "Bearer ${CARTOGRAPHER_TOKENS}" {
		t.Errorf("Authorization = %v, want verbatim", h["Authorization"])
	}
	if _, has := e["type"]; has {
		t.Error("no type key expected")
	}
}

func TestPiStdioBridgeSpec(t *testing.T) {
	e, _ := piEntry(t, configurator.ServerSpec{Type: "stdio", Command: "cartographer", Args: []string{"mcp", "--provider", "pi"}, EntryTools: true}, "cartographer")
	if e["command"] != "cartographer" {
		t.Errorf("entry = %v", e)
	}
	assertPiEntryToolsOnly(t, e)
	args := e["args"].([]any)
	if len(args) != 3 || args[2] != "pi" {
		t.Errorf("args = %v", args)
	}
	if _, has := e["env"]; has {
		t.Error("empty env must be omitted")
	}
}

func TestPiRefusesWholeValueCommand(t *testing.T) {
	_, err := configurator.EmitServer("kb-server", configurator.ServerSpec{Type: "http", URL: "https://example.com/mcp",
		Headers: map[string]string{"X-Key": "!cat secret"}}, configurator.ProviderPi)
	if err == nil || !strings.Contains(err.Error(), "kb-server") || !strings.Contains(err.Error(), "X-Key") {
		t.Errorf("err = %v, want server and key named", err)
	}
	_, err = configurator.EmitServer("local", configurator.ServerSpec{Type: "stdio", Command: "x",
		Env: map[string]string{"TOKEN": "!pass show t"}}, configurator.ProviderPi)
	if err == nil || !strings.Contains(err.Error(), "TOKEN") {
		t.Errorf("env err = %v", err)
	}
	if _, err := configurator.EmitServer("kb-server", configurator.ServerSpec{Type: "http", URL: "https://example.com/mcp",
		Headers: map[string]string{"X-Key": "a!b"}}, configurator.ProviderPi); err != nil {
		t.Errorf("'!' in the middle must be accepted: %v", err)
	}
}

func TestPiRefusesInvalidServerName(t *testing.T) {
	_, err := configurator.EmitServer("my.server", configurator.ServerSpec{Type: "http", URL: "https://example.com/mcp"}, configurator.ProviderPi)
	if err == nil || !strings.Contains(err.Error(), "my.server") {
		t.Errorf("err = %v", err)
	}
}

// assertPiEntryToolsOnly pins Cartographer's own Pi entry: the entry tools
// direct through toolExposure, and no entry-wide exposure, so every other tool
// stays in Pi's codemode (D-new-entry-tools).
func assertPiEntryToolsOnly(t *testing.T, e map[string]any) {
	t.Helper()
	if v, has := e["exposure"]; has {
		t.Errorf("exposure = %v: the entry must keep Pi's default for the other tools", v)
	}
	te, _ := e["toolExposure"].(map[string]any)
	want := configurator.EntryTools()
	if len(te) != len(want) {
		t.Errorf("toolExposure = %v, want exactly %v", te, want)
	}
	for _, name := range want {
		if te[name] != "direct" {
			t.Errorf("toolExposure[%q] = %v, want direct", name, te[name])
		}
	}
}

func TestPiOwnHTTPEntryPinsTheEntryToolsOnly(t *testing.T) {
	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "https://example.com/mcp"}
	r, err := configurator.Emit(cfg, configurator.ProviderPi)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(r.Content, &doc); err != nil {
		t.Fatal(err)
	}
	assertPiEntryToolsOnly(t, doc.MCPServers["cartographer"])
}
