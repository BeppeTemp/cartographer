package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		Token:      "${CARTOGRAPHER_TOKENS}",
	}

	// No form flag passed → inherit everything from existing, ignoring defaults.
	got := resolveConnectSettings(map[string]bool{}, defaults.DefaultMCPURL, false, "DEFAULT_ENV", existing)
	if got.ServerURL != existing.ServerURL {
		t.Errorf("ServerURL = %q, want inherited %q", got.ServerURL, existing.ServerURL)
	}
	if !got.Auth {
		t.Error("Auth = false, want inherited true")
	}
	if got.Token != existing.Token {
		t.Errorf("Token = %q, want inherited %q", got.Token, existing.Token)
	}

	// A literal token in the file is inherited as it is, never replaced by the
	// flag's variable name (D906).
	literal := &clientconfig.Config{ServerURL: "https://remote.example.com/mcp", Auth: true, Token: "s3cr3t-literal"}
	if got := resolveConnectSettings(map[string]bool{}, defaults.DefaultMCPURL, false, "DEFAULT_ENV", literal); got.Token != "s3cr3t-literal" {
		t.Errorf("Token = %q, want the literal inherited", got.Token)
	}

	// An explicitly passed flag wins over the persisted value.
	got = resolveConnectSettings(map[string]bool{"server-url": true}, "http://new.example.com/mcp", false, "DEFAULT_ENV", existing)
	if got.ServerURL != "http://new.example.com/mcp" {
		t.Errorf("ServerURL = %q, want explicit flag to win", got.ServerURL)
	}

	// First-ever connect (existing nil) → flag defaults apply as-is.
	got = resolveConnectSettings(map[string]bool{}, defaults.DefaultMCPURL, false, "DEFAULT_ENV", nil)
	if got.ServerURL != defaults.DefaultMCPURL || got.Auth || got.Token != "${DEFAULT_ENV}" {
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
	state, err := probeServer(connectOptions{ServerURL: srv.URL, Auth: true, Token: "${PROBE_TOKEN}"})
	if err != nil || state != probeReady {
		t.Fatalf("probeServer with auth: %v", err)
	}

	// Auth disabled: no Authorization header is sent even though the env var
	// is set (same rule as resolveToken in clientsync.go) → the server 401s.
	_, err = probeServer(connectOptions{ServerURL: srv.URL, Auth: false, Token: "${PROBE_TOKEN}"})
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
		loopback, installed, want bool
	}{
		{loopback: true, installed: false, want: true},
		// Installed, running or deliberately stopped: no fresh install.
		{loopback: true, installed: true, want: false},
		{loopback: false, installed: false, want: false},
		{loopback: false, installed: true, want: false},
	}
	for _, tc := range cases {
		if got := shouldOfferServiceInstall(tc.loopback, tc.installed); got != tc.want {
			t.Errorf("shouldOfferServiceInstall(%v, %v) = %v, want %v", tc.loopback, tc.installed, got, tc.want)
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

// A connect names the MCP entry it wrote and asks for a restart of every
// selected provider's sessions (D147).
func TestPrintConnectResult_ReportsEntryAndRestartHint(t *testing.T) {
	claude := []string{"claude"}
	out := withStdout(t, func() {
		printConnectResult(t.TempDir(), claude, connectOptions{}, connectResult{Providers: claude, MCPEntries: []string{"cartographer"}})
	})
	if !strings.Contains(out, "wrote MCP entry cartographer") {
		t.Errorf("output = %q, want the MCP entry reported", out)
	}
	if !strings.Contains(out, "restart the claude sessions") {
		t.Errorf("output = %q, want the restart hint", out)
	}
}

// TestDoConnect_MigratesLegacyDefaultURL: the pre-#329 default reaches connect
// from old scripts and docs; it is recorded, and written to the client, on the
// loopback literal the service listens on (D231). Whether a local server
// answers does not matter: a down server still records the connection.
func TestDoConnect_MigratesLegacyDefaultURL(t *testing.T) {
	// The migration target is the real default endpoint, so on a machine that
	// runs the native service doConnect reaches it and inherits its KBs — two or
	// more make the first connect fail on the D190 choice, which is not what
	// this test is about. CI has no such server and keeps the coverage.
	if _, err := client.New(defaults.DefaultMCPURL, "").Health(time.Second); err == nil {
		t.Skip("a local server answers on the default endpoint this test migrates to")
	}
	dir := t.TempDir()
	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: "http://localhost:39273/mcp", Name: "cartographer"}); err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerURL != defaults.DefaultMCPURL {
		t.Errorf("recorded server_url = %q, want %q", cfg.ServerURL, defaults.DefaultMCPURL)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "localhost:39273") {
		t.Errorf("the client's MCP entry still points at localhost:\n%s", data)
	}
}

