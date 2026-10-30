package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const listerSecret = "lister-secret-4d2b"

// sourceCfg writes a worker config with one exec recipe whose lister is the test
// binary itself (see TestMain); mode is "ok" or "fail".
func sourceCfg(t *testing.T, mode, serverURL string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LISTER_VAR", listerSecret)
	if err := os.WriteFile(filepath.Join(dir, "feed.md"), []byte("An entry is a page."), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `name: docs-night
server_url: ` + serverURL + `/mcp
token_env: WORKER_TOKEN
kbs: [kb-a, work-kb]
models:
  - name: cloud
    provider: anthropic
    model: some-model
    api_key_env: CLOUD_API_KEY
    budget: {daily_tokens: 1000}
schedule:
  window: {start: "23:00", end: "06:00", timezone: UTC}
tasks:
  - kind: poll
    model: cloud
recipes:
  my-feed:
    instructions_file: feed.md
    exec:
      command: ` + os.Args[0] + `
      args: ["--exec-helper", "` + mode + `"]
      env: {LISTER_VALUE: LISTER_VAR}
`
	p := filepath.Join(dir, "worker.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func runSource(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	stderr = withStderr(t, func() { stdout = withStdout(t, func() { code = cmdWorker(append([]string{"source"}, args...)) }) })
	return
}

func TestWorkerSourceDispatchAndUsage(t *testing.T) {
	if code, _, _ := runSource(t); code != 2 {
		t.Errorf("no subcommand: exit %d", code)
	}
	code, out, _ := runSource(t, "help")
	if code != 0 || !strings.Contains(out, "worker source test") {
		t.Errorf("help: %d %q", code, out)
	}
	if code, _, errs := runSource(t, "bogus"); code != 2 || !strings.Contains(errs, "bogus") {
		t.Errorf("unknown: %d %q", code, errs)
	}
	if code, _, _ := runSource(t, "test"); code != 2 {
		t.Errorf("test with no flag: exit %d", code)
	}
	if code := withExit(t, func() int { return cmdWorker(nil) }); code != 2 {
		t.Errorf("worker alone: exit %d", code)
	}
}

func withExit(t *testing.T, f func() int) (code int) {
	t.Helper()
	withStderr(t, func() { code = f() })
	return
}

func TestWorkerSourceFlagRules(t *testing.T) {
	cfg := sourceCfg(t, "ok", "http://127.0.0.1:1")
	for name, args := range map[string][]string{
		"both modes":      {"test", "--config", cfg, "--source", "s/x", "--recipe", "my-feed", "--locator", "l"},
		"neither":         {"test", "--config", cfg},
		"no locator":      {"test", "--config", cfg, "--recipe", "my-feed"},
		"kb with recipe":  {"test", "--config", cfg, "--recipe", "my-feed", "--locator", "l", "--kb", "kb-a"},
		"include source":  {"test", "--config", cfg, "--source", "s/x", "--include", "a"},
		"positional":      {"test", "--config", cfg, "--recipe", "my-feed", "--locator", "l", "extra"},
		"unknown recipe":  {"test", "--config", cfg, "--recipe", "ghost", "--locator", "l"},
		"missing config":  {"test", "--config", filepath.Join(t.TempDir(), "nope.yaml"), "--recipe", "my-feed", "--locator", "l"},
		"several kbs":     {"test", "--config", cfg, "--source", "s/x"},
		"kb not in file":  {"test", "--config", cfg, "--source", "s/x", "--kb", "other"},
		"no token in env": {"test", "--config", cfg, "--source", "s/x", "--kb", "kb-a"},
	} {
		t.Run(name, func(t *testing.T) {
			if code, _, errs := runSource(t, args...); code != 2 || errs == "" {
				t.Errorf("exit %d, stderr %q", code, errs)
			}
		})
	}
}

func TestWorkerSourceRecipeModeNeedsNoTokenAndNoModelKey(t *testing.T) {
	t.Setenv("WORKER_TOKEN", "")
	t.Setenv("CLOUD_API_KEY", "")
	cfg := sourceCfg(t, "ok", "http://127.0.0.1:1")
	code, out, errs := runSource(t, "test", "--config", cfg, "--recipe", "my-feed", "--locator", "wiki:space/ops", "--show", "a.md", "--cursor", "c0")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"recipe:\tmy-feed", "source:\t(not registered)", "cursor_in:\tc0", "added\ta.md\tr1\t", "next_cursor:\tc1", "--- content of a.md ---"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out+errs, "--exec-helper") {
		t.Error("a command's arguments must not be printed")
	}
}

func TestWorkerSourceUnsetVariableIsNamedNotItsValue(t *testing.T) {
	cfg := sourceCfg(t, "ok", "http://127.0.0.1:1")
	t.Setenv("LISTER_VAR", "")
	code, _, errs := runSource(t, "test", "--config", cfg, "--recipe", "my-feed", "--locator", "l")
	if code != 2 || !strings.Contains(errs, "LISTER_VAR") {
		t.Errorf("exit %d: %q", code, errs)
	}
}

func TestWorkerSourceListFailureExits1(t *testing.T) {
	cfg := sourceCfg(t, "fail", "http://127.0.0.1:1")
	code, _, errs := runSource(t, "test", "--config", cfg, "--recipe", "my-feed", "--locator", "l")
	if code != 1 || !strings.Contains(errs, "list_failed") || !strings.Contains(errs, "lister broke") {
		t.Errorf("exit %d: %q", code, errs)
	}
}

func TestWorkerSourceNeverPrintsTheSecret(t *testing.T) {
	cfg := sourceCfg(t, "ok", "http://127.0.0.1:1")
	_, out, errs := runSource(t, "test", "--config", cfg, "--recipe", "my-feed", "--locator", "l")
	// The lister echoes its variable as content, but only --show prints content.
	if strings.Contains(out+errs, listerSecret) {
		t.Errorf("secret on the output:\n%s%s", out, errs)
	}
}

// --source against a server: a 401 names token_env, and a missing Source is class 2.
func TestWorkerSourceAgainstAServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	t.Setenv("WORKER_TOKEN", "wrong")
	cfg := sourceCfg(t, "ok", srv.URL)
	code, _, errs := runSource(t, "test", "--config", cfg, "--source", "s/x", "--kb", "kb-a")
	if code != 2 || !strings.Contains(errs, "WORKER_TOKEN") {
		t.Errorf("exit %d: %q", code, errs)
	}
}
