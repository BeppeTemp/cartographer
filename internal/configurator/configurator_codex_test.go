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
		URL:         "https://mcp.example.test/mcp",
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
	if !strings.Contains(content, `url = "https://mcp.example.test/mcp"`) {
		t.Errorf("missing url key: %s", content)
	}
	if !strings.Contains(content, `bearer_token_env_var = "CARTOGRAPHER_TOKENS"`) {
		t.Errorf("missing bearer_token_env_var key when auth enabled: %s", content)
	}
}

func TestEmitCodex_TOML_NoAuth(t *testing.T) {
	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "https://mcp.example.test/mcp"}
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

	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "https://mcp.example.test/mcp"}
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
	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "https://mcp.example.test/mcp"}
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
	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "https://mcp.example.test/mcp"}
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
	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "https://mcp.example.test/mcp"}
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
    "cartographer": {"url": "https://mcp.example.test/mcp", "type": "http"},
    "other": {"url": "https://example.com/mcp", "type": "http"}
  }
}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "https://mcp.example.test/mcp"}
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
	legacy := `{"mcpServers": {"cartographer": {"url": "https://mcp.example.test/mcp", "type": "http"}}}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "https://mcp.example.test/mcp"}
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
	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "https://mcp.example.test/mcp"}
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
url = "https://mcp.example.test/mcp"

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
url = "https://mcp.example.test/mcp"

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
url = "https://mcp.example.test/mcp"

[projects."/Users/me"]
trust_level = "trusted"

[mcp_servers.cartographer]
url = "https://mcp.example.test/mcp"
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

// --- EvictForeignTablesFromBlock (D126) ---

const (
	evictTestBegin = "# cartographer:hook:sops-warn:begin"
	evictTestEnd   = "# cartographer:hook:sops-warn:end"
	evictTestBody  = "[[hooks.PreToolUse]]\nmatcher = \"Bash\"\n[[hooks.PreToolUse.hooks]]\ntype = \"command\"\ncommand = \"/Users/me/.codex/hooks/sops-warn/hook.sh\"\n"
)

func TestEvictForeignTablesFromBlock_RelocatesStateWrittenInsideBlock(t *testing.T) {
	dir := t.TempDir()
	path := writeCodexConfig(t, dir, `model = "gpt-5.6"

`+evictTestBegin+`
[[hooks.PreToolUse]]
matcher = "Bash"
[[hooks.PreToolUse.hooks]]
type = "command"
command = "/Users/me/.codex/hooks/sops-warn/hook.sh"

[hooks.state."/Users/me/.codex/config.toml:pre_tool_use:0:0"]
trusted_hash = "sha256:abc123"
`+evictTestEnd+`

[mcp_servers.other]
url = "https://example.com/mcp"
`)

	relocated, err := configurator.EvictForeignTablesFromBlock(path, evictTestBegin, evictTestEnd, evictTestBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The relocated key is dotted, as segments joined (unquoted), same
	// convention AdoptCodexOrphanTables already uses for its own removed keys.
	want := `hooks.state./Users/me/.codex/config.toml:pre_tool_use:0:0`
	if len(relocated) != 1 || relocated[0] != want {
		t.Fatalf("relocated = %v, want [%s]", relocated, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)

	stateIdx := strings.Index(got, `[hooks.state."`)
	beginIdx := strings.Index(got, evictTestBegin)
	endIdx := strings.Index(got, evictTestEnd)
	if stateIdx < 0 || beginIdx < 0 || endIdx < 0 {
		t.Fatalf("missing markers in:\n%s", got)
	}
	if !(stateIdx < beginIdx) {
		t.Errorf("the state table must now sit before the begin marker, got:\n%s", got)
	}
	if strings.Contains(got[beginIdx:endIdx], "hooks.state") {
		t.Errorf("the state table must not remain inside the block, got:\n%s", got)
	}
	if !strings.Contains(got, `trusted_hash = "sha256:abc123"`) {
		t.Errorf("trusted_hash value lost: %s", got)
	}
	if !strings.Contains(got, `[mcp_servers.other]`) {
		t.Errorf("unrelated content lost: %s", got)
	}
	if strings.Count(got, `[hooks.state."/Users/me/.codex/config.toml:pre_tool_use:0:0"]`) != 1 {
		t.Errorf("state table duplicated: %s", got)
	}
}

