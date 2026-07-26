package configurator_test

// Tests for the real Codex CLI integration (D58): config.toml with a managed
// block (instead of the legacy config.json, which Codex never reads), with
// pre-existing user content preserved byte-for-byte outside the block.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func TestEmitCodex_TOML(t *testing.T) {
	cfg := &configurator.ServerConfig{
		Name:        "cartographer",
		URL:         "http://localhost:8080/mcp",
		AuthEnabled: true,
		TokenEnv:    "CARTOGRAPHER_TOKENS",
	}
	r, err := configurator.Emit(cfg, configurator.ProviderCodex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.FilePath != filepath.Join(".codex", "config.toml") {
		t.Errorf("FilePath = %q, want .codex/config.toml", r.FilePath)
	}
	content := string(r.Content)
	if !strings.Contains(content, `[mcp_servers.cartographer]`) {
		t.Errorf("missing [mcp_servers.cartographer] section header: %s", content)
	}
	if !strings.Contains(content, `url = "http://localhost:8080/mcp"`) {
		t.Errorf("missing url key: %s", content)
	}
	if !strings.Contains(content, `bearer_token_env_var = "CARTOGRAPHER_TOKENS"`) {
		t.Errorf("missing bearer_token_env_var key when auth enabled: %s", content)
	}
}

func TestEmitCodex_TOML_NoAuth(t *testing.T) {
	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "http://localhost:8080/mcp"}
	r, err := configurator.Emit(cfg, configurator.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(r.Content), "bearer_token_env_var") {
		t.Errorf("bearer_token_env_var should not be present when auth is disabled: %s", r.Content)
	}
}

func TestApplyCodex_PreservesUserTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	preexisting := "# my own comment\nmodel = \"gpt-5.3-codex\"\n\n[mcp_servers.other]\nurl = \"https://example.com/mcp\"\n"
	if err := os.WriteFile(path, []byte(preexisting), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "http://localhost:8080/mcp"}
	r, err := configurator.Emit(cfg, configurator.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "# my own comment") || !strings.Contains(got, `model = "gpt-5.3-codex"`) {
		t.Errorf("user content not preserved: %s", got)
	}
	if !strings.Contains(got, "[mcp_servers.other]") {
		t.Errorf("other mcp_servers entry not preserved: %s", got)
	}
	if !strings.Contains(got, "[mcp_servers.cartographer]") {
		t.Errorf("cartographer entry not written: %s", got)
	}

	// Re-apply must replace, not duplicate, the managed block (idempotent).
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
		t.Fatalf("Apply (2): %v", err)
	}
	data2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data2), "[mcp_servers.cartographer]") != 1 {
		t.Errorf("re-apply must not duplicate the block: %s", data2)
	}
}

func TestRemoveCodex_StripsBlock_DeletesFileIfEmpty(t *testing.T) {
	dir := t.TempDir()
	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "http://localhost:8080/mcp"}
	r, err := configurator.Emit(cfg, configurator.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".codex", "config.toml")

	removed, err := configurator.Remove(cfg, configurator.ProviderCodex, dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !removed {
		t.Fatal("expected removed = true")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("config.toml should be removed once the managed block was its only content")
	}
}

func TestRemoveCodex_PreservesUserContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "http://localhost:8080/mcp"}
	r, err := configurator.Emit(cfg, configurator.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	preexisting := "model = \"gpt-5.3-codex\"\n"
	if err := os.WriteFile(path, []byte(preexisting), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
		t.Fatal(err)
	}

	removed, err := configurator.Remove(cfg, configurator.ProviderCodex, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("expected removed = true")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file should still exist (user content remains): %v", err)
	}
	if !strings.Contains(string(data), `model = "gpt-5.3-codex"`) {
		t.Errorf("user content not preserved: %s", data)
	}
	if strings.Contains(string(data), "mcp_servers") {
		t.Errorf("cartographer block should have been stripped: %s", data)
	}
}

func TestRemoveCodex_DryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "http://localhost:8080/mcp"}
	r, err := configurator.Emit(cfg, configurator.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".codex", "config.toml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	removed, err := configurator.Remove(cfg, configurator.ProviderCodex, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Error("dry-run should still report removed = true")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("dry-run must not modify the file")
	}
}

