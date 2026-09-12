package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// multiKBServer supplies the two client endpoints exercised by connect: /health
// enumerates the KBs and each sync_pull call returns an empty valid manifest.
func multiKBServer(t *testing.T, health string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(health))
			return
		}
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
			"content": []map[string]string{{"type": "text", "text": `{"revision":"test","artifacts":[]}`}},
		}})
	}))
}

func TestEnumerateKBs_HealthShapes(t *testing.T) {
	for _, tc := range []struct {
		name, health string
		present      bool
		want         []string
	}{
		{"present", `{"status":"ok","kbs":[{"name":"alpha"},{"name":"beta"}]}`, true, []string{"alpha", "beta"}},
		{"absent", `{"status":"ok"}`, false, nil},
		{"empty", `{"status":"ok","kbs":[]}`, true, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := multiKBServer(t, tc.health)
			defer srv.Close()
			facts, err := enumerateKBs(srv.URL+"/mcp", false, "")
			if err != nil || facts.Listed != tc.present || strings.Join(facts.Names, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("enumerateKBs = (%v, %v, %v), want (%v, %v, nil)", facts.Names, facts.Listed, err, tc.want, tc.present)
			}
		})
	}
}

func TestDoConnect_PerKBEntries_AllProviders(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"},{"name":"beta"},{"name":"gamma"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	providers := []string{"claude", "codex", "kiro", "opencode"}
	// D190: a first multi-KB connect must name its KBs; "all" is the explicit
	// way to say "every mounted one", which is what this case is about.
	res, err := doConnect(connectOptions{Providers: providers, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", TokenEnv: "TOKEN", Trust: true, KBs: []string{"all"}})
	if err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	if got, want := strings.Join(res.MCPEntries, ","), "cartographer-alpha,cartographer-beta,cartographer-gamma"; got != want {
		t.Fatalf("MCPEntries = %q, want %q", got, want)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil || strings.Join(cfg.KnownKBs, ",") != "alpha,beta,gamma" {
		t.Fatalf("persisted KBs = %v, err=%v", cfg.KnownKBs, err)
	}
	for _, provider := range providers {
		r, err := configurator.Emit(&configurator.ServerConfig{Name: "placeholder"}, configurator.Provider(provider))
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, r.FilePath))
		if err != nil {
			t.Fatalf("%s config: %v", provider, err)
		}
		for _, kb := range []string{"alpha", "beta", "gamma"} {
			name := "cartographer-" + kb
			if !strings.Contains(string(data), name) || !strings.Contains(string(data), "kb="+kb) {
				t.Errorf("%s missing scoped %s entry: %s", provider, name, data)
			}
		}
	}
}

// TestKiroFlatNamespaceWarning covers the D152 rework: the warning fires only
// when a flat-namespace provider would receive >=2 KBs that register their tools
// **unprefixed**, reading the effective prefixes /health advertises (D120). The
// pre-D152 version was unconditional on >=2 entries and fired on every sync of a
// deployment where nothing was wrong.
func TestKiroFlatNamespaceWarning(t *testing.T) {
	twoEntries := []mcpEntry{{Name: "cartographer-alpha", KBName: "alpha"}, {Name: "cartographer-beta", KBName: "beta"}}
	threeEntries := append(append([]mcpEntry{}, twoEntries...), mcpEntry{Name: "cartographer-gamma", KBName: "gamma"})
	oneEntry := []mcpEntry{{Name: "cartographer", KBName: "alpha"}}
	bothUnprefixed := map[string]string{"alpha": "", "beta": ""}
	bothPrefixed := map[string]string{"alpha": "a", "beta": "b"}
	// Three KBs, only one unprefixed: safe, and the case that produced the
	// reported false positive on every sync.
	oneUnprefixed := map[string]string{"alpha": "", "beta": "b", "gamma": "g"}

	t.Run("two unprefixed KBs warn and are named", func(t *testing.T) {
		w := kiroFlatNamespaceWarning([]string{"kiro"}, map[string][]mcpEntry{"kiro": twoEntries}, bothUnprefixed, nil)
		if w == "" {
			t.Fatal("expected a warning")
		}
		for _, name := range []string{"alpha", "beta"} {
			if !strings.Contains(w, name) {
				t.Errorf("warning %q does not name %q", w, name)
			}
		}
		if !strings.Contains(w, "tool_prefix_mode: kb-name") {
			t.Errorf("warning %q does not offer the global remedy", w)
		}
	})

	t.Run("a single unprefixed KB is safe", func(t *testing.T) {
		if w := kiroFlatNamespaceWarning([]string{"kiro"}, map[string][]mcpEntry{"kiro": threeEntries}, oneUnprefixed, nil); w != "" {
			t.Errorf("expected silence with one unprefixed KB, got %q", w)
		}
	})

	t.Run("all prefixed is silent", func(t *testing.T) {
		if w := kiroFlatNamespaceWarning([]string{"kiro"}, map[string][]mcpEntry{"kiro": twoEntries}, bothPrefixed, nil); w != "" {
			t.Errorf("expected silence, got %q", w)
		}
	})

	t.Run("unverifiable prefixes still warn, and say why", func(t *testing.T) {
		w := kiroFlatNamespaceWarning([]string{"kiro"}, map[string][]mcpEntry{"kiro": twoEntries}, nil, errors.New("connection refused"))
		if w == "" {
			t.Fatal("expected a warning when the prefixes cannot be read")
		}
		if !strings.Contains(w, "connection refused") {
			t.Errorf("warning %q does not say why the prefixes are unknown", w)
		}
	})

	t.Run("a single entry is silent", func(t *testing.T) {
		if w := kiroFlatNamespaceWarning([]string{"kiro"}, map[string][]mcpEntry{"kiro": oneEntry}, bothUnprefixed, nil); w != "" {
			t.Errorf("expected no warning for a single entry, got %q", w)
		}
	})

	t.Run("no flat-namespace provider is silent", func(t *testing.T) {
		if w := kiroFlatNamespaceWarning([]string{"claude", "codex", "opencode"}, map[string][]mcpEntry{"kiro": twoEntries}, bothUnprefixed, nil); w != "" {
			t.Errorf("expected no warning without a flat-namespace provider, got %q", w)
		}
	})
}

// TestAntigravityToolBudgetWarning verifies that the warning fires when per-KB
// MCP entries would produce tool identifiers exceeding Antigravity's
// 64-character limit (issue #273, problem 2).
func TestAntigravityToolBudgetWarning(t *testing.T) {
	longKB := []mcpEntry{
		{Name: "cartographer-morbos-agentic-wiki", KBName: "morbos-agentic-wiki"},
		{Name: "cartographer-server-casa-kb", KBName: "server-casa-kb"},
	}
	shortKB := []mcpEntry{
		{Name: "short-a", KBName: "a"},
		{Name: "short-b", KBName: "b"},
	}
	singleEntry := []mcpEntry{{Name: "cartographer", KBName: ""}}
	bothPrefixed := map[string]string{"morbos-agentic-wiki": "morbos_agentic_wiki", "server-casa-kb": "server_casa_kb"}
	bothUnprefixed := map[string]string{"a": "", "b": ""}
	shortPrefixed := map[string]string{"a": "a", "b": "b"}

	t.Run("long entry names with prefixes warn", func(t *testing.T) {
		w := antigravityToolBudgetWarning(
			[]string{"antigravity"},
			map[string][]mcpEntry{"antigravity": longKB},
			bothPrefixed,
		)
		if w == "" {
			t.Fatal("expected a warning for long entry names with prefixed tools")
		}
		if !strings.Contains(w, "mount_mode: routed") {
			t.Errorf("warning %q does not mention the remedy", w)
		}
		if !strings.Contains(w, "cartographer-morbos-agentic-wiki") {
			t.Errorf("warning %q does not name the offending entry", w)
		}
	})

	t.Run("short entry names without prefix are silent", func(t *testing.T) {
		if w := antigravityToolBudgetWarning(
			[]string{"antigravity"},
			map[string][]mcpEntry{"antigravity": shortKB},
			bothUnprefixed,
		); w != "" {
			t.Errorf("expected silence for short unprefixed entries, got %q", w)
		}
	})

	t.Run("short entry names with short prefix are silent", func(t *testing.T) {
		if w := antigravityToolBudgetWarning(
			[]string{"antigravity"},
			map[string][]mcpEntry{"antigravity": shortKB},
			shortPrefixed,
		); w != "" {
			t.Errorf("expected silence for short prefixed entries, got %q", w)
		}
	})

	t.Run("single entry is silent", func(t *testing.T) {
		if w := antigravityToolBudgetWarning(
			[]string{"antigravity"},
			map[string][]mcpEntry{"antigravity": singleEntry},
			bothPrefixed,
		); w != "" {
			t.Errorf("expected no warning for a single entry, got %q", w)
		}
	})

	t.Run("no antigravity provider is silent", func(t *testing.T) {
		if w := antigravityToolBudgetWarning(
			[]string{"claude", "codex"},
			map[string][]mcpEntry{"antigravity": longKB},
			bothPrefixed,
		); w != "" {
			t.Errorf("expected no warning without antigravity provider, got %q", w)
		}
	})

	t.Run("nil prefixes suppress warning", func(t *testing.T) {
		if w := antigravityToolBudgetWarning(
			[]string{"antigravity"},
			map[string][]mcpEntry{"antigravity": longKB},
			nil,
		); w != "" {
			t.Errorf("expected silence when prefixes are nil (server unreachable), got %q", w)
		}
	})
}

// TestDoConnect_Kiro_MultiKB_WarnsFlatNamespace exercises the warning through
// doConnect end-to-end: connecting kiro to a 2-KB server surfaces the
// warning in connectResult.Warnings (rendered on stderr by
// printConnectResult / the TUI).
func TestDoConnect_Kiro_MultiKB_WarnsFlatNamespace(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"},{"name":"beta"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	res, err := doConnect(connectOptions{Providers: []string{"kiro"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", TokenEnv: "TOKEN", Trust: true, KBs: []string{"all"}})
	if err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "flat MCP tool namespace") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a flat-namespace warning in %v", res.Warnings)
	}
}

