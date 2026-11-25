package service

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tlsConfig writes a server config with TLS on whose health listener is
// healthAddr; the public address answers nothing.
func tlsConfig(t *testing.T, home, healthAddr string) string {
	t.Helper()
	body := "http: \"127.0.0.1:1\"\ntls:\n  enabled: true\n  health_addr: " + healthAddr + "\n"
	p := filepath.Join(home, ".config", "cartographer", "server.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// AC-tls-native-17: with TLS on, Replace and Status read /health on the
// health listener, and Status.HTTPAddr stays the public address.
func TestProbeHealthWithTLSConfig(t *testing.T) {
	home := withTestHome(t, "linux")
	srv := newHealthServer(t, healthOK("v1.2.3"))
	path := tlsConfig(t, home, serverAddr(srv.Server))
	m, _ := newTestManager()
	if err := m.Replace(ReplaceOptions{ConfigPath: path, ExpectedVersion: "v1.2.3", Timeout: time.Second, PollInterval: 10 * time.Millisecond}); err != nil {
		t.Fatalf("Replace against the health listener: %v", err)
	}
	st, err := m.Status(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Healthy || st.ProbeAddr != serverAddr(srv.Server) || !st.TLS {
		t.Errorf("status = %+v, want healthy through the health listener", st)
	}
}

// AC-tls-native-17: the wait of `service restart` reads the health listener.
func TestVerifyServingReadsProbeAddrWithTLS(t *testing.T) {
	home := withTestHome(t, "linux")
	srv := newHealthServer(t, healthOK("v1.2.3"))
	path := tlsConfig(t, home, serverAddr(srv.Server))
	m, _ := newTestManager()
	if err := m.VerifyServing(ReplaceOptions{ConfigPath: path, ExpectedVersion: "v1.2.3", Timeout: time.Second, PollInterval: 10 * time.Millisecond}); err != nil {
		t.Fatalf("VerifyServing: %v", err)
	}
}

// AC-tls-native-17: Status.HTTPAddr is the public address with TLS on; with
// tls.health_addr off the server is not probeable, not foreign.
func TestStatusKeepsPublicHTTPAddrWithTLS(t *testing.T) {
	home := withTestHome(t, "linux")
	srv := newHealthServer(t, healthOK("v1.2.3"))
	path := tlsConfig(t, home, serverAddr(srv.Server))
	m, _ := newTestManager()
	st, _ := m.Status(path)
	if st.HTTPAddr != "127.0.0.1:1" {
		t.Errorf("HTTPAddr = %q, want the public address", st.HTTPAddr)
	}
	off := tlsConfig(t, t.TempDir(), "off")
	st, _ = m.Status(off)
	if st.HealthChecked || st.ForeignListener || st.HealthSkipReason != HealthSkipNoHealthListener || st.ProbeTarget() != "" {
		t.Errorf("health_addr off: %+v", st)
	}
}

// A TLS server read in clear answers 400: not "no token required".
func TestProbeAuthRequiredNamesTLSOn400(t *testing.T) {
	srv := newHealthServer(t, func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadRequest) })
	_, err := ProbeAuthRequired(serverAddr(srv.Server), time.Second)
	if err == nil || !strings.Contains(err.Error(), "tls.enabled") {
		t.Errorf("err = %v, want one naming tls.enabled", err)
	}
}
