package client

import (
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckServerURL(t *testing.T) {
	cases := []struct {
		url       string
		allow     bool
		wantError bool
	}{
		{"https://example.com/mcp", false, false},
		{"http://localhost:39273/mcp", false, false},
		{"http://LOCALHOST./mcp", false, false},
		{"http://127.0.0.1:39273/mcp", false, false},
		{"http://127.0.0.2/mcp", false, false},
		{"http://[::1]:39273/mcp", false, false},
		{"http://[::ffff:127.0.0.1]/mcp", false, false},
		{"http://10.0.0.5/mcp", false, true},
		{"http://10.0.0.5/mcp", true, false},
		{"http://example.com/mcp", false, true},
		{"http://example.com/mcp", true, false},
		// A DNS name that merely starts like an address is not loopback.
		{"http://127.0.0.1.example.com/mcp", false, true},
		{"ftp://example.com", false, true},
		{"ftp://example.com", true, true},
		{"", false, true},
		{"", true, true},
	}
	for _, tc := range cases {
		err := CheckServerURL(tc.url, tc.allow)
		if (err != nil) != tc.wantError {
			t.Errorf("CheckServerURL(%q, %v) error = %v, wantError %v", tc.url, tc.allow, err, tc.wantError)
		}
	}
}

func TestCheckServerURL_ErrorNamesTheFixes(t *testing.T) {
	err := CheckServerURL("http://10.0.0.5/mcp", false)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"https://", "--allow-insecure-http", InsecureHTTPEnv, "nothing was modified"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// A refused URL never dials: the request counter of a real listener stays at zero.
func TestClient_RefusesBeforeAnyDial(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	// Same listener under a non-loopback name: the check is on the URL, so the
	// host is rewritten to a name that is not loopback and would not resolve.
	u := strings.Replace(srv.URL, "127.0.0.1", "127.0.0.1.example.com", 1)
	c := New(u+"/mcp", "tok")
	if _, err := c.Health(time.Second); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("Health error = %v, want a refusal", err)
	}
	if _, err := c.Call("x", nil); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("Call error = %v, want a refusal", err)
	}
	if err := c.ReportUsage("kb-a", nil, time.Second); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("ReportUsage error = %v, want a refusal", err)
	}
	if hits != 0 {
		t.Errorf("server was dialed %d times", hits)
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "LocalHost.": true, "127.0.0.1": true, "::1": true,
		"127.0.0.1.example.com": false, "example.com": false, "": false, "10.0.0.5": false,
	} {
		if got := IsLoopbackHost(host); got != want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// An https endpoint that redirects to a cleartext remote must not receive the
// token on the second hop (Go keeps Authorization on same-host redirects).
func TestRedirectPolicy_RefusesDowngrade(t *testing.T) {
	mk := func(target string) *http.Request {
		r, _ := http.NewRequest(http.MethodGet, target, nil)
		return r
	}
	via := []*http.Request{mk("https://example.com/mcp")}
	if err := RedirectPolicy(false)(mk("http://example.com/mcp"), via); err == nil {
		t.Error("https -> remote http redirect was followed")
	}
	// The opt-in covers an http:// server URL, not a downgrade from https.
	if err := RedirectPolicy(true)(mk("http://example.com/mcp"), via); err == nil {
		t.Error("opt-in followed an https -> http downgrade")
	}
	if err := RedirectPolicy(true)(mk("http://example.com/b"), []*http.Request{mk("http://example.com/a")}); err != nil {
		t.Errorf("opt-in should follow http -> http: %v", err)
	}
	if err := RedirectPolicy(false)(mk("https://example.com/other"), via); err != nil {
		t.Errorf("https -> https refused: %v", err)
	}
	if err := RedirectPolicy(false)(mk("http://127.0.0.1/x"), via); err == nil {
		t.Error("https -> loopback http downgrade was followed")
	}
	if err := RedirectPolicy(true)(mk("http://127.0.0.1/x"), via); err == nil {
		t.Error("https -> http downgrade was followed under the opt-in")
	}
	if err := RedirectPolicy(false)(mk("http://127.0.0.1/y"), []*http.Request{mk("http://127.0.0.1/x")}); err != nil {
		t.Errorf("loopback http -> loopback http refused: %v", err)
	}
}

func TestClient_DoesNotFollowRedirectToCleartext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.5/mcp", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	if _, err := New(srv.URL+"/mcp", "tok").Call("x", nil); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("Call error = %v, want the redirect refused", err)
	}
}

func TestWithHTTPTimeoutCopiesTheClient(t *testing.T) {
	c := New("https://example.com/mcp", "tok")
	short := c.WithHTTPTimeout(5 * time.Second)
	if short.HTTP.Timeout != 5*time.Second || c.HTTP.Timeout != 30*time.Second {
		t.Errorf("timeouts: copy %v, original %v", short.HTTP.Timeout, c.HTTP.Timeout)
	}
	if short.Token != "tok" || short.ServerURL != c.ServerURL {
		t.Error("the copy lost the client's settings")
	}
}

