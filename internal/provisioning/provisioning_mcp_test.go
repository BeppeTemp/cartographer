package provisioning_test

// Test per il provisioning kind "mcp" (D69): server MCP di terze parti
// distribuiti da una KB via mcp/<nome>.json, materializzati come merge nel
// config nativo di ciascun provider (internal/provisioning/mcpsettings.go).

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
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

func mcpAllow(name, target string) provisioning.BuildOptions {
	return provisioning.BuildOptions{MCPAllowlists: map[string][]provisioning.MCPAllowlistEntry{
		"kb": {{Name: name, Transport: "http", Target: target}},
	}}
}

func mcpAllowStdio(name, command string) provisioning.BuildOptions {
	return provisioning.BuildOptions{MCPAllowlists: map[string][]provisioning.MCPAllowlistEntry{
		"kb": {{Name: name, Transport: "stdio", Target: command}},
	}}
}

// writeFakeExecutable writes a shell script at path that records its own
// invocation into markerPath so a test can assert Cartographer never runs a
// provisioned stdio MCP server.
func writeFakeExecutable(t *testing.T, path, markerPath string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntouch \""+markerPath+"\"\n"), 0o755); err != nil {
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
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllow("wiki-tools", "https://tools.example.com/mcp"))
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	for _, a := range m.Artifacts {
		if a.Kind == "mcp" {
			t.Errorf("no mcp/ directory: expected zero mcp artifacts, got %+v", a)
		}
	}
}

func TestBuildManifest_MCPAllowlistDenyByDefaultAndExactEndpoint(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "tools", `{"type":"http","url":"https://TOOLS.example.com/mcp"}`)

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllow("wiki-tools", "https://tools.example.com/mcp"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range m.Artifacts {
		if a.Kind == "mcp" {
			t.Fatalf("deny-by-default exposed %+v", a)
		}
	}
	wrong := mcpAllow("tools", "https://tools.example.com/other")
	m, err = provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, wrong)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range m.Artifacts {
		if a.Kind == "mcp" {
			t.Fatalf("wrong endpoint exposed %+v", a)
		}
	}
	allowed := mcpAllow("tools", "https://tools.example.com/mcp")
	m, err = provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, allowed)
	if err != nil {
		t.Fatal(err)
	}
	findMCPArtifact(t, m, "tools")
}

func TestApply_MCPHashBoundApproval(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "tools", `{"type":"http","url":"https://tools.example.com/mcp"}`)
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllow("tools", "https://tools.example.com/mcp"))
	if err != nil {
		t.Fatal(err)
	}
	a := findMCPArtifact(t, m, "tools")
	opts := provisioning.ApplyOptions{KBRoots: map[string]string{"kb": kbRoot}, Provider: configurator.ProviderClaudeCode, BaseDir: t.TempDir(), ApprovedMCP: map[string]string{"kb:kb\x00tools": a.ContentHash}}
	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, pending := range res.NeedsApproval {
		if pending.Kind == "mcp" {
			t.Fatalf("approved descriptor needs approval: %+v", res.NeedsApproval)
		}
	}
	if len(res.Written) == 0 {
		t.Fatal("approved descriptor was not materialized")
	}
	writeMCPFixture(t, kbRoot, "tools", `{"type":"http","url":"https://tools.example.com/changed"}`)
	m, err = provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllow("tools", "https://tools.example.com/changed"))
	if err != nil {
		t.Fatal(err)
	}
	res, err = provisioning.Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, pending := range res.NeedsApproval {
		if pending.Kind == "mcp" && pending.Name == "tools" {
			found = true
		}
	}
	if !found {
		t.Fatalf("changed descriptor must need reapproval: %+v", res.NeedsApproval)
	}
}

