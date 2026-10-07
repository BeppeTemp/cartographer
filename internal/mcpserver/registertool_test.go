package mcpserver

import (
	"encoding/json"
	"testing"
)

// TestRegisterTool_BareName verifies a tool is registered under its own name:
// no prefix is ever applied (D325).
func TestRegisterTool_BareName(t *testing.T) {
	s := New("test")
	s.RegisterTool(Tool{Name: "atlas_overview"})
	if _, ok := s.Tools()["atlas_overview"]; !ok {
		t.Fatalf("expected unprefixed tool name %q to be registered; got %v", "atlas_overview", s.Tools())
	}
}

// TestServer_DisplayName_ServerInfo verifies SetDisplayName overrides
// serverInfo.name, and that leaving it unset keeps the historical
// "cartographer" (single-KB path, asserted verbatim elsewhere too).
func TestServer_DisplayName_ServerInfo(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	s.SetDisplayName("cartographer:eng-team")
	RegisterKBTools(s, k, Deps{})

	initMsg := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	resps := runMCPSequence(t, s, []string{initMsg})
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	resultBytes, _ := json.Marshal(resps[0].Result)
	var result map[string]interface{}
	json.Unmarshal(resultBytes, &result)
	info, ok := result["serverInfo"].(map[string]interface{})
	if !ok || info["name"] != "cartographer:eng-team" {
		t.Errorf("initialize: unexpected serverInfo: %v", result["serverInfo"])
	}
}