func TestEntriesForKBs_SingleStaysBare(t *testing.T) {
	entries, err := entriesForKBs("wiki", "https://example.test/mcp", []string{"only"}, []string{"only"}, "")
	if err != nil || len(entries) != 1 || entries[0].Name != "wiki" || entries[0].URL != "https://example.test/mcp" {
		t.Fatalf("entriesForKBs = %+v, %v; want one bare entry", entries, err)
	}
}

func TestDoConnect_SingleKB_BareEntry_AllProviders(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"only"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	providers := []string{"claude", "codex", "kiro", "opencode"}
	res, err := doConnect(connectOptions{Providers: providers, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", TokenEnv: "TOKEN", Trust: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.MCPEntries, ","); got != "cartographer" {
		t.Fatalf("MCPEntries = %q, want bare cartographer", got)
	}
	for _, provider := range providers {
		r, _ := configurator.Emit(&configurator.ServerConfig{Name: "placeholder"}, configurator.Provider(provider))
		data, err := os.ReadFile(filepath.Join(dir, r.FilePath))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "cartographer") || strings.Contains(string(data), "cartographer-only") {
			t.Errorf("%s did not retain exactly the bare entry: %s", provider, data)
		}
	}
}

func TestRemoveMCPEntries_RemovesEveryManagedEntry(t *testing.T) {
	dir := t.TempDir()
	entries, err := entriesForKBs("wiki", "https://example.test/mcp", []string{"a", "b"}, []string{"a", "b"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := applyMCPEntries(sameEntriesFor([]string{"claude", "codex", "kiro", "opencode"}, entries), []string{"claude", "codex", "kiro", "opencode"}, dir, false, "", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := removeMCPEntries("wiki", []string{"a", "b"}, []string{"claude", "codex", "kiro", "opencode"}, dir, false, "", false); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"claude", "codex", "kiro", "opencode"} {
		r, _ := configurator.Emit(&configurator.ServerConfig{Name: "placeholder"}, configurator.Provider(provider))
		data, err := os.ReadFile(filepath.Join(dir, r.FilePath))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "wiki-a") || strings.Contains(string(data), "wiki-b") {
			t.Errorf("%s still contains managed multi-KB entry: %s", provider, data)
		}
	}
}

func TestCmdSync_ReconcilesOneToManyAndBack(t *testing.T) {
	health := `{"status":"ok","kbs":[{"name":"alpha"},{"name":"beta"}]}`
	srv := multiKBServer(t, health)
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", ServerName: "wiki", TokenEnv: "TOKEN", Agents: []string{"claude"}, KnownKBs: []string{"alpha"}, Trust: true}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	bare, _ := entriesForKBs("wiki", cfg.ServerURL, cfg.KnownKBs, cfg.KnownKBs, "")
	if _, _, err := applyMCPEntries(sameEntriesFor(cfg.Agents, bare), cfg.Agents, dir, false, "", false); err != nil {
		t.Fatal(err)
	}

	if code := cmdSync(nil); code != 0 {
		t.Fatalf("sync 1→2 = %d", code)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil || !strings.Contains(string(data), "wiki-alpha") || !strings.Contains(string(data), "wiki-beta") || strings.Contains(string(data), `"wiki":`) {
		t.Fatalf("1→2 entries = %s, err=%v", data, err)
	}
	updated, err := clientconfig.Load(dir)
	if err != nil || strings.Join(updated.KnownKBs, ",") != "alpha,beta" {
		t.Fatalf("1→2 persisted KBs = %v, err=%v", updated.KnownKBs, err)
	}

	// A fresh server instance is enough to model a KB disappearing while
	// keeping the same client configuration and provider files.
	srv.Close()
	srv = multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"}]}`)
	updated.ServerURL = srv.URL + "/mcp"
	if err := clientconfig.Save(dir, updated); err != nil {
		t.Fatal(err)
	}
	if code := cmdSync(nil); code != 0 {
		t.Fatalf("sync 2→1 = %d", code)
	}
	data, err = os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil || !strings.Contains(string(data), `"wiki":`) || strings.Contains(string(data), "wiki-alpha") || strings.Contains(string(data), "wiki-beta") {
		t.Fatalf("2→1 entries = %s, err=%v", data, err)
	}
}

func TestDoDisconnect_RemovesPersistedPerKBEntries(t *testing.T) {
	dir := t.TempDir()
	cfg := &clientconfig.Config{ServerURL: "https://example.test/mcp", ServerName: "wiki", Agents: []string{"claude"}, KnownKBs: []string{"alpha", "beta"}, Trust: true}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	entries, _ := entriesForKBs("wiki", cfg.ServerURL, cfg.KnownKBs, cfg.KnownKBs, "")
	if _, _, err := applyMCPEntries(sameEntriesFor(cfg.Agents, entries), cfg.Agents, dir, false, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "wiki-alpha") || strings.Contains(string(data), "wiki-beta") {
		t.Errorf("disconnect left a per-KB entry: %s", data)
	}
}

func TestCmdSync_ServerDownKeepsMCPEntriesAndKBs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfg := &clientconfig.Config{ServerURL: "http://127.0.0.1:1/mcp", ServerName: "wiki", Agents: []string{"claude"}, KnownKBs: []string{"alpha", "beta"}, Trust: true}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	entries, _ := entriesForKBs("wiki", cfg.ServerURL, cfg.KnownKBs, cfg.KnownKBs, "")
	if _, _, err := applyMCPEntries(sameEntriesFor(cfg.Agents, entries), cfg.Agents, dir, false, "", false); err != nil {
		t.Fatal(err)
	}
	beforeConfig, err := os.ReadFile(clientconfig.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	beforeMCP, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if code := cmdSync(nil); code != 2 {
		t.Fatalf("sync against down server = %d, want 2", code)
	}
	afterConfig, _ := os.ReadFile(clientconfig.Path(dir))
	afterMCP, _ := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if string(afterConfig) != string(beforeConfig) || string(afterMCP) != string(beforeMCP) {
		t.Error("server-down sync changed persisted KBs or MCP entries")
	}
}

func TestDoConnect_Codex_RepairsConfigDuplicatedByCodexRewrite(t *testing.T) {
	// Acceptance for D99: connect on a config.toml Codex rewrote (markers gone,
	// our table left behind) must leave a single [mcp_servers.cartographer],
	// keep every unrelated user section, and say why the file changed.
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"only"}]}`)
	defer srv.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := `model = "gpt-5.6-terra"

