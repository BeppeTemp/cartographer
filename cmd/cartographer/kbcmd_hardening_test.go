package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// stubRestart swaps the --restart seams for the test.
func stubRestart(t *testing.T, installed bool) *int32 {
	t.Helper()
	var restarts int32
	origRestart, origInstalled := restartServiceFn, serviceInstalledFn
	restartServiceFn = func() error { atomic.AddInt32(&restarts, 1); return nil }
	serviceInstalledFn = func(string) bool { return installed }
	t.Cleanup(func() { restartServiceFn, serviceInstalledFn = origRestart, origInstalled })
	return &restarts
}

func guidanceConfig(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("http: "+strings.TrimPrefix(srv.URL, "http://")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// An explicit --restart is honoured whatever /health says first: a server that
// fails once and then reports bootstrapping used to end the command before the
// flag was read, with no restart and no message.
func TestPrintPostCreateGuidance_RestartRunsWhenHealthFailsOrBootstraps(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch n := atomic.AddInt32(&calls, 1); {
		case n == 1:
			http.Error(w, "down", http.StatusServiceUnavailable)
		case n == 2:
			_, _ = w.Write([]byte(`{"status":"ok","bootstrapping":true}`))
		default:
			_, _ = w.Write([]byte(`{"status":"ok","started_at":"new"}`))
		}
	}))
	defer srv.Close()
	restarts := stubRestart(t, true)

	out := withStdout(t, func() { printPostCreateGuidance(guidanceConfig(t, srv), true) })
	if atomic.LoadInt32(restarts) != 1 {
		t.Errorf("restarts = %d, want 1", *restarts)
	}
	if !strings.Contains(out, "restarting it anyway") || !strings.Contains(out, "service healthy") {
		t.Errorf("output must say what was done:\n%s", out)
	}
}

// With nothing installed there is nothing to restart, and the operator is told.
func TestPrintPostCreateGuidance_RestartWithoutAServiceSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	restarts := stubRestart(t, false)

	stderr := withStderr(t, func() { printPostCreateGuidance(guidanceConfig(t, srv), true) })
	if atomic.LoadInt32(restarts) != 0 {
		t.Error("restarted a service that is not installed")
	}
	if !strings.Contains(stderr, "nothing was restarted") {
		t.Errorf("stderr = %q, want the reason", stderr)
	}
}

// A failed `git remote add` is not "only the push failed": no origin exists, so
// the scaffold is discarded and no push command is printed.
func TestAttachOrigin_AddRemoteFailureDiscards(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not in PATH")
	}
	notARepo := t.TempDir()
	var res originResult
	stderr := withStderr(t, func() {
		res = attachOrigin(notARepo, "https://git.example.com/org/kb-a.git", "kb-a", "", "User", "user@example.com", false, "", "")
	})
	if res.code == 0 || !res.discard {
		t.Errorf("result = %+v, want a failure with discard", res)
	}
	if strings.Contains(stderr, "push -u") {
		t.Errorf("a push command was printed for an origin that does not exist:\n%s", stderr)
	}
}

// A path with a space, an apostrophe and a dollar sign is one word in the
// printed command, whatever shell reads it.
func TestHintQuoteRoundTripsThroughSh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX quoting")
	}
	for _, p := range []string{"/data/My KBs/kb-a", "/data/it's/kb", "$HOME/kb;rm -rf x", "/plain/kb-a", ""} {
		out, err := exec.Command("sh", "-c", "printf %s "+hintQuote("linux", p)).Output()
		if err != nil || string(out) != p {
			t.Errorf("hintQuote(%q) read back as %q (%v)", p, out, err)
		}
	}
	if got := hintQuote("linux", "/plain/kb-a"); got != "/plain/kb-a" {
		t.Errorf("a plain path must stay unquoted, got %q", got)
	}
	if got := hintQuote("windows", "C:\\My KBs\\it's"); got != "'C:\\My KBs\\it''s'" {
		t.Errorf("windows quoting = %q", got)
	}
	if cmd := removeDirCommand("linux", "/data/My KBs/kb-a"); cmd != "rm -rf '/data/My KBs/kb-a'" {
		t.Errorf("removeDirCommand = %q", cmd)
	}
}

// `kb` commands act on the local service: a client configured for a remote
// server must not decide which server they ask or restart.
func TestServerBaseURL_IgnoresTheClientsRemoteServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg := clientconfig.Default()
	cfg.ServerURL = "https://kb.example.com/mcp"
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "no-service.yaml")
	if got := serverBaseURL(missing); got != "http://127.0.0.1:39273" {
		t.Errorf("serverBaseURL = %q, want the local default", got)
	}
}

// kb create --remote refuses a remote that carries a credential, as kb attach
// does, before anything is written (#1851).
func TestKBCreateRefusesARemoteWithACredential(t *testing.T) {
	for _, remote := range []string{
		"https://user:" + attachSecret + "@example.com/x.git",
		"https://" + attachSecret + "@example.com/org/repo.git",
		"https://user:p/ss@example.com/x.git",
		"ssh://user:" + attachSecret + "@example.com/x.git",
	} {
		dataDir := t.TempDir()
		var code int
		errOut := withStderr(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", remote})
			})
		})
		if code != 2 {
			t.Errorf("%q: code = %d, want 2", remote, code)
		}
		if strings.Contains(errOut, attachSecret) || !strings.Contains(errOut, "credentials directory") {
			t.Errorf("%q: message must point to the credentials and not echo the secret: %q", remote, errOut)
		}
		if entries, _ := os.ReadDir(dataDir); len(entries) != 0 {
			t.Errorf("%q: refusal left %d entries in the data dir", remote, len(entries))
		}
	}
}
