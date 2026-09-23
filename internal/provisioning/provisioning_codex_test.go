package provisioning_test

// Tests for the real Codex integration (D58): agent translated into Codex's
// TOML subagent schema, hook materialized and registered in .codex/hooks.json
// (D230). See provisioning_agent_hook_test.go for the
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
		AutoTrust: true,
		Provider:  configurator.ProviderCodex,
		BaseDir:   baseDir,
		Lock:      provisioning.Lock{},
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

	// The provenance block (D138) travels inside developer_instructions.
	assertCodexAgent(t, string(data), "name = \"reviewer\"\ndescription = \"Reviews the code\"\n", "Reviewer system prompt.\n")
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
		AutoTrust: true,
		Provider:  configurator.ProviderCodex,
		BaseDir:   baseDir,
		Lock:      provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	agentPath := filepath.Join(baseDir, ".codex", "agents", "plain.toml")
	data, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("agent not materialized at %s: %v", agentPath, err)
	}
	assertCodexAgent(t, string(data), "name = \"plain\"\ndescription = \"plain\"\n", "Body only, no frontmatter.\n")
}

// --- Hook → registration in hooks.json (D230) ---

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

// applyCodexHookKB builds the manifest of kbRoot and applies it for Codex.
func applyCodexHookKB(t *testing.T, kbRoot, baseDir string, lock provisioning.Lock) provisioning.AppliedResult {
	t.Helper()
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true,
		KBRoots:   map[string]string{"kb": kbRoot},
		Provider:  configurator.ProviderCodex,
		BaseDir:   baseDir,
		Lock:      lock,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return res
}

