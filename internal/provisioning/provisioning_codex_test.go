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

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true,
		KBRoots:   map[string]string{"kb": kbRoot},
		Provider:  configurator.ProviderCodex,
		BaseDir:   baseDir,
		Lock:      provisioning.Lock{},
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

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	opts := provisioning.ApplyOptions{
		AutoTrust: true,
		KBRoots:   map[string]string{"kb": kbRoot},
		Provider:  configurator.ProviderCodex,
		BaseDir:   baseDir,
		Lock:      provisioning.Lock{},
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
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	baseDir := t.TempDir()

	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "https://mcp.example.test/mcp"}
	r, err := configurator.Emit(cfg, configurator.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, baseDir, false); err != nil {
		t.Fatalf("configurator.Apply: %v", err)
	}

	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true,
		KBRoots:   map[string]string{"kb": kbRoot},
		Provider:  configurator.ProviderCodex,
		BaseDir:   baseDir,
		Lock:      provisioning.Lock{},
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
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	baseDir := t.TempDir()

	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true,
		KBRoots:   map[string]string{"kb": kbRoot},
		Provider:  configurator.ProviderCodex,
		BaseDir:   baseDir,
		Lock:      provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply (materialize): %v", err)
	}

	if err := os.RemoveAll(filepath.Join(kbRoot, "hooks", "notify")); err != nil {
		t.Fatal(err)
	}
	m2, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest (2): %v", err)
	}

	res2, err := provisioning.Apply(m2, provisioning.ApplyOptions{
		AutoTrust: true,
		KBRoots:   map[string]string{"kb": kbRoot},
		Provider:  configurator.ProviderCodex,
		BaseDir:   baseDir,
		Lock:      res.NewLock,
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
		"url = \"https://mcp.example.test/mcp\"\n" +
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

// --- Adoption of the registrations Codex leaves orphaned when it rewrites config.toml (D99) ---

// stripCodexComments simulates Codex CLI re-serializing config.toml when it
// persists its own settings: the tables survive, every comment line — the
// Cartographer markers included — is dropped.
func stripCodexComments(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestApply_Codex_Hook_ReApplyAfterCodexRewrite_NoDuplicate(t *testing.T) {
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")
	writeCodexHookKB(t, kbRoot, "other", "SessionStart", "", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	opts := provisioning.ApplyOptions{
		AutoTrust: true,
		KBRoots:   map[string]string{"kb": kbRoot},
		Provider:  configurator.ProviderCodex,
		BaseDir:   baseDir,
		Lock:      provisioning.Lock{},
	}
	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply (1): %v", err)
	}

	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	stripCodexComments(t, configPath)

	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply (2): %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, header := range []string{"[[hooks.PostToolUse]]", "[[hooks.SessionStart]]"} {
		if n := strings.Count(got, header); n != 1 {
			t.Errorf("expected 1 occurrence of %s after Codex's rewrite, found %d:\n%s", header, n, got)
		}
	}
	for _, name := range []string{"notify", "other"} {
		marker := "# cartographer:hook:" + name + ":begin"
		if !strings.Contains(got, marker) {
			t.Errorf("missing %s: %s", marker, got)
		}
		if n := strings.Count(got, filepath.Join(".codex", "hooks", name, "notify.sh")); n != 1 {
			t.Errorf("hook %q registered %d times:\n%s", name, n, got)
		}
	}
	if len(res.Warnings) != 2 {
		t.Errorf("expected one repair warning per hook, got %v", res.Warnings)
	}
}

func TestApply_Codex_Hook_Adoption_PreservesUserHookAndState(t *testing.T) {
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	preexisting := `model = "gpt-5.6"

[hooks.state."/Users/me/.codex/config.toml:post_tool_use:0:0"]
trusted_hash = "sha256:abc"

[[hooks.PostToolUse]]
matcher = "Bash"
[[hooks.PostToolUse.hooks]]
type = "command"
command = "/Users/me/scripts/mine.sh"
`
	if err := os.WriteFile(configPath, []byte(preexisting), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := provisioning.ApplyOptions{
		AutoTrust: true,
		KBRoots:   map[string]string{"kb": kbRoot},
		Provider:  configurator.ProviderCodex,
		BaseDir:   baseDir,
		Lock:      provisioning.Lock{},
	}
	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply (1): %v", err)
	}
	stripCodexComments(t, configPath)
	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply (2): %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if n := strings.Count(got, "[[hooks.PostToolUse]]"); n != 2 {
		t.Errorf("expected the user's registration plus ours, found %d:\n%s", n, got)
	}
	for _, want := range []string{
		`command = "/Users/me/scripts/mine.sh"`,
		`matcher = "Bash"`,
		`[hooks.state."/Users/me/.codex/config.toml:post_tool_use:0:0"]`,
		`trusted_hash = "sha256:abc"`,
		`model = "gpt-5.6"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q must not be touched:\n%s", want, got)
		}
	}
}

// injectBeforeEndMarker inserts text into the config.toml at path immediately
// before endMarker's line — simulating Codex persisting its own bookkeeping
// (e.g. [hooks.state."…"]) positionally after the last table it finds in the
// file, which lands inside a Cartographer-managed block once one has been
// written (D126).
func injectBeforeEndMarker(t *testing.T, path, endMarker, text string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	idx := strings.Index(content, endMarker)
	if idx < 0 {
		t.Fatalf("end marker %q not found in:\n%s", endMarker, content)
	}
	if err := os.WriteFile(path, []byte(content[:idx]+text+content[idx:]), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestApply_Codex_Hook_Eviction_PreservesStateWrittenInsideBlock(t *testing.T) {
	// D126: Codex records its per-hook trusted-hash bookkeeping
	// ([hooks.state."…"]) positionally after the last [[hooks.*]] table it
	// finds in the file — which, once the hook is registered, is the one
	// inside our own managed block. Re-registering the hook must relocate
	// that table out of the block instead of destroying it with the rewrite.
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "notify", "PostToolUse", "concept_write", "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	opts := provisioning.ApplyOptions{
		AutoTrust: true,
		KBRoots:   map[string]string{"kb": kbRoot},
		Provider:  configurator.ProviderCodex,
		BaseDir:   baseDir,
		Lock:      provisioning.Lock{},
	}
	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply (1): %v", err)
	}

	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	stateTable := "\n[hooks.state.\"/Users/me/.codex/config.toml:post_tool_use:0:0\"]\ntrusted_hash = \"sha256:6a78\"\n"
	injectBeforeEndMarker(t, configPath, "# cartographer:hook:notify:end", stateTable)

	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply (2): %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `[hooks.state."/Users/me/.codex/config.toml:post_tool_use:0:0"]`) {
		t.Fatalf("Codex's trusted-hash state table was lost:\n%s", got)
	}
	if !strings.Contains(got, `trusted_hash = "sha256:6a78"`) {
		t.Errorf("state table's own content lost:\n%s", got)
	}

	stateIdx := strings.Index(got, `[hooks.state."`)
	beginIdx := strings.Index(got, "# cartographer:hook:notify:begin")
	endIdx := strings.Index(got, "# cartographer:hook:notify:end")
	if stateIdx < 0 || beginIdx < 0 || endIdx < 0 {
		t.Fatalf("missing markers in:\n%s", got)
	}
	if !(stateIdx < beginIdx) {
		t.Errorf("the state table must now sit outside (before) the managed block, got:\n%s", got)
	}
	if strings.Contains(got[beginIdx:endIdx], "hooks.state") {
		t.Errorf("the state table must not remain inside the managed block:\n%s", got)
	}

	var relocationWarned bool
	for _, w := range res.Warnings {
		if strings.Contains(w, "moved") && strings.Contains(w, "notify") {
			relocationWarned = true
		}
	}
	if !relocationWarned {
		t.Errorf("expected a warning about the relocated table, got %v", res.Warnings)
	}

	// A second apply over the repaired file must be a no-op: the state table
	// is already outside every managed span.
	res2, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply (3): %v", err)
	}
	data2, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data2) != got {
		t.Errorf("re-apply over the repaired file must be a no-op:\nbefore:\n%s\nafter:\n%s", got, data2)
	}
	if len(res2.Warnings) != 0 {
		t.Errorf("nothing left to relocate on re-apply, got warnings %v", res2.Warnings)
	}
}

// --- Adoption of hooks whose command is a self-contained inline one-liner (D127) ---

// rewriteCodexInlineCommandsAsMultilineLiteral simulates the shape a real
// Codex CLI rewrite leaves behind (D127): comments — our markers included —
// are dropped, like any Codex rewrite (see stripCodexComments), and every
// `command = "…"` line we wrote as a basic string is re-serialized as a
// multi-line literal string (opened and closed with three single quotes) on
// the same physical line — the quoting subtlety the adoption match must see
// through.
func rewriteCodexInlineCommandsAsMultilineLiteral(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, `command = "`) {
			value, ok := configurator.CodexTableStringValue(trimmed+"\n", "command")
			if !ok {
				t.Fatalf("could not decode command line: %q", line)
			}
			line = "command = '''" + value + "'''"
		}
		kept = append(kept, line)
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestApply_Codex_Hook_ReApplyAfterCodexRewrite_InlineCommand_NoDuplicate(t *testing.T) {
	// The class of hook D99's marker alone could not recognize: env-block and
	// sops-warn-style hooks whose command is a self-contained "jq ..."
	// one-liner, with no path fragment into the hook's own materialized
	// directory. session-init (a script command, same shape as the real
	// cartographer-bootstrap hook) is registered alongside them to confirm
	// the legacy path-fragment match still adopts it too, in the same
	// rewritten file.
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "env-block", "PreToolUse", "Edit|Write",
		`jq -e '.tool_input.file_path | test("\.env$")' >/dev/null 2>&1 && exit 2 || true`)
	writeCodexHookKB(t, kbRoot, "sops-warn", "PreToolUse", "Bash",
		`jq -e '.tool_input.command | test("sops ")' >/dev/null 2>&1 && echo warn`)
	writeCodexHookKB(t, kbRoot, "session-init", "SessionStart", "", "./notify.sh")

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	opts := provisioning.ApplyOptions{
		AutoTrust: true,
		KBRoots:   map[string]string{"kb": kbRoot},
		Provider:  configurator.ProviderCodex,
		BaseDir:   baseDir,
		Lock:      provisioning.Lock{},
	}
	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply (1): %v", err)
	}

	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	rewriteCodexInlineCommandsAsMultilineLiteral(t, configPath)

	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply (2): %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if n := strings.Count(got, "[[hooks.PreToolUse]]"); n != 2 {
		t.Errorf("expected exactly 2 [[hooks.PreToolUse]] (env-block + sops-warn, no duplicate), found %d:\n%s", n, got)
	}
	if n := strings.Count(got, "[[hooks.SessionStart]]"); n != 1 {
		t.Errorf("expected exactly 1 [[hooks.SessionStart]], found %d:\n%s", n, got)
	}
	if len(res.Warnings) != 3 {
		t.Errorf("expected one adoption warning per hook (3), got %v", res.Warnings)
	}
}

func TestApply_Codex_Hook_Adoption_UserAuthoredInlineCommand_NotAdopted(t *testing.T) {
	// A user-authored [[hooks.PreToolUse]] registration with a different
	// inline command must survive: neither the legacy path-fragment marker
	// nor the decoded-command match applies to it (D127).
	kbRoot := t.TempDir()
	writeCodexHookKB(t, kbRoot, "env-block", "PreToolUse", "Edit|Write",
		`jq -e '.tool_input.file_path | test("\.env$")' >/dev/null 2>&1 && exit 2 || true`)

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	configPath := filepath.Join(baseDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	preexisting := "[[hooks.PreToolUse]]\nmatcher = \"Bash\"\n[[hooks.PreToolUse.hooks]]\ntype = \"command\"\ncommand = \"echo not ours\"\n"
	if err := os.WriteFile(configPath, []byte(preexisting), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := provisioning.ApplyOptions{
		AutoTrust: true,
		KBRoots:   map[string]string{"kb": kbRoot},
		Provider:  configurator.ProviderCodex,
		BaseDir:   baseDir,
		Lock:      provisioning.Lock{},
	}
	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `command = "echo not ours"`) {
		t.Errorf("user-authored registration with a different command must not be adopted:\n%s", got)
	}
	if n := strings.Count(got, "[[hooks.PreToolUse]]"); n != 2 {
		t.Errorf("expected 2 [[hooks.PreToolUse]] (user's + ours), found %d:\n%s", n, got)
	}
}

// assertCodexAgent checks a translated Codex agent: the TOML header verbatim,
// then developer_instructions holding the body plus exactly one provenance
// block (D138).
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
