package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// D-new-bootstrap-sync-scope: `sync --if-older-than` is the session-start
// script's freshness gate. These tests drive cmdSync as the script does.

func syncStampHome(t *testing.T, serverURL string) string {
	t.Helper()
	dir := t.TempDir()
	setHome(t, dir)
	cfg := &clientconfig.Config{ServerURL: serverURL, ServerName: "wiki", Token: "${TOKEN}", Agents: []string{"claude"}, KnownKBs: []string{"alpha"}}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A fresh stamp ends the run before the client lock and any network call: the
// lock is held by someone else and the server does not exist.
func TestSyncIfOlderThan_FreshStampTakesNoLockNoNetwork(t *testing.T) {
	dir := syncStampHome(t, "http://127.0.0.1:1/mcp")
	stamp := syncStampPath(dir, []string{"claude"})
	touchSyncStamp(dir, stamp, version)
	release, err := provisioning.LockClientState(dir, provisioning.DefaultClientLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	done := make(chan int, 1)
	go func() { done <- cmdSync([]string{"--client", "claude", "--if-older-than", "10m"}) }()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("fresh stamp: exit %d, want 0", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a fresh stamp still waited for the client lock")
	}
}

// A stale stamp runs the sync; a failed sync leaves the stamp as it was, a
// successful one moves it.
func TestSyncIfOlderThan_StaleRunsAndOnlySuccessStamps(t *testing.T) {
	dir := syncStampHome(t, "http://127.0.0.1:1/mcp")
	stamp := syncStampPath(dir, []string{"claude"})
	touchSyncStamp(dir, stamp, version)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stamp, old, old); err != nil {
		t.Fatal(err)
	}
	if code := cmdSync([]string{"--client", "claude", "--if-older-than", "10m"}); code == 0 {
		t.Fatal("sync against a dead server succeeded")
	}
	if info, err := os.Stat(stamp); err != nil || time.Since(info.ModTime()) < 30*time.Minute {
		t.Fatalf("a failed sync moved the stamp (%v, %v)", info, err)
	}

	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ServerURL = srv.URL + "/mcp"
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if code := cmdSync([]string{"--client", "claude", "--if-older-than", "10m"}); code != 0 {
		t.Fatalf("sync = %d", code)
	}
	if !syncStampFresh(stamp, 10*time.Minute, version) {
		t.Error("a successful sync did not stamp")
	}
}

// A client baked into the script that is not connected here must not turn the
// sync into a silent error: it falls back to every connected provider.
func TestSyncIfOlderThan_NotConnectedClientFallsBackToPlainSync(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := syncStampHome(t, srv.URL+"/mcp")
	if code := cmdSync([]string{"--client", "codex"}); code != 2 {
		t.Fatalf("without the gate a not-connected client stays an error, got %d", code)
	}
	if code := cmdSync([]string{"--client", "codex", "--if-older-than", "10m"}); code != 0 {
		t.Fatalf("gated sync of a not-connected client = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude.json")); err != nil {
		t.Errorf("the connected provider was not synced: %v", err)
	}
}

func TestSyncStampPath_RejectsOddNames(t *testing.T) {
	if p := syncStampPath("/d", []string{"../x"}); p != "" {
		t.Errorf("odd name gave %q", p)
	}
	if a, b := syncStampPath("/d", []string{"b", "a"}), syncStampPath("/d", []string{"a", "b"}); a != b || a == "" {
		t.Errorf("order matters: %q %q", a, b)
	}
}

// A stamp of another binary version is stale, so a brew upgrade's repair runs.
func TestSyncStamp_OtherVersionIsStale(t *testing.T) {
	dir := t.TempDir()
	stamp := syncStampPath(dir, []string{"claude"})
	touchSyncStamp(dir, stamp, "1.0.0")
	if !syncStampFresh(stamp, time.Minute, "1.0.0") || syncStampFresh(stamp, time.Minute, "1.0.1") {
		t.Error("the stamp must be fresh for its own version only")
	}
}

// A run that syncs nothing does not stamp.
func TestSyncIfOlderThan_NoAgentConnectedWritesNoStamp(t *testing.T) {
	dir := t.TempDir()
	setHome(t, dir)
	if err := clientconfig.Save(dir, &clientconfig.Config{ServerURL: "http://127.0.0.1:1/mcp", ServerName: "wiki"}); err != nil {
		t.Fatal(err)
	}
	if code := cmdSync([]string{"--if-older-than", "10m"}); code != 0 {
		t.Fatalf("sync = %d", code)
	}
	if _, err := os.Stat(syncStampPath(dir, nil)); err == nil {
		t.Error("a run that synced nothing wrote the stamp")
	}
}

// Sessions started together all pass the check before the lock: the one that
// syncs stamps before releasing it, the others find it fresh and do not sync.
func TestSyncIfOlderThan_ConcurrentRunsSyncOnce(t *testing.T) {
	var pulls atomic.Int32
	inner := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"}]}`)
	defer inner.Close()
	counter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/health") {
			pulls.Add(1)
		}
		http.Redirect(w, r, inner.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer counter.Close()
	dir := syncStampHome(t, inner.URL+"/mcp")
	cfg, _ := clientconfig.Load(dir)
	cfg.ServerURL = counter.URL + "/mcp"
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	// One full sync's probes are the baseline.
	if code := cmdSync([]string{"--client", "claude"}); code != 0 {
		t.Fatalf("baseline sync = %d", code)
	}
	perSync := pulls.Swap(0)
	if perSync == 0 {
		t.Fatal("the counting server saw no probe")
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmdSync([]string{"--client", "claude", "--if-older-than", "10m"})
		}()
	}
	wg.Wait()
	if got := pulls.Load(); got != perSync {
		t.Errorf("health probes = %d, want %d (one full sync)", got, perSync)
	}
}