// codexHookCommands returns every command registered for event in
// <baseDir>/.codex/hooks.json, with its group's matcher.
func codexHookCommands(t *testing.T, baseDir, event string) (commands, matchers []string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(baseDir, ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("hooks.json: %v", err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("hooks.json is not the Codex shape: %v\n%s", err, data)
	}
	for _, group := range doc.Hooks[event] {
		for _, h := range group.Hooks {
			if h.Type != "command" {
				t.Errorf("hook type = %q, want command", h.Type)
			}
			commands = append(commands, h.Command)
			matchers = append(matchers, group.Matcher)
		}
	}
	return commands, matchers
}

func TestApply_Codex_Hook_RegistersInHooksJSON(t *testing.T) {
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")
	baseDir := t.TempDir()

	for i := 0; i < 3; i++ {
		applyCodexHookKB(t, kbRoot, baseDir, provisioning.Lock{})
	}

	for _, rel := range []string{"hook.json", "notify.sh"} {
		if _, err := os.Stat(filepath.Join(baseDir, ".codex", "hooks", "notify", rel)); err != nil {
			t.Errorf("%s not materialized: %v", rel, err)
		}
	}
	commands, matchers := codexHookCommands(t, baseDir, "PostToolUse")
	wantCmd := filepath.Join(baseDir, ".codex", "hooks", "notify", "notify.sh")
	if len(commands) != 1 || commands[0] != wantCmd || matchers[0] != "concept_write" {
		t.Errorf("after 3 applies want exactly [%q] with matcher concept_write, got %q %q", wantCmd, commands, matchers)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Errorf("a hook must no longer touch config.toml (D230): %v", err)
	}
}

// TestApply_Codex_Hook_PreservesForeignEntries is the acceptance case of #327:
// another integration's hook in hooks.json (Herdr registers its SessionStart
// hook there) survives registration, re-application and prune.
func TestApply_Codex_Hook_PreservesForeignEntries(t *testing.T) {
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "SessionStart", "", "./notify.sh")
	baseDir := t.TempDir()
	hooksPath := filepath.Join(baseDir, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := `{"description":"herdr","hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"herdr agent-state","timeout":5}]}]}}`
	if err := os.WriteFile(hooksPath, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	res := applyCodexHookKB(t, kbRoot, baseDir, provisioning.Lock{})
	applyCodexHookKB(t, kbRoot, baseDir, res.NewLock)
	commands, _ := codexHookCommands(t, baseDir, "SessionStart")
	if len(commands) != 2 || commands[0] != "herdr agent-state" {
		t.Fatalf("want herdr's hook then ours, got %q", commands)
	}

	if _, err := provisioning.PruneManaged(res.NewLock.Managed, baseDir, false); err != nil {
		t.Fatalf("PruneManaged: %v", err)
	}
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("hooks.json with a foreign entry must survive prune: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	var want map[string]interface{}
	if err := json.Unmarshal([]byte(foreign), &want); err != nil {
		t.Fatal(err)
	}
	if gotJSON, _ := json.Marshal(got); string(gotJSON) != mustMarshal(t, want) {
		t.Errorf("foreign content altered by prune:\ngot  %s\nwant %s", gotJSON, mustMarshal(t, want))
	}
}

func mustMarshal(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApply_Codex_Hook_Removed_EmptiesHooksJSON(t *testing.T) {
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")
	baseDir := t.TempDir()
	res := applyCodexHookKB(t, kbRoot, baseDir, provisioning.Lock{})

	if err := os.RemoveAll(filepath.Join(kbRoot, "hooks", "notify")); err != nil {
		t.Fatal(err)
	}
	res2 := applyCodexHookKB(t, kbRoot, baseDir, res.NewLock)
	if len(res2.Pruned) == 0 {
		t.Fatalf("Apply (removal): expected Pruned not empty")
	}
	// Nothing but our entry was in it, so nothing is left: no `{}` residue.
	if _, err := os.Stat(filepath.Join(baseDir, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Errorf("an emptied hooks.json must be removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".codex", "hooks", "notify", "hook.json")); !os.IsNotExist(err) {
		t.Error("hook.json not removed")
	}
}

// --- Migration of a D58 config.toml registration (D230) ---

// legacyCodexConfig is a config.toml as a pre-D230 client left it after Codex
// rewrote it once: the MCP block, the hook's managed block, a marker-less copy
// of that registration outside it (D99), a user's own hook and Codex's trust
// bookkeeping.
func legacyCodexConfig(command string) string {
	quoted := configurator.QuoteTOMLString(command)
	return "# cartographer:mcp:begin — block managed by Cartographer, do not edit by hand\n" +
		"[mcp_servers.cartographer]\n" +
		"url = \"https://mcp.example.test/mcp\"\n" +
		"# cartographer:mcp:end\n\n" +
		"# cartographer:hook:notify:begin\n" +
		"[[hooks.PostToolUse]]\n" +
		"matcher = \"concept_write\"\n" +
		"[[hooks.PostToolUse.hooks]]\n" +
		"type = \"command\"\n" +
		"command = " + quoted + "\n" +
		"# cartographer:hook:notify:end\n\n" +
		"[[hooks.PostToolUse]]\n" +
		"matcher = \"concept_write\"\n" +
		"[[hooks.PostToolUse.hooks]]\n" +
		"type = \"command\"\n" +
		"command = " + quoted + "\n\n" +
		"[[hooks.Stop]]\n" +
		"[[hooks.Stop.hooks]]\n" +
		"type = \"command\"\n" +
		"command = \"say done\"\n\n" +
		"[hooks.state.\"/home/u/.codex/config.toml:post_tool_use:0:0\"]\n" +
		"trusted_hash = \"abc\"\n"
}

func TestApply_Codex_Hook_MigratesLegacyConfigTOML(t *testing.T) {
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")
	baseDir := t.TempDir()
	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(baseDir, ".codex", "hooks", "notify", "notify.sh")
	if err := os.WriteFile(configPath, []byte(legacyCodexConfig(command)), 0o644); err != nil {
		t.Fatal(err)
	}

	res := applyCodexHookKB(t, kbRoot, baseDir, provisioning.Lock{})

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "hooks.PostToolUse") || strings.Contains(content, "cartographer:hook:notify") {
		t.Errorf("the D58 registration (block and orphan) must be gone from config.toml:\n%s", content)
	}
	for _, keep := range []string{"[mcp_servers.cartographer]", "[[hooks.Stop]]", `command = "say done"`, `trusted_hash = "abc"`} {
		if !strings.Contains(content, keep) {
			t.Errorf("config.toml lost %q:\n%s", keep, content)
		}
	}
	commands, _ := codexHookCommands(t, baseDir, "PostToolUse")
	if len(commands) != 1 || commands[0] != command {
		t.Errorf("hooks.json registrations = %q, want exactly [%q]", commands, command)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "trust it again") {
		t.Errorf("the migration must tell the user Codex re-prompts for trust, warnings: %q", res.Warnings)
	}

	managed, stray, err := provisioning.HookRegistrations(baseDir, configurator.ProviderCodex, "notify")
	if err != nil || managed != 1 || stray != 0 {
		t.Errorf("HookRegistrations after migration = %d managed, %d stray, %v; want 1, 0", managed, stray, err)
	}
}

// An upgrade leaves a KB hook whose content did not change: nothing rewrites
// it, so the D58 registration used to stay in config.toml for good, and
// doctor's suggested `sync` changed nothing (#338). The first sync must
// migrate it, and a status (NoHeal) must report it as divergent until then.
func TestApply_Codex_Hook_UnchangedMigratesOnNextSync(t *testing.T) {
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")
	baseDir := t.TempDir()
	res := applyCodexHookKB(t, kbRoot, baseDir, provisioning.Lock{})

	// What a pre-D230 client left behind: the block, next to the hooks.json
	// entry the current client already wrote.
	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	command := filepath.Join(baseDir, ".codex", "hooks", "notify", "notify.sh")
	if err := os.WriteFile(configPath, []byte(legacyCodexConfig(command)), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := provisioning.VerifyManaged(res.NewLock, configurator.ProviderCodex, baseDir)
	if len(findings) != 1 || findings[0].Name != "notify" || findings[0].Reason != provisioning.DriftUnregistered {
		t.Fatalf("a registration still in config.toml must be unregistered drift, got %+v", findings)
	}

	applyCodexHookKB(t, kbRoot, baseDir, res.NewLock)

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hooks.PostToolUse") || strings.Contains(string(data), "cartographer:hook:notify") {
		t.Errorf("an unchanged hook's D58 registration must be migrated on sync:\n%s", data)
	}
	managed, stray, err := provisioning.HookRegistrations(baseDir, configurator.ProviderCodex, "notify")
	if err != nil || managed != 1 || stray != 0 {
		t.Errorf("HookRegistrations after sync = %d managed, %d stray, %v; want 1, 0", managed, stray, err)
	}
}

// Codex's own rewrite of config.toml can land a table of the user's inside a
// Cartographer hook block. Deleting the block during the migration must not
// delete it (#338).
func TestApply_Codex_Hook_MigrationKeepsForeignTablesInsideTheBlock(t *testing.T) {
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")
	baseDir := t.TempDir()
	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(baseDir, ".codex", "hooks", "notify", "notify.sh")
	legacy := "model = \"gpt-5\"\n\n" +
		"# cartographer:hook:notify:begin\n" +
		"[[hooks.PostToolUse]]\n" +
		"matcher = \"concept_write\"\n" +
		"[[hooks.PostToolUse.hooks]]\n" +
		"type = \"command\"\n" +
		"command = " + configurator.QuoteTOMLString(command) + "\n\n" +
		"[notice]\n" +
		"hide_rate_limit_model_nudge = true\n" +
		"# cartographer:hook:notify:end\n"
	if err := os.WriteFile(configPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	applyCodexHookKB(t, kbRoot, baseDir, provisioning.Lock{})

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "hooks.PostToolUse") || strings.Contains(content, "cartographer:hook:notify") {
		t.Errorf("the hook's registration must be gone:\n%s", content)
	}
	for _, keep := range []string{`model = "gpt-5"`, "[notice]", "hide_rate_limit_model_nudge = true"} {
		if !strings.Contains(content, keep) {
			t.Errorf("config.toml lost %q:\n%s", keep, content)
		}
	}
}

// An inline one-liner carries no path marker: its D58 orphan is recognized by
// its command alone (D127), and a user's identical-looking hook elsewhere with
// a different command is not taken.
func TestApply_Codex_Hook_MigratesLegacyInlineCommand(t *testing.T) {
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "PreToolUse", "", "jq -c . >/dev/null")
	baseDir := t.TempDir()
	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := "[[hooks.PreToolUse]]\n[[hooks.PreToolUse.hooks]]\ntype = \"command\"\ncommand = \"jq -c . >/dev/null\"\n\n" +
		"[[hooks.PreToolUse]]\n[[hooks.PreToolUse.hooks]]\ntype = \"command\"\ncommand = \"jq -r .tool >/dev/null\"\n"
	if err := os.WriteFile(configPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	applyCodexHookKB(t, kbRoot, baseDir, provisioning.Lock{})

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "jq -c .") {
		t.Errorf("the legacy inline registration must be migrated out:\n%s", data)
	}
	if !strings.Contains(string(data), "jq -r .tool") {
		t.Errorf("the user's own inline hook must stay:\n%s", data)
	}
	commands, _ := codexHookCommands(t, baseDir, "PreToolUse")
	if len(commands) != 1 || !strings.HasPrefix(commands[0], "jq -c . >/dev/null # cartographer-hook: .codex/hooks/notify/") {
		t.Errorf("hooks.json registrations = %q, want the inline command carrying its ownership marker", commands)
	}
}

// Prune strips a registration wherever it is: hooks.json, and a D58 block a
// client that never re-synced still has in config.toml.
func TestPruneManaged_Codex_Hook_RemovesBothRepresentations(t *testing.T) {
	baseDir := t.TempDir()
	command := filepath.Join(baseDir, ".codex", "hooks", "notify", "notify.sh")
	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	hookPath := filepath.Join(baseDir, ".codex", "hooks", "notify", "hook.json")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(legacyCodexConfig(command)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath, []byte(`{"event":"PostToolUse"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	hooksJSON := `{"hooks":{"PostToolUse":[{"matcher":"concept_write","hooks":[{"type":"command","command":` + mustMarshal(t, command) + `}]}]}}`
	if err := os.WriteFile(filepath.Join(baseDir, ".codex", "hooks.json"), []byte(hooksJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	managed := []provisioning.ManagedFile{{Kind: "hook", Name: "notify", Path: ".codex/hooks/notify/hook.json", ContentHash: "h"}}
	if _, err := provisioning.PruneManaged(managed, baseDir, false); err != nil {
		t.Fatalf("PruneManaged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Errorf("hooks.json emptied by prune must be removed: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.toml must survive (it holds the MCP block): %v", err)
	}
	if strings.Contains(string(data), "hooks.PostToolUse") {
		t.Errorf("legacy registration left in config.toml:\n%s", data)
	}
	if !strings.Contains(string(data), "[mcp_servers.cartographer]") || !strings.Contains(string(data), "[[hooks.Stop]]") {
		t.Errorf("prune touched what is not ours:\n%s", data)
	}
}

// A registration still in config.toml next to the hooks.json one fires twice:
// doctor reports it as a stray until the next sync migrates it.
func TestHookRegistrations_Codex_LegacyIsStray(t *testing.T) {
	baseDir := t.TempDir()
	command := filepath.Join(baseDir, ".codex", "hooks", "notify", "notify.sh")
	if err := os.MkdirAll(filepath.Join(baseDir, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, ".codex", "config.toml"), []byte(legacyCodexConfig(command)), 0o644); err != nil {
		t.Fatal(err)
	}
	managed, stray, err := provisioning.HookRegistrations(baseDir, configurator.ProviderCodex, "notify")
	if err != nil || managed != 0 || stray != 2 {
		t.Errorf("HookRegistrations = %d managed, %d stray, %v; want 0 managed, 2 stray (block + orphan)", managed, stray, err)
	}
}

func assertCodexAgent(t *testing.T, got, wantHeader, wantBody string) {
	t.Helper()
	const open = "developer_instructions = \"\"\"\n"
	idx := strings.Index(got, open)
	if idx == -1 {
		t.Fatalf("no developer_instructions block:\n%s", got)
	}
	if header := got[:idx]; header != wantHeader {
		t.Errorf("unexpected TOML header:\n%q\nexpected:\n%q", header, wantHeader)
	}
	instructions := strings.TrimSuffix(got[idx+len(open):], "\"\"\"\n")
	assertStampedOnce(t, instructions, wantBody)
}
