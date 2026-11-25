//go:build !windows

// The tests stop serve with a SIGTERM to their own process (syscall.Kill),
// which Windows has no equivalent of.

package main

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// tlsServer is a serveHTTP running with tls.enabled on a certificate made for
// the test; stop ends it with the signal the real server handles.
type tlsServer struct {
	addr   string
	client *http.Client
	stop   func()
}

func startTLSServer(t *testing.T, authCfg config.AuthConfig, tlsCfg config.TLSConfig, withKB bool) *tlsServer {
	t.Helper()
	credDir := t.TempDir()
	makeTLSPair(t, credDir, "localhost", time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
	pool := x509.NewCertPool()
	crt, err := os.ReadFile(filepath.Join(credDir, "tls.crt"))
	if err != nil {
		t.Fatal(err)
	}
	pool.AppendCertsFromPEM(crt)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	boot := func(phase func(string)) kbBootstrap {
		if !withKB {
			select {} // stays bootstrapping: /health answers, nothing else
		}
		k, err := kb.Open(kbtest.New(t).Root)
		if err != nil {
			t.Errorf("kb.Open: %v", err)
			return kbBootstrap{}
		}
		return kbBootstrap{kbs: []*kb.KB{k}, names: []string{"kb-a"}, artifactSigners: make([]ed25519.PrivateKey, 1), sqlIdxs: map[string]*sqlindex.Index{}}
	}
	cfg := &config.Config{CredentialsDir: credDir}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = serveHTTP(addr, boot, authCfg, nil, false, nil, nil, cfg, tlsCfg)
	}()
	s := &tlsServer{addr: addr, client: &http.Client{
		Timeout:   gitxtest.Deadline(5 * time.Second),
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost"}},
	}}
	var stopped bool
	s.stop = func() {
		if stopped {
			return
		}
		stopped = true
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(gitxtest.Deadline(10 * time.Second)):
			t.Error("serveHTTP did not return after SIGTERM")
		}
	}
	t.Cleanup(s.stop)
	for deadline := time.Now().Add(gitxtest.Deadline(5 * time.Second)); ; time.Sleep(20 * time.Millisecond) {
		resp, err := s.client.Get("https://" + addr + "/health")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the TLS port never answered: %v", err)
		}
	}
	return s
}

// AC-tls-native-9: with TLS on a TLS request is served and carries
// Strict-Transport-Security with the configured max-age, none when it is 0;
// a plain request on the same port is a 400 with no body of KB data.
func TestTLSListenerServesHSTS(t *testing.T) {
	for _, tc := range []struct {
		name string
		age  time.Duration
		want string
	}{
		{"configured", 4380 * time.Hour, "max-age=15768000"},
		{"off", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := startTLSServer(t, config.AuthConfig{Mode: "off"}, config.TLSConfig{Enabled: true, HSTSMaxAge: tc.age}, false)
			resp, err := s.client.Get("https://" + s.addr + "/health")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("TLS /health = %d", resp.StatusCode)
			}
			if got := resp.Header.Get("Strict-Transport-Security"); got != tc.want {
				t.Errorf("Strict-Transport-Security = %q, want %q", got, tc.want)
			}

			plain, err := http.Get("http://" + s.addr + "/health")
			if err == nil {
				body, _ := io.ReadAll(plain.Body)
				plain.Body.Close()
				if plain.StatusCode != http.StatusBadRequest || strings.Contains(string(body), `"status"`) {
					t.Errorf("plain request on the TLS port = %d %q, want 400 and no health data", plain.StatusCode, body)
				}
				if plain.Header.Get("Strict-Transport-Security") != "" {
					t.Error("a plain response carries HSTS")
				}
			}
		})
	}
}

// AC-tls-native-8 (the serveHTTP half): a missing certificate pair stops the
// server with an error naming the expected path, and no port is ever bound.
func TestServeHTTPTLSStopsOnMissingPair(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	credDir := t.TempDir()
	boot := func(func(string)) kbBootstrap { t.Error("bootstrap started"); return kbBootstrap{} }
	err = serveHTTP(addr, boot, config.AuthConfig{Mode: "off"}, nil, false, nil, nil, &config.Config{CredentialsDir: credDir}, config.TLSConfig{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), filepath.Join(credDir, "tls.crt")) {
		t.Fatalf("serveHTTP error = %v, want one naming tls.crt", err)
	}
	again, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the port was bound by a server that refused to start: %v", err)
	}
	again.Close()
}
