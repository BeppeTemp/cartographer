package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/service"
)

// tlsLocalConfig writes a TLS server config: public address 127.0.0.1:1, the
// health listener at health ("off" for none).
func tlsLocalConfig(t *testing.T, health string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "http: \"127.0.0.1:1\"\ntls:\n  enabled: true\n  health_addr: " + health + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// AC-tls-native-17: with TLS on the /health readers get the health listener's
// base; with it off, "" (no wait).
func TestServerHealthBaseURLWithTLS(t *testing.T) {
	if got := serverHealthBaseURL(tlsLocalConfig(t, "127.0.0.1:9")); got != "http://127.0.0.1:9" {
		t.Errorf("health base = %q", got)
	}
	if got := serverHealthBaseURL(tlsLocalConfig(t, "off")); got != "" {
		t.Errorf("health_addr off: base = %q, want empty", got)
	}
	cfg := filepath.Join(t.TempDir(), "plain.yaml")
	if err := os.WriteFile(cfg, []byte("http: \"127.0.0.1:7\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := serverHealthBaseURL(cfg); got != "http://127.0.0.1:7" {
		t.Errorf("TLS off: base = %q, want the listen address", got)
	}
}

// AC-tls-native-17: serverBaseURL stays the public address with TLS on.
func TestServerBaseURLUnchangedWithTLS(t *testing.T) {
	if got := serverBaseURL(tlsLocalConfig(t, "127.0.0.1:9")); got != "http://127.0.0.1:1" {
		t.Errorf("serverBaseURL = %q, want the public address", got)
	}
}

// AC-tls-native-17: kb secrets never sends /mcp to the health listener and
// reports tls.enabled.
func TestKBSecretsRefusesWithTLS(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	data := t.TempDir()
	body := "http: \"127.0.0.1:1\"\ndata: " + filepath.ToSlash(data) + "\ntls:\n  enabled: true\n  health_addr: " + strings.TrimPrefix(srv.URL, "http://") + "\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var code int
	stderr := withStderr(t, func() {
		code = cmdKBSecretsInit([]string{"kb-a", "--recipient", "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq", "--config", cfg, "--data", data, "--local"})
	})
	if code == 0 || !strings.Contains(stderr, "tls.enabled") || atomic.LoadInt32(&hits) != 0 {
		t.Errorf("code=%d hits=%d stderr=%q", code, hits, stderr)
	}
}

// AC-tls-native-17: the setup auth check is skipped with TLS on and never
// reaches the health listener.
func TestSetupAuthProbeSkippedWithTLS(t *testing.T) {
	stubSetup(t, setupFacts{Service: service.Status{Installed: true, Running: true, HTTPAddr: "127.0.0.1:1", TLS: true}})
	setupProbeAuth = func(addr string) (bool, error) {
		t.Errorf("setupProbeAuth called with %s", addr)
		return true, nil
	}
	if got := setupAuthMismatch(); got != "" {
		t.Errorf("setupAuthMismatch = %q, want nothing", got)
	}
}

// AC-tls-native-17: setup stops on a TLS server before probing it.
func TestSetupStopsOnTLSBeforeProbing(t *testing.T) {
	stubSetup(t, setupFacts{Service: service.Status{Installed: true, HTTPAddr: "127.0.0.1:1", TLS: true}})
	setupProbeListener = func(addr string) service.ListenerState {
		t.Errorf("probed %s", addr)
		return service.ListenerForeign
	}
	if _, err := gatherSetupFacts(setupOptions{}); err == nil || !strings.Contains(err.Error(), "tls.enabled") {
		t.Errorf("gatherSetupFacts err = %v, want one naming tls.enabled", err)
	}
}
