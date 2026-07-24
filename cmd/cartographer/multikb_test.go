package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
			got, present, err := enumerateKBs(srv.URL+"/mcp", false, "")
			if err != nil || present != tc.present || strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("enumerateKBs = (%v, %v, %v), want (%v, %v, nil)", got, present, err, tc.want, tc.present)
			}
		})
	}
}

func TestDoConnect_PerKBEntries_AllProviders(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"},{"name":"beta"},{"name":"gamma"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	providers := []string{"claude", "codex", "kiro", "opencode"}
	res, err := doConnect(connectOptions{Providers: providers, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", TokenEnv: "TOKEN", Trust: true})
	if err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	if got, want := strings.Join(res.MCPEntries, ","), "cartographer-alpha,cartographer-beta,cartographer-gamma"; got != want {
		t.Fatalf("MCPEntries = %q, want %q", got, want)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil || strings.Join(cfg.KBs, ",") != "alpha,beta,gamma" {
		t.Fatalf("persisted KBs = %v, err=%v", cfg.KBs, err)
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

func TestEntriesForKBs_SingleStaysBare(t *testing.T) {
	entries, err := entriesForKBs("wiki", "https://example.test/mcp", []string{"only"})
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
	entries, err := entriesForKBs("wiki", "https://example.test/mcp", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyMCPEntries(entries, []string{"claude", "codex", "kiro", "opencode"}, dir, false, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := removeMCPEntries("wiki", []string{"a", "b"}, []string{"claude", "codex", "kiro", "opencode"}, dir, false, "", false); err != nil {
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

	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", ServerName: "wiki", TokenEnv: "TOKEN", Agents: []string{"claude"}, KBs: []string{"alpha"}, Trust: true}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	bare, _ := entriesForKBs("wiki", cfg.ServerURL, cfg.KBs)
	if _, err := applyMCPEntries(bare, cfg.Agents, dir, false, "", false); err != nil {
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
	if err != nil || strings.Join(updated.KBs, ",") != "alpha,beta" {
		t.Fatalf("1→2 persisted KBs = %v, err=%v", updated.KBs, err)
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
	cfg := &clientconfig.Config{ServerURL: "https://example.test/mcp", ServerName: "wiki", Agents: []string{"claude"}, KBs: []string{"alpha", "beta"}, Trust: true}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	entries, _ := entriesForKBs("wiki", cfg.ServerURL, cfg.KBs)
	if _, err := applyMCPEntries(entries, cfg.Agents, dir, false, "", false); err != nil {
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
	cfg := &clientconfig.Config{ServerURL: "http://127.0.0.1:1/mcp", ServerName: "wiki", Agents: []string{"claude"}, KBs: []string{"alpha", "beta"}, Trust: true}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	entries, _ := entriesForKBs("wiki", cfg.ServerURL, cfg.KBs)
	if _, err := applyMCPEntries(entries, cfg.Agents, dir, false, "", false); err != nil {
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
