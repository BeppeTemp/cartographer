package main

// Tests for D120: client-side per-KB tool-name prefix discovery
// (resolveKBTargets, qualifyTool, callTool) and the guard against future
// direct administrative tool calls bypassing that seam.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func healthWithKBs(kbs ...client.HealthKB) *client.Health {
	list := append([]client.HealthKB{}, kbs...)
	return &client.Health{KBs: &list}
}

func TestQualifyTool(t *testing.T) {
	if got := qualifyTool("", "sync_pull"); got != "sync_pull" {
		t.Errorf("qualifyTool(\"\", ...) = %q, want unchanged base name", got)
	}
	if got := qualifyTool("ai_team", "sync_pull"); got != "ai_team__sync_pull" {
		t.Errorf("qualifyTool(prefix, ...) = %q, want ai_team__sync_pull", got)
	}
}

func TestResolveKBTargets(t *testing.T) {
	t.Run("unprefixed and prefixed KB, explicit selection (prefix differs from sanitized KB name)", func(t *testing.T) {
		health := healthWithKBs(
			client.HealthKB{Name: "alpha"},
			client.HealthKB{Name: "beta", ToolPrefix: "custom_name"},
		)
		targets, err := resolveKBTargets(health, []string{"alpha", "beta"})
		if err != nil {
			t.Fatalf("resolveKBTargets: %v", err)
		}
		want := []kbTarget{{Name: "alpha"}, {Name: "beta", ToolPrefix: "custom_name"}}
		if len(targets) != 2 || targets[0] != want[0] || targets[1] != want[1] {
			t.Fatalf("targets = %+v, want %+v", targets, want)
		}
	})

	t.Run("global kb-name mode: advertised prefix equals the KB name", func(t *testing.T) {
		health := healthWithKBs(client.HealthKB{Name: "wiki", ToolPrefix: "wiki"})
		targets, err := resolveKBTargets(health, []string{"wiki"})
		if err != nil || len(targets) != 1 || targets[0].ToolPrefix != "wiki" {
			t.Fatalf("targets = %+v, err=%v", targets, err)
		}
	})

	t.Run("legacy health without a kbs field: pre-D120 unprefixed behaviour preserved", func(t *testing.T) {
		targets, err := resolveKBTargets(&client.Health{}, []string{"alpha", "beta"})
		if err != nil {
			t.Fatalf("resolveKBTargets: %v", err)
		}
		want := []kbTarget{{Name: "alpha"}, {Name: "beta"}}
		if len(targets) != 2 || targets[0] != want[0] || targets[1] != want[1] {
			t.Fatalf("targets = %+v, want %+v", targets, want)
		}
	})

	t.Run("selected KB missing from current health: stale-selection error naming it", func(t *testing.T) {
		health := healthWithKBs(client.HealthKB{Name: "alpha"})
		_, err := resolveKBTargets(health, []string{"alpha", "gone"})
		if err == nil || !strings.Contains(err.Error(), `"gone"`) || !strings.Contains(err.Error(), "stale selection") {
			t.Fatalf("error = %v, want a stale-selection error naming %q", err, "gone")
		}
	})

	t.Run("malformed advertised prefix: protocol error, never sanitized or retried", func(t *testing.T) {
		health := healthWithKBs(client.HealthKB{Name: "alpha", ToolPrefix: "9bad"})
		_, err := resolveKBTargets(health, []string{"alpha"})
		if err == nil || !strings.Contains(err.Error(), "invalid tool_prefix") {
			t.Fatalf("error = %v, want an invalid tool_prefix error", err)
		}
	})

	t.Run("empty selection, exactly one advertised KB: bare endpoint qualified with its prefix", func(t *testing.T) {
		health := healthWithKBs(client.HealthKB{Name: "only", ToolPrefix: "only_prefix"})
		targets, err := resolveKBTargets(health, nil)
		if err != nil || len(targets) != 1 || targets[0].Name != "" || targets[0].ToolPrefix != "only_prefix" {
			t.Fatalf("targets = %+v, err=%v", targets, err)
		}
	})

	t.Run("empty selection, multiple advertised KBs: bare unqualified endpoint (server's own selection error surfaces)", func(t *testing.T) {
		health := healthWithKBs(client.HealthKB{Name: "alpha"}, client.HealthKB{Name: "beta", ToolPrefix: "beta"})
		targets, err := resolveKBTargets(health, nil)
		if err != nil || len(targets) != 1 || targets[0] != (kbTarget{}) {
			t.Fatalf("targets = %+v, err=%v, want one unqualified bare target", targets, err)
		}
	})

	t.Run("empty selection, no health metadata at all: bare unqualified endpoint", func(t *testing.T) {
		targets, err := resolveKBTargets(nil, nil)
		if err != nil || len(targets) != 1 || targets[0] != (kbTarget{}) {
			t.Fatalf("targets = %+v, err=%v", targets, err)
		}
	})
}