func TestBuildManifest_MCPAllowlistDiagnosticsAreSafe(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "tools", `{"type":"http","url":"https://tools.example.com/mcp","headers":{"Authorization":"Bearer ${TOKEN}"}}`)
	var diagnostics []string
	_, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{MCPDiagnostic: func(s string) { diagnostics = append(diagnostics, s) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0], "tools") || strings.Contains(diagnostics[0], "TOKEN") {
		t.Fatalf("unsafe or missing diagnostic: %v", diagnostics)
	}
}

func TestBuildManifest_MCP_ScansAndAlwaysUnsigned(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "wiki-tools",
		`{"type":"http","url":"https://tools.example.com/mcp","headers":{"Authorization":"Bearer ${WIKI_TOOLS_TOKEN}"}}`)

	{
		m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllow("wiki-tools", "https://tools.example.com/mcp"))
		if err != nil {
			t.Fatalf("BuildManifest: %v", err)
		}
		a := findMCPArtifact(t, m, "wiki-tools")
		if a.Source != "kb:kb" {
			t.Errorf("Source = %q, want kb:kb", a.Source)
		}
		if a.ContentHash == "" {
			t.Error("ContentHash should not be empty")
		}
		// Without a configured signer, an MCP artifact remains unsigned.
		if a.Signed {
			t.Error("Signed = true, want false without signer")
		}
	}
}

func TestBuildManifest_MCPAllowedDescriptorCanBeVerified(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "tools", `{"type":"http","url":"https://tools.example.com/mcp"}`)
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, ed25519.SeedSize))
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{
		Signers:       map[string]ed25519.PrivateKey{"kb": key},
		MCPAllowlists: map[string][]provisioning.MCPAllowlistEntry{"kb": {{Name: "tools", Transport: "http", Target: "https://tools.example.com/mcp"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := provisioning.VerifiedManifest(m, map[string][]ed25519.PublicKey{"kb": {key.Public().(ed25519.PublicKey)}})
	if err != nil {
		t.Fatal(err)
	}
	if !findMCPArtifact(t, verified, "tools").Signed {
		t.Fatal("verified allowed MCP must be authorized without point approval")
	}
}

func TestBuildManifest_MCP_MalformedFileFailsBuild(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "broken", `{not json`)

	if _, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{}); err == nil {
		t.Fatal("expected BuildManifest to fail on a malformed mcp/*.json file")
	}
}

func TestBuildManifest_MCP_LiteralSecretFailsBuild(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "leaky",
		`{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer sk-live-hardcoded"}}`)

	if _, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{}); err == nil {
		t.Fatal("expected BuildManifest to fail on a literal secret in headers")
	}
}

// signedMCPManifest builds a manifest from kbRoot and flips the named mcp
// artifact's Signed field to true — simulating an explicit operator approval
// (D69 WP5: BuildManifest itself never signs kind "mcp", see
// TestBuildManifest_MCP_ScansAndAlwaysUnsigned).
func signedMCPManifest(t *testing.T, kbRoot, name string) provisioning.Manifest {
	t.Helper()
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllow(name, "https://tools.example.com/mcp"))
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
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllow("wiki-tools", "https://tools.example.com/mcp"))
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