func TestEvictForeignTablesFromBlock_NoForeign_NoOp(t *testing.T) {
	dir := t.TempDir()
	content := evictTestBegin + "\n" + evictTestBody + evictTestEnd + "\n"
	path := writeCodexConfig(t, dir, content)

	relocated, err := configurator.EvictForeignTablesFromBlock(path, evictTestBegin, evictTestEnd, evictTestBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if relocated != nil {
		t.Errorf("relocated = %v, want nil", relocated)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file must be byte-identical when nothing is foreign, got:\n%s", data)
	}
}

func TestEvictForeignTablesFromBlock_MultipleForeign_OrderPreserved_NoBlankLineAccumulation(t *testing.T) {
	dir := t.TempDir()
	path := writeCodexConfig(t, dir, evictTestBegin+`
[[hooks.PreToolUse]]
matcher = "Bash"
[[hooks.PreToolUse.hooks]]
type = "command"
command = "/Users/me/.codex/hooks/sops-warn/hook.sh"

[hooks.state."a"]
trusted_hash = "sha256:aaa"

[hooks.state."b"]
trusted_hash = "sha256:bbb"
`+evictTestEnd+`
`)

	relocated, err := configurator.EvictForeignTablesFromBlock(path, evictTestBegin, evictTestEnd, evictTestBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantA, wantB := `hooks.state.a`, `hooks.state.b`
	if len(relocated) != 2 || relocated[0] != wantA || relocated[1] != wantB {
		t.Fatalf("relocated = %v, want [%s %s] in order", relocated, wantA, wantB)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)

	aIdx := strings.Index(got, `[hooks.state."a"]`)
	bIdx := strings.Index(got, `[hooks.state."b"]`)
	beginIdx := strings.Index(got, evictTestBegin)
	if aIdx < 0 || bIdx < 0 || beginIdx < 0 || !(aIdx < bIdx && bIdx < beginIdx) {
		t.Fatalf("expected [a], then [b], then the begin marker, got:\n%s", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("blank lines accumulated:\n%s", got)
	}
}

func TestEvictForeignTablesFromBlock_KeyDeclaredInBody_NotRelocated(t *testing.T) {
	dir := t.TempDir()
	path := writeCodexConfig(t, dir, evictTestBegin+`
[[hooks.PreToolUse]]
matcher = "Bash"
[[hooks.PreToolUse.hooks]]
type = "command"
command = "/Users/me/.codex/hooks/sops-warn/hook.sh"

[[hooks.PreToolUse]]
matcher = "Write"
[[hooks.PreToolUse.hooks]]
type = "command"
command = "/Users/me/.codex/hooks/sops-warn/other.sh"
`+evictTestEnd+`
`)

	relocated, err := configurator.EvictForeignTablesFromBlock(path, evictTestBegin, evictTestEnd, evictTestBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if relocated != nil {
		t.Errorf("relocated = %v, want nil: a key declared in newBody must never be relocated, even a second copy of it", relocated)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Index(got, `command = "/Users/me/.codex/hooks/sops-warn/other.sh"`) < strings.Index(got, evictTestBegin) {
		t.Errorf("the second copy must stay inside the block, not before it:\n%s", got)
	}
	if n := strings.Count(got, `command = "/Users/me/.codex/hooks/sops-warn/other.sh"`); n != 1 {
		t.Errorf("second copy must survive untouched, found %d occurrences:\n%s", n, got)
	}
}

func TestEvictForeignTablesFromBlock_BlockAbsent_NoOp(t *testing.T) {
	dir := t.TempDir()
	content := "model = \"gpt-5.6\"\n"
	path := writeCodexConfig(t, dir, content)

	relocated, err := configurator.EvictForeignTablesFromBlock(path, evictTestBegin, evictTestEnd, evictTestBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if relocated != nil {
		t.Errorf("relocated = %v, want nil when the block is absent", relocated)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file must be untouched when the block is absent, got:\n%s", data)
	}
}

func TestEvictForeignTablesFromBlock_MissingFile_NoOp(t *testing.T) {
	relocated, err := configurator.EvictForeignTablesFromBlock(filepath.Join(t.TempDir(), ".codex", "config.toml"), evictTestBegin, evictTestEnd, evictTestBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if relocated != nil {
		t.Errorf("relocated = %v, want nil when the file does not exist", relocated)
	}
}

// --- CodexTableStringValue (D127): decoding a command's value regardless of
// the TOML string form Codex re-serializes it as ---

func TestCodexTableStringValue_BasicString_RoundTripsQuoteTOMLString(t *testing.T) {
	value := "jq -e '.tool_input.file_path | test(\"\\.env$\")' >/dev/null 2>&1 && exit 2 || true"
	body := "[[hooks.PreToolUse.hooks]]\ntype = \"command\"\ncommand = " + configurator.QuoteTOMLString(value) + "\n"

	got, ok := configurator.CodexTableStringValue(body, "command")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if got != value {
		t.Errorf("got %q, want %q", got, value)
	}
}

func TestCodexTableStringValue_LiteralString(t *testing.T) {
	body := "[[hooks.PreToolUse.hooks]]\ncommand = 'jq -e \"literal, no escapes\\here\"'\n"

	got, ok := configurator.CodexTableStringValue(body, "command")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	want := `jq -e "literal, no escapes\here"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCodexTableStringValue_MultilineLiteralString_MatchesBasicFormOfSameValue(t *testing.T) {
	// The shape observed from Codex CLI: it re-serializes a command we wrote as
	// a basic string ("…") into a multi-line literal string ('''…''') on the
	// same physical line — the two spellings must decode to the same value.
	value := `jq -e '.tool_input.file_path | test("\\.env$")' >/dev/null 2>&1 && echo blocked`
	basicBody := "command = " + configurator.QuoteTOMLString(value) + "\n"
	multilineBody := "command = '''" + value + "'''\n"

	basicGot, ok := configurator.CodexTableStringValue(basicBody, "command")
	if !ok {
		t.Fatalf("basic form: expected ok=true")
	}
	multilineGot, ok := configurator.CodexTableStringValue(multilineBody, "command")
	if !ok {
		t.Fatalf("multi-line literal form: expected ok=true")
	}
	if basicGot != multilineGot {
		t.Errorf("the two TOML spellings decoded to different values: %q vs %q", basicGot, multilineGot)
	}
	if multilineGot != value {
		t.Errorf("multi-line literal decoded to %q, want %q", multilineGot, value)
	}
}

func TestCodexTableStringValue_MultilineLiteralString_OwnLine_DropsLeadingNewline(t *testing.T) {
	body := "command = '''\njq -e 'x'\n'''\n"

	got, ok := configurator.CodexTableStringValue(body, "command")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	want := "jq -e 'x'\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCodexTableStringValue_MultilineBasicString(t *testing.T) {
	// Manually escaped, TOML multi-line basic form of value: a lone quote
	// needs no escaping in this form (unlike single-line basic), only the
	// backslash does.
	value := "line one\nline two \"quoted\" and \\backslash"
	body := "developer_instructions = \"\"\"" + strings.ReplaceAll(value, `\`, `\\`) + "\"\"\"\n"

	got, ok := configurator.CodexTableStringValue(body, "developer_instructions")
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if got != value {
		t.Errorf("got %q, want %q", got, value)
	}
}

func TestCodexTableStringValue_NonStringValue_ReturnsFalse(t *testing.T) {
	if _, ok := configurator.CodexTableStringValue("trust_level = true\n", "trust_level"); ok {
		t.Errorf("expected ok=false for a bool value")
	}
	if _, ok := configurator.CodexTableStringValue("count = 42\n", "count"); ok {
		t.Errorf("expected ok=false for a number value")
	}
	if _, ok := configurator.CodexTableStringValue("names = [\"a\", \"b\"]\n", "names"); ok {
		t.Errorf("expected ok=false for an array value")
	}
}

func TestCodexTableStringValue_MissingKey_ReturnsFalse(t *testing.T) {
	body := "[[hooks.PreToolUse.hooks]]\ntype = \"command\"\n"
	if _, ok := configurator.CodexTableStringValue(body, "command"); ok {
		t.Errorf("expected ok=false when the key is absent")
	}
}
