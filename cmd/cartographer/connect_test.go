package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/defaults"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
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

// healthRecorder is a listener whose /health is public, like the server's, and
// that records the Authorization header of every request it gets. shape is the
// /health body it answers.
func healthRecorder(t *testing.T, shape string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(shape))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

const serverShape = `{"status":"ok","version":"v1.2.3","kbs":[]}`

func TestProbeServer_TokenOnlyWhenAuthEnabled(t *testing.T) {
	t.Setenv("PROBE_TOKEN", "sekret")

	// Auth enabled: the first /health is anonymous, the token goes only to a
	// listener whose answer is a Cartographer server's.
	srv, seen := healthRecorder(t, serverShape)
	if _, err := probeServer(connectOptions{ServerURL: srv.URL, Auth: true, Token: "${PROBE_TOKEN}"}); err != nil {
		t.Fatalf("probeServer with auth: %v", err)
	}
	if got := seen(); len(got) != 2 || got[0] != "" || got[1] != "Bearer sekret" {
		t.Errorf("Authorization headers = %q, want an anonymous probe then the token", got)
	}

	// Auth disabled: no Authorization header is sent even though the env var
	// is set (same rule as resolveToken in clientsync.go).
	srv, seen = healthRecorder(t, serverShape)
	if _, err := probeServer(connectOptions{ServerURL: srv.URL, Auth: false, Token: "${PROBE_TOKEN}"}); err != nil {
		t.Fatalf("probeServer without auth: %v", err)
	}
	if got := seen(); len(got) != 1 || got[0] != "" {
		t.Errorf("Authorization headers = %q, want one anonymous probe", got)
	}
}

// A listener that is not a Cartographer server never receives the token, from
// any caller that probes /health.
func TestHealthProbes_ForeignListenerGetsNoToken(t *testing.T) {
	t.Setenv("PROBE_TOKEN", "sekret")
	srv, seen := healthRecorder(t, `{"status":"ok"}`)

	if _, err := probeServer(connectOptions{ServerURL: srv.URL, Auth: true, Token: "${PROBE_TOKEN}"}); err != nil {
		t.Fatalf("probeServer: %v", err)
	}
	if _, err := enumerateKBs(srv.URL, true, "${PROBE_TOKEN}", false); err != nil {
		t.Fatalf("enumerateKBs: %v", err)
	}
	if _, note := fetchSettings(&clientconfig.Config{ServerURL: srv.URL, Auth: true, Token: "${PROBE_TOKEN}"}); note == "" {
		t.Error("fetchSettings on a foreign listener should say it is not a server")
	}
	for i, h := range seen() {
		if h != "" {
			t.Errorf("request %d carried Authorization %q to a foreign listener", i, h)
		}
	}
}

