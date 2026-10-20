package main

// The private token file (D699): written by connect, read by unattended runs.
// Nothing here registers a launchd/systemd job: HOME is a temp dir and only
// the connect/sync/doctor seams are exercised.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func authCfg() *clientconfig.Config {
	return &clientconfig.Config{ServerURL: "https://example.test/mcp", Auth: true, Token: "${CARTOGRAPHER_TEST_TOKEN}", Agents: []string{"claude"}}
}

func TestDoConnect_WritesTokenFileWithAuth(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "secret-1")
	opts := connectOptions{Providers: []string{"claude"}, Dir: home, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer", Auth: true, Token: "${CARTOGRAPHER_TEST_TOKEN}"}
	if _, err := doConnect(opts); err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	if got, err := clientconfig.ReadToken(home); err != nil || got != "secret-1" {
		t.Fatalf("token file = %q, %v", got, err)
	}
	// Rotated token: rewritten.
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "secret-2")
	if _, err := doConnect(opts); err != nil {
		t.Fatal(err)
	}
	if got, _ := clientconfig.ReadToken(home); got != "secret-2" {
		t.Fatalf("token not rewritten: %q", got)
	}
	// Bare shell: the existing file is kept.
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "")
	if _, err := doConnect(opts); err != nil {
		t.Fatal(err)
	}
	if got, _ := clientconfig.ReadToken(home); got != "secret-2" {
		t.Fatalf("token lost on a bare-shell connect: %q", got)
	}
}

func TestDoConnect_NoTokenFileWithoutAuth(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "secret")
	opts := connectOptions{Providers: []string{"claude"}, Dir: home, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer", Token: "${CARTOGRAPHER_TEST_TOKEN}"}
	if _, err := doConnect(opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(clientconfig.TokenPath(home)); !os.IsNotExist(err) {
		t.Fatalf("token file written without auth: %v", err)
	}
}

func TestDisconnectRemovesTokenWithLastProvider_ReconnectKeepsIt(t *testing.T) {
	dir := setupDisconnectFixture(t, "claude")
	if err := clientconfig.WriteToken(dir, "tok"); err != nil {
		t.Fatal(err)
	}
	if _, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir, KeepToken: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := clientconfig.ReadToken(dir); got != "tok" {
		t.Fatalf("KeepToken dropped the file: %q", got)
	}
	dir2 := setupDisconnectFixture(t, "claude")
	_ = clientconfig.WriteToken(dir2, "tok")
	if _, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir2}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(clientconfig.TokenPath(dir2)); !os.IsNotExist(err) {
		t.Fatalf("token file survived the last provider: %v", err)
	}
}

func TestResolveTokenFallsBackToFile(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "")
	cfg := authCfg()
	if resolveToken(cfg) != "" {
		t.Fatal("token from nowhere")
	}
	if err := clientconfig.WriteToken(home, "from-file"); err != nil {
		t.Fatal(err)
	}
	if got := resolveToken(cfg); got != "from-file" {
		t.Fatalf("resolveToken = %q, want from-file", got)
	}
	if err := preflightEnvironment(cfg); err != nil {
		t.Fatalf("preflight must accept the token file: %v", err)
	}
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "from-env")
	if got := resolveToken(cfg); got != "from-env" {
		t.Fatalf("env must win, got %q", got)
	}
	cfg.Auth = false
	if resolveToken(cfg) != "" {
		t.Fatal("auth off must send no token")
	}
}

func TestPreflightNamesBothPlacesAndUnsafeFile(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "")
	cfg := authCfg()
	err := preflightEnvironment(cfg)
	if err == nil || !strings.Contains(err.Error(), "cartographer reconnect") || !strings.Contains(err.Error(), "$CARTOGRAPHER_TEST_TOKEN") {
		t.Fatalf("message must name the variable and reconnect: %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.WriteFile(clientconfig.TokenPath(home), []byte("tok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = preflightEnvironment(cfg)
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("unsafe token file must be named: %v", err)
	}
}

// D906: `token` is a literal or "${NAME}". The reference reads the variable and
// falls back to the private file when it is absent; a literal wins over both
// and needs neither. Auth off sends nothing whatever the token says.
func TestResolveToken_EnvRefLiteralAndFile(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := clientconfig.WriteToken(home, "from-file"); err != nil {
		t.Fatal(err)
	}
	cfg := authCfg()
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "from-env")
	if got := resolveToken(cfg); got != "from-env" {
		t.Errorf("${NAME} with the variable set = %q, want from-env", got)
	}
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "")
	if got := resolveToken(cfg); got != "from-file" {
		t.Errorf("${NAME} with the variable absent = %q, want the private file", got)
	}
	if got := tokenEnvName(cfg); got != "CARTOGRAPHER_TEST_TOKEN" {
		t.Errorf("tokenEnvName = %q", got)
	}

	cfg.Token = "literal-token"
	if got := resolveToken(cfg); got != "literal-token" {
		t.Errorf("a literal token = %q, want itself", got)
	}
	if got := tokenEnvName(cfg); got != "" {
		t.Errorf("a literal token names no variable, got %q", got)
	}
	if err := preflightEnvironment(cfg); err != nil {
		t.Errorf("a literal token needs nothing from the environment: %v", err)
	}

	cfg.Auth = false
	if got := resolveToken(cfg); got != "" {
		t.Errorf("auth off sends no token, got %q", got)
	}
}

// A connect that finds a literal token in the file keeps it, writes no token
// file, and gives the agents' MCP entries the default variable name: an entry
// cannot carry a secret. The literal never appears in an agent's config.
func TestDoConnect_KeepsALiteralToken(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	opts := connectOptions{Providers: []string{"claude"}, Dir: home, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer", Auth: true, Token: "literal-secret-value"}
	if _, err := doConnect(opts); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(home)
	if err != nil || cfg.LiteralToken() != "literal-secret-value" {
		t.Fatalf("the literal token was not kept: %+v, %v", cfg, err)
	}
	if _, err := os.Stat(clientconfig.TokenPath(home)); !os.IsNotExist(err) {
		t.Errorf("a token file was written for a literal token: %v", err)
	}
	if fi, err := os.Stat(clientconfig.Path(home)); err == nil && runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf(".cartographer.yaml holds a token and has mode %v", fi.Mode().Perm())
	}
	claude, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(claude), "literal-secret-value") || !strings.Contains(string(claude), "${CARTOGRAPHER_TOKENS}") {
		t.Errorf("the agent entry must reference $CARTOGRAPHER_TOKENS and not carry the literal:\n%s", claude)
	}
}
