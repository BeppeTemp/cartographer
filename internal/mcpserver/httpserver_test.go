package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// newMultiKBTestHandler mounts the given KB names on a fresh MultiKBServer
// and returns its Handler() unwrapped (no auth middleware), for routing and
// readiness tests that don't care about scopes.
func newMultiKBTestHandler(t *testing.T, names ...string) *MultiKBServer {
	t.Helper()
	multi := NewMultiKBServer("test")
	for _, name := range names {
		k := setupTestKB(t)
		multi.MountKB(name, func(s *Server) {
			RegisterKBTools(s, k, Deps{})
		})
	}
	return multi
}

func TestMultiKB_PathRouting_KnownName(t *testing.T) {
	multi := newMultiKBTestHandler(t, "kbx", "kby")
	handler := multi.Handler()

	rr := doMCP(handler, "kby", "", toolsListBody) // sanity: ?kb= still works
	if rr.Code != http.StatusOK {
		t.Fatalf("?kb=kby: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	req := newMCPPost("/mcp/kbx", toolsListBody)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/mcp/kbx: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestMultiKB_PathRouting_UnknownName(t *testing.T) {
	multi := newMultiKBTestHandler(t, "kbx")
	handler := multi.Handler()

	req := newMCPPost("/mcp/does-not-exist", toolsListBody)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("/mcp/does-not-exist: status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestMultiKB_PathRouting_ConflictingKBSelection(t *testing.T) {
	multi := newMultiKBTestHandler(t, "kbx", "kby")
	handler := multi.Handler()

	req := newMCPPost("/mcp/kbx?kb=kby", toolsListBody)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("/mcp/kbx?kb=kby: status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestMultiKB_PathRouting_AgreeingKBSelection(t *testing.T) {
	multi := newMultiKBTestHandler(t, "kbx", "kby")
	handler := multi.Handler()

	req := newMCPPost("/mcp/kbx?kb=kbx", toolsListBody)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/mcp/kbx?kb=kbx: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// TestMultiKB_WellKnownProtectedResource_ServedWithoutAuth is the RFC 9728
// contract (D132, issue #122): docs/transport-auth.md promises the server
// publishes Protected Resource Metadata, and auth.go's isPublicPath already
// exempts this path from authentication — this asserts the production
// handler (MultiKBServer.Handler, the one cmd/cartographer/serve.go wraps in
// auth.TokenStore.Middleware) actually answers it, unauthenticated, with the
// expected JSON, instead of falling through to the "not found" default.
func TestMultiKB_WellKnownProtectedResource_ServedWithoutAuth(t *testing.T) {
	multi := newMultiKBTestHandler(t, "kbx")
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "some-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: false}}},
	})
	handler := ts.Middleware(multi.Handler())

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	req.Host = "cartographer.example.com"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req) // no Authorization header: must not be rejected

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["resource"] != "http://cartographer.example.com" {
		t.Errorf("resource = %v, want %q", body["resource"], "http://cartographer.example.com")
	}
	if _, ok := body["authorization_servers"]; !ok {
		t.Error("missing authorization_servers")
	}
	if _, ok := body["bearer_methods_supported"]; !ok {
		t.Error("missing bearer_methods_supported")
	}
}

func TestMultiKB_Ready_ZeroKBs(t *testing.T) {
	multi := NewMultiKBServer("test")
	handler := multi.Handler()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("/ready with 0 KBs: status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if ready, _ := body["ready"].(bool); ready {
		t.Fatalf("ready = %v, want false", body["ready"])
	}
	if kbs, _ := body["kbs"].(float64); kbs != 0 {
		t.Fatalf("kbs = %v, want 0", body["kbs"])
	}
}

func TestMultiKB_Ready_AtLeastOneKB(t *testing.T) {
	multi := newMultiKBTestHandler(t, "kbx")
	handler := multi.Handler()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/ready with 1 KB: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if ready, _ := body["ready"].(bool); !ready {
		t.Fatalf("ready = %v, want true", body["ready"])
	}
}

func TestMultiKB_Health_IncludesReady(t *testing.T) {
	// 0 KBs: status stays "ok" (liveness invariant), ready is false.
	multi := NewMultiKBServer("test")
	handler := multi.Handler()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/health: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %v, want \"ok\" (liveness must not break)", body["status"])
	}
	if ready, _ := body["ready"].(bool); ready {
		t.Fatalf("ready = %v, want false with 0 KBs", body["ready"])
	}

	// 1 KB: ready flips to true.
	multi = newMultiKBTestHandler(t, "kbx")
	handler = multi.Handler()
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %v, want \"ok\"", body["status"])
	}
	if ready, _ := body["ready"].(bool); !ready {
		t.Fatalf("ready = %v, want true with 1 KB", body["ready"])
	}
}

