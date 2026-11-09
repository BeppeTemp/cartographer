package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/testtree"
)

// seedAgentState writes a Claude config with an entry of ours pointing at the
// old server and one that is someone else's.
func seedAgentState(t *testing.T, dir string) {
	t.Helper()
	body := `{"mcpServers":{"cartographer":{"type":"http","url":"http://old.example.com/mcp"},"other":{"type":"http","url":"http://other.example.com/mcp"}}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The manifest is fetched and verified before anything is written: a pin that
// does not verify, or a rejected token, leaves the agents' configs and hooks
// byte-identical (they used to be rewritten for the new server first, with the
// message saying only that .cartographer.yaml was not updated).
func TestDoConnect_RefusalLeavesTheAgentFilesUntouched(t *testing.T) {
	t.Setenv("KB_A_TOKEN", "wrong")
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		serve func(t *testing.T) *httptest.Server
		tune  func(o *connectOptions)
		want  string
	}{
		{"rejected token", func(t *testing.T) *httptest.Server {
			return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusUnauthorized) }))
		}, func(o *connectOptions) { o.Auth, o.Token = true, "${KB_A_TOKEN}" }, "rejected the token"},
		{"pin that does not verify", func(t *testing.T) *httptest.Server {
			return syncPullServer(t, func(kb string) (string, bool) { return syncPullPayload(t, kb, "skill-"+kb), false })
		}, func(o *connectOptions) {
			o.KBs, o.PinKeys = []string{"one"}, []string{"one=" + hex.EncodeToString(pub)}
		}, "pinned keys"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setHome(t, t.TempDir())
			dir := t.TempDir()
			seedAgentState(t, dir)
			before, err := testtree.Capture(dir)
			if err != nil {
				t.Fatal(err)
			}
			srv := tc.serve(t)
			defer srv.Close()
			o := connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer"}
			tc.tune(&o)
			if _, err := doConnect(o); err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "nothing was written") {
				t.Fatalf("err = %v, want %q and 'nothing was written'", err, tc.want)
			}
			after, err := testtree.Capture(dir)
			if err != nil {
				t.Fatal(err)
			}
			// The client lock file is the lock itself, not a configuration.
			after = withoutLockFile(after)
			if !reflect.DeepEqual(before, after) {
				t.Errorf("the client directory changed although connect refused:\n before %s\n after  %s", treeNames(before), treeNames(after))
			}
		})
	}

	// A server that is down still defers the sync and still writes.
	setHome(t, t.TempDir())
	dir := t.TempDir()
	seedAgentState(t, dir)
	res, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer"})
	if err != nil || !res.Deferred || len(res.ConfigsWritten) == 0 {
		t.Fatalf("down server: res = %+v, err = %v, want a deferral that still wrote", res, err)
	}
}

// The first non-interactive connect honours CARTOGRAPHER_SERVER_URL; an
// explicit flag still wins; an existing config is not overridden by it.
func TestResolveConnectSettings_FirstConnectReadsTheEnvURL(t *testing.T) {
	t.Setenv("CARTOGRAPHER_SERVER_URL", "https://kb.example.com/mcp")
	got := resolveConnectSettings(map[string]bool{}, "http://127.0.0.1:39273/mcp", false, "CARTOGRAPHER_TOKENS", nil)
	if got.ServerURL != "https://kb.example.com/mcp" {
		t.Errorf("first connect URL = %q, want the env URL", got.ServerURL)
	}
	got = resolveConnectSettings(map[string]bool{"server-url": true}, "http://10.0.0.1:1/mcp", false, "CARTOGRAPHER_TOKENS", nil)
	if got.ServerURL != "http://10.0.0.1:1/mcp" {
		t.Errorf("an explicit --server-url lost to the env: %q", got.ServerURL)
	}
	existing := &clientconfig.Config{ServerURL: "https://already.example.com/mcp"}
	got = resolveConnectSettings(map[string]bool{}, "http://127.0.0.1:39273/mcp", false, "CARTOGRAPHER_TOKENS", existing)
	if got.ServerURL != "https://already.example.com/mcp" {
		t.Errorf("the env overrode an existing config: %q", got.ServerURL)
	}
}

// --token-env and the form store ${<value>}: only a variable NAME is valid.
func TestValidateTokenEnvName(t *testing.T) {
	for _, bad := range []string{"$MY_TOKEN", "my-token", "ghp_abc def", "1ABC", "a.b"} {
		err := validateTokenEnvName(bad)
		if err == nil {
			t.Errorf("%q accepted", bad)
		} else if strings.Contains(err.Error(), bad) {
			t.Errorf("the error echoes the value, which may be the token: %v", err)
		}
	}
	for _, good := range []string{"CARTOGRAPHER_TOKENS", "MY_TOKEN", "_x1"} {
		if err := validateTokenEnvName(good); err != nil {
			t.Errorf("%q rejected: %v", good, err)
		}
	}
	var code int
	stderr := withStderr(t, func() { code = cmdConnect([]string{"claude", "--no-input", "--token-env", "$MY_TOKEN"}) })
	if code != 2 || !strings.Contains(stderr, "--token-env") {
		t.Errorf("cmdConnect with a bad --token-env = %d, stderr %q", code, stderr)
	}
}

func treeNames(tr testtree.Tree) string {
	var out []string
	for _, e := range tr {
		out = append(out, e.Path)
	}
	return strings.Join(out, ", ")
}

func withoutLockFile(tr testtree.Tree) testtree.Tree {
	var out testtree.Tree
	for _, e := range tr {
		if e.Path != ".cartographer-client.lock" {
			out = append(out, e)
		}
	}
	return out
}

// The form validates and stores the same, trimmed, text: " FOO" used to pass the
// check and be stored as the literal bearer "${ FOO}".
func TestConnectForm_TokenEnvIsTrimmedOnce(t *testing.T) {
	m := newConnectFormModel("t", connectOptions{Providers: []string{"claude"}, ServerURL: "http://127.0.0.1:39273/mcp"})
	m.tokenEnv.SetValue("  FOO ")
	if got := m.Values().Token; got != "${FOO}" {
		t.Errorf("Token = %q, want ${FOO}", got)
	}
}