[mcp_servers.cartographer]
bearer_token_env_var = "CARTOGRAPHER_TOKENS"
url = "https://cartographer.example.test/mcp"

[mcp_servers.openaiDeveloperDocs]
url = "https://developers.openai.com/mcp"

[mcp_servers.cartographer]
url = "https://cartographer.example.test/mcp"
bearer_token_env_var = "CARTOGRAPHER_TOKENS"
`
	if err := os.WriteFile(configPath, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := doConnect(connectOptions{Providers: []string{"codex"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", TokenEnv: "TOKEN", Trust: true})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if n := strings.Count(got, "[mcp_servers.cartographer]"); n != 1 {
		t.Errorf("expected exactly 1 [mcp_servers.cartographer] after connect, found %d:\n%s", n, got)
	}
	for _, want := range []string{`model = "gpt-5.6-terra"`, "[mcp_servers.openaiDeveloperDocs]"} {
		if !strings.Contains(got, want) {
			t.Errorf("unrelated content %q lost:\n%s", want, got)
		}
	}
	if len(res.Warnings) == 0 {
		t.Error("the adopted duplicates must be reported through connectResult.Warnings")
	}
}

// D144: the process that knows the mounted configuration warns about a
// colliding tool surface, whatever the client was configured by hand with.
func TestFlatNamespaceMountWarning(t *testing.T) {
	cases := []struct {
		name     string
		names    []string
		prefixes []string
		want     bool
	}{
		{"two unprefixed KBs", []string{"a", "b"}, []string{"", ""}, true},
		{"three KBs, two unprefixed", []string{"a", "b", "c"}, []string{"", "cp", ""}, true},
		{"two prefixed KBs", []string{"a", "b"}, []string{"ap", "bp"}, false},
		{"one of two prefixed", []string{"a", "b"}, []string{"ap", ""}, false},
		{"single unprefixed KB", []string{"a"}, []string{""}, false},
		{"no KB", nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := flatNamespaceMountWarning(tc.names, tc.prefixes)
			if (w != "") != tc.want {
				t.Fatalf("flatNamespaceMountWarning(%v, %v) = %q, want warning=%v", tc.names, tc.prefixes, w, tc.want)
			}
			if !tc.want {
				return
			}
			for i, name := range tc.names {
				quoted := strconv.Quote(name)
				if unprefixed := tc.prefixes[i] == ""; unprefixed != strings.Contains(w, quoted) {
					t.Errorf("warning %q: KB %s named=%v, want %v", w, quoted, !unprefixed, unprefixed)
				}
			}
			if !strings.Contains(w, "tool_prefix") {
				t.Errorf("warning does not point at the fix: %q", w)
			}
		})
	}
}

// D141: connecting hermes registers it for artifact delivery without writing
// any MCP configuration — its endpoints are rendered by its own deployment —
// and says so, because silently writing nothing would read as a bug.
func TestDoConnect_Hermes_NoMCPConfigButSaysSo(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	hermesHome := t.TempDir()
	t.Setenv("HERMES_HOME", hermesHome)

	res, err := doConnect(connectOptions{Providers: []string{"hermes"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", TokenEnv: "TOKEN", Trust: true})
	if err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	if len(res.ConfigsWritten) != 0 {
		t.Errorf("connect hermes wrote MCP configs: %v", res.ConfigsWritten)
	}
	said := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "MCP endpoint is NOT configured") {
			said = true
		}
	}
	if !said {
		t.Errorf("connect hermes said nothing about the MCP endpoint: %v", res.Warnings)
	}
	for _, root := range []string{dir, hermesHome} {
		for _, name := range []string{".claude.json", "opencode.json"} {
			if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
				t.Errorf("unexpected %s under %s: %v", name, root, err)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "skills")); !os.IsNotExist(err) {
			t.Errorf("HERMES_HOME/skills/ must never be created (%s): %v", root, err)
		}
	}

	cfg, err := clientconfig.Load(dir)
	if err != nil || strings.Join(cfg.Agents, ",") != "hermes" {
		t.Fatalf("persisted agents = %v, err=%v", cfg.Agents, err)
	}

	// Disconnect touches nothing else and leaves no agent behind.
	if _, err := doDisconnect(disconnectOptions{Providers: []string{"hermes"}, Dir: dir}); err != nil {
		t.Fatalf("doDisconnect: %v", err)
	}
	cfg, err = clientconfig.Load(dir)
	if err != nil || len(cfg.Agents) != 0 {
		t.Fatalf("agents after disconnect = %v, err=%v", cfg.Agents, err)
	}
}

// Connecting a provider whose root directory is not configured fails naming
// the variable, before anything is written (D141).
func TestDoConnect_Hermes_MissingHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERMES_HOME", "")
	_, err := doConnect(connectOptions{Providers: []string{"hermes"}, Dir: dir, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer"})
	if err == nil || !strings.Contains(err.Error(), "HERMES_HOME") {
		t.Fatalf("doConnect error = %v, want one naming HERMES_HOME", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cartographer.yaml")); !os.IsNotExist(err) {
		t.Errorf("a failed connect persisted config anyway: %v", err)
	}
}

// sameEntriesFor is the pre-D170 shape — every provider receiving the same MCP
// entry set — for tests that do not exercise bindings.
func sameEntriesFor(providers []string, entries []mcpEntry) map[string][]mcpEntry {
	out := make(map[string][]mcpEntry, len(providers))
	for _, p := range providers {
		out[p] = entries
	}
	return out
}

// --- D170: entry shape vs entry set ---

// TestEntriesForKBs_ShapeFromServerSetFromBinding pins the trap: bare /mcp
// auto-routes only when the SERVER mounts one KB, so a client bound to one of
// four still needs ?kb=. Deriving the shape from the binding's length would
// hand that client a bare entry the server answers with 400.
func TestEntriesForKBs_ShapeFromServerSetFromBinding(t *testing.T) {
	cases := []struct {
		name      string
		mounted   []string
		bound     []string
		wantNames []string
		wantQuery bool
	}{
		{
			name:      "single-KB server: bare entry, whatever the binding says",
			mounted:   []string{"only"},
			bound:     []string{"only"},
			wantNames: []string{"wiki"},
		},
		{
			name:      "multi-KB server, bound to all: one entry per KB",
			mounted:   []string{"a", "b"},
			bound:     []string{"a", "b"},
			wantNames: []string{"wiki-a", "wiki-b"},
			wantQuery: true,
		},
		{
			name:      "multi-KB server, bound to one: still a scoped entry",
			mounted:   []string{"a", "b", "c", "d"},
			bound:     []string{"b"},
			wantNames: []string{"wiki-b"},
			wantQuery: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := entriesForKBs("wiki", "https://example.test/mcp", tc.mounted, tc.bound, "")
			if err != nil {
				t.Fatalf("entriesForKBs: %v", err)
			}
			if got := strings.Join(entryNames(entries), ","); got != strings.Join(tc.wantNames, ",") {
				t.Errorf("names = %q, want %q", got, strings.Join(tc.wantNames, ","))
			}
			for _, e := range entries {
				if strings.Contains(e.URL, "kb=") != tc.wantQuery {
					t.Errorf("URL %q: kb= present = %v, want %v", e.URL, !tc.wantQuery, tc.wantQuery)
				}
			}
		})
	}
}

// TestEntriesByProviderForKBs_EmptyBindingGetsNoEntry: a provider explicitly
// bound to nothing must not be handed a way in.
func TestEntriesByProviderForKBs_EmptyBindingGetsNoEntry(t *testing.T) {
	cfg := &clientconfig.Config{
		ServerName: "wiki", Agents: []string{"claude", "codex"},
		KnownKBs: []string{"a", "b"},
		Clients: map[string]clientconfig.ClientBinding{
			"claude": {KBs: nil},
			"codex":  {KBs: []string{"a"}},
		},
	}
	byProvider, err := entriesByProviderForKBs(cfg, cfg.Agents, "wiki", "https://example.test/mcp", cfg.KnownKBs, "")
	if err != nil {
		t.Fatalf("entriesByProviderForKBs: %v", err)
	}
	if len(byProvider["claude"]) != 0 {
		t.Errorf("claude got %+v, want no entry", byProvider["claude"])
	}
	if got := strings.Join(entryNames(byProvider["codex"]), ","); got != "wiki-a" {
		t.Errorf("codex got %q, want wiki-a", got)
	}
}

// TestManagedEntryNamesCoversEveryKnownKB pins the second trap: removal must be
// driven by the union of known KBs, never by a provider's filtered binding, or
// an unbound KB's entry is orphaned forever.
func TestManagedEntryNamesCoversEveryKnownKB(t *testing.T) {
	names := managedEntryNames("wiki", []string{"a", "b", "c"})
	for _, want := range []string{"wiki", "wiki-a", "wiki-b", "wiki-c"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("managedEntryNames missing %q: %v", want, names)
		}
	}
}

// --- least-privilege first connect (D190) ---

func TestResolveKBSelection(t *testing.T) {
	mounted := []string{"alpha", "beta", "gamma"}
	for _, tc := range []struct {
		name      string
		selection []string
		mounted   []string
		listed    bool
		want      []string
		wantErr   string
	}{
		{name: "one KB needs no choice", mounted: []string{"alpha"}, listed: true, want: []string{"alpha"}},
		{name: "several KBs and no choice is an error", mounted: mounted, listed: true,
			wantErr: "choose which ones this client receives"},
		{name: "all is explicit and expands to the mounted names", selection: []string{"all"},
			mounted: mounted, listed: true, want: mounted},
		{name: "named KBs", selection: []string{"alpha", "gamma"}, mounted: mounted, listed: true,
			want: []string{"alpha", "gamma"}},
		{name: "duplicates collapse", selection: []string{"alpha", "alpha"}, mounted: mounted, listed: true,
			want: []string{"alpha"}},
		{name: "unknown KB names the mounted ones", selection: []string{"delta"}, mounted: mounted, listed: true,
			wantErr: "this server mounts alpha, beta, gamma"},
		{name: "empty value is an error, not none", selection: []string{""}, mounted: mounted, listed: true,
			wantErr: "empty name"},
		{name: "all cannot be combined", selection: []string{"all", "alpha"}, mounted: mounted, listed: true,
			wantErr: "cannot be combined"},
		{name: "all needs a KB list", selection: []string{"all"}, mounted: nil, listed: false,
			wantErr: "could not be read"},
		{name: "unreachable server with no selection is not an error", mounted: nil, listed: false},
		{name: "an unlisted server does not validate names", selection: []string{"alpha"}, listed: false,
			want: []string{"alpha"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveKBSelection(tc.selection, tc.mounted, tc.listed)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got %v", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v; want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("selection = %v; want %v", got, tc.want)
			}
		})
	}
}

// The core guarantee: no ordering of commands can materialize an artifact from
// a KB the operator did not name. A scripted first connect against a multi-KB
// server fails, and writes nothing at all.
func TestDoConnect_MultiKB_WithoutSelection_FailsAndWritesNothing(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"},{"name":"beta"},{"name":"gamma"}]}`)
	defer srv.Close()
	dir := t.TempDir()

	_, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", TokenEnv: "TOKEN", Trust: true})
	if err == nil {
		t.Fatal("a first multi-KB connect with no --kb must fail")
	}
	for _, want := range []string{"alpha", "beta", "gamma", "--kb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q: %v", want, err)
		}
	}

	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		t.Fatalf("read dir: %v", rerr)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("nothing may be written before the choice is made, found: %v", names)
	}
}