// writeServerCA writes the certificate of a test TLS server as a PEM file.
func writeServerCA(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// AC-tls-native-14: the CA file is trusted for the server host only.
func TestServerCAScopedToTheServerHost(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	srv := httptest.NewTLSServer(http.HandlerFunc(ok))
	defer srv.Close()
	other := httptest.NewTLSServer(http.HandlerFunc(ok))
	defer other.Close()
	t.Cleanup(func() { _ = SetServerCA("", "") })
	hc := HTTPClient(&http.Client{}, false)

	if _, err := hc.Get(srv.URL); err == nil || !strings.Contains(err.Error(), "x509") {
		t.Fatalf("without the file the request must fail verification, got %v", err)
	}
	if err := SetServerCA(writeServerCA(t, srv), srv.URL+"/mcp"); err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatalf("with the file the request must succeed: %v", err)
	}
	resp.Body.Close()
	if _, err := hc.Get(other.URL); err == nil || !strings.Contains(err.Error(), "x509") {
		t.Fatalf("another host must not trust the file, got %v", err)
	}
	if err := SetServerCA(filepath.Join(t.TempDir(), "missing.pem"), srv.URL); err == nil || !strings.Contains(err.Error(), "ca_file") {
		t.Fatalf("an unreadable file must be an error naming the key, got %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetServerCA(bad, srv.URL); err == nil || !strings.Contains(err.Error(), "ca_file") {
		t.Fatalf("a file with no certificate must be an error naming the key, got %v", err)
	}
}

// A CA file error never satisfies errors.Is(os.ErrNotExist): callers read that
// as "no .cartographer.yaml" and would write defaults over the user's config.
func TestSetServerCAMissingFileIsNotErrNotExist(t *testing.T) {
	t.Cleanup(func() { _ = SetServerCA("", "") })
	missing := filepath.Join(t.TempDir(), "missing.pem")
	err := SetServerCA(missing, "https://example.com/mcp")
	var cae *CAFileError
	if !errors.As(err, &cae) {
		t.Fatalf("want a *CAFileError, got %v", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing CA file must not read as a missing config: %v", err)
	}
	if !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "server.ca_file") {
		t.Errorf("the error must name the path and the key: %v", err)
	}
}

// A second config (a workspace's) without ca_file, or for another server, must
// not clear or override the main server's CA.
func TestServerCASurvivesAnotherRegistration(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	srv := httptest.NewTLSServer(http.HandlerFunc(ok))
	defer srv.Close()
	other := httptest.NewTLSServer(http.HandlerFunc(ok))
	defer other.Close()
	t.Cleanup(func() { _ = SetServerCA("", "") })
	hc := HTTPClient(&http.Client{}, false)

	if err := SetServerCA(writeServerCA(t, srv), srv.URL+"/mcp"); err != nil {
		t.Fatal(err)
	}
	// Another server registers its own CA: both hosts verify.
	if err := SetServerCA(writeServerCA(t, other), other.URL+"/mcp"); err != nil {
		t.Fatal(err)
	}
	// A config without ca_file for the other server removes only that entry.
	if err := SetServerCA("", other.URL+"/mcp"); err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatalf("the main server must still verify: %v", err)
	}
	resp.Body.Close()
	if _, err := hc.Get(other.URL); err == nil {
		t.Error("the removed entry must not trust its host any more")
	}
}

// https://example.com and example.com:443 are the same endpoint.
func TestCanonicalHostDefaultsThePort(t *testing.T) {
	for raw, want := range map[string]string{
		"https://Example.com/mcp":     "example.com:443",
		"https://example.com:443/mcp": "example.com:443",
		"http://example.com/mcp":      "example.com:80",
		"https://example.com:8443/x":  "example.com:8443",
		"https://[::1]/mcp":           "[::1]:443",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := canonicalHost(u); got != want {
			t.Errorf("canonicalHost(%q) = %q, want %q", raw, got, want)
		}
	}
	t.Cleanup(func() { _ = SetServerCA("", "") })
	pemPath := filepath.Join(t.TempDir(), "ca.pem")
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	if err := os.WriteFile(pemPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetServerCA(pemPath, "https://example.com/mcp"); err != nil {
		t.Fatal(err)
	}
	if ServerRoots("example.com:443") == nil || ServerRoots("EXAMPLE.com") == nil {
		t.Error("a URL without a port and a request to :443 must share the scope")
	}
	if ServerRoots("example.com:8443") != nil {
		t.Error("another port is another endpoint")
	}
}
