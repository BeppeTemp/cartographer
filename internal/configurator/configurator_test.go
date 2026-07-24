package configurator_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func TestDefaultConfig(t *testing.T) {
	cfg := configurator.DefaultConfig()
	if cfg.Name == "" {
		t.Error("Name should have a default value")
	}
	if cfg.URL == "" {
		t.Error("URL should have a default value")
	}
	if cfg.AuthEnabled {
		t.Error("AuthEnabled should default to false")
	}
	if cfg.TokenEnv == "" {
		t.Error("TokenEnv should have a default value")
	}
}

func TestEmitClaudeCode(t *testing.T) {
	cfg := &configurator.ServerConfig{
		Name:        "wiki",
		URL:         "http://localhost:8080/mcp",
		AuthEnabled: true,
		TokenEnv:    "CARTOGRAPHER_TOKENS",
	}
	r, err := configurator.Emit(cfg, configurator.ProviderClaudeCode)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Provider != configurator.ProviderClaudeCode {
		t.Errorf("Provider = %q, want %q", r.Provider, configurator.ProviderClaudeCode)
	}
	if r.FilePath != ".claude.json" {
		t.Errorf("FilePath = %q, want .claude.json", r.FilePath)
	}

	var root map[string]any
	if err := json.Unmarshal(r.Content, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("missing or wrong type for mcpServers")
	}
	entry, ok := servers["wiki"].(map[string]any)
	if !ok {
		t.Fatalf("missing wiki entry in mcpServers")
	}
	if entry["type"] != "http" {
		t.Errorf("type = %v, want http", entry["type"])
	}
	if _, ok := entry["url"]; !ok {
		t.Error("missing url field in entry")
	}
	if _, ok := entry["headers"]; !ok {
		t.Error("missing headers field when auth enabled")
	}
}

