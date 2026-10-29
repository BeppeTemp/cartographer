package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// D737: the global MCP entry of a provider with a workspace binding is the
// local stdio bridge; without a binding it is the HTTP URL, unchanged.

func stubBridgeCommand(t *testing.T) {
	t.Helper()
	old := bridgeCommand
	bridgeCommand = func() (string, error) { return "/usr/local/bin/cartographer", nil }
	t.Cleanup(func() { bridgeCommand = old })
}

func bridgeConfigFor(t *testing.T, providers ...string) *clientconfig.Config {
	t.Helper()
	cfg := &clientconfig.Config{
		ServerName: "cartographer", ServerURL: "https://example.com/mcp", ServerRoutedPath: "/mcp/routed",
		Auth: true, Token: "${CARTOGRAPHER_TOKEN}", KnownKBs: []string{"kb-a", "kb-b"}, Agents: providers,
	}
	return cfg
}

func TestEntriesSwitchBetweenHTTPAndBridge(t *testing.T) {
	cfg := bridgeConfigFor(t, "claude", "codex")
	ws := t.TempDir()
	if err := cfg.BindWorkspace("claude", ws, []string{"kb-a"}); err != nil {
		t.Fatal(err)
	}
	got, err := entriesByProviderForKBs(cfg, []string{"claude", "codex"}, "cartographer", cfg.ServerURL, cfg.KnownKBs, "/mcp/routed")
	if err != nil {
		t.Fatal(err)
	}
	if e := got["claude"]; len(e) != 1 || !e[0].Bridge || e[0].Name != "cartographer" {
		t.Fatalf("claude (bound) = %+v, want the bridge", e)
	}
	if e := got["codex"]; len(e) != 1 || e[0].Bridge || e[0].URL != "https://example.com/mcp/routed?provisioned=1" {
		t.Fatalf("codex (unbound) = %+v, want the unchanged HTTP entry", e)
	}

	// An empty binding is still a binding: the bridge serves no KB there.
	if err := cfg.BindWorkspace("claude", ws, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = entriesByProviderForKBs(cfg, []string{"claude"}, "cartographer", cfg.ServerURL, cfg.KnownKBs, "/mcp/routed")
	if e := got["claude"]; len(e) != 1 || !e[0].Bridge {
		t.Fatalf("--no-kb binding = %+v", e)
	}

	// Unbinding the last workspace returns to HTTP.
	if err := cfg.UnbindWorkspace("claude", ws); err != nil {
		t.Fatal(err)
	}
	got, _ = entriesByProviderForKBs(cfg, []string{"claude"}, "cartographer", cfg.ServerURL, cfg.KnownKBs, "/mcp/routed")
	if e := got["claude"]; len(e) != 1 || e[0].Bridge || e[0].URL == "" {
		t.Fatalf("after unbind = %+v", e)
	}

	// A server without the routed endpoint cannot be bridged to.
	if err := cfg.BindWorkspace("claude", ws, []string{"kb-a"}); err != nil {
		t.Fatal(err)
	}
	got, _ = entriesByProviderForKBs(cfg, []string{"claude"}, "cartographer", cfg.ServerURL, cfg.KnownKBs, "")
	if e := got["claude"]; len(e) == 0 || e[0].Bridge {
		t.Fatalf("no routed path = %+v, want the HTTP shape", e)
	}
}

func TestApplyMCPEntriesWritesStdioBridgePerProvider(t *testing.T) {
	stubBridgeCommand(t)
	providers := []string{"claude", "codex", "kiro", "opencode", "antigravity", "crush", "copilot"}
	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			dir := t.TempDir()
			entries := map[string][]mcpEntry{provider: {{Name: "cartographer", Bridge: true}}}
			if _, _, err := applyMCPEntries(entries, []string{provider}, dir, nil, true, "CARTOGRAPHER_TOKEN", false); err != nil {
				t.Fatal(err)
			}
			r, err := configurator.EmitServer("placeholder", configurator.ServerSpec{Type: "stdio", Command: "x"}, configurator.Provider(provider))
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(dir, r.FilePath))
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, want := range []string{"/usr/local/bin/cartographer", "mcp", "--provider", provider} {
				if !strings.Contains(text, want) {
					t.Errorf("%s config misses %q:\n%s", provider, want, text)
				}
			}
			if strings.Contains(text, "https://example.com") || strings.Contains(strings.ToLower(text), "bearer") {
				t.Errorf("%s config still carries the HTTP entry:\n%s", provider, text)
			}
			// Codex filters the environment of an MCP server: the token variable
			// is named so the bridge can read it. No other client needs it.
			if got := strings.Contains(text, "env_vars"); got != (provider == "codex") {
				t.Errorf("%s: env_vars present = %v", provider, got)
			}
			if strings.Contains(text, "CARTOGRAPHER_TOKEN") != (provider == "codex") {
				t.Errorf("%s: token variable name mention is wrong:\n%s", provider, text)
			}
		})
	}
}