// A run limited by --client keeps the KBs it did not reconcile for the other
// providers: their later syncs remove stale entries by known_kbs.
func TestSyncClientRun_KeepsPreviousKnownKBs(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	setHome(t, dir)
	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", ServerName: "wiki", Token: "${TOKEN}", Agents: []string{"claude", "codex"}, KnownKBs: []string{"alpha", "beta"}}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if code := cmdSync([]string{"--client", "claude"}); code != 0 {
		t.Fatalf("scoped sync = %d", code)
	}
	got, err := clientconfig.Load(dir)
	if err != nil || strings.Join(got.KnownKBs, ",") != "alpha,beta" {
		t.Fatalf("scoped sync known_kbs = %v, err=%v; want alpha,beta", got.KnownKBs, err)
	}
	if code := cmdSync(nil); code != 0 {
		t.Fatalf("full sync = %d", code)
	}
	got, _ = clientconfig.Load(dir)
	if strings.Join(got.KnownKBs, ",") != "alpha" {
		t.Errorf("a full sync must reconcile known_kbs, got %v", got.KnownKBs)
	}
}

// runSync releases the client lock early, before its reports: the stamp must be
// written by then, or a session queued on the lock would run a second sync. The
// usage scan runs right after that release.
func TestSyncIfOlderThan_StampIsWrittenBeforeTheLockIsReleased(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := syncStampHome(t, srv.URL+"/mcp")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.UsageScan = true
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	stamp := syncStampPath(dir, []string{"claude"})
	scans, stampedAtRelease, lockFree := 0, false, false
	orig := scanUsage
	scanUsage = func(lf provisioning.LockFile, base string, w time.Duration, all bool) (map[string]provisioning.ArtifactUsage, error) {
		scans++
		stampedAtRelease = syncStampFresh(stamp, 10*time.Minute, version)
		if release, err := provisioning.LockClientState(dir, 200*time.Millisecond); err == nil {
			lockFree = true
			_ = release()
		}
		return nil, nil
	}
	t.Cleanup(func() { scanUsage = orig })
	if code := cmdSync([]string{"--client", "claude", "--if-older-than", "10m"}); code != 0 {
		t.Fatalf("sync = %d", code)
	}
	if scans != 1 || !lockFree || !stampedAtRelease {
		t.Errorf("scans=%d lockFree=%v stampedAtRelease=%v: the stamp must exist once the lock is released", scans, lockFree, stampedAtRelease)
	}
}