func TestEmitAll(t *testing.T) {
	cfg := &configurator.ServerConfig{
		Name: "wiki",
		URL:  "http://localhost:8080/mcp",
	}
	results, err := configurator.EmitAll(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	seen := map[string]bool{}
	for _, r := range results {
		if seen[r.FilePath] {
			t.Errorf("duplicate FilePath: %s", r.FilePath)
		}
		seen[r.FilePath] = true
		if r.Provider == configurator.ProviderCodex {
			// Codex's Content is a TOML block body, not JSON (D58) — see
			// TestEmitCodex_TOML for the format assertion.
			continue
		}
		// Every other provider's result must produce valid JSON.
		var parsed map[string]any
		if err := json.Unmarshal(r.Content, &parsed); err != nil {
			t.Errorf("provider %s: invalid JSON: %v", r.Provider, err)
		}
	}
}

func TestEmitOpenCode_HTTP_NoAuth(t *testing.T) {
	cfg := &configurator.ServerConfig{
		Name:        "wiki",
		URL:         "http://localhost:8080/mcp",
		AuthEnabled: false,
		TokenEnv:    "CARTOGRAPHER_TOKENS",
	}
	r, err := configurator.Emit(cfg, configurator.ProviderOpenCode)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.FilePath != "opencode.json" {
		t.Errorf("FilePath = %q, want opencode.json", r.FilePath)
	}

	var root map[string]any
	if err := json.Unmarshal(r.Content, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if root["$schema"] != "https://opencode.ai/config.json" {
		t.Errorf("$schema = %v, want https://opencode.ai/config.json", root["$schema"])
	}
	mcp, ok := root["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("missing or wrong type for mcp")
	}
	entry, ok := mcp["wiki"].(map[string]any)
	if !ok {
		t.Fatalf("missing wiki entry in mcp")
	}
	if entry["type"] != "remote" {
		t.Errorf("type = %v, want remote", entry["type"])
	}
	if entry["url"] != "http://localhost:8080/mcp" {
		t.Errorf("url = %v, want http://localhost:8080/mcp", entry["url"])
	}
	if entry["enabled"] != true {
		t.Errorf("enabled = %v, want true", entry["enabled"])
	}
	if _, hasHeaders := entry["headers"]; hasHeaders {
		t.Error("headers should not be present when auth is disabled")
	}
}

func TestEmitOpenCode_HTTP_Auth(t *testing.T) {
	cfg := &configurator.ServerConfig{
		Name:        "wiki",
		URL:         "http://localhost:8080/mcp",
		AuthEnabled: true,
		TokenEnv:    "CARTOGRAPHER_TOKENS",
	}
	r, err := configurator.Emit(cfg, configurator.ProviderOpenCode)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(r.Content, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	mcp := root["mcp"].(map[string]any)
	entry := mcp["wiki"].(map[string]any)
	headers, ok := entry["headers"].(map[string]any)
	if !ok {
		t.Fatalf("headers missing or wrong type when auth enabled")
	}
	authHeader, _ := headers["Authorization"].(string)
	// OpenCode usa {env:VAR} — non ${VAR}.
	if authHeader != "Bearer {env:CARTOGRAPHER_TOKENS}" {
		t.Errorf("Authorization header = %q, want Bearer {env:CARTOGRAPHER_TOKENS}", authHeader)
	}
}

func TestApplyDryRun(t *testing.T) {
	cfg := &configurator.ServerConfig{
		Name: "wiki",
		URL:  "http://localhost:8080/mcp",
	}
	results, err := configurator.EmitAll(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if _, err := configurator.Apply(results, dir, true); err != nil {
		t.Fatalf("Apply dry-run failed: %v", err)
	}

	// No files should have been written.
	for _, r := range results {
		fullPath := filepath.Join(dir, r.FilePath)
		if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
			t.Errorf("file %s should not exist after dry-run", r.FilePath)
		}
	}
}

func TestApply_ReturnsAbsolutePaths(t *testing.T) {
	r, err := configurator.Emit(&configurator.ServerConfig{Name: "wiki", URL: "http://localhost:8080/mcp"}, configurator.ProviderClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	written, err := configurator.Apply([]*configurator.EmitResult{r}, baseDir, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := filepath.Join(baseDir, r.FilePath)
	if r.AbsolutePath != want {
		t.Errorf("AbsolutePath = %q, want %q", r.AbsolutePath, want)
	}
	if len(written) != 1 || written[0] != want || !filepath.IsAbs(written[0]) {
		t.Errorf("written = %v, want [%q]", written, want)
	}
}

func TestRemove_NonDestructiveMerge(t *testing.T) {
	cfg := &configurator.ServerConfig{Name: "wiki", URL: "http://localhost:8080/mcp"}
	r, err := configurator.Emit(cfg, configurator.ProviderClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
		t.Fatal(err)
	}

	// Simulate a second, unrelated MCP server + an unrelated top-level key
	// already present in the file, as a real .claude.json would have.
	fullPath := filepath.Join(dir, r.FilePath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	servers := root["mcpServers"].(map[string]any)
	servers["other-server"] = map[string]any{"url": "http://example.com/mcp", "type": "http"}
	root["someOtherTopLevelKey"] = "keep-me"
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, out, 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := configurator.Remove(cfg, configurator.ProviderClaudeCode, dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !removed {
		t.Fatal("expected removed = true")
	}

	data, err = os.ReadFile(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("file is not valid JSON after Remove: %v", err)
	}
	gotServers, ok := got["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing or wrong type after Remove")
	}
	if _, ok := gotServers["wiki"]; ok {
		t.Error("wiki entry should have been removed")
	}
	if _, ok := gotServers["other-server"]; !ok {
		t.Error("other-server entry should be untouched")
	}
	if got["someOtherTopLevelKey"] != "keep-me" {
		t.Error("unrelated top-level key should be untouched")
	}
}

func TestRemove_NoFileIsNoop(t *testing.T) {
	cfg := &configurator.ServerConfig{Name: "wiki", URL: "http://localhost:8080/mcp"}
	dir := t.TempDir()

	removed, err := configurator.Remove(cfg, configurator.ProviderClaudeCode, dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed {
		t.Error("removed should be false when the file does not exist")
	}
}

func TestRemove_MissingKeyIsNoop(t *testing.T) {
	cfg := &configurator.ServerConfig{Name: "wiki", URL: "http://localhost:8080/mcp"}
	r, err := configurator.Emit(cfg, configurator.ProviderClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
		t.Fatal(err)
	}

	otherCfg := &configurator.ServerConfig{Name: "other", URL: "http://localhost:8080/mcp"}
	removed, err := configurator.Remove(otherCfg, configurator.ProviderClaudeCode, dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed {
		t.Error("removed should be false when the key is not present")
	}

	// Idempotent: removing twice for a key that was actually there also
	// reports removed=false the second time.
	if removed, err := configurator.Remove(cfg, configurator.ProviderClaudeCode, dir, false); err != nil || !removed {
		t.Fatalf("first Remove: removed=%v err=%v, want true/nil", removed, err)
	}
	if removed, err := configurator.Remove(cfg, configurator.ProviderClaudeCode, dir, false); err != nil || removed {
		t.Fatalf("second Remove: removed=%v err=%v, want false/nil", removed, err)
	}
}

func TestRemove_DryRunDoesNotWrite(t *testing.T) {
	cfg := &configurator.ServerConfig{Name: "wiki", URL: "http://localhost:8080/mcp"}
	r, err := configurator.Emit(cfg, configurator.ProviderClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
		t.Fatal(err)
	}
	fullPath := filepath.Join(dir, r.FilePath)
	before, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatal(err)
	}

	removed, err := configurator.Remove(cfg, configurator.ProviderClaudeCode, dir, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !removed {
		t.Error("dry-run should still report removed = true (what would happen)")
	}

	after, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("dry-run must not modify the file")
	}
}

func TestApplyWrite(t *testing.T) {
	cfg := &configurator.ServerConfig{
		Name: "wiki",
		URL:  "http://localhost:8080/mcp",
	}
	results, err := configurator.EmitAll(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if _, err := configurator.Apply(results, dir, false); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	for _, r := range results {
		fullPath := filepath.Join(dir, r.FilePath)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			t.Errorf("file %s not written: %v", r.FilePath, err)
			continue
		}
		if r.Provider == configurator.ProviderCodex {
			// codex writes TOML, not JSON (D58) — see TestEmitCodex_TOML.
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Errorf("file %s is not valid JSON: %v", r.FilePath, err)
		}
	}
}
