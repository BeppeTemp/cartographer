package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

const remoteHTTPURL = "http://10.0.0.5:39273/mcp"

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestDoConnect_RefusesRemotePlainHTTP_WritesNothing(t *testing.T) {
	t.Setenv("CARTOGRAPHER_ALLOW_INSECURE_HTTP", "")
	dir := t.TempDir()
	setHome(t, dir)
	_, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: remoteHTTPURL, Name: "cartographer"})
	if err == nil || !strings.Contains(err.Error(), "--allow-insecure-http") {
		t.Fatalf("doConnect error = %v, want a refusal naming --allow-insecure-http", err)
	}
	if got := dirEntries(t, dir); len(got) != 0 {
		t.Errorf("a refused connect wrote %v", got)
	}
}

func TestDoConnect_OptIn_PersistsKey(t *testing.T) {
	t.Setenv("CARTOGRAPHER_ALLOW_INSECURE_HTTP", "")
	dir := t.TempDir()
	setHome(t, dir)
	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: remoteHTTPURL, Name: "cartographer", AllowInsecureHTTP: true}); err != nil {
		t.Fatalf("doConnect with the opt-in: %v", err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AllowInsecureHTTP {
		t.Error("allow_insecure_http was not persisted")
	}
	raw, _ := os.ReadFile(clientconfig.Path(dir))
	if !strings.Contains(string(raw), "allow_insecure_http: true") {
		t.Errorf("config lacks the key:\n%s", raw)
	}
}

func TestPreflight_RefusesRemotePlainHTTP_EnvAndKeyAllow(t *testing.T) {
	t.Setenv("CARTOGRAPHER_ALLOW_INSECURE_HTTP", "")
	cfg := &clientconfig.Config{ServerURL: remoteHTTPURL, Agents: []string{"claude"}}
	if err := preflightEnvironment(cfg); err == nil || !strings.Contains(err.Error(), "nothing was modified") {
		t.Fatalf("preflight error = %v, want a refusal", err)
	}
	t.Setenv("CARTOGRAPHER_ALLOW_INSECURE_HTTP", "1")
	if err := preflightEnvironment(cfg); err != nil {
		t.Errorf("the env var should let it through: %v", err)
	}
	t.Setenv("CARTOGRAPHER_ALLOW_INSECURE_HTTP", "")
	cfg.AllowInsecureHTTP = true
	if err := preflightEnvironment(cfg); err != nil {
		t.Errorf("the key should let it through: %v", err)
	}
}

func TestRunSync_RefusedBeforeLockAndFiles(t *testing.T) {
	t.Setenv("CARTOGRAPHER_ALLOW_INSECURE_HTTP", "")
	dir := t.TempDir()
	setHome(t, dir)
	cfg := &clientconfig.Config{ServerURL: remoteHTTPURL, Agents: []string{"claude"}}
	if _, err := runSync(dir, cfg, syncOptions{}); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("runSync error = %v, want a refusal", err)
	}
	if got := dirEntries(t, dir); len(got) != 0 {
		t.Errorf("a refused sync wrote %v", got)
	}
}

func TestClientConfig_OlderFileLoadsWithKeyFalse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(clientconfig.Path(dir), []byte("server_url: http://127.0.0.1:39273/mcp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AllowInsecureHTTP {
		t.Error("an older file must load with allow_insecure_http false")
	}
}

func TestFetchSettings_RefusesRemotePlainHTTP(t *testing.T) {
	t.Setenv("CARTOGRAPHER_ALLOW_INSECURE_HTTP", "")
	reply, note := fetchSettings(&clientconfig.Config{ServerURL: remoteHTTPURL})
	if reply != nil || !strings.Contains(note, "server settings unavailable") || !strings.Contains(note, "refusing") {
		t.Errorf("fetchSettings = %v, %q, want the refusal as the unavailable note", reply, note)
	}
}
