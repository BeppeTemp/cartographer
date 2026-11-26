//go:build !windows

package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

func healthDo(t *testing.T, method, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Timeout: gitxtest.Deadline(5 * time.Second)}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	resp.Body.Close()
	return resp
}

// waitBootGateOpen waits until /ready on the health address stops answering
// the boot gate's 503: startTLSServer returns once /health answers, which the
// gate does too, so a test that needs the mounted KBs would race the bootstrap.
func waitBootGateOpen(t *testing.T, health string) {
	t.Helper()
	for deadline := time.Now().Add(gitxtest.Deadline(5 * time.Second)); ; time.Sleep(20 * time.Millisecond) {
		if healthDo(t, "GET", "http://"+health+"/ready").StatusCode != http.StatusServiceUnavailable {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the boot gate never opened")
		}
	}
}

// AC-tls-native-10: the health listener answers GET and HEAD of /health and
// /ready only, every other route and method with 404, and never sets HSTS.
func TestHealthListenerServesOnlyProbes(t *testing.T) {
	s := startTLSServer(t, config.AuthConfig{Mode: "off"}, config.TLSConfig{Enabled: true, HSTSMaxAge: 4380 * time.Hour}, true)
	if s.health == "" {
		t.Fatal("no health address")
	}
	if host, _, _ := net.SplitHostPort(s.health); host != "127.0.0.1" {
		t.Fatalf("health address %q is not loopback", s.health)
	}
	waitBootGateOpen(t, s.health)
	base := "http://" + s.health
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/health", 200},
		{"HEAD", "/health", 200},
		{"GET", "/ready", 200},
		{"HEAD", "/ready", 200},
		{"POST", "/health", 404},
		{"DELETE", "/ready", 404},
		{"GET", "/health/", 404},
		{"POST", "/mcp", 404},
		{"GET", "/mcp", 404},
		{"GET", "/ui/", 404},
		{"GET", "/.well-known/oauth-protected-resource", 404},
		{"GET", "/", 404},
	} {
		resp := healthDo(t, tc.method, base+tc.path)
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
		if resp.Header.Get("Strict-Transport-Security") != "" {
			t.Errorf("%s %s carries HSTS", tc.method, tc.path)
		}
	}
}

// /ready needs a token once auth is on, as on the public listener.
func TestHealthListenerReadyNeedsTokenWithAuth(t *testing.T) {
	s := startTLSServer(t, config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{{Token: "tok-example-0123456789abcdef", Scopes: []string{"kb:kb-a:rw"}}}}, config.TLSConfig{Enabled: true}, true)
	waitBootGateOpen(t, s.health)
	if got := healthDo(t, "GET", "http://"+s.health+"/ready").StatusCode; got != http.StatusUnauthorized {
		t.Errorf("GET /ready without a token = %d, want 401", got)
	}
	if got := healthDo(t, "GET", "http://"+s.health+"/health").StatusCode; got != http.StatusOK {
		t.Errorf("GET /health = %d, want 200", got)
	}
}

func TestHealthListenerOff(t *testing.T) {
	if got := config.TLSHealthAddr("127.0.0.1:39273", config.TLSConfig{Enabled: true, HealthAddr: config.TLSHealthOff}); got != "" {
		t.Fatalf("TLSHealthAddr off = %q", got)
	}
	ln, err := listenHealth("")
	if ln != nil || err != nil {
		t.Fatalf("listenHealth(\"\") = %v, %v: want nothing bound", ln, err)
	}
}

// Shutdown closes both listeners.
func TestHealthListenerClosedOnShutdown(t *testing.T) {
	s := startTLSServer(t, config.AuthConfig{Mode: "off"}, config.TLSConfig{Enabled: true}, false)
	s.stop()
	for _, a := range []string{s.addr, s.health} {
		if c, err := net.DialTimeout("tcp", a, time.Second); err == nil {
			c.Close()
			t.Errorf("%s still listens after shutdown", a)
		}
	}
}

// AC-tls-native-12: a health address that is in use, or a public port that is,
// stops the start with an error naming the address, and neither listener is
// left bound.
func TestTLSStartReleasesPortsOnBindError(t *testing.T) {
	credDir := t.TempDir()
	makeTLSPair(t, credDir, "localhost", time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
	cfg := &config.Config{CredentialsDir: credDir}
	boot := func(func(string)) kbBootstrap { select {} }
	free := func() (net.Listener, string) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return ln, ln.Addr().String()
	}

	// A port closed here to be "free" can be taken by another process before
	// the server binds it (the race of #2409): each subtest retries with new
	// ports, up to 3 times, and fails with the last real error.
	retry := func(t *testing.T, attempt func() error) {
		t.Helper()
		var err error
		for i := 0; i < 3; i++ {
			if err = attempt(); err == nil {
				return
			}
		}
		t.Fatal(err)
	}

	t.Run("health port taken", func(t *testing.T) {
		retry(t, func() error {
			pub, pubAddr := free()
			pub.Close()
			taken, takenAddr := free()
			defer taken.Close()
			err := serveHTTP(pubAddr, boot, config.AuthConfig{Mode: "off"}, nil, false, nil, nil, cfg, config.TLSConfig{Enabled: true, HealthAddr: takenAddr})
			if err == nil || !strings.Contains(err.Error(), takenAddr) {
				return fmt.Errorf("serveHTTP error = %v, want one naming %s", err, takenAddr)
			}
			again, err := net.Listen("tcp", pubAddr)
			if err != nil {
				return fmt.Errorf("the public port was left bound: %w", err)
			}
			again.Close()
			return nil
		})
	})

	t.Run("public port taken", func(t *testing.T) {
		retry(t, func() error {
			taken, takenAddr := free()
			defer taken.Close()
			hl, hAddr := free()
			hl.Close()
			err := serveHTTP(takenAddr, boot, config.AuthConfig{Mode: "off"}, nil, false, nil, nil, cfg, config.TLSConfig{Enabled: true, HealthAddr: hAddr})
			if err == nil || !strings.Contains(err.Error(), takenAddr) {
				return fmt.Errorf("serveHTTP error = %v, want one naming %s", err, takenAddr)
			}
			again, err := net.Listen("tcp", hAddr)
			if err != nil {
				return fmt.Errorf("the health port was bound: %w", err)
			}
			again.Close()
			return nil
		})
	})
}

// The health listener is always loopback: its Host guard does not depend on the
// public address, which here is non-loopback with auth off (no guard inside).
func TestHealthServerRejectsForeignHost(t *testing.T) {
	inner := withLoopbackHostGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "0.0.0.0:8080", false)
	h := newHealthServer("127.0.0.1:0", inner).Handler
	for host, want := range map[string]int{"evil.example.com": 403, "127.0.0.1:8081": 200} {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %q = %d, want %d", host, rec.Code, want)
		}
	}
}
