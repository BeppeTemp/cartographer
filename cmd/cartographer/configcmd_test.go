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
	body := "server_url: " + serverURL + "/mcp\ntrust: false\n"
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
			`{"key":"git.ssh_key","value":"/x","origin":"env","restart":true},` +
			`{"key":"update_check","value":true,"origin":"default"},` +
			`{"key":"auth.tokens","value":"(set)","origin":"file","secret":true}],` +
			`"kbs":[{"name":"kb-a","entries":[{"key":"kbs[].write_gate","value":"error","origin":"file"}]}]}`))
	}))
	defer srv.Close()
	dir := configDir(t, srv.URL)

	var out bytes.Buffer
	if code := runConfig(&out, dir, false, false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	got := out.String()
	for _, want := range []string{"server_url", "(file)", "trust", "/x", "git.ssh_key", "(env, restart required)", "auth.tokens", "(set)", "KB kb-a", "kbs[].write_gate"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "update_check") || strings.Contains(got, "token_env") {
		t.Errorf("a default key is listed without --all:\n%s", got)
	}

	out.Reset()
	runConfig(&out, dir, false, true)
	if !strings.Contains(out.String(), "update_check") || !strings.Contains(out.String(), "token_env") {
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
