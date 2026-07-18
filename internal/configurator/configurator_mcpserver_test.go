package configurator_test

// Golden tests per EmitServer (D69): l'emissione provider-neutra riusata sia da
// Emit (server Cartographer stesso) sia da internal/provisioning per i server
// MCP di terze parti distribuiti dalle KB. A differenza dei test in
// configurator_test.go/configurator_codex_test.go (che passano per Emit/
// ServerConfig), questi chiamano EmitServer direttamente con un nome/spec
// arbitrario, come farebbe internal/provisioning/mcpsettings.go.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func TestEmitServer_ClaudeCode(t *testing.T) {
	spec := configurator.ServerSpec{
		Type:    "http",
		URL:     "https://kb-server.example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${KB_TOKEN}"},
	}
	r, err := configurator.EmitServer("kb-server", spec, configurator.ProviderClaudeCode)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.FilePath != ".claude.json" {
		t.Errorf("FilePath = %q, want .claude.json", r.FilePath)
	}
	var root map[string]any
	if err := json.Unmarshal(r.Content, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	entry := root["mcpServers"].(map[string]any)["kb-server"].(map[string]any)
	if entry["url"] != spec.URL || entry["type"] != "http" {
		t.Errorf("unexpected entry: %+v", entry)
	}
	headers := entry["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer ${KB_TOKEN}" {
		t.Errorf("Authorization header = %v, want verbatim ${VAR} (claude native syntax)", headers["Authorization"])
	}
	if len(r.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", r.Warnings)
	}
}

func TestEmitServer_Kiro_IgnoresHeaders(t *testing.T) {
	spec := configurator.ServerSpec{
		Type:    "http",
		URL:     "https://kb-server.example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${KB_TOKEN}"},
	}
	r, err := configurator.EmitServer("kb-server", spec, configurator.ProviderKiro)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(r.Content, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	entry := root["mcpServers"].(map[string]any)["kb-server"].(map[string]any)
	if _, ok := entry["headers"]; ok {
		t.Error("kiro should not receive a headers field (pre-existing gap, unrelated to D69)")
	}
	if entry["url"] != spec.URL {
		t.Errorf("url = %v, want %v", entry["url"], spec.URL)
	}
}

func TestEmitServer_OpenCode_TranslatesEnvSyntax(t *testing.T) {
	spec := configurator.ServerSpec{
		Type:    "http",
		URL:     "https://kb-server.example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${KB_TOKEN}"},
	}
	r, err := configurator.EmitServer("kb-server", spec, configurator.ProviderOpenCode)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(r.Content, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	entry := root["mcp"].(map[string]any)["kb-server"].(map[string]any)
	headers := entry["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer {env:KB_TOKEN}" {
		t.Errorf("Authorization header = %v, want Bearer {env:KB_TOKEN} (opencode native syntax)", headers["Authorization"])
	}
}

func TestEmitServer_Codex_BearerAuth(t *testing.T) {
	spec := configurator.ServerSpec{
		Type:    "http",
		URL:     "https://kb-server.example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${KB_TOKEN}"},
	}
	r, err := configurator.EmitServer("kb-server", spec, configurator.ProviderCodex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.FilePath != filepath.Join(".codex", "config.toml") {
		t.Errorf("FilePath = %q, want .codex/config.toml", r.FilePath)
	}
	content := string(r.Content)
	if !strings.Contains(content, "[mcp_servers.kb-server]") {
		t.Errorf("missing section header: %s", content)
	}
	if !strings.Contains(content, `bearer_token_env_var = "KB_TOKEN"`) {
		t.Errorf("missing bearer_token_env_var: %s", content)
	}
	if len(r.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", r.Warnings)
	}
}

func TestEmitServer_Codex_UnsupportedHeaderWarns(t *testing.T) {
	spec := configurator.ServerSpec{
		Type: "http",
		URL:  "https://kb-server.example.com/mcp",
		Headers: map[string]string{
			"Authorization": "Bearer ${KB_TOKEN}",
			"X-Custom":      "${KB_CUSTOM}",
		},
	}
	r, err := configurator.EmitServer("kb-server", spec, configurator.ProviderCodex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(r.Content), "X-Custom") {
		t.Errorf("codex TOML should not contain the unsupported header: %s", r.Content)
	}
	if len(r.Warnings) == 0 {
		t.Error("expected a warning about the unrepresentable header")
	}
}

func TestEmitServer_Codex_NonBearerAuthorizationWarns(t *testing.T) {
	spec := configurator.ServerSpec{
		Type:    "http",
		URL:     "https://kb-server.example.com/mcp",
		Headers: map[string]string{"Authorization": "Basic ${KB_TOKEN}"},
	}
	r, err := configurator.EmitServer("kb-server", spec, configurator.ProviderCodex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(r.Content), "bearer_token_env_var") {
		t.Errorf("should not emit bearer_token_env_var for a non-Bearer Authorization: %s", r.Content)
	}
	if len(r.Warnings) == 0 {
		t.Error("expected a warning about the unrepresentable Authorization header")
	}
}

func TestEmitServer_NoHeaders(t *testing.T) {
	spec := configurator.ServerSpec{Type: "http", URL: "https://kb-server.example.com/mcp"}
	for _, provider := range []configurator.Provider{
		configurator.ProviderClaudeCode, configurator.ProviderCodex,
		configurator.ProviderKiro, configurator.ProviderOpenCode,
	} {
		r, err := configurator.EmitServer("kb-server", spec, provider)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", provider, err)
		}
		if len(r.Warnings) != 0 {
			t.Errorf("%s: unexpected warnings with no headers: %v", provider, r.Warnings)
		}
	}
}