func TestRemoveCodex_LegacyConfigJSON_Cleanup(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, ".codex", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "mcpServers": {
    "cartographer": {"url": "http://localhost:8080/mcp", "type": "http"},
    "other": {"url": "https://example.com/mcp", "type": "http"}
  }
}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "http://localhost:8080/mcp"}
	removed, err := configurator.Remove(cfg, configurator.ProviderCodex, dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !removed {
		t.Fatal("expected removed = true (legacy config.json entry)")
	}

	data, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("legacy file should still exist (other entry remains): %v", err)
	}
	if strings.Contains(string(data), `"cartographer"`) {
		t.Errorf("legacy cartographer entry not removed: %s", data)
	}
	if !strings.Contains(string(data), `"other"`) {
		t.Errorf("other legacy entry should be preserved: %s", data)
	}
}

func TestRemoveCodex_LegacyConfigJSON_DeletesIfOnlyEntry(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, ".codex", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"mcpServers": {"cartographer": {"url": "http://localhost:8080/mcp", "type": "http"}}}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "http://localhost:8080/mcp"}
	if _, err := configurator.Remove(cfg, configurator.ProviderCodex, dir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Error("legacy config.json should be removed once empty")
	}
}

// --- Adoption of the tables Codex leaves orphaned when it rewrites config.toml (D99) ---

// writeCodexConfig writes content as <dir>/.codex/config.toml and returns its path.
func writeCodexConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// applyCodexEntry applies Cartographer's own MCP entry for codex under dir and
// returns the resulting config.toml plus the warnings Apply produced.
func applyCodexEntry(t *testing.T, dir string) (string, []string) {
	t.Helper()
	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "http://localhost:8080/mcp"}
	r, err := configurator.Emit(cfg, configurator.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data), r.Warnings
}

func TestApplyCodex_AdoptsOrphanTable_AfterCodexRewrote(t *testing.T) {
	dir := t.TempDir()
	// What Codex leaves behind: our table hoisted near the top, comments (our
	// markers included) dropped.
	writeCodexConfig(t, dir, `model = "gpt-5.6"

[mcp_servers.cartographer]
bearer_token_env_var = "CARTOGRAPHER_TOKENS"
url = "http://localhost:8080/mcp"

[mcp_servers.other]
url = "https://example.com/mcp"
`)

	got, warnings := applyCodexEntry(t, dir)

	if n := strings.Count(got, "[mcp_servers.cartographer]"); n != 1 {
		t.Errorf("expected exactly 1 [mcp_servers.cartographer], found %d:\n%s", n, got)
	}
	if !strings.Contains(got, "# cartographer:mcp:begin") {
		t.Errorf("managed block not written: %s", got)
	}
	if !strings.Contains(got, "[mcp_servers.other]\nurl = \"https://example.com/mcp\"") {
		t.Errorf("unrelated mcp entry not preserved: %s", got)
	}
	if !strings.Contains(got, `model = "gpt-5.6"`) {
		t.Errorf("user content not preserved: %s", got)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "mcp_servers.cartographer") {
		t.Errorf("expected a warning about the removed duplicate, got %v", warnings)
	}
}

func TestApplyCodex_AdoptsOrphanTable_WithSubTable(t *testing.T) {
	dir := t.TempDir()
	writeCodexConfig(t, dir, `# my own comment
model = "gpt-5.6"

[mcp_servers.cartographer]
url = "http://localhost:8080/mcp"

[mcp_servers.cartographer.env]
FOO = "bar"

# comment introducing the next table
[mcp_servers.other]
url = "https://example.com/mcp"
`)

	got, _ := applyCodexEntry(t, dir)

	if n := strings.Count(got, "[mcp_servers.cartographer]"); n != 1 {
		t.Errorf("expected exactly 1 [mcp_servers.cartographer], found %d:\n%s", n, got)
	}
	if strings.Contains(got, "[mcp_servers.cartographer.env]") || strings.Contains(got, `FOO = "bar"`) {
		t.Errorf("the orphan's sub-table must be removed with it: %s", got)
	}
	for _, want := range []string{"# my own comment", "# comment introducing the next table", "[mcp_servers.other]"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q not preserved: %s", want, got)
		}
	}
}

