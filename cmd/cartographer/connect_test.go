package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// TestResolveConnectSettings_InheritsPersisted pins the fix for the footgun
// where a bare `connect <agent>` rewrote server_url/auth/token_env of an
// already-configured machine to the flag defaults (http://localhost:8080,
// auth:false). Flags not passed explicitly must inherit the persisted config.
func TestResolveConnectSettings_InheritsPersisted(t *testing.T) {
	existing := &clientconfig.Config{
		ServerURL:  "https://remote.example.com/mcp",
		ServerName: "cartographer",
		Auth:       true,
		TokenEnv:   "CARTOGRAPHER_TOKENS",
		Trust:      true,
	}

	// No form flag passed → inherit everything from existing, ignoring defaults.
	got := resolveConnectSettings(map[string]bool{}, "http://localhost:8080/mcp", false, "DEFAULT_ENV", existing)
	if got.ServerURL != existing.ServerURL {
		t.Errorf("ServerURL = %q, want inherited %q", got.ServerURL, existing.ServerURL)
	}
	if !got.Auth {
		t.Error("Auth = false, want inherited true")
	}
	if got.TokenEnv != existing.TokenEnv {
		t.Errorf("TokenEnv = %q, want inherited %q", got.TokenEnv, existing.TokenEnv)
	}

	// An explicitly passed flag wins over the persisted value.
	got = resolveConnectSettings(map[string]bool{"server-url": true}, "http://new.example.com/mcp", false, "DEFAULT_ENV", existing)
	if got.ServerURL != "http://new.example.com/mcp" {
		t.Errorf("ServerURL = %q, want explicit flag to win", got.ServerURL)
	}

	// First-ever connect (existing nil) → flag defaults apply as-is.
	got = resolveConnectSettings(map[string]bool{}, "http://localhost:8080/mcp", false, "DEFAULT_ENV", nil)
	if got.ServerURL != "http://localhost:8080/mcp" || got.Auth || got.TokenEnv != "DEFAULT_ENV" {
		t.Errorf("first connect: got %+v, want flag defaults", got)
	}
	if got.Name != "cartographer" {
		t.Errorf("Name = %q, want default \"cartographer\"", got.Name)
	}
}

// withTTY overrides isTerminal for the duration of the test, restoring it on
// cleanup. tty controls the return value for every fd.
func withTTY(t *testing.T, tty bool) {
	t.Helper()
	prev := isTerminal
	isTerminal = func(fd uintptr) bool { return tty }
	t.Cleanup(func() { isTerminal = prev })
}

// newParsedConnectFlagSet mirrors the flag set cmdConnect builds, parses args,
// and returns it plus the --no-input value, for wantsConnectForm tests.
func newParsedConnectFlagSet(t *testing.T, args ...string) (*flag.FlagSet, bool) {
	t.Helper()
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.String("server-url", "http://localhost:8080/mcp", "")
	fs.Bool("auth", false, "")
	fs.String("token-env", "CARTOGRAPHER_TOKENS", "")
	fs.Bool("dry-run", false, "")
	fs.Bool("auto-trust", false, "")
	noInput := fs.Bool("no-input", false, "")
	fs.String("agents", "", "")
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return fs, *noInput
}

func TestWantsConnectForm_NoFlagsAndTTY_OpensForm(t *testing.T) {
	withTTY(t, true)
	fs, noInput := newParsedConnectFlagSet(t)
	if !wantsConnectForm(fs, noInput) {
		t.Error("expected the form to open: no flags passed, TTY")
	}
}

func TestWantsConnectForm_FormFlagPassed_NoForm(t *testing.T) {
	withTTY(t, true)
	for _, args := range [][]string{
		{"--server-url=http://example.com/mcp"},
		{"--auth"},
		{"--token-env=OTHER_TOKEN"},
	} {
		fs, noInput := newParsedConnectFlagSet(t, args...)
		if wantsConnectForm(fs, noInput) {
			t.Errorf("args=%v: expected no form (an explicit form flag was passed)", args)
		}
	}
}

func TestWantsConnectForm_NonFormFlagsDoNotSuppressForm(t *testing.T) {
	withTTY(t, true)
	fs, noInput := newParsedConnectFlagSet(t, "--dry-run", "--auto-trust")
	if !wantsConnectForm(fs, noInput) {
		t.Error("expected the form to still open: only behavior flags were passed")
	}
}

func TestWantsConnectForm_NoInputSuppressesForm(t *testing.T) {
	withTTY(t, true)
	fs, noInput := newParsedConnectFlagSet(t, "--no-input")
	if wantsConnectForm(fs, noInput) {
		t.Error("expected no form: --no-input was passed")
	}
}