// The callers that go on to token-carrying calls after a /health that said
// nothing against the listener (sync with named KBs, reindex, kb repair,
// kb secrets, surveyor ping, usage and settings reports) get no token to the
// foreign listener either: the client refuses to send it.
func TestForeignListenerGetsNoTokenFromTheCallersAfterHealth(t *testing.T) {
	srv, seen := healthRecorder(t, `{"status":"ok"}`)
	cfg := clientconfig.Default()
	cfg.ServerURL = srv.URL + "/mcp"
	cfg.Auth, cfg.Token = true, "sekret"
	cfg.UsageScan = true

	token := "sekret"
	newClient := func() *client.MCPClient { return client.New(cfg.ServerURL, token).WithInsecureHTTP(true) }
	health, err := newClient().Health(time.Second)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	targets, err := resolveKBTargets(health, []string{"kb-a"})
	if err != nil {
		t.Fatalf("resolveKBTargets: %v", err)
	}
	if _, err := callTool(newClient().WithKB(targets[0].Name), targets[0], "reindex", map[string]any{}); err == nil {
		t.Error("reindex call reached a foreign listener")
	}
	if _, err := callTool(repairCaller(cfg, token, targets[0], t.TempDir(), io.Discard).c, targets[0], "kb_repair", map[string]any{}); err == nil {
		t.Error("kb repair call reached a foreign listener")
	}
	if err := newClient().Ping(time.Second); err == nil {
		t.Error("surveyor ping reached a foreign listener")
	}
	if err := newClient().ReportUsage("kb-a", []int{}, time.Second); err == nil {
		t.Error("usage report reached a foreign listener")
	}
	for i, h := range seen() {
		if h != "" {
			t.Errorf("request %d carried Authorization %q to a foreign listener", i, h)
		}
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
	saved := connectServiceBackend
	t.Cleanup(func() { connectServiceBackend = saved })
	connectServiceBackend = func() service.Backend { return service.BackendSystemd }
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

func TestShouldOfferServiceInstall_NotWithoutAServiceManager(t *testing.T) {
	saved := connectServiceBackend
	t.Cleanup(func() { connectServiceBackend = saved })
	connectServiceBackend = func() service.Backend { return service.BackendNone }
	if shouldOfferServiceInstall(true, false) {
		t.Error("the install was offered on a host with no service manager")
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

// #1302: a first connect writes .cartographer.yaml already migrated, so the
// first sync rewrites nothing; a client that already has agents keeps its
// marker unset, because its pre-approval installs are still to be migrated.
func TestConnectWritesTheConfigTheFirstSyncWouldWrite(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"one": ""}, "sync_pull", func(kb string) string {
		return pulledHookPayload(t, kb)
	})
	defer srv.Close()
	dir := t.TempDir()
	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer"}); err != nil {
		t.Fatal(err)
	}
	cfg := loadClientCfg(t, dir)
	if !cfg.ApprovalsMigrated {
		t.Fatal("connect left approvals_migrated unset")
	}
	before, err := os.ReadFile(clientconfig.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	withStdout(t, func() {
		if _, err := runSync(dir, cfg, syncOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	after, _ := os.ReadFile(clientconfig.Path(dir))
	if !bytes.Equal(before, after) {
		t.Errorf("the first sync rewrote the config:\n--- connect\n%s\n--- sync\n%s", before, after)
	}

	// An already connected client keeps the migration for its first sync.
	dir2 := t.TempDir()
	old := clientconfig.Default()
	old.ServerURL, old.Agents = srv.URL+"/mcp", []string{"codex"}
	if err := clientconfig.Save(dir2, old); err != nil {
		t.Fatal(err)
	}
	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir2, ServerURL: srv.URL + "/mcp", Name: "cartographer"}); err != nil {
		t.Fatal(err)
	}
	if loadClientCfg(t, dir2).ApprovalsMigrated {
		t.Error("connect spent the migration of a client that was already connected")
	}
}

// TestLockHoldsNothing_UnreadableLockKeepsTheMigration pins #1302's guard: only
// an absent or empty lockfile lets connect mark the approvals migrated; an
// unparseable one may hide an earlier install, so the first sync must migrate.
func TestLockHoldsNothing_UnreadableLockKeepsTheMigration(t *testing.T) {
	dir := t.TempDir()
	if !lockHoldsNothing(dir) {
		t.Fatal("an absent lockfile must hold nothing")
	}
	if err := os.WriteFile(lockFilePath(dir), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if lockHoldsNothing(dir) {
		t.Fatal("an unparseable lockfile must not count as empty")
	}
}

// #1299: connect classifies why the sync_pull after the config write failed.
// A transport failure defers the skill sync; a refused credential or a pin
// that does not verify is an error, and .cartographer.yaml is not saved.
func TestDoConnect_ClassifiesSyncPullFailure(t *testing.T) {
	status := func(code int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", code)
		}))
	}
	opts := func(url string) connectOptions {
		return connectOptions{Providers: []string{"claude"}, Dir: t.TempDir(), ServerURL: url + "/mcp", Name: "cartographer", Auth: true, Token: "${KB_A_TOKEN}"}
	}
	t.Setenv("KB_A_TOKEN", "wrong")

	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint("http ", code), func(t *testing.T) {
			setHome(t, t.TempDir())
			srv := status(code)
			defer srv.Close()
			o := opts(srv.URL)
			_, err := doConnect(o)
			if err == nil || !strings.Contains(err.Error(), "rejected the token") || !strings.Contains(err.Error(), "$KB_A_TOKEN") || strings.Contains(err.Error(), "unreachable") {
				t.Fatalf("err = %v, want the rejected token and its source named, not unreachable", err)
			}
			if _, statErr := os.Stat(clientconfig.Path(o.Dir)); statErr == nil {
				t.Errorf("%s was saved after a rejected token", clientconfig.FileName)
			}
		})
	}

	t.Run("signature", func(t *testing.T) {
		setHome(t, t.TempDir())
		pub, _, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatal(err)
		}
		srv := syncPullServer(t, func(kb string) (string, bool) { return syncPullPayload(t, kb, "skill-"+kb), false })
		defer srv.Close()
		o := opts(srv.URL)
		o.Auth, o.Token, o.KBs = false, "", []string{"one"}
		o.PinKeys = []string{"one=" + hex.EncodeToString(pub)}
		_, err = doConnect(o)
		if !errors.Is(err, provisioning.ErrSignature) || !strings.Contains(err.Error(), "one") || strings.Contains(err.Error(), "unreachable") {
			t.Fatalf("err = %v, want a signature error naming the KB", err)
		}
		if _, statErr := os.Stat(clientconfig.Path(o.Dir)); statErr == nil {
			t.Errorf("%s was saved with the wrong pin", clientconfig.FileName)
		}
	})

	t.Run("transport failure still defers", func(t *testing.T) {
		setHome(t, t.TempDir())
		res, err := doConnect(opts("http://127.0.0.1:1"))
		if err != nil || !res.Deferred || !isTransportErr(res.DeferredErr) || deferralReason(res.DeferredErr) != "server unreachable" {
			t.Fatalf("res = %+v, err = %v, want a deferral for a transport error", res, err)
		}
	})

	t.Run("an answering server is not unreachable", func(t *testing.T) {
		setHome(t, t.TempDir())
		srv := status(http.StatusNotFound)
		defer srv.Close()
		o := opts(srv.URL + "/routed")
		o.ServerURL = srv.URL + "/mcp/routed"
		res, err := doConnect(o)
		if err != nil || !res.Deferred {
			t.Fatalf("res = %+v, err = %v, want a deferral", res, err)
		}
		if isTransportErr(res.DeferredErr) || strings.Contains(deferralReason(res.DeferredErr), "unreachable") {
			t.Errorf("a 404 from the health route is a transport failure: %v", res.DeferredErr)
		}
		if _, probeErr := probeServer(o); isTransportErr(probeErr) {
			t.Errorf("the not-responding hint would print for an answering server: %v", probeErr)
		}
	})
}