func TestApplyCodex_NoOrphan_LeavesFileUntouchedOutsideTheBlock(t *testing.T) {
	dir := t.TempDir()
	preexisting := "# my own comment\nmodel = \"gpt-5.3-codex\"\n\n[mcp_servers.other]\nurl = \"https://example.com/mcp\"\n"
	writeCodexConfig(t, dir, preexisting)

	got, warnings := applyCodexEntry(t, dir)

	if !strings.HasPrefix(got, preexisting) {
		t.Errorf("without orphans the block must simply be appended, file was rewritten:\n%s", got)
	}
	if len(warnings) != 0 {
		t.Errorf("no orphan, no warning expected, got %v", warnings)
	}
}

func TestApplyCodex_DoesNotTouchAnotherManagedBlock(t *testing.T) {
	dir := t.TempDir()
	// A KB-sourced MCP server owns its own block (provisioning's per-name
	// markers): it is not an orphan, even though it too is an mcp_servers table.
	writeCodexConfig(t, dir, `model = "gpt-5.6"

# cartographer:mcp:cartographer:begin
[mcp_servers.cartographer]
url = "http://elsewhere:9090/mcp"
# cartographer:mcp:cartographer:end
`)

	got, _ := applyCodexEntry(t, dir)

	if !strings.Contains(got, `url = "http://elsewhere:9090/mcp"`) {
		t.Errorf("a table owned by another managed block must not be adopted: %s", got)
	}
}

func TestApplyCodex_RepairsDuplicatedFile_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	// The shape observed on the broken machine: Codex's normalized copy at the
	// top, the managed block (markers intact this time is not the case — Codex
	// dropped them everywhere) re-appended at the bottom, hook blocks in
	// between.
	writeCodexConfig(t, dir, `model = "gpt-5.6-terra"

[features]
hooks = true

[hooks.state."/Users/me/.codex/config.toml:session_start:0:0"]
trusted_hash = "sha256:abc"

[[hooks.SessionStart]]
[[hooks.SessionStart.hooks]]
type = "command"
command = "/Users/me/.codex/hooks/cartographer-bootstrap/bootstrap.sh"

[mcp_servers.openaiDeveloperDocs]
url = "https://developers.openai.com/mcp"

[mcp_servers.cartographer]
bearer_token_env_var = "CARTOGRAPHER_TOKENS"
url = "http://localhost:8080/mcp"

[projects."/Users/me"]
trust_level = "trusted"

[mcp_servers.cartographer]
url = "http://localhost:8080/mcp"
bearer_token_env_var = "CARTOGRAPHER_TOKENS"
`)

	got, _ := applyCodexEntry(t, dir)

	if n := strings.Count(got, "[mcp_servers.cartographer]"); n != 1 {
		t.Errorf("expected exactly 1 [mcp_servers.cartographer], found %d:\n%s", n, got)
	}
	for _, want := range []string{
		`model = "gpt-5.6-terra"`,
		"[features]",
		`[hooks.state."/Users/me/.codex/config.toml:session_start:0:0"]`,
		"[[hooks.SessionStart]]",
		`command = "/Users/me/.codex/hooks/cartographer-bootstrap/bootstrap.sh"`,
		"[mcp_servers.openaiDeveloperDocs]",
		`[projects."/Users/me"]`,
		`trust_level = "trusted"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unrelated content %q lost:\n%s", want, got)
		}
	}

	// Re-applying over the repaired file must be a no-op for the duplicate count.
	got2, warnings := applyCodexEntry(t, dir)
	if n := strings.Count(got2, "[mcp_servers.cartographer]"); n != 1 {
		t.Errorf("re-apply duplicated the entry again:\n%s", got2)
	}
	if len(warnings) != 0 {
		t.Errorf("nothing left to adopt on re-apply, got warnings %v", warnings)
	}
}
