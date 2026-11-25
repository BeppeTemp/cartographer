//go:build !windows

package main

import (
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// AC-tls-native-13: serve with tls.enabled listens with TLS 1.3 only on the
// configured address and answers an MCP initialize over https with a bearer
// token; a plain request on the same address is refused and serves nothing.
func TestServeTLSInitialize(t *testing.T) {
	const token = "tls-test-token"
	s := startTLSServer(t, config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{{Token: token, Scopes: []string{"kb:kb-a:r"}}}},
		config.TLSConfig{Enabled: true, HSTSMaxAge: time.Hour}, true)

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	var out string
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		req, _ := http.NewRequest(http.MethodPost, "https://"+s.addr+"/mcp", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			out = string(raw)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("initialize over https = %d %s", resp.StatusCode, raw)
		}
	}
	if !strings.Contains(out, `"protocolVersion"`) {
		t.Errorf("initialize answer = %s", out)
	}

	// TLS 1.2 is refused: the listener is 1.3 only.
	old := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12}}, Timeout: 5 * time.Second}
	if resp, err := old.Get("https://" + s.addr + "/health"); err == nil {
		resp.Body.Close()
		t.Error("a TLS 1.2 client was served")
	}

	// A plain request on the same address serves nothing.
	plain, err := http.Post("http://"+s.addr+"/mcp", "application/json", strings.NewReader(body))
	if err == nil {
		raw, _ := io.ReadAll(plain.Body)
		plain.Body.Close()
		if plain.StatusCode == http.StatusOK || strings.Contains(string(raw), "protocolVersion") {
			t.Errorf("plain request served: %d %s", plain.StatusCode, raw)
		}
	}
}