func TestLiteralTokenWarning(t *testing.T) {
	if w := literalTokenWarning(true, "s3cr3t"); !strings.Contains(w, "$CARTOGRAPHER_TOKENS") || strings.Contains(w, "s3cr3t") {
		t.Errorf("literal token warning = %q", w)
	}
	for _, c := range []struct {
		auth bool
		tok  string
	}{{false, "s3cr3t"}, {true, "${MY_TOKEN}"}, {true, ""}} {
		if w := literalTokenWarning(c.auth, c.tok); w != "" {
			t.Errorf("auth=%v token=%q warned: %q", c.auth, c.tok, w)
		}
	}
}

// D1184: --access is recorded for the providers of the run, inherited by a
// later bare connect, and refused where the profile cannot be carried.
func TestApplyConnectAccess(t *testing.T) {
	cfg := clientconfig.Default()
	opts := connectOptions{Providers: []string{"claude"}, Access: "read-min"}
	if err := applyConnectAccess(cfg, opts, "/mcp/routed"); err != nil {
		t.Fatal(err)
	}
	if cfg.Clients["claude"].Access != "read-min" || cfg.Clients["codex"].Access != "" {
		t.Errorf("clients = %+v", cfg.Clients)
	}
	// A later bare connect (no --access) changes nothing: the value is inherited.
	if err := applyConnectAccess(cfg, connectOptions{Providers: []string{"claude"}}, "/mcp/routed"); err != nil || cfg.Clients["claude"].Access != "read-min" {
		t.Errorf("bare connect: %v %+v", err, cfg.Clients)
	}

	if err := applyConnectAccess(clientconfig.Default(), opts, ""); err == nil || !strings.Contains(err.Error(), "routed mount") {
		t.Errorf("no routed endpoint: err = %v", err)
	}
	if err := applyConnectAccess(clientconfig.Default(), connectOptions{Providers: []string{"claude"}, Access: "write"}, "/mcp/routed"); err == nil {
		t.Error("a bogus value was accepted")
	}
	bridged := clientconfig.Default()
	if err := bridged.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := bridged.BindWorkspace("claude", t.TempDir(), []string{"kb-a"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConnectAccess(bridged, opts, "/mcp/routed"); err == nil || !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "bridge") {
		t.Errorf("workspace-bound provider: err = %v", err)
	}
}

// #1282: --unpin-key removes a KB's pins, replaces with --pin-key, and refuses
// a KB with no pin.
func TestDoConnect_UnpinKey(t *testing.T) {
	setHome(t, t.TempDir())
	dir := t.TempDir()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	key := hex.EncodeToString(pub)
	base := connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer"}

	pin := base
	pin.PinKeys = []string{"kb-a=" + key}
	if _, err := doConnect(pin); err != nil {
		t.Fatalf("pin: %v", err)
	}
	if cfg, _ := clientconfig.Load(dir); len(cfg.SigningKeys["kb-a"]) != 1 {
		t.Fatalf("pin not recorded: %v", cfg.SigningKeys)
	}

	unknown := base
	unknown.UnpinKeys = []string{"kb-zzz"}
	if _, err := doConnect(unknown); err == nil || !strings.Contains(err.Error(), "kb-zzz") {
		t.Errorf("unpinning an unknown KB: err = %v, want an error naming it", err)
	}

	unpin := base
	unpin.UnpinKeys = []string{"kb-a"}
	if _, err := doConnect(unpin); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if cfg, _ := clientconfig.Load(dir); len(cfg.SigningKeys) != 0 {
		t.Errorf("pin still there after --unpin-key: %v", cfg.SigningKeys)
	}
}
