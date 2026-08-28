package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/defaults"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// TestResolveConnectSettings_InheritsPersisted pins the fix for the footgun
// where a bare `connect <agent>` rewrote server_url/auth/token_env of an
// already-configured machine to the flag defaults,
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
	got := resolveConnectSettings(map[string]bool{}, defaults.DefaultMCPURL, false, "DEFAULT_ENV", existing)
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
	got = resolveConnectSettings(map[string]bool{}, defaults.DefaultMCPURL, false, "DEFAULT_ENV", nil)
	if got.ServerURL != defaults.DefaultMCPURL || got.Auth || got.TokenEnv != "DEFAULT_ENV" {
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
	fs.String("server-url", defaults.DefaultMCPURL, "")
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
		"http://localhost:39273/mcp":   true,
		"http://127.0.0.1:39273/mcp":   true,
		"http://[::1]:39273/mcp":       true,
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

func TestInstallServiceAndWaitHealthy_UsesDefaultListenAddress(t *testing.T) {
	previous := installService
	var got service.InstallOptions
	stop := errors.New("stop after recording options")
	installService = func(_ *service.Manager, opts service.InstallOptions) error {
		got = opts
		return stop
	}
	t.Cleanup(func() { installService = previous })

	err := installServiceAndWaitHealthy(nil, time.Second)
	if !errors.Is(err, stop) {
		t.Fatalf("installServiceAndWaitHealthy error = %v, want %v", err, stop)
	}
	if got.HTTPAddr != defaults.DefaultListenAddress {
		t.Errorf("automatic service HTTPAddr = %q, want %q", got.HTTPAddr, defaults.DefaultListenAddress)
	}
}

// A provider whose MCP configuration Cartographer does not write (D141, hermes)
// must not be told an MCP entry was written for it, nor to restart its sessions
// to load tools it never received — both contradict the warning printed
// alongside them (D147).
func TestPrintConnectResult_NoMCPClaimsForProvidersWithoutAnEmitter(t *testing.T) {
	hermes := []string{"hermes"}
	out := withStdout(t, func() {
		printConnectResult(t.TempDir(), hermes, connectOptions{}, connectResult{Providers: hermes})
	})
	if strings.Contains(out, "MCP entry") {
		t.Errorf("output = %q, must not announce an MCP entry for a provider with no emitter", out)
	}
	if strings.Contains(out, "load the MCP tools") {
		t.Errorf("output = %q, must not ask to restart sessions for MCP tools never delivered", out)
	}
	if !strings.Contains(out, "connected: hermes") {
		t.Errorf("output = %q, want the provider still reported as connected", out)
	}

	// A provider that does have an emitter keeps both lines.
	claude := []string{"claude"}
	out = withStdout(t, func() {
		printConnectResult(t.TempDir(), claude, connectOptions{}, connectResult{Providers: claude, MCPEntries: []string{"cartographer"}})
	})
	if !strings.Contains(out, "wrote MCP entry cartographer") {
		t.Errorf("output = %q, want the MCP entry reported", out)
	}
	if !strings.Contains(out, "restart the claude sessions") {
		t.Errorf("output = %q, want the restart hint", out)
	}
}

// With a mixed selection the restart hint names only the providers that
// actually received MCP configuration.
func TestPrintConnectResult_RestartHintNamesOnlyMCPProviders(t *testing.T) {
	providers := []string{"claude", "hermes"}
	out := withStdout(t, func() {
		printConnectResult(t.TempDir(), providers, connectOptions{}, connectResult{Providers: providers, MCPEntries: []string{"cartographer"}})
	})
	if !strings.Contains(out, "restart the claude sessions") {
		t.Errorf("output = %q, want only claude named in the restart hint", out)
	}
	if strings.Contains(out, "hermes sessions") {
		t.Errorf("output = %q, must not tell hermes to restart for MCP tools", out)
	}
}

// The predicate both the MCP-entry lines and the restart hint are scoped by.
func TestProvidersManagingMCP(t *testing.T) {
	got := providersManagingMCP([]string{"claude", "hermes", "codex"})
	want := []string{"claude", "codex"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("providersManagingMCP() = %v, want %v", got, want)
	}
	if got := providersManagingMCP([]string{"hermes"}); len(got) != 0 {
		t.Errorf("providersManagingMCP([hermes]) = %v, want empty — it has no MCP emitter", got)
	}
}
