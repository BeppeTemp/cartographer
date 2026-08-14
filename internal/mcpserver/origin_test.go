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

func doOrigin(t *testing.T, allowed []string, method, path, origin string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	var reached bool
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Host = "kb.example.com"
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rr := httptest.NewRecorder()
	OriginGuard(allowed, okHandler(&reached)).ServeHTTP(rr, req)
	return rr, reached
}

func TestOriginGuard_AllowListed(t *testing.T) {
	rr, reached := doOrigin(t, []string{"https://app.example.com"}, http.MethodPost, "/mcp", "https://app.example.com")
	if rr.Code != http.StatusOK || !reached {
		t.Fatalf("status = %d, reached = %v; want 200 and reached", rr.Code, reached)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the accepted origin echoed back", got)
	}
	if got := rr.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("Vary = %q, want it to name Origin", got)
	}
}

func TestOriginGuard_ForeignOriginRejected(t *testing.T) {
	rr, reached := doOrigin(t, []string{"https://app.example.com"}, http.MethodPost, "/mcp", "https://evil.example.net")
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

func TestOriginGuard_NoOriginPassesThrough(t *testing.T) {
	rr, reached := doOrigin(t, []string{"https://app.example.com"}, http.MethodPost, "/mcp", "")
	if rr.Code != http.StatusOK || !reached {
		t.Fatalf("status = %d, reached = %v; want a non-browser client served normally", rr.Code, reached)
	}
}

// The empty allow-list default accepts the request's own Host and nothing
// else, whatever scheme the origin names — a TLS-terminating proxy does not
// have to be configured with the scheme it terminated.
func TestOriginGuard_EmptyListIsSameOrigin(t *testing.T) {
	rr, _ := doOrigin(t, nil, http.MethodPost, "/mcp", "https://kb.example.com")
	if rr.Code != http.StatusOK {
		t.Fatalf("same-origin: status = %d, want 200", rr.Code)
	}
	rr, _ = doOrigin(t, nil, http.MethodPost, "/mcp", "https://other.example.com")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: status = %d, want 403", rr.Code)
	}
}

func TestOriginGuard_WildcardDisablesCheck(t *testing.T) {
	rr, reached := doOrigin(t, []string{"*"}, http.MethodPost, "/mcp", "https://anything.example.net")
	if rr.Code != http.StatusOK || !reached {
		t.Fatalf("status = %d, reached = %v; want the check disabled by \"*\"", rr.Code, reached)
	}
}

func TestOriginGuard_PreflightChecked(t *testing.T) {
	rr, reached := doOrigin(t, []string{"https://app.example.com"}, http.MethodOptions, "/mcp", "https://evil.example.net")
	if rr.Code != http.StatusForbidden || reached {
		t.Fatalf("status = %d, reached = %v; want the preflight refused too", rr.Code, reached)
	}
}

// Only the MCP endpoint is guarded: /health is what a liveness probe or an
// operator dashboard hits, and it exposes no KB content.
func TestOriginGuard_NonMCPPathUnguarded(t *testing.T) {
	rr, reached := doOrigin(t, []string{"https://app.example.com"}, http.MethodGet, "/health", "https://evil.example.net")
	if rr.Code != http.StatusOK || !reached {
		t.Fatalf("status = %d, reached = %v; want /health served regardless of origin", rr.Code, reached)
	}
}

func TestOriginGuard_PerKBPathGuarded(t *testing.T) {
	rr, _ := doOrigin(t, []string{"https://app.example.com"}, http.MethodPost, "/mcp/kbx", "https://evil.example.net")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 on /mcp/<kb> as well", rr.Code)
	}
}

func TestOriginGuard_TrailingSlashAndCaseIgnored(t *testing.T) {
	rr, _ := doOrigin(t, []string{"https://App.Example.com/"}, http.MethodPost, "/mcp", "https://app.example.com")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want the origin matched case-insensitively without its trailing slash", rr.Code)
	}
}