func TestDoConnect_KBSelection_BindsOnlyWhatWasNamed(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"},{"name":"beta"},{"name":"gamma"}]}`)
	defer srv.Close()
	dir := t.TempDir()

	res, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", TokenEnv: "TOKEN", Trust: true, KBs: []string{"beta"}})
	if err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	if got, want := strings.Join(res.MCPEntries, ","), "cartographer-beta"; got != want {
		t.Errorf("MCPEntries = %q, want %q", got, want)
	}

	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	bound, explicit := cfg.BoundKBs("claude")
	if !explicit {
		t.Fatal("the binding must be explicit, not the implicit 'all known' default")
	}
	if strings.Join(bound, ",") != "beta" {
		t.Errorf("bound = %v; want [beta]", bound)
	}
}

// --kb all records the names, not the implicit default: that is what stops a
// KB mounted later from widening a client that already exists.
func TestDoConnect_KBSelectionAll_IsExplicit(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"},{"name":"beta"}]}`)
	defer srv.Close()
	dir := t.TempDir()

	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", TokenEnv: "TOKEN", Trust: true, KBs: []string{"all"}}); err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	bound, explicit := cfg.BoundKBs("claude")
	if !explicit || strings.Join(bound, ",") != "alpha,beta" {
		t.Errorf("bound = %v (explicit=%v); want an explicit [alpha beta]", bound, explicit)
	}
}

