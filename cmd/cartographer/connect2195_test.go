package main

import (
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// #2195 item 1: a URL that is not an MCP endpoint of the server, or that names
// a KB it does not mount, is refused before anything is written, naming the
// KBs the server mounts.
func TestDoConnect_RefusesAServerURLThatIsNotAnMCPEndpoint(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"kb-a"},{"name":"kb-b"}]}`)
	defer srv.Close()
	for _, tc := range []struct{ path, want string }{
		{"/mcp/nope", `names the KB "nope"`},
		{"/mcp/", "trailing slash"},
		{"/", "not an MCP endpoint"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			dir := t.TempDir()
			_, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + tc.path, Name: "cartographer", KBs: []string{"kb-a"}})
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "kb-a, kb-b") {
				t.Fatalf("err = %v, want %q and the mounted KBs", err, tc.want)
			}
			if _, statErr := os.Stat(clientconfig.Path(dir)); !os.IsNotExist(statErr) {
				t.Errorf("a client config was written (%v)", statErr)
			}
		})
	}
	for _, path := range []string{"/mcp", "/mcp/kb-a", "/mcp/routed"} {
		dir := t.TempDir()
		if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + path, Name: "cartographer", KBs: []string{"kb-a"}, DryRun: true}); err != nil {
			t.Errorf("%s refused: %v", path, err)
		}
	}
}

// #2195 item 2: a 401 while reading the KB list is a token problem, named with
// the flags that exist (--auth, --token-env), for --kb all as for a named KB.
func TestDoConnect_ATokenRefusedOnHealthNamesTheRealFlags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"1.0.0","ready":true,"kbs":[]}`))
	}))
	defer srv.Close()
	t.Setenv("CARTOGRAPHER_TOKENS", "wrong")
	for _, kbs := range [][]string{{"all"}, {"kb-a"}} {
		_, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: t.TempDir(), ServerURL: srv.URL + "/mcp", Name: "cartographer", Auth: true, Token: "${CARTOGRAPHER_TOKENS}", KBs: kbs})
		if err == nil || !strings.Contains(err.Error(), "rejected the token") || !strings.Contains(err.Error(), "$CARTOGRAPHER_TOKENS") ||
			!strings.Contains(err.Error(), "--token-env") || strings.Contains(err.Error(), "--token,") || strings.Contains(err.Error(), "could not be read") {
			t.Errorf("--kb %v: err = %v", kbs, err)
		}
	}
	// Auth off: the message says to pass --auth.
	if err := rejectedTokenError(false, "", http.ErrAbortHandler); !strings.Contains(err.Error(), "pass --auth") {
		t.Errorf("auth off: %v", err)
	}
}

// #2195 item 3: a connect that fails after the first provider file was written
// leaves the client config behind and says how to resume.
func TestDoConnect_AFailurePartWayLeavesTheClientConfigAndSaysHowToResume(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"kb-a"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	// Codex's config dir is a file: its write fails, after claude's succeeded
	// (providers are written in the order given).
	if err := os.WriteFile(filepath.Join(dir, ".codex"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := doConnect(connectOptions{Providers: []string{"claude", "codex"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", KBs: []string{"kb-a"}})
	if err == nil {
		t.Skip("the write did not fail on this platform")
	}
	if !strings.Contains(err.Error(), "stopped part-way") || !strings.Contains(err.Error(), "cartographer connect --agents claude,codex") {
		t.Errorf("err = %v, want the resume hint", err)
	}
	cfg, loadErr := clientconfig.Load(dir)
	if loadErr != nil {
		t.Fatalf("no client config after a partial connect: %v", loadErr)
	}
	if !strings.Contains(strings.Join(cfg.Agents, ","), "claude") {
		t.Errorf("agents = %v, want claude recorded", cfg.Agents)
	}
}

// #2217 item 1: the resume and undo commands of a part-way connect parse with
// the real flag sets of connect and disconnect, for several providers.
func TestPartWayHintParsesWithTheRealFlags(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"kb-a"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".codex"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := doConnect(connectOptions{Providers: []string{"claude", "codex", "opencode"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", KBs: []string{"kb-a"}})
	if err == nil {
		t.Skip("the write did not fail on this platform")
	}
	cmds := regexp.MustCompile("`cartographer (connect|disconnect) ([^`]*)`").FindAllStringSubmatch(err.Error(), -1)
	if len(cmds) != 2 {
		t.Fatalf("want a connect and a disconnect command in %q", err)
	}
	for _, m := range cmds {
		fs := flag.NewFlagSet(m[1], flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		csv := fs.String("agents", "", "")
		if perr := fs.Parse(strings.Fields(m[2])); perr != nil || fs.NArg() != 0 || *csv != "claude,codex,opencode" {
			t.Errorf("%s %s: err=%v args=%v agents=%q", m[1], m[2], perr, fs.Args(), *csv)
		}
		if provs, rerr := resolveTargetProviders("", *csv); rerr != nil || len(provs) != 3 {
			t.Errorf("%s: providers %v, %v", m[1], provs, rerr)
		}
	}
}

// #2217 item 2: --kb all with --auth surfaces a refused or missing token when
// /health answers anonymously and lists no KB.
func TestDoConnect_KBAllWithARefusedTokenNamesTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok","version":"1.0.0","ready":true}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer good" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()
	for name, set := range map[string]func(){
		"wrong": func() { t.Setenv("CARTOGRAPHER_TOKENS", "bad") },
		"unset": func() { t.Setenv("CARTOGRAPHER_TOKENS", "") },
	} {
		set()
		_, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: t.TempDir(), ServerURL: srv.URL + "/mcp", Name: "cartographer", Auth: true, Token: "${CARTOGRAPHER_TOKENS}", KBs: []string{"all"}})
		if err == nil || !strings.Contains(err.Error(), "rejected the token") || !strings.Contains(err.Error(), "$CARTOGRAPHER_TOKENS") || strings.Contains(err.Error(), "could not be read") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// #2217 item 4: a trailing slash on a per-KB URL suggests the same URL without it.
func TestDoConnect_TrailingSlashSuggestsTheSameURLWithoutIt(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"kb-a"}]}`)
	defer srv.Close()
	_, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: t.TempDir(), ServerURL: srv.URL + "/mcp/kb-a/", Name: "cartographer", KBs: []string{"kb-a"}})
	if err == nil || !strings.Contains(err.Error(), "use "+srv.URL+"/mcp/kb-a") || strings.HasSuffix(err.Error(), "/mcp/kb-a/") {
		t.Errorf("err = %v", err)
	}
}

// #2217: a kb query parameter in the server URL is refused before anything is written.
func TestDoConnect_RefusesAServerURLWithAKBQuery(t *testing.T) {
	dir := t.TempDir()
	_, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: "http://127.0.0.1:1/mcp/kb-a?kb=kb-b", Name: "cartographer", KBs: []string{"kb-a"}})
	if err == nil || !strings.Contains(err.Error(), "kb query parameter") || !strings.Contains(err.Error(), "--kb") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(clientconfig.Path(dir)); !os.IsNotExist(statErr) {
		t.Errorf("a client config was written (%v)", statErr)
	}
}
