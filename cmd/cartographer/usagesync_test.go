package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// usageClientHome is a client home with a lockfile managing a skill from
// "kb-a" and one bundled skill for claude, and a transcript that uses both.
func usageClientHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{"claude": {Provider: "claude", Managed: []provisioning.ManagedFile{
		{Kind: "skill", Name: "ops-tool", Source: "kb:kb-a", Path: ".claude/skills/ops-tool/SKILL.md"},
		{Kind: "skill", Name: "bundled-tool", Source: "bundle", Path: ".claude/skills/bundled-tool/SKILL.md"},
	}}}}
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := func(skill string) string {
		b, _ := json.Marshal(map[string]any{"type": "assistant", "timestamp": at, "message": map[string]any{"content": []map[string]any{
			{"type": "tool_use", "name": "Skill", "input": map[string]any{"skill": skill}}}}})
		return string(b) + "\n"
	}
	dir := filepath.Join(home, ".claude", "projects", "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(line("ops-tool")+line("bundled-tool")), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

type usagePost struct {
	kb      string
	auth    string
	entries []provisioning.ArtifactUsage
}

func usageServer(t *testing.T, status int) (*httptest.Server, func() []usagePost) {
	t.Helper()
	var mu sync.Mutex
	var got []usagePost
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p := usagePost{kb: r.URL.Query().Get("kb"), auth: r.Header.Get("Authorization")}
		_ = json.Unmarshal(body, &p.entries)
		if r.URL.Path == "/api/usage" && r.Method == http.MethodPost {
			mu.Lock()
			got = append(got, p)
			mu.Unlock()
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []usagePost { mu.Lock(); defer mu.Unlock(); return append([]usagePost(nil), got...) }
}

func TestReportUsage_PostsOnlyTheAggregatePerKB(t *testing.T) {
	home := usageClientHome(t)
	srv, posts := usageServer(t, http.StatusOK)
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "secret")
	cfg := clientconfig.Default()
	cfg.ServerURL, cfg.Auth, cfg.TokenEnv = srv.URL+"/mcp", true, "CARTOGRAPHER_TEST_TOKEN"
	if n := reportUsage(cfg, home); n != 1 {
		t.Fatalf("reported %d entries, want 1 (the bundled skill is not a KB's)", n)
	}
	got := posts()
	if len(got) != 1 || got[0].kb != "kb-a" || got[0].auth != "Bearer secret" || len(got[0].entries) != 1 ||
		got[0].entries[0].Name != "ops-tool" || got[0].entries[0].Count != 1 || got[0].entries[0].Provider != "claude" {
		t.Fatalf("posts = %+v", got)
	}
}

func TestReportUsage_OptOutReadsAndSendsNothing(t *testing.T) {
	home := usageClientHome(t)
	srv, posts := usageServer(t, http.StatusOK)
	cfg := clientconfig.Default()
	cfg.ServerURL, cfg.UsageScan = srv.URL+"/mcp", false
	if n := reportUsage(cfg, home); n != 0 || len(posts()) != 0 {
		t.Fatalf("usage_scan: false still reported (%d, %+v)", n, posts())
	}
}

// An older server has no route: the report fails quietly and the sync goes on.
func TestReportUsage_OlderServerIsNotAnError(t *testing.T) {
	home := usageClientHome(t)
	srv, _ := usageServer(t, http.StatusNotFound)
	cfg := clientconfig.Default()
	cfg.ServerURL = srv.URL + "/mcp"
	if n := reportUsage(cfg, home); n != 0 {
		t.Fatalf("reported %d, want 0 on a 404", n)
	}
}

func TestReportUsage_UnreachableServerIsNotAnError(t *testing.T) {
	home := usageClientHome(t)
	srv, _ := usageServer(t, http.StatusOK)
	url := srv.URL
	srv.Close()
	cfg := clientconfig.Default()
	cfg.ServerURL = url + "/mcp"
	if n := reportUsage(cfg, home); n != 0 {
		t.Fatalf("reported %d, want 0", n)
	}
}