// prefixAwareMCPServer starts an httptest server whose /health advertises the
// given KBs (name -> tool_prefix, "" for unprefixed) and whose /mcp?kb=<name>
// endpoint only succeeds a tools/call whose "name" is exactly
// qualifyTool(prefix, wantBase) for that KB — any other name is answered with
// isError:true, "tool not found: <name>", mirroring the real server
// (Server.handleToolsCall) closely enough to prove the qualifier is what
// reached the wire.
func prefixAwareMCPServer(t *testing.T, prefixByKB map[string]string, wantBase string, resultFor func(kb string) string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			kbs := make([]map[string]any, 0, len(prefixByKB))
			for name, prefix := range prefixByKB {
				entry := map[string]any{"name": name}
				if prefix != "" {
					entry["tool_prefix"] = prefix
				}
				kbs = append(kbs, entry)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "kbs": kbs})
			return
		}
		kbName := r.URL.Query().Get("kb")
		var req struct {
			ID     int `json:"id"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		want := qualifyTool(prefixByKB[kbName], wantBase)
		if req.Params.Name != want {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"content": []map[string]string{{"type": "text", "text": "tool not found: " + req.Params.Name}},
				"isError": true,
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
			"content": []map[string]string{{"type": "text", "text": resultFor(kbName)}},
		}})
	}))
}

// TestFetchMergedManifest_QualifiesSyncPullPerKB proves fetchMergedManifest
// (WP2) calls sync_pull qualified with each KB's server-advertised prefix:
// one unprefixed KB, one with an explicit prefix that differs from its
// sanitized KB name.
func TestFetchMergedManifest_QualifiesSyncPullPerKB(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"alpha": "", "beta": "custom_name"}, "sync_pull", func(kb string) string {
		return `{"revision":"rev-` + kb + `","artifacts":[]}`
	})
	defer srv.Close()

	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", KBs: []string{"alpha", "beta"}}
	if _, err := fetchMergedManifest(cfg); err != nil {
		t.Fatalf("fetchMergedManifest: %v", err)
	}
}

// TestCmdReindex_QualifiesReindexPerKB proves the HTTP-owned branch of
// cmdReindex (WP2) calls the remote reindex tool qualified with each KB's
// server-advertised prefix.
func TestCmdReindex_QualifiesReindexPerKB(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"alpha": "", "beta": "custom_name"}, "reindex", func(kb string) string {
		return "indexed=0 updated=0 removed=0"
	})
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", KBs: []string{"alpha", "beta"}}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}

	var code int
	out := withStdout(t, func() { code = cmdReindex(nil) })
	if code != 0 {
		t.Fatalf("cmdReindex exit = %d, output=%s", code, out)
	}
	if !strings.Contains(out, "alpha:") || !strings.Contains(out, "beta:") {
		t.Fatalf("output = %q, want both KBs reported", out)
	}
}

// TestNoUnqualifiedProductionToolCalls guards the D120 seam: every
// Cartographer-owned direct administrative tools/call must be issued through
// callTool (qualifyTool), never a bare client.MCPClient.Call, so a future
// direct call cannot silently skip prefix discovery. multikb.go is exempt:
// it is where callTool itself is defined and legitimately calls c.Call.
func TestNoUnqualifiedProductionToolCalls(t *testing.T) {
	callRe := regexp.MustCompile(`[a-zA-Z0-9_]\.Call\(`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == "multikb.go" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if callRe.Match(data) {
			t.Errorf("%s calls MCPClient.Call directly; route direct administrative tool calls through callTool (D120) so prefix discovery cannot be bypassed", name)
		}
	}
}
