package main

import (
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

func getJSON(t *testing.T, url string) (int, map[string]interface{}) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]interface{}
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m
}

// TestServeHTTPListensBeforeBootstrap: the port answers /health while the KBs
// are still being cloned (D348); /mcp is a retryable 503 until the bootstrap
// ends, then /ready flips and /health loses its bootstrapping fields.
func TestServeHTTPListensBeforeBootstrap(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	if runtime.GOOS == "windows" {
		t.Skip("shuts the server down with SIGTERM")
	}
	tmp := t.TempDir()
	remote := bareRemoteWithKB(t, tmp)
	dataDir := filepath.Join(tmp, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	release := make(chan struct{})
	boot := func(phase func(string)) kbBootstrap {
		phase("cloning")
		<-release
		dest, err := ensureClonedKB(remote, "kb-a", dataDir, "")
		if err != nil {
			t.Errorf("ensureClonedKB: %v", err)
			return kbBootstrap{}
		}
		k, err := kb.Open(dest)
		if err != nil {
			t.Errorf("kb.Open: %v", err)
			return kbBootstrap{}
		}
		return kbBootstrap{
			kbs:             []*kb.KB{k},
			names:           []string{"kb-a"},
			artifactSigners: make([]ed25519.PrivateKey, 1),
			allowlists:      make([][]provisioning.MCPAllowlistEntry, 1),
			sqlIdxs:         map[string]*sqlindex.Index{},
		}
	}
	done := make(chan struct{})
	go func() {
		serveHTTP(addr, boot, config.AuthConfig{Mode: "off"}, nil, "", false, nil, nil)
		close(done)
	}()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("serveHTTP did not return after SIGTERM")
		}
	})

	base := "http://" + addr
	deadline := time.Now().Add(5 * time.Second)
	var code int
	var health map[string]interface{}
	for {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			code, health = getJSON(t, base+"/health")
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("port never answered while bootstrapping: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code != 200 || health["status"] != "ok" || health["bootstrapping"] != true || health["phase"] != "cloning" {
		t.Fatalf("/health while bootstrapping = %d %v", code, health)
	}
	resp, err := http.Post(base+"/mcp", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") == "" {
		t.Errorf("/mcp while bootstrapping = %d retry-after %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}

	close(release)
	for deadline := time.Now().Add(10 * time.Second); ; {
		code, _ := getJSON(t, base+"/ready")
		if code == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("/ready never became 200")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, health = getJSON(t, base+"/health")
	if _, has := health["bootstrapping"]; has {
		t.Errorf("/health still carries bootstrapping after bootstrap: %v", health)
	}
}
