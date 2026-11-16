package service

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestProbeListener(t *testing.T) {
	serve := func(h http.HandlerFunc) string {
		s := httptest.NewServer(h)
		t.Cleanup(s.Close)
		return strings.TrimPrefix(s.URL, "http://")
	}
	for _, tc := range []struct {
		name string
		addr string
		want ListenerState
	}{
		{"ours", serve(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"status":"ok","version":"v1","started_at":"x"}`))
		}), ListenerCartographer},
		{"ours, bootstrapping", serve(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"status":"ok","version":"v1","bootstrapping":true}`))
		}), ListenerCartographer},
		{"anything that says ok", serve(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"status":"ok"}`)) }), ListenerForeign},
		{"not json", serve(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`hello`)) }), ListenerForeign},
		{"404", serve(http.NotFound), ListenerForeign},
		{"nothing", "127.0.0.1:1", ListenerNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProbeListener(tc.addr, 2*time.Second); got != tc.want {
				t.Errorf("ProbeListener = %v, want %v", got, tc.want)
			}
		})
	}
}

// A 200 from another program on the server address is not a healthy service:
// Status says so, and ForeignListener names the cause (#1998).
func TestStatus_ForeignListenerIsNotHealthy(t *testing.T) {
	serve := func(h http.HandlerFunc) string {
		s := httptest.NewServer(h)
		t.Cleanup(s.Close)
		return strings.TrimPrefix(s.URL, "http://")
	}
	for _, tc := range []struct {
		name        string
		addr        string
		wantHealthy bool
		wantForeign bool
	}{
		{"ours", serve(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"status":"ok","version":"v1"}`))
		}), true, false},
		{"a 200 that is not ours", serve(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"status":"ok"}`)) }), false, true},
		{"an empty 200", serve(func(http.ResponseWriter, *http.Request) {}), false, true},
		{"nothing", "127.0.0.1:1", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := withTestHome(t, "darwin")
			configPath := filepath.Join(home, "server.yaml")
			if err := os.WriteFile(configPath, []byte("http: \""+tc.addr+"\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			m, _ := newTestManager()
			st, err := m.Status(configPath)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if !st.HealthChecked || st.Healthy != tc.wantHealthy || st.ForeignListener != tc.wantForeign {
				t.Errorf("HealthChecked=%v Healthy=%v ForeignListener=%v, want true/%v/%v", st.HealthChecked, st.Healthy, st.ForeignListener, tc.wantHealthy, tc.wantForeign)
			}
			if checkHealth(tc.addr) != tc.wantHealthy {
				t.Errorf("checkHealth(%s) = %v, want %v", tc.addr, !tc.wantHealthy, tc.wantHealthy)
			}
		})
	}
}

// A TCP listener that accepts and never answers (a database, say) holds the
// address as much as an HTTP one: it is not "nothing", so the install refuses it
// (#2141); it is not called foreign, because a busy server is as silent.
func TestProbeListener_MuteTCPListenerIsRefusedByTheInstall(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var held []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			c.Close()
		}
	})
	addr := ln.Addr().String()
	st := ProbeListener(addr, 300*time.Millisecond)
	if st != ListenerUnresponsive {
		t.Fatalf("ProbeListener = %v, want ListenerUnresponsive", st)
	}
	err = busyAddressError(addr, st)
	if err == nil || !strings.Contains(err.Error(), "a program accepted the connection on "+addr+" but did not answer /health") {
		t.Errorf("busyAddressError = %v, want the unresponsive refusal", err)
	}
	// A closed port stays "nothing": only a failed connect is.
	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln2.Addr().String()
	ln2.Close()
	if got := ProbeListener(closed, 300*time.Millisecond); got != ListenerNone {
		t.Errorf("ProbeListener(closed port) = %v, want ListenerNone", got)
	}
}

// A connection reset after the accept is not a Cartographer server: foreign.
func TestProbeListener_ResetAfterAcceptIsForeign(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Read the request first: the reset then reaches a client that is
			// past its connect, on every OS.
			c.Read(make([]byte, 1))
			c.(*net.TCPConn).SetLinger(0) // close sends RST
			c.Close()
		}
	}()
	if got := ProbeListener(ln.Addr().String(), 2*time.Second); got != ListenerForeign {
		t.Errorf("ProbeListener = %v, want ListenerForeign", got)
	}
}

// A Cartographer /health slower than the probe is unresponsive, never foreign:
// status must not blame another program for a busy server.
func TestStatus_SlowCartographerIsNotForeign(t *testing.T) {
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-release:
		case <-time.After(healthTimeout + time.Second):
		}
		w.Write([]byte(`{"status":"ok","version":"v1"}`))
	}))
	t.Cleanup(func() { close(release); s.Close() })
	addr := strings.TrimPrefix(s.URL, "http://")
	if got := ProbeListener(addr, 200*time.Millisecond); got != ListenerUnresponsive {
		t.Errorf("ProbeListener = %v, want ListenerUnresponsive", got)
	}
	home := withTestHome(t, "darwin")
	configPath := filepath.Join(home, "server.yaml")
	if err := os.WriteFile(configPath, []byte("http: \""+addr+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ := newTestManager()
	st, err := m.Status(configPath)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.ForeignListener || st.Healthy {
		t.Errorf("Healthy=%v ForeignListener=%v, want neither for a slow server", st.Healthy, st.ForeignListener)
	}
}

// deniedTransport refuses every dial the way a sandbox without network access
// does: connect(2) fails with EPERM, not ECONNREFUSED (#2165).
type deniedTransport struct{ errno error }

func (d deniedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", d.errno)}
}

// A connect the OS refused is not "nothing listens": the probe measured
// nothing, and no caller may act on it as on ListenerNone (#2165).
func TestProbeListener_DeniedConnectIsNotNone(t *testing.T) {
	for _, errno := range []error{syscall.EPERM, syscall.EACCES, wsaeacces} {
		t.Run(errno.Error(), func(t *testing.T) {
			old := healthTransport
			healthTransport = deniedTransport{errno}
			t.Cleanup(func() { healthTransport = old })
			if got := ProbeListener("127.0.0.1:39273", time.Second); got != ListenerDenied {
				t.Errorf("ProbeListener = %v, want ListenerDenied", got)
			}
			if _, err := ProbeHealth("127.0.0.1:39273", time.Second); !ProbeDenied(err) {
				t.Errorf("ProbeHealth err = %v, want a denied probe", err)
			}
			if err := busyAddressError("127.0.0.1:39273", ListenerDenied); err == nil || !strings.Contains(err.Error(), "denied by the OS") {
				t.Errorf("busyAddressError = %v, want the denied refusal", err)
			}
		})
	}
}

// Status skips the check with a reason: never Healthy=false with HealthChecked.
func TestStatus_DeniedProbeIsSkippedNotUnhealthy(t *testing.T) {
	old := healthTransport
	healthTransport = deniedTransport{syscall.EPERM}
	t.Cleanup(func() { healthTransport = old })
	home := withTestHome(t, "darwin")
	configPath := filepath.Join(home, "server.yaml")
	if err := os.WriteFile(configPath, []byte("http: \"127.0.0.1:39273\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ := newTestManager()
	st, err := m.Status(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.HealthChecked || st.Healthy || st.HealthSkipReason != HealthSkipDenied || st.HTTPAddr != "127.0.0.1:39273" {
		t.Errorf("status = checked %v healthy %v reason %q addr %q", st.HealthChecked, st.Healthy, st.HealthSkipReason, st.HTTPAddr)
	}
}

// Replace restarts nothing when the probe before the signal is refused: a
// restart it could not verify would be blind (#2165).
func TestReplace_DeniedProbeSignalsNothing(t *testing.T) {
	old := healthTransport
	healthTransport = deniedTransport{syscall.EPERM}
	t.Cleanup(func() { healthTransport = old })
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			home := withTestHome(t, goos)
			configPath := writeReplaceConfig(t, home, "127.0.0.1:39273")
			m, stub := newTestManager()
			err := m.Replace(ReplaceOptions{ConfigPath: configPath, ExpectedVersion: "v1.2.3", Timeout: time.Second, PollInterval: 10 * time.Millisecond})
			if err == nil || !strings.Contains(err.Error(), "nothing was restarted") || !strings.Contains(err.Error(), "denied by the OS") {
				t.Errorf("Replace err = %v", err)
			}
			for _, c := range stub.calls {
				if len(c) > 1 && (c[1] == "kill" || c[1] == "kickstart" || c[1] == "--user" && len(c) > 2 && c[2] == "restart") {
					t.Errorf("a restart signal was sent: %v", c)
				}
			}
		})
	}
}
