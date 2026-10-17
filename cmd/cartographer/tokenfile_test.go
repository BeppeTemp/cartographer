package main

// The private token file (D699): written by connect, read by unattended runs.
// Nothing here registers a launchd/systemd job: HOME is a temp dir and only
// the connect/sync/doctor seams are exercised.

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func authCfg() *clientconfig.Config {
	return &clientconfig.Config{ServerURL: "https://example.test/mcp", Auth: true, TokenEnv: "CARTOGRAPHER_TEST_TOKEN", Agents: []string{"claude"}}
}

func TestDoConnect_WritesTokenFileWithAuth(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("CARTOGRAPHER_TEST_TOKEN", "secret-1")
	opts := connectOptions{Providers: []string{"claude"}, Dir: home, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer", Auth: true, TokenEnv: "CARTOGRAPHER_TEST_TOKEN", Trust: true}
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
	opts := connectOptions{Providers: []string{"claude"}, Dir: home, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer", TokenEnv: "CARTOGRAPHER_TEST_TOKEN", Trust: true}
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