// TestApply_MCP_Stdio_AllProviders mirrors TestApply_MCP_AllProviders for the
// stdio transport (D116): every provider must emit the command/args/env
// natively, and the fake executable's marker must never appear — Apply only
// materializes configuration, it never runs the described command.
func TestApply_MCP_Stdio_AllProviders(t *testing.T) {
	kbRoot := t.TempDir()
	binDir := t.TempDir()
	marker := filepath.Join(binDir, "executed")
	script := filepath.Join(binDir, "fake-mcp")
	writeFakeExecutable(t, script, marker)
	writeMCPFixture(t, kbRoot, "local-tools", fmt.Sprintf(`{"type":"stdio","command":%q,"args":["serve","--flag"],"env":{"TOKEN":"${LOCAL_TOKEN}"}}`, script))

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
			entry := root["mcpServers"].(map[string]any)["local-tools"].(map[string]any)
			if entry["command"] != script {
				t.Errorf("unexpected command: %+v", entry)
			}
		}},
		{configurator.ProviderCodex, filepath.Join(".codex", "config.toml"), func(t *testing.T, data []byte) {
			content := string(data)
			if !strings.Contains(content, "[mcp_servers.local-tools]") || !strings.Contains(content, "command =") {
				t.Errorf("missing stdio section: %s", content)
			}
		}},
		{configurator.ProviderOpenCode, "opencode.json", func(t *testing.T, data []byte) {
			var root map[string]any
			if err := json.Unmarshal(data, &root); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			entry := root["mcp"].(map[string]any)["local-tools"].(map[string]any)
			if entry["type"] != "local" {
				t.Errorf("unexpected entry: %+v", entry)
			}
		}},
		{configurator.ProviderKiro, filepath.Join(".kiro", "settings", "mcp.json"), func(t *testing.T, data []byte) {
			var root map[string]any
			if err := json.Unmarshal(data, &root); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			entry := root["mcpServers"].(map[string]any)["local-tools"].(map[string]any)
			if entry["command"] != script {
				t.Errorf("unexpected entry: %+v", entry)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(string(tc.provider), func(t *testing.T) {
			m := signedMCPManifestStdio(t, kbRoot, "local-tools", script)
			dir := t.TempDir()
			res, err := provisioning.Apply(m, provisioning.ApplyOptions{
				KBRoots:  map[string]string{"kb": kbRoot},
				Provider: tc.provider,
				BaseDir:  dir,
			})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(dir, tc.filePath))
			if err != nil {
				t.Fatalf("read %s: %v", tc.filePath, err)
			}
			tc.check(t, data)
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatal("Apply executed the provisioned stdio command")
			}
			_ = res
		})
	}
}

// signedMCPManifestStdio mirrors signedMCPManifest for a stdio descriptor.
func signedMCPManifestStdio(t *testing.T, kbRoot, name, command string) provisioning.Manifest {
	t.Helper()
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllowStdio(name, command))
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