func TestWantsConnectForm_NonTTY_NoForm(t *testing.T) {
	withTTY(t, false)
	fs, noInput := newParsedConnectFlagSet(t)
	if wantsConnectForm(fs, noInput) {
		t.Error("expected no form: not a TTY")
	}
}

// pingServer spins up a fake MCP endpoint that answers the JSON-RPC "ping"
// method. If wantAuth is non-empty, requests without the matching bearer token
// get a 401 — mirroring auth.TokenStore.Middleware.
func pingServer(t *testing.T, wantAuth string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantAuth != "" && r.Header.Get("Authorization") != "Bearer "+wantAuth {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
	}))
}

func TestProbeServer_Success(t *testing.T) {
	srv := pingServer(t, "")
	defer srv.Close()

	state, err := probeServer(connectOptions{ServerURL: srv.URL})
	if err != nil || state != probeReady {
		t.Fatalf("probeServer: %v", err)
	}
}

func TestProbeServer_TokenOnlyWhenAuthEnabled(t *testing.T) {
	srv := pingServer(t, "sekret")
	defer srv.Close()
	t.Setenv("PROBE_TOKEN", "sekret")

	// Auth enabled: token read from the env var, probe succeeds.
	state, err := probeServer(connectOptions{ServerURL: srv.URL, Auth: true, TokenEnv: "PROBE_TOKEN"})
	if err != nil || state != probeReady {
		t.Fatalf("probeServer with auth: %v", err)
	}

	// Auth disabled: no Authorization header is sent even though the env var
	// is set (same rule as resolveToken in clientsync.go) → the server 401s.
	_, err = probeServer(connectOptions{ServerURL: srv.URL, Auth: false, TokenEnv: "PROBE_TOKEN"})
	if !errors.Is(err, client.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized without auth, got %v", err)
	}
}

func TestProbeErrorMessage_DistinguishesAuthFromNetwork(t *testing.T) {
	authMsg := probeErrorMessage(probeUnreachable, fmt.Errorf("wrap: %w", client.ErrUnauthorized))
	if !strings.Contains(authMsg, "token") {
		t.Errorf("401 message should point at the token, got %q", authMsg)
	}
	netMsg := probeErrorMessage(probeUnreachable, errors.New("dial tcp: connection refused"))
	if !strings.Contains(netMsg, "unreachable") {
		t.Errorf("network message should say unreachable, got %q", netMsg)
	}
	if strings.Contains(netMsg, "token was rejected") {
		t.Errorf("network message must not mention a rejected token, got %q", netMsg)
	}
}

func TestProbeServer_TriStateHealth(t *testing.T) {
	tests := []struct {
		name string
		body string
		want probeState
	}{
		{"ready false", `{"status":"ok","ready":false,"kbs":[]}`, probeNoKB},
		{"ready true", `{"status":"ok","ready":true,"kbs":["kb"]}`, probeReady},
		{"pre D84 empty kbs", `{"status":"ok","kbs":[]}`, probeNoKB},
		{"pre D84 nonempty kbs", `{"status":"ok","kbs":["kb"]}`, probeReady},
		{"pre D84 absent fields", `{"status":"ok"}`, probeReady},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/health" {
					t.Errorf("path = %q, want /health", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			state, err := probeServer(connectOptions{ServerURL: srv.URL + "/mcp"})
			if err != nil {
				t.Fatalf("probeServer: %v", err)
			}
			if state != tc.want {
				t.Errorf("state = %v, want %v", state, tc.want)
			}
		})
	}
}

func TestProbeErrorMessage_NoKBGuidance(t *testing.T) {
	msg := probeErrorMessage(probeNoKB, nil)
	for _, want := range []string{"server is up but no KB is mounted", "cartographer kb create <name>", "cartographer service restart"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}

func TestIsLoopbackURL(t *testing.T) {
	cases := map[string]bool{
		"http://localhost:8080/mcp":    true,
		"http://127.0.0.1:8080/mcp":    true,
		"http://[::1]:8080/mcp":        true,
		"https://wiki.example.com/mcp": false,
		"not a url \x7f":               false,
		"":                             false,
	}
	for url, want := range cases {
		if got := isLoopbackURL(url); got != want {
			t.Errorf("isLoopbackURL(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestShouldOfferServiceInstall(t *testing.T) {
	cases := []struct {
		loopback, running, want bool
	}{
		{loopback: true, running: false, want: true},
		{loopback: true, running: true, want: false},
		{loopback: false, running: false, want: false},
		{loopback: false, running: true, want: false},
	}
	for _, tc := range cases {
		if got := shouldOfferServiceInstall(tc.loopback, tc.running); got != tc.want {
			t.Errorf("shouldOfferServiceInstall(%v, %v) = %v, want %v", tc.loopback, tc.running, got, tc.want)
		}
	}
}
