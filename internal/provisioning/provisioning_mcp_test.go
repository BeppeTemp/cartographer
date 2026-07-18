package provisioning_test

// Test per il provisioning kind "mcp" (D69): server MCP di terze parti
// distribuiti da una KB via mcp/<nome>.json, materializzati come merge nel
// config nativo di ciascun provider (internal/provisioning/mcpsettings.go).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// writeMCPFixture writes mcp/<name>.json in kbRoot with the given content.
func writeMCPFixture(t *testing.T, kbRoot, name, content string) {
	t.Helper()
	mcpDir := filepath.Join(kbRoot, "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mcpDir, name+".json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findMCPArtifact(t *testing.T, m provisioning.Manifest, name string) provisioning.Artifact {
	t.Helper()
	for _, a := range m.Artifacts {
		if a.Kind == "mcp" && a.Name == name {
			return a
		}
	}
	t.Fatalf("mcp artifact %q not found in manifest: %+v", name, m.Artifacts)
	return provisioning.Artifact{}
}

func TestBuildManifest_MCP_NoDirIsRetrocompat(t *testing.T) {
	kbRoot := t.TempDir()
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	for _, a := range m.Artifacts {
		if a.Kind == "mcp" {
			t.Errorf("no mcp/ directory: expected zero mcp artifacts, got %+v", a)
		}
	}
}

func TestBuildManifest_MCP_ScansAndAlwaysUnsigned(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "wiki-tools",
		`{"type":"http","url":"https://tools.example.com/mcp","headers":{"Authorization":"Bearer ${WIKI_TOOLS_TOKEN}"}}`)

	for _, autoTrust := range []bool{false, true} {
		m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, autoTrust)
		if err != nil {
			t.Fatalf("BuildManifest(autoTrust=%v): %v", autoTrust, err)
		}
		a := findMCPArtifact(t, m, "wiki-tools")
		if a.Source != "kb:kb" {
			t.Errorf("Source = %q, want kb:kb", a.Source)
		}
		if a.ContentHash == "" {
			t.Error("ContentHash should not be empty")
		}
		// D69 WP5: never signed regardless of autoTrust — a stricter policy
		// than the other kinds.
		if a.Signed {
			t.Errorf("autoTrust=%v: Signed = true, want always false for kind mcp", autoTrust)
		}
	}
}

func TestBuildManifest_MCP_MalformedFileFailsBuild(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "broken", `{not json`)

	if _, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, false); err == nil {
		t.Fatal("expected BuildManifest to fail on a malformed mcp/*.json file")
	}
}

func TestBuildManifest_MCP_LiteralSecretFailsBuild(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "leaky",
		`{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer sk-live-hardcoded"}}`)

	if _, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, false); err == nil {
		t.Fatal("expected BuildManifest to fail on a literal secret in headers")
	}
}

// signedMCPManifest builds a manifest from kbRoot and flips the named mcp
// artifact's Signed field to true — simulating an explicit operator approval
// (D69 WP5: BuildManifest itself never signs kind "mcp", see
// TestBuildManifest_MCP_ScansAndAlwaysUnsigned).
func signedMCPManifest(t *testing.T, kbRoot, name string) provisioning.Manifest {
	t.Helper()
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	for i := range m.Artifacts {
		if m.Artifacts[i].Kind == "mcp" && m.Artifacts[i].Name == name {
			m.Artifacts[i].Signed = true
		}
	}
	return m
}

func TestApply_MCP_UnsignedNeedsApproval(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "wiki-tools", `{"type":"http","url":"https://tools.example.com/mcp"}`)
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	dir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  dir,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// "instructions" (D56) is always present/materialized regardless — only
	// assert nothing of kind "mcp" was written.
	for _, w := range res.Written {
		if w.Kind == "mcp" {
			t.Errorf("expected no mcp artifact written, got %+v", w)
		}
	}
	found := false
	for _, a := range res.NeedsApproval {
		if a.Kind == "mcp" && a.Name == "wiki-tools" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected wiki-tools in NeedsApproval, got %+v", res.NeedsApproval)
	}
}

