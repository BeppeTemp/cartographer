package provisioning_test

// Tests for the real Codex integration (D58): agent translated into Codex's
// TOML subagent schema, hook materialized and registered in the managed block of
// .codex/config.toml. See provisioning_agent_hook_test.go for the
// pre-existing tests (D48) and hooksettings_test.go for the Claude equivalent (D57).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// --- Agent → TOML (D58) ---

func TestApply_Codex_MaterializzaAgent_ConFrontmatter(t *testing.T) {
	baseDir := t.TempDir()
	src := "---\nname: reviewer\ndescription: Reviews the code\ntools: Read, Grep\nmodel: sonnet\n---\nReviewer system prompt.\n"

	a := provisioning.Artifact{
		Kind: "agent", Name: "reviewer", Source: "kb:x", ContentHash: "h1", Signed: true,
		Files: []provisioning.ArtifactFile{{Path: "reviewer.md", Content: []byte(src)}},
	}
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderCodex,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.NeedsApproval) != 0 || len(res.Unsupported) != 0 {
		t.Fatalf("Apply codex agent: expected materialized, NeedsApproval=%v Unsupported=%v", res.NeedsApproval, res.Unsupported)
	}

	agentPath := filepath.Join(baseDir, ".codex", "agents", "reviewer.toml")
	data, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("agent not materialized at %s: %v", agentPath, err)
	}

	want := "name = \"reviewer\"\n" +
		"description = \"Reviews the code\"\n" +
		"developer_instructions = \"\"\"\n" +
		"Reviewer system prompt.\n" +
		"\"\"\"\n"
	if string(data) != want {
		t.Errorf("unexpected translated content:\n%q\nexpected:\n%q", data, want)
	}
	// Non-mappable Claude-only fields must not appear.
	for _, unwanted := range []string{"tools", "model"} {
		if strings.Contains(string(data), unwanted) {
			t.Errorf("the translated TOML must not contain %q: %s", unwanted, data)
		}
	}
}

func TestApply_Codex_MaterializzaAgent_SenzaFrontmatter(t *testing.T) {
	baseDir := t.TempDir()
	src := "Body only, no frontmatter.\n"

	a := provisioning.Artifact{
		Kind: "agent", Name: "plain", Source: "kb:x", ContentHash: "h1", Signed: true,
		Files: []provisioning.ArtifactFile{{Path: "plain.md", Content: []byte(src)}},
	}
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	_, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderCodex,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	agentPath := filepath.Join(baseDir, ".codex", "agents", "plain.toml")
	data, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("agent not materialized at %s: %v", agentPath, err)
	}
	want := "name = \"plain\"\n" +
		"description = \"plain\"\n" +
		"developer_instructions = \"\"\"\n" +
		"Body only, no frontmatter.\n" +
		"\"\"\"\n"
	if string(data) != want {
		t.Errorf("unexpected translated content (fallback with no frontmatter):\n%q\nexpected:\n%q", data, want)
	}
}

// --- Hook → registration in config.toml (D58) ---

func writeCodexHookKB(t *testing.T, kbRoot, name, event, matcher, command string) {
	t.Helper()
	hookDir := filepath.Join(kbRoot, "hooks", name)
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := map[string]string{"event": event, "command": command}
	if matcher != "" {
		spec["matcher"] = matcher
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "hook.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "notify.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestApply_Codex_Hook_RegistraConfigTOML(t *testing.T) {
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderCodex,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Written) == 0 {
		t.Fatalf("Apply: expected Written not empty")
	}

	for _, rel := range []string{"hook.json", "notify.sh"} {
		if _, err := os.Stat(filepath.Join(baseDir, ".codex", "hooks", "notify", rel)); err != nil {
			t.Errorf("%s not materialized: %v", rel, err)
		}
	}

	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.toml not written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[[hooks.PostToolUse]]") {
		t.Errorf("missing [[hooks.PostToolUse]]: %s", content)
	}
	if !strings.Contains(content, `matcher = "concept_write"`) {
		t.Errorf("missing matcher: %s", content)
	}
	wantCmd := filepath.Join(baseDir, ".codex", "hooks", "notify", "notify.sh")
	if !strings.Contains(content, `command = "`+wantCmd+`"`) {
		t.Errorf("missing resolved command %q: %s", wantCmd, content)
	}
	if !strings.Contains(content, "# cartographer:hook:notify:begin") {
		t.Errorf("missing begin marker: %s", content)
	}
}

