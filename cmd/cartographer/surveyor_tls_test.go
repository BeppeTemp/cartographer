package main

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// surveyorTLSConfig writes a Surveyor config for url with the given ca_file line.
func surveyorTLSConfig(t *testing.T, url, caLine string) string {
	t.Helper()
	t.Setenv("SURVEYOR_TOKEN", surveyorCanary+"-token")
	t.Setenv("CLOUD_API_KEY", surveyorCanary+"-key")
	body := "name: docs-night\nserver_url: " + url + "/mcp\n" + caLine + "token_env: SURVEYOR_TOKEN\nkbs: [kb-a]\n" +
		"models:\n  - name: cloud\n    provider: anthropic\n    model: some-model\n    api_key_env: CLOUD_API_KEY\n    budget: {daily_tokens: 1000}\n" +
		"schedule:\n  window: {start: \"23:00\", end: \"06:00\", timezone: UTC}\ntasks:\n  - kind: noop\n    model: cloud\n"
	p := filepath.Join(t.TempDir(), "surveyor.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// AC-tls-native-16
func TestSurveyorStartsAgainstPrivateCA(t *testing.T) {
	t.Cleanup(func() { _ = client.SetServerCA("", "") })
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"status":"ok","version":"test","ready":true}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+surveyorCanary+"-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(ca, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	// Without the file the self-signed certificate is refused.
	p := surveyorTLSConfig(t, srv.URL, "")
	if _, err := surveyorStartup(context.Background(), p, noop(), fastRetry(20*time.Millisecond), &strings.Builder{}); err == nil {
		t.Fatal("start without ca_file against a private CA must fail")
	}

	// With it the health check passes (the start may still stop later, on the stub's answers).
	p = surveyorTLSConfig(t, srv.URL, "ca_file: "+ca+"\n")
	_, err := surveyorStartup(context.Background(), p, noop(), fastRetry(time.Second), &strings.Builder{})
	if err != nil && strings.Contains(err.Error(), "certificate") {
		t.Fatalf("ca_file not honoured: %v", err)
	}

	// An unreadable file refuses the start and names the key.
	p = surveyorTLSConfig(t, srv.URL, "ca_file: "+filepath.Join(t.TempDir(), "missing.pem")+"\n")
	_, err = surveyorStartup(context.Background(), p, noop(), fastRetry(20*time.Millisecond), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "ca_file") {
		t.Fatalf("unreadable ca_file: want an error naming ca_file, got %v", err)
	}
}

// A certificate the machine does not accept cannot clear by itself: the start
// stops at once with exit code surveyorExitStart and names ca_file, never
// waiting out the retry budget.
func TestSurveyorStopsAtOnceOnUnacceptedCertificate(t *testing.T) {
	t.Cleanup(func() { _ = client.SetServerCA("", "") })
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok","version":"test","ready":true}`))
	}))
	defer srv.Close()
	retry := retryPolicy{total: 5 * time.Minute, first: time.Second, max: time.Second,
		sleep: func(context.Context, time.Duration) bool {
			t.Error("a rejected certificate must not be retried")
			return false
		}}
	p := surveyorTLSConfig(t, srv.URL, "")
	begin := time.Now()
	_, err := surveyorStartup(context.Background(), p, noop(), retry, &strings.Builder{})
	if el := time.Since(begin); el > 10*time.Second {
		t.Fatalf("start took %s, want it to stop at once", el)
	}
	se, ok := err.(*startError)
	if !ok || se.code != surveyorExitStart {
		t.Fatalf("want a start error with code %d, got %#v", surveyorExitStart, err)
	}
	if !strings.Contains(err.Error(), "set ca_file in the Surveyor config to the PEM of the server's CA") {
		t.Fatalf("message does not name the fix: %v", err)
	}
}

// The ca_file error names the Surveyor key, not the client's server.ca_file.
func TestSurveyorCAFileErrorNamesSurveyorKey(t *testing.T) {
	t.Cleanup(func() { _ = client.SetServerCA("", "") })
	p := surveyorTLSConfig(t, "https://example.com", "ca_file: "+filepath.Join(t.TempDir(), "missing.pem")+"\n")
	_, err := surveyorStartup(context.Background(), p, noop(), fastRetry(20*time.Millisecond), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "(ca_file in the Surveyor config)") || strings.Contains(err.Error(), "server.ca_file") {
		t.Fatalf("want the Surveyor key named, got %v", err)
	}
}
