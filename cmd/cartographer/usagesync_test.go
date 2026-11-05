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
	cfg.ServerURL, cfg.Auth, cfg.Token = srv.URL+"/mcp", true, "${CARTOGRAPHER_TEST_TOKEN}"
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

func TestReportOnly_SendsToTheGivenURLAndChangesNothing(t *testing.T) {
	home := usageClientHome(t)
	setHome(t, home)
	var mu sync.Mutex
	paths := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	cfg := clientconfig.Default()
	cfg.ServerURL = "http://configured.example.com/mcp"
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(clientconfig.Path(home))
	if err != nil {
		t.Fatal(err)
	}

	if code := reportOnly(home, srv.URL+"/mcp"); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	mu.Lock()
	got := paths["/api/client-settings"]
	usage := paths["/api/usage"]
	mu.Unlock()
	if got != 1 {
		t.Fatalf("client settings posted %d times, want 1 (%v)", got, paths)
	}
	if usage != 1 {
		t.Fatalf("usage posted %d times, want 1 for kb-a (%v)", usage, paths)
	}
	after, err := os.ReadFile(clientconfig.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("client config changed:\n%s\n---\n%s", before, after)
	}
}

func TestReportOnly_RefusesAServerOffThisMachine(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := clientconfig.Save(home, clientconfig.Default()); err != nil {
		t.Fatal(err)
	}
	if code := reportOnly(home, "https://example.com/mcp"); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestReportUsageThrottled_ScansOncePerWindow(t *testing.T) {
	home := usageClientHome(t)
	srv, _ := usageServer(t, http.StatusOK)
	cfg := clientconfig.Default()
	cfg.ServerURL = srv.URL + "/mcp"
	scans := 0
	orig := scanUsage
	scanUsage = func(lf provisioning.LockFile, base string, w time.Duration, all bool) (map[string]provisioning.ArtifactUsage, error) {
		scans++
		return orig(lf, base, w, all)
	}
	t.Cleanup(func() { scanUsage = orig })

	reportUsageThrottled(cfg, home)
	reportUsageThrottled(cfg, home)
	if scans != 1 {
		t.Fatalf("%d scans inside the window, want 1", scans)
	}
	old := time.Now().Add(-2 * usageScanInterval)
	if err := os.Chtimes(filepath.Join(home, usageStampName), old, old); err != nil {
		t.Fatal(err)
	}
	reportUsageThrottled(cfg, home)
	if scans != 2 {
		t.Fatalf("%d scans after the window, want 2", scans)
	}
}

// #1673: a stamp slightly ahead of the clock (a step back, a filesystem that
// stamps ahead of time.Now) is still the claim just taken, not a stale one:
// treating it as stale let a concurrent session scan a second time.
func TestClaimUsageScan_StampSlightlyInTheFutureIsFresh(t *testing.T) {
	dir := t.TempDir()
	if !claimUsageScan(dir) {
		t.Fatal("first claim refused")
	}
	ahead := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, usageStampName), ahead, ahead); err != nil {
		t.Fatal(err)
	}
	if claimUsageScan(dir) {
		t.Fatal("a stamp 2s ahead of the clock was treated as stale")
	}
	far := time.Now().Add(2 * usageScanInterval)
	if err := os.Chtimes(filepath.Join(dir, usageStampName), far, far); err != nil {
		t.Fatal(err)
	}
	if !claimUsageScan(dir) {
		t.Fatal("a stamp far in the future must not block scans forever")
	}
}

// The usage scan runs after the client lock is released: a second sync that
// waits for the lock is not made to wait for the scan.
func TestRunSync_ReleasesLockBeforeUsageScan(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"one": ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, "solo")
	})
	defer srv.Close()
	dir := t.TempDir()
	cfg := &clientconfig.Config{
		ServerURL: srv.URL + "/mcp", ServerName: "cartographer",
		Agents: []string{"claude"}, KnownKBs: []string{"one"}, UsageScan: true,
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	scanned := false
	var lockErr error
	orig := scanUsage
	scanUsage = func(lf provisioning.LockFile, base string, w time.Duration, all bool) (map[string]provisioning.ArtifactUsage, error) {
		scanned = true
		var release func() error
		release, lockErr = provisioning.LockClientState(dir, 50*time.Millisecond)
		if lockErr == nil {
			_ = release()
		}
		return nil, nil
	}
	t.Cleanup(func() { scanUsage = orig })
	withStdout(t, func() {
		if _, err := runSync(dir, cfg, syncOptions{}); err != nil {
			t.Fatalf("sync: %v", err)
		}
	})
	if !scanned {
		t.Fatal("the usage scan did not run")
	}
	if lockErr != nil {
		t.Fatalf("the client lock was still held during the scan: %v", lockErr)
	}
}

// Sessions starting together claim the window before scanning: the scan runs
// once, not once per session that saw a stale stamp.
func TestReportUsageThrottled_ConcurrentCallsScanOnce(t *testing.T) {
	home := usageClientHome(t)
	srv, _ := usageServer(t, http.StatusOK)
	cfg := clientconfig.Default()
	cfg.ServerURL = srv.URL + "/mcp"
	var mu sync.Mutex
	scans := 0
	orig := scanUsage
	scanUsage = func(lf provisioning.LockFile, base string, w time.Duration, all bool) (map[string]provisioning.ArtifactUsage, error) {
		mu.Lock()
		scans++
		mu.Unlock()
		time.Sleep(100 * time.Millisecond) // a slow scan: the others arrive meanwhile
		return orig(lf, base, w, all)
	}
	t.Cleanup(func() { scanUsage = orig })
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); reportUsageThrottled(cfg, home) }()
	}
	wg.Wait()
	if scans != 1 {
		t.Fatalf("%d scans for concurrent calls, want 1", scans)
	}
}
