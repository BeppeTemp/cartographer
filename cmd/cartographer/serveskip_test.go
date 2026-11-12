package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
)

// #1963: a KB declared in kbs[] whose path is missing, or whose remote cannot be
// cloned, is skipped and reported; the healthy KB next to it still mounts.
func TestBootstrapSkipsAKBThatCannotBeMountedOrCloned(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	buf := captureLog(t)
	data := t.TempDir()
	if _, err := kb.Init(filepath.Join(data, "kb-ok")); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Data = data
	cfg.HTTP = "127.0.0.1:0"
	cfg.KBs = []config.KBSpec{
		{Name: "kb-moved", Path: filepath.Join(t.TempDir(), "gone")},
		{Name: "kb-remote", Remote: fileURL(filepath.Join(t.TempDir(), "nowhere.git"))},
	}
	t.Setenv("K_SERVICE", "")

	bs := bootstrapKBs(cfg, func(string) {})
	t.Cleanup(func() {
		for _, ix := range bs.sqlIdxs {
			ix.Close()
		}
	})
	if len(bs.names) != 1 || bs.names[0] != "kb-ok" {
		t.Fatalf("mounted %v, want only kb-ok", bs.names)
	}
	moved, ok := bs.skipped.Lookup("kb-moved")
	if !ok || !strings.Contains(moved.Reason, "path missing: moved? update kbs[].path") || moved.Retrying {
		t.Errorf("kb-moved = %+v", moved)
	}
	if strings.Contains(moved.Reason, "gone") {
		t.Errorf("the reason shown to clients names a server path: %q", moved.Reason)
	}
	remote, ok := bs.skipped.Lookup("kb-remote")
	if !ok || !remote.Retrying || !strings.Contains(remote.Reason, "clone failed") || strings.Contains(remote.Reason, data) || strings.Contains(remote.Reason, "nowhere") {
		t.Errorf("kb-remote = %+v", remote)
	}
	if len(bs.retries) != 1 || bs.retries[0].name != "kb-remote" {
		t.Errorf("retries = %+v, want kb-remote", bs.retries)
	}
	for _, want := range []string{`ERROR: KB "kb-moved"`, `ERROR: KB "kb-remote"`, "is skipped"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, buf.String())
		}
	}
}

// A clone that failed is attempted again with back-off, the reason follows the
// last failure, and once it succeeds the server is asked to restart to mount it.
func TestRetryClonesRestartsOnceTheCloneWorks(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	captureLog(t)
	oldFirst, oldMax := cloneRetryFirst, cloneRetryMax
	cloneRetryFirst, cloneRetryMax = 5*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { cloneRetryFirst, cloneRetryMax = oldFirst, oldMax })

	tmp := t.TempDir()
	data := filepath.Join(tmp, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(tmp, "kb-remote.git")
	kbSourceWithBare(t, filepath.Join(tmp, "src"), bare)
	if err := os.Rename(bare, bare+".later"); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Data = data
	spec := config.KBSpec{Name: "kb-remote", Remote: fileURL(bare)}
	skipped := mcpserver.NewSkippedKBs()
	skipped.Set(mcpserver.SkippedKB{Name: "kb-remote", Reason: "first", Retrying: true})
	restarted := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		retryClones(t.Context(), cfg, []cloneRetry{{spec: spec, name: "kb-remote"}}, skipped, func(string) { restarted <- struct{}{} })
		close(done)
	}()

	// The remote is still away: the retry fails and updates the reason.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if row, _ := skipped.Lookup("kb-remote"); row.Reason != "first" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the retry never ran")
		}
		time.Sleep(2 * time.Millisecond)
	}
	select {
	case <-restarted:
		t.Fatal("restart requested while the clone still fails")
	default:
	}

	// The network is back: the next attempt clones and asks for the restart.
	if err := os.Rename(bare+".later", bare); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-time.After(10 * time.Second):
		t.Fatal("no restart after the clone succeeded")
	}
	<-done
	if _, err := os.Stat(filepath.Join(data, "kb-remote", ".git")); err != nil {
		t.Errorf("the KB was not cloned: %v", err)
	}
}