func TestApply_Codex_Hook_ReApply_NessunDuplicato(t *testing.T) {
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	opts := provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderCodex,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	}
	for i := 0; i < 3; i++ {
		if _, err := provisioning.Apply(m, opts); err != nil {
			t.Fatalf("Apply (%d): %v", i, err)
		}
	}

	data, err := os.ReadFile(filepath.Join(baseDir, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "[[hooks.PostToolUse]]"); n != 1 {
		t.Errorf("expected 1 occurrence of [[hooks.PostToolUse]] after 3 applies, found %d:\n%s", n, data)
	}
}

func TestApply_Codex_Hook_MCPBlock_CoesisteConHook(t *testing.T) {
	// The [mcp_servers.cartographer] block (configurator) and the hook's
	// block (provisioning) live in the same file: neither must
	// erase the other.
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	baseDir := t.TempDir()

	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "http://localhost:8080/mcp"}
	r, err := configurator.Emit(cfg, configurator.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, baseDir, false); err != nil {
		t.Fatalf("configurator.Apply: %v", err)
	}

	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderCodex,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	}); err != nil {
		t.Fatalf("provisioning.Apply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "[mcp_servers.cartographer]") {
		t.Errorf("mcp_servers block missing: %s", content)
	}
	if !strings.Contains(content, "[[hooks.PostToolUse]]") {
		t.Errorf("hook block missing: %s", content)
	}
}

func TestApply_Codex_Hook_Removed_RipulisceConfigTOML(t *testing.T) {
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	baseDir := t.TempDir()

	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderCodex,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply (materialize): %v", err)
	}

	if err := os.RemoveAll(filepath.Join(kbRoot, "hooks", "notify")); err != nil {
		t.Fatal(err)
	}
	m2, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest (2): %v", err)
	}

	res2, err := provisioning.Apply(m2, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderCodex,
		BaseDir:  baseDir,
		Lock:     res.NewLock,
	})
	if err != nil {
		t.Fatalf("Apply (removal): %v", err)
	}
	if len(res2.Pruned) == 0 {
		t.Fatalf("Apply (removal): expected Pruned not empty")
	}

	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	if data, err := os.ReadFile(configPath); err == nil {
		if strings.Contains(string(data), "hooks.PostToolUse") {
			t.Errorf("hook entry not removed from config.toml: %s", data)
		}
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".codex", "hooks", "notify", "hook.json")); !os.IsNotExist(err) {
		t.Error("hook.json not removed")
	}
}

func TestPruneManaged_Codex_Hook_RimuoveEntryConfigTOML(t *testing.T) {
	baseDir := t.TempDir()
	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// The begin marker deliberately carries the LEGACY Italian tail: blocktext
	// must recognize blocks written by older versions via the stable prefix
	// (everything before the em dash), or the block would be duplicated.
	preexisting := "# cartographer:mcp:begin — blocco gestito da Cartographer, non modificare a mano\n" +
		"[mcp_servers.cartographer]\n" +
		"url = \"http://localhost:8080/mcp\"\n" +
		"# cartographer:mcp:end\n\n" +
		"# cartographer:hook:notify:begin\n" +
		"[[hooks.PostToolUse]]\n" +
		"[[hooks.PostToolUse.hooks]]\n" +
		"type = \"command\"\n" +
		"command = \"" + filepath.Join(baseDir, ".codex", "hooks", "notify", "notify.sh") + "\"\n" +
		"# cartographer:hook:notify:end\n"
	if err := os.WriteFile(configPath, []byte(preexisting), 0o644); err != nil {
		t.Fatal(err)
	}

	hookPath := filepath.Join(baseDir, ".codex", "hooks", "notify", "hook.json")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath, []byte(`{"event":"PostToolUse"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	managed := []provisioning.ManagedFile{
		{Kind: "hook", Name: "notify", Path: filepath.Join(".codex", "hooks", "notify", "hook.json"), ContentHash: "h"},
	}
	pruned, err := provisioning.PruneManaged(managed, baseDir, false)
	if err != nil {
		t.Fatalf("PruneManaged: %v", err)
	}
	if len(pruned) != 1 {
		t.Errorf("expected 1 pruned file, got %d", len(pruned))
	}
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Error("hook.json not removed")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.toml must not be removed (mcp_servers residue): %v", err)
	}
	content := string(data)
	if strings.Contains(content, "hooks.PostToolUse") {
		t.Errorf("hook entry not removed: %s", content)
	}
	if !strings.Contains(content, "[mcp_servers.cartographer]") {
		t.Errorf("mcp_servers block must not be touched: %s", content)
	}
}