// #1299 review: an answering server is never worded "unreachable" by the probe.
func TestProbeErrorMessage_AnsweringServerIsNotUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", http.StatusNotFound) }))
	defer srv.Close()
	state, err := probeServer(connectOptions{ServerURL: srv.URL + "/mcp/routed"})
	msg := probeErrorMessage(state, err)
	if state != probeUnreachable || isTransportErr(err) || strings.Contains(msg, "unreachable") || !strings.Contains(msg, "answered") {
		t.Errorf("state=%v err=%v msg=%q", state, err, msg)
	}
	if _, derr := probeServer(connectOptions{ServerURL: "http://127.0.0.1:1/mcp"}); !isTransportErr(derr) {
		t.Errorf("refused connection is a transport error: %v", derr)
	}
}

// connect --server-url <the default> on a file without server_url records it:
// it was chosen, and a later change of the default must not repoint the client.
func TestDoConnect_ExplicitDefaultURLIsPersisted(t *testing.T) {
	if _, err := client.New(defaults.DefaultMCPURL, "").Health(time.Second); err == nil {
		t.Skip("a local server answers on the default endpoint")
	}
	t.Setenv("CARTOGRAPHER_SERVER_URL", "")
	dir := t.TempDir()
	if err := os.WriteFile(clientconfig.Path(dir), []byte("auth: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: defaults.DefaultMCPURL, Name: "cartographer"}); err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	data, _ := os.ReadFile(clientconfig.Path(dir))
	if !strings.Contains(string(data), "\n  url: "+defaults.DefaultMCPURL) {
		t.Errorf("the explicit server.url was not persisted:\n%s", data)
	}
}

// The MCP restart hint names only providers that got an MCP entry; a cli
// provider is told to load skills, a none provider gets nothing (#1787).
func TestPrintConnectResult_RestartHintSkipsProvidersWithoutMCPEntry(t *testing.T) {
	oldCLI, oldNone := usesCLI, usesNoMCP
	usesCLI = func(p configurator.Provider) bool { return p == "cli-provider" }
	usesNoMCP = func(p configurator.Provider) bool { return p == "none-provider" }
	t.Cleanup(func() { usesCLI, usesNoMCP = oldCLI, oldNone })

	run := func(ps ...string) string {
		return withStdout(t, func() {
			printConnectResult(t.TempDir(), ps, connectOptions{}, connectResult{Providers: ps})
		})
	}
	if out := run("cli-provider", "none-provider"); strings.Contains(out, "MCP tools") || strings.Contains(out, "none-provider sessions") {
		t.Errorf("output = %q, want no MCP hint and no none-provider hint", out)
	} else if !strings.Contains(out, "restart the cli-provider sessions to load the skills and instructions") {
		t.Errorf("output = %q, want the skills hint for the cli provider", out)
	}
	out := run("claude", "cli-provider")
	if !strings.Contains(out, "restart the claude sessions to load the MCP tools") || strings.Contains(out, "claude, cli-provider") {
		t.Errorf("output = %q, want the MCP hint naming only claude", out)
	}
}