// A single-KB server needs no flag: there is nothing to choose.
func TestDoConnect_SingleKB_NeedsNoSelection(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := t.TempDir()

	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", TokenEnv: "TOKEN", Trust: true}); err != nil {
		t.Fatalf("doConnect on a single-KB server: %v", err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if bound, explicit := cfg.BoundKBs("claude"); !explicit || strings.Join(bound, ",") != "alpha" {
		t.Errorf("bound = %v (explicit=%v); want an explicit [alpha]", bound, explicit)
	}
}

// An already-bound provider is not a first connect: its recorded choice stands
// and a re-run never re-opens a catalogue the operator narrowed.
func TestDoConnect_ExistingBinding_IsPreserved(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"},{"name":"beta"},{"name":"gamma"}]}`)
	defer srv.Close()
	dir := t.TempDir()

	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", TokenEnv: "TOKEN", Trust: true, KBs: []string{"beta"}}); err != nil {
		t.Fatalf("first doConnect: %v", err)
	}
	// The same command again, with no --kb: it must neither fail nor widen.
	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", TokenEnv: "TOKEN", Trust: true}); err != nil {
		t.Fatalf("second doConnect: %v", err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if bound, _ := cfg.BoundKBs("claude"); strings.Join(bound, ",") != "beta" {
		t.Errorf("bound = %v; want it unchanged at [beta]", bound)
	}
}

func TestDoConnect_UnknownKB_FailsBeforeAnyWrite(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"},{"name":"beta"}]}`)
	defer srv.Close()
	dir := t.TempDir()

	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", TokenEnv: "TOKEN", Trust: true, KBs: []string{"delta"}}); err == nil {
		t.Fatal("an unmounted KB name must fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a rejected selection must write nothing, found %d entries", len(entries))
	}
}