// TestApply_MCP_StdioPreflight_CommandResolution covers PATH lookup, absolute
// path, a missing command, and a non-executable file (WP3): a bad command
// fails before any provider file is written, and Cartographer never persists
// the resolved absolute path for a bare command (normal PATH semantics are
// preserved for the provider itself).
func TestApply_MCP_StdioPreflight_CommandResolution(t *testing.T) {
	t.Run("bare command resolved via PATH", func(t *testing.T) {
		binDir := t.TempDir()
		marker := filepath.Join(binDir, "executed")
		script := filepath.Join(binDir, "fake-mcp")
		writeFakeExecutable(t, script, marker)
		t.Setenv("PATH", binDir)

		kbRoot := t.TempDir()
		writeMCPFixture(t, kbRoot, "local-tools", `{"type":"stdio","command":"fake-mcp"}`)
		m := signedMCPManifestStdio(t, kbRoot, "local-tools", "fake-mcp")
		dir := t.TempDir()
		if _, err := provisioning.Apply(m, provisioning.ApplyOptions{KBRoots: map[string]string{"kb": kbRoot}, Provider: configurator.ProviderClaudeCode, BaseDir: dir}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), script) {
			t.Errorf("bare command was persisted as its resolved absolute path: %s", data)
		}
		if !strings.Contains(string(data), `"fake-mcp"`) {
			t.Errorf("bare command not preserved verbatim: %s", data)
		}
	})

	t.Run("absolute path", func(t *testing.T) {
		binDir := t.TempDir()
		marker := filepath.Join(binDir, "executed")
		script := filepath.Join(binDir, "fake-mcp")
		writeFakeExecutable(t, script, marker)

		kbRoot := t.TempDir()
		writeMCPFixture(t, kbRoot, "local-tools", fmt.Sprintf(`{"type":"stdio","command":%q}`, script))
		m := signedMCPManifestStdio(t, kbRoot, "local-tools", script)
		dir := t.TempDir()
		if _, err := provisioning.Apply(m, provisioning.ApplyOptions{KBRoots: map[string]string{"kb": kbRoot}, Provider: configurator.ProviderClaudeCode, BaseDir: dir}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	})

	t.Run("missing command fails closed", func(t *testing.T) {
		kbRoot := t.TempDir()
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		writeMCPFixture(t, kbRoot, "local-tools", fmt.Sprintf(`{"type":"stdio","command":%q}`, missing))
		m := signedMCPManifestStdio(t, kbRoot, "local-tools", missing)
		dir := t.TempDir()
		_, err := provisioning.Apply(m, provisioning.ApplyOptions{KBRoots: map[string]string{"kb": kbRoot}, Provider: configurator.ProviderClaudeCode, BaseDir: dir})
		if err == nil || !strings.Contains(err.Error(), "unavailable") {
			t.Fatalf("Apply error = %v, want an unavailable-command error", err)
		}
		if _, statErr := os.Stat(filepath.Join(dir, ".claude.json")); !os.IsNotExist(statErr) {
			t.Fatal("provider config was written despite a missing command")
		}
	})

	t.Run("non-executable file fails closed", func(t *testing.T) {
		kbRoot := t.TempDir()
		notExec := filepath.Join(t.TempDir(), "not-executable")
		if err := os.WriteFile(notExec, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeMCPFixture(t, kbRoot, "local-tools", fmt.Sprintf(`{"type":"stdio","command":%q}`, notExec))
		m := signedMCPManifestStdio(t, kbRoot, "local-tools", notExec)
		dir := t.TempDir()
		_, err := provisioning.Apply(m, provisioning.ApplyOptions{KBRoots: map[string]string{"kb": kbRoot}, Provider: configurator.ProviderClaudeCode, BaseDir: dir})
		if err == nil || !strings.Contains(err.Error(), "not an executable regular file") {
			t.Fatalf("Apply error = %v, want a not-executable error", err)
		}
		if _, statErr := os.Stat(filepath.Join(dir, ".claude.json")); !os.IsNotExist(statErr) {
			t.Fatal("provider config was written despite a non-executable command")
		}
	})
}

// TestApply_MCP_StdioDryRun verifies --dry-run computes the plan without
// writing any provider file or the lockfile, for a stdio descriptor.
func TestApply_MCP_StdioDryRun(t *testing.T) {
	kbRoot := t.TempDir()
	binDir := t.TempDir()
	script := filepath.Join(binDir, "fake-mcp")
	writeFakeExecutable(t, script, filepath.Join(binDir, "executed"))
	writeMCPFixture(t, kbRoot, "local-tools", fmt.Sprintf(`{"type":"stdio","command":%q}`, script))
	m := signedMCPManifestStdio(t, kbRoot, "local-tools", script)

	dir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  dir,
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	found := false
	for _, w := range res.Written {
		found = found || w.Kind == "mcp"
	}
	if !found {
		t.Fatal("dry-run should still report the planned write in res.Written")
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".claude.json")); !os.IsNotExist(statErr) {
		t.Fatal("dry-run wrote the provider config file")
	}
	if _, statErr := os.Stat(filepath.Join(dir, provisioning.LockFileName)); !os.IsNotExist(statErr) {
		t.Fatal("dry-run wrote the lockfile")
	}
}

// TestBuildManifest_MCPAllowlistMatchesStdioCommandExactly is the stdio
// counterpart of TestBuildManifest_MCPAllowlistDenyByDefaultAndExactEndpoint.
func TestBuildManifest_MCPAllowlistMatchesStdioCommandExactly(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "local-tools", `{"type":"stdio","command":"cartographer-test-tool"}`)

	denied, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllowStdio("local-tools", "other-tool"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range denied.Artifacts {
		if a.Kind == "mcp" {
			t.Fatalf("mismatched stdio command was allowed: %+v", a)
		}
	}

	allowed, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllowStdio("local-tools", "cartographer-test-tool"))
	if err != nil {
		t.Fatal(err)
	}
	findMCPArtifact(t, allowed, "local-tools")
}