func kbSourceWithBare(t *testing.T, srcDir, bareDir string) {
	t.Helper()
	if _, err := kb.Init(srcDir); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, "", "init", "--bare", bareDir)
	mustRunGit(t, srcDir, "remote", "add", "origin", bareDir)
	mustRunGit(t, srcDir, "push", "origin", "HEAD")
	mustRunGit(t, bareDir, "symbolic-ref", "HEAD", strings.TrimSpace(gitOut(t, srcDir, "symbolic-ref", "HEAD")))
}

func TestStatusAndDoctorReportASkippedKB(t *testing.T) {
	s := statusSnapshot{Schema: statusSchema, Reachable: true, Server: "v1", Client: "v1", ServerURL: "http://127.0.0.1:1/mcp",
		SkippedKBs: []client.SkippedKB{{Name: "kb-moved", Reason: "path missing: moved? update kbs[].path"}}}
	out := withStdout(t, func() { printServerLines(os.Stdout, os.Stderr, s) })
	if !strings.Contains(out, "KB not mounted: kb-moved — path missing: moved? update kbs[].path") {
		t.Errorf("status lacks the skipped KB:\n%s", out)
	}

	facts := serverFacts{health: &client.Health{SkippedKBs: s.SkippedKBs}}
	got := checkSkippedKBs(t.TempDir(), facts, nil)
	if len(got) != 1 || got[0].Severity != doctorWarning || !strings.Contains(got[0].Message, "kb-moved") {
		t.Errorf("doctor findings = %+v", got)
	}
	if got := checkSkippedKBs(t.TempDir(), facts, os.ErrNotExist); len(got) != 0 {
		t.Errorf("unreachable server: findings = %+v, want none", got)
	}
}

// A server run by hand is not restarted by anyone: it says a restart is needed
// and keeps serving; a supervised one asks for the restart.
func TestRestartToMountOnlyUnderASupervisor(t *testing.T) {
	buf := captureLog(t)
	ch := make(chan struct{}, 1)
	requestRestartToMount("kb-remote", false, ch)
	if len(ch) != 0 || !strings.Contains(buf.String(), "restart the server to mount it") {
		t.Errorf("by hand: restart requested=%d, log:\n%s", len(ch), buf.String())
	}
	requestRestartToMount("kb-remote", true, ch)
	if len(ch) != 1 || !strings.Contains(buf.String(), "the server restarts to mount it") {
		t.Errorf("supervised: restart requested=%d, log:\n%s", len(ch), buf.String())
	}

	none := func(string) string { return "" }
	env := func(k, v string) func(string) string {
		return func(g string) string {
			if g == k {
				return v
			}
			return ""
		}
	}
	for name, tc := range map[string]struct {
		getenv func(string) string
		want   bool
	}{
		"terminal":          {none, false},
		"macOS tty":         {env("XPC_SERVICE_NAME", "0"), false},
		"launchd (old)":     {env("XPC_SERVICE_NAME", "io.example.cartographer"), true},
		"systemd (old)":     {env("INVOCATION_ID", "abc"), true},
		"kubernetes":        {env("KUBERNETES_SERVICE_HOST", "10.0.0.1"), true},
		"marker":            {env(supervisedEnv, "1"), true},
		"marker off":        {env(supervisedEnv, "0"), false},
		"marker beats heur": {func(k string) string { return map[string]string{supervisedEnv: "0", "INVOCATION_ID": "x"}[k] }, false},
	} {
		if got := supervisedProcess(tc.getenv); got != tc.want {
			t.Errorf("%s: supervised = %v, want %v", name, got, tc.want)
		}
	}
}

// With `data` configured over HTTP, a sole KB that cannot be mounted leaves the
// server up with no KB (a fresh empty data directory is the same state), where
// without `data` it stops. docs/deployment.md says exactly this.
func TestBootstrapSoleSkippedKBWithDataLeavesTheServerUp(t *testing.T) {
	captureLog(t)
	cfg := config.Default()
	cfg.Data = t.TempDir()
	cfg.HTTP = "127.0.0.1:0"
	cfg.KBs = []config.KBSpec{{Name: "kb-moved", Path: filepath.Join(t.TempDir(), "gone")}}
	t.Setenv("K_SERVICE", "")

	bs := bootstrapKBs(cfg, func(string) {})
	if len(bs.names) != 0 {
		t.Fatalf("mounted %v, want none", bs.names)
	}
	if _, ok := bs.skipped.Lookup("kb-moved"); !ok {
		t.Errorf("the sole KB is not reported as skipped")
	}
}