// TestEntriesForKBs_Routed_OneEntry is D187's client-side acceptance: a server
// with three KBs produces ONE MCP entry, pointing at the routed endpoint, with
// no ?kb= in the URL — the KB now travels in each tool call.
func TestEntriesForKBs_Routed_OneEntry(t *testing.T) {
	entries, err := entriesForKBs("wiki", "https://example.test/mcp", []string{"a", "b", "c"}, []string{"a", "b", "c"}, "/mcp/routed")
	if err != nil {
		t.Fatalf("entriesForKBs: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("routed server produced %d entries, want 1: %+v", len(entries), entries)
	}
	if entries[0].Name != "wiki" {
		t.Errorf("entry name = %q, want the bare server name", entries[0].Name)
	}
	if !strings.HasSuffix(entries[0].URL, "/mcp/routed") {
		t.Errorf("entry URL = %q, want the routed path", entries[0].URL)
	}
	if strings.Contains(entries[0].URL, "kb=") {
		t.Errorf("entry URL carries a kb selector: %q", entries[0].URL)
	}
}

// TestEntriesForKBs_Routed_NarrowBindingStillOneEntry: routing changes the
// transport, not the authorization. A provider bound to one of three KBs still
// gets the single routed entry — what it may *use* is the binding's business,
// enforced during sync, not the entry set's.
func TestEntriesForKBs_Routed_NarrowBindingStillOneEntry(t *testing.T) {
	entries, err := entriesForKBs("wiki", "https://example.test/mcp", []string{"a", "b", "c"}, []string{"b"}, "/mcp/routed")
	if err != nil {
		t.Fatalf("entriesForKBs: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "wiki" {
		t.Fatalf("entries = %+v, want the single routed entry", entries)
	}
}

// TestManagedEntryNames_CoversModeSwitch: switching a deployment between the
// two topologies must not leave orphan entries behind. The removal set has to
// name both shapes, because a reconnect removes what the client owned before
// it writes what it owns now.
func TestManagedEntryNames_CoversModeSwitch(t *testing.T) {
	names := managedEntryNames("wiki", []string{"a", "b"})
	want := map[string]bool{"wiki": false, "wiki-a": false, "wiki-b": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, seen := range want {
		if !seen {
			t.Errorf("managedEntryNames does not cover %q: a %s entry would survive a mode switch", n, n)
		}
	}
}
