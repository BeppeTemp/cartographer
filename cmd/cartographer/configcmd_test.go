package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func configDir(t *testing.T, serverURL string) string {
	t.Helper()
	dir := t.TempDir()
	body := "server_url: " + serverURL + "/mcp\nusage_scan: false\n"
	if err := os.WriteFile(clientconfig.Path(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestConfigShowsClientOriginsAndServerSection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ui/v1/settings" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"version":"v1","server":[` +
			`{"key":"git.sync","value":false,"origin":"env","restart":true},` +
			`{"key":"git.autocommit","value":true,"origin":"default"},` +
			`{"key":"auth.tokens","value":"(set)","origin":"file","secret":true}],` +
			`"kbs":[{"name":"kb-a","entries":[{"key":"kbs[].usage_stale_days","value":"30","origin":"file"}]}]}`))
	}))
	defer srv.Close()
	dir := configDir(t, srv.URL)

	var out bytes.Buffer
	if code := runConfig(&out, dir, false, false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	got := out.String()
	for _, want := range []string{"server_url", "(file)", "usage_scan", "false", "git.sync", "(env, restart required)", "auth.tokens", "(set)", "KB kb-a", "kbs[].usage_stale_days"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "git.autocommit") || strings.Contains(got, "update_check") {
		t.Errorf("a default key is listed without --all:\n%s", got)
	}

	out.Reset()
	runConfig(&out, dir, false, true)
	if !strings.Contains(out.String(), "git.autocommit") || !strings.Contains(out.String(), "update_check") {
		t.Errorf("--all lacks the default keys:\n%s", out.String())
	}
}

func TestConfigUnreachableServerPrintsClientAndANote(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	var out bytes.Buffer
	if code := runConfig(&out, configDir(t, url), false, false); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(out.String(), "server_url") || !strings.Contains(out.String(), "not reachable") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestConfigJSON(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	var out bytes.Buffer
	runConfig(&out, configDir(t, srv.URL), true, false)
	var v configView
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if len(v.Client) == 0 || v.Client[0].Key != "server_url" || v.Note == "" {
		t.Errorf("view = %+v", v)
	}
}

func TestRunDispatchesConfig(t *testing.T) {
	orig := configFn
	defer func() { configFn = orig }()
	called := false
	configFn = func(args []string) int { called = true; return 0 }
	if run([]string{"config", "--json"}) != 0 || !called {
		t.Error("config was not dispatched")
	}
}

// A literal token is never printed (table or --json); a ${NAME} reference is.
func TestConfigRedactsLiteralClientToken(t *testing.T) {
	dir := t.TempDir()
	write := func(tok string) {
		body := "server_url: http://127.0.0.1:1/mcp\ntoken: " + tok + "\n"
		if err := os.WriteFile(clientconfig.Path(dir), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("s3cr3t-literal-value")
	for _, asJSON := range []bool{false, true} {
		var out bytes.Buffer
		runConfig(&out, dir, asJSON, false)
		if strings.Contains(out.String(), "s3cr3t-literal-value") {
			t.Fatalf("json=%v: literal token printed:\n%s", asJSON, out.String())
		}
		if !strings.Contains(out.String(), "(set)") {
			t.Errorf("json=%v: no (set) marker:\n%s", asJSON, out.String())
		}
	}
	write("${MY_TOKEN}")
	var out bytes.Buffer
	runConfig(&out, dir, false, false)
	if !strings.Contains(out.String(), "${MY_TOKEN}") {
		t.Errorf("env reference should be shown:\n%s", out.String())
	}
}