func TestApply_MCP_AllProviders(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "wiki-tools",
		`{"type":"http","url":"https://tools.example.com/mcp","headers":{"Authorization":"Bearer ${WIKI_TOOLS_TOKEN}"}}`)

	cases := []struct {
		provider configurator.Provider
		filePath string
		check    func(t *testing.T, data []byte)
	}{
		{configurator.ProviderClaudeCode, ".claude.json", func(t *testing.T, data []byte) {
			var root map[string]any
			if err := json.Unmarshal(data, &root); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			entry := root["mcpServers"].(map[string]any)["wiki-tools"].(map[string]any)
			if entry["url"] != "https://tools.example.com/mcp" {
				t.Errorf("unexpected entry: %+v", entry)
			}
		}},
		{configurator.ProviderCodex, filepath.Join(".codex", "config.toml"), func(t *testing.T, data []byte) {
			content := string(data)
			if !strings.Contains(content, "[mcp_servers.wiki-tools]") {
				t.Errorf("missing section header: %s", content)
			}
			if !strings.Contains(content, `bearer_token_env_var = "WIKI_TOOLS_TOKEN"`) {
				t.Errorf("missing bearer_token_env_var: %s", content)
			}
		}},
		{configurator.ProviderOpenCode, "opencode.json", func(t *testing.T, data []byte) {
			var root map[string]any
			if err := json.Unmarshal(data, &root); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			entry := root["mcp"].(map[string]any)["wiki-tools"].(map[string]any)
			headers := entry["headers"].(map[string]any)
			if headers["Authorization"] != "Bearer {env:WIKI_TOOLS_TOKEN}" {
				t.Errorf("Authorization = %v, want opencode {env:VAR} syntax", headers["Authorization"])
			}
		}},
		{configurator.ProviderKiro, filepath.Join(".kiro", "settings", "mcp.json"), func(t *testing.T, data []byte) {
			var root map[string]any
			if err := json.Unmarshal(data, &root); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			entry := root["mcpServers"].(map[string]any)["wiki-tools"].(map[string]any)
			if _, hasHeaders := entry["headers"]; hasHeaders {
				t.Error("kiro should not receive headers")
			}
		}},
	}

	for _, tc := range cases {
		t.Run(string(tc.provider), func(t *testing.T) {
			m := signedMCPManifest(t, kbRoot, "wiki-tools")
			dir := t.TempDir()
			res, err := provisioning.Apply(m, provisioning.ApplyOptions{
				KBRoots:  map[string]string{"kb": kbRoot},
				Provider: tc.provider,
				BaseDir:  dir,
			})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			// "instructions" (D56) is always present/materialized alongside —
			// isolate the mcp-kind ManagedFile among res.Written.
			var mcpWritten *provisioning.ManagedFile
			for i, w := range res.Written {
				if w.Kind == "mcp" {
					mcpWritten = &res.Written[i]
				}
			}
			if mcpWritten == nil || mcpWritten.Name != "wiki-tools" {
				t.Fatalf("expected wiki-tools mcp ManagedFile in Written, got %+v", res.Written)
			}
			if mcpWritten.Path != tc.filePath {
				t.Errorf("ManagedFile.Path = %q, want %q", mcpWritten.Path, tc.filePath)
			}

			data, err := os.ReadFile(filepath.Join(dir, tc.filePath))
			if err != nil {
				t.Fatalf("read %s: %v", tc.filePath, err)
			}
			tc.check(t, data)

			if tc.provider == configurator.ProviderKiro && len(res.Warnings) == 0 {
				t.Error("expected a warning: kiro cannot represent the mcp server's auth header")
			}
		})
	}
}

func TestApply_MCP_PreservesOtherEntriesInSharedFile(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "wiki-tools", `{"type":"http","url":"https://tools.example.com/mcp"}`)
	dir := t.TempDir()

	// Pre-existing .claude.json with Cartographer's own entry and unrelated keys.
	claudeJSONPath := filepath.Join(dir, ".claude.json")
	preexisting := `{"mcpServers":{"cartographer":{"url":"http://localhost:8080/mcp","type":"http"}},"model":"opus"}`
	if err := os.WriteFile(claudeJSONPath, []byte(preexisting), 0o644); err != nil {
		t.Fatal(err)
	}

	m := signedMCPManifest(t, kbRoot, "wiki-tools")
	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  dir,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(claudeJSONPath)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if root["model"] != "opus" {
		t.Error("unrelated top-level key not preserved")
	}
	servers := root["mcpServers"].(map[string]any)
	if _, ok := servers["cartographer"]; !ok {
		t.Error("cartographer's own entry should not be disturbed")
	}
	if _, ok := servers["wiki-tools"]; !ok {
		t.Error("wiki-tools entry should have been written")
	}
}

func TestApply_MCP_PruneRestoresSharedFile(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "wiki-tools",
		`{"type":"http","url":"https://tools.example.com/mcp","headers":{"Authorization":"Bearer ${WIKI_TOOLS_TOKEN}"}}`)
	dir := t.TempDir()

	m := signedMCPManifest(t, kbRoot, "wiki-tools")
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  dir,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if _, err := provisioning.PruneManaged(res.NewLock.Managed, dir, false); err != nil {
		t.Fatalf("PruneManaged: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		t.Fatalf(".claude.json should still exist (D63 rule): %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if servers, ok := root["mcpServers"]; ok {
		t.Errorf("mcpServers key should have been dropped once empty, got %v", servers)
	}
}

func TestApply_MCP_PruneCodexPreservesUserContent(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "wiki-tools", `{"type":"http","url":"https://tools.example.com/mcp"}`)
	dir := t.TempDir()

	codexPath := filepath.Join(dir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	userConfig := "# la mia config\nmodel = \"gpt-5.3-codex\"\n"
	if err := os.WriteFile(codexPath, []byte(userConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	m := signedMCPManifest(t, kbRoot, "wiki-tools")
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderCodex,
		BaseDir:  dir,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := provisioning.PruneManaged(res.NewLock.Managed, dir, false); err != nil {
		t.Fatalf("PruneManaged: %v", err)
	}

	data, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "mcp_servers") {
		t.Errorf("wiki-tools block should have been stripped: %s", data)
	}
	if !strings.Contains(string(data), "# la mia config") || !strings.Contains(string(data), `model = "gpt-5.3-codex"`) {
		t.Errorf("user content not preserved: %s", data)
	}
}
