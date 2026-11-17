package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// healthServer answers /health with the given KB names and nothing else.
func healthServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	body := `{"status":"ok","kbs":[`
	for i, n := range names {
		if i > 0 {
			body += ","
		}
		body += `{"name":"` + n + `"}`
	}
	body += `]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func clientPointingAt(t *testing.T, url string, kbs ...string) {
	t.Helper()
	dir := t.TempDir()
	setHome(t, dir)
	if err := clientconfig.Save(dir, &clientconfig.Config{ServerURL: url + "/mcp", KnownKBs: kbs}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

// #2198 item 1: a KB typed on the command line that the server does not mount
// is "no KB named", never the stale-selection advice meant for .cartographer.yaml.
func TestTypedUnknownKB_NamesTheMountedOnes(t *testing.T) {
	srv := healthServer(t, "kb-a", "kb-b")
	clientPointingAt(t, srv.URL)

	var code int
	stderr := withStderr(t, func() { code = cmdReindex([]string{"--kb", "nope"}) })
	if code == 0 || !strings.Contains(stderr, `no KB named "nope" on `+srv.URL) || !strings.Contains(stderr, "mounted: kb-a, kb-b") || strings.Contains(stderr, "stale selection") {
		t.Errorf("reindex --kb nope: code=%d stderr=%q", code, stderr)
	}
	stderr = withStderr(t, func() { code = cmdKBRepair([]string{"nope"}) })
	if code == 0 || !strings.Contains(stderr, `no KB named "nope"`) || strings.Contains(stderr, "stale selection") {
		t.Errorf("kb repair nope: code=%d stderr=%q", code, stderr)
	}
}

// A name read from .cartographer.yaml keeps the stale-selection text.
func TestConfiguredUnknownKB_KeepsStaleSelection(t *testing.T) {
	srv := healthServer(t, "kb-a")
	clientPointingAt(t, srv.URL, "gone")

	var code int
	stderr := withStderr(t, func() { code = cmdReindex(nil) })
	if code == 0 || !strings.Contains(stderr, "stale selection") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestKBRenameUnmount_MissingKBNamesTheMountedOnes(t *testing.T) {
	data := t.TempDir()
	if err := os.MkdirAll(filepath.Join(data, "kb-a", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "kb-a", "data", "index.md"), []byte("# i\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "server.yaml")
	_, err := planKBRename("nope", "other", data, cfg)
	if err == nil || !strings.Contains(err.Error(), `no KB named "nope"`) || !strings.Contains(err.Error(), "mounted: kb-a") {
		t.Errorf("rename: %v", err)
	}
	_, err = planKBUnmount("nope", data, cfg, false)
	if err == nil || !strings.Contains(err.Error(), `no KB named "nope"`) || !strings.Contains(err.Error(), "mounted: kb-a") {
		t.Errorf("unmount: %v", err)
	}
	// A directory that exists without data/index.md is still "not an OKF KB".
	if err := os.MkdirAll(filepath.Join(data, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err = planKBUnmount("plain", data, cfg, false); err == nil || !strings.Contains(err.Error(), "not an OKF KB") {
		t.Errorf("plain dir: %v", err)
	}
}

// #2198 item 2: kb secrets init asks the server its --config describes, not the
// client's server_url.
func TestKBSecretsInit_TalksToTheConfigsServer(t *testing.T) {
	cfgServer := healthServer(t, "kb-a")
	clientPointingAt(t, "http://127.0.0.1:1") // nothing answers there
	data := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "srv.yaml")
	if err := os.WriteFile(cfg, []byte("http: "+strings.TrimPrefix(cfgServer.URL, "http://")+"\ndata: "+filepath.ToSlash(data)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var code int
	stderr := withStderr(t, func() {
		code = cmdKBSecretsInit([]string{"nope", "--recipient", "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq", "--config", cfg, "--data", data, "--local"})
	})
	if code == 0 || !strings.Contains(stderr, `no KB named "nope" on `+cfgServer.URL) {
		t.Errorf("code=%d stderr=%q: the command did not reach the config's server", code, stderr)
	}
}

// #2198 item 3: a failed --restart exits non-zero.
func TestPrintPostCreateGuidance_FailedRestartExitsNonZero(t *testing.T) {
	srv := healthServer(t)
	cfg := guidanceConfig(t, srv)
	stubRestart(t, true)
	restartServiceFn = func() error { return errors.New("boom") }

	var code int
	stderr := withStderr(t, func() { code = printPostCreateGuidance(cfg, true) })
	if code != restartFailedExit || !strings.Contains(stderr, "restart failed") {
		t.Errorf("create guidance: code=%d stderr=%q", code, stderr)
	}
	withStderr(t, func() { code = printPostUnmountGuidance(cfg, true) })
	if code != restartFailedExit {
		t.Errorf("unmount guidance: code=%d", code)
	}
	// Not asked, or nothing to restart: exit 0.
	stubRestart(t, false)
	withStderr(t, func() { code = printPostCreateGuidance(cfg, false) })
	if code != 0 {
		t.Errorf("no --restart: code=%d", code)
	}
}

// #2198 item 4: bare reindex on a multi-KB server says to pass --kb.
func TestReindex_BareOnMultiKBServerSaysPassKB(t *testing.T) {
	srv := healthServer(t, "kb-a", "kb-b")
	clientPointingAt(t, srv.URL)

	var code int
	stderr := withStderr(t, func() { code = cmdReindex(nil) })
	if code == 0 || !strings.Contains(stderr, "--kb <kb>") || !strings.Contains(stderr, "kb-a, kb-b") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestImportReindexHint_NamesKBFlag(t *testing.T) {
	orig := reindexAfterImportFn
	reindexAfterImportFn = func(string) bool { return false }
	t.Cleanup(func() { reindexAfterImportFn = orig })
	out := withStdout(t, func() { printReindexHint(3, t.TempDir()) })
	if !strings.Contains(out, "cartographer reindex --kb <kb>") {
		t.Errorf("out = %q", out)
	}
}