// TestMultiKB_Health_AdvertisesToolPrefix is D120: /health must expose each
// mounted KB's effective tool-name prefix so clients discover it instead of
// re-deriving config.ResolveToolPrefix themselves.
func TestMultiKB_Health_AdvertisesToolPrefix(t *testing.T) {
	multi := NewMultiKBServer("test")
	k1 := setupTestKB(t)
	if err := multi.MountKBWithPrefix("alpha", "", func(s *Server) { RegisterKBTools(s, k1, Deps{}) }); err != nil {
		t.Fatalf("MountKBWithPrefix(alpha): %v", err)
	}
	k2 := setupTestKB(t)
	if err := multi.MountKBWithPrefix("beta", "custom_name", func(s *Server) { RegisterKBTools(s, k2, Deps{}) }); err != nil {
		t.Fatalf("MountKBWithPrefix(beta): %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	multi.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/health: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		KBs []struct {
			Name       string `json:"name"`
			ToolPrefix string `json:"tool_prefix"`
		} `json:"kbs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v; body=%s", err, rr.Body.String())
	}
	byName := make(map[string]string, len(body.KBs))
	for _, kb := range body.KBs {
		byName[kb.Name] = kb.ToolPrefix
	}
	if got := byName["alpha"]; got != "" {
		t.Fatalf("alpha tool_prefix = %q, want empty (unprefixed)", got)
	}
	if got := byName["beta"]; got != "custom_name" {
		t.Fatalf("beta tool_prefix = %q, want \"custom_name\"", got)
	}

	// The raw body must carry exactly one "tool_prefix" occurrence (beta's):
	// the unprefixed KB (alpha) omits the field entirely (json:",omitempty"),
	// preserving the exact shape a pre-D120 client already tolerates.
	if n := strings.Count(rr.Body.String(), `"tool_prefix"`); n != 1 {
		t.Fatalf(`"tool_prefix" occurrences = %d, want 1 (only beta's); body=%s`, n, rr.Body.String())
	}
}

func TestServer_Health_IncludesReady(t *testing.T) {
	s := New("test")
	handler := s.HTTPHandler()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/health: status = %d, want 200", rr.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %v, want \"ok\"", body["status"])
	}
	if ready, _ := body["ready"].(bool); !ready {
		t.Fatalf("ready = %v, want true (single-KB server always ready)", body["ready"])
	}
}

func TestServer_Ready(t *testing.T) {
	s := New("test")
	handler := s.HTTPHandler()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/ready: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if ready, _ := body["ready"].(bool); !ready {
		t.Fatalf("ready = %v, want true", body["ready"])
	}
}

// TestClients_EmptyRoster verifies a fresh server returns {"clients": []} with
// HTTP 200, never 404 and never a null array.
func TestClients_EmptyRoster(t *testing.T) {
	s := New("test")
	handler := s.HTTPHandler()

	req := httptest.NewRequest(http.MethodGet, "/clients", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/clients: status = %d, want 200", rr.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	clients, ok := body["clients"]
	if !ok {
		t.Fatal("missing 'clients' key")
	}
	arr, ok := clients.([]interface{})
	if !ok {
		t.Fatalf("clients is not an array: %T", clients)
	}
	if len(arr) != 0 {
		t.Fatalf("clients length = %d, want 0", len(arr))
	}
}

// TestClients_MethodNotAllowed verifies POST (and other non-GET methods) return
// 405 on /clients for every handler constructor.
func TestClients_MethodNotAllowed(t *testing.T) {
	s := New("test")
	// Single-KB handler.
	for _, v := range []struct {
		h    http.Handler
		desc string
	}{
		{s.HTTPHandler(), "HTTPHandler"},
	} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			req := httptest.NewRequest(method, "/clients", nil)
			rr := httptest.NewRecorder()
			v.h.ServeHTTP(rr, req)
			if rr.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s: status = %d, want 405", v.desc, method, rr.Code)
			}
		}
	}
	// Multi-KB handler.
	multi := NewMultiKBServer("test")
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/clients", nil)
		rr := httptest.NewRecorder()
		multi.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("MultiKB %s: status = %d, want 405", method, rr.Code)
		}
	}
}