// TestApply_MCP_StdioContentHashCoversEveryDescriptorField is the D116
// regression test for the D115 hash-bound approval: the content hash is the
// whole mcp/<name>.json file hash (WP3), so changing command, an argument, or
// an environment reference must each invalidate a prior point approval.
func TestApply_MCP_StdioContentHashCoversEveryDescriptorField(t *testing.T) {
	kbRoot := t.TempDir()
	binDir := t.TempDir()
	script := filepath.Join(binDir, "fake-mcp")
	writeFakeExecutable(t, script, filepath.Join(binDir, "executed"))

	baseline := fmt.Sprintf(`{"type":"stdio","command":%q,"args":["serve"],"env":{"TOKEN":"${TOKEN}"}}`, script)
	writeMCPFixture(t, kbRoot, "local-tools", baseline)
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllowStdio("local-tools", script))
	if err != nil {
		t.Fatal(err)
	}
	baselineHash := findMCPArtifact(t, m, "local-tools").ContentHash
	approved := map[string]string{"kb:kb\x00local-tools": baselineHash}

	variants := []struct {
		name           string
		content        string
		allowedCommand string
	}{
		// The allow-list also matches on exact command identity (D115), so a
		// changed-command variant needs its own allow-list entry: the
		// approval hash mismatch, not the allow-list, is what's under test here.
		{"changed command", fmt.Sprintf(`{"type":"stdio","command":%q,"args":["serve"],"env":{"TOKEN":"${TOKEN}"}}`, script+"-other"), script + "-other"},
		{"changed args", fmt.Sprintf(`{"type":"stdio","command":%q,"args":["serve","--extra"],"env":{"TOKEN":"${TOKEN}"}}`, script), script},
		{"changed env ref", fmt.Sprintf(`{"type":"stdio","command":%q,"args":["serve"],"env":{"TOKEN":"${OTHER_TOKEN}"}}`, script), script},
	}
	for _, v := range variants {
		name, content, allowedCommand := v.name, v.content, v.allowedCommand
		t.Run(name, func(t *testing.T) {
			writeMCPFixture(t, kbRoot, "local-tools", content)
			changed, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllowStdio("local-tools", allowedCommand))
			if err != nil {
				t.Fatal(err)
			}
			a := findMCPArtifact(t, changed, "local-tools")
			if a.ContentHash == baselineHash {
				t.Fatalf("%s: content hash unchanged (%s)", name, a.ContentHash)
			}
			res, err := provisioning.Apply(changed, provisioning.ApplyOptions{
				KBRoots:     map[string]string{"kb": kbRoot},
				Provider:    configurator.ProviderClaudeCode,
				BaseDir:     t.TempDir(),
				ApprovedMCP: approved,
			})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			found := false
			for _, pending := range res.NeedsApproval {
				found = found || (pending.Kind == "mcp" && pending.Name == "local-tools")
			}
			if !found {
				t.Fatalf("%s: stale approval was accepted for the changed descriptor", name)
			}
		})
	}
	writeMCPFixture(t, kbRoot, "local-tools", baseline)
}

func TestApply_MCP_PreservesOtherEntriesInSharedFile(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "wiki-tools", `{"type":"http","url":"https://tools.example.com/mcp"}`)
	dir := t.TempDir()

	// Pre-existing .claude.json with Cartographer's own entry and unrelated keys.
	claudeJSONPath := filepath.Join(dir, ".claude.json")
	preexisting := `{"mcpServers":{"cartographer":{"url":"https://mcp.example.test/mcp","type":"http"}},"model":"opus"}`
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
