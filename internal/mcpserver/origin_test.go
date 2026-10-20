package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// okHandler stands in for the guarded handler and records whether it ran: a
// rejected origin must never reach it.
func okHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

func doOrigin(t *testing.T, method, path, origin string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	var reached bool
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Host = "kb.example.com"
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rr := httptest.NewRecorder()
	OriginGuard(okHandler(&reached)).ServeHTTP(rr, req)
	return rr, reached
}

// D906: the rule is fixed. An Origin matching the request's own Host is
// accepted whatever scheme it names (a TLS-terminating proxy does not have to
// be told which one it terminated), and the CORS grant echoes it.
func TestOriginGuard_SameOriginAccepted(t *testing.T) {
	t.Parallel()
	rr, reached := doOrigin(t, http.MethodPost, "/mcp", "https://kb.example.com")
	if rr.Code != http.StatusOK || !reached {
		t.Fatalf("status = %d, reached = %v; want 200 and reached", rr.Code, reached)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://kb.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the accepted origin echoed back", got)
	}
	if got := rr.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("Vary = %q, want it to name Origin", got)
	}
}

func TestOriginGuard_CrossOriginRejected(t *testing.T) {
	t.Parallel()
	rr, reached := doOrigin(t, http.MethodPost, "/mcp", "https://evil.example.net")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if reached {
		t.Error("a refused origin reached the wrapped handler")
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want no CORS grant on a refusal", got)
	}
}

// The port is part of the host: the same name on another port is another site.
func TestOriginGuard_PortMustMatch(t *testing.T) {
	t.Parallel()
	rr, _ := doOrigin(t, http.MethodPost, "/mcp", "https://kb.example.com:8443")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for the same host on another port", rr.Code)
	}
}

func TestOriginGuard_NoOriginPassesThrough(t *testing.T) {
	t.Parallel()
	rr, reached := doOrigin(t, http.MethodPost, "/mcp", "")
	if rr.Code != http.StatusOK || !reached {
		t.Fatalf("status = %d, reached = %v; want a non-browser client served normally", rr.Code, reached)
	}
}

func TestOriginGuard_PreflightChecked(t *testing.T) {
	t.Parallel()
	rr, reached := doOrigin(t, http.MethodOptions, "/mcp", "https://evil.example.net")
	if rr.Code != http.StatusForbidden || reached {
		t.Fatalf("status = %d, reached = %v; want the preflight refused too", rr.Code, reached)
	}
}

// Only the MCP endpoint is guarded: /health is what a liveness probe or an
// operator dashboard hits, and it exposes no KB content.
func TestOriginGuard_NonMCPPathUnguarded(t *testing.T) {
	t.Parallel()
	rr, reached := doOrigin(t, http.MethodGet, "/health", "https://evil.example.net")
	if rr.Code != http.StatusOK || !reached {
		t.Fatalf("status = %d, reached = %v; want /health served regardless of origin", rr.Code, reached)
	}
}

func TestOriginGuard_PerKBPathGuarded(t *testing.T) {
	t.Parallel()
	rr, _ := doOrigin(t, http.MethodPost, "/mcp/kbx", "https://evil.example.net")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 on /mcp/<kb> as well", rr.Code)
	}
}

func TestOriginGuard_CaseIgnored(t *testing.T) {
	t.Parallel()
	rr, _ := doOrigin(t, http.MethodPost, "/mcp", "https://KB.Example.com/")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want the origin matched case-insensitively without its trailing slash", rr.Code)
	}
}

// Hardening of the same-origin rule (D906): on a loopback server with
// authentication off, every route needs a loopback Host.
func TestLoopbackHostGuard(t *testing.T) {
	for _, tc := range []struct {
		host, path string
		origin     string
		ok         bool
	}{
		{"localhost:39273", "/mcp", "", true},
		{"127.0.0.1:39273", "/mcp", "http://127.0.0.1:39273", true},
		{"[::1]:39273", "/ui/", "", true},
		{"127.0.0.1", "/health", "", true},
		{"rebound.example.com:39273", "/mcp", "http://rebound.example.com:39273", false},
		{"rebound.example.com:39273", "/mcp/kb-a", "", false},
		{"rebound.example.com", "/api/ui/v1/kbs", "http://rebound.example.com", false},
		{"rebound.example.com", "/health", "", false},
		{"10.0.0.5:39273", "/ui/", "", false},
		{"", "/mcp", "", false},
	} {
		var reached bool
		req := httptest.NewRequest("POST", tc.path, strings.NewReader("{}"))
		req.Host = tc.host
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		rr := httptest.NewRecorder()
		LoopbackHostGuard(okHandler(&reached)).ServeHTTP(rr, req)
		if reached != tc.ok || (!tc.ok && rr.Code != http.StatusForbidden) {
			t.Errorf("Host %q on %s: reached=%v code=%d, want allowed=%v", tc.host, tc.path, reached, rr.Code, tc.ok)
		}
	}
}

// An Origin carrying userinfo is refused even when its host matches.
func TestOriginWithUserinfoRejected(t *testing.T) {
	if originAllowed("http://user@kb.example.com", "kb.example.com") {
		t.Error("an origin with userinfo was accepted")
	}
}