// TestClients_HandlerConstructors verifies /clients answers on both handler
// constructors (HTTPHandler, MultiKBServer.Handler).
func TestClients_HandlerConstructors(t *testing.T) {
	// Build a single-KB server and populate the roster by hitting /mcp.
	k := setupTestKB(t)
	s := New("test")
	s.SetPolicyKB("docs")
	RegisterKBTools(s, k, Deps{})

	// Simulate a few MCP requests so the roster is non-empty.
	mcpBody := writeToolCallBody("atlas_overview")
	for i := 0; i < 3; i++ {
		req := newMCPPost("/mcp", mcpBody)
		rr := httptest.NewRecorder()
		s.handleMCPPost(rr, req)
	}

	for _, v := range []struct {
		desc string
		h    http.Handler
	}{
		{"HTTPHandler", s.HTTPHandler()},
	} {
		req := httptest.NewRequest(http.MethodGet, "/clients", nil)
		rr := httptest.NewRecorder()
		v.h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200; body=%s", v.desc, rr.Code, rr.Body.String())
		}
		var body struct {
			Clients []struct {
				KB         string `json:"kb"`
				ClientName string `json:"client_name"`
			} `json:"clients"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: invalid JSON: %v", v.desc, err)
		}
		if len(body.Clients) == 0 {
			t.Fatalf("%s: expected at least 1 client row", v.desc)
		}
		if body.Clients[0].KB != "docs" {
			t.Fatalf("%s: kb = %q, want \"docs\"", v.desc, body.Clients[0].KB)
		}
	}

	// Multi-KB handler with two KBs.
	multi := NewMultiKBServer("test")
	k1 := setupTestKB(t)
	k2 := setupTestKB(t)
	if err := multi.MountKBWithPrefix("alpha", "", func(s *Server) { RegisterKBTools(s, k1, Deps{}) }); err != nil {
		t.Fatalf("MountKBWithPrefix(alpha): %v", err)
	}
	if err := multi.MountKBWithPrefix("beta", "", func(s *Server) { RegisterKBTools(s, k2, Deps{}) }); err != nil {
		t.Fatalf("MountKBWithPrefix(beta): %v", err)
	}
	// Hit /mcp?kb=alpha to populate its roster.
	for i := 0; i < 2; i++ {
		req := newMCPPost("/mcp?kb=alpha", mcpBody)
		rr := httptest.NewRecorder()
		multi.Handler().ServeHTTP(rr, req)
	}
	// Hit /mcp?kb=beta to populate its roster.
	for i := 0; i < 1; i++ {
		req := newMCPPost("/mcp?kb=beta", mcpBody)
		rr := httptest.NewRecorder()
		multi.Handler().ServeHTTP(rr, req)
	}

	req := httptest.NewRequest(http.MethodGet, "/clients", nil)
	rr := httptest.NewRecorder()
	multi.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("MultiKB /clients: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Clients []struct {
			KB string `json:"kb"`
		} `json:"clients"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// Both KBs must appear, sorted alphabetically: alpha before beta.
	if len(body.Clients) < 2 {
		t.Fatalf("expected >= 2 client rows, got %d; body=%s", len(body.Clients), rr.Body.String())
	}
	if body.Clients[0].KB != "alpha" {
		t.Fatalf("first row kb = %q, want \"alpha\" (sorted order)", body.Clients[0].KB)
	}
	if body.Clients[1].KB != "beta" {
		t.Fatalf("second row kb = %q, want \"beta\" (sorted order)", body.Clients[1].KB)
	}
}

// TestClients_NoOverflow omits overflow when zero.
func TestClients_NoOverflow(t *testing.T) {
	s := New("test")
	handler := s.HTTPHandler()
	req := httptest.NewRequest(http.MethodGet, "/clients", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	var raw map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := raw["overflow"]; ok {
		t.Fatalf("'overflow' present in response with 0 overflow")
	}
}
