package client

import (
	"net/http"
	"net/http/httptest"
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
