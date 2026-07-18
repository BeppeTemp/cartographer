package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// newScopedTestHandler mounts two KBs ("kbx" and "kby") on a MultiKBServer and
// wraps it with ts.Middleware, reproducing the exact production wiring
// (store.Middleware(multi.Handler())) used by serveHTTP.
func newScopedTestHandler(t *testing.T, ts *auth.TokenStore) http.Handler {
	t.Helper()
	multi := NewMultiKBServer("test")
	for _, name := range []string{"kbx", "kby"} {
		k := setupTestKB(t)
		multi.MountKB(name, func(s *Server) {
			RegisterKBTools(s, k, Deps{})
		})
	}
	return ts.Middleware(multi.Handler())
}

// doMCP sends a JSON-RPC body to /mcp?kb=<kbName> with the given bearer token
// and returns the raw HTTP response recorder.
func doMCP(handler http.Handler, kbName, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp?kb="+kbName, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

const toolsListBody = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`

func writeToolCallBody(name string) string {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": map[string]any{}},
	})
	return string(b)
}

func TestHTTPGuard_ReadScope_BlocksWrite_AllowsRead(t *testing.T) {
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "r-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: false}}},
	})
	handler := newScopedTestHandler(t, ts)

	// concept_write is a write tool: must be forbidden with a read-only scope.
	rr := doMCP(handler, "kbx", "r-tok", writeToolCallBody("concept_write"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("write tool with r scope: status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}

	// atlas_overview is read-only: must be allowed.
	rr = doMCP(handler, "kbx", "r-tok", writeToolCallBody("atlas_overview"))
	if rr.Code != http.StatusOK {
		t.Fatalf("read tool with r scope: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHTTPGuard_RWScope_AllowsWriteAndRead(t *testing.T) {
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "rw-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: true}}},
	})
	handler := newScopedTestHandler(t, ts)

	rr := doMCP(handler, "kbx", "rw-tok", writeToolCallBody("concept_write"))
	if rr.Code != http.StatusOK {
		t.Fatalf("write tool with rw scope: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	rr = doMCP(handler, "kbx", "rw-tok", writeToolCallBody("atlas_overview"))
	if rr.Code != http.StatusOK {
		t.Fatalf("read tool with rw scope: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHTTPGuard_NoScopeToken_FullAccess(t *testing.T) {
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "admin-tok"}, // nil scopes = full access
	})
	handler := newScopedTestHandler(t, ts)

	for _, name := range []string{"concept_write", "atlas_overview"} {
		rr := doMCP(handler, "kbx", "admin-tok", writeToolCallBody(name))
		if rr.Code != http.StatusOK {
			t.Fatalf("no-scope token calling %s: status = %d, want 200; body=%s", name, rr.Code, rr.Body.String())
		}
	}
}

func TestHTTPGuard_CrossKB_Forbidden(t *testing.T) {
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "x-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: true}}},
	})
	handler := newScopedTestHandler(t, ts)

	// Token only has access to kbx; a request against kby must be forbidden
	// even for a read-only tool.
	rr := doMCP(handler, "kby", "x-tok", writeToolCallBody("atlas_overview"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-KB read: status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHTTPGuard_NonToolsCallMethod(t *testing.T) {
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "r-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: false}}},
	})
	handler := newScopedTestHandler(t, ts)

	// tools/list is not tools/call: treated as read, r-scope on kbx suffices.
	rr := doMCP(handler, "kbx", "r-tok", toolsListBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("tools/list on kbx with r scope: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	// Same method against a KB the token has no access to at all: forbidden.
	rr = doMCP(handler, "kby", "r-tok", toolsListBody)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("tools/list on kby with no access: status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHTTPGuard_BodyRestored verifies that after the guard peeks the request
// body it is fully restored, so the wrapped handler (which reads r.Body again
// from scratch) still sees the original JSON-RPC request and produces the
// expected tool result rather than a parse error / empty body.
func TestHTTPGuard_BodyRestored(t *testing.T) {
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "r-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: false}}},
	})
	handler := newScopedTestHandler(t, ts)

	rr := doMCP(handler, "kbx", "r-tok", writeToolCallBody("atlas_overview"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response body is not valid JSON-RPC (body not restored?): %v; body=%s", err, rr.Body.String())
	}
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error (body not restored?): %v", resp.Error)
	}
	tr := decodeToolResult(t, resp)
	if tr.IsError {
		t.Fatalf("atlas_overview returned isError=true: %v", tr.Content)
	}
	if len(tr.Content) == 0 || tr.Content[0].Text == "" {
		t.Fatal("atlas_overview returned empty content — body was likely not restored for the downstream handler")
	}
}

// writeServiceGetCallBody builds a tools/call body for service_get with the
// given resolve_secrets argument.
func writeServiceGetCallBody(resolveSecrets bool) string {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "service_get",
			"arguments": map[string]any{
				"service_id":      "svc-does-not-matter",
				"resolve_secrets": resolveSecrets,
			},
		},
	})
	return string(b)
}

// TestHTTPGuard_ServiceGet_ResolveSecrets_RequiresRW verifies the D47 special
// case in mcpAccessGuard: service_get is classified ReadOnly by tool name,
// but a call with resolve_secrets=true decrypts and returns secrets, so it
// must be rejected for a read-only scope and allowed for rw.
func TestHTTPGuard_ServiceGet_ResolveSecrets_RequiresRW(t *testing.T) {
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "r-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: false}}},
		{Token: "rw-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: true}}},
	})
	handler := newScopedTestHandler(t, ts)

	// resolve_secrets=false: read scope suffices (regular service_get read).
	rr := doMCP(handler, "kbx", "r-tok", writeServiceGetCallBody(false))
	if rr.Code != http.StatusOK {
		t.Fatalf("service_get resolve_secrets=false with r scope: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	// resolve_secrets=true: read scope must be forbidden.
	rr = doMCP(handler, "kbx", "r-tok", writeServiceGetCallBody(true))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("service_get resolve_secrets=true with r scope: status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}

	// resolve_secrets=true: rw scope must pass the guard (the tool handler
	// itself may still error, e.g. missing service/age key — that's a 200
	// with an isError tool result, not a 403 from the guard).
	rr = doMCP(handler, "kbx", "rw-tok", writeServiceGetCallBody(true))
	if rr.Code != http.StatusOK {
		t.Fatalf("service_get resolve_secrets=true with rw scope: status = %d, want 200 (guard should pass); body=%s", rr.Code, rr.Body.String())
	}
}

// TestReadOnlyToolsGolden verifies that the set of tools marked ReadOnly:true
// in the real registry (built via RegisterKBTools, including the BundleFS-gated
// sync tools) matches exactly the readOnlyToolNames source of truth consulted
// by ToolRequiresWrite. If someone adds a new tool without marking it, or
// mismarks an existing one, this test fails.
func TestReadOnlyToolsGolden(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	bundleFS := fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide KB creation\nversion: \"1.0\"\n---\nBody here.\n"),
		},
	}
	RegisterKBTools(s, k, Deps{BundleFS: bundleFS})

	got := map[string]bool{}
	for name, tool := range s.Tools() {
		if tool.ReadOnly {
			got[name] = true
		}
	}

	for name := range readOnlyToolNames {
		if !got[name] {
			t.Errorf("expected tool %q to be registered and marked ReadOnly:true, but it wasn't", name)
		}
	}
	for name := range got {
		if !readOnlyToolNames[name] {
			t.Errorf("tool %q is marked ReadOnly:true in the registry but missing from readOnlyToolNames", name)
		}
	}
}