// Replacing an HTTP entry by the bridge must leave no stale url/headers behind.
func TestBridgeEntryReplacesTheHTTPOne(t *testing.T) {
	stubBridgeCommand(t)
	dir := t.TempDir()
	http := map[string][]mcpEntry{"claude": {{Name: "cartographer", URL: "https://example.com/mcp/routed"}}}
	if _, _, err := applyMCPEntries(http, []string{"claude"}, dir, nil, true, "CARTOGRAPHER_TOKEN", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := removeMCPEntries("cartographer", []string{"kb-a"}, []string{"claude"}, dir, nil, true, "CARTOGRAPHER_TOKEN", false); err != nil {
		t.Fatal(err)
	}
	bridge := map[string][]mcpEntry{"claude": {{Name: "cartographer", Bridge: true}}}
	if _, _, err := applyMCPEntries(bridge, []string{"claude"}, dir, nil, true, "CARTOGRAPHER_TOKEN", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	e := doc["mcpServers"]["cartographer"]
	if e["command"] == nil || e["url"] != nil || e["headers"] != nil {
		t.Fatalf("entry = %v", e)
	}
}

// The command in the entry is the one found on PATH, never a path of the
// running binary (a Cellar directory the next upgrade deletes).
func TestBridgeCommandIsFoundOnPATH(t *testing.T) {
	dir := t.TempDir()
	name := "cartographer"
	if os.PathSeparator == '\\' {
		name += ".exe"
	}
	exe := filepath.Join(dir, name)
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got, err := bridgeCommand()
	if err != nil || got != exe {
		t.Fatalf("bridgeCommand = %q, %v; want %q", got, err, exe)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := bridgeCommand(); err == nil || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("missing binary: %v", err)
	}
}

func TestReportProvidersFollowsTheEntryKind(t *testing.T) {
	cfg := bridgeConfigFor(t, "claude", "codex")
	if err := cfg.BindWorkspace("claude", t.TempDir(), []string{"kb-a"}); err != nil {
		t.Fatal(err)
	}
	// claude keeps the default artifact scope: bindings are listed anyway.
	if cfg.WorkspaceScope("claude") != clientconfig.ScopeProvider {
		t.Fatal("test precondition: scope provider")
	}
	got := reportProviders(cfg)
	if len(got) != 2 {
		t.Fatalf("providers = %+v", got)
	}
	claude, codex := got[0], got[1]
	if claude.Entry != "bridge" || claude.Isolation != "workspace" || len(claude.Workspaces) != 1 {
		t.Fatalf("claude = %+v", claude)
	}
	if codex.Entry != "http" || codex.Isolation != "none" || len(codex.Workspaces) != 0 {
		t.Fatalf("codex = %+v", codex)
	}
	// Without the routed endpoint there is no bridge, and the report says so.
	cfg.ServerRoutedPath = ""
	if p := reportProviders(cfg)[0]; p.Entry != "http" || p.Isolation != "none" {
		t.Fatalf("no routed path: %+v", p)
	}
}

func TestWorkspaceResolveGolden(t *testing.T) {
	root := t.TempDir()
	area := filepath.Join(root, "area")
	repo := filepath.Join(area, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	canon, err := clientconfig.CanonicalWorkspacePath(area)
	if err != nil {
		t.Fatal(err)
	}
	cfg := bridgeConfigFor(t, "claude", "codex")
	if err := cfg.BindWorkspace("claude", area, []string{"kb-b", "kb-a"}); err != nil {
		t.Fatal(err)
	}
	got := renderWorkspaceResolve(cfg, repo, []string{"claude", "codex"})
	want := "folder " + repo + "\n" +
		"claude  (global MCP entry: bridge)\n" +
		"  bound by " + canon + "  ->  kb-a, kb-b\n" +
		"codex  (global MCP entry: http)\n" +
		"  unbound: the provider's default set (every KB)\n" +
		"  this client has no bridge entry, so bindings do not narrow its session\n"
	if got != want {
		t.Fatalf("resolve output:\n%s\nwant:\n%s", got, want)
	}

	// Outside the binding: unbound, with the roots note on a bridge entry.
	other := t.TempDir()
	got = renderWorkspaceResolve(cfg, other, []string{"claude"})
	if !strings.Contains(got, "unbound: the provider's default set (every KB)") || !strings.Contains(got, "MCP roots") ||
		!strings.Contains(got, "  access: read-only (default)\n") || !strings.Contains(got, "set `unbound_access` to write") {
		t.Fatalf("unbound output:\n%s", got)
	}
	if strings.Contains(renderWorkspaceResolve(cfg, repo, []string{"claude"}), "access:") {
		t.Error("a bound folder prints an access line")
	}

	// #1356: the setting, by origin.
	cfg.UnboundModes = map[string]string{"default": "write", "claude": "read"}
	got = renderWorkspaceResolve(cfg, other, []string{"claude"})
	if !strings.Contains(got, "  access: read-only (unbound_access)\n") {
		t.Fatalf("explicit read output:\n%s", got)
	}
	delete(cfg.UnboundModes, "claude")
	got = renderWorkspaceResolve(cfg, other, []string{"claude"})
	if !strings.Contains(got, "  access: read-write (unbound_access)\n") || !strings.Contains(got, "no KB artifact here; bind the folder for the KB's skills and agents") {
		t.Fatalf("write output:\n%s", got)
	}

	// An empty binding says no KB.
	if err := cfg.BindWorkspace("claude", area, nil); err != nil {
		t.Fatal(err)
	}
	if got = renderWorkspaceResolve(cfg, repo, []string{"claude"}); !strings.Contains(got, "->  no KB") {
		t.Fatalf("no-kb output:\n%s", got)
	}
}

// TRAP: the folder is os.Getwd, never $PWD. OpenCode starts its MCP children
// with $PWD set to $HOME; the session must still be scoped by where it runs.
func TestCmdMCPUsesTheWorkingDirectoryNotPWD(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ws := filepath.Join(t.TempDir(), "area")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := bridgeConfigFor(t, "claude")
	cfg.Auth = false
	if err := cfg.BindWorkspace("claude", ws, nil); err != nil { // --no-kb: answered locally
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	t.Chdir(ws)
	t.Setenv("PWD", home) // what OpenCode does

	oldIn, oldOut := mcpStdin, mcpStdout
	defer func() { mcpStdin, mcpStdout = oldIn, oldOut }()
	var out bytes.Buffer
	mcpStdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}` + "\r\n")
	mcpStdout = &out
	if code := cmdMCP([]string{"--provider", "claude"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "bound to no KB") {
		t.Fatalf("the bound folder was not honoured (PWD won?): %s", out.String())
	}
}

func TestCmdMCPRequiresAProvider(t *testing.T) {
	if code := cmdMCP(nil); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

// A bridge that starts names the writer per call (D904), so its entry carries
// none, and git is not asked for an identity no entry would send.
func TestSetEntryAuthorsLeavesAStartableBridgeToTheBridge(t *testing.T) {
	stubBridgeCommand(t)
	cfg := bridgeConfigFor(t, "claude")
	if err := cfg.BindWorkspace("claude", t.TempDir(), []string{"kb-a"}); err != nil {
		t.Fatal(err)
	}
	entries, err := entriesByProviderForKBs(cfg, []string{"claude"}, "cartographer", cfg.ServerURL, cfg.KnownKBs, "/mcp/routed")
	if err != nil {
		t.Fatal(err)
	}
	asked := 0
	w := setEntryAuthors(cfg, entries, func() (string, bool) {
		asked++
		return "Git Person <git@example.test>", false
	})
	if e := entries["claude"][0]; !e.Bridge || e.Author != "" || asked != 0 || len(w) != 0 {
		t.Fatalf("bridge %v, author %q, git asked %d times, warnings %q; want a bridge naming none, no ask, no warning", e.Bridge, e.Author, asked, w)
	}
}

// With no `cartographer` on PATH the provider keeps its HTTP entry, a warning
// says why, and the other providers are still written; both name the writer.
func TestMissingBridgeCommandFallsBackToHTTPWithAWarning(t *testing.T) {
	old := bridgeCommand
	bridgeCommand = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { bridgeCommand = old })

	cfg := bridgeConfigFor(t, "claude", "codex")
	if err := cfg.BindWorkspace("claude", t.TempDir(), []string{"kb-a"}); err != nil {
		t.Fatal(err)
	}
	cfg.Identities = map[string]string{"default": "Ada Lovelace <ada@example.com>"}
	entries, err := entriesByProviderForKBs(cfg, []string{"claude", "codex"}, "cartographer", cfg.ServerURL, cfg.KnownKBs, "/mcp/routed")
	if err != nil {
		t.Fatal(err)
	}
	if w := setEntryAuthors(cfg, entries, func() (string, bool) { return "", false }); len(w) != 0 {
		t.Fatalf("author warnings = %v", w)
	}
	dir := t.TempDir()
	_, warnings, err := applyMCPEntries(entries, []string{"claude", "codex"}, dir, nil, true, "CARTOGRAPHER_TOKEN", false)
	if err != nil {
		t.Fatalf("a missing bridge command must not fail the run: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "claude") || !strings.Contains(warnings[0], "PATH") {
		t.Fatalf("warnings = %v", warnings)
	}
	claude, _ := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if !strings.Contains(string(claude), "https://example.com/mcp/routed") || strings.Contains(string(claude), `"command"`) {
		t.Fatalf("claude kept no HTTP entry: %s", claude)
	}
	codex, _ := os.ReadFile(filepath.Join(dir, ".codex/config.toml"))
	if !strings.Contains(string(codex), "https://example.com/mcp/routed") {
		t.Fatalf("codex was not written: %s", codex)
	}
	var claudeCfg struct {
		MCPServers map[string]struct{ Headers map[string]string } `json:"mcpServers"`
	}
	if err := json.Unmarshal(claude, &claudeCfg); err != nil {
		t.Fatal(err)
	}
	if got := claudeCfg.MCPServers["cartographer"].Headers[client.AuthorHeader]; got != "Ada Lovelace <ada@example.com>" ||
		!strings.Contains(string(codex), `"Cartographer-Author" = "Ada Lovelace <ada@example.com>"`) {
		t.Fatalf("the writer is missing: claude %q, codex:\n%s", got, codex)
	}
}

// D1184: a provider with an access profile never gets the bridge, which does not
// carry the profile: with an explicit or a derived workspace binding it keeps
// the HTTP entry with access=read-min, and the sync says so.
func TestAccessProfileKeepsTheHTTPEntryDespiteWorkspaceBindings(t *testing.T) {
	cfg := bridgeConfigFor(t, "claude")
	if err := cfg.SetAccess("claude", clientconfig.AccessReadMin); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	check := func(what string) {
		t.Helper()
		if cfg.UsesBridge("claude", "/mcp/routed") || !cfg.ProfileIgnoresWorkspaces("claude", "/mcp/routed") {
			t.Fatalf("%s: the provider with a profile must not use the bridge", what)
		}
		got, err := entriesByProviderForKBs(cfg, []string{"claude"}, "cartographer", cfg.ServerURL, cfg.KnownKBs, "/mcp/routed")
		if err != nil {
			t.Fatal(err)
		}
		if e := got["claude"]; len(e) != 1 || e[0].Bridge || e[0].URL != "https://example.com/mcp/routed?access=read-min&provisioned=1" {
			t.Fatalf("%s: entries = %+v, want the HTTP entry with the profile", what, e)
		}
	}
	if err := cfg.BindWorkspace("claude", t.TempDir(), []string{"kb-a"}); err != nil {
		t.Fatal(err)
	}
	check("explicit binding")

	derived := bridgeConfigFor(t, "claude")
	if err := derived.SetAccess("claude", clientconfig.AccessReadMin); err != nil {
		t.Fatal(err)
	}
	if err := derived.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	derived.SetDerived(clientconfig.DerivedState{Bindings: map[string][]clientconfig.DerivedBinding{
		"claude": {{Path: t.TempDir(), KBs: []string{"kb-a"}}},
	}})
	cfg = derived
	check("derived binding")
}

func TestWorkspaceBindRefusesAProviderWithAnAccessProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir, err := clientconfig.TargetDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg := clientconfig.Default()
	if err := cfg.SetAccess("claude", clientconfig.AccessReadMin); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if code := cmdWorkspaceBind([]string{"claude", t.TempDir(), "--kb", "kb-a"}); code != 2 {
		t.Fatalf("workspace bind exit = %d, want 2", code)
	}
	got, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Workspaces["claude"]) != 0 {
		t.Errorf("a binding was saved: %+v", got.Workspaces)
	}
}

// A bridge entry names the `cartographer` found on PATH when it was written. Once
// that binary moves, the entry is stale: status reports it and sync rewrites it,
// while an entry the user wrote under the same name is neither.
func TestStaleBridgeEntriesAreReportedAndRewritten(t *testing.T) {
	stubBridgeCommand(t)
	moved := filepath.Join(t.TempDir(), "gone", "cartographer")
	bridge := map[string][]mcpEntry{}
	for _, provider := range []string{"claude", "codex", "opencode"} {
		bridge[provider] = []mcpEntry{{Name: "cartographer", Bridge: true}}
	}
	for provider, entries := range bridge {
		t.Run(provider, func(t *testing.T) {
			dir := t.TempDir()
			old := bridgeCommand
			bridgeCommand = func() (string, error) { return moved, nil }
			if _, _, err := applyMCPEntries(map[string][]mcpEntry{provider: entries}, []string{provider}, dir, nil, true, "CARTOGRAPHER_TOKEN", false); err != nil {
				t.Fatal(err)
			}
			bridgeCommand = old

			if stale := staleBridgeEntries(provider, entries, dir, nil); len(stale) != 1 || stale[0].Have != moved || stale[0].Want != "/usr/local/bin/cartographer" || !stale[0].Missing {
				t.Fatalf("stale = %+v, want the moved binary reported as missing", stale)
			}

			before := snapshotMCPConfigFiles([]string{provider}, dir, nil)
			if _, _, err := applyMCPEntries(map[string][]mcpEntry{provider: entries}, []string{provider}, dir, nil, true, "CARTOGRAPHER_TOKEN", false); err != nil {
				t.Fatal(err)
			}
			if changed := changedMCPEntries([]string{provider}, map[string][]mcpEntry{provider: entries}, before, dir, nil, true, "CARTOGRAPHER_TOKEN"); len(changed) != 1 {
				t.Fatalf("sync reports %v, want the entry rewritten", changed)
			}
			if stale := staleBridgeEntries(provider, entries, dir, nil); len(stale) != 0 {
				t.Fatalf("after sync stale = %+v", stale)
			}
		})
	}
}

func TestStaleBridgeEntriesLeaveUserOwnedEntriesAlone(t *testing.T) {
	stubBridgeCommand(t)
	entries := []mcpEntry{{Name: "cartographer", Bridge: true}}
	dir := t.TempDir()
	claude := `{"mcpServers":{"cartographer":{"type":"stdio","command":"/opt/mine/server","args":["--stdio"]}}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(claude), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	codex := "[mcp_servers.cartographer]\nurl = \"https://example.com/mcp\"\n"
	if err := os.WriteFile(filepath.Join(dir, ".codex", "config.toml"), []byte(codex), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"claude", "codex"} {
		if stale := staleBridgeEntries(provider, entries, dir, nil); len(stale) != 0 {
			t.Errorf("%s: a user-owned entry was reported as stale: %+v", provider, stale)
		}
	}
}
